# Fixes found while populating `example/` through MCP

Written 2026-08-07, after building the `example` collection end to end using only the MCP
tools (`create_*`, `update_*`, `move_item`, `invoke_saved`, `run_script`) plus the CLI for
verification. Every item below is a defect or a gap that surfaced *because* an agent, not
the UI, was the author. Nothing here is built.

Ordered by how much it hurts the agent surfaces.

---

## 1. A mutation returns the whole collection, including every request's history

**What happens.** Every write tool — `create_script`, `update_script`, `create_request`,
`update_request`, `create_folder`, `update_folder`, `move_item`, `delete_script` — returns
a full `Collection`. In the `example` collection that response is ~186 KB, which is over
the MCP client's per-result token cap, so **every single mutation in the session came back
as an overflow error** and had to be re-read off disk with `jq` to confirm it landed.

**Where the bytes are.** Not `descriptor_set` — `service/mcp/mcp.go:97` `clearDescriptorSets`
already blanks that field (measured: 2 bytes). The bulk is `item`, at 174 KB of a 186 KB
response, and 160 KB of *that* is a single request's `history`: `Collections/DescribeMethod`
returns a `DescribeMethodResponse` whose body carries a base64 `descriptorSet`, and every
recorded run keeps a full copy. History grows without bound, so this gets worse with use,
and a collection does not have to be unusual to hit it — this one has 9 requests.

**Why the obvious fix is wrong.** "Strip history in the MCP shim" is one line in the
existing `clearDescriptorSetFields` walk, but `get_collection` is an agent's *only* access
to history — no other tool exposes it. Stripping it everywhere removes a capability to fix
a payload problem.

**Suggested shape**, in preference order:

1. **A mutation should not return the collection at all.** The CLI already decided this:
   "a mutation prints nothing and exits 0 — silence is success" (`AGENTS.md` §The CLI).
   Return the mutated node (the `Script`, the `Item`) or an empty response. This is an RPC
   contract change, so it touches the UI's query invalidation too — check whether
   `ui/src/features/workspace/` relies on the returned collection to reseed its cache
   before changing the proto.
2. **If the response must stay a `Collection`**, clear `history` on *mutation* responses
   only, leaving `get_collection` whole. Same walk, different registration.
3. **Independently, cap history growth**: `history` should keep the last N entries, and a
   recorded response over some byte cap should be elided with a marker rather than stored
   verbatim. A descriptor set in a response body is not a pathological case — `describe`,
   `get_collection` and `list_collections` all return large payloads, and grpcview's own
   API is the thing agents are most likely to point this tool at.

**Acceptance:** running the same sequence that built `example` (10 mutations) produces zero
overflow results, and `get_collection` still returns history.

---

## 2. Request metadata is evaluated raw; only the body gets the expression wrap

**What happens.** `resolveInvokeBody` (`service/workspace/invoke.go:359`) wraps a body that
has no `export default` in `export default async () => ( … )`, so a bare object literal is a
valid body on every surface. The two metadata seams do not:

- `resolveInvokeMetadata` (`invoke.go:445`) passes `metadataScript` straight to
  `RunRequestBody`.
- `foldAncestorMetadata` (`invoke.go:536`) passes each folder's script straight through.

A bare `{ "x-demo-suite": ["echo"] }` therefore parses as a *block statement* and fails with

```
folder "Echo" metadata: failed_precondition: cannot evaluate the request metadata:
scripting: bundle failed: Expected ";" but found ":" (script.ts:3:16)
```

The browser never sees this because `metadata-wrapper.ts` stores the wrapped module. Every
other author — MCP, CLI, an agent, a human editing `folder.json` — hits it, and the error
names a line in a file they did not write.

**Suggested shape.** Apply the same wrap at both metadata seams, so `resolveInvokeBody`'s
rule ("a module, or an expression that gets wrapped") is the *one* rule for all four script
positions: body, request metadata, folder metadata, middleware. Factor the
`HasDefaultExport`-then-wrap into one helper and call it from all three sites.

**Watch out for:** line-number remapping. `remapJSError` subtracts the prelude lines; adding
a wrap line to metadata must adjust the same way the body path does, or every metadata
error moves off by one.

**Acceptance:** a folder metadata script saved as a bare `{ … }` object evaluates, and its
syntax errors still report the author's line number.

---

## 3. Middleware cannot call the collection's generators

`applyRequestMiddleware` (`service/workspace/middleware.go:66`) calls `RunMiddleware` with no
generator map, while every body/metadata path calls `RunRequestBody` with
`transitiveGenerators(...)`. So `requestId()` works in a body and is a `ReferenceError` in a
middleware — the asymmetry is real, undocumented outside the code, and the natural use for a
middleware (stamp a trace id, sign a request) is exactly what a generator is for.

**Decide, then write it down**: either compose generators into the middleware path too
(`compileMiddleware` would need the same `buildEntryBundleComposed` treatment
`compileRequestBody` gets), or state the restriction in `AGENTS.md` §"The `gv` global" and in
the middleware editor's placeholder text.

---

## 4. `example/` cannot be brought up with one command

The collection only works with two servers running: `//service/echo/cmd` on `127.0.0.1:50055`
(the target of every `Echo/` request) and `//service/cmd/dev` on `localhost:10000` (the
reflection source, grpcview serving itself). Today that is two `bazel run`s in two shells,
described in prose in `example/README.md`.

**Suggested shape.** A `//example:up` target (or `tools/example-up.sh`) that starts both,
waits for both ports, and holds. Then `example/README.md` collapses to one command, and CI
can run `grpcview script run smoke --collection example` as a real end-to-end test — which
is the actual prize here: `smoke` is 10 assertions across bodies, generators, folder metadata,
middleware and `gv.invoke`, and nothing runs it automatically.

**Watch out for:** `//service/cmd/dev` writes to the real per-workspace state dir under
`os.UserConfigDir()`. A CI or throwaway run wants an isolated `HOME`, or the run pollutes the
developer's history.

---

## 5. Streaming: `Unimplemented` is only discoverable by invoking

`invokeUnary` rejects any streaming method (`service/workspace/invoke.go:79`), but nothing
upstream says so: the tree tags method kinds, `grpcview ls` lists streaming requests
identically to unary ones, `describe` describes them, and `create_request` accepts them
happily. An agent authoring against this API finds out at invoke time, after saving.

Not a request to implement streaming (that is `invoke_stream` in
[`roadmap.md`](./roadmap.md)). The cheap half is **honesty at authoring time**: have
`describe_method` / `ls` mark a streaming method as not-yet-invocable, and have
`create_request` return a warning rather than silence.

---

## Non-fix: a rename never re-slugs the directory

Recording this so nobody "fixes" it. `UpdateScript` (`service/store/scripts.go:77-89`) writes
`meta.Name` and leaves the directory slug alone, so renaming `test-goals` to `smoke` leaves
`scripts/test-goals/script.json` with `"name": "smoke"` inside. This initially reads as a bug,
but requests (`fs.go:245`) and folders (`fs.go:305`) do exactly the same thing: **the slug is
the on-disk identity and the display name is data**. Changing it for scripts alone would break
that invariant and churn git history on every rename.

What is missing is one line in `AGENTS.md` §"The workspace and its collections" saying so —
an agent that creates `requestId` and then sees `scripts/requestid/` has no way to tell
whether the drift is intentional.
