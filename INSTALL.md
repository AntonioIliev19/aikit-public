# ConMesh installation guide for coding agents

Use this guide when the user asks you to install or configure ConMesh. Keep confirmations in the user's chat. When sharing commands, use the user's shell and quote paths as data. Run project commands with each approved repository as the working directory.

## Ask first

Before planning, ask these together in one message, and wait for the answers:

1. **Scope:** CLI only, or setup for one or more repositories? For repositories, suggest the current Git root when available and ask for the exact paths of any others.
2. **AI coding tools:** which tools the user works in for these repositories: Claude Code, Codex, Cursor, VS Code with GitHub Copilot, OpenCode, Windsurf, or another MCP-capable tool. Explain that ConMesh connects its MCP server (indexed code search, symbols and project knowledge) to each one, and publishes approved project knowledge in the formats those tools load by themselves.
3. **Indexing:** whether to index any repository. Ask this separately and confirm each exact path; the MCP search tools need an index to return results.

CLI-only installation skips questions 2 and 3 and ends after verification.

## Plan and confirm installation

1. Detect the OS and architecture and check whether `conmesh --version` already works. Confirm the chosen repository roots and tools with the user.
2. Explain what you intend to download, where it will go, and whether the installer changes the user's PATH. Wait for confirmation before downloading or installing.
3. On Windows x64, use the ZIP and `SHA256SUMS` from the same [stable GitHub release](https://github.com/AntonioIliev19/conmesh/releases/latest). Download them into a fresh temporary directory. Use `Get-FileHash -Algorithm SHA256 -LiteralPath <zip>` and compare it with the checksum for the exact ZIP filename before extracting. Stop on a mismatch. Run the extracted `install.cmd` from its directory. The installer writes to the user's ConMesh directory and updates their user PATH. If the current shell cannot resolve `conmesh`, verify via `Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'ConMesh/bin/conmesh.exe'` or start a new terminal.
4. For platforms without a matching release, offer the README's [source build](README.md#build-from-source). Ask before installing prerequisites or writing the executable to a user bin directory. A source build without the compiled release verification key can run the CLI and lacks signed self-update support. If you lack terminal access, give the user the corresponding manual steps.
5. Verify `conmesh --version` and `conmesh doctor`. For indexing, check that Doctor reports native Tree-sitter support.

## Configure the approved repositories

1. Show the proposed commands and get confirmation before creating `.conmesh/config.yaml` or changing an existing configuration. Initialize with `conmesh init` for indexing off, or `conmesh init --index` when explicitly approved. Add `--harness` with the user's tools that ConMesh integrates directly, comma-separated: `claude`, `codex`, `cursor`, `copilot` (for example `--harness claude,cursor`). Existing settings are preserved by init; to add tools later, show and confirm the edit to the `harnesses` array in `.conmesh/config.yaml`. If indexing is off in an existing config and the user opts in, show and confirm the targeted settings edit. Follow the [migration instructions](MIGRATION.md) if legacy state is detected.
2. Before indexing, point out that ConMesh applies its built-in exclusions, `.gitignore`, and `.conmeshignore`. Run `conmesh index` only in the confirmed roots with indexing enabled. Model downloads, semantic indexing, and research runs each require a separate user request.
3. Run `conmesh sync --dry-run`, present the proposed file changes, and ask before applying `conmesh sync --trust`. For Claude Code and Codex, sync also connects the MCP server (`.mcp.json` and `.codex/config.toml`). For Cursor and Copilot it publishes approved knowledge only; connect their MCP server in the next section.
4. Connect the MCP server for every chosen tool that sync does not configure, following "Connect the MCP server" below.
5. Verify `conmesh status` per configured repository. Summarize which directories were configured and indexed, and which tools are connected to the MCP server. Tell the user to restart or reload each tool so it starts the server, and to approve the server if the tool asks. Explain that a harness model provider may receive selected code if the user later runs research.

## Connect the MCP server

Every tool runs the same local stdio server, one per repository:

- **Command:** the absolute path of the ConMesh executable. Resolve it with `(Get-Command conmesh).Source` in PowerShell or `command -v conmesh` in a POSIX shell. Use the absolute path because desktop apps often do not see an updated PATH.
- **Arguments:** `mcp`, `serve`, `--project`, and the repository's absolute root path.

Rules for every tool:

- Prefer the tool's project-level configuration file in the repository root, so the server is scoped to that repository. Use a user-level file only when the tool has no project-level option, and then name the server `conmesh-<repository folder name>` so several repositories can coexist.
- If the file exists, read it, add only the `conmesh` entry and keep every other setting. If a `conmesh` entry already exists and differs, show both and ask which to keep. Write valid JSON or TOML without comments.
- Show the exact file path and the final content, and wait for confirmation before writing. Never add credentials or environment secrets.
- Formats change between tool versions. If the tool's current documentation differs from the shape below, follow the documentation and tell the user.

| Tool | File | Entry |
| --- | --- | --- |
| Claude Code | `.mcp.json` (written by `conmesh sync --trust`) | `"mcpServers": { "conmesh": { "type": "stdio", "command": "<exe>", "args": ["mcp", "serve", "--project", "<root>"] } }` |
| Codex | `.codex/config.toml` (written by `conmesh sync --trust`) | `[mcp_servers.conmesh]` with `command = "<exe>"`, `args = ["mcp", "serve", "--project", "<root>"]` and `cwd = "<root>"` |
| Cursor | `.cursor/mcp.json` | `"mcpServers": { "conmesh": { "command": "<exe>", "args": ["mcp", "serve", "--project", "<root>"] } }` |
| VS Code with GitHub Copilot | `.vscode/mcp.json` | `"servers": { "conmesh": { "type": "stdio", "command": "<exe>", "args": ["mcp", "serve", "--project", "<root>"] } }` |
| OpenCode | `opencode.json` | `"mcp": { "conmesh": { "type": "local", "command": ["<exe>", "mcp", "serve", "--project", "<root>"], "enabled": true } }` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json` (user-level) | `"mcpServers": { "conmesh-<folder>": { "command": "<exe>", "args": ["mcp", "serve", "--project", "<root>"] } }` |

For any other MCP-capable tool, configure a stdio server with the same command and arguments in its settings, after confirming the change with the user.

In JSON, escape Windows backslashes (`C:\\repo`) or use forward slashes (`C:/repo`). Both work for ConMesh.

After writing, check the connection where the tool allows it, for example `claude mcp list` for Claude Code, or the tool's MCP settings panel, and report each server's status. If a server fails to start, run the same command in a terminal from the repository root (`conmesh mcp serve --project <root>`); it should wait for input without printing an error. Stop it with Ctrl-C.
