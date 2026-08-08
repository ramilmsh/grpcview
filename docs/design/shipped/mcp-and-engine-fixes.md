# The eight things the example rework found

**Status: SHIPPED 2026-08-08 — all eight items.** Behavior lives in `AGENTS.md` (§"The MCP
server", §"Verify through MCP or the CLI, not the browser") and in the scripting section;
this doc is kept for the decisions behind it, not as a worklist. Every claim below was
measured on trunk at **2026-08-08**, during the rework that made `example/` dogfood
grpcview against itself (commit `5dd2813`), whose brief said *report* the tool gaps and
*do not fix the server*. This is that report turned into work.

**What differed from the plan, all deliberate:**

1. **Item 3b is not a fallback, it is the first attempt.** The plan said "before falling
   back to the completion-value path, try compiling as an expression"; that is what
   shipped, and the ordering matters more than it reads: a single-expression scratchpad
   never touches esbuild's statement printer at all, so folding cannot lose it. Anything
   that is not one expression fails to parse in the wrapper and takes the old path, which
   still reports the real error. The wrapper itself was hoisted to
   `scripting.WrapExpression` and `service/workspace/invoke.go` now calls it, so the body,
   the metadata and the scratchpad share one definition of "an expression position".
2. **Item 3's table had a wrong row.** The plan predicted `"a"` → `"a"` and it does, but
   it also predicted `const x = 1; "a" + "b"` leaks `gv` — after 3a that row is *no value*,
   not `undefined`-as-a-string. All nine rows are pinned in
   `service/scripting/engine_core_test.go` (`TestScratchpadValue`).
3. **Item 6 was generalized past what the measurement asked for.** The plan offered
   "strip `services`" or "return only the touched subtree". What shipped is the first,
   turned into a rule: a derived list survives only on the RPCs that can change it
   (`fieldOwners`, `dropSet`), which covers `services`, `scripts` and `history` in one
   table instead of three special cases. Measured on `example`: a request edit's response
   went from 19.8 KB to 7.9 KB. Returning only the touched subtree stays rejected — it
   changes the RPC contract for every surface to save the same bytes at the seam.
4. **Item 5 covers two message types, not one.** The plan scoped the guard to an invoke's
   own response body. `get_collection` replays every *recorded* body out of history, which
   turned out to be the larger source: after the other seven items landed, one measured
   read was 396 KB. `responseBodyOwners` covers `Request.Response` and `History.Response`
   both, and that read is 48 KB.
5. **Item 6's `historyBearingMethod` is gone**, folded into `readMethod` + `fieldOwners`,
   and `TestTrimHeavyFieldsKeepsHistoryOnlyForGet` was replaced by
   `TestTrimHeavyFieldsKeepsDerivedListsOnlyForTheirOwners`.

**One thing the plan's premise got wrong.** Item 6 says `get_collection` is "168 KB of
which `history` is 158 KB and `services` 8.4 KB". Re-measured on the 9-request `example`
at implementation time, the whole `Collection` is 116.6 KB: 87.5 KB descriptor set (already
stripped before this work), 16.0 KB tree, 9.3 KB history, 7.0 KB `services`, 5.5 KB
`scripts`. The 158 KB figure was a collection whose history happened to hold a recorded
descriptor set — which is exactly what item 5's second half now elides.

Building a whole collection through the MCP tools alone is the first time those tools were
the only surface available, and it is a better test of them than any unit test: six of the
eight items below are things that only hurt when you cannot fall back to an editor.

| # | Symptom | Where | Kind | Effort |
|---|---|---|---|---|
| 1 | `add_source` cannot add a reflection or bazel source at all | `service/mcp/schema.go` | **broken** | half a day |
| 2 | `grpcview ls` says a streaming request is `not invocable yet`; it is invocable | `service/cli/ls.go:139` | stale text | minutes |
| 3 | A scratchpad whose value is a folded string literal answers with nothing — or with `gv` | `service/scripting` | **two bugs** | half a day |
| 4 | `invoke_saved` without a `spec` answers `NOT_FOUND: collection not found` | `service/workspace/invoke.go` | misleading error | minutes |
| 5 | An invoke response carrying a descriptor set blows the tool-output limit | `service/mcp/mcp.go` | ergonomics | half a day |
| 6 | Every mutating tool returns the whole `Collection` | `service/mcp/mcp.go` | ergonomics | needs measuring first |
| 7 | `run_script` can only run inline source, never a saved script | `proto` + `service/workspace` | missing feature | half a day |
| 8 | A stale committed reflection snapshot makes a *current* field look unknown | — | documentation | minutes |

Items 1–4 are defects and should land first. 5–7 are API shape. 8 is a paragraph in
`AGENTS.md`.

---

## 1. `add_source` lost the `source` oneof

**What happens.** The tool's input schema exposes four properties — `collection`,
`commit_descriptors`, `file_name`, `path` — and none of the three things a source can
actually be. Its description ends with the client's own admission:

> Input constraint: Provide parameters for at least one of the documented parameter groups
> (flattened from a JSON Schema anyOf).

There are no documented groups, because the groups are what got dropped. Attempts during
the rework produced `proto: syntax error (line 1:10): unexpected token "{\"label\": …}"`
and `{"code":"INVALID_ARGUMENT","message":"unknown source type: <<nil>> <nil>"}`. The
workaround was to call `AddDescriptorSource` through the generic `invoke` tool instead,
which works and is what the collection was built with.

**Why.** `AddDescriptorSourceRequest` (`proto/grpcview/v1/service.proto`) carries
`oneof source { bytes descriptor_set = 2; Server reflection = 3; Bazel bazel = 6; }`. In
the plugin's standard schema mode (`pkg/gen/schema.go:85-135`) a non-synthetic oneof is
emitted as a **message-level `anyOf` of `oneOf` groups** and its member fields are
deliberately kept *out* of `properties`. Our own `annotateSchema` already recurses into
`anyOf`/`oneOf`/`allOf` (`service/mcp/schema.go`, pinned by
`TestAnnotateSchema_OneofAnyOf`), so the branches exist and even carry their `.proto`
comments — but a client that flattens `anyOf` into a single property bag keeps only
`properties`, and the branches vanish before the model ever sees them.

This is the only request-side oneof in the file. The only other one is
`InvokeStreamingResponse.event`, which is a response and unaffected.

**Rejected fix: the plugin's OpenAI mode.** `RegisterServiceOptions.Provider =
runtime.LLMProviderOpenAI` flattens oneof members into `properties` exactly as wanted, and
`runtime.FixOpenAI` converts the arguments back. But the same flag also marks **every
field required** (`pkg/gen/schema.go:130`) and sets `additionalProperties: false`, and it
rewrites `google.protobuf.Struct` into a JSON string. All 26 tools would start demanding
every optional field. Not worth it for one message.

**Fix.** Flatten oneofs ourselves, in `service/mcp/schema.go`, as one more pass in the
same walk that already annotates. For each object schema that has an `anyOf` of `oneOf`
groups:

- hoist each branch's single property into the object's `properties`, unchanged;
- delete the `anyOf` (and any `required` it introduced — the branches stay optional);
- append to each hoisted property's description the note the OpenAI path uses: it is part
  of the `<name>` oneof, only one member may be set, setting two is an error.

Nothing is needed on the argument side: the plugin marshals the arguments back to JSON and
unmarshals them with `protojson` (`pkg/gen/register.go:145`), the hoisted names are real
proto field names, and `protojson` already rejects two members of one oneof with a clear
error rather than silently picking one.

**Tests.** In `service/mcp`: pin the post-processed schema for `AddDescriptorSource` —
`properties` contains `descriptor_set`, `reflection` (with its nested `target`) and
`bazel` (with its nested `label`), and the schema has no `anyOf`. Plus one dispatch test
that `{"collection":"example","bazel":{"label":"//proto/grpcview/v1:grpcviewv1_proto"}}`
reaches the handler as the right oneof case, and one that setting two members errors.

**Verify.** Add a reflection source and a bazel source to a throwaway collection through
`add_source` alone.

---

## 2. `grpcview ls` still says streaming is not invocable

`service/cli/ls.go:139` hand-writes `[server-streaming: not invocable yet]` (and the
client/bidi variants), asserted in `service/cli/ls_test.go:139-140`. It is wrong:
`grpcview invoke "Workspace/Streaming/InvokeStreaming" --collection example` prints one
frame per line and exits 0.

The server-side wording was already fixed for `describe` —
`service/workspace/describe.go:57` sets `NotInvocableReason` to a string that names
`invoke_streaming` / `invoke_saved_streaming` — so only the CLI's own label is stale.

**Fix.** Drop the claim from `ls`: `[server-streaming]`, `[client-streaming]`,
`[bidi-streaming]`. `ls` is a listing; the reason a call would fail belongs to `describe`,
which has it right. Update `ls_test.go` and the "Known gaps" paragraph in
`example/README.md`.

---

## 3. The scratchpad's value is unreliable in two different ways

`RunScript` with no `kind` is a scratchpad and answers with the program's completion value
(`service/workspace/runscript.go` `default:` → `Engine.RunScenario`,
`service/scripting/profiles.go:120`). Three of the example collection's requests depend on
that: it is what makes grpcview able to echo something back to itself without a second
service.

**Measured**, all through the MCP `run_script` tool with `kind` unset:

| Source | Value |
|---|---|
| `1 + 1` | `2` |
| `"a"` | `"a"` |
| `"a" + String(1)` | `"a1"` |
| `"a" + "b"` | *no value at all* |
| `1 + 1;` then `"a" + "b";` | `2` — the string statement is dropped wherever it sits |
| `const x = "a" + "b"; x` | `"ab"` |
| `const x = 1; "a" + "b"` | `{"request":{"params":{}},"metadata":{}}` — **`gv` leaks out** |

**Why.** Two independent defects compose.

*The dropped statement.* The scratchpad compiles through `bundler.compile` → esbuild, ESM
format, `TreeShaking: api.TreeShakingFalse`, no minification
(`service/scripting/bundler.go:107-122`). esbuild constant-folds `"a" + "b"` to a single
string literal, and a lone string-literal expression statement cannot be printed at the
top of a module without becoming a directive, so it is dropped instead. `"a"` on its own
survives only because it *becomes* a directive and QuickJS still reports it as the
completion value. Anything unfoldable (`"a" + String(1)`) survives as a real expression.

*The leaked prelude.* `runCompiled` (`service/scripting/engine.go:244-259`) evaluates
`buildInputPrelude(in) + c.code` as **one program** and takes the program's completion
value. When the user's code contributes none, the value falls back to whatever the
prelude's last expression evaluated to — the `gv` object. When the bundle is empty
outright, the early return at `:245` yields nothing at all.

**Fix, in two parts.**

*3a — stop the leak (one line).* End `buildInputPrelude` with a statement that has no
useful completion value (`void 0;`). The fallback then decodes as `undefined`, which
`runscript.go` already reports as "no value" (`out.Value` is set only when
`res.Value != nil`). Line-count bookkeeping for error mapping is computed from the prelude
string, so it follows automatically.

*3b — capture the value deliberately.* Before falling back to the completion-value path,
try compiling the scratchpad as an expression: `export default async () => (\n<source>\n)`
through the existing `compileEntry` + postlude machinery, which already works for request
bodies and generators and does not depend on esbuild's statement printing. If that fails
to parse — any multi-statement scenario — fall back to today's path, which will surface
the real compile error if the source is genuinely broken. Single-expression scratchpads,
which is what the echo requests are, then have a value that does not depend on whether
esbuild could fold it.

**Tests.** A table test in `service/scripting` pinning all seven rows above, plus the two
that must keep working: a multi-statement scenario ending in an expression, and one ending
in a `gv.assert` call with no value.

**Follow-through.** Once 3b lands, the `example/scripts/trace-headers` middleware can stop
wrapping its rewrite in `( … ).replace(/^/, …)`, and the paragraph in `example/README.md`
plus the `example-collection` memory note both lose a caveat.

---

## 4. `invoke_saved` without a `spec` blames the collection

`InvokeSavedRequest` nests everything in `SavedInvokeSpec spec = 1`. Passing the fields
flat — `collection`, `item_name`, `path` — answers
`{"code":"NOT_FOUND","message":"collection not found"}`, because the absent `spec` leaves
`spec.collection` empty and the lookup fails on `""`. The error sends you looking at the
collection, which is fine, for several minutes.

**Fix.** Validate up front in `service/workspace/invoke.go`: an absent `spec`, or a `spec`
with no `collection`, is `INVALID_ARGUMENT` naming the field. Keep the nested shape — it is
the right proto and it is shared with `InvokeSavedStreaming`.

---

## 5. Descriptor sets inside an invoke response overflow the output limit

`invoke_saved` on `Workspace/DescribeMethod (JSON)` returned **119,061 characters** and
spilled to a file; reading it back cost a `jq` plus a base64 decode.

`trimHeavyFields` / `clearHeavyFields` (`service/mcp/mcp.go`, pinned by
`TestTrimHeavyFieldsStripsEveryShape`) strip `descriptor_set` from every proto response
that has one — but here the descriptor set is base64 inside `DescribeMethodResponse`
(`proto/grpcview/v1/service.proto:339`), which is itself JSON **text** inside
`InvokeResponse.response.body`. A proto walk cannot see into a string.

**Fix.** Add a generic size guard to the MCP layer, on `Request.Response.body` only: parse
the body as JSON, and replace any string value longer than a threshold (8 KB is well above
every real field in the example collection and well below a descriptor set) with a marker
naming the elided size. Nothing about `DescribeMethod` is special-cased, and the shape the
model sees stays a valid object. If the body does not parse as JSON, cap it whole. Say in
the marker that the full value is available through the underlying RPC.

---

## 6. Every mutating tool answers with the whole collection

`CreateScriptResponse`, `UpdateRequestResponse` and the rest all carry
`Collection collection = 1`, so a one-field edit costs roughly 6 k tokens of tree read
back. Stripping `history` everywhere except `Get` already happened
(`TestTrimHeavyFieldsKeepsHistoryOnlyForGet`); measured on `example`, `get_collection` is
168 KB of which `history` is 158 KB and `services` 8.4 KB.

This is the one item on the list that should be **measured before it is designed**: after
`history` is gone, what is actually left in a mutating response is the tree, and the tree
is the part a caller plausibly wants back. Step one is a test that prints the byte size of
each mutating response for `example`. Then choose between stripping `services` from
mutating responses (cheap, ~8 KB) and returning only the touched subtree (a real
projection, and a behavioural difference between the MCP surface and the RPC). It stays the
trade it already is in [`roadmap.md`](./roadmap.md) until the numbers exist.

---

## 7. `run_script` cannot run a saved script

The tool takes inline `source` only, so `smoke` — a saved 13-assertion scenario — has to be
pasted in to run from MCP, or run from the CLI (`grpcview script run smoke`). The CLI and
the UI can both address it by name; MCP cannot.

**Fix.** `RunScriptRequest` gains `optional string script`, exclusive with `source`,
resolved against the collection's scripts and run with that script's own kind. Setting
both, or neither, is `INVALID_ARGUMENT`. The MCP tool inherits it for free.

---

## 8. A stale committed snapshot makes a current field look unknown

During the rework, `invoke` on `AddDescriptorSource` answered
`AddDescriptorSourceRequest has no known field named bazel` — not because the field is
missing, but because the collection's *committed* reflection descriptors predated it.
`refresh_source` fixed it immediately.

Nothing to fix in code; it belongs in `AGENTS.md` next to the MCP verification recipe: a
collection that reflects grpcview itself is describing a *snapshot* of grpcview, and after
a proto change the snapshot has to be refreshed before the new field exists on that path.

---

## Sequencing

1. **2** and **4** — text and a validation branch. One commit.
2. **1** — the only tool that cannot do its job.
3. **3** — the engine defects, then revert the example collection's workaround.
4. **5**, then **7**.
5. **6** — measure first, decide after.

Each step ends the same way it was found: run it through the MCP tools against `example`,
then `bazel test //...`.
