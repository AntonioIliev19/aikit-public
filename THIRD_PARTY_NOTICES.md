# Third-party notices

ConMesh is licensed under [Apache-2.0](LICENSE). Third-party components retain their upstream licenses and notices.

The pinned Go module graph and complete collected license texts are available in:

- [Dependency inventory](results/dependencies-phase5/dependencies.json)
- [CycloneDX SBOM](results/dependencies-phase5/sbom.cdx.json)
- [Full module dependency graph](results/dependencies-phase5/go-module-graph.txt)
- [Preserved license and notice texts](results/dependencies-phase5/THIRD_PARTY_LICENSE_TEXTS.md)

The inventory includes the entire module graph, including build/test-only modules;
it is broader than the set linked into an individual binary. Per-artifact build
information records the linked modules separately. Module `h1` values are Go module
content checksums, not hashes of native release artifacts.

Native components include Tree-sitter and TypeScript/C# grammars, modernc SQLite
and its libc support. Nested native notices are collected along with top-level
module licenses. The preserved text also includes Go toolchain LICENSE/PATENTS
and its vendored library notices. Review every artifact's inventory after a
dependency or toolchain change; generated collection assists that review and does
not settle license compatibility on its own.

Regenerate in a **new** directory with:

```text
go run ./tools/release --inventory-only --out path-to-new-private-inventory
```

No embedding model weights, Matt Pocock skills or other external skill assets are
bundled by this release-preparation work. A user-supplied model or package has its
own separate license and provenance requirements.
