package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestColorDetection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tty         bool
		term        string
		noColorEnv  bool
		noColorFlag bool
		want        terminal
	}{
		{"tty", true, "xterm-256color", false, false, terminal{live: true, color: true}},
		{"NO_COLOR", true, "xterm", true, false, terminal{live: true}},
		{"--no-color keeps live progress", true, "xterm", false, true, terminal{live: true}},
		{"TERM=dumb", true, "dumb", false, false, terminal{}},
		{"not a tty", false, "xterm", false, false, terminal{}},
	} {
		if got := streamPolicy(tc.tty, tc.term, tc.noColorEnv, tc.noColorFlag); got != tc.want {
			t.Fatalf("%s: got %+v want %+v", tc.name, got, tc.want)
		}
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	t.Setenv("TERM", "xterm")
	for _, w := range []any{write, &bytes.Buffer{}} {
		if terminalForOptions(w.(interface{ Write([]byte) (int, error) }), false) != (terminal{}) {
			t.Fatal("redirected stream treated as a colour terminal")
		}
	}
}

func TestGlyphFallback(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"windows", map[string]string{"LANG": "C"}, true},
		{"linux", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"linux", map[string]string{"LANG": "C"}, false},
		{"linux", map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, false},
		{"darwin", map[string]string{"LC_CTYPE": "UTF-8"}, true},
		{"linux", map[string]string{}, true},
	} {
		if got := localeUTF8(tc.goos, env(tc.env)); got != tc.want {
			t.Fatalf("%s %v: got %t", tc.goos, tc.env, got)
		}
	}
	if glyphsFor(terminal{live: true}) != asciiGlyphs || glyphsFor(terminal{}) != asciiGlyphs {
		t.Fatal("colour-off output must use ASCII glyphs")
	}
	if runtime.GOOS == "windows" && glyphsFor(terminal{live: true, color: true}) != unicodeGlyphs {
		t.Fatal("colour terminal should use Unicode glyphs")
	}
}

func TestTypoSuggestion(t *testing.T) {
	for word, want := range map[string]string{
		"serch": "search", "sycn": "sync", "stauts": "status", "doctr": "doctor", "Search": "search",
		"indx": "index", "skils": "skill", "sensitive-input": "", "x": "", "--hlep": "", "search": "", "zzzzzz": "",
	} {
		if got := suggestCommand(word); got != want {
			t.Fatalf("suggestCommand(%q) = %q, want %q", word, got, want)
		}
	}
	for _, name := range commandNames {
		if !strings.Contains(help, "\n  "+name+" ") && !strings.Contains(help, "|"+name+" ") && !strings.Contains(help, "\n  "+name+"|") {
			t.Fatalf("suggestion list names %q, which the command reference does not document", name)
		}
	}
	for answer, want := range map[string]bool{"y\n": true, "Y\r\n": true, "yes\n": true, "\n": false, "n\n": false, "": false, "sure\n": false} {
		var out bytes.Buffer
		if got := offerSuggestion(strings.NewReader(answer), &out, terminal{}, "search", true, true); got != want {
			t.Fatalf("answer %q: got %t", answer, got)
		}
		if !strings.Contains(out.String(), "Did you mean conmesh search with the same arguments? [y/N]") {
			t.Fatal(out.String())
		}
	}
	var out bytes.Buffer
	if offerSuggestion(strings.NewReader("y\n"), &out, terminal{}, "search", false, false) {
		t.Fatal("non-interactive suggestion must never run")
	}
	if strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "Did you mean conmesh search?") || !strings.Contains(out.String(), "conmesh --help") {
		t.Fatal(out.String())
	}
	// Through Run with redirected streams: suggest, do not run, exit 2, never echo input.
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"serch", "private-query"}, &stdout, &stderr, "test"); code != 2 || stdout.Len() != 0 {
		t.Fatalf("code %d stdout %q", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "Did you mean conmesh search with the same arguments?") || strings.Contains(stderr.String(), "serch") || strings.Contains(stderr.String(), "private-query") {
		t.Fatal(stderr.String())
	}
}

func TestGlobalFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--no-color", "--version", "--no-input"}, &stdout, &stderr, "v1"); code != 0 || stdout.String() != "conmesh v1\n" {
		t.Fatal(code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"--no-input"}, &stdout, &stderr, "v1"); code != 0 || stdout.String() != help {
		t.Fatal("--no-input without a command should print help")
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--no-input", "menu"}, &stdout, &stderr, "v1"); code != 2 || !strings.Contains(stderr.String(), "--no-input") {
		t.Fatal(code, stderr.String())
	}
	if code := Run([]string{"menu"}, &stdout, &stderr, "v1"); code != 2 {
		t.Fatal("non-interactive menu exit code changed")
	}
}

func TestJSONOutput(t *testing.T) {
	dir, global, source := physicalTemp(t), filepath.Join(physicalTemp(t), "global"), cliSource(t)
	check := func(args []string) []byte {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut, "test", dir, global); code != 0 {
			t.Fatalf("%v: exit %d %s", args, code, errOut.String())
		}
		if !json.Valid(out.Bytes()) {
			t.Fatalf("%v: invalid JSON %q", args, out.String())
		}
		return out.Bytes()
	}
	var d doctorReport
	if err := json.Unmarshal(check([]string{"doctor", "--json"}), &d); err != nil || d.Version != "test" || d.GoVersion == "" {
		t.Fatal("doctor JSON", err, d)
	}
	if run([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}, "test", dir, global) != 0 {
		t.Fatal("init")
	}
	if a, b := check([]string{"status"}), check([]string{"status", "--json"}); !bytes.Equal(a, b) {
		t.Fatal("status --json differs from status")
	}
	if got := strings.TrimSpace(string(check([]string{"skill", "list", "--json"}))); got != "[]" {
		t.Fatal("empty list should be []", got)
	}
	if run([]string{"skill", "add", source, "--trust"}, &bytes.Buffer{}, &bytes.Buffer{}, "test", dir, global) != 0 {
		t.Fatal("add")
	}
	var items []map[string]any
	if err := json.Unmarshal(check([]string{"skill", "list", "--json", "--project"}), &items); err != nil || len(items) != 1 || items[0]["id"] != "example" || items[0]["effective"] != true {
		t.Fatal("list JSON", err, items)
	}
	if err := json.Unmarshal(check([]string{"skill", "list", "--global", "--json"}), &items); err != nil || len(items) != 0 {
		t.Fatal("scoped list JSON", items)
	}
	for _, args := range [][]string{{"doctor", "--json", "extra"}, {"status", "--json", "--json"}, {"skill", "add", source, "--json"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}, "test", dir, global); code != 2 {
			t.Fatalf("%v accepted: %d", args, code)
		}
	}
}

func TestStepReportsOnlyOnTerminal(t *testing.T) {
	dir, global, source := physicalTemp(t), filepath.Join(physicalTemp(t), "global"), cliSource(t)
	live := terminal{live: true}
	step := func(args []string, outTerminal terminal) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := runTerminal(context.Background(), args, &out, &errOut, "test", dir, global, outTerminal, terminal{}); code != 0 {
			t.Fatalf("%v: exit %d %s", args, code, errOut.String())
		}
		if strings.Contains(out.String(), "\x1b") {
			t.Fatal("colour-off report contains escapes")
		}
		return squeeze(out.String())
	}
	if got := step([]string{"init", "--harness", "codex"}, live); !strings.Contains(got, "+ Initialized "+filepath.Base(dir)+" indexing off, harness codex") ||
		!strings.Contains(got, "Changed + .conmesh/config.yaml") || !strings.Contains(got, "conmesh sync --dry-run preview harness integration") || strings.Contains(got, "conmesh index") {
		t.Fatal(got)
	}
	if got := step([]string{"init"}, live); !strings.Contains(got, "Already initialized") || !strings.Contains(got, "Changed nothing") {
		t.Fatal(got)
	}
	if got := step([]string{"skill", "add", source, "--trust"}, live); !strings.Contains(got, "+ Installed skill example version 1, project scope") ||
		!strings.Contains(got, "Changed + skill example 1 (project)") || !strings.Contains(got, "Next\nconmesh sync --dry-run preview what will change in this project\nconmesh sync --trust apply the changes") {
		t.Fatal(got)
	}
	if got := step([]string{"package", "update", "example", "--type", "skill", "--trust"}, live); !strings.Contains(got, "Unchanged skill example") || !strings.Contains(got, "Changed nothing") || strings.Contains(got, "Next") {
		t.Fatal(got)
	}
	if got := step([]string{"skill", "remove", "example"}, live); !strings.Contains(got, "+ Removed skill example project scope") || !strings.Contains(got, "Changed - skill example (project)") {
		t.Fatal(got)
	}
	if got := step([]string{"skill", "remove", "example"}, live); !strings.Contains(got, "Already absent") || strings.Contains(got, "Next") {
		t.Fatal(got)
	}
	// Redirected output keeps the original text byte for byte.
	if got := step([]string{"skill", "add", source, "--trust"}, terminal{}); got != squeeze("Installed skill example 1 in project scope; omitted files/directories: 0\n") {
		t.Fatal(got)
	}
	if got := step([]string{"skill", "list"}, live); strings.Contains(got, "Changed") {
		t.Fatal("read-only command rendered a change report")
	}
}
