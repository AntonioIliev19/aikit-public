package spike

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

func TestFTS5Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE VIRTUAL TABLE docs USING fts5(content); INSERT INTO docs VALUES ('aikit bootstrap');"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow("SELECT count(*) FROM docs WHERE docs MATCH 'bootstrap'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
}

// TestMain uses this test binary as the stdio server, avoiding external tools.
func TestMain(m *testing.M) {
	if os.Getenv("AIKIT_COMPAT_STDIO_SERVER") == "1" {
		server := mcp.NewServer(&mcp.Implementation{Name: "phase0", Version: "dev"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "status", Description: "Read-only compatibility status"},
			func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ready"}}}, nil, nil
			})
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestMCPStdioRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "AIKIT_COMPAT_STDIO_SERVER=1")
	client := mcp.NewClient(&mcp.Implementation{Name: "phase0-client", Version: "dev"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "status" {
		t.Fatal("expected exactly the status tool")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatal("unexpected status result")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "ready" {
		t.Fatal("unexpected status content")
	}
}
