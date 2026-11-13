package cli

import (
	"bytes"
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/packages"
)

const down, up = "\x1b[B", "\x1b[A"

// menuIndex returns the position of a main-menu item so tests do not depend on order.
func menuIndex(t *testing.T, id string) int {
	t.Helper()
	for i, o := range menuOptions {
		if o.id == id {
			return i
		}
	}
	t.Fatalf("menu item %q missing", id)
	return -1
}

func moveTo(t *testing.T, id string) string {
	return strings.Repeat(down, menuIndex(t, id)) + "\r"
}

func TestMenu(t *testing.T) {
	var out bytes.Buffer
	var calls [][]string
	input := strings.NewReader(moveTo(t, "install") + "\rhttps://example.test/agent\n\rv1.0.0\n\x7f" +
		moveTo(t, "uninstall") + down + "\rlocal-skill\n" + down + "\r" + down + "\r" +
		moveTo(t, "doctor") + "\x7f")
	if err := runMenu(input, &out, terminal{}, "dev", func(args []string) {
		calls = append(calls, args)
	}, menuUpdates{}, false, nil, menuInfo{workspace: workspaceStatus{state: "none"}}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"skill", "remove", "local-skill", "--global"}, {"doctor"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %v, want %v", calls, want)
	}
	screen := out.String()
	for _, text := range []string{"* ConMesh dev   context layer for AI agents", "o no workspace", "conmesh init",
		"Packages  agents and skills", "\nProject\n", "\nConMesh\n", "same as  conmesh skill add <source> --trust  or  conmesh agent add <source> --trust",
		"^v navigate   enter select   esc back   q quit"} {
		if !strings.Contains(screen, text) {
			t.Fatalf("menu screen missing %q:\n%s", text, screen)
		}
	}
	if regexp.MustCompile(`\x1b\[[0-9;]*m`).MatchString(screen) || strings.Contains(screen, "/ filter") {
		t.Fatal("colourless menu painted text or advertised an unimplemented filter")
	}
}

// Every rendered line must fit the terminal, or the redraw line count is wrong.
func TestMenuFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{6, 12, 17, 40, 80, 120} {
		for selected := range menuOptions {
			for _, line := range strings.Split(renderPicker(terminal{}, width, menuOptions, selected), "\n") {
				if cells(line) > width-1 {
					t.Fatalf("width %d: line of %d cells: %q", width, cells(line), line)
				}
			}
		}
	}
	at80 := renderPicker(terminal{}, 80, menuOptions, 0)
	if !strings.Contains(at80, "same as  conmesh skill add <source> --trust\n         or  conmesh agent add <source> --trust\n") {
		t.Fatalf("80 columns lost an alternative:\n%s", at80)
	}
}

func TestMenuGroupsAndRenames(t *testing.T) {
	var groups, names []string
	for _, o := range menuOptions {
		if len(groups) == 0 || groups[len(groups)-1] != o.group {
			groups = append(groups, o.group)
		}
		names = append(names, o.name)
	}
	if !reflect.DeepEqual(groups, []string{"Packages", "Project", "ConMesh"}) {
		t.Fatal("groups", groups)
	}
	if !reflect.DeepEqual(names, []string{"Install", "Update", "List", "Uninstall", "Workspace", "Doctor", "Help", "Self-update", "Quit"}) {
		t.Fatal("labels", names)
	}
}

// sameAsMatches reports whether args are an instance of a "same as" command:
// placeholders like <id> match any argument and a|b matches either word.
func sameAsMatches(command string, args []string) bool {
	words := strings.Fields(strings.TrimPrefix(command, "conmesh "))
	next := 0
	for _, word := range words {
		found := false
		for next < len(args) && !found {
			arg := args[next]
			next++
			if strings.HasPrefix(word, "<") || word == arg {
				found = true
			}
			for _, alternative := range strings.Split(word, "|") {
				found = found || alternative == arg
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestMenuItemsMatchTheirSameAsCommands(t *testing.T) {
	run := func(keys string) [][]string {
		t.Helper()
		var calls [][]string
		var out bytes.Buffer
		if err := runMenu(strings.NewReader(keys), &out, terminal{}, "dev", func(args []string) {
			calls = append(calls, args)
		}, menuUpdates{}, false, nil, menuInfo{}); err != nil {
			t.Fatal(err)
		}
		return calls
	}
	for _, o := range menuOptions {
		if o.id == "quit" {
			if len(o.sameAs) != 0 {
				t.Fatal("quit has no command")
			}
			continue
		}
		if len(o.sameAs) == 0 {
			t.Fatalf("%s has no same-as command", o.name)
		}
		// Each flow picks a package kind, enters a value, picks project scope,
		// skips the optional ref where asked, and confirms.
		confirm := "\r" + down + "\r"
		var flows []string
		switch o.id {
		case "install":
			flows = []string{"\r./source\n" + confirm, down + "\r./source\n" + confirm}
		case "uninstall":
			flows = []string{"\rexample\n" + confirm, down + "\rexample\n" + confirm}
		case "update":
			flows = []string{"\rexample\n\r\n" + down + "\r", down + "\rexample\n\r\n" + down + "\r"}
		case "self-update":
			continue // covered by TestMenuUpdateRequiresConfirmation
		default:
			flows = []string{""}
		}
		for _, flow := range flows {
			calls := run(moveTo(t, o.id) + flow + "q")
			if o.sameAsJoin == "and" {
				if len(calls) != len(o.sameAs) {
					t.Fatalf("%s ran %v for %v", o.name, calls, o.sameAs)
				}
				for i, call := range calls {
					if !sameAsMatches(o.sameAs[i], call) {
						t.Fatalf("%s ran %v, which is not %q", o.name, call, o.sameAs[i])
					}
				}
				continue
			}
			if len(calls) != 1 {
				t.Fatalf("%s ran %v for %v", o.name, calls, o.sameAs)
			}
			matched := false
			for _, command := range o.sameAs {
				matched = matched || sameAsMatches(command, calls[0])
			}
			if !matched {
				t.Fatalf("%s ran %v, which is not %v", o.name, calls[0], o.sameAs)
			}
		}
	}
}

func TestMenuKeys(t *testing.T) {
	for name, keys := range map[string]string{
		"q quits":                        "q",
		"Q quits":                        "Q",
		"esc at top level exits":         "\x1b",
		"esc followed by a key is back":  "\x1bx",
		"backspace at top level exits":   "\x7f",
		"ctrl-c exits":                   "\x03",
		"q quits from a sub-menu":        "\rq" + "\r",
		"esc backs out of a sub-menu":    "\r\x1b" + "q",
		"esc backs out of a text prompt": "\r\r\x1b" + "q",
		"ctrl-c exits from a prompt":     "\r\r\x03",
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := runMenu(strings.NewReader(keys), &out, terminal{}, "dev", func(args []string) {
				t.Fatalf("unexpected command %v", args)
			}, menuUpdates{}, false, nil, menuInfo{})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	// An arrow key sequence must still navigate rather than count as Esc.
	var calls [][]string
	keys := moveTo(t, "doctor") + strings.Repeat(down, menuIndex(t, "doctor")+1) + up + "\rq"
	if err := runMenu(strings.NewReader(keys), &bytes.Buffer{}, terminal{}, "dev", func(args []string) {
		calls = append(calls, args)
	}, menuUpdates{}, false, nil, menuInfo{}); err != nil || !reflect.DeepEqual(calls, [][]string{{"doctor"}, {"doctor"}}) {
		t.Fatalf("calls = %v, err = %v", calls, err)
	}
}

func TestMenuEOFDoesNotInstall(t *testing.T) {
	var out bytes.Buffer
	input := strings.NewReader("\r\x1b[B\rlocal-skill\n\r")
	if err := runMenu(input, &out, terminal{}, "dev", func(args []string) {
		t.Fatalf("unexpected command at EOF: %v", args)
	}, menuUpdates{}, false, nil, menuInfo{}); err != nil {
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
			keys := moveTo(t, "self-update")
			switch answer {
			case "y":
				keys += "\x1b[B\r"
			case "n":
				keys += "\x7f"
			}
			input := strings.NewReader(keys)
			if err := runMenu(input, &out, terminal{}, "v1.0.0", func(args []string) {
				calls = append(calls, args)
			}, updates, false, nil, menuInfo{}); err != nil {
				t.Fatal(err)
			}
			if checks != 1 || !strings.Contains(out.String(), "Update available: v1.1.0") {
				t.Fatalf("checks = %d, output = %q", checks, out.String())
			}
			if answer == "y" {
				if !reflect.DeepEqual(calls, [][]string{{"update", "--version", "v1.1.0"}}) {
					t.Fatalf("commands = %v", calls)
				}
				if self := menuOptions[menuIndex(t, "self-update")]; !sameAsMatches(self.sameAs[0], calls[0]) {
					t.Fatal("self-update does not match its same-as command")
				}
			} else if len(calls) != 0 {
				t.Fatalf("updated without confirmation: %v", calls)
			}
		})
	}
}

func TestMenuOpenMakesNoNetworkRequest(t *testing.T) {
	checks := 0
	updates := menuUpdates{latest: func(context.Context) (string, error) {
		checks++
		return "v1.1.0", nil
	}}
	if err := runMenu(strings.NewReader("q"), &bytes.Buffer{}, terminal{}, "v1.0.0", func([]string) {}, updates, false, nil, menuInfo{}); err != nil || checks != 0 {
		t.Fatalf("menu checked for updates without CONMESH_AUTO_UPDATE_CHECK: checks = %d, err = %v", checks, err)
	}
}

// squeeze collapses runs of spaces in each line so assertions ignore column padding.
func squeeze(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.Join(lines, "\n")
}

func TestMenuStatusStrip(t *testing.T) {
	ok := workspaceStatus{state: "ok", name: "project", indexing: true, present: true, files: 1284, harnesses: []string{"codex", "claude"}}
	head := squeeze(menuHeader(terminal{}, menuInfo{workspace: ok}, "v1.0.0", menuUpdates{}, false))
	if !strings.Contains(head, "\n* workspace project index ready, 1,284 files, harness codex + claude\n") {
		t.Fatal(head)
	}
	for _, tc := range []struct {
		ws   workspaceStatus
		want string
	}{
		{workspaceStatus{state: "ok", name: "project"}, "indexing off, no harness"},
		{workspaceStatus{state: "ok", name: "project", indexing: true}, "index not built, no harness"},
		{workspaceStatus{state: "ok", name: "p", statusErr: true}, "index status unavailable"},
		{workspaceStatus{state: "error"}, "o workspace unavailable run conmesh status for details"},
		{workspaceStatus{state: "none"}, "o no workspace run conmesh init"},
	} {
		if head := squeeze(menuHeader(terminal{}, menuInfo{workspace: tc.ws}, "v1.0.0", menuUpdates{}, false)); !strings.Contains(head, tc.want) || strings.Contains(head, "files") {
			t.Fatalf("want %q in\n%s", tc.want, head)
		}
	}
	latest := func(context.Context) (string, error) { return "", nil }
	checked := time.Now()
	for _, tc := range []struct {
		installed string
		cached    checkedRelease
		want      string
	}{
		{"v1.0.0", checkedRelease{}, "\no version v1.0.0\n"},
		{"v1.0.0", checkedRelease{"v1.2.0", checked}, "\n^ update available v1.2.0 installed v1.0.0, checked " + checked.Format("15:04") + "\n"},
		{"v1.2.0", checkedRelease{"v1.2.0", checked}, "\n+ up to date v1.2.0 last checked " + checked.Format("15:04") + "\n"},
		{"dev", checkedRelease{"v1.2.0", checked}, "\no version dev latest verified v1.2.0, checked "},
	} {
		head := squeeze(menuHeader(terminal{}, menuInfo{workspace: workspaceStatus{state: "none"}}, tc.installed, menuUpdates{latest: latest, cached: tc.cached}, false))
		if !strings.Contains(head, tc.want) {
			t.Fatalf("want %q in\n%s", tc.want, head)
		}
	}
	if head := menuHeader(terminal{}, menuInfo{}, "dev", menuUpdates{}, false); !strings.Contains(head, "self-update unavailable in this build") {
		t.Fatal(head)
	}
}

func TestMenuLinesFitTerminalWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80} {
		for selected := range menuOptions {
			for _, line := range strings.Split(renderPicker(terminal{}, width, menuOptions, selected), "\n") {
				if cells(line) > width-1 {
					t.Fatalf("width %d: line %q is %d cells", width, line, cells(line))
				}
			}
		}
	}
}

func TestMenuHelpReturnsToShell(t *testing.T) {
	var out bytes.Buffer
	var calls [][]string
	err := runMenu(strings.NewReader(moveTo(t, "help")+"\r"), &out, terminal{}, "v1.0.0", func(args []string) {
		calls = append(calls, args)
		_, _ = out.WriteString(help)
	}, menuUpdates{}, false, nil, menuInfo{})
	if err != nil || !reflect.DeepEqual(calls, [][]string{{"--help"}}) {
		t.Fatalf("calls = %v, err = %v", calls, err)
	}
	screen := out.String()
	options, commands := strings.Index(screen, "\nOptions:\n"), strings.Index(screen, "\nCommands:\n")
	if options < 0 || commands <= options || strings.Contains(screen[commands:], "navigate") {
		t.Fatal("expected options and commands followed by a return to the shell")
	}
}

func TestMenuInstallsPredefinedPackageAndListsByKind(t *testing.T) {
	catalog := []packages.CatalogEntry{
		{ID: "angular-agent", Type: "agent", Version: "1.0.0", Title: "Angular Agent", Description: "Angular work", Source: "builtin:angular-agent"},
		{ID: "dotnet-agent", Type: "agent", Version: "1.0.0", Title: ".NET Agent", Description: "C# work", Source: "builtin:dotnet-agent"},
	}
	list := func(kind string) ([]packages.Item, error) {
		if kind == "agent" {
			return []packages.Item{{Entry: packages.Entry{ID: "dotnet-agent", Version: "1.0.0", Scope: "project", Source: "builtin:dotnet-agent"}, Effective: true}}, nil
		}
		return nil, nil
	}
	var out bytes.Buffer
	var calls [][]string
	// Install: second predefined item, project scope, Confirm. Then List, then quit.
	input := moveTo(t, "install") + down + "\r" + "\r" + down + "\r" + moveTo(t, "list") + "q"
	if err := runMenu(strings.NewReader(input), &out, terminal{}, "dev", func(args []string) {
		calls = append(calls, args)
	}, menuUpdates{}, false, nil, menuInfo{packages: menuPackages{catalog: catalog, list: list}}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"agent", "add", "builtin:dotnet-agent", "--project", "--trust"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %v, want %v", calls, want)
	}
	screen := out.String()
	for _, text := range []string{"Predefined agents  shipped with ConMesh", "Angular Agent", "Folder or web URL",
		"same as  conmesh agent add builtin:angular-agent --trust",
		"Agents\n  dotnet-agent  1.0.0  project  predefined\n\nSkills\n  none installed\n\n"} {
		if !strings.Contains(screen, text) {
			t.Fatalf("screen missing %q:\n%s", text, screen)
		}
	}
	if strings.Count(screen, "none installed") != 1 || strings.Contains(screen, "requested scope") {
		t.Fatalf("list output repeated or unclear:\n%s", screen)
	}
}

func TestMenuInstallOtherSourceUsesPackageFlow(t *testing.T) {
	catalog := []packages.CatalogEntry{{ID: "angular-agent", Type: "agent", Version: "1.0.0", Title: "Angular Agent", Source: "builtin:angular-agent"}}
	var calls [][]string
	// Other source -> skill -> local folder -> project -> Confirm.
	input := moveTo(t, "install") + down + "\r" + down + "\r" + "C:/pkgs/review\r" + "\r" + down + "\r" + "q"
	if err := runMenu(strings.NewReader(input), &bytes.Buffer{}, terminal{}, "dev", func(args []string) {
		calls = append(calls, args)
	}, menuUpdates{}, false, nil, menuInfo{packages: menuPackages{catalog: catalog}}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"skill", "add", "C:/pkgs/review", "--project", "--trust"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %v, want %v", calls, want)
	}
}

func TestAltScreenEnterLeaveOnce(t *testing.T) {
	var out bytes.Buffer
	s := &altScreen{out: &out}
	s.enter()
	s.leave()
	s.leave()
	if got := out.String(); got != "\x1b[?1049h\x1b[H\x1b[2J\x1b[?1049l" {
		t.Fatalf("screen sequence %q", got)
	}
}

// Help and Self-update leave the alternate screen first so their output stays.
func TestMenuLeavesScreenBeforeLastingOutput(t *testing.T) {
	var events []string
	info := menuInfo{leaveScreen: func() { events = append(events, "leave") }}
	if err := runMenu(strings.NewReader(moveTo(t, "help")), &bytes.Buffer{}, terminal{}, "dev", func(args []string) {
		events = append(events, strings.Join(args, " "))
	}, menuUpdates{}, false, nil, info); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"leave", "--help"}) {
		t.Fatalf("events = %v", events)
	}
}
