# The workspace and its collections

A workspace is a repository; collections are what's in it. Root owns no requests; a
collection is `grpcview.json` + `tree/` + `scripts/`.

- Root found by walking up: `--workspace` wins, else nearest `.git`, else cwd with a stderr
  warning (`service/wsroot.Discover`, the one place this happens).
- A collection is addressed by its path relative to root (`.` = root); id is disk location,
  never written to disk. Only `CreateCollection` creates one. `UpdateCollection` takes
  `name` (manifest write) and `new_collection` (a move — `os.Rename` + moving local state).
- Directory slug is identity, display name is data. Renaming writes `meta.Name`, leaves the
  directory alone (avoids git-history churn). A request directory holds `request.json` +
  `body.ts` + `metadata.ts`, moving together. **Scripts are the exception** — path is
  identity, renaming moves the file.
- TopBar picker switches collections via pure UI state — every query key is built from the
  active id, so switching triggers no reload.
- Local state (resolve caches, run history) lives outside the collection, under
  `service/wsroot.ConfigRoot()` (`GRPCVIEW_CONFIG_DIR`) — a collection directory is
  therefore 100% committed content.
- Discovery is declared-or-scanned: a non-empty `grpcview.work.json` `collections` list wins
  (globs allowed); otherwise the root is scanned for `grpcview.json`, pruning
  dot-dirs/`node_modules`/`bazel-*`/gitignored paths. **Not cached** — a mtime cache
  previously missed collections created below the root. `ListCollections` reads manifests
  only, never trees.

On-disk schema (`grpcview.store.v1`) is decoupled from wire (`grpcview.v1`); `convert.go`
bridges the two, so a storage-format change never touches the wire proto.
