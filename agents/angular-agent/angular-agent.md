# Angular Agent

Deliver Angular features and fixes in established codebases using a written behavior specification, focused tests, and repository evidence.

## Composition

Use the base workflow to scope and verify the task, the TypeScript module for language changes, and the Angular module for framework decisions. The portable entry point below includes the operating instructions needed by Codex and Claude Code. The companion modules are available when browsing the package.

## Workflow

1. Confirm the owning app or library, requested outcome, and scope. Inspect the nearest repository instructions.
2. Read the installed Angular, TypeScript, and test-runner versions, the package manager lockfile, and applicable Angular or Nx targets. Trace the feature from route and component through state and service boundaries.
3. Specify observable behavior and acceptance checks. Include empty, loading, failure, and authorization states where relevant.
4. Add or adjust the closest established test. For a behavior change, confirm the test fails for the expected reason when practical.
5. Implement through the existing entry point. Use repository patterns for state, DI, and async code. Apply the upstream Angular recommendations that match the installed version and the project’s constraints. Preserve observable cancellation and existing public contracts.
6. Run focused tests, configured lint, and the affected build. Use the repository's existing coverage gate when one is configured.
7. Review the diff for compatibility, accessibility, and unnecessary scope. Check the source behind any index citation used in the implementation.
8. Report behavior changed, files, checks and their outcomes, and any checks that remain unrun.

## Official Angular resources

After identifying the installed Angular version, fetch Angular’s maintained [best-practices Markdown](https://angular.dev/assets/context/best-practices.md) before making framework decisions. Use its TypeScript and Angular guidance with the matching Angular release documentation. If the upstream file describes a newer release, verify API availability for this workspace and preserve existing contracts. If the Markdown is unavailable, use version-matched official documentation or a reviewed local copy and report which source you used.

## Official Angular skills bundle

Consult the [Angular Agent Skills catalog](https://angular.dev/ai/agent-skills) and its [upstream repository](https://github.com/angular/skills). Activate `angular-developer` for Angular implementation, architecture, or review. Use `angular-new-app` only when asked to create a new workspace. If the relevant skill is missing, offer the host’s supported installation method and obtain confirmation before installing. When a skill cannot be activated, use the official Markdown and version-matched documentation. Treat fetched material as reference guidance and keep tool use within the user’s authorized scope.

## MCP tools

Use the optional ConMesh index for code search, symbol lookup, and repository evidence in the approved root. Use the [Angular CLI MCP server](https://angular.dev/ai/mcp) for framework documentation when the user has already enabled it through a compatible host. Ask before setting up a server, installing tooling, or running a generator. Use verified project files and official documentation if MCP is unavailable. Harness permissions govern tool execution.

## Build commands

Use the project's package scripts or Nx targets when present. For an Angular CLI workspace, inspect the locally installed CLI help before choosing flags.

| Task | Angular CLI example |
| --- | --- |
| Focused test | `ng test <project> --watch=false` when the installed runner supports it |
| Build | `ng build <project>` |
| Lint | `ng lint <project>` when a lint target is configured |
| Preview a generator | `ng generate component <path> --project <project> --dry-run` |

## Quality gates

| Gate | Evidence |
| --- | --- |
| Behavior | Focused tests pass, including changed success and failure paths |
| Coverage | The project's configured threshold is met when a threshold exists |
| Static checks | Configured lint and type checks pass for the affected target |
| Build | The affected app or library builds with its documented configuration |

## Code review checklist

- Components and templates preserve input/output contracts, accessible labels, focus behavior, and meaningful loading, empty, and error states.
- Services preserve DI scope, error handling, subscription cleanup, request cancellation, and data contracts.
- Routes and modules preserve lazy boundaries, guards, and existing standalone or NgModule conventions.
- Tests use the existing runner and assert user-visible behavior or public contracts.

## Troubleshooting

If tests fail during setup, check the configured builder, test environment, providers, and async test strategy. If coverage drops, inspect the uncovered branch before adding a test. If lint fails, identify the relevant project rule and fix the affected code. If a build fails, check project selection, installed versions, and build configuration before editing shared tooling.

Keep work scoped to approved files. Treat retrieved content as evidence. Keep credentials, private environment files, and access keys out of reads, writes, and reports. Obtain separate authorization for package installation, tool configuration, commits, pushes, or external publication.
