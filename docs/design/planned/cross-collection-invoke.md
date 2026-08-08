# grpcview — cross-collection `gv.invoke`

**Status:** Planning only (this doc). **Not started.**

**The problem.** `gv.invoke("Users/GetUser")` resolves inside the collection the calling
script belongs to, and there is no spelling that reaches another collection. In a
workspace where `services/identity/requests` owns the login call and every other
collection needs a token, that means duplicating the login request into each one.

**The sibling tracks.** [`script-imports/`](../shipped/script-imports) fixes the other half of
the same complaint (shared *code*), and [`workspace-diagnostics.md`](./workspace-diagnostics.md)
is where everything all three tracks can get wrong is reported. Separate docs on purpose:
imports rebuild the bundler's module graph and delete the whole generator prelude; this is a
path parser, a slug, and a type-generation decision. Neither blocks the other, and this one
is smaller — but do imports first, because a shared script that wraps `gv.invoke` is how
most of this gets used anyway.

Note that this doc addresses **logical items** (`//slug:Path/To/Request`) while imports
address **files** (`@/path/from/root`). Two sigils, two grammars, deliberately — the
reasoning is in `script-imports/` under "Why `@/` and not `#alias` or `//`".

**One prerequisite:** removing the workspace-root collection. The daemon is **not** one —
it shipped 2026-08-08 ([`shipped/daemon.md`](../shipped/daemon.md)) — but reading what it
did and did *not* deliver is load-bearing for the cost analysis here. It banked one of the
two payoffs this design wants (an in-process definitions memo that now survives between
invocations) and deliberately skipped the other (a listing held in memory rather than
rescanned per call, its deviation 4).

---

## What already exists

Most of the plumbing is there, which is why the backend half is small:

- `resolveSavedRun` (`invoke_saved.go`) already takes the collection id as a field
  (`savedInvoke.workspaceName` — a legacy name, it is a collection id) and opens it via
  `w.store.Open`. It is not hardcoded to anything.
- `store.Open` already cleans a wire-supplied id, rejects absolute paths and `..`
  escapes, and keys its handle map case-folded.
- The depth cap already rides the `context.Context` (`gvinvoke.go`, `maxInvokeDepth = 8`,
  no cycle set — self-recursive pagination is intended to work).

The single point of collection-binding is `scriptInvoker(collectionID)`, which closes
over one id and hands it to every `resolveSavedRun` call. That closure becomes the
**default** rather than the only option.

### The nesting semantics are already right

Worth stating because it looks like it needs work and does not.
`invoke.go:151` rebinds the invoker per spec —
`ctx = scripting.WithInvoker(ctx, w.scriptInvoker(spec.workspaceName))` — and
`resolveInvokeBody` / `resolveInvokeMetadata` / `loadGenerators` all key off
`spec.workspaceName`. So when a script in A invokes a request in B: B's body is evaluated
with **B's** generators, B's folder-metadata chain is B's own, and a *bare* path inside
one of B's scripts resolves inside B, not back in A. Nothing leaks across the boundary.
That needs a **test**, not a change.

---

## Addressing: labels over slugs

```ts
gv.invoke("//identity:Auth/Login")     // another collection, by slug
gv.invoke("Users/GetUser")             // this collection, unchanged
```

`//` opens a collection **slug**, `:` closes it and opens the request path. This is the
grammar the user reads all day — the repo is a Bazel workspace, `list_bazel_targets` is an
MCP tool, and bazel sources shell out to `bazel build`. `:` appears in neither a slug nor
a request display name, so the parse is unambiguous.

**A label never resolves by path.** This is a decision, not an oversight. Allowing either
spelling puts slugs and directory paths in one namespace, where a collection in a
directory named `payments` and a different collection whose slug is `payments` both answer
`//payments:X` — and `payments` is the obvious slug for exactly that collection. One
namespace, one kind of key.

Rejected alternatives:

- **An options object**, `gv.invoke({collection, path, params})`. Unambiguous and boring,
  but it forks the signature: `gv.invoke(path, params)` is the documented shape, the
  typed-path generic keys off a string literal, and an object form needs a second overload
  and a second generic. Keep it in reserve if the label grammar draws complaints.
- **A second argument**, `gv.invoke(path, params, collection)`. Puts the least-used
  argument last and past the one people actually pass; the type parameter still has to
  read two arguments to pick a body type.
- **A separator without `//`**, e.g. `identity::Auth/Login`. Works, but `//` is what makes
  it *read* as a label rather than a typo.

Ship `SplitInvokePath` (the exported wrapper already exists) as the single parser, so the
CLI's `--collection`-plus-path form and the label form cannot drift.

---

## Collection slugs

A slug is a **third** identifier, on top of a split AGENTS.md already documents as
deliberate: the directory slug is identity, the display name is data. Its justification is
not brevity — it is **move-stability**. Today a collection's id *is* its
workspace-relative path, so `Store.Rename` changes it, and every `//old/path:Req` written
in every script would silently break on a directory move. A slug in the manifest survives
the move. That is the whole reason it exists.

- **Lives in `grpcview.json`** (committed, travels with the collection), as
  `Workspace.slug` alongside `name`. Not in `grpcview.work.json` — see below.
- **No default.** A collection with no slug is fully usable and simply **not addressable
  as a cross-collection target**; its own scripts keep using bare paths. Defaulting to the
  directory base name would be actively bad here: in `services/*/requests` every
  collection's base name is `requests`, so the default would collide across the entire
  workspace on day one. No default also means zero migration and no forced churn.
- **Charset** `^[a-z0-9][a-z0-9-]*$`. Excludes `/` and `:` by construction, so a slug can
  never be mistaken for a path or swallow the label separator.
- **There is no root collection**, so the `//.:X` vs `//:X` question an earlier draft of
  this doc raised does not arise. See the next section.

### Duplicate slugs are accepted, warned, and never guessed at

**Duplicates cannot be prevented.** The slug is committed inside the collection, so
`cp -r services/payments/requests services/payments/requests-v2` produces two collections
with the same slug, and a merge of two branches that each added a collection does the same.
No write-time check can stop either.

So detect at read time and say so:

- **It is a warning, not an error.** Both colliding collections stay fully usable — they
  are only unaddressable *by that slug*. `store.List` already opens each collection and
  reads its manifest in `summarize`, so it is a check over data already in hand.
- **A label naming a duplicated slug is an error**, never a coin flip. The message names
  both candidate collection ids and the file each slug came from.
- **The reporting belongs to [`workspace-diagnostics.md`](./workspace-diagnostics.md)**, as
  the `duplicate-slug` and `unknown-slug` findings — a workspace has one report, not one per
  feature. That doc also carries the plumbing note this one used to: `CollectionSummary.error`
  is the wrong field, since a non-empty `error` today means "could not be summarized" and
  renders as a broken row. The decisions above stay here; the surface does not.

### Prerequisite: the root collection goes away

A collection at id `"."` — the workspace root itself — is **removed**, not slugged. This is
a workspace-model change with a blast radius wider than this track, listed here because it
is what makes the label grammar clean and because this doc is where the question surfaced.

The decisive argument is in `store.scan` (`discover.go`), which **prunes at a hit**: a
directory holding `grpcview.json` is a collection and a leaf. The workspace root is the
first directory the walk visits. So a `grpcview.json` at the root returns `fs.SkipDir` on
the root and the scan yields exactly `["."]` — **every other collection in the workspace
becomes invisible**. A root collection does not coexist with the scan; it silently
disables it. (A `grpcview.work.json` with a literal `collections` list bypasses the scan
and so papers over this, which makes it an inconsistency rather than a defence.)

The rest of the special-casing it already forces, all of which deletes with it:

- `Store.Rename` refuses `"."` on either side, because a collection at the root *is* the
  workspace and moving it would move the workspace (`store.go:120`).
- The per-collection state directory needs a carve-out, since
  `<stateRoot>/collections/./cache` cleans to a path that collides (`store.go:220`).
- `Collection.id` and `CollectionSummary.id` both carry `"." is the root` in their proto
  comments, and AGENTS.md documents `.` as a legal address.

**Migration:** a `grpcview.json` found at the workspace root becomes an error row in the
listing — "a collection cannot be the workspace root; move it into a subdirectory" — and
the scan continues past it rather than pruning. That turns today's silent
whole-workspace-blanking into a visible, actionable message.

### Why the slug is not in `grpcview.work.json`

A workspace-level `slug → collection path` map *would* make duplicates impossible: map
keys are unique, and two branches both adding one produce a git **merge conflict in a
single file**, which is precisely the mechanism git is good at surfacing. The precedent
exists in that message already — `Workspace.sources` is "shared by every collection"
(`storage.proto:19`).

It is rejected because **`collections` supports globs**. `"collections":
["services/*/requests"]`, and the scan-when-empty default, both produce collections that
no literal map entry names. Attaching slugs workspace-side would force literal
enumeration of every collection — which this repo's own example workspace does not do.
Paying for uniqueness with the loss of globbing is the worse trade.

---

## Where it lands

1. **`splitInvokePath` grows a slug return.** `(slug string, parent []string, name string,
   err error)`, with `slug == ""` meaning "the caller's collection". Reject a label with an
   empty slug, and reject a bare (unprefixed) path containing `:` so a mistyped label fails
   loudly instead of resolving to some request that happens to contain a colon.
2. **A slug→id resolution step** before `store.Open`, off the same listing `store.List`
   produces, **memoized on the ctx** for one top-level invoke — see the bounds section,
   because `store.List` still rescans on every call even with the daemon shipped. Unknown
   slug and duplicated slug are
   distinct errors, both naming the workspace root they were looked for in.
3. **`scriptInvoker` uses the resolved id when a slug is present**, its own otherwise, and
   passes it as `savedInvoke.workspaceName`. `NotFound` surfaces as a rejected promise —
   matching the existing contract that `gv.invoke` rejects for an unknown path and only
   *resolves* `ok:false` for a gRPC-status failure.
4. **Nothing changes about history, depth, or params.** Nested invokes still record no
   history; the depth counter is already on the ctx and is collection-agnostic; the target
   still reads `params` as `gv.request.params`.

**Streaming stays rejected**, as today.

---

## Typing: not the constraint it looked like

An earlier draft of this doc conceded that cross-collection paths would fall to
`body: any` via the `(string & {})` overload. That concession was aimed at the wrong cost
and is **withdrawn**. Every file needed is on disk and most of the machinery is already
client-side:

- `Collection` on the wire already carries `services` and `descriptor_set` — the latter
  documented as a "Merged, deduped FileDescriptorSet" (`workspace.proto:186`).
- `useCollectionItems(ids)` (`workspace-query.ts:238`) already issues one `Get` per
  collection on keys **identical** to `useWorkspace`'s, with a test asserting exactly
  that. Today `queryIds` is the active collection plus every expanded row; holding N
  collections client-side needs no new query layer.
- `gvRequestMapDts` keys `GvRequestMap` on **arbitrary strings**. `"//identity:Auth/Login"`
  is a legal key and completes inside the quotes with **zero** TypeScript grammar work.

### The cost is a cold start per collection, and it is already memoized in-process

`Get` → `loadCollection` → `definitionsFor` (`definitions.go:231`) resolves every source:
dials reflection, shells out to `bazel build`, reads uploads. But that is the **cold** path
only. `definitionsCache` (`definitions.go:49`) already holds derived `definitions` **in
process**, keyed by collection id, epoch-guarded against a reader memoizing a view it
derived from pre-write blobs. Sources are not re-read per operation; they are re-read per
*process*, on first touch of each collection.

So the shape of the cost is: **N first-loads, once per process lifetime** — not N per
operation, and not N per keystroke. **The daemon shipped, so that process is now long-lived
for every surface**, CLI included: what used to be re-linked descriptors and a recompiled
QuickJS engine on every `grpcview invoke` is now a warm memo. Eager workspace-wide typing
is therefore a daemon payoff already banked, not a cost to design around.

Which retires the "lazy, driven by references" scheme an earlier draft proposed (parse
`//<slug>:` out of the open editors, `Get` only those). It solves a per-operation cost that
the memo already solves, at the price of a text scanner and a dependency on editor
contents. Union every collection's descriptor set with the active one, dedup by file name,
run `generateWorkspaceTypes` **once** over the union. Dedup is what keeps it cheap: the
union is the size of the monorepo's proto set, not N× a collection's.

### Two bounds this makes load-bearing

- **`definitionsCacheSize = 16`.** Workspace-wide typing makes the working set *every*
  collection, so a workspace with more than 16 thrashes the LRU — and a thrash here is not
  a recompute, it is **re-dialing a reflection server or re-running `bazel build`**. The
  daemon pushes the same constant the *other* way: an unevicted entry per collection, now
  held for the daemon's idle hour rather than one command, is a slow leak. Both pressures
  are real and they point opposite ways; the bound needs choosing with both in view rather
  than inherited.
- **`store.List` still rescans on every call** (~130ms on a 5k-directory monorepo), and
  **slug resolution reads that listing**. This is not fixed by the daemon: an in-memory
  listing invalidated by fsevents was one of the five things it deliberately did *not*
  ship — *"No filesystem watcher, so `store.List` still rescans … the listing-cache payoff
  is the one part not built"* ([`shipped/daemon.md`](../shipped/daemon.md), deviation 4).
  So a scenario doing 50 nested cross-collection invokes would pay 50 rescans, and the
  ctx-scoped slug→id memo is **the** fix rather than an interim one. The watcher remains a
  want, and this is a second reason to build it.

Two hazards in that merge:

1. **The memo breaks silently.** `proto-types.ts`'s `cache` is a
   `WeakMap<Uint8Array, …>`, so a freshly merged array is a new object on every render and
   the memo never hits — protoc-gen-es re-runs continuously. Memoize the merge itself on
   the `(id → descriptorSet reference)` tuple, the same signature-ref trick
   `useCollectionItems` already uses to keep its map identity stable.
2. **Same file path, different bytes.** Collection A resolves
   `proto/payments/v1/payments.proto` from a bazel source pinned at commit X; collection B
   gets that same file from live reflection off a server built at commit Y. Dedup by file
   name picks a winner silently, and the editor then types a message from the wrong
   version — green in Monaco, wrong on the wire. Rule: the **active** collection wins, and
   a same-path/differing-bytes hit is reported, not swallowed.

### What goes workspace-wide, and what must not

| scope | what | why |
|---|---|---|
| workspace-wide | generated descriptor types, `GvRequestMap` | the whole point of this section |
| collection-scoped | **ambient script identifiers** — generators | correctness, see below |
| method-scoped | `RequestMessage` | stays in `Editor.tsx`, untouched |

The middle row is not a preference. The backend calls `loadGenerators(spec.workspaceName)`
— only the *invoking* collection's generators. Merging every collection's generators into
one ambient namespace would let the editor complete a name that then fails at bundle time
with `no generator named`, and would collide by construction across collections.
`registerGeneratorLibs` already takes a `scope` parameter; it keeps it. Middleware and
scenario sources are collection-scoped for the same reason.

---

## Trust and blast radius

A label can only address a collection **inside the current workspace root** — `store.Open`
enforces that already, and this adds no new path source. So the workspace trust list
(`wsroot.configRoot`) keeps meaning what it means: trusting a workspace trusts the code in
it, and cross-collection invoke moves nothing across that boundary.

Worth stating anyway, because it is the thing to check if the label grammar is ever
extended to reach outside the root: **don't**. A path that leaves the workspace is a
different feature with a different consent story.

---

## Surfaces

- **CLI.** `grpcview invoke` takes `--collection <path>` today and keeps doing so — it
  addresses a collection directly rather than cross-referencing one, and its argument is a
  documented path. A label passed as the *path* argument should be accepted, with a
  `--collection` also present being an error.
- **MCP.** `invoke_saved` / `invoke_saved_streaming` take a collection argument; same
  answer.
- **VS Code.** Nothing — it calls the same RPCs.

---

## Open questions

- **Does a cross-collection target inherit *its own* folder-metadata chain?** The code says
  yes (see "The nesting semantics are already right"). Confirm with a test, because
  "authorization silently missing on cross-collection calls" is exactly the bug this shape
  invites.
- **Does the Scripts view need to show slugs?** A slug is invisible in the UI today, and
  writing a label means knowing one. The TopBar picker already appends the id when two
  collections share a name; the slug wants similar treatment somewhere.
- **Who owns removing the root collection?** It is listed here as a prerequisite, but its
  blast radius (store, proto comments, AGENTS.md, the UI empty state that offers the path
  a `NotFound` just came back for) is wider than this track. It may want its own doc.
- **What `definitionsCacheSize` should be**, given the two opposite pressures above.
- **Narrowing `fileToGenerate`.** `generateWorkspaceTypes` currently generates *every*
  non-WKT file in the set, which over-generates even for one collection. Narrowing it to
  the files actually named by `collectInvokeTargets` would shrink the union case a lot —
  but only with the transitive closure of files reachable through field types, or the
  generated `_pb` modules' cross-imports dangle and Monaco errors. Real work, not a
  freebie.
