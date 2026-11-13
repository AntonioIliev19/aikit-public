# ConMesh installation guide for coding agents

Use this guide when the user asks you to install or configure ConMesh. Keep confirmations in the user's chat. When sharing commands, use the user's shell and quote paths as data. Run project commands with each approved repository as the working directory.

## Plan and confirm installation

1. Detect the OS and architecture and check whether `conmesh --version` already works. Ask whether the user wants the CLI only or wants to configure repositories as well. For repository setup, suggest the current Git root when available and ask for the exact paths for any additional repositories. Confirm the chosen roots with the user.
2. Explain what you intend to download, where it will go, and whether the installer changes the user's PATH. Wait for confirmation before downloading or installing.
3. On Windows x64, use the ZIP and `SHA256SUMS` from the same [stable GitHub release](https://github.com/AntonioIliev19/conmesh/releases/latest). Download them into a fresh temporary directory. Use `Get-FileHash -Algorithm SHA256 -LiteralPath <zip>` and compare it with the checksum for the exact ZIP filename before extracting. Stop on a mismatch. Run the extracted `install.cmd` from its directory. The installer writes to the user's ConMesh directory and updates their user PATH. If the current shell cannot resolve `conmesh`, verify via `Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'ConMesh/bin/conmesh.exe'` or start a new terminal.
4. For platforms without a matching release, offer the README's [source build](README.md#build-from-source). Ask before installing prerequisites or writing the executable to a user bin directory. A source build without the compiled release verification key can run the CLI and lacks signed self-update support. If you lack terminal access, give the user the corresponding manual steps.
5. Verify `conmesh --version` and `conmesh doctor`. For indexing, check that Doctor reports native Tree-sitter support.

## Configure the approved repositories

1. For each confirmed root, ask if the user wants indexing, which of the confirmed roots to index, and whether they want Codex or Claude Code integration. CLI-only installation ends after verification.
2. Show the proposed commands and get confirmation before creating `.conmesh/config.yaml` or changing an existing configuration. Initialize with `conmesh init` for indexing off, or `conmesh init --index` when explicitly approved. Include `--harness codex`, `--harness claude`, or `--harness claude,codex` when the user chooses a supported integration. Existing settings are preserved by init. If indexing is off in an existing config and the user opts in, show and confirm the targeted settings edit. Follow the [migration instructions](MIGRATION.md) if legacy state is detected.
3. Before indexing, point out that ConMesh applies its built-in exclusions, `.gitignore`, and `.conmeshignore`. Run `conmesh index` only in the confirmed roots with indexing enabled. Model downloads, semantic indexing, and research runs each require a separate user request.
4. For harness integration, run `conmesh sync --dry-run`, present the proposed file changes, and ask before applying `conmesh sync --trust`. For other MCP-capable hosts, explain how to configure `conmesh mcp serve --project <absolute-root>` in the host and confirm before modifying its settings.
5. Verify `conmesh status` per configured repository. Summarize which directories were configured and indexed. Explain that a harness model provider may receive selected code if the user later runs research.
