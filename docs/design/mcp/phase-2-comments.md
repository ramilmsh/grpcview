# Phase 2 — the proto comments serve four audiences

**Prereqs:** [phase 1](./phase-1-server.md) (you need a live tool list to read the
descriptions in). **Unblocks:** the CLI track, which reads the same strings for `--help`.
See [`README.md`](./README.md) Decision 5.

## Goal

Rewrite every RPC leading comment in `proto/grpcview/v1/service.proto` so one string works
as a Go doc comment, a TS doc comment, CLI help text, and an MCP tool description. Move the
implementation rationale currently living in those comments into `AGENTS.md` and the design
docs.

**This phase touches comments only.** No RPC, message, or field is added, removed, or
renamed. The proto changes in [phase 3](./phase-3-gaps.md) are separate.

## The two facts that shape everything

1. **The RPC leading comment is the entire MCP description.** `gen.ToolForMethod` takes
   `meth.Comments.Leading`, runs it through `CleanComment` (strip `buf:lint:` /
   `@ignore-comment` lines, trim each line, rejoin) and stores it verbatim as
   `Tool.Description`. Nothing else in the proto reaches an agent.
2. **Field comments reach every audience *except* MCP.** Schemas are built from
   `protoreflect.MessageDescriptor`, and protoreflect has no comment API. So
   `update_middleware`'s carefully documented set-flag protocol, `metadata_script`'s module
   shape, and `file_name`'s identity semantics are invisible to a model — and the schema
   marks every field optional, because grpcview has no protovalidate rules for
   `IsFieldRequired` to find.

**Consequence, stated as a rule:** *a field comment is developer-only. Anything an agent
must know to call the RPC correctly has to be restated in the RPC comment.* That is
duplication, and it is deliberate — the alternative is a broken tool.

## Audit of what is there now

| RPC | Tool | Diagnosis |
|---|---|---|
| `Get` | `get_workspace` | **Stub + dangling name.** "Get returns the workspace snapshot" — the agent never sees "Get", and "snapshot" says nothing about the four things inside it. |
| `AddDescriptorSource` | `add_source` | **Leaks internals; points at an invisible field.** "see AddDescriptorSourceRequest.file_name" is unfollowable; "the merged view is rebuilt from every source's cached resolve" is our problem, not the caller's. |
| `RemoveDescriptorSource` | `remove_source` | **Leaks internals.** Half the comment argues why removal needs no network. |
| `RefreshDescriptorSource` | `refresh_source` | **Adequate but silent on *when* to use it** and on the unreachable-target outcome. |
| `ReorderDescriptorSources` | `reorder_sources` | **Best of the current set** — actually says what it is for. Still omits that `ids` must be the complete set. |
| `CreateFolder` | `create_folder` | **Stub.** Says nothing about `path` vs `item_name`. |
| `CreateRequest` | `create_request` | **Stub.** Four required args, none described. |
| `DeleteRequest` | `delete_request` | **Stub and misleading** — it deletes folders too, recursively, and a missing item is a silent success. |
| `UpdateRequest` | `update_request` | **Stub over the most complex input in the API** — 8 optional fields, two of them needing companion set-flags. |
| `UpdateFolder` | `update_folder` | **Stub.** "updates a folder's metadata script" with no hint what a metadata script is. |
| `Invoke` | `invoke` | **Says nothing about `body`'s form at all.** Per [the body contract](../request-body-contract.md) an agent should be told "a JSON object", with the TS-module form named as the escape hatch for generated values. |
| `InvokeStreaming` | — | **Design history.** "not the bidi the design sketch suggested" is archaeology. |
| `RunScript` | `run_script` | **Leaks internals.** "the scratchpad and the per-kind test-run surface that validates the engine end to end" describes our test strategy. |
| `CreateScript` | `create_script` | **Thin.** Does not say what the three kinds are for. |
| `UpdateScript` | `update_script` | **Thin but honest.** |
| `DeleteScript` | `delete_script` | **Stub.** |

Three failure modes, then: **stubs that restate the method name** (8), **leaked
implementation rationale** (5), and **one undocumented argument that decides whether
the tool works at all** (`Invoke`'s body).

## House style for RPC comments

1. **Verb-first. No leading identifier.** "Calls one unary gRPC method…", not "Invoke
   executes…". This deliberately breaks Go's godoc convention: the comment's least-equipped
   reader is a model that never sees the Go identifier, and in the generated Connect
   interface the method name sits on the line below anyway. One rule, applied to RPC
   comments only — message and field comments keep the `Name does X` form, because their
   only readers are developers who benefit from it.
2. **Line 1 is a self-contained sentence under ~100 chars.** It is the CLI's one-line help
   and the first thing the model reads. It must say the *effect*, not the category.
3. **Then a blank line, then a body in this order:** (a) which arguments are required and in
   what format — this is the only channel for it, since the schema says nothing; (b) what
   comes back; (c) at most one gotcha that changes how you would call it.
4. **Budget ~700 characters, ~8 lines.** `invoke` may exceed it; nothing else should.
5. **No rationale.** *If a sentence explains why the code is shaped this way rather than how
   to call it, it does not belong in an RPC comment.* It goes to `AGENTS.md` or a design doc.
6. **No internal names.** No `gv.metadata.inherit()`, `body-wrapper.ts`, `configDigest`,
   `foldAncestorMetadata`, "merged view", "cached resolve". Describe the observable behaviour.
7. **No cross-references to messages or fields by type name.** "see
   AddDescriptorSourceRequest.file_name" resolves for nobody outside Go. Inline it.
8. **Prose, not markdown.** No headings, no tables, at most a three-item inline list. Most
   clients render the description as plain text.
9. **Name the required fields explicitly**, using the wire (snake_case) names, since that is
   what the JSON schema keys are.

## The rewrites

Ordered as they appear in `service.proto`. `//` prefixes omitted for readability.

**`AddDescriptorSource` → `add_source`**
> ~~AddDescriptorSource adds a descriptor source to the workspace — where its definitions come from. A source whose id already exists is refreshed in place (see AddDescriptorSourceRequest.file_name); a new one is appended at LOWEST priority… the merged view is rebuilt from every source's cached resolve.~~
```
Adds a source of service definitions to the workspace and resolves it, making the
services it describes callable.

Requires workspace_name and exactly one of: reflection (address is a dial string
like "localhost:9000"; set tls for a TLS target) or descriptor_set (raw
FileDescriptorSet bytes, with file_name naming it, e.g. "api.binpb").

Adding a source that is already listed refreshes it in place; a genuinely new one
goes to the end of the priority list, so adding never moves an existing service to a
different source. Returns the workspace, whose services list is the set of
service/method names that can now be invoked.
```

**`RemoveDescriptorSource` → `remove_source`**
> ~~RemoveDescriptorSource drops the source with the given id and rebuilds the merged view from the cached resolves of those that remain — no network, so an unreachable sibling source can never block a removal.~~
```
Removes one source of service definitions from the workspace.

Requires workspace_name and id — a value from the workspace's sources list, e.g.
"reflection:localhost:9000" or "upload:api.binpb". Returns the workspace.

Services that only that source provided disappear from the services list. Saved
requests against them are kept but can no longer be invoked.
```

**`RefreshDescriptorSource` → `refresh_source`**
> ~~RefreshDescriptorSource re-resolves exactly one source (re-dialing a reflection target, re-parsing an upload) and rebuilds the merged view.~~
```
Re-reads one source of service definitions — re-dialing a reflection target, or
re-reading an uploaded descriptor set — and updates the workspace's service list.

Requires workspace_name and id (a value from the workspace's sources list). Returns
the workspace.

Use this after the target server is redeployed with changed or added methods. If the
target is unreachable the source stays listed with the reason in its resolved.error
and contributes nothing, rather than being dropped.
```

**`ReorderDescriptorSources` → `reorder_sources`**
> ~~ReorderDescriptorSources sets the source priority order and rebuilds the merged view from the cached resolves — no network. This is how you switch which source's definitions win when several describe the same protos.~~
```
Sets the priority order of the workspace's definition sources. When several sources
describe the same protos, the earliest one in the list wins — so this is how you
choose whose definitions take effect.

Requires workspace_name and ids: every id currently in the workspace's sources list,
in the order you want. A missing or unknown id fails the whole call rather than
reordering part of it. Returns the workspace.
```

**`Get` → `get_workspace`**
> ~~Get returns the workspace snapshot~~
```
Returns the whole workspace: the tree of saved requests and folders (item), the
definition sources (sources), every callable service with its methods (services),
and the saved scripts (scripts). Creates an empty workspace if none exists.

Requires workspace_name. Start here: the services list is where the service and
method names that invoke takes come from.
```

**`CreateFolder` → `create_folder`**
> ~~CreateFolder creates a new folder in the workspace~~
```
Creates an empty folder in the request tree.

Requires workspace_name and item_name (the folder's display name). path is the
display names of the folders to nest it under, outermost first; leave it empty for
the top level. Returns the workspace.

A name that collides with an existing sibling fails.
```

**`CreateRequest` → `create_request`**
> ~~CreateRequest creates a new request in the workspace~~
```
Creates a saved request bound to one method, with an empty body.

Requires workspace_name, item_name (the request's display name), service (fully
qualified, e.g. "user.v1.UserService") and method (the bare method name, e.g.
"CreateUser") — both exactly as they appear in the workspace's services list. path is
the display names of the folders to nest it under, outermost first; leave it empty for
the top level. Returns the workspace.

Saving a request is optional: invoke can call a method without one. Create a request
when the call is worth keeping and re-running.
```

**`DeleteRequest` → `delete_request`**
> ~~DeleteRequest deletes a request from the workspace~~
```
Deletes one item from the request tree. Deleting a folder deletes everything inside
it.

Requires workspace_name and item_name; path is the display names of the enclosing
folders, outermost first. Returns the workspace.

Deleting something that is not there succeeds silently, so check the returned tree
rather than relying on an error.
```

**`UpdateRequest` → `update_request`**
> ~~UpdateRequest updates a request in the workspace~~
```
Changes one saved request. Everything except the fields that locate it is optional,
and an omitted field is left as it was.

Requires workspace_name and item_name, plus path (the enclosing folder names,
outermost first) to locate the request. Can change: name (renames it; a collision
with a sibling fails), service and method, draft_body (a TypeScript module — see
invoke), draft_metadata_script (a TypeScript module returning
{header: [values]}), target (only when update_target is true; leaving target unset
there reverts to the source's own address) and middleware (only when
update_middleware is true; an empty list clears it). Returns the workspace.
```

**`UpdateFolder` → `update_folder`**
> ~~UpdateFolder updates a folder's metadata script in the workspace~~
```
Changes a folder's metadata script: a TypeScript module returning
{header: [values]} that requests inside the folder can inherit, so shared headers
are written once.

Requires workspace_name and item_name, plus path (the enclosing folder names,
outermost first). draft_metadata_script replaces the script; present but empty clears
it; omitted leaves it unchanged. Returns the workspace.
```

**`Invoke` → `invoke`** (over budget on purpose — it is the tool that matters)
> ~~Invoke executes a unary RPC against a target server and returns the result (status, response body, request/response metadata, latency).~~
```
Calls one unary gRPC method on a server and returns the result: the gRPC status, the
response as JSON, the request and response metadata, and the latency.

Requires workspace_name, service (fully qualified, e.g. "user.v1.UserService") and
method (the bare method name), as listed in the workspace's services.

body is the request message as a JSON object, e.g. {"field": "value"}. Quote any
64-bit integer ("id": "123") — an unquoted one loses precision. body may instead
be a TypeScript module whose default export returns that object
(export default () => ({ field: "value" })), which is how a body calls a
workspace generator for a value like a fresh uuid — prefer plain JSON otherwise.
An empty body sends an empty message. metadata maps a header name to a string or a
list of strings. target overrides where the call goes; without it the call goes to
the first reflection source serving that service. Passing path and item_name of a
saved request also applies that request's middleware and records the call in its
history.

A non-OK gRPC status from the called server is returned in the result's status, not
as an error — only grpcview's own failures are errors (no target, unknown method, a
body that does not fit the request type). Streaming methods are rejected; this calls
unary methods only.
```

**`InvokeStreaming`** (no tool; rewritten for the other three audiences)
> ~~…Modeled as server-streaming — not the bidi the design sketch suggested — because the browser transport (connect-web) cannot stream a request body; see InvokeStreamRequest for how client messages are supplied.~~
```
Calls a method of any streaming kind (unary, server-, client- or bidi-streaming) and
streams the response messages back, followed by one terminal result frame carrying
the final status, metadata and latency.

Requires workspace_name, service and method. All client messages are supplied up
front in messages, because a browser transport cannot stream a request body — there
is no live interleave.
```
The bidi-vs-server-streaming rationale moves to `AGENTS.md`; the frame protocol stays where
it already is, on `InvokeStreamResponse`.

**`RunScript` → `run_script`**
> ~~RunScript evaluates a script through the scripting engine and returns its value, console output, and any error — the scratchpad and the per-kind test-run surface that validates the engine end to end.~~
```
Runs TypeScript or JavaScript in grpcview's sandbox and returns its value, its
captured console output, and any error.

Requires workspace_name and source. kind picks the calling convention: GENERATOR
calls the module's default export, MIDDLEWARE calls handle(ctx), and omitting kind
evaluates the source and returns its last expression.

A script that throws or times out is reported in error, not as a failed call. The run
touches no workspace state. The sandbox has fetch but no filesystem access.
```

**`CreateScript` → `create_script`**
> ~~CreateScript creates a new, empty script of a given kind in the workspace.~~
```
Creates an empty saved script.

Requires workspace_name, name (unique among the workspace's scripts) and kind:
GENERATOR (a helper a request body can call), MIDDLEWARE (rewrites a request just
before it is sent) or SCENARIO. Returns the workspace; use update_script to fill in
the source.
```

**`UpdateScript` → `update_script`**
> ~~UpdateScript edits a script's source and/or renames it.~~
```
Changes a saved script's source, or renames it.

Requires workspace_name and name (the script's current name). source replaces the
source; new_name renames it and fails if another script already has that name. An
omitted field is left unchanged. Returns the workspace.
```

**`DeleteScript` → `delete_script`**
> ~~DeleteScript removes a script from the workspace.~~
```
Deletes a saved script by name.

Requires workspace_name and name. Returns the workspace.

Requests that still list the script as middleware keep the reference and fail when
invoked, so clear it from them first.
```

## Field comments worth rewriting (for the other three audiences)

These never reach MCP, so the column that matters is *what got lifted into the RPC comment
above*. Rewrite them for the dev/CLI/VS Code readers, and delete the parts now stated better
upstream.

| Field | Today | Change |
|---|---|---|
| `InvokeRequest.body` | **no comment at all** | The most important missing comment in the file. Per [the body contract](../request-body-contract.md): the request message as a JSON object, or a TS module default-exporting one — two forms, because valid JSON is already valid TS. Do **not** write "a bare JSON object is not accepted" — that was true of `emptyTSBody` (`invoke.go:48`) and is exactly what the contract retires. Do mention that 64-bit integers must be quoted strings. |
| `InvokeRequest.metadata_script` | 7 lines incl. "same engine as the TS body, generators composable as ambient globals" and "Carries the editor's current source, like `body`, so a send never depends on a prior UpdateRequest landing first" | Keep the module shape, add that plain JSON works too, cut the editor rationale — it is a UI concern and now stated in the RPC comment as "carries unsaved editor state". The name `metadata_script` becomes a misnomer once JSON is accepted; renaming it to `metadata` (absorbing the redundant `Struct` field) is a contract-level cleanup, tracked there rather than here. |
| `UpdateRequestRequest.update_middleware` / `update_target` | Explains the proto3 `optional`-on-`repeated` limitation | Keep — this is genuine developer-facing protocol. The *behaviour* is now also in the RPC comment; the *reason* stays here. |
| `AddDescriptorSourceRequest.file_name` | Good, but the identity rule is only here | Keep, and note the RPC comment now duplicates the caller-visible half. |
| `ReorderDescriptorSourcesRequest.ids` | "a missing or unknown id is an error rather than a partial reorder, so a stale client can never silently drop a source" | Keep verbatim — a model rewrite of this would lose the reason. |
| `Request.Response.response` | **no comment** | Add: JSON text of the response message. Phase 3 also changes its type. |
| `InvokeRequest.workspace_name` etc. | none | Leave uncommented. If VS Code phase 1 deletes the field, commenting it now is wasted work. |

## What moves out of the proto

Into `AGENTS.md`:
- Why `InvokeStreaming` is server-streaming rather than bidi (browser transport).
- That the merged view is re-derived from per-source caches on every mutation, and that
  remove/reorder never touch the network — this is already documented at length in
  `AGENTS.md` §"Definition sources", so the proto copy is pure duplication and should just
  be deleted.
- That `RunScript` is the surface that exercises the engine end to end.

Into a new `AGENTS.md` §"Proto comments are the four-surface contract": the nine house rules
above, plus the reason they exist (field comments do not reach MCP; nothing is `required`).
This is the durable artifact — this phase doc gets deleted when the work lands, per
`docs/design/`'s rule.

## Verify

- `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy` then `bazel build //...` — comment
  changes must land in the committed `.d.ts` and break nothing.
- Re-run [phase 1](./phase-1-server.md)'s `tools/list` smoke test and **read all 15
  descriptions as a block**. This is the actual test: if you cannot tell from the list alone
  which tool to call for a task, the rewrite is not done.
- In a real client, with no other help: ask the agent to add a source and invoke a method on
  the echo server. It must get `body` right **on the first attempt** — that is the specific
  regression the `Invoke` rewrite exists to fix.
- Hover a couple of RPCs in the UI (Monaco reads the `.d.ts`) to confirm they still read
  well for a developer. If a rewrite reads badly there, rule 1 was applied too bluntly.

## Out of scope

Adding, removing or renaming any RPC, message or field; the `bytes` → `string` change
(phase 3); `buf lint`/`buf format` configuration; comments in `workspace.proto` beyond the
handful listed above; `grpcview.store.v1` comments, which have exactly one audience.
