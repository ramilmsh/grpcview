# grpcview — script imports

**Status:** Planning only (this doc). **Not started.** Supersedes an earlier draft of this
file called `script-libraries.md`, whose whole framing — declared "library roots", `#alias`
specifiers, a second manifest key — was rejected in review. Nothing of it survives except
the problem statement.

**The problem, in the user's words:** scripts are not shareable between collections. A
generator written in `services/payments/requests` cannot be reached from
`services/identity/requests`; the only remedy is copy-paste, and the copies drift.

**The answer:** a script is an ordinary `.ts` file, addressed by its path from the
workspace root, reached by `import` or `require`. Nothing declares it. There is no library
concept, no alias map, no `ScriptKind`, and no `script.json`.

```ts
// any script position
import { pageAll } from "@/lib/pagination";
export default async () => ({ users: await pageAll() });
```

```ts
// a request body in expression position — the Monaco hidden-wrapper form
{ userId: require("@/services/identity/requests/scripts/uuid").default() }
```

**The sibling tracks.** [`cross-collection-invoke.md`](../../planned/cross-collection-invoke.md) shares
the diagnosis (everything resolves inside one collection) and nothing else — it addresses
*requests*, this addresses *files*. [`workspace-diagnostics.md`](../../planned/workspace-diagnostics.md)
is the load-bearing dependency: this design deliberately lets a move break references, and
that doc is what makes the break visible.

---

## The premise, stated because it overrules the obvious objection

**Portability of a single collection is not a design goal.** Copying a collection out of
its workspace and leaving its imports behind is the same class of event as deleting a
script by hand — user action with a visible consequence, not a shape the tool must
prevent. A workspace is a static unit; moving things inside it is refactoring, and
refactoring breaks references in every language with a module system.

So the rule is: **references may break, and the tool must say so loudly.** That is the
whole reason [`workspace-diagnostics.md`](../../planned/workspace-diagnostics.md) exists, and it is
why the two docs should be built together.

This retires the earlier draft's central argument — that hoisting shared code above a
collection would break the "a collection is 100% committed, portable content" invariant.
The invariant is real for *what a collection contains*; it was never a promise that a
collection resolves standalone.

### What actually is rejected

Not workspace-scoped sharing — this design *is* workspace-scoped sharing. What is rejected
is **implicit resolution**: a flat pool of scripts, hoisted to the workspace root, resolved
by bare name with no import. That is what the codebase does today inside one collection,
and scaling it up is what fails:

- A flat name namespace across a whole workspace collides by construction.
- Bare-name resolution has to be discovered by a **textual scan** — that is exactly what
  `transitiveGenerators` + `calledNames` (`invoke.go:399`) is. A scan cannot see through an
  import, cannot see a re-export, and cannot distinguish a call from a coincidence.
- It is silently lossy. A generator whose name is not a simple identifier is dropped by
  `isEmittableName` (`generator-libs.ts`) and `composeGeneratorPrelude`'s `simpleIdentRe`
  (`compose.go:10`) — so `my-gen` is creatable today and simply never callable, with no
  error anywhere.

An import graph fixes all three, and esbuild already computes it.

---

## `script.json` goes away

It holds exactly three fields (`storage.proto:58`) — and the ordering that looks like it
belongs there actually lives in `grpcview.json` as `Collection.scripts` (`storage.proto:47`).

| field | disposition |
|---|---|
| `source` | becomes the `.ts` file. The point. |
| `meta.name` | becomes the **filename**. See below. |
| `kind` | **deleted**. See below. |

`Collection.scripts` (ordered slugs) is deleted with it: the filesystem is the listing.
That takes `writeScriptOrder`, `scriptSlugSet`, and script-side `uniqueSlug` /
`reconcileOrder` with it, and reduces `store/scripts.go` (210 lines) to a directory walk.

### `name` → the filename, which is a fix

Today `name` is the **reference key** and the directory slug is the **location**, and they
drift by design:

- `loadGenerators` keys `gens[s.GetName()]` (`invoke.go:437`) — bare `uuid()` resolves by
  name.
- `Request.middleware` is *"Ordered display names of the MIDDLEWARE scripts run before the
  call"* (`storage.proto:80`).
- AGENTS.md documents the drift as intended: renaming `test-goals` to `smoke` leaves
  `scripts/test-goals/script.json` holding `"name": "smoke"`.

Collapsing them into one key — the filename — is strictly better here:

- The silent-drop bug above becomes unexpressible: a filename either is a usable
  identifier or the import names it explicitly.
- `import "@/…/pagination"` addressing by location while `pagination()` addresses by name
  is an incoherence this design would otherwise ship.
- Renaming becomes a file move. Git records a rename; importers must move with it. That is
  a refactor, caught by diagnostics — consistent with the premise above.

**This is a narrowing of the AGENTS.md rule, not a repeal.** Requests and folders keep the
slug-is-identity / name-is-data split, because a request's display name is prose that
appears in a tree and in an invoke path. A script's name is an identifier. Different
things.

### `kind` → deleted

`ScriptKind` (GENERATOR / MIDDLEWARE / SCENARIO) exists in both the wire and store protos
and is read in exactly two places: `loadGenerators` filters for GENERATOR,
`loadMiddlewareSources` filters for MIDDLEWARE.

Three homes were considered for it — a filename suffix (`uuid.gen.ts`), a directory per
kind (`scripts/generators/`), inference from exports — and all three were rejected in
favour of **not having kinds**. Whether a file works as middleware is decided by whether
its shape fits when used as middleware: it fails at runtime, and it is red in Monaco.

Two consequences to build, not assume:

- **`Request.middleware` becomes a list of paths**, not display names. Order still lives on
  the request — middleware is a chain applied *around* the call, not something the body
  imports, so it does not become an import.
- **"Red in Monaco" is not automatic.** Nothing makes TypeScript compare
  `export default (ctx) => …` against the middleware signature. Ship `GvMiddleware` /
  `GvScenario` types and have the author write `satisfies GvMiddleware`; without the
  annotation it is runtime-only. The middleware picker should also check the shape at
  attach time. Both, probably.

Kind-less is what forces import-only, and that is the next section — you cannot keep
ambient injection without knowing which files are generators, and "inject every script
name" is not an option.

---

## What deletes

This is the largest deletion in the design, and it is the point rather than a side effect.
**The prelude was the workaround for not having imports.**

| deleted | why it existed |
|---|---|
| `composeGeneratorPrelude`, `generatorResolverPlugin`, the `grpcview:gen/` namespace | faking `import` for generators |
| `transitiveGenerators`, `calledNames` | discovering what to fake |
| `buildBundleComposed` / `buildEntryBundleComposed` and the cached-vs-composed compile fork | composed bundles fold gens in, so `(source, grant)` was an unsound key |
| `loadGenerators`, `loadMiddlewareSources` | kind filters |
| `registerGeneratorLibs`, `isEmittableName`, its reserved-word list | the editor half of the fake |
| `buildGvPrelude`, `writeGlobal` | assembling `gv` as a global |
| `ScriptKind` (both protos), `wireToDiskScriptKind`, `CreateScript`'s kind arg, MCP `create_script`'s kind param | kinds |
| `script.json`, `Collection.scripts`, `writeScriptOrder` | see above |

The ergonomic AGENTS.md defends — `{"userId": uuid()}` with **no ceremony**, the reason the
whole TS-body design exists — survives, delivered by **Monaco auto-import** instead of a
backend prelude. Type `uuid`, take the completion, the import line writes itself. Which is
the standing rule anyway: copy VS Code where an equivalent exists.

---

## `gv` stops being global

`import { invoke } from "grpcview:invoke"`, `{ assert } from "grpcview:assert"`, and so on
— **split per concern, not one `grpcview:gv` barrel.**

The reason is consistency with a rule the codebase already chose. `capabilityPlugin`'s
comment (`bundler.go:319`) states it: *"a capability module resolves only if the grant
permits it, so an ungranted import leaves no call site at all."* Today `fs` obeys that and
`gv.invoke` does not — it is always present, always wired, whether or not the script uses
it. Splitting `gv` into modules makes **the module graph the capability graph**, uniformly.

`grpcview:` is the right prefix: it matches the existing `grpcview:gen/` scheme, and it can
never be a file path or an npm package name.

What that buys:

- The freeze choreography deletes. AGENTS.md currently has to explain that `gv` is *"frozen
  exactly once"*, that the two callables are *"hung off the containers before the single
  `__ff` freeze pass, which recurses only on `typeof === 'object'` and so leaves them
  callable"*, and that a second `globalThis.gv =` would clobber the first. Modules are not
  clobberable; there is nothing left to defend.
- The `inherit(` **text gate** in `foldAncestorMetadata` becomes "does the graph import
  `inherit`" — precise instead of textual. Keep the separate empty-script case, which
  inherits unconditionally: that is a default, not a gate.
- Tree-shaking means a body that never invokes does not carry the invoke host call.

Three more currently-user-visible globals join the same conversion: `globalThis.request`
(read by middleware at `entry.go:35` as `request.body` / `.metadata` / `.target`),
`console` (`marshal.go:62`), and `fetch` (`net.go:36`).

### The one thing to test rather than assume

`gv.assert` is pure prelude JS (`gvAssertShim`), and its frames are **deliberately named**
(`gvAssert`, `gvAssertFail`) so they can be filtered out before the throw — because
`remapJSError` reads the **first** frame's line, and an unfiltered stack blames a prelude
line instead of the failing assertion's. As a bundled module the names survive (esbuild
preserves them), but the positions move. Write the test that asserts a failed assertion
still reports the author's line.

### "Nothing is global" — precisely

The prelude does not vanish, it becomes **entirely internal**: `__ff`,
`__grpcview_invoke`, `__grpcview_fs_read`, `__grpcview_net_fetch`, `__grpcview_console`,
`__grpcview_entry`. Those are host bridges the shims call. The exact claim is that **no
user-visible identifier is global** — worth writing into AGENTS.md in those words, because
"nothing is global" read literally is false and the next reader will find `__ff` and file
a bug.

---

## Resolution

`@/` = the workspace root. One esbuild plugin, in the `extra` slot of
`bundler.plugins(g, extra...)` — which places it after the capability plugin and **before**
the registry plugin, and that ordering is load-bearing: `registryResolverPlugin`'s filter
is `^[^./]`, which matches `@/lib/x`, and it would otherwise claim the path, stat
`registryDir/@/lib/package.json`, miss, return an empty result, and fall through to
esbuild's default resolution with a confusing error.

```go
func atRootResolverPlugin(wsRoot string) api.Plugin  // OnResolve filter ^@/
```

Resolve against `wsRoot`, then apply the **same containment guard the registry plugin
already applies** — `withinDir(wsRoot, r.Path)` (`bundler.go:284`), error text in the shape
of *"resolves outside the workspace"*. That guard is what keeps the trust boundary intact.

**Unlike the generator and capability plugins, this resolves to real files on disk** rather
than to a namespace with a synthetic `OnLoad`. So there is no store dependency, no module
map threaded alongside `gens`, and no layering inversion — `service/scripting` never learns
about `service/store`.

**Imports resolve from disk, so unsaved editor edits do not take effect.** That is not a new
concept to explain: AGENTS.md already states it for the adjacent case — *"Ancestor scripts
are read from the store, so folder edits only take effect after saving."* Same rule, one
sentence.

### Why `@/` and not `#alias` or `//`

`@/` is the Vite / `tsconfig` `paths` convention for "project root", so a TypeScript author
reads it without being taught. `@/foo` can never be a real npm package (`@scope/name`
requires a scope segment), so there is no true collision with the registry namespace —
though plugin order still has to put ours first, per above.

**Two sigils, deliberately.** `@/` addresses **files** (TypeScript convention); `//`
addresses **logical items** in the `gv.invoke` label
([`cross-collection-invoke.md`](../../planned/cross-collection-invoke.md)). They are different kinds of
thing and they get different grammars. Recorded here as a choice so it is not later
discovered as an inconsistency.

---

## `require` — measured, not assumed

Probed against the vendored esbuild (**v0.28.1**, `go.mod:42`), bundling with the real
options from `esbuildBundle` and a `^@/` resolver plugin:

| source | errors | warnings | emitted |
|---|---|---|---|
| `require("@/x")`, ESM out | 0 | 0 | resolved, inlined via `__esm` / `__toCommonJS` |
| `require("@/x")`, IIFE out | 0 | 0 | same, works |
| `require("@/x")` inside the hidden `=> ( … )` wrapper | 0 | 0 | works |
| `import("@/x")` — literal | 0 | 0 | resolved, `kind: "dynamic-import"` |
| `require(p)` — computed | **0** | **0** | a `__require` shim that throws `Dynamic require of "…" is not supported` |
| `import(p)` — computed | **0** | **0** | **a live `import(p)`, passed through verbatim** |
| `import` statement in expression position | 1 | 0 | `Expected "(" but found "uuid"` |

`Platform` (browser / neutral / node) changes none of it.

Two conclusions:

1. **The rule is literal-vs-computed, not require-vs-import.** A literal specifier resolves
   and bundles in every form; a computed one never does.
2. **esbuild reports nothing for the computed forms.** No error, no warning — it emits code
   that fails at runtime, and for `import(p)` it emits a live dynamic import into QuickJS.
   `esbuildBundle` currently reads only `result.Errors`, so both would sail through today.

### `require` is the tool for expression position

An `import` statement cannot sit in expression position — measured above as a hard parse
error. `require` is a call expression and can. So the two body forms
`request-body-contract.md` already documents each get their own tool:

- **module form** (`has export default`) — `import`
- **expression form** (wrapped by `wrapExpressionScript` into `export default async () => ( … )`) — `require`

Which retires the hoisting scheme an earlier draft of this doc proposed — splitting leading
`import` statements out of the body and lifting them above the wrapper, with line
accounting preserved so a bundler error still names the author's line. None of it is
needed. **The wrap continues to open no new line.**

One follow-through: the parse error a user gets for putting `import` in a body is
`Expected "(" but found "uuid"`, which explains nothing. Detect the case and say *"a
request body in expression form cannot use an `import` statement; use `require("…")`, or
write the body as a module with `export default`."*

### The `.default` wart

`require` of an ES module returns the **namespace**, so a default export needs
`.default` — visible in the measured output:

```js
var script_default = async () => ({ id: (init_uuid(), __toCommonJS(uuid_exports)).default() });
```

Declare `require` in the ambient `.d.ts` typed as the module namespace. Then omitting
`.default` is red in the editor with the fix in the completion, and named exports (which do
not have the wart) are the natural style for shared scripts.

### Rejecting the computed forms

esbuild will not, so we must, and it needs no parser — **because we are forbidding, not
resolving.** `bundler.go:201` already carries `dynamicImportRe = \b(import|require)\s*\(`.
Add a literal-form pattern and error whenever `dynamicImportRe` matches something the
literal form does not cover. A conservative regex is safe here: every false positive says
"use a string literal", which is the rule regardless.

Two weaker signals were checked and are **not** sufficient on their own: the metafile
records `imports: [{"path": "<runtime>"}]` for a computed `require` (and only for that
case), but a computed `import()` leaves `imports: []` — no signal at all.
`Supported: {"dynamic-import": false}` removes the live `import()` from the output without
raising an error; what it becomes was not verified, so it is not a recommendation.

---

## The dependency graph is esbuild's, and there is no second parser

**tree-sitter was considered and rejected.** A host-side parse that decides what to inject
into a prelude *is* `transitiveGenerators`; replacing a regex with a grammar makes it more
accurate and keeps the architecture we are deleting. Two parsers of one language will
disagree, and the disagreement surfaces as "analysis says X, the bundle does Y". esbuild is
already vendored, already parses TypeScript, and already handles cycles, re-exports,
tree-shaking and sourcemaps. And there is no prelude left to load anything into.

Build with `Metafile: true, Write: false` and read the graph. Measured, on real files:

```json
"body.ts": { "imports": [
  { "path": "services/identity/requests/scripts/auth.ts",
    "kind": "require-call", "original": "@/services/identity/requests/scripts/auth" },
  { "path": "lib/pagination.ts", "kind": "dynamic-import", "original": "@/lib/pagination" } ] },
"services/identity/requests/scripts/auth.ts": { "imports": [
  { "path": "lib/pagination.ts", "kind": "import-statement", "original": "@/lib/pagination" } ] },
"lib/pagination.ts": { "imports": [] }
```

- **`original` carries the author's specifier verbatim** — the string a refactor has to find
  and rewrite, without re-deriving it from the resolved path.
- **`path` is the resolved real file**, normalizable to workspace-relative.
- **`kind`** separates `import-statement` / `require-call` / `dynamic-import`.
- Transitive edges are all present.

Three limits, all real:

1. **No line/column.** You get "A imports B via this specifier", not where in A. For a
   rewrite, either string-match `original` (safe — it is distinctive and exact) or take
   positions from the TypeScript language service.
2. **Forward and reachable-only.** A script nothing imports **does not appear in the
   metafile at all** — verified. So "who imports X" means building every entry and
   unioning; "is X dead" means enumerating the filesystem and diffing against the graph.
3. **Computed specifiers are absent** — which is why they must be rejected outright rather
   than allowed to pass unseen.

### Division of labour

**esbuild metafile is the truth.** Backend, no editor needed: it is what `grpcview check`
runs in CI, what feeds "unused script", and what makes the bundle cache key sound. The
graph the analysis reads is the graph the bundle used, so they cannot drift.

**The TypeScript language service performs the edit.** Rename-with-references, positions,
multi-file apply — Monaco has it, VS Code has it, and reimplementing it is the thing the
standing preference forbids. esbuild says the edge exists; the language service changes it.

### The bundle cache becomes sound again

`cacheKey` hashes `(cacheSalt, variant, grant, source)` (`bundler.go:71`) and the composed
path bypasses the cache entirely, with the comment *"gens folds into the blob, so a key over
(source, grant) alone would be unsound."* With imports, the imported files are the same
class of hidden input — but now the metafile names them exactly, so the key can include
every input's path and content digest and the cache stays correct. The composed-path bypass
deletes along with the composed path.

Add `wsRoot` to `cacheSalt`, which is already where per-engine configuration lives
(`newBundler`, `bundler.go:37`).

---

## Frontend

Monaco type-checks in the browser, so every importable `.ts` in the workspace must reach it.

- **A new RPC** listing workspace TypeScript sources as `{path, content}[]`. Contents, not
  declarations — these are TypeScript files and their inferred types are the point.
- **`compilerOptions.paths`**: `"@/*"` → `"file:///grpcview/ws/*"`, with each source
  registered at that prefix, so the editor's resolution matches the plugin's.
- **Registration is app-level, not per-editor.** Same reasoning that moved
  `gv-requests.d.ts` and the generated `gen/**` modules out of `Editor.tsx` into
  `gv-types.ts`'s `useGvInvokeTypes` (called once from `App.tsx`'s `CurrentView`, above its
  early returns): imports are legal from every script surface, including the Scripts view,
  which mounts no body editor.
- `registerGeneratorLibs` is the model for the mechanics — including its disposal
  discipline: `typescriptDefaults` is global and a same-path re-add **throws**, so dispose
  before re-adding.
- **Auto-import must work**, because it is what replaces the bare-name ergonomic. Verify it
  in the browser rather than assuming; it depends on the extra libs being registered as
  modules at resolvable paths.

---

## `scripts/` stops being structural

If nothing declares a kind, nothing needs to declare a script either. **Any `.ts` file
under the workspace root is importable** — `<root>/lib/pagination.ts` works with no
collection involved at all. The "library" idea the earlier draft tried to build as a
feature arrives here as a consequence.

`scripts/` survives as **convention**: it is what the Scripts view lists, and what
"unused script" diagnostics scan. Resolution does not care.

Which raises a strategic consequence worth naming rather than discovering: once a script is
a plain file at a plain path, the web UI's Scripts view is a file browser with a Monaco
editor — i.e. a worse VS Code. That is not a blocker, but it means the real answer for
authoring lives in the VS Code track, and the web view should stop growing.

---

## Convergence with VS Code phase 2

[`phase-2-body-files.md`](../vscode/phase-2-body-files.md) moves `draft_body` and
`draft_metadata_script` out of `request.json` into sibling files. That is **this same move
applied to bodies**, and the two should be one track rather than two that meet awkwardly.

The concrete payoff: esbuild accepts multiple `EntryPoints` in a single build and the
metafile covers all of them. So **the whole workspace graph is one `Write: false` build** —
if every entry is a real file. While bodies live inside `request.json` they have to be fed
through `Stdin`, one build per body, and the refactor story special-cases two of the three
positions.

---

## Migration — the one decision still open

Every existing collection has `scripts/<slug>/script.json`; every existing body calls bare
generators; the in-repo `example` collection does both.

- **A one-shot converter** (`grpcview migrate`): write `script.ts`, delete `script.json`,
  drop `kind`, and rewrite bare generator calls into imports. The call rewrite is mechanical
  — `calledNames` already finds them, which is that function's last job before deletion.
  Recommended.
- **A hard break**: read the new layout only, and have `grpcview check` report old-layout
  collections with the fix. Cheapest to build and defensible pre-1.0, but it hands the
  body rewrite to the user.
- **Read-both**: rejected. Two resolution models is what this deletes.

Needs sign-off before implementation starts, because it decides whether `calledNames`
survives one release.

---

## Open questions

- **Does anything still need `scripts/` to be a directory per script?** With `source` in the
  file, `scripts/uuid.ts` is enough; `scripts/uuid/index.ts` buys sibling files (fixtures,
  helpers) at the cost of a level. `index.ts` resolves natively, `script.ts` would not.
- **npm inside an imported file.** A shared `.ts` importing `dayjs` resolves through the
  registry plugin, which is fine, but no `package.json` near it is consulted. Interacts with
  roadmap S5; defer.
- **Capability grants over an import graph.** A script's requested capabilities become the
  union over its transitive imports, and the S4 launch-consent digest has to cover every
  input the metafile names — not just the entry.
- **Does `Request.middleware` stay a list at all**, or become a single module exporting an
  ordered array? A list on the request keeps the UI's attach/reorder affordance; a module
  makes the chain reviewable in one diff.
