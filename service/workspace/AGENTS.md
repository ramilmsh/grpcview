# Request authoring model

Core of the product. TypeScript is the authoring affordance, not the contract — a body is
protojson everywhere it's accepted; see
[`docs/design/request-body-contract.md`](../../docs/design/request-body-contract.md), the
authoritative doc.

- Body: a bare TS object literal in Monaco, edited inside `// grpcview:script start`/`end`
  markers inside `export default async (): Promise<RequestMessage> => ( … )`. Leading `{`
  stays wrapped, else plain (backend still wraps a bare expression). Imports above the
  marker are auto-managed in wrapped mode; author-owned in plain mode.
- Metadata authored identically under `Promise<Metadata>`; filename picks the skeleton.
- Both evaluated in QuickJS at invoke time, persisted beside `request.json`, written
  verbatim with one trailing newline — always `.ts`. **Do not normalize on read**:
  untouched files round-trip byte-identical. Absent `body.ts` reads as `{}`; **empty
  `metadata.ts` means "inherit the folder chain"**. `draftBody`/`draftMetadataScript` in
  `request.json` fails to load loudly.
- Plain protojson is equally valid — a **module** or **expression** (wrapped by
  `wrapExpressionScript`, `invoke.go`, the one seam, opening no new line so bundler errors
  still name the author's line).
- Scripts are `.ts` under `scripts/`, esbuild-bundled, run under a grant; path is identity.

## The `grpcview:` modules

Imports, not globals. `invoke` (`grpcview:invoke`, `(path, params?) => Promise<InvokeResult>`)
runs another saved request through the same pipeline — a gRPC-status failure **resolves**
`ok:false`, rejecting only for unknown path/streaming target/un-evaluable body/depth cap.
`inherit` (`grpcview:metadata`) returns merged ancestor-folder metadata, unconditional
(transitivity is userland `{...inherit(), …}`). `assert` throws `AssertionError` on falsy —
sync path throws synchronously, a thenable condition returns a promise. `params`
(`grpcview:request`) is the invoke-time params object. All degrade gracefully with no context.

## Imports, sigils, and typed paths

A script's path is its identity. `@/…` resolves against the workspace root; `#/…` against
the compiling script's collection root. `import` can't stand in expression position —
bare-object bodies use `require("…")`, modules use `import` (UI wrapper spares the author
this); computed specifiers are rejected before the build. `Request.middleware` holds
specifiers, not names. `invoke`/`require` are generic over their path literal — it completes
inside the quotes and infers the real response/export type (the checker won't follow `paths`
from a call expression). Degradation is the point: no descriptor set / unresolvable symbol /
computed specifier → `any`, never a false error.

# Definition sources (where schemas come from)

Services and `descriptor_set` are derived, never authored — from a priority-ordered list of
sources (reflection targets, uploaded `FileDescriptorSet`s, bazel labels), merged by
`sources.go`. Each resolves independently, storing per `commit_descriptors`: **off
(default)** — content-addressed blob under the workspace state root, shared across
collections; **on** — protojson in the collection, so a fresh clone resolves offline
(sticky: on but never off again — `sources commit --off` is the escape). Merged view is
derived in memory per collection on first touch (`mergeSources`, front to back — first
source to define a file/serve a service wins); nothing persisted.

Order is precedence only, never recency. Identity is config-derived (`store.SourceID`:
`reflection:<address>[+tls]`, `upload:<file name>`, `bazel:<canonical label>`, computed in
`service/store/sources.go`); re-adding refreshes in place, new ones append at lowest
priority. No resolved definitions → `FailedPrecondition`. A service's dial target is
independent of who won its descriptors — `Service.source` is the first *reflection* source
serving it; `resolveTarget` honors a per-request override first.

**Definitions are shared; order is per collection.** `grpcview.work.json` holds the
unordered definition set; `grpcview.json` holds the ordered per-collection list, inline or
by **reference**. An upload can't be a definition (bytes belong to the collection); a bazel
label can — no RPC edits a `WORKSPACE`-origin definition's address in place. An upload's
identity is its file name; `path` is only a refresh recipe (browser uploads record none —
refresh is then `FailedPrecondition`); read-time confinement refuses `..`/symlink escape.
Descriptor bytes are normalized once at the write boundary (`DiscardUnknown` + deterministic
marshal) — digest is a function of schema, not encoder.

Descriptor sets are cached: `definitions.go`'s LRU holds the 16 most recently used, keyed
per collection, kept warm across daemon requests.

## The bazel kind, and workspace trust

A `Bazel{label}` refresh **builds**: `bazel query` for the descriptor-set closure, `bazel
build`, `bazel cquery --output=files` — argv slices, never a shell string; downstream it
behaves like an upload (no dial target). Adding one is a combobox pick, not free-typed.

Gated on **workspace trust** (VS Code's model, since a build runs arbitrary repo code):
granted once per root, stored in user state (`service/wsroot`), not the repo. Untrusted is a
working state, not broken — only a build is refused (`ListCollectionsResponse.trusted` +
`SetWorkspaceTrust`).
