# AIKIT

AIKIT is a cross-platform, local-first command-line toolkit that manages reusable agents and skills, studies repository architecture, saves small evidence-backed knowledge packs, and exposes a shared hybrid code index to Claude Code and Codex via MCP.

## Install AIKIT

AIKIT is a native executable. Installing an existing trusted Windows binary
requires no Go, Node.js, administrator privileges, model or account. Build the executable from source using the instructions below.

In your own PowerShell window, from this repository with `aikit.exe` in its root:

```powershell
Test-Path .\aikit.exe
.\scripts\install.cmd .\aikit.exe
```

The installer copies the binary into your per-user AIKIT directory and adds that
directory to your user PATH. If you run the installer from another directory,
replace `C:\path\to\AIKIT` with the full path to your AIKIT checkout.

```powershell
& 'C:\path\to\AIKIT\scripts\install.cmd' 'C:\path\to\AIKIT\aikit.exe'
```

Close and reopen the terminal application, including a parent editor such as VS
Code if it launches your terminals. Then verify from any directory:

```powershell
aikit --version
aikit --help
```

Bare `aikit` also displays help; it does not open an interactive application.
Adding a directory to PATH does not create or install `aikit.exe`. If command
discovery fails, check that the installed file exists and run it directly:

```powershell
$aikitExe = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AIKIT\bin\aikit.exe'
Test-Path $aikitExe
& $aikitExe --version
Get-Command aikit -All
```

If `Test-Path` returns `False`, install a trusted binary first. Restarting parent
applications refreshes inherited PATH; it cannot fix a missing executable.

## Setup for your project

Follow these steps to build a local index and run the Research Agent in your
existing project chat. The examples use PowerShell.

### 1. Open your project and initialize AIKIT

Replace the example path with the root of your own project.

```powershell
Set-Location 'C:\path\to\your-project'
aikit init --harness codex --index
aikit status
```

`init` creates `.aikit/config.yaml`. `--harness codex` selects Codex and `--index`
enables indexing in the settings. Use `--harness claude` for Claude Code or
`--harness claude,codex` for both. For OpenCode, Cursor, or another local host,
use `aikit init --index`. If you only want package management, use `aikit init`
and stop here. `aikit doctor` checks available runtime features.

If the project is already initialized, `init` preserves its settings. Edit
`.aikit/config.yaml` to set `indexing.enabled` to `true` and add `"codex"` to the
`harnesses` array as needed. The file uses JSON syntax despite its extension.

### 2. Build the local index

```powershell
aikit index
```

This builds the searchable index of allowed files. Run it again after source
changes. AIKIT reuses entries for unchanged files. To try the index, run
`aikit search "request handler" --limit 10` or `aikit symbol Handler`, using a
query or exact symbol name from your project.

### 3. Install the skill and connect your agent tool

Preview the project configuration changes.

```powershell
aikit sync --dry-run
```

Review the plan and any installed package instructions, then apply it.

```powershell
aikit sync --trust
```

Sync installs the shared `aikit-research` project skill. It also connects MCP
and installs a custom Research Agent for selected Codex and Claude Code
integrations. For OpenCode, Cursor, or another host, set up the local AIKIT MCP
server once in that tool. Configure it as a stdio server using the command `aikit mcp serve` from the project root.

### 4. Start research in the chat you already have open

Send this message in your local project chat.

```text
Run AIKIT research
```

You can also select `aikit-research` from your tool's skill picker when available.
The skill asks your current agent to delegate to one local subagent. The worker
checks the index, creates `.aikit/knowledge/manifest.yaml` if it is missing,
and registers and validates its proposed Markdown documents. It returns the
results to the same chat. Your tool needs local subagent support with MCP and
project write access. The session's model provider may receive selected source
during research.

For other skills you've installed with `aikit skill add`, `aikit sync --trust` exports
the effective project and global skills to the project's `.agents/skills/`
directory. Select the exported skill by name in Codex to use it directly. In
Codex CLI or the IDE extension, type `$` or use `/skills`. See
[Codex skill discovery](https://learn.chatgpt.com/docs/build-skills#how-chatgpt-and-codex-use-skills).

### 5. Review the proposed knowledge

Review the Markdown files and the manifest diff. For each entry you want to
approve, run `aikit knowledge show <id>` with its manifest ID. If the document
and citations look right, use the returned review digest in
`aikit knowledge approve <id> --reviewed-digest <digest>`.

## Implementation status

Implemented: `--version`, help, `doctor`, `init`, `status`, scoped agent/skill
add/list/remove, explicit package update, opt-in incremental SQLite/FTS5 indexing,
TypeScript/C# symbols, authorized search, read-only MCP stdio and owned project
sync for Codex/Claude, scoped knowledge with explicit approval, a portable
in-chat research skill, opt-in local semantic retrieval and audited
last-resort fallback.
Signed updates require a production signing key. Source builds have no production signing key configured.

## Build from source

Requires Go 1.27.4. Native indexing builds need a C compiler; other CLI commands
and read-only index queries can use a CGO-disabled build. SQLite is embedded;
no separate database installation or model is required.

```text
go test ./...
go build -o aikit.exe ./cmd/aikit
go run ./cmd/aikit --version
go run ./cmd/aikit --help
go run ./cmd/aikit doctor
```

Use `-o aikit` on Linux/macOS. `doctor` reports implementation status without
scanning. Indexing requires explicit opt-in.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

AIKIT is licensed under [Apache-2.0](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md) for dependencies.

## External inspirations (not code dependencies)

- [AWS AI-DLC workflows](https://github.com/awslabs/aidlc-workflows) — harness-neutral methodologies.
- [Yuval Yeret Portfolio PDLC](https://github.com/yyeret/portfolio-pdlc) — evidence-based workflows.
- [Matt Pocock skills](https://github.com/mattpocock/skills) — composable skills, optional third-party packs.

Do not automatically vendor or copy external repositories. Verify source, license and compatibility before any incorporation.
