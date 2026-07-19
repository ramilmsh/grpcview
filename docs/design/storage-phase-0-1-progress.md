# Storage rewrite — Phase 0 + Phase 1 progress

Status: **complete and verified.** Implements Phases 0 and 1 of
[`storage.md`](./storage.md) §14. Branch: `feat/fs-storage` (not committed/pushed).

Goal delivered: today's storage (the whole `Workspace` proto serialized as one
opaque binary blob per workspace) is replaced by a git-versionable directory
tree of `protojson`/JSON files — **without** changing the proto, the Connect RPC
surface, or the React UI. The same 6 RPCs behave identically to the client.

---

## Phase 0 — cleanup (no behavior change)

- Removed `github.com/mattn/go-sqlite3` (it was opened at `/tmp/ws.db` but never
  used — dead code, and the repo's only cgo dependency):
  - `service/workspace/workspace.go`: the `db *sql.DB` field, `sql.Open`, the
    `database/sql` + `_ "…go-sqlite3"` imports.
  - `service/service.go`: the `_ "…go-sqlite3"` import.
  - `go.mod` require, `MODULE.bazel` `use_repo`, and the dep in both
    `service/BUILD.bazel` and `service/workspace/BUILD.bazel`.
  - Reconciled with `go mod tidy` + `bazel mod tidy` + `gazelle`.
- Renamed `package service` → `package workspace` in
  `service/workspace/workspace.go`; dropped the now-redundant import alias in
  `service/service.go`.
- **Result:** the binary no longer pulls the SQLite C dependency. `otool -L` on
  the dev binary shows only standard macOS system frameworks (present in any Go
  darwin binary); `nm`/`strings` show zero `sqlite3_` symbols.

## Phase 1 — blob → directory tree

New package `service/store/`:

| File | Responsibility |
|---|---|
| `store.go` | `Store` (multi-collection manager, name→root, cached per-name `Collection`), `Collection` (per-collection mutex), sentinel errors, `RequestPatch`. |
| `layout.go` | Slug generation/sanitization/uniqueness, reserved names (incl. Windows device names), ordering reconciliation. Pure, no I/O. |
| `codec.go` | protojson (un)marshal helpers, atomic temp-file+rename writes. |
| `fs.go` | Filesystem store: walk `tree/` to assemble the `Workspace`; mutations that touch only affected files + the parent's `items[]`; legacy-blob migration. |
| `store_test.go` | 9 tests — round-trip, slug uniqueness/reserved, ordering reconciliation, rename, cross-folder move (+ move-into-descendant guard), delete, migration, descriptor state, missing-collection. All pass. |

`service/workspace/workspace.go` rewritten as a thin adapter: every handler
delegates to the store and then **reloads the whole `Workspace`** so responses
keep the exact shape the client already expects. Removed the debug `fmt.Println`s
and the dead blob `load`/`save`/`find*` helpers.

Addressing (Phase 1, unchanged from before): `workspace_name` → a collection
directory at `os.UserConfigDir()/.grpcview/<name>` — the same base the old code
used. "Open any directory" is Phase 2.

### On-disk layout (verified end-to-end)

```
<UserConfigDir>/.grpcview/<name>/
  grpcview.json          # {schemaVersion, name, items:[root slugs], sources:[protojson DescriptorSource]}
  .gitignore             # generated; ignores .grpcview/
  tree/
    users/               # a folder (dir name = stable slug)
      folder.json        # {meta:{name:"Users"}, items:["get-user"]}
      get-user/          # a request
        request.json     # {meta:{name:"Get User"}, service, method, draftBody?, draftMetadata?}
  .grpcview/             # gitignored local state
    cache/services.json  # resolved-schema cache (protojson)
```

Identity = stable slug (dir name) + display name in `meta.name`; renaming edits
only `meta.name`. Ordering = the parent's `items:[slug,…]`, reconciled with disk
on load (drop listed-but-missing with a warning; append on-disk-but-unlisted in
display-name order). All writes are atomic (temp file + `rename`); mutations are
serialized per collection by an in-process mutex.

### Migration

`Store.Open` detects a legacy blob **file** at `…/.grpcview/<name>`,
`proto.Unmarshal`s it, materializes the directory tree, and backs the blob up as
`<name>.blob.bak` (which frees the path for the new directory). Idempotent — once
the path is a directory it does nothing. Covered by `TestMigrationFromLegacyBlob`
(exercises the same `Store.Open` entry point the handler uses, with a real
`proto.Marshal`'d blob).

---

## Verification

- `bazel build //...` — 42 targets, incl. the React UI — green.
- `bazel test //service/...` — `store_test` passes (9 tests).
- End-to-end against the running dev backend (isolated `HOME`, real config dir
  untouched):
  - `Get` auto-creates an empty workspace with the correct shape.
  - `CreateFolder` → `CreateRequest` → `UpdateRequest` produce exactly the tree
    above (slug dirs, `meta.name`, ordered `items[]`, inline `draftBody`,
    `.gitignore`).
  - `AddDescriptorSource` (reflecting the backend's own service) persists the
    source to `grpcview.json` and resolved schemas to gitignored
    `.grpcview/cache/services.json`.
  - Full process restart reloads the tree/services/sources identically.
  - `DeleteRequest` removes the item from tree, disk, and parent ordering.

### Commands

```bash
bazel build //service/cmd //service/cmd/dev   # release + dev binaries
bazel test  //service/...                     # store tests
bazel run   //:gazelle                         # regenerate BUILD files
bazel run   //service/cmd/dev                  # backend (dev)
bazel run   //ui:dev                           # frontend (dev)
```

---

## Resolved ambiguities / deviations (within the locked decisions)

- **Root config lives in `grpcview.json`**, not a separate `tree/folder.json`.
  §5 explicitly allows "collection.json for the root." So `grpcview.json` carries
  the root ordering + manifest + sources; non-root folders use `folder.json`.
- **Hybrid file format.** Proto-typed payloads (`draftMetadata` Struct, `sources`
  `DescriptorSource`, the services cache) use `protojson`; the thin container
  wrappers carrying `meta.name` / `items[]` (not expressible in any existing
  proto message, and no proto changes allowed) use stdlib `encoding/json`. No new
  deps.
- **`Workspace.sources` is now populated** (persisted to `grpcview.json`), whereas
  the old code left it empty. Additive and UI-invisible → client behavior
  unchanged; satisfies the design's config/state split.
- **Fixed a latent dedup bug** in `AddDescriptorSource`: it compared a service's
  short name to its fully-qualified name (never matched → duplicates accumulated
  on repeat calls). Now compares `package.name` to the FQN. Single-call behavior
  is identical.
- **Draft body is a `draft_body` `string` field in `request.json`**, not a separate
  `body.json` sidecar. §4/§5 originally specified a raw sidecar for exact-diff Monaco
  edits; folding it into `request.json` as a `string` (protojson emits it as a plain
  JSON string, not base64, since the body is always UTF-8 text) removes the separate
  read/write/convert code path — one file and one write per request. Trade-off: a body
  edit shows as one escaped line in the diff rather than raw multi-line JSON. The wire
  `Request.draft_body` / `UpdateRequestRequest.draft_body` and the frontend changed
  from `bytes`/`Uint8Array` to `string` to match, dropping the `TextEncoder`/
  `TextDecoder` round-trip.

## Deferred (noted, not silently dropped)

- **`Request.history`** is not persisted — no RPC populates it (no invoke RPC
  exists), so round-trip is vacuously correct. When invoke lands, persist under
  `.grpcview/history/` per §4.
- **`Move`/rename is a store capability only** — implemented and unit-tested, but
  not exposed via an RPC (that needs a proto/API addition, out of scope). The
  UI's `renameItem` still no-ops.
- **`AGENTS.md`** remains stale (says Vue, not React) — out of scope for the
  storage rewrite; left untouched.

## Next up (Phase 2, per storage.md)

Config inheritance/cascade, environments, shared resources (targets/auth/…) with
id-primary + path-hint refs (§7), broken-ref validation surfaced to the UI,
"open any directory" addressing. None started.
