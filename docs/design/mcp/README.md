# grpcview — MCP server track

**Status:** Planning only. **Not started.**

**Goal.** Expose grpcview's own `WorkspaceService` to AI agents as MCP tools, generated
from the same proto that already drives the web UI, so an agent can point grpcview at a
server, browse its methods, author/save requests, and invoke them.

## Delta vs. the reference implementation

The model is `/Users/r/dev/core/gateways/admin/mcp/mcp.go`: `protoc-gen-go-mcp` turns each
proto service into `<Service>_<Method>Tool` values plus a `ForwardTo…Client` wiring
function, served through the MCP go-sdk. We keep **exactly that** and drop everything
around it: no auth, no header forwarding, no fx, no echo/middleware stack, no tool
whitelist. What replaces them is smaller than what they were: **one 18-service `Params`
struct becomes one in-process handler value**, **one 44-entry whitelist becomes one
15-entry rename map**, and **streamable HTTP behind a gateway becomes a `grpcview mcp`
stdio subcommand of the same single binary**. The reference is a hosted multi-tenant
gateway; this is a local single-user tool, and the whole surface is ~120 lines in one
package.

| Phase | Doc | Summary |
|---|---|---|
| 0 | [`phase-0-dependencies.md`](./phase-0-dependencies.md) | Get three new Go modules into an offline Bazel workspace |
| 1 | [`phase-1-server.md`](./phase-1-server.md) | Generate the tools, rename them, serve them over stdio |
| 2 | [`phase-2-comments.md`](./phase-2-comments.md) | Rewrite the proto comments for four audiences |
| 3 | [`phase-3-gaps.md`](./phase-3-gaps.md) | `DescribeMethod`, response types, streaming |

Phases 1 and 2 together are the minimum shippable unit; **phase 3 is what makes the
server actually useful** (see Decision 6). Phase 0 is a prerequisite with a network
caveat — read it first.

---

## What the plugin actually does (verified, not assumed)

Read from `~/go/pkg/mod/github.com/redpanda-data/protoc-gen-go-mcp@v0.0.0-20260430225748-67e0bd25a988`.
These five facts drive every decision below.

1. **One flag: `package_suffix`** (default `"mcp"`, `cmd/protoc-gen-go-mcp/main.go`). There
   is no tool-name knob, no description knob, no proto option, no extension. Renaming
   must happen in Go.
2. **Streaming methods are silently skipped** — `generator.go:390` and
   `gen/register.go:84` both `continue` on `IsStreamingClient() || IsStreamingServer()`.
   `InvokeStreaming` gets no tool for free.
3. **Tool names are the full proto name with dots replaced**
   (`gen.ToolForMethod` → `grpcview_v1_WorkspaceService_Invoke`), truncated to 64 chars
   with a sha256/base36 head-mangle. All 15 of ours land under 64, so nothing is mangled —
   the names are merely long and repetitive.
4. **Only the RPC's leading comment becomes the tool description.** `CleanComment` strips
   `buf:lint:` / `@ignore-comment` lines, trims each line, and passes the rest through
   verbatim. **Field comments never reach MCP at all**: schemas are built from
   `protoreflect.MessageDescriptor`, and protoreflect has no comment API. The only
   `description` values inside a field schema are the plugin's own canned strings for
   oneofs, maps, and `Struct`/`Value`/`ListValue`.
5. **`runtime.MCPServer` is a one-method interface** (`AddTool(Tool, ToolHandler)`) and
   `runtime.Tool` is a plain struct with exported `Name`, `Description`,
   `RawInputSchema`, `RawOutputSchema`. Everything we want to change is reachable by
   wrapping that interface — the same seam the reference already uses for whitelisting.

Two more facts about the generated schemas, in standard (non-OpenAI) mode:

- **Nothing is `required`.** `IsFieldRequired` only fires for protovalidate `required`
  rules or proto2 `required`; grpcview has neither. Every tool's schema marks every field
  optional, including `workspace_name`. The description is the *only* place required-ness
  can be stated.
- `bytes` → `{"type":"string","contentEncoding":"base64","format":"byte"}`;
  `google.protobuf.Struct` → `{"type":"object","additionalProperties":true}`; oneofs →
  `anyOf` of single-property objects; message recursion is expanded 3 deep then replaced
  by a JSON-string placeholder.

---

## Decision 1 — tools are grpcview's own RPCs, not the reflected target's methods

**v1 exposes the 15 unary `WorkspaceService` RPCs. The dynamic layer is rejected, not
deferred.**

The tempting alternative is real: `gen.RegisterService(s, serviceDescriptor, handler, opts)`
exists in the plugin's `pkg/gen` and registers tools from a *runtime* descriptor, which is
exactly what grpcview has after resolving its sources. One tool per reflected method would
give the agent a per-method input schema and argument validation.

Rejected for three reasons, in order of weight:

1. **It re-introduces the layer this repo deleted.** `AGENTS.md`: "There is no JSON-schema
   layer — an earlier design that converted proto descriptors to JSON schemas has been
   removed entirely." A tool-per-method *is* that layer, reintroduced through a different
   door. Bodies are TypeScript checked against generated types; that is the product.
2. **The tool list becomes unbounded and mutable.** Pointing at a large server yields
   hundreds of tools whose schemas dwarf everything else in the agent's context, and
   `add_source` would have to fire `notifications/tools/list_changed` mid-session — support
   for which is uneven across clients.
3. **It duplicates grpcurl.** grpcview's value is the *saved, scripted, inheriting* request,
   not a raw punch-through.

**The tradeoff we accept:** `invoke` takes `service`, `method`, and a hand-written body,
with no per-method schema. The agent can get the method's *name* from `get_workspace` but
not its *fields*. That gap is real and Decision 6 closes it with one tool, not hundreds.

## Decision 2 — short tool names, via a rename shim

`grpcview_v1_WorkspaceService_AddDescriptorSource` (48 chars) is legal but wasteful:
grpcview has exactly one service, so every tool repeats the same 29-character prefix.

The plugin cannot be configured for this (Fact 1), so rename in the `MCPServer` wrapper
(Fact 5) — keyed off the generated `…Tool.Name` constants so a proto rename is a *compile*
error and an unnamed RPC is a *test* failure:

```go
var toolNames = map[string]string{
    grpcviewv1mcp.WorkspaceService_GetTool.Name:    "get_workspace",
    grpcviewv1mcp.WorkspaceService_InvokeTool.Name: "invoke",
    // …
}
```

**Constraints checked.** The go-sdk validates names against `[A-Za-z0-9_.-]` and a
**128-char** limit (`mcp/tool.go:109`). Claude Code presents MCP tools as
`mcp__grpcview__<name>`, so the real budget is 113 — no name below is close.

**Names avoid bare generic verbs.** `Get` → `get_workspace`, not `get`: when grpcview is
one of several servers in a session, an unprefixed `get` is ambiguous to a human reading a
transcript even though the client namespaces it. `invoke` is kept bare because it is the
product's defining verb and reads unambiguously.

| RPC | Tool name | Notes |
|---|---|---|
| `Get` | `get_workspace` | the entry point; returns tree + sources + services + scripts |
| `AddDescriptorSource` | `add_source` | |
| `RemoveDescriptorSource` | `remove_source` | |
| `RefreshDescriptorSource` | `refresh_source` | |
| `ReorderDescriptorSources` | `reorder_sources` | plural — it takes the whole order |
| `CreateFolder` | `create_folder` | |
| `CreateRequest` | `create_request` | |
| `UpdateRequest` | `update_request` | |
| `DeleteRequest` | `delete_request` | deletes requests *and* folders (`fs.go:479`) |
| `UpdateFolder` | `update_folder` | |
| `Invoke` | `invoke` | |
| `RunScript` | `run_script` | |
| `CreateScript` | `create_script` | |
| `UpdateScript` | `update_script` | |
| `DeleteScript` | `delete_script` | |
| `InvokeStreaming` | — | no tool; see Decision 3 |

15 tools. Phase 3 adds a 16th (`describe_method`).

## Decision 3 — `InvokeStreaming` gets no tool in v1

MCP tool calls are request/response. Progress notifications exist but carry no payload
semantics an agent can consume as data, so there is no faithful streaming mapping. The
plugin's own rule (Fact 2) already produces this outcome, and we accept it rather than
fight it.

**What the agent loses:** the ability to call server-, client-, or bidi-streaming methods
at all. The failure is clean, not confusing: `invokeUnary` rejects them with
`Unimplemented: streaming methods are not supported yet` (`invoke.go:105`), so an agent
that tries gets a legible error rather than a hang.

**The substitute (phase 3, optional):** a hand-written `invoke_stream` tool that drains the
stream to completion and returns `{messages: [...], status, truncated}` under an explicit
frame/byte cap and timeout. "Run to completion, return everything" is the only honest
collapse of a stream into a tool result, and stating the cap in the description is better
than pretending to stream. Not v1: no evidence yet that agents need streaming targets,
and it is the one tool that cannot be generated.

## Decision 4 — stdio only, in-process, same directory

**Ships:** a `grpcview mcp` subcommand of the existing binary speaking stdio.
**Does not ship:** an HTTP endpoint. The reference does streamable HTTP only because it is
a hosted gateway behind an auth boundary.

- **Stdio is what the clients want.** Claude Code, Claude Desktop, Cursor and Zed all
  launch local servers as `{"command": …, "args": ["mcp"]}`. Stdio needs no port, no CORS,
  no localhost-protection flag, no auth story — all four of which the HTTP path would drag
  in for no user.
- **One binary stays one binary.** `grpcview mcp` is a mode, not a second artifact.
- **The MCP session runs in-process against the store**, not against a running instance:
  `workspace.New(ctx)` + `ForwardToConnectWorkspaceServiceClient(shim, ws)` with no HTTP
  hop at all. This works because grpcview's Connect *handler* methods already have exactly
  the `ConnectWorkspaceServiceClient` signature the generated code wants —
  `Method(ctx, *connect.Request[X]) (*connect.Response[Y], error)` — for every unary RPC.
  Zero adapter code.
- **Rejected: dialing a running instance.** It would make `grpcview mcp` fail when no UI
  server is up, add port discovery, and buy nothing: the shared state is the *directory*,
  not the process.

**Concurrency.** Two processes (UI server + MCP server) on one collection directory is
already grpcview's model — every RPC calls `store.Open` and re-reads from disk, and
`Store.mu` is a write lock *within* a process only. So cross-process writes are
last-write-wins per file, and the agent's writes are visible to the next UI fetch. The
residual risk is a lost update when a human saves the same request in the same instant.
Acceptable for a single-user tool; it is the same exposure the
[VS Code track](../vscode/README.md) already accepts for external editors.

## Decision 5 — the proto comments are the shared contract, and they need rewriting

One proto now serves four surfaces (web UI, VS Code, CLI, MCP), and Fact 4 makes the
consequences sharp:

- **The RPC leading comment is the whole MCP description.** It is also the CLI's help text
  and the Go/TS doc comment. Four readers, one string.
- **Field comments reach three of the four audiences and never MCP.** So today's careful
  field documentation — `update_middleware`'s set-flag protocol, `metadata_script`'s
  module shape — is invisible to an agent. Anything an agent must know has to be **lifted
  into the RPC comment**; the field comment stays for developers.
- **Nothing is `required` in the schema**, so required-ness must be stated in prose.

Today's comments fail as tool descriptions in three distinguishable ways: eight are
one-liners that restate the method name ("CreateFolder creates a new folder in the
workspace"); six leak implementation rationale a model should never pay tokens for ("the
merged view is rebuilt from every source's cached resolve", "not the bidi the design sketch
suggested"); and one omits the argument that decides whether the tool works at all —
`Invoke`'s comment says nothing about `body`'s form. Per
[the body contract](../request-body-contract.md) an agent should be told `body` is the
request message **as a JSON object**, with the TS-module form named only as the escape
hatch for generated values. (An earlier draft of this plan proposed documenting the
module form as mandatory; the contract retires that, and this is the better outcome for
MCP specifically — protojson is exactly what a model produces most reliably.)

[Phase 2](./phase-2-comments.md) has the house style and a per-RPC before/after table.

## Decision 6 — v1 does not cover the walkthrough; one tool fixes it

Trace *"point grpcview at localhost:9000, find the method that creates a user, call it,
show me the response"*:

| Step | Tool | Works? |
|---|---|---|
| 1. Point at the server | `add_source {reflection:{address:"localhost:9000"}}` | ✅ returns `services[]` with every service and method |
| 2. Find the method | scan `services[].methods[].name` for `CreateUser` | ✅ (see risk: large targets) |
| 3. Write the body | — | ❌ **nothing returns the input message's fields** |
| 4. Call it | `invoke {service, method, body}` | ✅ once step 3 is solved |
| 5. Show the response | read `response.response` | ⚠️ it is base64 |

**Step 3 is a hard gap.** The agent learns the input message's *name*
(`methods[].input.{package,name}`) but never its fields. `Workspace.descriptor_set` has
them, but it is base64 protobuf a model cannot read — and phase 1 strips it from responses
anyway because it is unbounded binary. So the agent must guess field names, or probe by
invoking and reading errors.

**Step 5 is friction, not a gap.** `Request.Response.response` is `bytes` holding JSON
*text* (`invoke.go:181`, `dm.MarshalJSON()`), so protojson base64-encodes it and the model
must decode. `bytes` is simply the wrong type for a JSON string.

Phase 3 fixes both, and both fixes are **shared-layer**, not MCP-specific:

- **A `DescribeMethod` RPC** returning a method's input/output as `.proto` text.
  `jhump/protoreflect`'s `desc/protoprint` is already a dependency and already offline
  (see [`descriptor-explorer-plan.md`](../descriptor-explorer-plan.md)), so this is new
  wiring, not a new dep. It serves MCP (`describe_method`), the CLI (`grpcview describe`),
  and the UI's message-shape modal from one implementation.
- **`bytes` → `string`** for the four fields that hold JSON text.

---

## Non-goals

- **Auth, of any kind.** No OAuth, no bearer tokens, no header forwarding into the
  upstream call. The transport is a pipe to a child process the user launched.
- **Tool filtering or whitelisting.** Every unary RPC is a tool. The rename map's
  totality is *tested*, so "we forgot to expose one" is a build failure.
- **Multi-tenancy and remote hosting.** No HTTP endpoint, no session isolation, no
  per-request server construction.
- **A second MCP surface.** No prompts, no resources, no sampling, no elicitation — tools
  only. A `resources` view of the workspace tree is a plausible future, not a v1.
- **OpenAI-compat schemas.** The generated `…ToolOpenAI` variants stay unused; standard
  mode keeps `Struct` as a real object instead of a stringified one.

## Risks / open questions

- **The dependency step needs the network on a cold machine.** Everything needed is in
  *this* machine's Go module cache, so it can be done offline here — but that is a
  machine-local fact, not a repo-portable one. [Phase 0](./phase-0-dependencies.md) states
  the recipe and the caveat honestly.
- **`run_script` hands an agent arbitrary JS with `fetch` on.** `AGENTS.md`: "Network is on
  for every script." That is not a new capability in a host that also has Bash, but in a
  host where grpcview is the *only* server it is an escalation, and "no filtering" is a
  hard constraint here. Named, not mitigated.
- **`services` can be huge.** A workspace pointed at a large API returns thousands of
  methods on every workspace-returning tool. Phase 1 strips only `descriptor_set`. Open:
  should the shim also trim `services` on *mutation* responses (keeping it only on
  `get_workspace`), or does `describe_method` plus a future `find_method` make that moot?
- **`workspace_name` is dead weight in every schema.** [VS Code phase 1](../vscode/phase-1-collection-dir.md)
  plans to delete it ("the collection is the directory grpcview runs in"). Until then every
  tool description must tell the agent to pass it. Sequencing question: is it cheaper to do
  that deletion *before* phase 2's comment rewrite, so the rewrite is written once?
- **`grpcview mcp` has no `--dir`.** It inherits `os.UserConfigDir()/.grpcview` like the
  server. After VS Code phase 1 it must accept `--dir` too, or an agent cannot be pointed
  at a project's collection.
- **The generated package name.** `package_suffix=mcp` puts the tools in
  `grpcviewv1mcp` because `package_suffix=` (empty, the pattern
  `//tools:connect_go_proto_compiler` already uses) collides: both the connect plugin and
  the mcp plugin declare `type WorkspaceServiceClient interface` in `grpcviewv1`. Phase 1
  covers the Bazel consequence.
