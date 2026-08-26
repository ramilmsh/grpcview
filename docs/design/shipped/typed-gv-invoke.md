# Exploration — strongly typed `gv.invoke`

**Status:** BUILT (2026-08-05), editor-side only — no RPC, proto or runtime change.
Shipped as sketched below with D1 open (`string & {}` fallback), D2 resolved by
*skipping* streaming targets rather than typing them `never`, and D3 left at tier 1
(`params` stays `Record<string, unknown>`). Behavior is documented in AGENTS.md
§"Typed `gv.invoke` paths"; this doc is kept for the decisions still open — D3
(`export type Params`), D4 (moving the producer into the phase-3 sink seam), and D6
(display names containing `/`, still only worked around).

## The idea

`gv.invoke(path)` can only reach a saved request in the *current collection*
(`scriptInvoker` closes over the collection id, `gvinvoke.go:55`). Every saved
request names a concrete `service` + `method` (`workspace.proto:128`), and the
collection's merged `FileDescriptorSet` gives that method's output message. So the
editor already knows, statically, both the set of legal paths and the response
shape behind each one. Today all of that is thrown away: `invoke(path: string)`
returns `InvokeResult` with `body: any`.

Target state:

```ts
const c = (await gv.invoke("Collections/ListCollections")).body.collections[0].id;
//                          ^ completes from the real tree      ^ typed string
```

with a typo in the path being a compile error rather than a runtime rejection.

## What already exists (so this is smaller than it looks)

- **Every method's Json type is already in the body editor's type space.**
  `Editor.tsx:128-132` registers the generated `_pb.ts` for the *whole* merged
  workspace descriptor set, not just the open request's closure — the response type
  of every other request in the collection is already resolvable. What is missing is
  only a map from path → symbol.
- **The per-method alias is the pattern to copy.** `requestMessageAlias`
  (`proto-types.ts:57-75`) emits a tiny ambient `.d.ts` that imports from
  `./gen/<file>_pb` and declares one global. It resolves nested/short names through
  `resolveLocalSymbol`. A path map is the same trick, N entries wide.
- **The runtime body is exactly `<Msg>Json`.** `gv.invoke`'s `body` is the raw
  `dm.MarshalJSON()` output of the invoked call (`gvinvoke.go:108` ← `invoke.go:138`)
  — the same bytes the response pane renders, and the same protojson dialect the
  request side is already typed against.
- **`strict: false`** (`monaco-scripts.ts:23`). No `strictNullChecks`, so typing
  `body` as `T` while it is `null` on failure produces no "possibly null" noise, and
  no discriminated union on `ok` is needed to keep `.body.x` ergonomic.
- **Sibling display names are unique** — create/rename/move all return
  `ErrAlreadyExists` (`fs.go:222,272,329,521`), so `parent/child` path keys do not
  collide.

## Sketch

One generated artifact, per collection, registered next to the existing
`request-message.d.ts` so the same relative `./gen/...` import style works:

```ts
// file:///grpcview/request/gv-requests.d.ts   (regenerated, never hand-edited)
import type { ListCollectionsResponseJson, DescribeMethodResponseJson }
  from "./gen/grpcview/v1/service_pb";

declare global {
  interface GvRequestMap {
    "Collections/ListCollections": { response: ListCollectionsResponseJson; streaming: false };
    "Collections/DescribeMethod":  { response: DescribeMethodResponseJson;  streaming: false };
  }
}
export {};
```

and a static change in `gv.d.ts` (`monaco-scripts.ts`):

```ts
// Declared empty here so the generic still compiles before/without the generated
// map; interface merging fills it in. Empty map ⇒ keyof = never ⇒ today's behavior.
interface GvRequestMap {}

type GvPath = keyof GvRequestMap;
type GvBody<P> = P extends GvPath
  ? GvRequestMap[P]["streaming"] extends true ? never : GvRequestMap[P]["response"]
  : any;

declare const gv: {
  invoke<P extends GvPath | (string & {})>(
    path: P,
    params?: Record<string, unknown>
  ): Promise<InvokeResult<GvBody<P>>>;
};

type InvokeResult<T = any> = {
  ok: boolean;
  status: { code: number; message: string };
  /** Decoded response JSON; `null` at runtime when `ok` is false. */
  body: T;
  metadata: Record<string, string[]>;
  requestMetadata: Record<string, string[]>;
  latencyMs: number;
};
```

`P extends GvPath | (string & {})` is the load-bearing trick: TS still offers the
literal members as completions inside the quotes, but a computed path
(`` `Users/${name}` ``) keeps compiling and degrades to `body: any`.

## Decisions this needs

**D1 — closed union or open string.** Closed (`P extends GvPath`) turns every
dynamic path into an error; open keeps them working and silently untyped.
*Lean open*, with the completion behavior preserved as above. A "strict paths"
toggle can come later if anyone wants it.

**D2 — streaming targets.** `gv.invoke` rejects them at runtime. Either omit them
from the map (they fall through to `any` — silent) or include them with
`streaming: true` so `GvBody` resolves `never` and the call errors in the editor.
*Lean include*, since catching it at author time is the whole point.

**D3 — typing `params`.** Three tiers:
1. leave `Record<string, unknown>` (status quo, zero work);
2. convention: a target body may `export type Params = {…}`, and the producer slices
   that declaration out of the target's `draft_body` text the way `sliceTypeBlock`
   already slices a type block (`proto-types.ts:99`), emitting it into the map entry.
   The same declaration can alias `gv.request.params` *inside* the target — symmetric
   with `RequestMessage`;
3. infer from `gv.request.params.X` uses — needs a real TS AST, and the `typescript`
   package is deliberately stubbed out of the bundle (`vite.config.ts:20-25`). Only
   the Monaco worker has services. Not worth it.

*Lean 1 now, 2 as an opt-in later.*

**D4 — where the producer lives.** This is a third artifact of the producer/sink
seam already planned in [vscode/phase-3-type-sinks.md](../active/vscode/phase-3-type-sinks.md):
a pure `(descriptorSet, requests) → dts` function with no `monaco-editor` import, so
the VS Code disk sink gets typed invoke for free instead of reimplementing it.
*Lean: land phase 3 first, or at minimum write the function pure inside
`proto-types.ts` so the later lift is mechanical.*

**D5 — invalidation.** The map depends on the tree (names, nesting) and each
request's service/method, not just the descriptor set. Rename, move, create, delete
and method-switch must regenerate it. `Editor.tsx` today only receives
`input{Package,Name,File}`; it would need the request list threaded in. Regeneration
is cheap (N lines of text), so "rebuild on any Collection change" is fine.

**D6 — path keys are display names.** They can contain quotes and backslashes (escape
on emit) and, more awkwardly, `/` — `splitInvokePath` (`gvinvoke.go:98`) splits on it
unconditionally, so a request literally named `a/b` is *already* an ambiguous runtime
path. The map would inherit that ambiguity. Options: skip such names from the map, or
reject `/` in names at the store layer. Open question, and arguably a pre-existing bug
worth fixing independently.

**D7 — protojson dialect fidelity.** jhump's `dynamic.MarshalJSON` vs. protoc-gen-es
`json_types=true`: int64-as-string, enum-as-name, bytes-as-base64, absent-when-default.
These are expected to agree (both are jsonpb), but a divergence would make the typed
body subtly wrong. Note the request side already carries this exact risk through
`RequestMessage`, so it is not a new class of bug — worth one spot check, not a
redesign.

## What it buys

- Path completion from the actual tree, inside the string literal.
- A renamed or deleted target becomes a type error in every caller that names it —
  today that is a runtime rejection discovered on invoke.
- `.body.` completion and correct types through the response message, including
  assigning straight into a typed request field.
- Same generated snapshot serves VS Code once the sink seam exists.

## What it costs

- One more generated artifact plus its invalidation wiring (D5), which is the only
  genuinely new plumbing.
- A stale or unreachable source yields an empty descriptor set → an empty map →
  `body: any` and no path completions. Degradation is silent but not *wrong*, which
  is the right failure mode.

## Smallest thing that proves it

1. Add the empty `interface GvRequestMap {}` + generic `InvokeResult<T>` to the static
   `gv.d.ts`. Behavior identical to today; nothing else changes.
2. Hand-write `gv-requests.d.ts` for the `example/` collection, register it in
   `Editor.tsx` next to the `RequestMessage` alias, and check in the browser that
   `gv.invoke("` completes both paths and `.body.collections[0].id` types as `string`.
3. Only then replace the hand-written file with the generator, and wire D5.

Step 2 is the decision point: if literal-union completion inside the quotes does not
actually fire in this Monaco/TS build, the headline benefit is gone and the rest is
not worth much.
