package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/index"
	"github.com/AntonioIliev19/conmesh/internal/packages"
	"github.com/AntonioIliev19/conmesh/internal/update"
)

// menuOptions is the main menu. sameAs holds the equivalent non-interactive
// commands, using the real subcommand names, shown for the highlighted item.
var menuOptions = []menuOption{
	{id: "install", group: "Packages", subtitle: "agents and skills", name: "Install", description: "Add an agent or skill",
		sameAs: []string{"conmesh skill add <source> --trust", "conmesh agent add <source> --trust"}},
	{id: "update", group: "Packages", name: "Update", description: "Update an installed agent or skill",
		sameAs: []string{"conmesh package update <id> --type skill|agent --trust"}},
	{id: "list", group: "Packages", name: "List", description: "Show what is installed",
		sameAs: []string{"conmesh agent list", "conmesh skill list"}, sameAsJoin: "and"},
	{id: "uninstall", group: "Packages", name: "Uninstall", description: "Remove a managed package",
		sameAs: []string{"conmesh skill remove <id>", "conmesh agent remove <id>"}},
	{id: "workspace", group: "Project", name: "Workspace", description: "Index and harness status",
		sameAs: []string{"conmesh status"}},
	{id: "doctor", group: "Project", name: "Doctor", description: "Check runtime capabilities",
		sameAs: []string{"conmesh doctor"}},
	{id: "help", group: "ConMesh", name: "Help", description: "Command reference",
		sameAs: []string{"conmesh --help"}},
	{id: "self-update", group: "ConMesh", name: "Self-update", description: "Check for and install a verified release",
		sameAs: []string{"conmesh update --version <vX.Y.Z>"}},
	{id: "quit", group: "ConMesh", name: "Quit"},
}

// menuInfo carries facts gathered once when the menu opens. No network is used.
type menuInfo struct {
	width     int
	workspace workspaceStatus
	packages  menuPackages
	// leaveScreen returns to the normal screen before output that must stay
	// visible after the menu closes (Help, Self-update). Nil in tests.
	leaveScreen func()
	// updated is called after the menu ran a confirmed self-update, before the
	// menu closes; the caller reopens the new menu if the update succeeded.
	updated func()
}

// altScreen switches to the terminal's alternate screen buffer and back.
// leave is safe to call more than once.
type altScreen struct {
	out    io.Writer
	active bool
}

func (s *altScreen) enter() {
	if _, err := io.WriteString(s.out, "\x1b[?1049h\x1b[H\x1b[2J"); err == nil {
		s.active = true
	}
}

func (s *altScreen) leave() {
	if s.active {
		s.active = false
		io.WriteString(s.out, "\x1b[?1049l")
	}
}

// menuPackages gives the menu direct access to installed and predefined
// packages. When unset (tests), List and Install fall back to plain commands.
type menuPackages struct {
	catalog []packages.CatalogEntry
	list    func(kind string) ([]packages.Item, error)
}

func loadMenuPackages(cwd string) menuPackages {
	var mp menuPackages
	mp.catalog, _ = packages.Catalog("")
	project, _, err := config.Find(cwd)
	if err != nil {
		project = ""
	}
	global, err := config.GlobalDir()
	if err != nil {
		return mp
	}
	m := packages.New(project, global)
	mp.list = func(kind string) ([]packages.Item, error) { return m.List(context.Background(), kind, "") }
	return mp
}

// packageListing shows installed agents and skills under their own headings.
func packageListing(t terminal, list func(string) ([]packages.Item, error)) string {
	var b strings.Builder
	for i, kind := range []string{"agent", "skill"} {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		fmt.Fprintln(&b, t.paint(styleBold, strings.ToUpper(kind[:1])+kind[1:]+"s"))
		items, err := list(kind)
		if err != nil {
			fmt.Fprintln(&b, "  "+t.paint(styleWarn, "unavailable")+t.paint(styleDim, "  run conmesh "+kind+" list for details"))
			continue
		}
		if len(items) == 0 {
			fmt.Fprintln(&b, t.paint(styleDim, "  none installed"))
			continue
		}
		idWidth := 0
		for _, item := range items {
			idWidth = max(idWidth, cells(item.ID))
		}
		for _, item := range items {
			source := printable(item.Source)
			if strings.HasPrefix(source, packages.BuiltinPrefix) {
				source = "predefined"
			}
			detail := item.Version + "  " + item.Scope + "  " + source
			if !item.Effective {
				detail += "  shadowed by " + printable(item.ShadowedBy)
			}
			fmt.Fprintln(&b, "  "+pad(item.ID, idWidth)+"  "+t.paint(styleDim, detail))
		}
	}
	return b.String()
}

type workspaceStatus struct {
	state     string // "", "none" (no project), "error", "ok"
	name      string
	indexing  bool
	present   bool
	statusErr bool
	files     int
	harnesses []string
}

// loadWorkspace reads the same configuration and index status as `conmesh status`.
// index.Status never creates state.
func loadWorkspace(ctx context.Context, cwd string) workspaceStatus {
	if cwd == "" {
		return workspaceStatus{state: "error"}
	}
	project, c, err := config.Find(cwd)
	if errors.Is(err, config.ErrNoProject) {
		return workspaceStatus{state: "none"}
	}
	if err != nil {
		return workspaceStatus{state: "error"}
	}
	ws := workspaceStatus{state: "ok", name: printable(filepath.Base(project)), indexing: c.Indexing.Enabled, harnesses: c.Harnesses}
	core, err := index.New(project)
	if err != nil {
		ws.statusErr = true
		return ws
	}
	status, err := core.Status(ctx)
	if err != nil {
		ws.statusErr = true
		return ws
	}
	ws.indexing, ws.present, ws.files = status.Enabled, status.Present, status.Files
	return ws
}

func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func menuHeader(t terminal, info menuInfo, version string, updates menuUpdates, checking bool) string {
	g := glyphsFor(t)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\n", t.paint(styleAccent, g.brand), t.paint("1;34", "ConMesh"), t.paint(styleDim, version+"   context layer for AI agents"))
	fmt.Fprintln(&b, t.paint(styleDim, menuRule(g, info.width)))

	var rows [2]statusRow
	ws := info.workspace
	switch ws.state {
	case "ok":
		detail := "indexing off"
		switch {
		case ws.statusErr:
			detail = "index status unavailable"
		case ws.indexing && !ws.present:
			detail = "index not built"
		case ws.indexing:
			detail = "index ready, " + thousands(ws.files) + " files"
		}
		rows[0] = statusRow{glyph: t.paint(styleOK, g.on), label: "workspace", value: ws.name, detail: detail + ", " + harnessList(ws.harnesses)}
	case "none":
		rows[0] = statusRow{glyph: t.paint(styleDim, g.off), label: "no workspace", detail: "run " + t.paint(styleAccent, "conmesh init")}
	default:
		rows[0] = statusRow{glyph: t.paint(styleDim, g.off), label: "workspace", value: "unavailable", detail: "run " + t.paint(styleAccent, "conmesh status") + " for details"}
	}
	rows[1] = versionRow(t, g, version, updates)
	if checking {
		if rows[1].detail != "" {
			rows[1].detail += ", "
		}
		rows[1].detail += "checking for updates..."
	}
	labelWidth := max(cells(rows[0].label), cells(rows[1].label))
	valueWidth := max(cells(rows[0].value), cells(rows[1].value))
	for _, r := range rows {
		labelStyle := styleDim
		if r.warn {
			labelStyle = styleWarn
		}
		line := r.glyph + " " + t.paint(labelStyle, pad(r.label, labelWidth))
		if r.value != "" {
			line += "  " + t.paint(styleBold, pad(r.value, valueWidth))
		}
		if r.detail != "" {
			line += "  " + t.paint(styleDim, r.detail)
		}
		fmt.Fprintln(&b, strings.TrimRight(line, " "))
	}
	fmt.Fprintln(&b)
	return b.String()
}

// versionRow uses only the cached result of the last update check.
type statusRow struct {
	glyph, label, value, detail string
	warn                        bool
}

func versionRow(t terminal, g glyphSet, installed string, updates menuUpdates) statusRow {
	if updates.latest == nil {
		return statusRow{glyph: t.paint(styleDim, g.off), label: "version", value: installed, detail: "self-update unavailable in this build"}
	}
	cached := updates.cached
	if cached.Checked.IsZero() || !releaseVersion(cached.Version) {
		return statusRow{glyph: t.paint(styleDim, g.off), label: "version", value: installed}
	}
	checked := checkedAt(cached.Checked)
	switch {
	case update.Newer(cached.Version, installed):
		return statusRow{glyph: t.paint(styleWarn, g.up), label: "update available", value: cached.Version, detail: "installed " + installed + ", checked " + checked, warn: true}
	case releaseVersion(installed):
		return statusRow{glyph: t.paint(styleOK, g.ok), label: "up to date", value: installed, detail: "last checked " + checked}
	default:
		return statusRow{glyph: t.paint(styleDim, g.off), label: "version", value: installed, detail: "latest verified " + cached.Version + ", checked " + checked}
	}
}

func releaseVersion(v string) bool { return v == "v0.0.0" || update.Newer(v, "v0.0.0") }

func checkedAt(when time.Time) string {
	local, now := when.Local(), time.Now()
	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
		return local.Format("15:04")
	}
	return local.Format("2006-01-02 15:04")
}

func menuRule(g glyphSet, width int) string {
	n := 46
	if width > 1 {
		n = min(n, width-1)
	}
	return strings.Repeat(g.rule, n)
}

func runMenu(input io.Reader, out io.Writer, t terminal, version string, run func([]string), updates menuUpdates, autoCheck bool, raw func() (func() error, error), info menuInfo) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pending <-chan string
	if updates.latest != nil && autoCheck && time.Since(updates.cached.Checked) >= 24*time.Hour {
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
	}
	if _, err := io.WriteString(out, menuHeader(t, info, version, updates, pending != nil)); err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	leave := func() {
		if info.leaveScreen != nil {
			info.leaveScreen()
		}
	}
	var inputErr error
	pick := func(title string, options []menuOption) int {
		if inputErr != nil {
			return -1
		}
		var choice int
		choice, inputErr = pickMenu(reader, out, t, info.width, title, options, raw)
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
		return pick(label, []menuOption{{name: "Cancel"}, {name: "Confirm"}}) == 1
	}
	for {
		choice := pick("", menuOptions)
		select {
		case status := <-pending:
			if _, err := fmt.Fprintln(out, "  "+status); err != nil {
				return err
			}
			pending = nil
		default:
		}
		id := "quit"
		if choice >= 0 {
			id = menuOptions[choice].id
		}
		switch id {
		case "quit":
			if inputErr == nil || errors.Is(inputErr, io.EOF) || errors.Is(inputErr, errMenuInterrupt) || errors.Is(inputErr, errMenuQuit) {
				return nil
			}
			return inputErr
		case "install":
			menuInstall(out, prompt, pick, confirm, run, info.packages.catalog)
		case "uninstall":
			menuPackage(out, prompt, pick, confirm, run, "remove")
		case "update":
			menuPackage(out, prompt, pick, confirm, run, "update")
		case "list":
			if info.packages.list != nil {
				if _, err := io.WriteString(out, packageListing(t, info.packages.list)); err != nil {
					return err
				}
			} else {
				run([]string{"agent", "list"})
				run([]string{"skill", "list"})
			}
		case "workspace":
			run([]string{"status"})
		case "doctor":
			run([]string{"doctor"})
		case "help":
			leave()
			run([]string{"--help"})
			return nil
		case "self-update":
			// The update closes the menu; its result must stay on the normal screen.
			err := updates.install(ctx, out, version, confirm, func(args []string) {
				leave()
				run(args)
			})
			if err == errMenuExit {
				if info.updated != nil {
					info.updated()
				}
				return nil
			}
			if err != nil {
				return err
			}
		}
		// Separate the action's output from the menu drawn below it.
		if _, err := fmt.Fprintln(out); err != nil {
			return err
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
			key, err := readMenuEscape(input)
			if err != nil {
				return "", err
			}
			if key == keyBack {
				_, err := fmt.Fprintln(out)
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
	kinds := []menuOption{{name: "agent", description: "Managed agent"}, {name: "skill", description: "Managed skill"}}
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
	scopes := []menuOption{{name: "project", description: "Current project"}, {name: "global", description: "All projects for this user"}}
	selected = pick("Choose a scope", scopes)
	if selected < 0 {
		return
	}
	scope := scopes[selected].name
	args := menuPackageArgs(action, kind, value, scope)
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
		fmt.Fprintln(out, "  Packages from a folder or URL come from their authors, who are responsible for them, not ConMesh.")
		fmt.Fprintln(out, "  Review the package instructions and license before confirming.")
	}
	fmt.Fprintf(out, "  %s %s %q (%s scope)\n", action, kind, value, scope)
	if confirm("Apply this package change?") {
		run(args)
	}
}

// menuInstall offers the predefined packages first, then any local folder or
// HTTPS Git URL through the regular package flow.
func menuInstall(out io.Writer, prompt func(string) string, pick func(string, []menuOption) int, confirm func(string) bool, run func([]string), catalog []packages.CatalogEntry) {
	if len(catalog) == 0 {
		menuPackage(out, prompt, pick, confirm, run, "add")
		return
	}
	options := make([]menuOption, 0, len(catalog)+1)
	for _, e := range catalog {
		options = append(options, menuOption{group: "Predefined " + e.Type + "s", subtitle: "shipped with ConMesh", name: e.Title, description: e.Description,
			sameAs: []string{"conmesh " + e.Type + " add " + e.Source + " --trust"}})
	}
	options = append(options, menuOption{group: "Other source", name: "Folder or web URL", description: "Local package directory or HTTPS Git URL",
		sameAs: []string{"conmesh skill add <source> --trust", "conmesh agent add <source> --trust"}})
	choice := pick("Choose what to install", options)
	if choice < 0 {
		return
	}
	if choice == len(catalog) {
		menuPackage(out, prompt, pick, confirm, run, "add")
		return
	}
	e := catalog[choice]
	scopes := []menuOption{{name: "project", description: "Current project"}, {name: "global", description: "All projects for this user"}}
	selected := pick("Choose a scope", scopes)
	if selected < 0 {
		return
	}
	scope := scopes[selected].name
	fmt.Fprintf(out, "  add %s %s %s (%s scope)\n", e.Type, e.ID, e.Version, scope)
	fmt.Fprintln(out, "  Its instructions and license are copied into ConMesh's package store;")
	fmt.Fprintln(out, "  review them and `conmesh sync --dry-run` before applying with --trust.")
	if confirm("Apply this package change?") {
		run([]string{e.Type, "add", e.Source, "--" + scope, "--trust"})
	}
}

// menuPackageArgs is the command a package menu flow runs before optional
// --version and --trust are appended.
func menuPackageArgs(action, kind, value, scope string) []string {
	if action == "update" {
		return []string{"package", "update", value, "--type", kind, "--" + scope}
	}
	return []string{kind, action, value, "--" + scope}
}
