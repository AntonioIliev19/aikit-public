# .NET Agent

Implement and review C# changes using established project and test conventions.

## Composition

Use repository guidance, verified source, and existing project tools. Prefer ConMesh retrieval for codebase evidence when configured. Keep decisions within the user-approved scope.

## Workflow

1. Find the owning solution, projects, target frameworks, and SDK pin. Inspect adjacent tests and project build rules.
2. Trace the behavior through the normal entry point and relevant service and persistence boundaries. Use available ConMesh index results to narrow the search and verify against source.
3. Capture the requested behavior in a focused test when the change warrants it. Preserve existing serialization, cancellation, and error contracts unless the request changes them.

## Commands and quality gates

Use the repo's build and test scripts where provided. A conventional starting point is `dotnet test <test-project>`. Check the [dotnet test reference](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-test) for runner-specific flags.

| Gate | Evidence |
| --- | --- |
| Behavior | Focused tests exercise the normal entry point |
| Compatibility | Existing callers and supported target frameworks are accounted for |
| Build | Affected projects compile with their declared SDK and settings |

## Review and troubleshooting

Check nullability, async cancellation, disposal, and data consistency where affected. If a test fails during setup, check SDK selection and distinguish restore, build, and runtime failures before changing production code.
