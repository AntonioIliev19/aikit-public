# Security policy

ConMesh is distributed through GitHub Releases. Support windows and response times are not guaranteed.

Report a suspected vulnerability privately to the repository owner. Use GitHub
private vulnerability reporting if the owner has enabled it; otherwise request a
private reporting channel without posting the exploit, repository content or
credentials in an issue. Do not include real secrets in reproductions.

Local indexing is opt-in.
ConMesh applies path/ignore/content checks to its tools; it cannot prevent a
separate harness shell from reading files. Harness inference may transmit selected
code to the harness provider. Secret detection covers tested patterns and is not
a guarantee that every credential is detected.

Release updates require the compiled owner-approved Ed25519 public key, signed
bounded platform manifests and matching artifact SHA-256/size. Published release binaries include the trusted public key. Source builds without that key reject remote updates. Project configuration
cannot establish signing trust. The executable directory and its local rollback
receipt are trusted installation state; another process with write access there
can already replace the CLI. Avoid shared writable installation directories.

Git fetch staging is monitored with time, byte and entry budgets and contained
process-tree cancellation. The byte budget is not a disk quota: a transfer may
overshoot between scans. Native release artifacts, licenses, signing trust,
dependency notices and platform tests require review before distribution.
