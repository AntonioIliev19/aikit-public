package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AntonioIliev19/aikit-public/internal/config"
	"github.com/AntonioIliev19/aikit-public/internal/index"
	"github.com/AntonioIliev19/aikit-public/internal/parser"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("AIKIT_MCP_TEST_SERVER") == "1" {
		if err := Serve(context.Background(), os.Getenv("AIKIT_MCP_TEST_PROJECT"), "test", &sdk.StdioTransport{MaxLineLength: 16 << 10}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture(t *testing.T, enabled bool) (*index.Core, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Indexing.Enabled = enabled
	if _, _, err := config.Init(dir, cfg); err != nil {
		t.Fatal(err)
	}
	core, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return core, dir
}
func client(t *testing.T, ctx context.Context, dir string) *sdk.ClientSession {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "AIKIT_MCP_TEST_SERVER=1", "AIKIT_MCP_TEST_PROJECT="+dir)
	c := sdk.NewClient(&sdk.Implementation{Name: "independent-sdk-client", Version: "test"}, nil)
	session, err := c.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
func call(ctx context.Context, session *sdk.ClientSession, name string, args any) (response, error) {
	r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return response{}, err
	}
	if len(r.Content) != 1 {
		return response{}, errors.New("unexpected content")
	}
	text, ok := r.Content[0].(*sdk.TextContent)
	if !ok {
		return response{}, errors.New("unexpected content type")
	}
	if len(text.Text) > MaxOutputBytes {
		return response{}, errors.New("unbounded content")
	}
	var value response
	if json.Unmarshal([]byte(text.Text), &value) != nil || r.IsError != (value.Error != "") {
		return response{}, errors.New("invalid structured result")
	}
	return value, nil
}

func TestMissingDisabledAndAdversarialArguments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, enabled := range []bool{true, false} {
		_, dir := fixture(t, enabled)
		session := client(t, ctx, dir)
		tools, err := session.ListTools(ctx, nil)
		if err != nil || len(tools.Tools) != 8 {
			t.Fatal("unexpected advertised tools", err)
		}
		for _, tool := range tools.Tools {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("missing read-only annotation")
			}
		}
		value, err := call(ctx, session, "aikit_search_codebase", map[string]any{"query": "hello"})
		want := "INDEX_DISABLED"
		if enabled {
			want = "INDEX_ABSENT"
		}
		if err != nil || value.Error != want {
			t.Fatal("unexpected missing index result", value, err)
		}
		for _, raw := range []string{`null`, `[]`, `{"query":"hello","root":"/elsewhere"}`, `{"query":"hello","scope":"../outside"}`, `{"query":"hello","scope":"x:stream"}`, `{"query":"hello","limit":0}`, `{"query":"hello","limit":null}`, `{"query":"hello","scope":null}`, `{"query":"hello","limit":51}`, `{"query":"hello","limit":1.5}`, `{"query":"hello","query":"other"}`, `{"query":42}`, `{}`} {
			v, err := call(ctx, session, "aikit_search_codebase", json.RawMessage(raw))
			if err != nil || v.Error != "INVALID_ARGUMENTS" {
				t.Fatalf("adversarial args accepted: %s (%s) %v", raw, v.Error, err)
			}
		}
		for _, raw := range []string{`null`, `{"root":"elsewhere"}`, `{"scope":"src"}`} {
			v, err := call(ctx, session, "aikit_index_status", json.RawMessage(raw))
			if err != nil || v.Error != "INVALID_ARGUMENTS" {
				t.Fatal("status accepted arguments", v, err)
			}
		}
		status, err := call(ctx, session, "aikit_index_status", map[string]any{})
		if err != nil || status.Status == nil || status.Status.Present || status.Status.Enabled != enabled {
			t.Fatal("bad status", status, err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".aikit", "state")); !os.IsNotExist(err) {
			t.Fatal("read-only server created absent state")
		}
	}
}

func TestTwoIndependentStdioReadersSharedIndexWithWriter(t *testing.T) {
	if !parser.Available() {
		t.Skip("native CGO parser required for indexed subprocess evidence")
	}
	core, dir := fixture(t, true)
	source := filepath.Join(dir, "source.ts")
	if err := os.WriteFile(source, []byte("export function sharedEvidence() { return 'shared'; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := core.Index(ctx, nil); err != nil {
		t.Fatal(err)
	}
	first, second := client(t, ctx, dir), client(t, ctx, dir)
	for _, name := range []string{"aikit_search_codebase", "aikit_find_symbol"} {
		a, err := call(ctx, first, name, map[string]any{"query": "sharedEvidence"})
		if err != nil || len(a.Evidence) != 1 || a.Error != "" {
			t.Fatal(a, err)
		}
		b, err := call(ctx, second, name, map[string]any{"query": "sharedEvidence"})
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatal("independent process evidence differs", err)
		}
	}
	// The writer changes an unrelated source while both subprocesses keep their
	// own database connections. Unchanged evidence remains identical across commits.
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for _, session := range []*sdk.ClientSession{first, second} {
		wg.Add(1)
		go func(s *sdk.ClientSession) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				v, err := call(ctx, s, "aikit_search_codebase", map[string]any{"query": "sharedEvidence"})
				if err != nil {
					errs <- err
					return
				}
				if v.Error != "" || len(v.Evidence) != 1 || v.Evidence[0].Path != "source.ts" {
					errs <- errors.New("reader/writer evidence changed")
					return
				}
			}
		}(session)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if err := os.WriteFile(filepath.Join(dir, "other.ts"), []byte("export const unrelated = 1;\n"+strings.Repeat("\n", i)), 0600); err != nil {
				errs <- err
				return
			}
			if _, err := core.Index(ctx, nil); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := os.WriteFile(source, []byte("export function changedSource() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := call(ctx, first, "aikit_find_symbol", map[string]any{"query": "sharedEvidence"})
	if err != nil || v.Error != "INDEX_STALE" {
		t.Fatal("stale indexed evidence exposed", v, err)
	}
	if _, err := core.Index(ctx, nil); err != nil {
		t.Fatal(err)
	}
	v, err = call(ctx, second, "aikit_find_symbol", map[string]any{"query": "changedSource"})
	if err != nil || v.Error != "" || len(v.Evidence) != 1 {
		t.Fatal("second reader missed writer commit", v, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".aikitignore"), []byte("source.ts\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err = call(ctx, first, "aikit_find_symbol", map[string]any{"query": "changedSource"})
	if err != nil || v.Error != "INDEX_STALE" || len(v.Evidence) != 0 {
		t.Fatal("newly excluded evidence exposed", v, err)
	}
}

func TestOutputBoundAndErrorSanitization(t *testing.T) {
	if !parser.Available() {
		t.Skip("native parser required")
	}
	core, dir := fixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 12; i++ {
		name := filepath.Join(dir, string(rune('a'+i))+".ts")
		body := "export const bounded = '" + strings.Repeat("word ", 900) + "';\n"
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := core.Index(ctx, nil); err != nil {
		t.Fatal(err)
	}
	v, err := call(ctx, client(t, ctx, dir), "aikit_search_codebase", map[string]any{"query": "bounded", "limit": 50})
	if err != nil || !v.Truncated || len(v.Evidence) == 0 {
		t.Fatal("output was not bounded", v.Truncated, err)
	}
	if code := errorCode(errors.New("password=longSyntheticValue123456")); code != "INDEX_UNAVAILABLE" {
		t.Fatal("raw diagnostic exposed")
	}
}
