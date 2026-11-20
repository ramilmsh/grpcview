# grpcview — two explorers over a served filesystem

**Status:** Planning only (this doc). **P0 has partly shipped**: `codegen.Pool` (`f8216a6`) runs
`protoc-gen-es` in QuickJS-WASM, but nothing calls it yet (`service/scripting/codegen/pool.go:8`).

**Goal.** Add two VS Code-style explorers to the web UI, both backed by one filesystem the daemon
serves over Connect:

- **Definitions**: the generated TypeScript for the collection's schema. Read-only.
- **Requests**: the collection directory itself (`tree/`, `scripts/`, the manifest).

**Why.**

- **One producer for every surface.** Today three browser call sites generate types:
  `gv-types.ts:59`, `Editor.tsx:306` and `TypesModal.tsx:32`. The extension host would be a
  fourth (phase 3's producer/sink seam). If the daemon generates types and serves them as
  files, there is only one producer.
- **The extension becomes a mapping.** VS Code's `FileSystemProvider` is
  stat/readDirectory/readFile/writeFile/delete/rename/watch. If the wire looks like that,
  [phase 5](../active/vscode/phase-5-extension.md)'s tree provider and custom editor are thin
  adapters, not a second client.
- **`Get` stops being the bottleneck.** `Collection` carries every body, every history entry,
  every script's source and the merged descriptor set (`workspace.proto:238-253`). About 16
  mutating responses return the whole thing too (`service.proto`, every
  `Collection collection = 1`). MCP already trims these by hand: a descriptor set is "160 KB of
  a 186 KB `Collection`" (`service/mcp/trim.go:43`). The roadmap's
  [Watch](./roadmap.md#watch-instead-of-get-live-editing-across-surfaces) item stalls on the
  same payload ("~200 KB per keystroke-sized edit"). A listing plus per-file watch events
  removes the problem instead of designing deltas.

---

## The two trees

### Definitions — virtual, read-only

```
gen/                          ← merged set, one file per proto file, named by protoc-gen-es
  grpcview/v1/service_pb.ts
  grpcview/v1/workspace_pb.ts
node_modules/@bufbuild/protobuf/   ← today's hand-fed Monaco stubs, served as files
```

**Decision: the tree is the merged set, not one tree per source.** Monaco type-checks against
the *merged* `FileDescriptorSet`, and the first source to define a file wins
(`sources.go:308-318`). A per-source tree would show files that were overridden and never
type-check. The explorer shows the merged set and **labels each file with the source that won
it**. `claimedBy` (`sources.go:308`) already records this but `mergedView` drops it, so keep it.
Overridden files are not listed.

**Paths equal Monaco's extra-lib URIs.** Today generated files live under `GEN_PREFIX`
(`file:///grpcview/request/gen/`, `gv-types.ts:37`). The Definitions tree uses the same paths,
so opening a file from the explorer and go-to-definition from a body land on the same model.

The descriptor explorer's rendered `.proto` files
([`descriptor-explorer-plan.md`](./descriptor-explorer-plan.md)) slot in later as a `proto/`
sibling of `gen/`. That plan's own RPC (`ExploreDescriptorSource`) becomes unnecessary. Its
symbol index stays.

### Requests — the real collection directory

This is the on-disk layout as it is (`service/store/layout.go:15-45`): `grpcview.json`,
`tree/**/{folder.json, request.json, body.ts, metadata.ts}`, `scripts/**/*.ts`. History lives
in the state root, not in the collection (`store/AGENTS.md`), so it is not in this tree.

What opening a file does, like VS Code `customEditors`:

| File | Opens in |
|---|---|
| `request.json` | the existing structured request editor (method, target, body, metadata, response) |
| `folder.json` | the folder editor (`FolderWorkspace.tsx`) |
| `body.ts`, `metadata.ts`, `scripts/**/*.ts` | plain Monaco, as a module |
| `grpcview.json` | Sources view |

The explorer displays an item's name from `ItemMeta.name`, not its slug: a rename never moves
the directory (`store/AGENTS.md`). The slug is the identity. That is
[phase 4](../active/vscode/phase-4-request-management.md) §1's key, now spelled as a path.

---

## Raw FS vs typed RPCs — the split

**Use the filesystem for navigation and for `.ts` text. Keep the typed RPCs for everything that
has semantics.**

- **FS writes** are allowed only to `.ts` files: `body.ts`, `metadata.ts`, `scripts/**`. These
  are opaque text the store already writes verbatim and never normalizes (`workspace/AGENTS.md`).
- **JSON manifests are read-only over FS.** A `request.json` carrying `draftBody` "fails to load
  loudly" (`workspace/AGENTS.md`). Creating a request means making a directory and a slug.
  Renaming writes `meta.Name` and leaves the directory alone. Moving reorders the parent's
  `folder.json`. Raw writes would have to re-derive all of that. `CreateRequest`, `MoveItem`,
  `UpdateRequest` and the source RPCs keep owning it.
- **FS `rename`/`delete`** apply only under `scripts/`, where the path is the identity
  (`store/AGENTS.md`). In the tree they return `FailedPrecondition` naming the typed RPC. VS Code
  shows that as an ordinary error.

Revisit when the extension wants VS Code's native JSON editor on `request.json`. Until then,
the custom editor saves through `UpdateRequest`, exactly as the web UI does.

---

## Phases

### P0 — QuickJS generation matches the browser

`codegen.Pool` exists (`f8216a6`), and its entry is a line-for-line port of
`generateWorkspaceTypes` (`codegen_entry.js` ↔ `proto-types.ts:17`). Its tests only check
substrings (`codegen/pool_test.go:49`). What is left:

- A golden test: for the `example/` collection's merged set, the map from `Pool.Generate` is
  **byte-identical** to the one the browser path produces. Check in the browser output as
  testdata, generated once by a vitest that runs `generateWorkspaceTypes`.
- Record cold-start and per-job timings on a large set, such as grpcview's own schema plus a
  bazel source.

**Done when:** the golden test passes under `bazel test //...`.

### P1 — the daemon generates types and serves them

- The daemon owns one `codegen.Pool`. Generated output is memoized next to the definitions LRU
  (`workspace/definitions.go`, 16 entries) and keyed on the merged set's digest. The digest is
  also phase 3's freshness stamp.
- A temporary `GetDefinitions(collection) → {digest, files[]}` lets P1 land before the FS
  surface. P2 replaces it.
- The UI's three call sites read from it. `proto-types.ts` keeps only its pure helpers
  (`resolveLocalSymbol`, `requestMessageAlias`, `messageTypeText`, `gvRequestMapDts`). The
  `protocGenEs` import and the `@bufbuild/protoc-gen-es` UI dependency (`ui/package.json:8`) go
  away.
- Delete `vendor/ts-stub.ts` and `vendor/ts-vfs-stub.ts`. Move `bufbuild-stubs.ts` server-side
  as served `node_modules/@bufbuild/protobuf/*` files.
- **Monaco still loads every `.d.ts`.** The checker needs the whole import graph, so the
  explorer is a *view* of files Monaco already holds, not lazy loading. Payload drops only
  because types leave `Get`. The Monaco sink still fetches the full set once per digest.

**Done when:** `descriptor_set` has no UI reader except the Sources view's counts; IntelliSense
and diagnostics are unchanged on body, metadata and scripts (phase 3's extra-lib diff check);
the stubs are gone.

### P2 — the FS RPC surface

```proto
service FileService {
  rpc Stat(StatRequest)                   returns (StatResponse);
  rpc ReadDirectory(ReadDirectoryRequest) returns (ReadDirectoryResponse);
  rpc ReadFile(ReadFileRequest)           returns (ReadFileResponse);
  rpc WriteFile(WriteFileRequest)         returns (WriteFileResponse);
  rpc Delete(DeleteRequest)               returns (DeleteResponse);
  rpc Rename(RenameRequest)               returns (RenameResponse);
  rpc Watch(WatchRequest)                 returns (stream WatchResponse);
}
// Every request carries `Uri uri`: {root: COLLECTION | DEFINITIONS, collection, path}.
// Stat: {type, mtime, size, readonly} plus a `label` (ItemMeta.name / winning source id).
// WriteFile: {content, expected_mtime}; a stale mtime is FailedPrecondition, VS Code's
// FileSystemError.FileModifiedSince.
```

- **Validation lives in the store**, not the handler. A write must target an allowed path (see
  the split), be confined to the collection (the same `..`/symlink refusal uploads use), and
  take `Collection.mu`.
- **Watch needs a store invalidation signal** (roadmap: today "a write returns the new
  `Collection` to its caller and tells nobody else") **and a filesystem watcher**. External
  edits, `git checkout` and hand-written `grpcview.json` are the events Watch exists for. The
  roadmap's
  [listing watcher](./roadmap.md#the-collection-listing-held-in-memory-and-watched) stops being
  optional, with the same prune rules and the same test: a collection grpcview did not create.
  No fsnotify dependency exists yet (`go.mod`).
- Definitions emits a single "changed" event per digest change, not per-file diffs.
- `ListWorkspaceModules` (`service.proto:293-307`, path + content of every importable module)
  is a `readDirectory` + `readFile` walk. Fold it in.

**Done when:** a Go test drives every method on both roots; an external `echo > body.ts` reaches
a `Watch` subscriber; a write to `request.json` is refused.

### P3 — slim `Get` to a listing

`Collection` becomes the listing:

- **Tree:** names, slugs, kind, request `service`/`method`/`middleware`/`target`.
- **Sources and `services`.**
- **Script paths.**

It drops `draft_body`, `draft_metadata_script`, `history`, `Script.source` and `descriptor_set`.
Every mutating response shrinks with it, and `mcp/trim.go`'s `fieldOwners` table mostly deletes
itself.

Impact on the other surfaces:

- **CLI:** `get` and `write` read through `readWorkspace` (`cli/get.go:45`, `cli/write.go:43`).
  `ls` only needs listing fields (`cli/ls.go:109-114`). `get` prints the whole snapshot; it
  gains `--with-bodies` or reads files, whichever P2 makes cheaper.
- **MCP:** `get_collection` is "`history`'s only read access" (`mcp/trim.go:13`). History needs a
  read path first, either a typed `GetHistory(collection, slug)` or a virtual `history.json`
  under the request directory in the FS. A typed RPC is preferred, because history is state,
  not collection content. Bodies are read through `ReadFile`, or `get_collection` gains an
  include flag.
- **UI:** the request editor reads `body.ts`/`metadata.ts` per open tab. That is phase 4 §2's
  document registry, keyed by FS path.

**Done when:** `Get` on `example/` is under 5 KB; the CLI, MCP and UI tests are green; MCP has a
history read.

### P4 — the two explorers

- Reuse `components/tree` for both: it is domain-agnostic (`components/tree/AGENTS.md`). It
  needs the async `getChildren` that file lists as unbuilt, but only if a directory is fetched
  lazily. The listing makes the Requests tree eager anyway.
- Rail entries: **Requests** replaces today's request tree. **Definitions** is new.
- Opening a file routes by the table above. Definitions files open read-only in Monaco, on the
  same model the type sink registered.
- Watch drives both: tree refresh, and reload of a buffer that is not dirty.

**Done when:** you can browse `gen/` and jump from a body's type to its definition file; an MCP
edit to a request appears in an open tab without a refresh.

---

## Relationships

- **Absorbs** [phase 6](../active/vscode/phase-6-optional.md)'s "`protoc-gen-es` under QuickJS".
  It is no longer optional: it is what Definitions serves.
- **Reshapes [phase 3](../active/vscode/phase-3-type-sinks.md):** the producer moves to the
  server. Phase 3 shrinks to a Monaco sink reading Definitions, plus the per-method
  `RequestMessage` alias. That alias stays client-side: it depends on the open request, not the
  schema.
- **Feeds [phase 4](../active/vscode/phase-4-request-management.md):** document identity is the FS
  path (slug-based), and the document registry reads and writes through it.
- **Feeds [phase 5](../active/vscode/phase-5-extension.md):** two `FileSystemProvider`s (one
  scheme per root, Definitions `isReadonly`), a `customEditors` entry for `request.json`, and
  `DiskSink` becomes optional for good.
- **Hosts [descriptor-explorer-plan.md](./descriptor-explorer-plan.md)** as `proto/` under
  Definitions.
- **Implements the roadmap's [Watch](./roadmap.md#watch-instead-of-get-live-editing-across-surfaces)
  and [listing watcher](./roadmap.md#the-collection-listing-held-in-memory-and-watched)** items.

## Open questions

- **Scripts' generator signatures** (phase 3's open question): are they derived on the server
  from scripts, or do they stay client-side? They invalidate on script edits, not source
  refreshes.
- **Workspace-wide Definitions.** Cross-collection invoke
  ([`cross-collection-invoke.md`](./cross-collection-invoke.md)) wants another collection's
  types. Should there be a `DEFINITIONS` root per collection, or one root with a collection
  segment?
- **Codegen failure.** A merge that does not link already fails `Get` (`sources.go:323`). Should
  a codegen failure mark the Definitions root as errored, so Requests still browses?
- **Pool sizing.** Each worker holds 64 MiB of linear memory (`pool.go:23`). Is one worker enough
  for one daemon? Generation is memoized per digest.
