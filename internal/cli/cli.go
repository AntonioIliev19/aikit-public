// Package cli implements ConMesh's native command surface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/harness"
	"github.com/AntonioIliev19/conmesh/internal/index"
	"github.com/AntonioIliev19/conmesh/internal/packages"
	"github.com/AntonioIliev19/conmesh/internal/update"
)

const help = `ConMesh - local-first development toolkit

Usage: conmesh [options]
       conmesh <command> [command options]

Options:
  -h, --help                        Show the command reference
  --version                         Show build version
  --no-color                        Disable colour (also NO_COLOR=1 or TERM=dumb)
  --no-input                        Never prompt; fail instead of asking for input

Commands:
  menu                              Open the interactive terminal menu
  help                              Show the command reference
  doctor [--json]                   Show runtime and implementation status
  update --version vX.Y.Z [--check] Verify signed native release; apply unless checking
  update --rollback                Restore retained previous executable
  init [--harness claude,codex]      Initialize project; indexing defaults off
       [--index | --no-index]        Explicitly enable or disable indexing
  status [--json]                   Show project configuration (always JSON)
  index                             Update explicitly enabled local index
  index --semantic [--scope path]   Embed indexed chunks using opted-in local CPU model
  search <query> [--scope path] [--limit 1..50]
         [--semantic | --fallback] Explicit semantic fusion or audited last resort
  symbol <name> [--scope path] [--limit 1..50]
  references <name>                 Approximate lexical candidates, no LSP
  research-skill                    Print shared harness-run research workflow
  knowledge scaffold|list|show|approve  Scoped proposed knowledge and explicit review
  mcp serve [--project path]        Read-only stdio tools, trusted startup root
  sync --dry-run                    Plan project Claude/Codex integration
  sync --trust                      Apply reviewed plan with owned backups
  agent|skill add <directory|https|builtin:id> --trust [--version ref] [--global|--project]
  agent|skill list [--all|--global|--project] [--json]
  agent|skill list --available [--json]  Predefined packages shipped with ConMesh
  agent|skill remove <id> [--global|--project]
  package update <id> --type agent|skill --trust [--version ref] [--global|--project]

Review package instructions and license before --trust. Git sources require an
exact --version ref; builtin:id sources need none. Archives and package hooks are unsupported. Package scope
for add/remove/update defaults to project; list defaults to all scopes. Project
overrides shadow project packages, then global ones.
`

// Run executes one command. Exit codes: 0 success, 1 operational/output failure,
// 2 invalid usage. Untrusted arguments are never echoed in diagnostics.
func Run(args []string, stdout, stderr io.Writer, version string) int {
	g, args := splitGlobalOptions(args)
	if len(args) == 0 || len(args) == 1 && args[0] == "menu" {
		out := terminalForOptions(stdout, g.noColor)
		if file, ok := stdout.(*os.File); !g.noInput && out.live && ok && isTerminal(os.Stdin) && enableColor(file) {
			cwd, _ := os.Getwd()
			info := menuInfo{width: terminalWidth(file), workspace: loadWorkspace(context.Background(), cwd), packages: loadMenuPackages(cwd)}
			// The menu runs on the terminal's alternate screen, like full-screen
			// terminal programs: the user's earlier commands are hidden while it is
			// open and come back unchanged when it closes.
			screen := &altScreen{out: stdout}
			screen.enter()
			defer screen.leave()
			info.leaveScreen = screen.leave
			err := runMenu(os.Stdin, stdout, out, version, func(args []string) {
				Run(g.with(args), stdout, stderr, version)
			}, loadMenuUpdates(), os.Getenv("CONMESH_AUTO_UPDATE_CHECK") == "1", terminalMenuMode(os.Stdin), info)
			screen.leave() // before any error, so it stays visible
			if err != nil {
				fmt.Fprintln(stderr, "conmesh:", err)
				return 1
			}
			return 0
		}
		if len(args) == 1 {
			if g.noInput {
				fmt.Fprintln(stderr, "conmesh: menu needs interactive input; run it without --no-input")
			} else {
				fmt.Fprintln(stderr, "conmesh: menu requires an interactive terminal; use --help for commands")
			}
			return 2
		}
	}
	if len(args) > 0 {
		if suggestion := suggestCommand(args[0]); suggestion != "" {
			errTerminal := terminalForOptions(stderr, g.noColor)
			interactive := !g.noInput && errTerminal.live && isTerminal(os.Stdin)
			if !offerSuggestion(os.Stdin, stderr, errTerminal, suggestion, len(args) > 1, interactive) {
				return 2
			}
			return Run(g.with(append([]string{suggestion}, args[1:]...)), stdout, stderr, version)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runContextOptions(ctx, args, stdout, stderr, version, "", "", g)
}

// globalOptions are accepted anywhere on the command line and removed before
// command parsing, so they never change how a subcommand reads its arguments.
type globalOptions struct{ noColor, noInput bool }

func splitGlobalOptions(args []string) (globalOptions, []string) {
	var g globalOptions
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--no-color":
			g.noColor = true
		case "--no-input":
			g.noInput = true
		default:
			rest = append(rest, arg)
		}
	}
	return g, rest
}

func (g globalOptions) with(args []string) []string {
	out := append([]string{}, args...)
	if g.noColor {
		out = append(out, "--no-color")
	}
	if g.noInput {
		out = append(out, "--no-input")
	}
	return out
}

var errUsage = errors.New("invalid command or arguments; use --help")

func run(args []string, stdout, stderr io.Writer, version, cwd, global string) int {
	return runContext(context.Background(), args, stdout, stderr, version, cwd, global)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer, version, cwd, global string) int {
	return runContextOptions(ctx, args, stdout, stderr, version, cwd, global, globalOptions{})
}

func runContextOptions(ctx context.Context, args []string, stdout, stderr io.Writer, version, cwd, global string, g globalOptions) int {
	if len(args) > 0 && args[0] == "mcp" {
		if err := serveMCP(ctx, args[1:], version, cwd, os.Stdin, stdout); err != nil {
			fmt.Fprintln(stderr, "conmesh:", err)
			if errors.Is(err, errUsage) {
				return 2
			}
			return 1
		}
		return 0
	}
	return runTerminal(ctx, args, stdout, stderr, version, cwd, global, terminalForOptions(stdout, g.noColor), terminalForOptions(stderr, g.noColor))
}

func runTerminal(ctx context.Context, args []string, stdout, stderr io.Writer, version, cwd, global string, outTerminal, errTerminal terminal) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	live := indexProgress{writer: stderr, terminal: errTerminal, cancel: cancel}
	var progress func(index.Progress)
	if errTerminal.live {
		progress = live.update
	}
	output, report, err := executeReport(ctx, args, version, cwd, global, progress)
	if live.err != nil {
		err = errors.New("could not write progress")
	}
	if err != nil {
		prefix := ""
		if errTerminal.color {
			prefix = glyphsFor(errTerminal).fail + " "
		}
		fmt.Fprintln(stderr, errTerminal.paint("31", prefix+"conmesh: "+err.Error()))
		if errors.Is(err, errUsage) {
			return 2
		}
		return 1
	}
	text := outTerminal.output(output)
	if report != nil && outTerminal.live {
		text = report.render(outTerminal)
	}
	if _, err := io.WriteString(stdout, text); err != nil {
		fmt.Fprintln(stderr, errTerminal.paint("31", "conmesh: could not write output"))
		return 1
	}
	return 0
}

func execute(ctx context.Context, args []string, version, cwd, global string) (string, error) {
	return executeProgress(ctx, args, version, cwd, global, nil)
}

func executeProgress(ctx context.Context, args []string, version, cwd, global string, progress func(index.Progress)) (string, error) {
	output, _, err := executeReport(ctx, args, version, cwd, global, progress)
	return output, err
}

// executeReport returns the command's plain output plus, for state-changing
// commands, a step report rendered instead of the plain text on a live terminal.
func executeReport(ctx context.Context, args []string, version, cwd, global string, progress func(index.Progress)) (string, *stepReport, error) {
	var report *stepReport
	output, err := executeCommand(ctx, args, version, cwd, global, progress, &report)
	if err != nil {
		return "", nil, err
	}
	return output, report, nil
}

func executeCommand(ctx context.Context, args []string, version, cwd, global string, progress func(index.Progress), report **stepReport) (string, error) {
	if len(args) == 0 {
		return help, nil
	}
	switch args[0] {
	case "--help", "-h", "help":
		if len(args) != 1 {
			return "", errUsage
		}
		return help, nil
	case "--version":
		if len(args) != 1 {
			return "", errUsage
		}
		return fmt.Sprintf("conmesh %s\n", version), nil
	case "research-skill":
		if len(args) != 1 {
			return "", errUsage
		}
		return researchSkill(), nil
	case "doctor":
		if len(args) == 2 && args[1] == "--json" {
			data, _ := json.MarshalIndent(doctorStatus(version), "", "  ")
			return string(data) + "\n", nil
		}
		if len(args) != 1 {
			return "", errUsage
		}
		return doctor(version), nil
	case "update":
		opts, err := parseOptions(args[1:], map[string]bool{"version": true, "check": false, "rollback": false})
		if err != nil || len(opts.positionals) != 0 || opts.has("rollback") && (opts.has("version") || opts.has("check")) || !opts.has("rollback") && !opts.has("version") {
			return "", errUsage
		}
		executable, err := os.Executable()
		if err != nil {
			return "", errors.New("executable unavailable")
		}
		if opts.has("rollback") {
			if err := update.Rollback(ctx, executable); err != nil {
				return "", err
			}
			return "Previous executable restored.\n", nil
		}
		client, err := update.New()
		if err != nil {
			return "", err
		}
		if opts.has("check") {
			m, err := client.Check(ctx, opts.values["version"])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Verified release %s for %s/%s; executable unchanged.\n", m.Version, m.OS, m.Arch), nil
		}
		if err = client.Apply(ctx, opts.values["version"], executable); err != nil {
			return "", err
		}
		return "Verified update installed; previous executable retained for update --rollback.\n", nil
	case "init", "status", "index", "search", "symbol", "references", "knowledge", "agent", "skill", "package", "sync":
	default:
		return "", errUsage
	}
	var err error
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", errors.New("working directory unavailable")
		}
	}
	if args[0] == "init" {
		opts, err := parseOptions(args[1:], map[string]bool{"harness": true, "index": false, "no-index": false})
		if err != nil || len(opts.positionals) != 0 || opts.has("index") && opts.has("no-index") {
			return "", errUsage
		}
		c := config.Default()
		c.Indexing.Enabled = opts.has("index")
		if opts.has("harness") {
			c.Harnesses = strings.Split(opts.values["harness"], ",")
		} else {
			c.Harnesses = discoverHarnesses(exec.LookPath)
		}
		c, created, err := config.Init(cwd, c)
		if err != nil {
			return "", err
		}
		name := printable(filepath.Base(cwd))
		if !created {
			*report = &stepReport{
				steps: []reportStep{{"Already initialized", name, "existing configuration preserved"}},
				next:  []nextCommand{{"conmesh status", "show project configuration"}},
			}
			return fmt.Sprintf("Project already initialized; existing configuration preserved. Indexing enabled: %t\n", c.Indexing.Enabled), nil
		}
		r := &stepReport{
			steps:   []reportStep{{"Initialized", name, fmt.Sprintf("indexing %s, %s", onOff(c.Indexing.Enabled), harnessList(c.Harnesses))}},
			changed: []string{"+ .conmesh/" + config.File},
		}
		if c.Indexing.Enabled {
			r.next = append(r.next, nextCommand{"conmesh index", "build the local index"})
		}
		if len(c.Harnesses) > 0 {
			r.next = append(r.next, nextCommand{"conmesh sync --dry-run", "preview harness integration for this project"})
		}
		*report = r
		return fmt.Sprintf("Project initialized. Indexing enabled: %t; semantic indexing enabled: %t\n", c.Indexing.Enabled, c.Indexing.Semantic), nil
	}
	project, c, findErr := "", config.Config{}, error(config.ErrNoProject)
	explicitGlobal := false
	for _, arg := range args[1:] {
		if arg == "--global" {
			explicitGlobal = true
		}
	}
	if !explicitGlobal || args[0] == "status" {
		project, c, findErr = config.Find(cwd)
	}
	if findErr != nil && !errors.Is(findErr, config.ErrNoProject) {
		return "", findErr
	}
	if args[0] == "knowledge" {
		if findErr != nil {
			return "", findErr
		}
		output, err := knowledgeCommand(ctx, project, args[1:])
		if err == nil && args[1] == "approve" {
			*report = &stepReport{
				steps:   []reportStep{{"Approved", "reviewed digests", "entry, document and citation digests"}},
				changed: []string{"+ approval for this user and project"},
			}
		}
		return output, err
	}
	if args[0] == "sync" {
		opts, err := parseOptions(args[1:], map[string]bool{"dry-run": false, "trust": false})
		if err != nil || len(opts.positionals) != 0 || opts.has("dry-run") && opts.has("trust") {
			return "", errUsage
		}
		if findErr != nil {
			return "", findErr
		}
		if global == "" {
			global, err = config.GlobalDir()
			if err != nil {
				return "", err
			}
		}
		executable, err := os.Executable()
		if err != nil {
			return "", errors.New("executable unavailable")
		}
		manager := harness.Manager{Project: project, Global: global, Executable: executable}
		plan, err := manager.Sync(ctx, opts.has("dry-run"), opts.has("trust"))
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(plan, "", "  ")
		return string(data) + "\n", nil
	}
	if args[0] == "index" || args[0] == "search" || args[0] == "symbol" || args[0] == "references" {
		if findErr != nil {
			return "", findErr
		}
		core, err := index.New(project)
		if err != nil {
			return "", err
		}
		var value any
		if args[0] == "index" {
			opts, parseErr := parseOptions(args[1:], map[string]bool{"semantic": false, "scope": true})
			if parseErr != nil || len(opts.positionals) != 0 || opts.has("scope") && !opts.has("semantic") {
				return "", errUsage
			}
			if opts.has("semantic") {
				value, err = core.IndexSemantic(ctx, opts.values["scope"])
			} else {
				value, err = core.Index(ctx, progress)
			}
		} else {
			opts, parseErr := parseOptions(args[1:], map[string]bool{"scope": true, "limit": true, "semantic": false, "fallback": false})
			if parseErr != nil || len(opts.positionals) != 1 || opts.has("semantic") && opts.has("fallback") || args[0] != "search" && (opts.has("semantic") || opts.has("fallback")) {
				return "", errUsage
			}
			query := index.Query{Text: opts.positionals[0], Scope: opts.values["scope"]}
			if opts.has("limit") {
				query.Limit, err = strconv.Atoi(opts.values["limit"])
				if err != nil || query.Limit < 1 || query.Limit > index.MaxResults {
					return "", errUsage
				}
			}
			if args[0] == "search" {
				if opts.has("semantic") {
					value, err = core.Hybrid(ctx, query)
				} else if opts.has("fallback") {
					value, err = core.Fallback(ctx, query)
				} else {
					value, err = core.Search(ctx, query)
				}
			} else if args[0] == "references" {
				value, err = core.FindReferenceCandidates(ctx, query)
			} else {
				value, err = core.FindSymbol(ctx, query)
			}
		}
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(value, "", "  ")
		return string(data) + "\n", nil
	}
	if args[0] == "status" {
		// Status output is already JSON; --json is accepted so scripts can be explicit.
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			return "", errUsage
		}
		if findErr != nil {
			return "", findErr
		}
		core, err := index.New(project)
		if err != nil {
			return "", err
		}
		status, err := core.Status(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(struct {
			config.Config
			Index index.Status `json:"index"`
		}{c, status}, "", "  ")
		return string(data) + "\n", nil
	}
	if global == "" {
		global, err = config.GlobalDir()
		if err != nil {
			return "", err
		}
	}
	m := packages.New(project, global)
	if len(args) < 2 {
		return "", errUsage
	}
	if args[0] == "package" {
		if args[1] != "update" {
			return "", errUsage
		}
		opts, err := parseOptions(args[2:], map[string]bool{"global": false, "project": false, "trust": false, "version": true, "type": true})
		if err != nil || len(opts.positionals) != 1 || !opts.has("type") {
			return "", errUsage
		}
		scope, err := opts.scope()
		if err != nil {
			return "", err
		}
		kind := opts.values["type"]
		if kind != "agent" && kind != "skill" {
			return "", errUsage
		}
		result, err := m.Update(ctx, kind, opts.positionals[0], opts.values["version"], scope, opts.has("trust"))
		if err != nil {
			return "", err
		}
		*report = packageReport(result, true, project)
		return formatResult(result), nil
	}
	kind, action := args[0], args[1]
	allowed := map[string]bool{"global": false, "project": false}
	switch action {
	case "add":
		allowed["version"] = true
		allowed["trust"] = false
	case "list":
		allowed["all"] = false
		allowed["json"] = false
		allowed["available"] = false
	case "remove":
	default:
		return "", errUsage
	}
	opts, err := parseOptions(args[2:], allowed)
	if err != nil {
		return "", errUsage
	}
	scope, err := opts.scope()
	if err != nil {
		return "", err
	}
	switch action {
	case "add":
		if len(opts.positionals) != 1 {
			return "", errUsage
		}
		source := opts.positionals[0]
		if !strings.Contains(source, "://") && !strings.HasPrefix(source, packages.BuiltinPrefix) && !filepath.IsAbs(source) {
			source = filepath.Join(cwd, source)
		}
		result, err := m.Add(ctx, kind, source, opts.values["version"], scope, opts.has("trust"))
		if err != nil {
			return "", err
		}
		*report = packageReport(result, false, project)
		return formatResult(result), nil
	case "remove":
		if len(opts.positionals) != 1 {
			return "", errUsage
		}
		removed, err := m.Remove(ctx, kind, opts.positionals[0], scope)
		if err != nil {
			return "", err
		}
		// Remove validated the ID before succeeding, so it is safe to show.
		subject := kind + " " + opts.positionals[0]
		if !removed {
			*report = &stepReport{steps: []reportStep{{"Already absent", subject, scope + " scope"}}}
			return "Package already absent in requested scope.\n", nil
		}
		r := &stepReport{
			steps:   []reportStep{{"Removed", subject, scope + " scope"}},
			changed: []string{fmt.Sprintf("- %s (%s)", subject, scope)},
		}
		if project != "" {
			r.next = syncNext()
		}
		*report = r
		return "Package removed from requested scope.\n", nil
	case "list":
		if len(opts.positionals) != 0 || opts.has("all") && (opts.has("global") || opts.has("project")) {
			return "", errUsage
		}
		if opts.has("available") {
			if opts.has("all") || opts.has("global") || opts.has("project") {
				return "", errUsage
			}
			return availableOutput(kind, opts.has("json"))
		}
		selectedScope := ""
		if opts.has("global") {
			selectedScope = "global"
		} else if opts.has("project") {
			selectedScope = "project"
		}
		items, err := m.List(ctx, kind, selectedScope)
		if err != nil {
			return "", err
		}
		selected := []packages.Item{}
		for _, item := range items {
			if opts.has("global") && item.Scope != "global" || opts.has("project") && item.Scope == "global" {
				continue
			}
			selected = append(selected, item)
		}
		if opts.has("json") {
			data, _ := json.MarshalIndent(selected, "", "  ")
			return string(data) + "\n", nil
		}
		var out strings.Builder
		for _, item := range selected {
			state := "effective"
			if !item.Effective {
				state = "shadowed by " + item.ShadowedBy
			}
			fmt.Fprintf(&out, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Version, item.Scope, state, item.Source)
		}
		if out.Len() == 0 {
			return "No packages installed in requested scope.\n", nil
		}
		return out.String(), nil
	}
	return "", errUsage
}

// availableOutput lists the predefined packages shipped in this binary.
func availableOutput(kind string, asJSON bool) (string, error) {
	entries, err := packages.Catalog(kind)
	if err != nil {
		return "", err
	}
	if asJSON {
		if entries == nil {
			entries = []packages.CatalogEntry{}
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data) + "\n", nil
	}
	if len(entries) == 0 {
		return "No predefined " + kind + "s in this build.\n", nil
	}
	var out strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s\n", e.Source, e.Version, e.Title, e.Description)
	}
	return out.String(), nil
}

func formatResult(r packages.Result) string {
	verb := "Installed"
	if r.Unchanged {
		verb = "Unchanged"
	}
	return fmt.Sprintf("%s %s %s %s in %s scope; omitted files/directories: %d\n", verb, r.Entry.Type, r.Entry.ID, r.Entry.Version, r.Entry.Scope, r.Omitted)
}

// packageReport describes an add or update from the manager's real result.
// Sync is suggested only inside a project, where it renders packages into harnesses.
func packageReport(r packages.Result, updated bool, project string) *stepReport {
	e := r.Entry
	detail := fmt.Sprintf("version %s, %s scope", e.Version, e.Scope)
	if r.Omitted > 0 {
		detail += fmt.Sprintf(", %d files/directories omitted", r.Omitted)
	}
	label, sign := "Installed", "+"
	if updated {
		label, sign = "Updated", "~"
	}
	report := &stepReport{}
	if r.Unchanged {
		label = "Unchanged"
	} else {
		report.changed = []string{fmt.Sprintf("%s %s %s %s (%s)", sign, e.Type, e.ID, e.Version, e.Scope)}
		if project != "" {
			report.next = syncNext()
		}
	}
	report.steps = []reportStep{{label, e.Type + " " + e.ID, detail}}
	return report
}

type options struct {
	values      map[string]string
	positionals []string
}

func (o options) has(name string) bool { _, ok := o.values[name]; return ok }
func (o options) scope() (string, error) {
	if o.has("global") && o.has("project") {
		return "", errUsage
	}
	if o.has("global") {
		return "global", nil
	}
	return "project", nil
}

func parseOptions(args []string, allowed map[string]bool) (options, error) {
	o := options{values: map[string]string{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			o.positionals = append(o.positionals, arg)
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			return o, errUsage
		}
		name, value, hasEqual := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		needsValue, ok := allowed[name]
		if !ok || o.has(name) {
			return o, errUsage
		}
		if needsValue {
			if !hasEqual {
				i++
				if i >= len(args) {
					return o, errUsage
				}
				value = args[i]
			}
			if value == "" || strings.HasPrefix(value, "-") {
				return o, errUsage
			}
		} else if hasEqual {
			return o, errUsage
		}
		o.values[name] = value
	}
	return o, nil
}
