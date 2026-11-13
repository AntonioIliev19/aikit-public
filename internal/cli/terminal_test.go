package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/index"
	"github.com/AntonioIliev19/conmesh/internal/parser"
)

func TestTerminalPolicyAndRedirectedBytes(t *testing.T) {
	for _, tc := range []struct {
		tty     bool
		term    string
		noColor bool
		want    terminal
	}{
		{true, "xterm", false, terminal{true, true}},
		{true, "xterm", true, terminal{true, false}},
		{true, "dumb", false, terminal{}},
		{false, "xterm", false, terminal{}},
	} {
		if got := terminalPolicy(tc.tty, tc.term, tc.noColor); got != tc.want {
			t.Fatal("terminal policy", got, tc.want)
		}
	}
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	if terminalFor(write) != (terminal{}) || terminalFor(&bytes.Buffer{}) != (terminal{}) {
		t.Fatal("pipe treated as terminal")
	}
	jsonText := "{\n  \"enabled\": true,\n  \"source\": \"private : true\"\n}\n"
	for _, plain := range []string{jsonText, help, doctor("test")} {
		if got := (terminal{}).output(plain); got != plain {
			t.Fatal("redirected bytes changed")
		}
		if got := (terminal{true, false}).output(plain); got != plain {
			t.Fatal("NO_COLOR bytes changed")
		}
		if got := (terminal{true, true}).output(plain); !strings.Contains(got, "\x1b[") {
			t.Fatal("missing terminal color")
		} else if stripped := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(got, ""); stripped != plain {
			t.Fatal("styling changed visible output")
		}
	}
	if got := (terminal{true, true}).output(researchSkill()); got != researchSkill() {
		t.Fatal("research workflow text changed")
	}
	states := (terminal{true, true}).output("Project initialized. Indexing enabled: true; semantic indexing enabled: false\n")
	if !strings.Contains(states, "\x1b[32mtrue") || !strings.Contains(states, "\x1b[33mfalse") {
		t.Fatal("mixed state colors missing")
	}
}

type completionFailureWriter struct{ buffer bytes.Buffer }

func (w *completionFailureWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("complete")) {
		return 0, errors.New("completion output failed")
	}
	return w.buffer.Write(data)
}

func TestCompletionOutputFailureKeepsCommittedState(t *testing.T) {
	if !parser.Available() {
		t.Skip("native parser unavailable")
	}
	dir := physicalTemp(t)
	var stdout, stderr bytes.Buffer
	if run([]string{"init", "--index"}, &stdout, &stderr, "test", dir, "") != 0 {
		t.Fatal(stderr.String())
	}
	stdout.Reset()
	var failed completionFailureWriter
	if runTerminal(context.Background(), []string{"index"}, &stdout, &failed, "test", dir, "", terminal{}, terminal{true, false}) != 1 || stdout.Len() != 0 {
		t.Fatal("completed output failure ignored")
	}
	core, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	status, err := core.Status(context.Background())
	if err != nil || status.IndexedAt == "" {
		t.Fatal("successful commit lost after output failure", err)
	}
	if !strings.Contains(failed.buffer.String(), "could not write progress") || strings.Contains(failed.buffer.String(), "changes not committed") {
		t.Fatal("output failure misrepresented commit", failed.buffer.String())
	}
}

func TestProgressPercentageAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		p    index.Progress
		want int
	}{
		{index.Progress{Phase: "processing", Scanned: 10, Processed: 5, Skipped: 100}, 50},
		{index.Progress{Phase: "processing", Scanned: 10, Processed: 10}, 99},
		{index.Progress{Phase: "finalizing", Scanned: 10, Processed: 10}, 99},
		{index.Progress{Phase: "failed", Scanned: 10, Processed: 10}, 99},
		{index.Progress{Phase: "processing"}, 0},
		{index.Progress{Phase: "done"}, 100},
	} {
		if got := progressPercent(tc.p); got != tc.want {
			t.Fatal("false percentage", got, tc.want)
		}
	}
	var out bytes.Buffer
	p := indexProgress{writer: &out, terminal: terminal{true, false}, cancel: func() {}}
	for _, phase := range []string{"scanning", "processing", "finalizing", "failed"} {
		p.update(index.Progress{Phase: phase, Scanned: 2, Processed: 2})
	}
	if strings.Contains(out.String(), "100%") || !strings.Contains(out.String(), "total unknown") || !strings.HasSuffix(out.String(), "\n") || strings.Contains(out.String(), "\x1b") {
		t.Fatal("dishonest/unclean progress", out.String())
	}
	out.Reset()
	p.update(index.Progress{Phase: "done", Scanned: 2, Processed: 2, Malformed: 1})
	if !strings.Contains(out.String(), "100%") || !strings.Contains(out.String(), "Warning:") || !strings.Contains(out.String(), "elapsed") {
		t.Fatal("missing completed summary", out.String())
	}
	cancelled := false
	p = indexProgress{writer: brokenWriter{}, cancel: func() { cancelled = true }}
	p.update(index.Progress{Phase: "scanning"})
	if p.err == nil || !cancelled {
		t.Fatal("stream failure did not cancel")
	}
}

func TestTerminalIndexFlowAndStreamFailure(t *testing.T) {
	if !parser.Available() {
		t.Skip("native parser unavailable")
	}
	dir := physicalTemp(t)
	var stdout, stderr bytes.Buffer
	if run([]string{"init", "--index"}, &stdout, &stderr, "test", dir, "") != 0 {
		t.Fatal(stderr.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "code.ts"), []byte("export const Code = 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code := runTerminal(context.Background(), []string{"index"}, &stdout, &stderr, "test", dir, "", terminal{}, terminal{true, true})
	if code != 0 || !json.Valid(stdout.Bytes()) || !strings.Contains(stderr.String(), "100%") {
		t.Fatal("terminal index output", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if runTerminal(context.Background(), []string{"index", "--semantic"}, &stdout, &stderr, "test", dir, "", terminal{}, terminal{true, true}) != 1 || strings.Contains(stderr.String(), "%") {
		t.Fatal("semantic command fabricated basic progress")
	}
	stdout.Reset()
	if runTerminal(context.Background(), []string{"index"}, &stdout, brokenWriter{}, "test", dir, "", terminal{}, terminal{true, false}) != 1 || stdout.Len() != 0 {
		t.Fatal("progress stream failure ignored")
	}
	stdout.Reset()
	stderr.Reset()
	if run([]string{"mcp", "invalid"}, &stdout, &stderr, "test", dir, "") != 2 || strings.Contains(stderr.String(), "\x1b") || stdout.Len() != 0 {
		t.Fatal("MCP diagnostics changed")
	}
	stdout.Reset()
	stderr.Reset()
	if run([]string{"status"}, &stdout, &stderr, "test", dir, "") != 0 {
		t.Fatal(stderr.String())
	}
	want, err := execute(context.Background(), []string{"status"}, "test", dir, "")
	if err != nil || stdout.String() != want || stderr.Len() != 0 {
		t.Fatal("redirected JSON bytes changed")
	}
}
