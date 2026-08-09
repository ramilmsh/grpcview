# Project Overview

`grpcview` is a gRPC client — a Postman-like tool for exploring and calling gRPC
services. It reflects a server's schema, lets you author requests, and invokes
them, all from a single self-contained binary.

The defining design decision: **requests are authored as TypeScript.** A request
body is a typed TS object literal, checked in-browser against the selected
method's input message; request metadata is authored the same way; and both are
*evaluated* (in a sandboxed JS engine) at invoke time, so they can call into
user-defined scripts. There is no JSON-schema layer — an earlier design that
converted proto descriptors to JSON schemas has been removed entirely.

**But TypeScript is the authoring affordance, not the contract: a body is
protojson.** Plain protojson is accepted everywhere a body is — the backend supplies
the wrapper, so nobody is required to speak TypeScript to send a request they already
have. See
[`docs/design/request-body-contract.md`](docs/design/request-body-contract.md) for
the two accepted forms and the one seam that normalizes them.

## Project Stage

**This project has no users yet — it is way pre-release.** Breaking any contract
you like is perfectly fine. **SIMPLICITY is the important part; backwards
compatibility is IRRELEVANT at this stage.** Don't add migrations, compatibility
shims, or `reserved` proto markers to preserve old on-disk/wire data — change the
schema and delete freely. Always favor the simplest change that works over the
most backwards-compatible one. Dead and legacy code should be deleted on sight.

## Working in this repo (read before running commands)

- **This is a Bazel workspace. Never run `go build`, `go test`, `go run`, or —
  especially — `go mod` / `go mod tidy`.** Bare `go` commands reach the network,
  hang, and can wedge git. Use Bazel for everything (see Commands below).
- **`.envrc` already exports `GOPROXY=off`** (via direnv), so Bazel/Go commands
  run offline by default — you don't need to prefix anything. Offline builds are
  green; a command that wants the network is a bug in the command, not a missing
  dependency.
- The default shell here is fish. For commands that need bash semantics, wrap
  them: `bash -c '...'`.

## Delegating to background agents

Code-writing here is delegated to background agents (Workflow / Agent) while the
main thread orchestrates, verifies, and commits.

Measured across the first 25 agents of the tree rewrite: **~83% of token spend
was context handling, not reasoning or writing.** Cache reads alone fit
`≈ 1300 × turns²` (so a 100-turn agent costs 4× a 50-turn one, and a 180-turn
agent 16×), and wall clock runs ~10s/turn. **Turn count is the lever that
matters** — every rule below exists to cut it.

- **Scope one agent to ~40 turns.** Halving a job quarters its cache cost. The
  ~30–50k of context each additional agent re-establishes is ~1% of even a small
  agent's total, so splitting never eats the gain — a worry that sounds right and
  is wrong by two orders of magnitude.
- **Pre-load context; don't make the agent find it.** Paste the relevant excerpt
  into the brief, and name exact paths and line ranges. "Check how the vendored
  monaco does it" buys a ~15-turn grep expedition *per agent*; the 40 lines it
  eventually reads cost ~500 tokens to paste. Agents left to discover their own
  context hit Read×40+.
- **Cap the verify loop: run each gate at most twice, then report the failure
  verbatim and stop.** "Make all the gates pass" licenses iterate-until-green,
  the single biggest turn driver observed (one agent made 46 `Bash` calls). The
  orchestrator re-runs every gate before committing anyway, so grinding to green
  inside the agent is duplicated work at quadratic cost.
- **One reviewer, handed the diff.** Read-only review agents burned 25% of all
  output tokens and wrote no code. Keep the adversarial pass — it has caught real
  bugs that two implementers and a typecheck all missed — but put `git diff` in
  the brief instead of letting the reviewer rediscover it.
- **`effort`: one tier below the session's, floored at `medium`. Never `low`** —
  low mangles output, and a mangled result costs a whole re-run.
- **Verify through the CLI unless the change is UI-only** — a browser session
  costs multiples of a verb per check. See "Verify through the CLI, not the
  browser" below.

Capping the verify loop is a ban on *grinding*, never a licence to skip testing.
Agents in this repo have a track record of **reporting passes that never
happened**, in two specific ways:

- A new `.go` file that was never added to its `BUILD.bazel` `srcs` isn't
  compiled, so the package still builds and its tests still "pass". Check that
  new sources actually landed in `srcs`.
- Bazel serves cached results, so a test target can report `PASSED` without
  running. When validating someone else's claimed pass, always
  `--nocache_test_results`, and check the named test count changed.

## Architecture

- **Frontend** (`ui/`): a React 18 + TypeScript single-page app built with Vite.
  Compiled to a **single HTML file** (`vite-plugin-singlefile`) and embedded into
  the Go binary, so distribution is one standalone static executable.
- **Backend** (`service/`): a Go server exposing the `WorkspaceService` over
  [Connect] (h2c). It handles gRPC **reflection** and **request proxying/invoke**,
  persists the workspace to disk, and hosts the scripting engine.
- **Store** (`service/store/`): a filesystem-backed collection persisted as a
  git-versionable **protojson directory tree**. The on-disk schema
  (`grpcview.store.v1`) is deliberately decoupled from the wire schema
  (`grpcview.v1`) and bridged by `convert.go`.
- **Scripting** (`service/scripting/`): a QuickJS-WASM engine (wazero) that runs
  user JS/TS — request-body/metadata evaluation, imported scripts, middleware,
  and scenarios. **Network is on for every script**: a browser-style global `fetch`
  (a deliberate subset of WHATWG fetch — see `net.go`) is available with no grant
  and no capability manager. The **filesystem** capability is still deny-by-default
  behind a `Grant` (`node:fs`). Sources are bundled with **esbuild** before execution.

[Connect]: https://connectrpc.com

## Request authoring model

This is the core of the product; understand it before touching `ui/` or
`service/scripting/`.

**Why TypeScript:** it replaced `{{ }}` templating. Postman-class tools need a token
syntax because their bodies are *data* — text you interpolate into — which buys escaping
rules, no autocomplete, no type-checking, and no composition. grpcview's bodies are
*expressions*, so a computed value is just a call written where the value goes
(`{ userId: uuid() }`), and IntelliSense comes free from the host language. The static
and dynamic cases are therefore one gradient, not two modes: `{"userId":"u_1"}` becomes
`{"userId": uuid()}` by editing it, with no conversion step. Preserving that is why
plain protojson must run the *same* evaluation path rather than a separate one — a
protojson body that could not import a script would be `{{ }}` all over again.

- A request **body** is authored as a bare TypeScript object literal in a Monaco
  editor. What the user edits is wrapped in a hidden canonical module —
  `export default (): RequestMessage => ( <body> )` — whose prefix/suffix lines
  the editor hides (`body-wrapper.ts`). A body that carries its own `export default`
  is a module already, so the editor shows it whole and hides nothing; `module-sniff.ts`
  is what tells the two apart, over comment- and string-masked source for the same
  reason `maskLiterals` exists on the backend. Hiding the wrapper above a module would
  also put auto-import's insertion point inside a region the author cannot see.
  Because `isCanonical` matches on `endsWith(")")` while the store normalizes every
  `body.ts` / `metadata.ts` to exactly one trailing newline, a persisted body reaches the
  UI as `"<canonical>\n"` and must be **hosted** before the canonical test — trailing
  newlines are stripped by `migrateBodyToTs` and `hostMetadataScript`, and the store
  re-adds one on write, so the round trip stays byte-identical. Skip that and the wrapper
  silently stops being hidden and becomes editable.
  The `RequestMessage` type is generated
  **in the browser** by `@bufbuild/protoc-gen-es` from the workspace's reflected
  `FileDescriptorSet` (`proto-types.ts`), giving full IntelliSense and
  type-checking against the selected method's input message.
- Request **metadata** is authored identically — a bare object evaluated to
  `{ [key: string]: string[] }` under a hidden `=> ( … )` wrapper
  (`metadata-wrapper.ts`, `MetadataEditor.tsx`).
- Both body and metadata strings are **evaluated on the backend in QuickJS** at
  invoke time (same machinery as scripts), so they can import the collection's own
  scripts and npm packages.
- **Both persist as files, not proto fields**: `body.ts` and `metadata.ts` sit beside
  `request.json` in the request's directory, written **verbatim** (the only
  normalization is exactly one trailing newline). So a body edit is a line diff, and a
  body is `tsc`-checkable, hand-editable, and usable as an esbuild entry point. Always
  `.ts`, never `.json` — valid JSON is valid TypeScript, so `.ts` is never *wrong*, and
  esbuild picks its loader from the extension, which would make `require("@/…")` inside a
  `body.json` entry a syntax error. **Do not normalize on read**: a `body.ts` holding
  plain protojson that the user never edited must round-trip byte-identical, or the app
  writes a git diff on a file nobody touched. An absent `body.ts` is legal and reads as
  `{}`; an **empty `metadata.ts` is not the same as `"{}"`** — empty means "inherit the
  folder chain" (`resolveInvokeMetadata` treats any non-empty script as authoritative and
  skips the inherit fold), which is why a new request is seeded with `EmptyBody` for the
  body and a zero-byte metadata file. A `request.json` still carrying `draftBody` or
  `draftMetadataScript` **fails the load loudly**, naming the file and the fix: `DiscardUnknown`
  would otherwise load it fine with an empty body and the next write would delete the
  body silently. **Folder** metadata is the exception — still inline in `folder.json`.
  Neither file reaches `ListWorkspaceModules`: a body is an entry point, never an
  importable module. Plan: [`docs/design/shipped/vscode/phase-2-body-files.md`](docs/design/shipped/vscode/phase-2-body-files.md).
- **Plain protojson is equally valid** for both, because **valid JSON is valid
  TypeScript** — a JSON object is a TS object literal in expression position, so it is
  not a second case and gets no second code path. There are two forms: a **module**
  (has `export default`) or an **expression** (anything else, wrapped in
  `export default async () => ( … )` and run on the same path). The Monaco
  hidden-wrapper form above is what the browser authors; it is not what the backend
  requires. `wrapExpressionScript` (`service/workspace/invoke.go`) is the single seam
  that applies the wrap, and **all three object positions go through it** — request
  body, request metadata, folder metadata — so every surface (UI, VS Code, CLI, MCP,
  a hand-edited `request.json`) inherits one behavior. The wrap opens no new line, so
  a bundler error still names the author's line.
  Full contract: [`docs/design/request-body-contract.md`](docs/design/request-body-contract.md).
- **Scripts** are `.ts` files under a collection's `scripts/`, authored in the Scripts
  view, bundled with esbuild, and run in the same sandbox under a grant. A script has
  no name and no kind — see "Imports and the two sigils" below.

### The `grpcview:` modules

**No grpcview-specific identifier is global.** Not "nothing is global": `console`,
`fetch` and the internal `__grpcview_*` host bridges all are. The rule is narrower and
deliberate — the long-term goal is emulating enough of the Node/browser environment to
run third-party libraries unmodified, and a library that calls `console.log` must not
need a grpcview import. Everything grpcview *adds* is an import instead:

```ts
import { invoke } from "grpcview:invoke";     // (path, params?) => Promise<InvokeResult>
import { assert } from "grpcview:assert";     // (description, condition) => void | Promise<void>
import { inherit } from "grpcview:metadata";  // () => { [k: string]: string[] }
import { params } from "grpcview:request";    // Readonly<Record<string, any>>
```

Each is a static shim resolved by `grpcviewModulesPlugin` (`bundler.go`) into a synthetic
namespace, so the module **text** is constant and stays cacheable. Per-run *data* rides the
prelude under internal names the shims read — `__grpcview_params`, `__grpcview_inherited`,
`__grpcview_request` (`writeDataGlobal` in `marshal.go`). **Data in the prelude, code in the
module.** The editors see the same four modules as `declare module` blocks in the ambient
`gv.d.ts` registered once at `file:///grpcview/gv.d.ts` (`monaco-scripts.ts`).

`params` values and `InvokeResult.body` are typed `any`, not `unknown`, on purpose: both
hold data whose real shape the ambient `.d.ts` cannot name, and both are meant to be
drilled into or assigned straight into a typed request field —
`(await invoke("Collections/ListCollections")).body.collections[0].id`,
`refresh: params.refresh ?? true`. Under `unknown` each of those is a checker error in the
body editor for code that runs correctly.

Members degrade gracefully rather than being absent: `inherit()` is `{}` with no
inheritance context, `params` is `{}` on a top-level invoke, and `invoke` rejects when no
`Invoker` rides the ctx.

- **`inherit()`** returns the already-evaluated, merged metadata of the
  node's ancestor **folder** chain. Folders carry their own metadata script
  (`Folder.draft_metadata_script`, edited via the folder-row gear); `invoke.go`'s
  `foldAncestorMetadata` walks them root→parent as an iterative Go fold, capped at
  `MaxFolderMetadataDepth`. The fold is **unconditional**: it used to be gated on the
  leaf's script textually mentioning `inherit(`, and that gate died with the import
  rewrite, because a leaf can now reach `inherit` through an imported file and no text
  scan can see it. The chain is computed and the leaf decides — an empty script inherits
  it, a script that spreads `inherit()` inherits it, a script that does neither gets
  nothing. One consequence to know: the fold opens the collection even for a request with
  no folder path, so a test that skips workspace setup now fails with
  `not_found: collection not found` where it used to pass. Transitivity is **userland
  spread**: a folder that writes `{ ...inherit(), … }` carries ancestors forward, an empty
  folder is transparent, and a non-empty folder that omits the spread is a
  deliberate barrier. Ancestor scripts are read from the **store**, so folder edits
  only take effect after saving.
- **`invoke(path, params)`** runs another saved request through the same
  pipeline and resolves an `InvokeResult` (`{ok, status, body, metadata,
  requestMetadata, latencyMs}`); the target reads them as `params` from
  `grpcview:request`.
  `path` splits on `/` into display-name segments. A gRPC-status failure **resolves**
  with `ok:false` (fetch-style); it rejects only for unknown path, a streaming
  target, un-evaluable body/metadata, or the depth cap. Nested invokes do **not**
  record history. Bounded by a ctx depth counter (`gvinvoke.go`) — a depth cap only,
  with no cycle set, so self-recursive pagination still works.
- **`assert(description, condition)`** is the whole test-harness primitive, and it is
  pure JS in the `grpcview:assert` shim (`bundler.go`) — no host call, no wire field, no UI. It
  **throws** on failure: an `AssertionError` reading `assertion failed: <description>`, with
  the underlying text appended when the predicate throws or its promise rejects. Truthiness
  decides, not `=== true`. Three things about it are load bearing:
  - **The sync path throws synchronously and returns `undefined`**; only a *thenable*
    condition returns a promise. Wrapping the sync case would be silently broken, because an
    unawaited rejection is dropped by `evalRaw`'s top-level settle — a failed assertion
    would read as a pass. The two `.d.ts` overloads exist to keep that visible in the
    editor: the sync form types as `void`, so nobody is nudged into `await`ing it.
  - **Its own frames are named (`gvAssert`, `gvAssertFail`) and filtered out of the stack**
    before the throw. `remapJSError` reads the *first* frame's line and the throw happens
    inside the shim, so an unfiltered stack blames the shim instead of the failing
    assertion's line. The filtering has to survive **bundling** now that the shim is a
    module folded into the bundle rather than prelude text.
  - **Nothing is logged on success**, and a failure aborts the run — silence is a pass, and
    one script is one assertion budget rather than a report over many.

### Imports and the two sigils

A script is a `.ts` file and **its path is its identity** — no name, no kind, no
`script.json`. `Script` on the wire is `{ path, source }`, where `path` is
collection-relative *with* the extension (`scripts/uuid.ts`), and it is what
`UpdateScript` / `DeleteScript` / `RunScript.script` address. Renaming is a path change,
i.e. a file move. The store lists scripts by walking `<collection>/scripts/**/*.ts`
(sorted); the filesystem is the listing, so there is no ordered slug array to reconcile.

Two sigils, both resolving to real files:

| specifier | resolves against |
| --- | --- |
| `@/…` | the **workspace root** — anything, including another collection's scripts |
| `#/…` | the **collection root** of the script being compiled |

Facts that are load bearing:

- **One plugin, `pathResolverPlugin` (`bundler.go`), filter `^[@#]/`, and its position in
  the plugin list is a contract**: it must claim these before `registryResolverPlugin`'s
  `^[^./]` filter, which would otherwise take them for npm packages. `grpcviewModulesPlugin`
  sits ahead of both for the same reason.
- **Both roots are canonicalized with `filepath.EvalSymlinks` up front.** esbuild returns
  symlink-resolved paths, and macOS puts `/tmp` and `/var` behind symlinks, so an
  un-canonicalized root fails its own containment guard for a file genuinely inside it. This
  is the single most likely thing to break a test on this path.
- **The workspace root is per-engine** (`scripting.WithWorkspaceRoot`), **the collection root
  is per-compile** (`Input.CollectionRoot`) — a run can have one, both or neither, and a
  missing one reports `no <which> root for this run` rather than resolving to something else.
  Escaping a root is `resolves outside the <which>`.
- **`import` is a statement, so it cannot stand in expression position.** A body or metadata
  written as a bare object literal must use `require("…")`; a module (anything with
  `export default`) uses `import`. The rule is grammatical, not a preference. The
  `Expected "(" but found …` parse error is caught and replaced with a message that says so.
- **Computed specifiers are rejected before the build** (`rejectComputedImports`). esbuild
  reports neither an error nor a warning for `require(p)`, and emits code that fails at run
  time. A conservative regex is correct here: we are forbidding, not resolving.
- **`Request.middleware` holds specifiers, not names** — `#/scripts/trace-headers.ts` or
  `@/lib/mw/auth.ts`, stored exactly as written, with no canonicalization. The source is read
  from **disk** at the resolved path (`resolveMiddlewareSpecifier` in
  `service/workspace/middleware.go`, which mirrors the plugin's two sigils and containment
  guard), so a middleware is an ordinary module that imports whatever it needs. There is no
  composition step left anywhere.
- **Import suggestions in Monaco are ours, not TypeScript's.** Monaco's bundled worker calls
  `getCompletionsAtPosition` with no options and drops the import-inserting `codeActions`, so
  `includeCompletionsForModuleExports` can't be reached and TS-native auto-import is
  unavailable at any configuration. `module-auto-import.ts` registers a completion provider
  that offers each workspace module's exports (parsed in `auto-import.ts`) with an
  `additionalTextEdits` that writes the `import`, skipping names already in scope so it never
  duplicates the built-in provider. It computes the specifier itself, so the inserted form is
  always `#/` or `@/` and never relative.
- **The bundle cache is keyed over the whole graph.** `Metafile: true` on every build,
  `cacheKey` covers `(cacheSalt, variant, grant, collectionRoot, source)`, and the resolved
  input list is stored *with* the compiled output and re-hashed on every hit — a hit whose
  inputs changed is discarded. Keying on the entry source alone would serve a stale bundle
  after an imported file is edited.

### Typed `invoke` paths

`invoke` is generic over its path, so a literal path completes inside the quotes
and types `body` as the target method's response message:

```ts
import { invoke } from "grpcview:invoke";
(await invoke("Collections/ListCollections")).body.collections[0].id  // string
```

The mechanism is an ambient interface merged into from a generated file:

- `gv.d.ts` (`monaco-scripts.ts`) declares `interface GvRequestMap {}` empty, plus
  `grpcview:invoke`'s `invoke<P extends GvPath | (string & {})>(…): Promise<InvokeResult<GvBody<P>>>`.
  `(string & {})` is load-bearing: a computed path still compiles and just gets
  `body: any`, while literals keep their completions.
- `collectInvokeTargets` (`gv-requests.ts`) walks the collection tree and pairs each
  **unary** saved request's display-name path with its **output** message. Streaming
  requests are skipped (`invoke` rejects them), as is any name containing `/`,
  which `splitInvokePath` would resolve elsewhere.
- `gvRequestMapDts` (`proto-types.ts`) turns that list into
  `gv-requests.d.ts`, importing each `<Message>Json` from the generated `./gen/**_pb`
  modules under a positional alias (two files may export the same symbol).

**The registration is app-level, not per-editor** (`gv-types.ts`'s `useGvInvokeTypes`,
called once from `App.tsx`'s `CurrentView` *above* its early returns). `invoke` is
importable from every script surface, so the two **collection-scoped** libs — the generated
`file:///grpcview/request/gen/**` modules and `gv-requests.d.ts` — cannot belong to the body
editor: owned there, they existed only while a request tab was open with a method selected,
and the Scripts view (which mounts no body editor) got `keyof GvRequestMap === never` and
`body: any`. Only the **method-scoped** `request-message.d.ts` alias stays in `Editor.tsx`.
Two consequences to keep: both must keep the same `file:///grpcview/request/` prefix, since
that shared prefix is what resolves the alias's relative `./gen/…` import; and the hook uses
a direct `import * as monaco from "monaco-editor"` rather than `useMonaco()`, which returns
null until the loader runs on the first editor mount — the very coupling being removed
(`monaco-nocturne.ts` does `loader.config({ monaco })`, so they are one instance).

Degradation is the point: no descriptor set, an unresolvable symbol or an empty
collection means no map, `keyof GvRequestMap` is `never`, and every path falls back
to `body: any` — never a false error. The map is rebuilt whenever the tree or the
descriptor set changes, so a rename retargets the paths.

## Definition sources (where schemas come from)

A workspace's services and `descriptor_set` are **derived**, never authored. They
come from a **priority-ordered list of descriptor sources** — reflection targets,
uploaded `FileDescriptorSet`s and bazel labels that *build* one — merged by
`service/workspace/sources.go`.

The layering is the whole point, so don't collapse it:

1. **Each source resolves independently** to its own `FileDescriptorSet` plus the
   list of services it serves. A reflection source resolves by dialing; an upload
   from the bytes its add call carried, or by re-reading the file that add recorded;
   a bazel source by **building** its label. Where that resolve is *stored* is a
   per-source flag, `commit_descriptors`, and it changes only the location — never
   how anything resolves, merges or dials:
   - **off (the default, every kind)** — a **content-addressed blob** under the
     workspace state root (`blobs/<sha256>.binpb`, binary, never in the repo), with
     each collection keeping a `descriptors.json` index pointing its source **ids**
     at digests. The two keys are different on purpose — an id is a pointer and
     survives its content changing, which is what makes re-adding a source a
     refresh — and the blobs are shared, so five collections resolving one target
     hold one copy of its bytes and a blob is collected only when no collection in
     the workspace references it.
   - **on** — protojson **in the collection**, one sidecar per source at
     `descriptors/<slug>-<hash of the id>.json`, holding the descriptor set *and*
     the served service names (those are not derivable: a reflection server's
     `ListServices` is authoritative and narrower than "every service these files
     define"). That is what makes a fresh clone resolve with **no refresh and no
     network** — the point of the flag.

   The naming asymmetry is deliberate and the reasons are opposite. Content
   addressing is right for a cache: dedup across collections, immutability, and
   "same digest, no write". A **digest**-named committed file would make every
   refresh an add plus a delete instead of a diff, destroying the readable-protojson
   rationale for committing at all — so a sidecar is named by **source id** (hashed
   as well as slugified, so `localhost:8080` and `localhost.8080` cannot collide).
   Each source is written to exactly ONE of the two places, which is what makes
   **toggling the flag a move that acquires nothing**
   (`SetDescriptorSourceCommit`, `grpcview sources commit [--off]`): on writes the
   sidecar from the bytes already stored, off drops the sidecar and writes the blob.
   Turning it on for a source that has never resolved is `InvalidArgument` naming
   `refresh`, because resolve-then-commit would be acquisition triggered by a config
   change. The flag is also **sticky across a re-add**: since re-adding an id is the
   documented refresh gesture — and the browser's only way to refresh an upload — an
   add can turn committing on but never off, or re-adding with the box unticked
   would silently delete a sidecar the repo carries. `sources commit --off` is the
   one way off. Descriptors are never inline in `grpcview.json`, for any kind: that
   file also holds root ordering, the source list and script ordering, so an inline
   multi-megabyte descriptor set would mean dragging one request rewrites megabytes.
2. **The merged view is derived in memory, per collection, on first touch** —
   `mergeSources`, walking the list front to back: the first source to define a
   proto **file** (by name) wins it, the first to serve a **service** (by full
   name) wins its list entry, later sources fill the gaps. Then the whole claimed
   set is *link-checked*, so sources that disagree about shared protos fail loudly
   instead of producing a subtly broken workspace. Nothing about that result is
   persisted: it is a pure function of (the blobs, the source order in
   `grpcview.json`), the writer drops the memo entry, and the next read rebuilds
   it. A *read* survives an unmergeable list — the error lands on the source rows
   and `services` comes back empty, so a colleague's commit cannot stop a
   collection loading — while a *mutation* still fails outright, because the user
   is changing those sources right now.

Consequences worth preserving, each of which was a bug before:

- **Order is precedence, and only order.** The outcome is a pure function of the
  source list, never of which source was added or refreshed last. That is what
  makes two sources describing the *same* protos usable: gRPC reflection strips
  `source_code_info`, so a `buf build` upload of those files carries doc comments
  the live server cannot, and whoever wins decides whether hovers show them.
  `ReorderDescriptorSources` is the user-facing switch.
- **Only the added/refreshed source touches the network.** Remove and reorder are
  pure cache re-derivations, so an unreachable target can't block them. A source
  that fails to resolve stays listed with the reason in `Resolved.error` and
  contributes nothing, rather than being dropped or failing the mutation.
- **Identity is config-derived** (`store.SourceID`, the one definition of the
  format): `reflection:<address>[+tls]`, `upload:<file name>` or
  `bazel:<canonical label>`. Re-adding the same id **refreshes in place at its
  existing priority**; a genuinely new source appends at lowest priority. Keying an
  upload by file name (not by a content hash) is deliberate — rebuilding the image
  must refresh the source it came from, not spawn a second one — and it is the same
  reason a label is canonicalized *before* an id is derived from it (below).
- **Every source has a unique, non-empty id, guaranteed at load**
  (`store.normalizeSources`, run from `readCollection`). Refresh, remove and
  reorder all address a source *by id*, so two rows sharing one id — as a manifest
  written before ids existed produces — silently retarget those operations at the
  first of them. A source with no id gets one derived; duplicate and contentless
  entries are dropped.
- **The sources are the *only* resolver; invoke never resolves again.** Both
  Describe and Invoke read method descriptors from the merged set through the one
  seam in `service/workspace/definitions.go`, and neither reflects on the target at
  call time. Invoke used to run `grpcreflect` over its own connection, which made a
  reflection service a *precondition for calling anything* — so a workspace whose
  schema came from a `buf build` upload could describe a method it could not invoke,
  and the deployment you actually call (behind a gateway, with reflection stripped)
  answered `Unimplemented` no matter how well its descriptors were known. Dialing is
  now the only thing invoke needs the network for. The corollary is that a workspace
  with no resolved definitions gets `FailedPrecondition` and nothing is sent, which
  is also the contract the CLI already enforced before reaching the backend.
- **The merged set is memoized, not repeated.** `definitionsCache` holds the whole
  derived view — linked descriptors, services, merged bytes, per-source summaries —
  keyed by collection and by nothing else, so a hit is a plain map lookup with no
  read, no stat and no hash. It is a small LRU because one entry is a fully linked
  descriptor set. The five source-writing RPCs invalidate (the four plus
  `SetDescriptorSourceCommit`); nothing else can change a collection's descriptors.
- **A service's dial target is independent of who won its descriptors**:
  `Service.source` is the first *reflection* source that serves it. Neither an
  upload nor a bazel label has an address, so without that split, placing one first
  for its comments would strand every request it claimed with no target. The UI
  keeps the two questions visually separate too: the request header's chip names
  the source the **schema** came from (`schemaSourceFor`, off
  `Resolved.won_service_names`), while the target bar under it shows where the
  request is **sent**. Neither is "no source" merely because the other is absent — and the
  target field is **always editable**, never a message telling you to go add a reflection
  source. `resolveTarget` honors a per-request override *before* it looks at the sources, so a
  collection whose schema came from an upload or a bazel label is invokable by typing an
  address; refusing to accept one was the bug. With nothing set the field is an empty
  `host:port` prompt, and `MethodHeader` gates Invoke on the target's **address** rather than
  on a `Server` existing, because an override starts life empty the moment the field is
  touched and dialing `""` is not an invoke.

**Definitions are shared; the order stays per collection.** Five collections pointing
at one target must not mean five copies of its config, but precedence is per
collection by design — so the split follows exactly that line. `grpcview.work.json`
holds source **definitions**, keyed by the same config-derived `store.SourceID`, and
that list is deliberately *not* an order: it is a set. A collection's `grpcview.json`
holds the ordered **list**, and an entry in it is either an inline definition (as
before) or a **reference** — an id with no oneof arm, resolved against the workspace
manifest as the wire list is built (`diskToWireSource`). The disk entry stays bare,
which is the load-bearing invariant: every mutation round-trips the list through the
wire form, so `wireToDiskSource` writes any source the workspace defines back as a
bare reference, and a reorder in one collection cannot inline shared config into all
five. It also *is* the dedup — re-adding an address the workspace already declares
produces a reference, because the same id is the same config by construction. Blobs
need no scheme of their own: the CAS is already per workspace, so one resolve serves
every collection referencing it. A definition may not be an upload — not even one that
recorded a path, because its bytes belong to the collection that supplied them and a
digest is content rather than config — while a bazel label is a fine definition, since
it re-produces its own bytes for every collection that references it, and
`commit_descriptors` on a definition is ignored — a sidecar can only live inside a
collection, so the flag belongs to the referencing *entry*, which carries its own. Both
rules warn and skip on read rather than failing: this is committed config a colleague
wrote, and one bad entry must not stop a workspace loading.

The wire `DescriptorSource.origin` (`COLLECTION` | `WORKSPACE`) is read-only,
server-set from where the config was found, and `sources ls` spells it out in its own
column. Everything a client may do to a `WORKSPACE` source is **per collection**:
reorder it, remove it (which drops *this* collection's reference, never the shared
definition), refresh it, toggle its `commit_descriptors`. What it may not do is edit
its address — no RPC edits a definition in place, deliberately, because identity is
config-derived and a different address is a different source. A reference the manifest
does not define keeps its row with no kind, and its `Resolved.error` names
`grpcview.work.json`: a reference whose definition was renamed must be visible and
removable, not silently absent. `defaults.sources` seeds a new collection's list with
bare references to those ids, in order, skipping any the manifest does not define —
**pointers only**, so a seeded collection resolves nothing until something acquires,
which is why `grpcview init` says so on stderr when it seeded any.

**An upload's identity is its file name; its `path` is only a refresh recipe.** The two
are deliberately different jobs: the file name is what makes re-adding a rebuilt image
refresh the source it came from, and the path — workspace-root-relative, so it reads the
same in every checkout — is merely how to get new bytes without being handed them, so a
`git mv` of the file changes the recipe and not which source this is. The CLI records it
(`sources add ./image.binpb` sends the absolute path and the store keeps the root-relative
form); a browser has a file picker and never learns a path, so a browser upload records
none. `RefreshDescriptorSource` on an upload **with** a path re-reads that file; on a
pathless one it is still `FailedPrecondition`, now worded around the case that produces one:
hand the file over again — that *is* its refresh, since the same file name is the same id and
a re-add refreshes in place. A bare `grpcview sources refresh` therefore skips only
**pathless** uploads, rather than every upload.

The two ends of that recipe are confined differently, and on purpose. At **add** time a
path that does not confine to the workspace root is not an error: the bytes are already in
the request and already valid, so the source lands with **no recipe** and a warning that it
is not refreshable — failing the add over a `buf build` image in `~/Downloads` would break
the ordinary workflow, and a dead or unknown path costs a refresh and nothing else. On the
**read** side confinement is strict, because a recorded path is wire input all over again —
it lives in a `grpcview.json` a colleague committed — so a refresh refuses an absolute path
outside the root, a `..` escape and a *symlink* escape, and then reads the
`EvalSymlinks`-resolved path rather than the one it checked, which is what closes the
check-then-read window. `service/workspace/paths.go` is the one confinement helper, so
there is a single place to be right about all of that.

An uncommitted upload's only copy still lives in local state, so a clone of a repo whose
upload was not committed has no schema for it until someone hands the file over again — or,
when the recipe names a file the repo itself carries, until a refresh re-reads it. That is
accepted deliberately, given a durable state root: forcing `commit_descriptors` on for
uploads is unnecessary, and it stays a real choice.

Descriptor bytes are normalized once, at the store's write boundary
(`normalizeDescriptorSet`): re-encode with `DiscardUnknown` and marshal
deterministically, so the digest — and a committed sidecar's bytes — are a function
of the *schema* rather than of the encoder that produced it. That drops a
`buf build` image's buf-specific extension fields, which nothing reads, while
`source_code_info` (the doc comments) round-trips intact; and it is what makes
refreshing twice against an unchanged upstream leave `git status` clean.

### The bazel kind, and workspace trust

A `Bazel{label}` source — id `bazel:<canonical label>` — is the one kind that **produces**
its own bytes, which is exactly what a monorepo needs: the descriptors are a build output
of the repo the collection lives in, and they change on every proto edit. So refreshing it
**builds** — three invocations, all with *identical* flags, because cquery reports the
output path of the configuration it is asked about and a flag differing from the build's
would name a path the build never wrote:

1. `bazel query --output=label --order_output=no -- kind("^(proto_library|proto_descriptor_set) rule$", deps(<label>))`
   — the label's **descriptor-set closure**. This is not an optimization: a `proto_library`'s
   descriptor set holds only the files *that* target declares, so a label whose protos import
   another target's — `google/protobuf/any.proto`, most often — links to nothing on its own
   (`link descriptor set: no such file: "google/protobuf/any.proto"`). Reading the closure's
   per-target sets and deduping by file name is what reconstitutes what
   `protoc --include_imports` would emit. A failed query is fatal rather than degraded to the
   bare label, since resolving without the closure is the very failure it prevents.
2. `bazel build --curses=no --color=no --noshow_progress -- <label> <closure…>` — patterns,
   so the closure arrives as separate arguments.
3. `bazel cquery --output=files` over `<label> + <dep> + …` — cquery takes ONE expression, so
   the same closure arrives unioned into it.

All three run as an argv slice and never as a shell string, with `--` before the labels so a
label can never arrive as a flag (`//x --output_base=/tmp`) — a label is untrusted text out
of a committed manifest, and so is every label bazel prints back, which is re-canonicalized
before it reaches an argv. cquery hands back
workspace-root-relative paths, which are read from disk as they come rather than assembled
as `bazel-bin/<pkg>/<name>-descriptor-set.proto.bin`, which would bake in both the rule's
output naming and `--symlink_prefix`. Several outputs, and the same proto file name in more
than one of them, are the expected case and not an error — the closure above emits one set
per target, and a merging rule concatenates its inputs' sets on top of that — so the sets
are deduped by file name, first spelling winning,
before anything links, because `desc.CreateFileDescriptorsFromSet` rejects a duplicate.
Deduping by name is safe *here* precisely because every copy came out of one build of one
target; two different sources disagreeing is `mergeSources`' problem, not this one's.

Downstream, a bazel source behaves like an upload in the two ways that matter. It has no
`ListServices` to narrow it, so what it **serves** is every service the built set defines
(which is why it shares an upload's resolve), and it has no dial target at all (`server` is
nil), so a service whose descriptors it wins still dials the first *reflection* source
serving it.

Labels are canonicalized before any id is derived from them — `//pkg` becomes
`//pkg:<last segment>`, `pkg:target` becomes `//pkg:target`, and `@repo//pkg:target` and
`@@canonical_repo+//pkg:target` are kept as written — in `AddDescriptorSource` and again on
**every manifest read**, because a colleague's `grpcview.json` saying `//pkg` must not
become a second source alongside `//pkg:pkg`. Target patterns (`:all`, `:all-targets`, `*`,
`...`) are refused outright: a source names one target, and a pattern would resolve it to
whatever the package happens to contain. So is an output that parses but defines no proto
files, naming both the file and the label — `proto.Unmarshal` succeeds on nearly any bytes,
so a source pointed at a `go_binary` has to fail loudly instead of resolving to nothing.
The bazel root is the nearest ancestor of the workspace root holding `MODULE.bazel`,
`WORKSPACE` or `WORKSPACE.bazel` (walked, not asked of `bazel info`, which would start a
server), overridable as `bazel.root` in `grpcview.work.json` next to
`bazel.timeout_seconds` (default 600). A build that succeeds while leaving its output only
on a remote cache gets an error of its own naming `--remote_download_toplevel`, because
`--remote_download_minimal` is a real configuration and grpcview must never silently add
download flags to somebody's build; every other failure carries the tail of bazel's stderr,
which is where a bazel failure's reason actually lives.

**Adding one is a pick, not a recall.** `ListBazelTargets` runs
`bazel query --output=label --order_output=no --keep_going -- kind("^(proto_library|proto_descriptor_set) rule$", //...)`
and the add form's label field is an editable selector over the result
(`ui/src/components/ui/Combobox.tsx`). Three things about it are load bearing:

- **The field still takes anything.** It is a combobox and not a `<select>`, because the
  kind regex is exact — `go_proto_library`, `cc_proto_library` and friends carry "proto"
  in their kind and output no descriptor set, so a loose pattern would fill the list with
  targets that fail on add — and a ruleset the regex does not know about must still be
  reachable by typing its label.
- **Nothing waits for it.** The query is issued only while the form is open, never
  retried, and cached with `staleTime: Infinity` (`useBazelTargets`): a cold bazel server
  answers in tens of seconds, so a form that blocked on it would be unusable. A failure is
  a hint *under* the field, in the server's own words. The cache is keyed by **workspace
  root**, which is in the react-query key and not in the request: `ListBazelTargets` takes
  no arguments, and the server answering a given URL can be restarted in another directory,
  so without the root a page that was never reloaded offers another repo's targets forever.
- **The popup renders at most 100 matches**, each `flex: none`. Both are about a monorepo
  with thousands of `proto_library` targets: the cap keeps the mount cheap and says how many
  matches it dropped, and without `flex: none` a flex column with a `max-height` *shrinks*
  its rows past their line-height instead of scrolling — 60 labels render as 10px slivers.
- **It is trust-gated like a build**, and refuses with `FailedPrecondition` rather than
  answering an empty list — `bazel query` loads BUILD files and can fetch external repos,
  which is the same "runs this repo's code" that trust exists for. The query expression is
  a constant assembled server-side and never client input, because the label validator
  that guards every other bazel entry point cannot check a query expression. A partial
  listing (`--keep_going` over an unloadable package) comes back as labels *plus* a
  `warning`, never as an error: one broken package must not blank the picker.

**There is still no load-time acquisition, and that is the invariant to keep.** Only the
source-mutating RPCs build, so a bazel source's descriptors change on an explicit refresh
and never because a terminal `bazel build` happened. Opening a collection stats no recipe
and rebuilds no label; a source with no stored resolve is reported as unresolved instead.

Because a build runs arbitrary code out of the repo's own BUILD files — with the user's
privileges, since bazel actions are not guaranteed to be sandboxed — the bazel kind is
gated on **workspace trust**, copied from VS Code's Workspace Trust because the threat is
the same class as VS Code tasks: a committed `grpcview.json` naming a label means that
merely opening a repo can run `bazel build`. Trust is per workspace **root**, granted once,
and stored in user state (`<UserConfigDir>/grpcview/trust.json`) rather than in the repo,
where a `trusted: true` a repo commits about itself would say nothing. It is on the
**folder**, not on its content: a workspace trusted yesterday whose manifest changed today
is still trusted, because content-hashing the manifest would mean a prompt on every
`git pull` — which trains people to click through it — and would be theater anyway, since
the BUILD files a label reaches are not in the manifest.

The check lives inside the **one** constructor of a builder (`Workspace.bazelBuilder`),
i.e. next to the exec, so no future caller can reach a build by another path; and
`bazel.root` must be an ancestor of, or inside, the trusted root, since trust covers one
root and a build whose cwd is an unrelated repo would run *that* repo's BUILD files.
Untrusted is a working state and not a broken one: everything still loads, reflection and
upload sources still resolve, and only a build is refused — with the reason on that
source's `Resolved.error`, the existing channel for a source that cannot resolve. Revoking
un-resolves nothing: the bytes already acquired stay exactly where they are and only future
builds are refused, which is what makes revoking a safe thing to do. The wire surface is
two names, `ListCollectionsResponse.trusted` (it rides along on the call a client makes
first) and `SetWorkspaceTrust`; and both the UI's banner and `sources ls`' note appear only
when the workspace is untrusted **and** a bazel source is actually listed, because a
permission prompt for a capability nobody is using is what teaches users to click through
one.

## The collection tree

`ui/src/components/tree/` is a **hand-rolled, domain-agnostic tree component** —
not a library wrapper, and it knows nothing about gRPC. `features/workspace/`
supplies the gRPC half (`request-tree.tsx`'s adapter + row renderer,
`CollectionPanel.tsx`'s callbacks). Keep that boundary: everything in
`components/tree/` must remain reusable by a second tree (the descriptor explorer
is the intended one), which is what forces every gRPC-shaped decision out into the
host.

- **One contract, two row tiers** (`types.ts`). A `TreeAdapter<T>` supplies
  `getId` / `getChildren` / `getCollapsibleState` / `getParent` / `getTreeItem`. A
  caller that supplies only that gets the **portable** tier: a default row built
  from `getTreeItem` (label, description, an abstract `IconToken`), renderable by a
  VS Code `TreeItem` too. Passing `renderRow` opts into the **rich** tier —
  arbitrary React per row, standalone-only. The request tree is rich (method-kind
  tags, hover buttons); a portable provider must avoid `renderRow`, stick to the
  `IconToken` vocabulary, and enumerate its `kind` strings.
- **The flat visible-rows array is the load-bearing decision.** `flatten.ts`
  reduces roots + the expanded set to an ordered `TreeRowModel[]` plus an id→index
  map, and *every* behavior — arrow keys, range select, drop targeting — is array
  arithmetic over it, never recursion. A node's own `"expanded"` default is
  *reported* by `flatten`, never self-applied; `resolveExpansion` folds it in
  synchronously during render (so there is no collapsed first frame) and
  `useTreeState` remembers which ids it has force-opened so a manual collapse is
  never sprung back open.
- **State is controlled, owned by zustand** (`ui-store.ts`: `treeExpanded`,
  `treeSelection`, `treeFocused`) so it survives re-renders and view switches. Each
  pair is independently controlled; omit one to fall back to internal state. The
  range-select anchor is internal by design.
- **Decisions are pure functions; `Tree.tsx` is a thin interpreter.** `keymap.ts`
  maps a keystroke (+ `isMac`) to an intent with no DOM; `dispatch.ts` maps an
  intent *or* a click *or* a twistie click to a plain `TreeAction[]`; `navigate.ts`
  does the index math; `dnd.ts` does the drop geometry. `Tree.tsx` builds events,
  measures the DOM, and applies actions in one place. New interaction behavior
  belongs in the pure module, with a unit test — the suite has **no jsdom**, which
  is exactly why the decisions are DOM-free.
- **Keyboard/mouse follow VS Code per platform**, verified against the vendored
  monaco sources in `ui/node_modules/monaco-editor/esm/vs/base/browser/ui/`
  (`listWidget.js`, `listView.js`, `abstractTree.js`) — cite them when changing
  behavior. Arrows/Home/End/PageUp/PageDown move a *logical* cursor: DOM focus
  stays on the `.tree` container with `aria-activedescendant` naming the row, never
  a roving per-row tabindex. `Enter` is platform-split (macOS renames, `cmd+↓`
  opens; elsewhere `Enter` opens), `F2` renames everywhere, `shift`+click/arrow
  extend from the anchor, `cmd/ctrl`+click toggles, `cmd/ctrl+A` selects all
  visible, `Escape` clears. Paging measures the nearest scrollable ancestor, not
  `.tree` (which has no bounded height of its own).
- **Rename is the component's** (`RenameInput.tsx`): it renders the box, validates
  against the row's visible siblings, commits on Enter/blur, cancels on Escape, and
  reports exactly once as `onRenameCommit`. The host only persists it; the server
  stays the collision authority. `TreeHandle.startRename(id)` is how an outside
  affordance (the row pencil, the context menu) starts one.
- **The context menu is the host's.** The tree selects/focuses the row and hands
  over `(nodes, ev)`; `CollectionPanel` renders `components/ui/Menu.tsx`, because
  the items are gRPC-shaped. Empty-space right-click is the panel's own handler,
  guarded on `defaultPrevented`. Its items come from `collection-menu.ts`, keyed off
  the selection: no rows means the collection root — and, behind a separator, the one
  workspace-level item ("New collection…"), since a collection row unwraps to no item
  and lands on that same menu.
- **Drag and drop is native HTML5, no library.** A row is `draggable`; every other
  drag event is delegated to the container, which recovers the row from
  `data-index` (monaco's own structure). Geometry: a folder row splits into
  quarters — outer quarters are *between-rows*, the middle half is *into* — and a
  leaf splits in half, since there is no inside of a request. `after` an **expanded**
  folder means position 0 *inside* it, because that is where the indicator line
  visibly sits. `into` washes the row; between-rows draws a 2px accent line
  **indented to the destination's depth** (a full-width line cannot say which parent
  the item lands in). The dragged set is the selection if the drag started on a
  selected row, else that row alone. The tree rejects the structurally impossible
  (into a leaf, into a dragged node's own subtree, a no-op); the host's `canDrop`
  covers only what the tree cannot see — a destination that already holds the same
  display name, whose children may be collapsed or filtered out. One `MoveItem` per
  moved item; a `new_path` resolving to the current parent is a pure reorder, so
  reorder and reparent are one call. A multi-row move is the **one batch in this app
  that is sequenced** rather than fired concurrently (each call from the previous
  one's `onSuccess`): every call carries the same `before`, so the order the server
  processes them in *becomes* the persisted sibling order. Do not "simplify" it back
  into a loop.
- **The identity hazard: `itemKey` is path+name derived**, so a rename *or a move*
  changes an item's key — and for a folder, every descendant's. Any such mutation
  must call `ui-store.ts`'s `moveSubtree(oldKey, newKey, newName)`, which prefix-remaps
  `openTabs` / `drafts` / `invokes` / `treeSelection` / `treeFocused` / `treeExpanded`.
  Getting it wrong silently detaches an open tab from its draft and last response,
  which reads as lost work rather than as a bug. There is one remapper; do not write
  a second.
- **Still outstanding: typeahead (T3).** Letter keys are deliberately unclaimed by
  `keymap.ts` — they fall through untouched — and there is no `typeahead.ts`. The
  intended behavior is VS Code's: jump focus to the next label match, 1s buffer,
  wrap-around, composing with (not replacing) the header filter box. Also unbuilt:
  compact folders (the `compactFolders` prop is accepted and does nothing), sticky
  scroll, virtualization, and the async `getChildren` promise path (`flatten` and
  `reveal` both throw loudly on a thenable rather than silently dropping a branch).

## The workspace and its collections

**A workspace is a repository; the collections are what's in it.** Three tiers:
the workspace root owns no requests, a collection is the unchanged unit
(`grpcview.json`, `tree/`, `scripts/`), and a request is unchanged. The plan is
`docs/design/shipped/vscode/phase-1-workspace.md`, and all of it — sub-phases 1a through 1e —
is shipped.

- **The root is found by walking up** (`service/wsroot`): `--workspace <path>`
  wins, else the nearest ancestor of the cwd holding `.git`, else the cwd with a
  warning on stderr. `--workspace` names a **directory**, not a collection — the
  flag that names a collection is `--collection`.
- **A collection is addressed by its path** relative to the root
  (`services/payments/requests`, `.` for the root itself), and that id is never
  its display name: the id is the disk location and is never written to disk, the
  name lives in the manifest and defaults to the directory's base name.
  `store.Open` cleans the wire-supplied id, rejects absolute paths and `..`
  escapes, and keys its handle map **case-folded**, because `Requests` and
  `requests` are one directory on macOS and `Collection.mu` is the only write
  serializer.
- **Nothing is created that the user did not ask for.** `CreateCollection` is the
  only thing that creates a collection; every other handler returns `NotFound`. An
  id joined onto a repo root means a typo'd `--collection` would otherwise
  materialise a collection among project files. Three surfaces reach it — `grpcview
  init [dir]`, the UI's empty state (`NoCollection`), and `NewCollectionDialog` from
  either the TopBar collection picker or the tree's empty-space context menu. The
  dialog suggests **no** directory, unlike the empty state, which offers the path
  that just came back `NotFound`: with nothing already asked for, a default of `"."`
  would scatter a `grpcview.json` across the repo root on a stray Enter.
- **Both of a collection's names are editable, and they are different jobs.**
  `UpdateCollection` takes `name` and `new_collection`, each proto3-optional so an omitted
  field is left alone (an *empty* `name` resets it to the directory's base name).
  `Collection.SetName` is a manifest write; `new_collection` is a **move**, so `Store.Rename`
  `os.Rename`s the directory and then moves the collection's local state dir — run history
  lives there — log-and-continue, because the directory move is already committed and
  returning an error would be a lie. It refuses `"."` on either side (a collection at `"."`
  *is* the workspace root, so moving it would move the workspace), an existing destination, a
  destination inside the source, and a destination nested under another collection (a
  collection is a leaf; the scan prunes at a hit, so a nested one would be invisible). Both
  cached handles are dropped from `Store.colls` before re-`Open`, since a handle caches
  `root`/`state`/`id` and a stale one keeps addressing the old path, and the handler
  invalidates the definitions memo under the **old** key while it is still addressable.
  Client-side, the id is the first segment of every `itemKey`, so `ui-store.ts`'s
  `renameCollection` runs the **same** prefix remapper `moveSubtree` does (`remapKeyedState`
  — there is still exactly one) plus `OpenTab.collection` and `activeCollection`; the old
  id's `Get` entry is then dropped, *after* the remap, or a live observer still pointed at it
  refetches and gets `NotFound`.
- **Inside a collection the same split repeats: the directory slug is identity, the
  display name is data.** A rename writes `meta.Name` and leaves the directory alone —
  for requests (`store/fs.go`) and folders (`store/fs.go`) alike. So renaming a request
  from `test-goals` to `smoke` leaves `tree/…/test-goals/request.json` holding
  `"name": "smoke"`, and that drift is **intended**: the slug is what UI state,
  `Item.slug` and every on-disk reference are keyed by, and re-slugging would churn git
  history on every rename. Do not "fix" it.
  **A request directory holds three files** — `request.json` (identity, service, method,
  middleware, target), `body.ts` and `metadata.ts` — and all three move together on a
  rename or a move, because the directory is what moves. `body.ts`/`metadata.ts` are
  reserved slugs, so no child directory can collide with them, and a body-only patch does
  **not** rewrite `request.json`: a keystroke in the body editor must not touch a file it
  has nothing to do with. `readChildren` is an ordering pass and deliberately does not
  read either file — only `readItem` and `ResolveRequest` do.
  **Scripts are the deliberate exception**: a script has no display name at all, its path
  *is* its identity, and renaming one moves the file (`store/scripts.go`). That is what
  makes it importable — an import specifier has to name something stable on disk.
- **The TopBar picker is where a collection is switched**, and switching is pure UI
  state (`activeCollection`) because every query key is built from the active id —
  no reload, no refetch of anything but the collection now addressed. A row is
  labelled by name, with the id appended only when another collection shares that
  name, since a collection is addressed by its path and named separately.
- **Local state lives outside the collection directory** — one durable
  per-workspace state root under `os.UserConfigDir()` keyed by a hash of the
  root's absolute path (`wsroot.StateDir`), holding the resolve caches and run
  history. Not `os.UserCacheDir()`: history is user data.
  A collection directory is therefore 100% committed content, and there is nothing
  left to `.gitignore`. **`GRPCVIEW_CONFIG_DIR` moves that root**, and the trust
  list with it (`wsroot.configRoot`), and the daemon registrations too — that is
  what a throwaway run uses
  (CI, `example/README.md`'s throwaway run). Overriding `HOME` would do the same to grpcview
  and *also* relocate the output base of the `bazel build` a bazel source shells
  out to, which is a different request entirely.
- **Discovery is declared-or-scanned** (`store.List`). A `grpcview.work.json` with
  a non-empty `collections` list wins (globs allowed; a glob matching nothing is
  fine, a missing *literal* is reported as a row carrying an error). Otherwise the
  root is scanned for `grpcview.json`, pruning dot-directories, `node_modules`,
  `bazel-*` (symlinks into an unbounded output base) and anything gitignored, and
  **pruning at a hit**: a collection is a leaf, nested collections are not a
  supported shape, and that invariant is what makes `<collection id>/<path>` an
  unambiguous key later. Past 20k directories it fails with
  `ErrWorkspaceTooLarge` rather than hanging on a `$HOME` that happens to be a
  repo.
- **The scan is not cached, deliberately.** It was memoized to `collections.json`
  in the state root keyed by the workspace root directory's *own* mtime, which a
  collection created at any depth below the root never changes — a hand-written
  `grpcview.json` or one arriving on a `git checkout` stayed invisible until
  something unrelated touched the root, and no `InvalidateList()` call can cover a
  writer that is not grpcview. There is no cheap fingerprint of "the set of
  `grpcview.json` files": computing one *is* the scan, which costs ~130ms on a
  5k-directory monorepo. A warm in-memory listing invalidated by filesystem events
  is [the daemon's](docs/design/shipped/daemon.md) to hold; a one-shot CLI process
  can neither keep it nor watch for changes.
- **go-git supplies only the ignore matcher.** `gitignore.ParsePattern` +
  `NewMatcher`, accumulated per directory as the scan enters it. Its
  `ReadPatterns` helper is deliberately unused: it does its own recursive
  `ReadDir` of the whole tree, descending `node_modules` and the `bazel-*`
  symlinks before our cap can apply.
- **`ListCollections` is cheap by construction** and is the only thing a client
  needs before it knows which collection to `Get`: it reads manifests, never
  trees. Never `Get` every collection eagerly — `Collection.descriptor_set` is a
  merged `FileDescriptorSet` in bytes.

## The CLI

`grpcview` with no subcommand still serves the UI + API. Everything else is a
cobra verb in `service/cli/`, on the **same binary** — the embedded UI is 26.9 MB
of the 51.5 MB binary, so a second CLI binary would duplicate ~20 MB of Go.

```
grpcview                                serve the UI + API (default), open a browser
grpcview serve [--port 10000] [--idle-timeout <d>] [--no-open]
grpcview url | open | shutdown          address this workspace's daemon
grpcview invoke <request-path>|<service>/<method>
grpcview describe <service>/<method>    [-o proto|json]
grpcview ls [<folder-path>]             [-o text|json]
grpcview get
grpcview sources ls | add | commit | refresh | rm | reorder
grpcview trust [--off]                  allow sources that run a build
grpcview request create | rm | mv        grpcview folder create
grpcview script ls | run                 grpcview completion bash|zsh|fish
grpcview init [dir] [--name]            create a collection
grpcview collections ls                 [-o text|json] [--refresh]
grpcview mcp                            serve MCP over stdio
```

The reason it exists is one verb: **run a saved request from a shell, with an exit
code that reflects the gRPC status.** The rest is in service of that.

Every verb takes `--workspace <root>`, `--collection <id>`, `--server <addr>` and
`--in-process`.

**The workspace root resolves in one place, `wsroot.Discover`, in this order:** explicit
`--workspace`, else **`$BUILD_WORKSPACE_DIRECTORY`**, else the nearest ancestor of the
current directory holding `.git`, else the current directory with a warning. The env var
sits second, not as a seed for the `.git` walk, so a bazel workspace nested inside a larger
repository resolves to the bazel workspace rather than the outer repo's root. `bazel run`
sets that variable and replaces cwd with the runfiles tree, so without this every verb run
that way — `ls`, `collections ls`, `mcp` — would walk up from `bazel-out` and find nothing.
`wsroot.InvocationDir` answers the adjacent but different question of *which directory the
user is standing in* (which collection does it address); it is not the root.

- **`service.Run` does not own argv.** It takes a `service.Options`; the CLI (or
  `dev`'s own two-line flag set) parses. The flag is `--port`: pflag reads
  Go-flag style `-port` as the shorthand cluster `-p -o -r -t`, and there is no
  alias. It *does* own lifecycle — bind, publish, drain, idle out — see "The
  workspace daemon".
- **`service/cli` must not import `//service`.** The UI embed lives in
  `//service/cmd`, and that edge would drag 26.9 MB of `embedsrcs` into every CLI
  test. `cli.Main` receives a `serve` closure instead.
- **One `Client` interface, three bindings, and the wire is the default.** The
  interface and its bindings live in `service/wire`, not in `cli`, because
  `service/mcp` takes the same value: local (`workspace.Workspace` called as a
  plain Go value), pinned-remote, and reconnecting-remote. A verb talks to this
  workspace's daemon, starting one if none is running; `--server <addr>` pins a
  specific one and `--in-process` starts nothing. "Dial the local server if one happens to be
  listening" is still rejected — that was ambient, this is addressed: the
  registration is keyed by workspace root and re-verified over the wire, so
  *which process wrote my history* is always this workspace's one. Unary RPCs need no adapter — the handler and
  the generated client have the same signature, asserted at compile time. Only
  streaming differs (a handler takes `*connect.ServerStream`, a client returns
  `*connect.ServerStreamForClient`, and connect cannot build the former outside a
  served request), so `Client` declares the callback shape a CLI wants and
  `workspace` exports `InvokeStream`/`InvokeSavedStream` in send-func form.
- **Exit codes are the contract.** `0` = the call returned status OK; `1` = it
  returned any other gRPC status (which arrives *inside* `Request.Response.status`
  with a nil error); `2` = grpcview's own failure, nothing invoked. That 1-vs-2
  line is exactly the Connect-error-vs-status-in-payload line the backend already
  draws, so it needs no new classification — but it does need the invariant to
  hold, which `invoke_saved_test.go` pins directly.
- **Where you stand decides what you address**, like `git` and `bazel`. With no
  `--collection`, a verb resolves one: the nearest collection at or above the cwd
  bounded by the workspace root, else the workspace's only collection, else **exit
  2 listing the candidates** — never a guess, since guessing runs a request
  against the wrong service. `withCollection` (`service/cli/write.go`) is the one
  seam that resolves; no verb reads the raw flag. `init` is the exception and
  resolves its own address, because it runs in a workspace that may hold zero
  collections. `collections ls` marks the row the cwd resolves to with `*`, and
  marks nothing when the answer is ambiguous.
- **stdout is data, stderr is everything else.** Latency, status text, warnings
  and `describe`'s source id are stderr. `-o body` (default) prints nothing on a
  failed call; `-o json` prints the whole `Request.Response` either way, because
  there the status *is* the data. Streaming prints NDJSON. A mutation prints
  **nothing** and exits 0 — silence is success. No colors, no TTY detection, no
  pager, permanently. `-o` is per verb with disjoint value sets, never persistent.
- **Structured input only, never per-field flags** (`PodSpec` has hundreds of
  fields and kubectl has no `--containers-0-image`). Bodies arrive as `-f file`,
  `-f -`, or a bare pipe, and the bytes are passed through **unchanged** —
  `resolveInvokeBody` normalizes protojson and TypeScript at one seam, so `-f`
  behaves identically for `body.json` and `body.ts`. For a client-streaming or
  bidi method stdin is NDJSON (one message per line); for every other kind it is
  one message verbatim, since a TS module is multi-line. That asymmetry is the
  sharpest trap in the verb.
- **`invoke`'s argument is resolved against both interpretations** — a saved-request
  path and a `service/method` — off a single `Get` snapshot. One that matches both
  is exit 2, never a guess: catching `NotFound` from `InvokeSaved` cannot work,
  because a miss on one interpretation says nothing about the other. Paths split
  through `workspace.SplitInvokePath`, the same parser `invoke()` uses.
- **`InvokeSaved`/`InvokeSavedStreaming`** are the addressed counterparts to
  `Invoke`: they resolve the *saved* body, metadata script, middleware and target
  server-side, take `params` (reaching scripts as `params` from `grpcview:request`), record
  history by default, and support `dry_run`, which stops after the shared pre-send
  steps and reports the **evaluated** request without dialing. `resolveSavedRun` is
  the one place a saved request becomes an `invokeSpec`, shared with `invoke()`.
- **`describe` never dials.** It answers from the workspace's cached merged
  descriptor set, so it works from a box with no route to the target, and it
  reports which source it read: doc comments survive only if that source carried
  them (reflection strips `source_code_info`, a `buf build` upload keeps it), so an
  empty-comment result must be attributable. `-o json` is the protojson of a
  `FileDescriptorSet` — the descriptors themselves, not an invented flat field
  list, which would be a lossy re-encoding of a standard format.
- **`sources add` reads its kind out of the argument**, and the first test is the
  filesystem: an argument that stats as a file is uploaded as a `FileDescriptorSet`,
  with its **absolute** path riding along as the refresh recipe (the server confines
  that against the workspace root and stores it root-relative, so it cannot be
  resolved against this process's cwd); one that starts with `//` or `@` is a bazel
  label; anything else is dialed as a reflection address. Bazel's `pkg:target`
  shorthand is deliberately **not** accepted and cannot be — `localhost:8080` is
  indistinguishable from it, so whichever way the verb guessed, it would sometimes
  dial a label and sometimes try to build an address.
- **`grpcview trust [--off]`** grants or revokes trust for the workspace root and
  prints nothing on success. It resolves no collection, deliberately: trust is a
  property of the root, and a repo with no collection in it yet is a perfectly good
  thing to trust. The *note* about an untrusted workspace lands where it is
  actionable — `sources ls` prints one line after the table when the workspace is
  untrusted **and** at least one listed source is a bazel source, which costs it one
  extra (cheap) `ListCollections`. `collections ls` says nothing about trust at all,
  also deliberately: it reads manifests and never source lists, so it cannot know
  whether anything here would build, and a permission prompt for a capability nobody
  is using teaches people to click through it.
- **`--timeout` defaults per verb, because one of them builds.** 30s everywhere,
  except that `sources refresh` and `sources add` of a bazel *label* default to 10
  minutes when the flag was not passed — a cold build of a large target is minutes,
  and a bare refresh spends that one budget across every source it walks. `sources
  add` of an *address* keeps the 30s default: the reflection dial has no timeout of
  its own and would otherwise hang. Passing `--timeout` explicitly always wins, in
  either direction.
- **One writer, because everything routes through one process.** The default path
  puts every verb — `mcp` included — on this workspace's daemon, so
  `Collection.mu`, which is in-process only, is again the only serialization
  anyone needs. `--in-process` is the documented way back to two writers on one
  directory (`--no-history` removes the only write `invoke` performs). Nothing
  but a human with an editor writes a collection behind the daemon's back.

## The workspace daemon

Bazel's client/server model, copied whole rather than just its port file: **a CLI verb
connects to the workspace's server if one is running, starts one if not, and the server
exits after a few hours of inactivity.** The design and the calls behind it are
[`docs/design/shipped/daemon.md`](docs/design/shipped/daemon.md).

The payoff is that every surface ends up in one process, so "one workspace, one writer"
stops being aspirational — and the linked-descriptor cache (`definitionsCacheSize`, an LRU
of 16) and the compiled QuickJS engine stay warm between invocations instead of being
rebuilt per `grpcview invoke`.

- **The registration file is the rendezvous, and it is a hint, never an authority.**
  `<cache>/grpcview/servers/<sha256 of the absolute root>.json`, `0600` inside a `0700`
  directory, holding port, pid, root, and the executable's identity. Not inside the
  workspace (a read-only or network-mounted checkout breaks it) and not bare `/tmp` (mode
  1777, shared between users). `GRPCVIEW_CONFIG_DIR` moves it, so a throwaway run cannot
  adopt the developer's daemon. A client checks pid-alive → connects → **verifies the
  server reports the same workspace root**; pid reuse and hash collisions both die there,
  and anything that fails is treated as stale.
- **The port defaults to `10000` and falls back.** A busy default takes an ephemeral port
  instead of failing; a busy `--port` is an error. So the single-workspace case keeps a
  guessable URL, a second workspace still starts, and nothing downstream may assume a port
  — `grpcview url` is how you learn it. Bind first, publish second: the port written down
  is read off `l.Addr()`.
- **`ServerService` (`ServerInfo`, `Shutdown`) is a second service on purpose.** They are
  properties of a *process*, not of a workspace, and putting them on `WorkspaceService`
  would have registered them as MCP tools. In-process, `ServerInfo` describes this process
  and `Shutdown` is `Unimplemented` rather than a silent success.
- **Nothing signals a pid the connect has not vouched for.** `grpcview shutdown` and the
  version-skew restart ask **over the wire**, after identity is verified; `SIGTERM` is the
  last resort for a process that answered and then refused to leave.
- **Startup is locked, and a failure is never a hang.** An advisory `flock` covers
  *check → spawn → wait → connect* and is released once connected — a rendezvous lock, not
  a command lock, so concurrent verbs are not serialized. The spawn is a detached self-exec
  (`serve --workspace <abs root> --no-open --idle-timeout <d>`, `Setsid`, stdio to
  `<cache>/grpcview/servers/<hash>.log`); the child inherits one end of a pipe it never
  learns about, so a crash is EOF in milliseconds rather than a 10s timeout, and the failure
  path prints the log tail and exits 2.
- **`cwd` never crosses the wire.** The client resolves the collection to an id and spawns
  with an absolute root; the daemon's own cwd is whatever shell first started it.
- **Version skew restarts the server, keyed on the *binary*, not the version string.**
  `version` links `"dev"` for every unstamped build, so a string compare would miss exactly
  the rebuild you just did. The registration carries `os.Executable()` + mtime + size, and
  any change restarts — with one line on stderr, because a daemon serving last hour's code
  is a trap that looks like success.
- **Idle exit is a counter, not a timestamp.** The clock runs from the *last* request, not
  from startup, and the deadline is only armed when nothing is in flight, so a
  server-streaming invoke that outruns it survives to completion (verified: 100 frames over
  12s against a 5s timeout). The default is **1 hour since last use**. **Only an
  auto-spawned server idles out** — a
  hand-run `grpcview` keeps running until stopped. That is the same predicate as the
  browser: an explicitly launched server opens one and lives; an auto-spawned one is silent
  and dies. `grpcview invoke` must never pop a tab.
- **`--no-open`, and degrade rather than fail.** No `DISPLAY`, a headless box or an SSH
  session prints the URL and carries on. `grpcview url` prints to **stdout** so it stays
  scriptable (`open "$(grpcview url)"`); `grpcview open` names what it launched on stderr,
  since the launch is the action.
- **Every surface is a client of it, `grpcview mcp` included** — `service/wire` holds the one
  `Client` interface and its bindings so `cli` and `mcp` take the same value, and the daemon
  binding *reconnects* so a long-lived MCP session survives an idle-out or a skew restart.
- **Repairing the connection and replaying the request are two decisions, not one.** Every
  failure that is the connection's rather than the server's re-runs connect-or-spawn, because
  the *next* call must not find the same dead server. Only then does replay come up, and it
  is narrow: a **dial** failure proves nothing was written to a socket, so anything may run
  again; a connection that broke **in flight** proves nothing at all — the write may already
  be on disk — so only reads (`Get`, `ListCollections`, `ListBazelTargets`, `DescribeMethod`,
  `ServerInfo`, marked by `read` rather than `call` in `service/wire/reconnect.go`) run
  twice. A client timeout is neither: a caller who gave up must not spawn a daemon, so
  `classify` checks `ctx.Err()` first. Anything the server itself answered, including its own
  `Unavailable`, is returned untouched.
- **A quiet client heartbeats, because silence is what arms the idle timer.** A connected but
  idle session is indistinguishable, server-side, from no session at all. `wire.Keepalive`
  beats `ServerInfo` — a real request on the same idle timer, no separate liveness path —
  every `idle/3`, clamped to [30s, 10min] and retuned from the timeout the server reports.
  `grpcview mcp` runs it, so a daemon outlives its agent by one idle window and no longer;
  the browser runs its own in `ui/src/lib/keepalive.ts`, since `grpcview open` spawns a
  daemon that would otherwise die under a tab left open. A beat that fails repairs on its own
  schedule, so a session quiet for an hour is already pointed at a live server when its next
  real call arrives. A server reporting no idle timeout ends the loop — nothing to hold open.
  The one case a heartbeat cannot cover: a fully backgrounded tab has its timers frozen, and
  a page cannot start a process, so it beats again on `visibilitychange` and that is the
  ceiling.
- **`--in-process` is bazel's `--batch`** — the escape hatch for CI, a read-only checkout,
  and debugging. A run with a throwaway `GRPCVIEW_CONFIG_DIR` wants it for the setup calls it
  makes before trust has been granted, and because a daemon would outlive that state directory.
- **The dev flow stays out of the registry entirely.** `ui/src/lib/client.ts` hardcodes
  `http://127.0.0.1:10000` when `import.meta.env.PROD` is false, so `//service/cmd/dev`
  pins that port and registers nothing: it serves a dummy index page and is a different
  binary, so a registered dev server would be shut down as skew by the next CLI verb.
- **The daemon's environment is frozen at spawn**, and `exec.Command("bazel", …)` for a
  bazel source resolves `PATH` as the spawning shell had it. Nothing else reads the
  environment (`service/scripting/` has no `os.Environ`/`os.Getenv`/`process.env`), and that
  is worth re-checking before adding one.
- **Not built: a filesystem watcher.** `store.List` still rescans on every call. Holding the
  listing in a daemon and invalidating from fsevents/inotify is the remaining payoff, and
  deliberately not the shipped shape — the mtime-keyed memo that preceded it missed
  collections created below the root, so a cache that reintroduces that is worse than none.
- **The boundary is loopback + origin policy, and nothing more.** No token: the browser
  cannot read a file, so the server would have to inject it into the HTML anything can
  `GET`, which is hygiene rather than a boundary, and it defeats neither of the attacks
  usually cited for it. A unix socket for the CLI is the properly-scoped version and is
  still open — the browser cannot speak to one, so it would be a *second* transport, not a
  replacement.

## The MCP server

`grpcview mcp` speaks MCP over stdio on the **same single binary** as everything
else — no HTTP endpoint, no auth, because the transport is a pipe to a child
process the user (or their agent's config, see `.mcp.json`) launched directly.
`--timeout` does not apply to it: the verb calls `mcp.Run(cmd.Context(), ...)`
straight, bypassing `withSession`/`withCollection`, because an MCP session is
long-lived and the global 30s default would kill it mid-conversation.

- **Tools are grpcview's own unary RPCs**, registered at runtime from
  `WorkspaceService`'s `protoreflect.ServiceDescriptor` via
  `protoc-gen-go-mcp/pkg/gen.RegisterService` — **not** one tool per reflected
  target method. The dynamic-tool-per-target design was rejected: it reintroduces
  the JSON-schema-per-target layer this repo deleted (see the request-body-contract
  history), makes the tool list unbounded and mutable across a session, and
  duplicates what grpcurl already does better.
- **The plugin's protoc codegen is deliberately unused.** rules_go passes the
  `go_proto_library` `importpath` as the proto's `go_package`, and the plugin then
  appends `package_suffix` again, so the generated file imports itself. Runtime
  registration against a live descriptor sidesteps codegen entirely, and also buys
  the `CommentProvider` and `NewMessage` seams the static path has no equivalent
  for.
- **Streaming RPCs get two hand-registered tools**, `invoke_streaming` and
  `invoke_saved_streaming` (`service/mcp/streaming.go`). The plugin's
  `RegisterService` skips any method where `IsStreamingClient() ||
  IsStreamingServer()` returns true, but `gen.ToolForMethod` never asks whether a
  method streams — so the schema is still the plugin's and only the registration
  and the handler are hand-written. Both go through `shim.AddTool` under the
  generated name, so the rename map, `annotateSchema` and `defaultCollection` apply
  as they do to a unary tool, and `service/mcp` stays the one seam.
  - **Two tools, not auto-routing inside `invoke`.** `InvokeRequest` carries one
    `body string` and `InvokeStreamRequest` carries `repeated string messages`; one
    tool covering both would need a hand-merged schema and would let an agent send a
    body shape the method cannot accept.
  - **A tool call is request/response, so the handler drains the stream** and returns
    `{messages, result, truncated}` whole — under a frame cap, a byte cap and a
    deadline (200 / 256 KB / 60 s), all three stated in the tool description.
    Unbounded, a server stream is a context bomb with no Ctrl-C: an agent cannot
    interrupt a tool call. Whichever cap bites cancels the call's context so the RPC
    actually stops, and everything collected so far is still returned.
  - **Message frames are raw JSON, not base64**, unlike unary `invoke`'s `response`.
    `InvokeStreamingResponse.message` is `bytes` holding UTF-8 JSON, so `protojson`
    would base64 it; the streaming result is assembled by hand and does not inherit
    that. The inconsistency is deliberate until the `bytes` → `string` roadmap item.
  - The invoked call's gRPC status lives in `result`, never in the tool's error
    channel — the same rule as the CLI's exit codes.
- **A streaming method is flagged at authoring time, not at invoke time.**
  `notInvocableReason` (`service/workspace/describe.go`) is the one string, and three
  surfaces carry it: `describe_method` returns it as `not_invocable_reason`,
  `create_request` returns it in `warnings` (non-fatal — the request *is* created),
  and `grpcview ls` tags the row. Without that, `ls` listed a streaming request
  identically to a unary one and an agent found out only after saving. `create_request`
  resolves the method best-effort: a collection whose sources are cold must still be
  authorable, so an unresolvable method warns about nothing rather than failing.
- **`service/mcp` is the one seam for every payload rule**: it renames the
  plugin's generated tool names to short ones (a generated tool with no entry in
  the rename map panics on startup, and a totality test makes adding an RPC without
  updating the map a `bazel test` failure), drops the output schema entirely,
  annotates the input schema with the proto's own field comments, strips every
  `descriptor_set` out of every response (unstripped, it is megabytes of base64
  and would land in the calling agent's context on every single tool call), and
  defaults the `collection` argument so a tool call that omits it still resolves.
  The strip walks the whole response rather than one known key, because two
  unrelated shapes carry one: `Collection.descriptor_set` and, at the top level,
  `DescribeMethodResponse.descriptor_set`.
- **A non-synthetic oneof is flattened back into `properties`** (`hoistOneofs`).
  The plugin emits one as a message-level `anyOf` of `oneOf` groups and keeps its
  members *out* of `properties`; a client that flattens `anyOf` into a single
  property bag then shows the model nothing, which is why `add_source` could not
  add a reflection or a bazel source at all. The branches are hoisted after they
  are annotated, so their `.proto` comments come along, and each hoisted property
  says which oneof it belongs to. Nothing changes on the argument side: the names
  are real proto field names and `protojson` still rejects two members of one
  oneof. The plugin's OpenAI mode does the same flattening but also marks **every**
  field required and rewrites `google.protobuf.Struct` into a JSON string — not
  worth it for one message.
- **An oversized string inside a response body is elided** (`shrinkResponseBody`,
  8 KB). A proto walk cannot see into a string, and the worst offender is exactly
  that shape: a base64 descriptor set inside a JSON body inside a `bytes` field.
  One `DescribeMethod` invoke measured 119,061 characters and spilled to a file.
  Two message types carry a body and both are covered (`responseBodyOwners`):
  `Request.Response`, which an invoke answers with, and `History.Response`, which
  `get_collection` replays — the second is the larger source in practice, because
  every recorded call keeps its body and they accumulate. Nothing is special-cased by
  type — any string over the threshold becomes a marker naming the elided size and
  pointing at the underlying RPC; a body that is not JSON at all is capped whole.
- **A derived list survives only on the RPCs that can change it** (`fieldOwners`,
  `dropSet`). Every write RPC answers with the whole `Collection`, and most of it
  has nothing to do with the edit. `history` is the biggest offender — a recorded
  response can itself hold a base64 descriptor set, and one 9-request collection
  measured 160 KB of history in a 186 KB response, over the MCP client's per-result
  cap, so every mutation came back as an overflow error. It is now owned by no
  mutating RPC at all. `services` is owned by the five descriptor-source RPCs plus
  `set_workspace_trust`, `scripts` by the three script RPCs. `get_collection` is
  an agent's only access to `history` and keeps everything (`readMethod`).
  Measured on `example` (9 requests, compact JSON): the whole `Collection` is
  116.6 KB — 87.5 KB descriptor set, 16.0 KB tree, 9.3 KB history, 7.0 KB
  `services`, 5.5 KB `scripts`. A request edit's response was 19.8 KB after the
  descriptor-set and history strips and is 6.6 KB after this one. Returning only
  the touched subtree was rejected: it changes the RPC contract for every surface,
  not just MCP, to save the same 13 KB this does at the seam.
- **Descriptions and field docs come from an embedded descriptor set that retains
  `source_code_info`.** Generated Go protobuf strips that section, so
  `protoregistry.GlobalFiles` carries no comments at all — this is *why* proto
  comments elsewhere in this repo stay terse: the JSON Schema now carries the
  field-level detail, so the RPC-level comment does not have to repeat it.
- **`run_script` hands the calling agent arbitrary JS with `fetch` enabled.** That
  is named here as a known exposure, not mitigated — there is no sandboxing beyond
  what the scripting engine already does for the UI/CLI paths, and an agent with
  this tool available can reach the network from inside the collection process.
- **An MCP session is a client of the workspace daemon, like every other verb.**
  `newMcpCmd` opens a session through the same `openClient` and hands `mcp.Run` a
  `wire.Workspace`, so an agent's writes, the UI's and the CLI's all serialize on
  one `Collection.mu`. MCP is exempt from the *discovery* half and only that half:
  its client launches it over stdio, so it has no port to publish and nothing to
  find out about itself. `--in-process` opts a session out.
- **The session outlives its backend, so the binding reconnects.** A daemon idles
  out an hour after its last request and a rebuild restarts it, both of which would kill a
  long-running session. `wire.Reconnecting` re-runs connect-or-spawn on a **dial**
  failure only — proof the request never reached a server, so replaying it cannot
  duplicate a write — and anything the server itself answered is returned as-is.
- **`run_script` now runs in the daemon**, not in the MCP child. Same exposure
  (arbitrary JS with `fetch`), different process, and it outlives the session.
- **`run_script` takes `source` *or* `script`**, never both and never neither.
  `script` is a saved script's collection-relative **path** (`scripts/smoke.ts`), so a
  saved scenario no longer has to be pasted in to run from MCP. There is no kind: a
  source with `export default` is compiled as an entry and its default export is
  **called**; anything else is evaluated as a scratchpad whose value is its last
  expression.

## Views (no router)

The SPA has **no URL router**. `App.tsx` renders a single `AppShell` and switches
the main pane on a `zustand` store field (`activeView` in `lib/ui-store.ts`)
between three feature views:

- **Workspace** (`features/workspace/`) — the collection tree + request editor +
  response pane; the default view.
- **Sources** (`features/sources/`) — the priority-ordered definition-source list
  (see above): add / refresh / reorder / remove, with each source's contribution
  shown so a shadowed one is visible as such.
- **Scripts** (`features/scripts/`) — authoring a collection's `.ts` files, listed and
  addressed by path.

Server state is fetched via `@connectrpc/connect-query` on top of
`@tanstack/react-query`; local/view state lives in `zustand`.

## Verify through MCP or the CLI, not the browser

**Prefer the MCP tools first, the CLI second, and the browser last.** All three
surfaces share the whole backend — store, resolve, invoke, scripting, TS
body/metadata evaluation — so for anything below the React layer an MCP tool call
or a CLI run tests the same code the UI would, without a browser session. Driving
Chrome is *far* slower per check: tab setup, screenshots, DOM reads and click
round-trips cost multiples of what a verb (or a tool call) costs, and that overhead
lands on every iteration.

The CLI remains the right tool for what MCP doesn't cover: shell/exit-code checks
(`invoke`'s 0/1/2 contract, piping, `-o` variants), incremental streaming output
(MCP's streaming tools drain the whole stream before returning, and cap it), and
verifying the CLI's own argv/flag surface — none of which an MCP tool call
exercises.

**A collection that reflects grpcview describes a *snapshot* of grpcview.** The
`example/` collection targets `localhost:10000`, which is grpcview's own workspace
server, and its reflection descriptors are committed. After a `.proto` change the
snapshot still describes the old service, so `invoke` answers
`AddDescriptorSourceRequest has no known field named bazel` about a field that
exists — `refresh_source` (or `grpcview sources refresh`) fixes it immediately.
The same staleness applies to the server on the other end: `:10000` is held by
whichever daemon bound it first, so verifying a *server-side* change means killing
that daemon (or `grpcview shutdown`) and letting the new build take the port.

**A CLI check leaves a daemon running.** That is the design, not a leak — but a
rebuild is what makes it visible: the next verb restarts the server and says so on
stderr, and `grpcview shutdown` ends it outright. Add `--in-process` when a check
must not outlive itself (a temp `GRPCVIEW_CONFIG_DIR`, a workspace you are about to
delete).

The browser is the last resort, reserved for what only it can exercise: rendering,
Monaco behavior, the tree's keyboard/mouse/DnD semantics, focus and layout,
zustand/query-cache state, and any bug that does not reproduce from MCP or the
CLI. A change confined to `ui/` is a UI bug by definition — verify that one in a
browser (hook below), and say in the report which surface you used.

## Browser verification hook (editors)

When a change does need a browser, this hook makes driving it cheap. Because
several Monaco editors coexist (each with its own model) and there
is no global `monaco`, the request **body** and **metadata** editors register
themselves on a `window` map keyed by model URI (`ui/src/lib/editor-debug.ts`), so
the devtools console — or a browser-automation harness — can read and drive their
exact contents without reaching into React or guessing which DOM node is which:

- `window.__grpcviewEditors["file:///grpcview/request/body.ts"]` — the body editor
- `window.__grpcviewEditors["file:///grpcview/request/metadata.ts"]` — the metadata editor

Each value is a Monaco `IStandaloneCodeEditor`: `.getValue()` reads the exact buffer
and `.setValue(src)` drives it (the latter also sidesteps Monaco's auto-closing
brackets/quotes, which corrupt naively *typed* code — set the value instead of
typing it). App code only ever WRITES the map, so it is inert in normal use. The
Scripts scratchpad editor is not registered (its model is `SCRATCH_PATH`).

## Design language

The UI targets the **Nocturne** design system (dark, compact, token-driven; a
single blurple accent `#9184d9`, Inter for UI text, JetBrains Mono for code,
Phosphor icons, outlined actions, 8px radii, ~0.7× density). Tokens live in
`ui/src/theme/` (`nocturne.css`, `app-tokens.css`) and are consumed through
Tailwind utilities plus the design-system primitives in `ui/src/components/ui/`.
The reference is the "Nocturne" Claude Design project; the migration plan and
current status are in `docs/design/shipped/ui-redesign-plan.md`.

## Build System (Bazel)

Bazel drives building, testing, proto generation (Go + TypeScript), and embedding.

### Commands

- **Build the release binary** (standalone, frontend embedded):

  ```bash
  bazel build //service/cmd            # host arch -> bazel-bin/service/cmd/grpcview_/grpcview
  bazel build //service/cmd:release    # all four published arches
  ```

  `//service/cmd` is an alias for `//service/cmd:grpcview`. `:release` is a
  filegroup over one `go_cross_binary` per entry in `RELEASE_PLATFORMS`
  (`darwin_amd64`, `darwin_arm64`, `linux_amd64`, `linux_arm64`), each named
  `grpcview_<goos>_<goarch>`. Their outputs land in per-configuration
  `bazel-out/...-ST-<hash>/bin/` directories rather than `bazel-bin`, so locate
  them with `bazel cquery --output=files` rather than by guessing a path.

  The embedded UI is pinned to `@platforms//host` by a
  `platform_transition_filegroup`. Without that pin the cross transition would
  rebuild the vite bundle once per arch, and rules_js resolves rollup's native
  binding from the target platform, so every non-host arch fails on a missing
  `@rollup/rollup-<os>-<cpu>`.

- **Build & test everything:**

  ```bash
  bazel build //...
  bazel test //...
  ```

- **Run the dev backend** (serves the API without embedding the frontend):

  ```bash
  bazel run //service/cmd/dev          # -port 10000; dev is serve-only, no verbs
  ```

- **Run the frontend dev server** (Vite):

  ```bash
  bazel run //ui:dev
  ```

- **Regenerate TypeScript proto types** (run after editing any `.proto`):

  ```bash
  bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy
  ```

  This copies the regenerated `.d.ts` declarations into the source tree. The
  runtime `_pb` modules are Bazel-generated and not committed.

### Releasing

```bash
tools/release.sh --bucket gs://BUCKET            # or set GRPCVIEW_RELEASE_BUCKET
tools/release.sh --bucket gs://BUCKET --dry-run  # build and stage, upload nothing
```

It builds `//service/cmd:release` with `--stamp -c opt`, stages the four
binaries with a `SHA256SUMS`, and uploads them to
`gs://BUCKET/grpcview/<version>/`, then rewrites `gs://BUCKET/grpcview/latest`
to hold the version string. Version directories are treated as immutable —
uploaded with a one-year `Cache-Control` and refused if they already exist,
unless `--force`. A modified worktree is refused unless `--allow-dirty`.

**`tools/install.sh` is the public installer**, published to the same bucket:

```bash
curl -fsSL https://storage.googleapis.com/BUCKET/grpcview/install.sh | sh
```

It resolves `latest` (or `--version vX.Y.Z`), picks `grpcview_<goos>_<goarch>`
from `uname`, verifies the download against the published `SHA256SUMS`, and
installs it as `grpcview` in the first writable directory of
`$GRPCVIEW_BIN_DIR`, `/usr/local/bin`, `~/.local/bin` — `--bin-dir` overrides.
It writes to a temporary name in that directory and renames, so upgrading a
running binary can't hit `ETXTBSY`. `--list` resolves and prints without
installing.

When the chosen directory is not on `PATH`, the hint it prints is keyed off
`${SHELL##*/}`, the user's **login** shell rather than the `/bin/sh` running the
script: fish gets `fish_add_path`, csh/tcsh get `setenv`, and only unrecognized
shells get a bare `export PATH=`. Printing POSIX syntax to a fish user is advice
that silently does nothing.

**`tools/uninstall.sh` is the counterpart.** By default it only deletes
binaries — every `grpcview` it finds in `$GRPCVIEW_BIN_DIR`, `/usr/local/bin`,
`~/.local/bin`, `/opt/homebrew/bin`, `~/bin`, plus whatever `command -v
grpcview` resolves to. `--purge` also deletes the state directory:

```bash
curl -fsSL https://storage.googleapis.com/BUCKET/grpcview/uninstall.sh | sh
tools/uninstall.sh --purge --dry-run     # show what a purge would delete
```

The state root is `os.UserConfigDir()/grpcview`, which the script recomputes
from `$XDG_CONFIG_HOME` / `~/Library/Application Support` rather than asking the
binary — the binary may already be gone. **It is not a cache** (see the comment
at `service/wsroot/wsroot.go:59`): a purge loses the workspace trust list,
cached descriptor blobs, and run history. Collections, requests, and scripts
live in the user's repositories, so a purge never touches them. A state root
moved with `GRPCVIEW_CONFIG_DIR` is deliberately **not** purged: the guard here
demands a path ending in `/grpcview`, and the override exists for throwaway
directories that clean themselves up.

Both scripts are POSIX `sh`, not bash, because they run under whatever `/bin/sh`
the target machine has. Guards worth keeping:

- The installer's release root is a `@BASE_URL@` placeholder that `release.sh`
  substitutes at upload time — the bucket is only known there — so the
  checked-in copy needs `--base-url` or `$GRPCVIEW_INSTALL_BASE_URL` to run, and
  `release.sh` fails the upload if a placeholder survives. The installer never
  compares `$BASE_URL` against the placeholder text: that substitution is
  global, so a guard naming the placeholder would itself be rewritten into one
  matching the real URL. It checks for an `http(s)://` scheme instead.
- Deletions are confirmed from `/dev/tty`, not stdin, since stdin is the curl
  pipe. The open is tested in a subshell first: `/dev/tty` can pass `[ -r ]` and
  still fail to open, and a failed redirection on the main shell would kill the
  script with a raw error. No terminal and no `--yes` means refuse, never
  assume yes.
- A purge target must be a nested directory literally named `grpcview` that is
  neither `$HOME` nor `/grpcview`, and must hold a `trust.json` or a
  `workspaces/` — otherwise it takes `--force`. Symlinked binaries are skipped
  by default too, since a symlink is how Homebrew or Bazel owns a name in `bin`.

Each release directory keeps an immutable copy of both scripts as they shipped;
the top-level copies are the URLs users curl and, like `latest`, are uploaded
`no-cache`. They also get `text/plain` rather than the guessed
`application/x-sh`, so they can be read in a browser before being piped to a
shell.

**Versions come from `tools/version.sh`**, which `tools/workspace_status.sh`
stamps into `STABLE_VERSION_TAG` and thence into `cli.version` — what both
`grpcview version` and `grpcview --version` print. The flag exists because
setting `root.Version` is the only thing that makes cobra accept `--version` at
all; its version template is overridden to drop cobra's `grpcview version `
prefix so the flag and the verb emit the same single line. An exact `vX.Y.Z` tag on HEAD wins; otherwise it emits a Go
pseudo-version — `v0.0.0-20260806152233-1a2b3c4d5e6f` with no tags in the repo,
or `v0.1.1-0.<timestamp>-<sha>` once `v0.1.0` exists. That is the canonical
date-based version for an untagged commit: the timestamp is the commit time in
UTC, so the strings sort chronologically, and they compare as semver
prereleases below the tag they build on, which keeps `go get` and any semver
range check honest. A dirty worktree gets a `+dirty` suffix.

Stamping is opt-in per build. `.bazelrc` deliberately omits `--stamp` because
it would cost a remote-cache hit on every stamped target on every commit; an
unstamped build leaves `cli.version` at its `dev` default.

### Frontend gates

Three, and they check different things — a change to `ui/` isn't verified until
all three are green:

```bash
cd ui && ./node_modules/.bin/tsc --noEmit -p tsconfig.json  # the only real typecheck
bazel test //ui:test                                        # vitest
bazel build //ui:ui                                         # the real release bundle
```

**`bazel build //ui:ui` does not typecheck.** Vite builds with esbuild, which
strips types without checking them, so a genuine type error produces a green
build. `tsc --noEmit` is the only gate that catches it, and it is not yet a Bazel
target — it has to be run by hand.

`//ui:test` runs vitest under `environment: "node"` with no jsdom. Component
behavior is tested by rendering with `renderToStaticMarkup` and asserting on
markup; anything needing real layout, focus, or event dispatch can only be
verified in a browser (see Browser verification hook above).

## Directory Structure

```
proto/
  grpcview/v1/      Wire API: service.proto (WorkspaceService + ServerService
                    Connect RPCs) + workspace.proto (messages)
  grpcview/store/v1/ On-disk persistence schema (storage.proto)
  echo/v1/          A trivial echo service used for testing invoke end-to-end
service/
  service.go        Serves and owns the server's lifecycle (bind, publish, drain);
                    idle.go and logging.go alongside
  cmd/              Production entry point (main.go embeds index.html)
  cmd/dev/          Dev backend entry point
  daemon/           Registration file, spawn lock, connect-or-spawn, browser launch
  wire/             The one Client interface + its local and remote bindings
  workspace/        WorkspaceService handler (reflection, invoke, CRUD)
  store/            Filesystem-backed collection (protojson tree); convert.go
                    bridges store↔wire schemas
  scripting/        QuickJS-WASM engine, esbuild bundler, capability layer
  echo/             The echo service implementation + its cmd/ server
ui/
  src/
    App.tsx, main.tsx   App root + view switch
    components/shell/    AppShell, TopBar, Rail, StatusBar
    components/ui/       Nocturne design-system primitives (Button, Dialog, …)
    features/workspace/  Request editor, tree, response pane, body/metadata
    features/sources/    Reflection-source configuration
    features/scripts/    Script authoring
    lib/                 client.ts (transport), ui-store.ts (zustand),
                         workspace-query.ts, format.ts
    theme/               Nocturne tokens, fonts, Monaco theme
third_party/quickjs/  Vendored QuickJS-WASM build inputs
tools/                Repo tooling
docs/design/          Design docs and plans (see below)
```

## Design docs

`docs/design/` is sorted by how much of each doc is real code — read
[`docs/design/README.md`](docs/design/README.md) for the index:

- `shipped/` — the arc is finished; kept for the decisions, never as a worklist.
- `active/` — **stopped mid-arc**: written-out work that is genuinely unbuilt.
- `planned/` — nothing built. `planned/roadmap.md` is the backlog of *wants* with no plan
  behind them yet; everything else there is a real plan with decisions and steps.
- `research/` — background research, closed.

A shipped plan does not stay `active/` because it ends in a wishlist — the leftovers go to
`planned/roadmap.md` and the doc goes to `shipped/`. Re-check leftovers against trunk
before carrying them; stale "remaining" bullets are the normal failure here.

Multi-phase tracks sort **per doc**, so one track can span folders: the VS Code track has
phase 1 in `shipped/vscode/`, its daemon in `planned/`, and phases 2–6 in `active/vscode/`.
The track README stays with the unbuilt phases and maps the rest.

`request-body-contract.md` and `known-bugs.md` stay at the top level: the first is
authoritative on what a request body may be across all four surfaces, the second tracks
defects deliberately left unfixed.

Every doc is written in the present tense about the code as it stood when it was written;
its `file.go:line` citations are the premise of a decision, not a description of trunk.
Move a doc when its status changes, and delete a `shipped/` one once nothing in it is
worth keeping — the code and this file are the source of truth, not a plan archive.

# Claude for Chrome

- Use `read_page` to get element refs from the accessibility tree
- Use `find` to locate elements by description
- Click/interact using `ref`, not coordinates
- NEVER take screenshots unless explicitly requested by the user
- Prepare and execute sequences of actions, evaluate the final result. Only go step by step, if it failed in an unobvious manner
