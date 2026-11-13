package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

var menuOptions = []menuOption{
	{"Install", "Add an agent or skill"},
	{"Uninstall", "Remove a managed package"},
	{"Update package", "Update an agent or skill"},
	{"List", "Show installed agents and skills"},
	{"Workspace", "Show ConMesh project status"},
	{"Doctor", "Check runtime capabilities"},
	{"Help", "Show the command reference"},
	{"Update ConMesh", "Check for and install a verified release"},
	{"Quit", ""},
}

// Each row draws CONMESH. The shadow is the same shape shifted one cell down and right.
var wordmark = [...]string{
	" ###   ###  #   # #   # #####  #### #   #",
	"#     #   # ##  # ## ## #     #     #   #",
	"#     #   # # # # # # # ####   ###  #####",
	"#     #   # #  ## #   # #         # #   #",
	" ###   ###  #   # #   # ##### ####  #   #",
}

func menuBanner(t terminal, version string) string {
	const width = 49
	border := strings.Repeat("─", width)
	green := [...]string{"38;5;22", "38;5;28", "38;5;34", "38;5;35"}
	var b strings.Builder
	fmt.Fprintln(&b, "╭"+border+"╮")
	for y := range len(wordmark) + 1 {
		var row strings.Builder
		for x := range len(wordmark[0]) + 1 {
			switch {
			case y < len(wordmark) && x < len(wordmark[y]) && wordmark[y][x] == '#':
				row.WriteString(t.paint("38;5;120", "█"))
			case y > 0 && x > 0 && x-1 < len(wordmark[y-1]) && wordmark[y-1][x-1] == '#':
				row.WriteString(t.paint(green[min(x/11, len(green)-1)], "▓"))
			default:
				row.WriteByte(' ')
			}
		}
		fmt.Fprintf(&b, "│  %s%s│\n", row.String(), strings.Repeat(" ", width-3-len(wordmark[0])))
	}
	fmt.Fprintf(&b, "│  %-47s│\n", "CONMESH "+version)
	fmt.Fprintf(&b, "│  %-47s│\n", "Codebase-aware context for AI agents")
	fmt.Fprintln(&b, "╰"+border+"╯")
	return b.String()
}

func runMenu(input io.Reader, out io.Writer, t terminal, version string, run func([]string), updates menuUpdates, autoCheck bool, raw func() (func() error, error)) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := "Self-update unavailable in this build."
	var pending <-chan string
	if updates.latest != nil {
		status = releaseStatus(version, updates.cached)
		if autoCheck && time.Since(updates.cached.Checked) >= 24*time.Hour {
			results := make(chan string, 1)
			pending = results
			go func(updates menuUpdates) {
				result, err := updates.refresh(ctx)
				if err != nil {
					results <- "Could not verify the latest release. " + releaseStatus(version, updates.cached)
					return
				}
				results <- releaseStatus(version, result)
			}(updates)
			status += " Checking for updates…"
		}
	}
	if _, err := io.WriteString(out, menuBanner(t, version)+"  "+status+"\n"); err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	var inputErr error
	pick := func(title string, options []menuOption) int {
		if inputErr != nil {
			return -1
		}
		var choice int
		choice, inputErr = pickMenu(reader, out, t, title, options, raw)
		return choice
	}
	prompt := func(label string) string {
		if inputErr != nil {
			return ""
		}
		var value string
		value, inputErr = readMenuText(reader, out, t.paint("96", label)+" ", raw)
		return value
	}
	confirm := func(label string) bool {
		return pick(label, []menuOption{{"Cancel", ""}, {"Confirm", ""}}) == 1
	}
	for {
		choice := pick("What would you like to do?", menuOptions)
		select {
		case status := <-pending:
			if _, err := fmt.Fprintln(out, "  "+status); err != nil {
				return err
			}
			pending = nil
		default:
		}
		switch choice {
		case -1, 8:
			if errors.Is(inputErr, io.EOF) || errors.Is(inputErr, errMenuInterrupt) {
				return nil
			}
			return inputErr
		case 0:
			menuPackage(out, prompt, pick, confirm, run, "add")
		case 1:
			menuPackage(out, prompt, pick, confirm, run, "remove")
		case 2:
			menuPackage(out, prompt, pick, confirm, run, "update")
		case 3:
			run([]string{"agent", "list"})
			run([]string{"skill", "list"})
		case 4:
			run([]string{"status"})
		case 5:
			run([]string{"doctor"})
		case 6:
			run([]string{"--help"})
			return nil
		case 7:
			err := updates.install(ctx, out, version, confirm, run)
			if err == errMenuExit {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
}

func readMenuText(input *bufio.Reader, out io.Writer, label string, raw func() (func() error, error)) (value string, err error) {
	if raw != nil {
		restore, err := raw()
		if err != nil {
			return "", err
		}
		defer func() { err = errors.Join(err, restore()) }()
	}
	if _, err := io.WriteString(out, label); err != nil {
		return "", err
	}
	var text []rune
	for {
		r, _, err := input.ReadRune()
		if err != nil {
			return "", err
		}
		switch r {
		case 27:
			if _, err := skipMenuEscape(input); err != nil {
				return "", err
			}
		case '\r', '\n':
			_, err := fmt.Fprintln(out)
			return strings.TrimSpace(string(text)), err
		case 3:
			return "", errMenuInterrupt
		case '\b', 127:
			if len(text) == 0 {
				_, err := fmt.Fprintln(out)
				return "", err
			}
			text = text[:len(text)-1]
			_, err = io.WriteString(out, "\b \b")
		default:
			if unicode.IsPrint(r) && len(text) < 4096 {
				text = append(text, r)
				_, err = fmt.Fprint(out, string(r))
			}
		}
		if err != nil {
			return "", err
		}
	}
}

func menuPackage(out io.Writer, prompt func(string) string, pick func(string, []menuOption) int, confirm func(string) bool, run func([]string), action string) {
	kinds := []menuOption{{"agent", "Managed agent"}, {"skill", "Managed skill"}}
	selected := pick("Choose a package type", kinds)
	if selected < 0 {
		return
	}
	kind := kinds[selected].name
	label := "Package ID"
	if action == "add" {
		label = "Local package directory or HTTPS Git URL"
	}
	value := prompt(label + " [Enter to go back] ›")
	if value == "" || strings.HasPrefix(value, "-") {
		return
	}
	scopes := []menuOption{{"project", "Current project"}, {"global", "All projects for this user"}}
	selected = pick("Choose a scope", scopes)
	if selected < 0 {
		return
	}
	scope := scopes[selected].name
	args := []string{kind, action, value, "--" + scope}
	if action == "update" {
		args = []string{"package", "update", value, "--type", kind, "--" + scope}
	}
	if action == "update" || action == "add" && strings.HasPrefix(value, "https://") {
		ref := prompt("Exact Git ref [Enter to skip for local updates] ›")
		if ref != "" {
			args = append(args, "--version", ref)
		} else if action == "add" {
			return
		}
	}
	if action != "remove" {
		args = append(args, "--trust")
		fmt.Fprintln(out, "  Review the package instructions and license before confirming.")
	}
	fmt.Fprintf(out, "  %s %s %q (%s scope)\n", action, kind, value, scope)
	if confirm("Apply this package change?") {
		run(args)
	}
}
