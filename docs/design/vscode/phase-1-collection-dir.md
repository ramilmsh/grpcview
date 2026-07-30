# Phase 1 — the collection is a directory

**Prereqs:** none. **Unblocks:** everything (the extension must be able to say "the
collection is *this* workspace folder"). See [`README.md`](./README.md) for the track
overview.

## Goal

Make the collection the directory grpcview is invoked in, with a `--dir` override.

`docs/design/storage.md` already states the intent — "a workspace = **any**
directory the user opens" — but it was never wired: `service/workspace/workspace.go:52`
hardcodes `store.New(filepath.Join(configDir, ".grpcview"))`, i.e. the user config
dir.

## Changes

- **`service/service.go:35`** — add `--dir` (default `.`) alongside the existing
  `--port`.
- **`service/workspace/workspace.go:52`** — `store.New(filepath.Join(configDir,
  ".grpcview"))` → `store.New(dir)`.
- **Collapse the workspace-*name* layer.** `store.Open(ctx, name)` resolves a named
  collection under a base dir, but there is now exactly one collection per process.
  Drop the parameter, and with it `workspace_name` on every RPC in
  `proto/grpcview/v1/service.proto` and the UI's `WORKSPACE_NAME` constant. Large
  mechanical diff across every handler and every `connect-query` call site, no
  compatibility concerns at this stage.
- **`.gitignore` must append when the file already exists.** `ensureGitignore`
  (`fs.go:778`) returns early if a `.gitignore` is present, so it does *not* clobber
  an existing one — the bug is the opposite: pointed at an existing repo it silently
  never adds grpcview's `.grpcview/` line, leaving local state untracked-but-unignored.
  Make it read-modify-write, appending only the lines not already present, and
  idempotent across runs.

- **`store.New(base)` roots collections at `<base>/<name>`** (`store.go:127`,
  `Open`), so passing `dir` as the base would put the collection in
  `<dir>/<name>`, not `<dir>`. Root the single collection **at** the base and cache
  it under a fixed key rather than per name — otherwise two names map to one
  directory through two `Collection` handles with independent mutexes, defeating the
  write serialization. `name` stays only as the display field `writeCollection`
  reads (`fs.go:773`). This is the minimum needed before the wire cleanup below.

## Watch out

A collection root creates `grpcview.json`, `tree/`, `scripts/` and `.grpcview/`.
Do **not** point it at this repo's root — those four would scatter among the project
files. Use `grpcview --dir requests` here, and make that the documented pattern for
using grpcview inside an existing project.

## Verify

- `bazel test //service/...`
- Run the binary in a scratch dir: confirm the four entries are created there and
  nothing lands in `~/.config`.
- Run it in a dir that already has a `.gitignore` with unrelated content: confirm the
  existing lines survive and grpcview's are appended once (idempotent on a second
  run).
- Browser: the workspace loads and a request invokes, with the `workspace_name` field
  gone from the wire.

## Open questions

- Delete `workspace_name` outright, or keep it as an ignored field to shrink the diff?
  Project stage (`AGENTS.md` — "backwards compatibility is IRRELEVANT") says delete.
- Should `--dir` create the collection if absent, or require an explicit
  `grpcview init`? Auto-create is simpler and matches "any directory you open."
