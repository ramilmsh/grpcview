# Codebase audit plan

A repo-wide tightening pass: one agent per module, run in parallel, in the spirit
of `/simplify` — reuse, simplification, altitude — plus the two things
`/simplify` on a diff cannot see: **naming drift** and **cross-module
duplication**.

**Not in scope:** bug hunting (that is `/code-review`), behavior changes, new
features, perf rewrites, new tests. A finding that changes what the program does
is out, with one exception: deleting dead code, which `AGENTS.md` §Project Stage
explicitly licenses ("Dead and legacy code should be deleted on sight").

---

## Why not just run `/simplify` per directory

Per-module review is blind to exactly what the audit is for. Sixteen agents each
looking at one package will each conclude their local naming is consistent, and
none will notice that the same concept is called `collection` in the store,
`workspace` on the wire, and `tree` in the UI. Same for duplication: a helper
reimplemented in three packages looks original in all three.

So the plan is three layers, and the middle one is the point:

1. **Deterministic scans** (no LLM) produce evidence: duplicate blocks, dead
   exports, a vocabulary census.
2. **Per-module audits** run in parallel, each handed the slice of that evidence
   that touches its files.
3. **Cross-cutting synthesis** reads all module reports *plus* the global scans
   and finds what only shows up in aggregate.

---

## Phase 0 — freeze and scan (main thread, no agents)

1. Commit or stash the two dirty docs so the tree is clean, then pin the audit to
   that SHA. Every finding cites `path:line`; line numbers must not move
   underneath the agents.
2. Write and run three AST-based scanners into the scratchpad. All run offline
   and cost zero agent turns.

   **Parser choice: the official per-language parsers, not tree-sitter.**
   tree-sitter buys error-tolerant parsing across many grammars; this repo has
   exactly two languages, and both ship an official parser that additionally
   does semantic analysis, which tree-sitter cannot do. It is also not installed
   and cannot be (`GOPROXY=off`, nothing in `node_modules`). Concretely:

   - **Go** — `go/parser` + `go/ast`. Stdlib, zero new deps, no network. Built
     as a `go_binary` under `tools/`.
   - **TypeScript** — the `typescript@5.9.3` compiler API already in
     `node_modules`. `ts.createSourceFile` for ASTs, and crucially
     `ts.LanguageService` for exact find-all-references.

   Symbol resolution is the reason. "Is this export referenced outside its own
   module" is a semantic question; the LanguageService answers it precisely,
   whereas a syntax-only parser degrades it to grep with a false positive on
   every matching string and comment.

   - **`dupes`** — **AST-subtree clone detector.** Hash every subtree above a
     size threshold with identifiers canonicalized to positional placeholders
     and literals bucketed by type, then cluster by hash. Catches
     structurally-identical-but-renamed clones, which is most real duplication
     and precisely what a token/line-shingle detector misses. Go and TS scanned
     separately. This is the ground truth for "duplicate code" — an LLM asked to
     recall duplication across 21k lines will miss most of it.
   - **`dead`** — exported-symbol census. TS: `ts.LanguageService`
     find-all-references per export, exact. Go: exported idents in package X
     with no referencing identifier outside X, excluding `_test.go` and Connect
     handler methods.
   - **`vocab`** — identifier census taken from the ASTs, so every term carries
     its declaration kind (type / field / func / local). Kind is what makes
     synonym detection sharp: `source` the type and `source` the loop variable
     are not the same signal. Frequency table plus a synonym-suspect report for
     known-risky pairs (`collection`/`workspace`/`tree`,
     `request`/`node`/`item`, `delete`/`remove`, `get`/`fetch`/`load`/`read`,
     `id`/`key`/`name`/`path`, `source`/`definition`/`descriptor`).

   **Known limit:** `golang.org/x/tools` is not in `go.mod` and adding it needs
   network, so the Go side gets AST-level analysis without full type resolution.
   Go dead-export findings are therefore a reference index rather than a
   type-checked call graph — high recall, with edge cases needing a human
   glance. The TS side has no such gap.

3. Read the outputs and pre-slice them per module, so each brief carries its own
   evidence inline (per `AGENTS.md`: pasting 40 lines beats a 15-turn grep
   expedition).

Already found while sizing this plan, no agent needed:

- `ui/src/features/tree-spike/` is an empty directory — delete. **Done** in
  Phase 0 (untracked and empty, so it never entered a diff).
- `docs/design/tree-spike-findings.md` describes a spike that shipped; check
  against §Design docs ("shipped plans are deleted once their work lands").
  **Checked: it should go, but not unilaterally.** `tree-rewrite-plan.md:15`
  and `:870` cite it as the rationale for rejecting the monaco-tree approach,
  and that plan is still live (T3 typeahead unbuilt). Deleting the findings doc
  therefore also means rewriting those two references. Left for W0 + triage.

### Phase 0 results (run 2026-08-03)

Pinned at **`1440d82`** — every line number in every scan output and evidence
brief is against that commit.

Scanners live in the session scratchpad, not the repo, so no audit scaffolding
has to be deleted later: `audit/go/main.go` (stdlib `go/parser`, subcommands
`dupes|dead|vocab`) and `audit/ts/{scan.mjs,report.mjs}` (the
`typescript@5.9.3` compiler API + LanguageService already in `ui/node_modules`).
They are pure functions of the tree, so a later wave can re-run them to verify
a deletion actually removed the last reference. Both run offline; the Go one via
the `bazel_env` toolchain on a throwaway stdlib-only module, which keeps the
`AGENTS.md` ban on repo-touching bare `go` commands intact.

| scan | Go + proto | TypeScript |
|---|---|---|
| `dupes` | 29 clusters (12 production, 17 test-only), min 40 AST nodes | 11 clusters (9 production, 2 test-only), min 55 |
| `dead` | 198 exported symbols → 1 referenced nowhere, 5 test-only, 20 never referenced outside their package | 215 exports → 0 referenced nowhere, 16 test-only, 24 never referenced outside their file |
| `vocab` | 4,476 (word, decl-kind, site) records | 6,010 records |

Outputs: `dupes-{go,ts}.{tsv,txt}`, `dead-{go,ts}.{tsv,txt}`,
`vocab-{go,ts}.tsv`, `vocab-census.txt`, `vocab-synonyms.txt` (12 concept
groups), `slice-coverage.md`, and `evidence/<slice>.md` × 17.

**Two corrections to the slice table below, both found by making the slicer
prove coverage instead of assuming it.**

1. **The sixteen slices missed 16 `ui/src` files** (562 production LOC):
   `WorkspaceView`, `RequestTabs`, `MessageTab`, `MetadataTab`, `JsonViewer`,
   `FolderMetadataDialog`, `collection-menu`, `delete-confirm`,
   `generator-libs`, `vendor/*`, `index.css`, `vite-env.d.ts`. Added as
   **U8**, so Phase 1 is **17 agents**. Note `MessageTab.tsx` and
   `MessagesTab.tsx` both exist — U2 gets the plural, U8 the singular, and
   whether both should is itself a finding.
2. **Ten Go test files have no production counterpart** and so matched no rule
   (`capabilities_test.go`, `gv_test.go`, `engine_core_test.go`,
   `body_test.go`, `folder_metadata_test.go`, `invoke_history_test.go`,
   `invoke_streaming_test.go`, `metadata_test.go`, `resolve_target_test.go`,
   `scripts_test.go`). Hand-assigned to G5 and G3.

The backend slice LOC in the table below counts production code only; the
frontend counts tests too (U1 says so explicitly). Actual totals per slice,
after the fixes: G3 1376+1972 test, G8 1530+2045 test, U1 1662+2159 test are
the heavy ones. G9 is 682, matching the estimate.

Headline evidence, already visible without an agent:

- **`Editor.tsx` and `MetadataEditor.tsx` are near-clones.** UD01 is a 495-node
  identical subtree (`Editor.tsx:158-247` ≡ `MetadataEditor.tsx:137-226`), and
  UD02 another 162 nodes (`:92-113` ≡ `:85-106`). Together with the twinned
  `body-wrapper.ts` / `metadata-wrapper.ts`, U3 is the largest single
  duplication in the repo.
- **8 clusters span slice boundaries**, which is what no per-module agent could
  have seen: GD04 `store/fs.go` ×2 + `store/scripts.go`, GD11
  `layout.go`≡`scripts.go`, GD16/GD26/GD29 across store↔workspace↔cli tests,
  UD04 `ScriptsView`≡`Editor`≡`MetadataEditor`, UD08
  `CollectionPanel`≡`RequestWorkspace`.
- **The predicted vocabulary split is real.** `collection` (9 declarations,
  store + UI panel), `workspace` (75, wire + Go service + UI hooks), `tree` (42,
  store dir + UI component) name overlapping concepts; `folder` adds 42 more.
  X1 has its opening question.
- `WasmPageSize` (`service/scripting/engine.go:23`) is the one Go export
  referenced nowhere at all.
- 20 Go exports are never used outside their own package, **19 of them in
  `service/scripting`** — that package's surface is exported by habit, not need.
  `Collection.Root` (`store/store.go:99`) and `Engine.RunGenerator`
  (`scripting/profiles.go:107`) have production callers only in tests.
- The `dead` Go pass needed one correction the plan's "known limit" predicted:
  interface-satisfying methods look unreferenced, because the dispatch is
  dynamic and the interface (`connect.Interceptor`) is declared outside the
  scanned tree. The scanner now suppresses any method name matching an
  interface declared in-tree plus a small external list; that alone removed
  five false positives, `logging.go`'s three `Wrap*` methods among them.
  **Treat every remaining Go dead-code line as a lead, not a verdict.**

---

## Phase 1 — module audits (17 parallel agents, read-only)

Slices are sized ~600–1500 source LOC so each agent stays near the ~40-turn
budget. No agent edits anything in this phase; the output is a findings file.

### Backend

| # | Slice | Files | ~LOC |
|---|---|---|---|
| G1 | store / filesystem | `store/{fs,layout,codec,store}.go` | 1260 |
| G2 | store / model | `store/{convert,scripts,sources}.go` | 490 |
| G3 | workspace / invoke | `workspace/{invoke,invoke_saved,gvinvoke,runscript,middleware}.go` | 1370 |
| G4 | workspace / CRUD + server | `workspace/{workspace,sources,describe}.go`, `service/{service,logging}.go`, `echo/`, `cmd/` | 1350 |
| G5 | scripting / runtime | `scripting/{engine,pool,net,marshal,invoke,profiles}.go` | 1150 |
| G6 | scripting / toolchain | `scripting/{bundler,sourcemap,npm,entry,compose}.go` | 730 |
| G7 | cli / read verbs | `cli/{root,client,resolve,ls,get,describe}.go` | 750 |
| G8 | cli / write + invoke | `cli/{write,invoke,body,sources}.go` | 1530 |
| G9 | proto contract | all four `.proto` files | 680 |

G9 is deliberately its own slice: the proto names are the vocabulary both other
layers inherit, so its findings gate several UI and Go renames.

### Frontend

| # | Slice | Files | ~LOC |
|---|---|---|---|
| U1 | tree component | `components/tree/*` (+ its 1.9k of tests) | 1250 |
| U2 | workspace shell | `RequestWorkspace`, `RequestPane`, `ResponsePane`, `MethodHeader`, `TargetBar`, `MessagesTab` | 1290 |
| U3 | authoring editors | `Editor`, `MetadataEditor`, `MiddlewareTab`, `*-wrapper.ts`, `proto-types`, `TypesModal`, `MethodPickerModal` | 1300 |
| U4 | collection panel | `CollectionPanel`, `request-tree.tsx` | 550 |
| U5 | scripts view | `ScriptsView` (956 lines in one file), `monaco-scripts` | 1110 |
| U6 | lib + state + sources | `lib/*`, `features/sources/*`, `App.tsx`, `main.tsx` | 1400 |
| U7 | design system | `components/ui/*`, `components/shell/*`, `theme/*` | 1500 |
| U8 | workspace odds and ends | `WorkspaceView`, `RequestTabs`, `MessageTab`, `MetadataTab`, `JsonViewer`, `FolderMetadataDialog`, `collection-menu`, `delete-confirm`, `generator-libs`, `vendor/*` | 800 |

### Rubric each agent applies

1. **Reuse** — does this reimplement something the repo already has?
2. **Simplification** — same behavior, fewer moving parts.
3. **Altitude** — over- or under-abstracted; a wrapper that only ever wraps once.
4. **Naming** — same concept → same word, and same word → same concept.
5. **Duplication** — within the slice, and against the `dupes` clusters given.
6. **Dead code** — unused exports, unreachable branches, orphan files.
7. **Shape consistency** — does this file handle errors / plumb `ctx` / build
   options / structure hooks the way its neighbors do?
8. **Efficiency** — only obvious waste, not speculative optimization.

### Findings format (strict — 16 reports have to merge)

```
### F-<slice>-<n> · <one-line claim>
axis:       naming | duplication | dead-code | simplification | reuse | altitude | shape | efficiency
where:      path:line[-line]  (+ other sites)
confidence: high | medium        (drop anything lower)
blast:      local | module | cross-module
delta:      ~N lines
what:       ≤2 sentences
fix:        the concrete change; inline the diff if under 10 lines
```

Sorted by blast radius, then delta. One file per slice in the scratchpad.

### Agent settings

`effort: medium` (one tier below session, floored at medium — `AGENTS.md`
§Delegating). Read-only; no Edit/Write except the report. Briefs carry the file
list, the relevant `AGENTS.md` sections, and the pre-sliced scan evidence, so no
agent spends turns discovering its own context.

---

## Phase 2 — cross-cutting synthesis (3 agents, sequential after Phase 1)

Each is fed *all seventeen* module reports plus the global scan output.

- **X1 · Vocabulary.** `vocab` output + every `axis: naming` finding.
  Produces one canonical term per concept and the full rename list across proto,
  Go, TS, CLI flags, and on-disk field names. Pre-release means renames are free
  — no migrations, no `reserved` markers.
- **X2 · Duplication & reuse.** `dupes` clusters + every duplication/reuse
  finding. Decides for each cluster: extract, inline, or leave (three similar
  things that will diverge are not duplication).
- **X3 · Layering & surface.** `dead` output + the module map + `AGENTS.md`
  §Architecture. Asks: is any stated boundary no longer true, does any package
  still earn its existence, does the UI reimplement backend logic, is
  `convert.go` still the only store↔wire bridge?

Main thread then dedupes into one ranked ledger.

### Phase 2 results (run 2026-08-03)

**210 findings → 91 decisions.** All three reports reconcile 1:1 against their
ledger subsets, checked mechanically by `audit/reconcile.mjs` rather than taken
from the agents' self-reports: X1 44→25, X2 74→40, X3 92→26. 69 decisions `high`,
13 `medium`, 9 mixed-per-finding. **Zero `DROP`s**, one `ESCALATE`. Net ~3400
lines touched, of which roughly **−1500 is deletion**.

Ran X1 first rather than all three at once — its canonical-term table is a real
input to the other two, so X2/X3 name what they extract instead of inventing
words that a later rename undoes. X2 and X3 then ran in parallel.

**The ordering held.** The 91 decisions carry 56 constraints, 29 cross-agent, and
the graph is **acyclic in four topological layers** — three agents that could not
see each other's reports produced a globally consistent order. Only two edges
point backward into X1, both caught by X3 because it ran last: `D-X3-7` before
`D-X1-14` (collapse `service/scripting` before renaming its concepts, or you
rename twenty names and delete five of them), and `D-X2-27` before `D-X1-8`.

**51 files are touched by more than one agent, 13 by three or more** — those need
one owner and one pass regardless of wave. The list is in the ledger.

Highest-leverage decision is `D-X1-1`: `collection` names the persisted artifact
on every layer, `workspace` only the UI view. X1 found this doc's own
§Architecture cannot hold one word across two adjacent lines — `:92` "persists
the workspace to disk", `:93` "a filesystem-backed collection" — so the doc was a
fourth instance of the drift, not the authority licensing it.

Three things worth keeping from how the phase went:

- **Both falsifications of §Architecture's disk↔wire claim were real, and the
  code was right both times.** X3 overruled G9's proposed fix on G1's evidence
  and moved the *doc* instead: committed files use `grpcview.store.v1`,
  disposable `.grpcview/` state carries whatever is cheapest. A cache's migration
  story is `rm -rf`.
- **Phase 1 fixes got overruled on checkable facts, not taste** — G2 lost a
  cluster to G1 by claiming a field was "three different types" when all three
  sites assign `*grpcviewstorev1.ItemMeta`; G3's own blank-body fix was too
  aggressive (two of its four guards run on *evaluated* bodies, so normalizing
  would turn a defaulted `{}` into `InvalidArgument`); G5 proposed a test helper
  that already exists twice.
- **X2 found the one thing no per-slice agent could:** collapsing the 14
  identical mutation responses lets `mutate` return the `*connect.Response`
  itself, turning nine 8-line handlers into nine 3-line ones. That deletes one of
  G4's two reasons for retiring cluster GD02, and X2 reaffirms the retirement
  anyway so the shrunken cluster is not read as an invitation.

**Wave-0 hazard for any proto renumber:** `.grpcview/cache/sources/*.binpb`
decodes *wrong* rather than failing, because `SourceResolves` keys its map by
`r.GetId()` and a moved `id` field number yields an empty string. Disposable is
not self-invalidating. Every renumbering wave carries "delete `.grpcview/cache/`"
in its change note, and the browser smoke test starts from a cleared cache or it
is testing stale bytes.

**Defects are now eight, not five.** Phase 2 added: `Tree.tsx:293`'s drag
comparator using `?? 0` where four sibling sites use `?? -1` (sorting an absent
id to the front of a multi-row drag); `format.ts`'s `serviceName` missing the
empty-package guard that both Go and `TypesModal` have; and `History.Response`
carrying one `metadata` field where `Request.Response` has the request/response
split, so recorded history silently loses one direction. `D-X2-8` and `D-X2-5`
fix the last two as side effects — **review those two as behavior changes.**

`AGENTS.md` corrections now total ten plus one new paragraph (the UI layering
rule the doc has never stated, which is why three slices independently found
violations of it). Two were already being reasoned from by Phase-1 agents, and
one had generated a proposed 15-line patch to make correct code match an
incorrect sentence — the cost of a stale doc, measured.

---

## Phase 3 — triage (you)

The ledger lands as a checklist. Some "inconsistencies" are deliberate and only
you know which — `AGENTS.md` §VS Code familiarity, for one, makes some
non-obvious naming intentional. Nothing gets applied without a tick.

### Triage decisions (2026-08-03)

**`D-X3-2` (`Store.Open`) — resolved: rename it, the name is the defect.**
`Open` does no I/O at all (mutex, map lookup, `filepath.Join`, insert,
`return coll, nil`); the failure a reader expects from it is one line later at
`coll.Load(ctx)`, which returns `store.ErrNotFound`. Becomes:

```go
// Collection returns the named collection's handle, creating the in-memory
// record on first use; it does not touch disk.
func (s *Store) Collection(name string) *Collection {
```

after `database/sql`'s `db.Conn(ctx) *sql.Conn`. Both the `error` and the ignored
`ctx` go with it, unblocking `D-X3-3`'s 40 dead branches (11 production + 29
test, verified by grep).

**`D-X1-1` (`workspace` → `collection` everywhere) — REJECTED, and the diagnosis
was wrong.** X1 read the two words as synonym drift and picked one. They are
instead a distinction the project has not made yet: `workspace` is heading toward
the Go/Bazel sense — one workspace, a settings file, several folders inside it,
monorepo-shaped — which makes an inner grouping a *different* thing from the
outer container. Collapsing the words would destroy the distinction just before
it is needed. **Nothing is renamed in either direction.** The counts show why
this matters: `workspace` appears ~940 times against `collection`'s ~225, so the
rejected change was also the most expensive one in the audit.

What survives: `AGENTS.md:92-93` still contradicts itself ("persists the
**workspace** to disk" / "a filesystem-backed **collection**"), but the fix is to
**define both terms**, not collapse them. The store's Go type `Collection` is
today a handle to a whole workspace directory, which is the one place the words
are genuinely misapplied — left alone for now, deliberately, until the workspace
model is decided.

Consequences for the rest of the audit: `D-X1-1` is dropped, `D-X1-8`
(`WorkspaceService`) is dropped, `D-X1-7` and `D-X1-10` need their target names
revisited, and `D-X1-22`'s doc edit changes from collapsing the vocabulary to
defining it. The **seven cross-agent ordering edges out of `D-X1-1` dissolve**,
which removes the audit's biggest wave-1 blocker — deletions can now go first
without fighting a rename.

**`D-X3-17` (Scripts subtabs) — REJECTED as filed.** X3 called the Capabilities
pane a false security claim and deleted it. It is *aspirational*: network is open
today only because capability management is unbuilt, and the pane describes the
state it will be gated behind. The pane stays; only its copy needs to stop
asserting a present-tense guarantee. The Dependencies pane stays on the same
grounds. This also means the Capabilities pane is **not** one of the audit's
tracked defects — the count drops from eight to seven.

Neither G1 nor X3 proposed a rename — both narrowed the signature and left the
misleading name, which is why the dead `error` looked load-bearing to three
separate readers. **And X3 overstated the objection it escalated on:** it called
`_ context.Context` a package-wide convention that excepting `Open` would break,
but `Open` is the *only* method on `*Store`. Every other `_ context.Context` is on
`*Collection`, and all of those touch disk in `fs.go` — so the convention is
"`*Collection` methods carry `ctx` because they do I/O", which `Open` was never a
member of. The rename creates zero exceptions rather than one, so there was less
to trade than the escalation claimed.

---

## Phase 4 — apply in waves

Grouped by blast radius so conflicts are structural, not accidental:

- **W0 · Deletions.** Dead exports, orphan files, empty dirs, stale docs. One
  agent, mechanical, cheap. Biggest LOC win per token spent.
- **W1 · Intra-slice.** Parallel, one agent per slice. Files are disjoint, so no
  worktree isolation needed.
- **W2 · Cross-module renames.** Serial. One agent per rename, each atomic
  across proto → Go → TS, including
  `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy`.
- **W3 · Extractions.** Serial, largest blast last.

Gates after every wave, run by the main thread (agents cap their own verify loop
at two runs then report verbatim, per `AGENTS.md`):

```bash
bazel test //... --nocache_test_results
cd ui && ./node_modules/.bin/tsc --noEmit -p tsconfig.json
bazel build //ui:ui
```

Plus, for any wave touching `ui/`, a browser smoke pass against the prod binary
on `:10000` under an isolated `HOME`. Two known traps to check explicitly: a new
`.go` file missing from `BUILD.bazel` `srcs` compiles as absent, and Bazel serves
cached `PASSED` without running.

Checkpoint commit per wave.

---

## Phase 5 — reconcile the docs

`AGENTS.md` names specific files, directories, and terms throughout. Any rename
or deletion invalidates it, and it is the file every future agent reads first.
One agent, handed the cumulative diff, updates `AGENTS.md` and the design docs
that reference moved code.

---

## Cost

Sixteen Phase-1 agents at ~35 turns each is the bulk of it. Using the repo's own
measured `≈ 1300 × turns²` cache-read curve, that is ~1.6M cache reads per agent,
~25M for the phase, before Phases 2 and 4. Knobs if that is too much:

- Merge slices to ~8 (pair G1+G2, G5+G6, G7+G8, U2+U4, U6+U7) — roughly halves
  agent count but doubles per-agent turns, and the curve is quadratic, so it is
  *not* a saving. Merging is a wall-clock/parallelism trade, not a cost one.
- Cut to backend-only or UI-only and run the other half later. This is the real
  lever.
- Skip Phase 1 entirely for the slices where the deterministic scans already
  found nothing.

### Triage round 2 (2026-08-03)

- **`D-X3-6` generator result cache — delete.** Confirmed; not wired up.
- **`tree-spike-findings.md` — delete**, and rewrite the two `tree-rewrite-plan.md`
  citations to state their conclusion inline.
- **`D-X2-8` (`serviceName` guard) and `D-X2-5` (`History.Response` metadata
  split) — approved as behavior fixes**, reviewed as such rather than as
  refactors.
- **`mdToStringMap` multi-valued metadata — out of scope, fix later.** Written up
  in `known-bugs.md` with three options and a recommendation.
- **Bug writeups live in `docs/design/known-bugs.md`** — anything whose fix is
  longer than a few lines gets an entry there rather than an inline aside.
