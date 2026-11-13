<div align="center">
  <img src="./conmesh.svg" alt="ConMesh" width="740">
  <p><strong>The Context Intelligence Layer for AI Agents</strong></p>
  <p>Local-first • Agent-agnostic • Codebase-aware</p>
</div>

**Give coding agents context about the codebase you already have.** ConMesh builds an opt-in local index for code search and symbol lookup. Its Research Agent uses repository evidence to draft scoped Markdown guidance about architecture, conventions, and examples for review. This is designed for established codebases, including enterprise projects where teams want control over what gets indexed and which guidance gets approved.

ConMesh also manages agents and skills and can expose its index over MCP to Claude Code, Codex, and other compatible hosts.

Browse the [predefined agents](agents/) for Angular, .NET, and repository research.
They ship inside ConMesh: pick one under **Install** in the menu, or run
`conmesh agent list --available` and `conmesh agent add builtin:<id> --trust`.

## Fast install with your coding agent

Paste this prompt into a coding-agent session with terminal access.

```text
Set up ConMesh using the steering guide at
https://raw.githubusercontent.com/AntonioIliev19/conmesh/main/INSTALL.md
Read it first. Ask me whether I want CLI-only installation or setup for one or more repositories,
and which AI coding tools I use so you can connect the ConMesh MCP server to each of them.
Show the plan and get my confirmation before installing or changing anything.
Ask separately whether I want to index any repository, and confirm each exact path first.
```

The [installation steering guide](INSTALL.md) also works as a manual checklist.

## Install ConMesh

Download the Windows x64 ZIP from [GitHub Releases](https://github.com/AntonioIliev19/conmesh/releases/latest), extract it, and run `install.cmd`. Open a new terminal and run `conmesh --version`. The installer uses your user account and adds ConMesh to your user PATH.

For a source build or a trusted executable you already have, use the steps below.

In your own PowerShell window, from this repository with `conmesh.exe` in its root:

```powershell
Test-Path .\conmesh.exe
.\scripts\install.cmd .\conmesh.exe
```

The installer copies the binary into your per-user ConMesh directory and adds that
directory to your user PATH. If you run the installer from another directory,
replace `C:\path\to\ConMesh` with the full path to your ConMesh checkout.

```powershell
& 'C:\path\to\ConMesh\scripts\install.cmd' 'C:\path\to\ConMesh\conmesh.exe'
```

Close and reopen the terminal application, including a parent editor such as VS
Code if it launches your terminals. Then verify from any directory:

```powershell
conmesh --version
conmesh --help
```

Run `conmesh` or `conmesh menu` in a terminal. The menu shows the current workspace
and version, then groups its items under **Packages** (Install, Update, List,
Uninstall; see [Third-party agents and skills](#third-party-agents-and-skills)), **Project** (Workspace, Doctor) and **ConMesh** (Help, Self-update,
Quit). **Install** lists the predefined agents first, then accepts any local
package folder or HTTPS Git URL. **List** shows installed agents and skills under
their own headings. **Update** updates an installed agent or skill; **Self-update**
checks for and installs a verified ConMesh release. The "same as" line under the menu shows
the equivalent command for the highlighted item.

| Key | Action |
|---|---|
| Up / Down | Move the selection |
| Enter | Select |
| Esc or Backspace | Go back (at the top level, leave the menu) |
| `q` | Quit the menu |
| Ctrl-C | Exit immediately |

Opening the menu makes no network request unless `CONMESH_AUTO_UPDATE_CHECK=1`
is set, in which case it checks for a release in the background at most once a
day. When stdin is not a terminal (for
example `echo x | conmesh`), or with `--no-input`, `conmesh` prints the command
reference instead of opening the menu.

Scripting options:

- `--json` gives machine-readable output for `doctor`, `agent list` and
  `skill list`. `status` always prints JSON and also accepts `--json`.
- `--no-input` never prompts. `conmesh --no-input menu` fails and names the flag;
  a mistyped command only prints its suggestion.
- Colour is off when `NO_COLOR` is set, `TERM=dumb`, `--no-color` is passed, or the
  output is not a terminal. Each of stdout and stderr is checked separately.
- A mistyped command such as `conmesh serch` suggests the closest real command. In
  a terminal it asks `[y/N]` (default No); otherwise it only prints the suggestion
  and exits with status 2.

If command
discovery fails, check that the installed file exists and run it directly:

```powershell
$conmeshExe = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'ConMesh\bin\conmesh.exe'
Test-Path $conmeshExe
& $conmeshExe --version
Get-Command conmesh -All
```

If `Test-Path` returns `False`, install a trusted binary first. Restarting parent
applications refreshes inherited PATH; it cannot fix a missing executable.

## Setup for your project

Follow these steps to build a local index and run the Research Agent in your
existing project chat. The examples use PowerShell.

### 1. Open your project and initialize ConMesh

Replace the example path with the root of your own project.

```powershell
Set-Location 'C:\path\to\your-project'
conmesh init --harness codex --index
conmesh status
```

`init` creates `.conmesh/config.yaml`. `--harness codex` selects Codex and `--index`
enables indexing in the settings. Use `--harness claude` for Claude Code or
`--harness claude,codex` for both. Add `cursor` or `copilot` to the list to also
publish approved project knowledge as Cursor rules or GitHub Copilot
instructions (see step 6); ConMesh does not configure MCP for those two. For
OpenCode or another local host, use `conmesh init --index`. If you only want package management, use `conmesh init`
and stop here. `conmesh doctor` checks available runtime features.

If the project is already initialized, `init` preserves its settings. Edit
`.conmesh/config.yaml` to set `indexing.enabled` to `true` and add `"codex"` to the
`harnesses` array as needed. The file uses JSON syntax despite its extension.

### 2. Build the local index

```powershell
conmesh index
```

This builds the searchable index of allowed files. Run it again after source
changes. ConMesh reuses entries for unchanged files. To try the index, run
`conmesh search "request handler" --limit 10` or `conmesh symbol Handler`, using a
query or exact symbol name from your project. Search also matches the parts of
camelCase and snake_case names, so `handler` finds `requestHandler`.

A file with syntax errors, or one over the parser limits, is still indexed as
plain text without symbols, after the same ignore and secret checks as every
other file. The summary line reports how many, and why any file was skipped.

The index format is upgraded in place the first time a newer ConMesh writes to
it, and all files are re-extracted once. An older ConMesh cannot read the
upgraded index. If you go back to an older version, for example with
`conmesh update --rollback`, delete `.conmesh/state/index.sqlite` and its
`-wal` and `-shm` files, then run `conmesh index` again.

### 3. Install the skill and connect your agent tool

Preview the project configuration changes.

```powershell
conmesh sync --dry-run
```

Review the plan and any installed package instructions, then apply it.

```powershell
conmesh sync --trust
```

Sync installs the shared `conmesh-research` project skill. It also connects MCP
and installs a custom Research Agent for selected Codex and Claude Code
integrations. For OpenCode, Cursor, or another host, set up the local ConMesh MCP
server once in that tool. Configure it as a stdio server using the command `conmesh mcp serve` from the project root.

### 4. Start research in the chat you already have open

Send this message in your local project chat.

```text
Run ConMesh research
```

You can also select `conmesh-research` from your tool's skill picker when available.
The skill asks your current agent to delegate to one local subagent. The worker
checks the index, creates `.conmesh/knowledge/manifest.yaml` if it is missing,
and registers and validates its proposed Markdown documents. It returns the
results to the same chat. Your tool needs local subagent support with MCP and
project write access. The session's model provider may receive selected source
during research.

For other skills you've installed with `conmesh skill add`, `conmesh sync --trust` exports
the effective project and global skills to the project's `.agents/skills/`
directory. Select the exported skill by name in Codex to use it directly. In
Codex CLI or the IDE extension, type `$` or use `/skills`. See
[Codex skill discovery](https://learn.chatgpt.com/docs/build-skills#how-chatgpt-and-codex-use-skills).

### 5. Review the proposed knowledge

Review the Markdown files and the manifest diff. For each entry you want to
approve, run `conmesh knowledge show <id>` with its manifest ID. If the document
and citations look right, use the returned review digest in
`conmesh knowledge approve <id> --reviewed-digest <digest>`.

### 6. Publish the knowledge to every session

Run `conmesh sync --dry-run`, review the plan, then `conmesh sync --trust`. Each
approved guidance entry is published in the format of every tool listed in
`harnesses`, so the tool loads it for the matching code by itself:

| Tool | Published as |
| --- | --- |
| Claude Code | Skill in `.claude/skills/conmesh-kb-<id>/SKILL.md` |
| Codex and other tools that read `.agents/skills` | Skill in `.agents/skills/conmesh-kb-<id>/SKILL.md` |
| Cursor | Rule in `.cursor/rules/conmesh-kb-<id>.mdc`, attached for the entry's scopes |
| GitHub Copilot | Instructions in `.github/instructions/conmesh-kb-<id>.instructions.md`, applied to the entry's scopes |

Research also proposes project agents (entries with `kind: agent`), such as a
feature developer for each major part of the code. Once approved, they are
published as custom agents for Claude Code (`.claude/agents/conmesh-agent-<id>.md`)
and Codex (`.codex/agents/conmesh-agent-<id>.toml`), each pointing to the
published guidance for its part of the code. Cursor and Copilot receive the
guidance only.

Guidance and agents are also listed in a marked "Project knowledge" section of
`CLAUDE.md` and `AGENTS.md`. Sessions that know nothing about ConMesh, with any
model, then load the guidance on their own when they work in the matching part
of the code. Commit these files to share them with your team.

- **Your instructions stay yours:** in a `CLAUDE.md` or `AGENTS.md` you wrote,
  ConMesh changes only the section between its `BEGIN`/`END CONMESH PROJECT
  KNOWLEDGE` markers. It refuses to overwrite that section if someone edited it.
- **Only current, reviewed guidance is published:** proposed entries are not
  published. When a cited file changes, the entry is withdrawn at the next sync
  until you review and approve it again. `sync --dry-run` lists every entry it
  left out and why.
- **Edit the source, not the copy:** change `.conmesh/knowledge/`, approve it,
  and sync again. Edits to the published skill files are overwritten.

## Third-party agents and skills

ConMesh can install agents and skills from three kinds of source: the predefined
packages shipped with ConMesh (`builtin:<id>`), a local folder, or any HTTPS Git
repository. ConMesh verifies what it copies (manifest, license, file limits, no
secrets, no executable hooks), but it does not review what a third-party
package tells your agent to do.

**Packages from a local folder or a Git URL are provided by their authors, not
by the ConMesh project.** Their authors are responsible for their content,
license and behaviour. Before you pass `--trust`, read the package's
instructions and license yourself, and install only packages you trust. The
ConMesh project and its contributors accept no responsibility for third-party
packages or for anything an agent does while following them.

## Implementation status

Implemented: `--version`, help, `doctor`, `init`, `status`, scoped agent/skill
add/list/remove, explicit package update, opt-in incremental SQLite/FTS5 indexing,
TypeScript/C# symbols, authorized search, read-only MCP stdio and owned project
sync for Codex/Claude, scoped knowledge with explicit approval, a portable
in-chat research skill, opt-in local semantic retrieval and audited
last-resort fallback.
GitHub release binaries include the trusted public key for signed updates. Source builds require a one-time install of a release binary to use self-update.

## Build from source

Requires Go 1.27.4. Native indexing builds need a C compiler; other CLI commands
and read-only index queries can use a CGO-disabled build. SQLite is embedded;
no separate database installation or model is required.

```text
go test ./...
go build -o conmesh.exe ./cmd/conmesh
go run ./cmd/conmesh --version
go run ./cmd/conmesh --help
go run ./cmd/conmesh doctor
```

Use `-o conmesh` on Linux/macOS. `doctor` reports implementation status without
scanning. Indexing requires explicit opt-in.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

ConMesh is licensed under [Apache-2.0](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md) for dependencies.

## External inspirations (not code dependencies)

- [AWS AI-DLC workflows](https://github.com/awslabs/aidlc-workflows) — harness-neutral methodologies.
- [Yuval Yeret Portfolio PDLC](https://github.com/yyeret/portfolio-pdlc) — evidence-based workflows.
- [Matt Pocock skills](https://github.com/mattpocock/skills) — composable skills, optional third-party packs.

Do not automatically vendor or copy external repositories. Verify source, license and compatibility before any incorporation.


## Release versions

Release tags use `vMAJOR.MINOR.PATCH`. The first stable release is `v1.0.0`.
Use `v1.0.1` for compatible fixes and `v1.1.0` for compatible features.
Signed release builds can check for updates from the menu. Set
`CONMESH_AUTO_UPDATE_CHECK=1` to opt into background checks.
