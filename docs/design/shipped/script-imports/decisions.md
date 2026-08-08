# script imports — decisions taken before implementation

This file resolves every "open question" and "one decision still open" in
[`README.md`](./README.md). Where the two disagree, **this file wins**. It is the
normative spec for the implementation phases in [`phases.md`](./phases.md).

Nothing here re-argues the design; read `README.md` for the reasoning.

---

## 1. Migration: hard break, no converter

There is no `grpcview migrate` verb and no read-both compatibility path. The backend
reads the new layout only. `calledNames` and `transitiveGenerators` are deleted in the
same change that lands the resolver — they do not survive a release.

The in-repo `example/` collection is migrated **by hand**, as part of the track. It is
the dogfooding surface, so its migration is the acceptance test for the whole design,
not a mechanical afterthought.

Consequence: a pre-existing collection with `scripts/<slug>/script.json` on disk is
simply not seen. Nothing errors; the scripts are absent. Reporting that clearly is
[`workspace-diagnostics.md`](../../planned/workspace-diagnostics.md)'s job, and that
doc is **not** part of this track.

## 2. Two sigils, both resolving to files

| specifier | resolves against | use |
|---|---|---|
| `@/…` | the **workspace root** | anything, including another collection's scripts |
| `~/…` | the **collection root** of the script being compiled | this collection's own files |

Both are handled by one esbuild plugin whose `OnResolve` filter is `^[@~]/`, installed
in the `extra` slot of `bundler.plugins(g, extra...)`. The slot matters: it places the
plugin after the capability plugin and **before** the registry plugin, whose `^[^./]`
filter would otherwise claim both forms.

Each resolves to a real file on disk, then passes the same containment guard the
registry plugin uses (`withinDir`). `@/x` outside the workspace root, or `~/x` outside
the collection root, is an error reading `resolves outside the workspace` /
`resolves outside the collection`.

`~/` is not a home directory here and never reaches the OS. `@/` can never be a real
npm package (`@scope/name` needs a scope segment), and `~/` cannot either.

**A collection root is per-compile, not per-engine.** The workspace root is fixed at
engine construction; the collection root rides each compile call. Both therefore enter
the bundle cache key (§6).

## 3. `console` and `fetch` stay global

`gv` is de-globalized in full — `invoke`, `assert`, `metadata.inherit`, `request.params`
all become `grpcview:` modules — and `globalThis.request` is deleted outright (middleware
already receives its ctx as the `handle(ctx)` argument).

`console` and `fetch` **remain ambient globals**. They carry no capability grant, they
are what every JS author expects, and the standing rule is to copy the familiar
environment where an equivalent exists. The longer-term goal they serve is emulating
enough of the Node/browser environment to run third-party libraries unmodified; a
library that calls `console.log` must not need a grpcview-specific import.

So the claim to write into AGENTS.md is precisely: **no grpcview-specific identifier is
global.** Not "nothing is global" — `console`, `fetch`, and the internal `__grpcview_*`
host bridges all are.

### The four `grpcview:` modules

```ts
import { invoke } from "grpcview:invoke";     // (path, params?) => Promise<InvokeResult>
import { assert } from "grpcview:assert";     // (description, condition) => void | Promise<void>
import { inherit } from "grpcview:metadata";  // () => { [k: string]: string[] }
import { params } from "grpcview:request";    // Readonly<Record<string, any>>
```

Each is a static shim resolved by the capability plugin's mechanism (a namespace plus a
synthetic `OnLoad`), so the module **text** is constant and stays cacheable. Per-run data
— the inherited metadata map, the params object — is passed the way it already is, in the
prelude, under internal names the shims read (`__grpcview_inherited`, `__grpcview_params`).
Data in the prelude, code in the module.

`grpcview:invoke` is granted unconditionally today (as `gv.invoke` was). Making the grant
real is [`README.md`](./README.md)'s "capability grants over an import graph" question and
is out of scope here.

## 4. Expression-position bodies use `require`

Unchanged from the doc, restated because it is the ergonomic every reviewer will ask about:

```ts
// request body / metadata, expression form — what the Monaco hidden wrapper produces
{
  ...require("grpcview:metadata").inherit(),
  "x-request-id": [require("~/scripts/ids").requestId()],
}
```

An `import` **statement** in expression position is a hard parse error
(`Expected "(" but found …`). Detect that case and replace the message with:
*a request body in expression form cannot use an `import` statement; use `require("…")`,
or write the body as a module with `export default`.*

Computed specifiers — `require(p)` / `import(p)` where `p` is not a string literal — are
**rejected before the build**, because esbuild reports neither an error nor a warning for
them and emits code that fails at runtime (or, for `import(p)`, a live dynamic import into
QuickJS). A conservative regex is correct here: we are forbidding, not resolving, so a
false positive says "use a string literal", which is the rule anyway.

## 5. Script identity is its path

- `ScriptKind` is deleted from both protos, from `CreateScript`, from `RunScript`, from
  the MCP tools and from the CLI's `--kind`.
- `script.json` is deleted. A script is a `.ts` file.
- `Collection.scripts` (the ordered slug list) is deleted. The filesystem is the listing.
  `writeScriptOrder`, `scriptSlugSet`, and the script-side `uniqueSlug` / `reconcileOrder`
  go with it.
- **Layout is flat files, not a directory per script**: `scripts/uuid.ts`, not
  `scripts/uuid/script.ts`. Subdirectories under `scripts/` are allowed and are walked
  recursively; they are just path segments.
- **`grpcviewv1.Script` becomes `{ string path = 1; string source = 2; }`**, where `path`
  is **collection-relative and includes the extension** — `scripts/uuid.ts`. It addresses
  the script in every RPC (`UpdateScript`, `DeleteScript`, `RunScript.script`), and
  renaming is a path change, i.e. a file move.
- The Scripts view lists `<collection>/scripts/**/*.ts`. Resolution does not care about
  `scripts/` at all: any `.ts` under the workspace root is importable. `scripts/` is
  convention — what the view lists and what "unused script" diagnostics would scan.

`RunScript` loses its kind, so it has one rule: a source with `export default` is compiled
as an entry and its default export is **called**; anything else is evaluated as a
scratchpad and its last expression is the value. That covers what GENERATOR and SCENARIO
separately covered. Middleware is not run through `RunScript`; it runs on the invoke path.

## 6. `Request.middleware` holds specifiers, in either grammar

`Request.middleware` stops being display names and becomes a list of **specifiers**, each
either `@/…` or `~/…`, resolved by the same rules as an import — so a request can attach a
middleware from its own collection (`~/scripts/trace-headers.ts`) or from anywhere in the
workspace (`@/lib/mw/auth.ts`). Whichever the author wrote is what is stored; there is no
canonicalization pass.

Order still lives on the request: middleware is a chain applied *around* the call, not
something the body imports.

The middleware source is read from **disk**, at the resolved path, with the same
containment guard. `loadMiddlewareSources` and the kind filter are deleted.

**`GvMiddleware` is a shipped type**, so an author can write
`export default handler satisfies GvMiddleware` and get a red squiggle for the wrong
shape. Without the annotation the shape is checked at runtime only, on attach and on run.

## 7. The bundle cache is keyed over every input

`cacheKey` becomes a hash of `(cacheSalt, variant, grant, collectionRoot, source, and every
resolved input's path + content digest)`. The inputs come from the metafile —
`Metafile: true, Write: false` — so the graph the key is built from is the graph the bundle
used, and they cannot drift.

`cacheSalt` gains the workspace root, joining the per-engine configuration already there.
The composed-path cache bypass is deleted along with the composed path.

## 8. Frontend resolution mirrors the plugin

A new RPC lists the workspace's TypeScript sources as `{path, content}[]` — contents, not
declarations, because these are TS files whose inferred types are the point.

- Registered at `file:///grpcview/ws/<workspace-relative path>`.
- `compilerOptions.paths`: `"@/*"` → `["file:///grpcview/ws/*"]`, and `"~/*"` →
  `["file:///grpcview/ws/<active collection id>/*"]`, rewritten when the active collection
  changes.
- **App-level registration, not per-editor** — same reasoning that moved `gv-requests.d.ts`
  into `useGvInvokeTypes`: imports are legal from every script surface, including the
  Scripts view, which mounts no body editor.
- `typescriptDefaults` is global and a same-path re-add throws, so dispose before re-adding.
- **Auto-import must work** — it is what replaces the bare-name ergonomic that
  `registerGeneratorLibs` provided. Verify in the browser rather than assuming.

Verifying it in the browser is what showed Monaco cannot do it: its bundled worker exposes
`getCompletionsAtPosition(fileName, position)` and `getCompletionEntryDetails(fileName,
position, entry)` and hardcodes every options/preferences argument to `undefined`, so
`includeCompletionsForModuleExports` is unreachable and the built-in adapter also drops the
`codeActions` that would insert the import. Reaching it would mean shipping a custom TS
worker, which this app's single-file build does not make cheap.

So the import suggestions are ours: `module-auto-import.ts` registers one completion provider
that offers each workspace module's exported names — parsed from the source in
`auto-import.ts`, over the same literal-masking the module sniff uses — with an
`additionalTextEdits` that writes the `import` line. It offers only names not already in
scope, which is also what keeps it from duplicating the built-in provider's suggestions. The
specifier is computed rather than chosen by TypeScript, so it is always the sigil form (`~/`
inside the active collection, `@/` outside it) and can never come out relative.

`registerGeneratorLibs`, `isEmittableName` and its reserved-word list are deleted.

## 9. Out of scope for this track

- `workspace-diagnostics.md` — this design deliberately lets a move break a reference, and
  that doc is what makes the break visible. It is the right next track, not part of this one.
- `cross-collection-invoke.md` — shares the diagnosis, addresses requests rather than files.
- VS Code phase 2 (bodies as `.ts` files). The convergence argument in `README.md` stands;
  bodies keep living in `request.json` and keep being fed through `Stdin` for now, which
  works with the resolver plugin unchanged.
- npm resolution from inside an imported file (roadmap S5), and capability grants computed
  over the import graph (S4).
