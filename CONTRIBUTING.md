# Contributing

Keep changes bounded, preserve existing work, and add regression
tests for changed behavior. Discuss architecture changes with the maintainers. Do not introduce a mandatory JavaScript runtime, container, external
database, GPU or cloud model dependency.

Use the Go version pinned in go.mod. Native Tree-sitter builds require a
supported C compiler and CGO_ENABLED=1; users of prepared native binaries do not
need a compiler at runtime. Run:

```text
gofmt -w cmd internal tools
go test -timeout 180s ./...
go vet ./...
go build ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Also run CGO_ENABLED=0 checks; parser-dependent tests deliberately report missing
native capabilities. Linux CI runs race tests with CGO enabled. Do not report a
platform or tool gate as passed when it was not run. Windows local race linking
with the available Zig compiler previously failed; ordinary native Windows tests
are a separate gate.

Use original, synthetic, temporary fixtures; never commit actual credentials or
proprietary source. No test should invoke a real harness, approve repository
knowledge, download a model or write user harness settings implicitly.

Third-party code, native grammars, copied guidance and model weights require
license/provenance review and preserved notices. Do not contribute assets without
permission to distribute them.
