# Predefined agents

| Agent | Use it for |
| --- | --- |
| [Angular](angular-agent/AGENT.md) | Angular changes and review in an existing workspace |
| [.NET](dotnet-agent/AGENT.md) | C# implementation and review |
| [Repository research](repository-research/AGENT.md) | Scoped, evidence-backed Markdown guidance |

From an initialized project, install one package using its directory in a local ConMesh checkout.

```text
conmesh agent add "<conmesh-checkout>/agents/angular-agent" --trust --project
conmesh agent list --project
conmesh sync --dry-run
```

Review the agent and the sync preview before running the corresponding trusted operation. Apply an approved sync with `conmesh sync --trust`. Codex and Claude Code receive the Markdown playbook through their project agent configuration.

Each package includes a ConMesh manifest, a catalog metadata file, a JSON descriptor pointing to `AGENT.md`, and its license. The manifest and Markdown are used by the current installer and sync command. The metadata and descriptor document the catalog entry. Harness permissions control tool access.

For framework and language agents, prefer guidance and task-specific skills maintained by the upstream project and verify applicability against the installed version. The Angular package links to the official [best-practices Markdown](https://angular.dev/assets/context/best-practices.md) and [skills bundle](https://angular.dev/ai/agent-skills). Review and approve any skill installation through the target harness.
