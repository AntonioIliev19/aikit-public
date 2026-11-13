package cli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestMenu(t *testing.T) {
	var out bytes.Buffer
	var calls [][]string
	down := "\x1b[B"
	input := strings.NewReader("\r\rhttps://example.test/agent\n\rv1.0.0\n\x7f" +
		down + "\r" + down + "\rlocal-skill\n" + down + "\r" + down + "\r" +
		strings.Repeat(down, 5) + "\r\x7f")
	if err := runMenu(input, &out, terminal{}, "dev", func(args []string) {
		calls = append(calls, args)
	}, menuUpdates{}, false, nil); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"skill", "remove", "local-skill", "--global"}, {"doctor"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %v, want %v", calls, want)
	}
	if strings.Contains(menuBanner(terminal{}, "dev"), "\x1b") ||
		!strings.Contains(out.String(), "ConMesh dev") ||
		!strings.Contains(out.String(), "●─┼─●") {
		t.Fatal("menu banner is missing the plain version label or mesh mark")
	}
}

func TestMenuEOFDoesNotInstall(t *testing.T) {
	var out bytes.Buffer
	input := strings.NewReader("\r\x1b[B\rlocal-skill\n\r")
	if err := runMenu(input, &out, terminal{}, "dev", func(args []string) {
		t.Fatalf("unexpected command at EOF: %v", args)
	}, menuUpdates{}, false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMenuUpdateRequiresConfirmation(t *testing.T) {
	for _, answer := range []string{"y", "n", ""} {
		t.Run("answer="+answer, func(t *testing.T) {
			var out bytes.Buffer
			var calls [][]string
			checks := 0
			updates := menuUpdates{latest: func(context.Context) (string, error) {
				checks++
				return "v1.1.0", nil
			}}
			keys := "\x1b[A\x1b[A\r"
			switch answer {
			case "y":
				keys += "\x1b[B\r"
			case "n":
				keys += "\x7f"
			}
			input := strings.NewReader(keys)
			if err := runMenu(input, &out, terminal{}, "v1.0.0", func(args []string) {
				calls = append(calls, args)
			}, updates, false, nil); err != nil {
				t.Fatal(err)
			}
			if checks != 1 || !strings.Contains(out.String(), "Update available: v1.1.0") {
				t.Fatalf("checks = %d, output = %q", checks, out.String())
			}
			if answer == "y" {
				if !reflect.DeepEqual(calls, [][]string{{"update", "--version", "v1.1.0"}}) {
					t.Fatalf("commands = %v", calls)
				}
			} else if len(calls) != 0 {
				t.Fatalf("updated without confirmation: %v", calls)
			}
		})
	}
}

func TestMenuHelpReturnsToShell(t *testing.T) {
	var out bytes.Buffer
	var calls [][]string
	keys := strings.Repeat("\x1b[B", 6) + "\r\r"
	err := runMenu(strings.NewReader(keys), &out, terminal{}, "v1.0.0", func(args []string) {
		calls = append(calls, args)
		_, _ = out.WriteString(help)
	}, menuUpdates{}, false, nil)
	if err != nil || !reflect.DeepEqual(calls, [][]string{{"--help"}}) {
		t.Fatalf("calls = %v, err = %v", calls, err)
	}
	screen := out.String()
	options, commands := strings.Index(screen, "\nOptions:\n"), strings.Index(screen, "\nCommands:\n")
	if options < 0 || commands <= options || strings.Contains(screen[commands:], "↑↓ navigate") {
		t.Fatal("expected options and commands followed by a return to the shell")
	}
}
