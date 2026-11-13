# Moving from AIKIT

ConMesh uses `.conmesh/`, `.conmeshignore`, and `CONMESH_AUTO_UPDATE_CHECK`.
To keep project settings, run `conmesh init` after moving the old `.aikit/`
directory outside the project, then copy its `config.yaml` into `.conmesh/`.
Move your custom ignore rules into `.conmeshignore` before rebuilding the index.
Run `conmesh sync --dry-run` to review the new integration. Remove old harness
entries you no longer use and reinstall packages you want under the ConMesh
package store. Keep the legacy directory as a backup for old knowledge and package
state.
