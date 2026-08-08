# grpcview — workspace diagnostics

**Status:** Planning only (this doc). **Not started.**

**Why this exists.** Two sibling tracks deliberately allow references to break:
[`script-imports/`](../shipped/script-imports) makes a script import a path from the workspace
root, so moving a file breaks its importers; [`cross-collection-invoke.md`](./cross-collection-invoke.md)
lets a script address a request in another collection by slug, so a slug change or a
duplicate breaks the label. Both are the right trade — a workspace is a static unit and
moving things inside it is refactoring — **on the condition that the tool says so loudly.**
This doc is that condition.

It is also the answer to a problem that predates both: every file in a collection is
hand-editable committed content, and nothing today reports the incongruities that hand
editing and `git merge` introduce.

**One analysis pass, one finding model, three surfaces.** Not a per-feature warning bolted
onto each code path.

---

## The finding model

A finding is `{severity, code, message, location}` where `location` is a workspace-relative
file path plus an optional line, and `code` is a stable machine-readable slug
(`broken-import`, `duplicate-slug`, …) so a surface can filter and a user can suppress.

**Two severities, and the split is what CI keys on:**

- **error** — the thing is broken now. A body that cannot bundle, an import that resolves to
  nothing, a request naming a method no source defines.
- **warning** — the thing works but is probably not what was meant. A script nothing
  imports, a duplicated slug (both collections stay usable, they are only unaddressable *by
  that slug*), a middleware file whose shape does not match the signature.

**Not a third severity, and not a finding at all: "unanalyzable."** A computed
`require(expr)` or a `gv.invoke(someVariable)` cannot be resolved statically. Reporting it
as a defect trains people to ignore the report. Report it as **coverage** — "3 invoke paths
were not statically resolvable" — in the summary, not the findings list.

### What must never become a finding

- **`meta.name` drifting from the directory slug for requests and folders.** AGENTS.md
  documents this as intended: the slug is what UI state and on-disk references key by, and
  re-slugging would churn git history on every rename. [`script-imports/`](../shipped/script-imports)
  narrows the rule for *scripts* only, by making the filename the identifier. Requests and
  folders keep the split, and flagging it would be flagging the design.
- **A collection with no slug.** Per the invoke doc, that collection is simply not
  addressable as a cross-collection target. It is a complete, working collection.
- **A glob in `grpcview.work.json` matching nothing.** Already deliberate (`store.declaredIDs`
  reports a missing *literal* and tolerates an unmatched glob). Keep both behaviours.

---

## The checks

| code | severity | what |
|---|---|---|
| `broken-import` | error | an `@/…` specifier resolving to no file |
| `import-escapes-workspace` | error | resolution landing outside the workspace root — the `withinDir` guard, surfaced instead of only thrown |
| `computed-specifier` | error | a non-literal `require`/`import` — rejected at bundle time too, per the sibling doc, because esbuild reports nothing for it |
| `bundle-failed` | error | any body, metadata script, folder metadata, middleware or script that does not compile |
| `unused-script` | warning | a file under `scripts/` reachable from no entry |
| `broken-invoke-path` | error | a **literal** `gv.invoke` path resolving to no saved request |
| `invoke-target-streaming` | error | a literal `gv.invoke` path naming a streaming request, which `gv.invoke` rejects at runtime |
| `broken-middleware-ref` | error | `Request.middleware` naming a script that does not exist |
| `middleware-shape` | warning | a middleware file whose default export does not satisfy `GvMiddleware` |
| `duplicate-slug` | warning | two collections claiming one slug — moved here from the invoke doc |
| `unknown-slug` | error | a literal label naming no collection |
| `missing-collection` | error | a **literal** entry in `grpcview.work.json` `collections` that does not exist |
| `collection-at-workspace-root` | error | a `grpcview.json` at the root, which makes `store.scan` prune at the first hit and hide every other collection |
| `unresolvable-source` | error | a definition source that fails to resolve — **network/bazel gated, see below** |
| `method-not-in-sources` | error | a request naming a `service/method` absent from every resolved source — same gate |

Three of these are the invoke track's, listed here because a workspace has one report, not
three. That doc keeps the *decisions* (why duplicates are unpreventable, why a label naming
one is a hard error rather than a coin flip); this doc owns the *reporting*.

---

## The import graph comes from esbuild, and there is no second parser

Build with `Metafile: true, Write: false` and read `inputs[*].imports`, which carries the
resolved `path`, the `kind` (`import-statement` / `require-call` / `dynamic-import`) and
`original` — the author's specifier verbatim. [`script-imports/`](../shipped/script-imports)
has the measured output and the reasoning for why tree-sitter is not the tool.

The point for this doc: **the graph the analysis reads is the graph the bundle used**, so a
finding cannot disagree with a build.

Two properties shape the implementation:

- **The metafile is forward and reachable-only.** A script nothing imports does not appear
  in it at all. So `unused-script` is a *diff*: enumerate `**/scripts/**/*.ts` on disk, and
  subtract everything the graph reached.
- **One build, if every entry is a file.** esbuild accepts multiple `EntryPoints` in a
  single build and the metafile covers all of them. Bodies and metadata scripts live inside
  `request.json` today, so they would have to be fed through `Stdin` one at a time — N
  builds. [VS Code phase 2](../active/vscode/phase-2-body-files.md) moving them to sibling
  files collapses that to one, which is the concrete reason to sequence phase 2 first.

`gv.invoke` paths are **not** in the graph — they are string arguments, not module
specifiers. Extracting the literal ones needs its own pass. Do it with the same
conservative-regex approach the sibling doc uses for computed specifiers rather than a
parser: match `gv.invoke("…")` / `invoke("…")` with a literal first argument, count
everything else as unanalyzable coverage.

---

## Network and bazel are gated, and this is the sharpest design call

`unresolvable-source` and `method-not-in-sources` need resolved descriptors, and resolving a
source **dials a reflection server, shells out to `bazel build`, or reads an upload**
(`definitionsFor`, `definitions.go:231`). A `check` that does that by default is a bad
surprise twice over: it touches the network from CI, and it can take minutes on a cold bazel
cache.

So: **the default pass is local-only.** Everything above the gate in the table runs off
files. Source-dependent checks require an explicit opt-in (`--sources`, or the inverse
`--offline` defaulting to true — pick one spelling and keep it). The summary must state
plainly which checks were skipped, or a clean local run reads as a clean full run.

Two bounds this makes load-bearing, both already flagged elsewhere:

- **`store.List` rescans the workspace on every call** (~130ms on a 5k-directory monorepo)
  and is deliberately not cached — and stays that way even with the daemon shipped, since a
  filesystem watcher was one of the five things it explicitly did not build
  ([`shipped/daemon.md`](../shipped/daemon.md), deviation 4). One pass calls it once, so
  this is fine here — but the UI surface must not poll it.
- **`definitionsCacheSize = 16`.** A source-enabled pass touches *every* collection, so a
  workspace with more than 16 thrashes an LRU whose miss is a network dial or a bazel build.
  [`cross-collection-invoke.md`](./cross-collection-invoke.md) flags the same constant from
  the typing side, and the shipped daemon from the leak side (entries now live for its idle
  hour rather than one command); three pressures, one number, chosen once.

---

## Surfaces

### `grpcview check` — the one that matters

CI runs this, so it is the primary surface and the others are conveniences.

The CLI's exit-code contract is already established — `invoke` is 0/1/2, and 2 means
"usage/ambiguity, never a guess" — so `check` inherits it:

- **0** — no errors. Warnings may have been printed.
- **1** — at least one error finding.
- **2** — could not run: bad flags, unresolvable workspace root, `ErrWorkspaceTooLarge`.

`--strict` promotes warnings to the exit-1 set. `-o json` prints the whole finding list,
matching the existing rule that `-o json` prints the full structure while the default output
is for humans. No colors, no TTY detection — permanently, per the CLI's stated constraints.

It takes `--workspace` and, unlike every other verb, **must not require `--collection`**:
the unit of analysis is the workspace. That makes it the first verb where `withCollection`
is not the seam, which is worth a note in the CLI's own docs.

### MCP

A `check_workspace` tool returning the finding list. Note the existing rule it must follow:
*"the invoked call's gRPC status lives in `result`, never in the tool's error channel."*
Same here — findings are the result, not an error. An agent that hand-edits files (which is
the primary way this codebase gets edited) gets a verification loop it does not have today.

### The UI

A **Problems panel**, VS Code's, since copying VS Code where an equivalent exists is the
standing rule: a list grouped by file, click-to-open, a status-bar count, and squiggles in
the editor for findings that carry a line.

One piece of plumbing this needs and does not have: `CollectionSummary.error` is the wrong
field. Today a non-empty `error` means "this collection could not be summarized" and renders
as a broken row. Findings need a distinct workspace-level list on
`ListCollectionsResponse` — the invoke doc reaches the same conclusion from the
duplicate-slug side.

---

## Not a linter

Worth stating so the check list stops growing. This reports **incongruities between files** —
a reference that does not resolve, a declaration nothing satisfies. It does not report style,
naming, complexity, unused variables, or anything a TypeScript config already covers, because
the editor and `tsc` own that and a second opinion is noise.

The test for whether a proposed check belongs: **could a hand edit or a `git merge` have
created it?** If yes it is a diagnostic; if it is just code someone wrote badly, it is not.

---

## Open questions

- **Suppression.** A `unused-script` warning on a file that is deliberately a scratchpad
  needs a way to be silenced — an inline comment pragma, a workspace-level ignore list, or
  nothing at all (and the report stays noisy). Nothing is a legitimate first answer.
- **Does `check` run on save in the UI, or only on demand?** On-save needs an incremental
  pass; a full pass per keystroke is not viable with a workspace-wide build. Probably: the
  editor's own diagnostics cover the open file live, and `check` is explicit.
- **Autofix.** Several findings have mechanical fixes (a moved file's importers, a renamed
  slug's labels). The edit belongs to the TypeScript language service, per the sibling doc's
  division of labour — but a headless `check --fix` for CI is a separate, tempting, and
  probably premature feature.
- **Where the launch warning surfaces**, carried from the invoke doc: a toast, a status-bar
  item, or a banner. It must survive dismissal and reappear next launch while the finding
  stands.
