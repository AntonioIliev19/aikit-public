package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AntonioIliev19/aikit-public/internal/config"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("AIKIT_CLI_TEST_STDIO") == "1" {
		os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr, "test"))
	}
	os.Exit(m.Run())
}

func TestCLIMCPStdioAndSyncCommands(t *testing.T) {
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Harnesses = []string{"codex", "claude"}
	if _, _, err := config.Init(project, cfg); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(project, "global")
	var out, diag bytes.Buffer
	if code := run([]string{"sync", "--dry-run"}, &out, &diag, "test", project, global); code != 0 || !strings.Contains(out.String(), ".codex/config.toml") {
		t.Fatal("sync planning CLI failed", code, diag.String())
	}
	out.Reset()
	diag.Reset()
	if code := run([]string{"sync"}, &out, &diag, "test", project, global); code != 1 || !strings.Contains(diag.String(), "HARNESS_TRUST_REQUIRED") {
		t.Fatal("sync CLI did not require trust")
	}
	out.Reset()
	diag.Reset()
	if code := run([]string{"sync", "--trust"}, &out, &diag, "test", project, global); code != 0 {
		t.Fatal("sync apply CLI failed", code, diag.String())
	}
	for _, args := range [][]string{{"mcp"}, {"mcp", "serve", "--root", "sensitive-input"}, {"mcp", "serve", "extra"}, {"sync", "--trust", "--dry-run"}, {"sync", "--global"}} {
		out.Reset()
		diag.Reset()
		if code := run(args, &out, &diag, "test", project, global); code != 2 || out.Len() != 0 || strings.Contains(diag.String(), "sensitive-input") {
			t.Fatalf("invalid CLI accepted: %v", args)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "mcp", "serve", "--project", project)
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "AIKIT_CLI_TEST_STDIO=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "CLI-integration-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 8 {
		t.Fatal("CLI did not expose tools", err)
	}
	response, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "aikit_index_status", Arguments: map[string]any{}})
	if err != nil || response.IsError {
		t.Fatal("CLI status failed", err)
	}
	var content map[string]any
	if json.Unmarshal([]byte(response.Content[0].(*sdk.TextContent).Text), &content) != nil || content["index"].(map[string]any)["enabled"] != false {
		t.Fatal("wrong CLI status")
	}
	session.Close()
	if stderr.Len() != 0 {
		t.Fatal("unexpected CLI protocol diagnostics")
	}
	if _, err := os.Stat(filepath.Join(project, ".aikit/state/index.sqlite")); !os.IsNotExist(err) {
		t.Fatal("CLI MCP created database")
	}
}
