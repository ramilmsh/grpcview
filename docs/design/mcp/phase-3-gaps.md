# Phase 3 — close the two gaps that stop an agent being useful

**Prereqs:** [phase 1](./phase-1-server.md). Independent of phase 2.
See [`README.md`](./README.md) Decision 6 for the walkthrough that produced this list.

## Goal

Make *"find the method that creates a user, call it, show me the response"* work without
guesswork. Two changes, both in the **shared** proto/handler layer rather than the MCP layer,
because all four surfaces want them.

Ordered by value: **3a is the phase.** 3b is a one-line-per-field cleanup that happens to
fix an agent annoyance. 3c is optional and may never be needed.

---

## 3a — `DescribeMethod`: the missing tool

**The gap.** After `add_source`, an agent knows a method exists and knows its input message's
*name* (`services[].methods[].input.{package,name}`). It never learns the input's *fields*.
`Workspace.descriptor_set` has them but is base64 protobuf a model cannot read — and
[phase 1](./phase-1-server.md) strips it anyway. So the agent must guess field names or probe
by invoking and reading the resulting `InvalidArgument`.

**Why not one tool per method.** That is the dynamic layer rejected in
[Decision 1](./README.md#decision-1--tools-are-grpcviews-own-rpcs-not-the-reflected-targets-methods).
Returning the schema *as data from one tool* gets the same information into the agent for a
fixed cost, and does not reintroduce a JSON-schema layer the repo deliberately deleted.

**Why `.proto` text and not JSON Schema or TS.** `jhump/protoreflect`'s `desc/protoprint`
renders a descriptor to real `.proto` source — field numbers, nested types, enums, and
comments when the source carries `source_code_info`. It is **already a direct dependency and
already offline** (`descriptor-explorer-plan.md` establishes exactly this), so this is
wiring, not a dependency. `.proto` is also the densest possible encoding of a message shape
for a model, and the one it has seen most of in training.

### New RPC

```proto
// Returns the shape of one method's request and response as .proto source, so a
// caller can see the exact field names, types and numbers to send.
//
// Requires workspace_name, service (fully qualified) and method, as listed in the
// workspace's services. Returns request and response as .proto text, plus the
// transitively referenced message and enum definitions.
//
// Comments appear only when the winning definition source carries them: gRPC
// reflection strips them, an uploaded buf-built descriptor set keeps them.
rpc DescribeMethod(DescribeMethodRequest) returns (DescribeMethodResponse);

message DescribeMethodRequest {
  string workspace_name = 1;
  string service = 2;
  string method = 3;
}

message DescribeMethodResponse {
  // request/response are the input and output messages as .proto text.
  string request = 1;
  string response = 2;
  // referenced holds the message and enum definitions those two refer to,
  // transitively, each as .proto text — so the caller never has to make a second
  // call to resolve a nested type.
  repeated string referenced = 3;
}
```

Tool name: **`describe_method`**. Add it to `toolNames` in `service/mcp/mcp.go`; the totality
test then requires it.

**Implementation.** `service/workspace/describe.go`: resolve the workspace's merged
descriptors (the same path `resolveMethod` already walks, minus the dial), find the method,
and render `methodDesc.GetInputType()` / `GetOutputType()` plus their transitive
message/enum closure with `protoprint.Printer`. Message-granular printing, not the
file-granular `PrintProtoToString` the descriptor explorer wants — `protoprint` can print an
individual descriptor, so the two tracks share the dependency and the printer configuration
but not a producer.

**Bound the closure.** A method whose input reaches deep into a large API can pull in
hundreds of messages. Cap the transitive set (depth *and* count), and put the cap in the
response — a truncated `referenced` that says so is fine; an unbounded one repeats
`descriptor_set`'s mistake in a new shape.

**Four surfaces, one RPC:**

| Surface | Use |
|---|---|
| MCP | `describe_method` — closes the walkthrough gap |
| CLI | `grpcview describe <service> <method>` |
| UI | a real backend for the message-shape modal, which today is TS-only and drops field numbers and comments (`gv-features-plan.md` §Feature 2) |
| VS Code | the same modal inside the custom editor |

**Verify.** `bazel test //service/workspace/...` for the printer; then, in a real client with
no other hints, ask the agent to invoke an echo-server method it has not seen. It must call
`describe_method` and then get `body` right first time. That is the whole point of the phase.

---

## 3b — `bytes` → `string` for the four fields that hold JSON text

**The gap.** `Request.Response.response` is `bytes` containing the **JSON text** of the
response message (`invoke.go:181`, `dm.MarshalJSON()`). protojson base64-encodes `bytes`, so
an agent asking "show me the response" gets base64 and has to decode it. The CLI would have
the same problem, and a `curl` against the Connect API already does.

`bytes` is simply the wrong type: the value is UTF-8 JSON, never arbitrary binary.

| Field | Today | Change |
|---|---|---|
| `Request.Response.response` | `bytes` | `string` |
| `History.Request.body` | `bytes` | `string` |
| `History.Response.response` | `bytes` | `string` |
| `InvokeStreamResponse.message` | `bytes` | `string` |

`AddDescriptorSourceRequest.descriptor_set` and `Workspace.descriptor_set` stay `bytes` —
those really are binary.

**Blast radius.** The store schema (`grpcview.store.v1`) and `convert.go` for the history
fields; `invoke.go`'s two assignment sites; and the UI wherever it decodes these
(`TextDecoder` → the string as-is). `AGENTS.md`: "Don't add migrations… change the schema and
delete freely", so on-disk history written by an older build may simply be dropped.

**Verify.** `bazel test //...`; then invoke in the browser and confirm the response pane still
renders; then in the MCP client confirm `invoke`'s result is readable JSON with no decode
step.

---

## 3c — `invoke_stream`, only if wanted

Not planned; specified here so the decision is not re-litigated.

The plugin skips streaming methods, so this tool must be hand-written against
`runtime.MCPServer` directly with a hand-written schema — the one tool in the surface that
cannot be generated. Shape:

```
invoke_stream(workspace_name, service, method, messages[], metadata?, target?)
  -> { messages: [...], status, request_metadata, response_metadata, latency_ms, truncated }
```

It drains `streamInvoke` to completion under an explicit frame cap, byte cap and timeout, all
three named in the description, and sets `truncated` when a cap fires. "Run to completion,
return everything" is the only faithful collapse of a stream into a request/response tool
call; a capped, self-describing collapse is better than a tool that pretends to stream.

**Do it when** a real session needs a server-streaming target and the current
`Unimplemented: streaming methods are not supported yet` (`invoke.go:105`) is the thing
blocking it. Not before.

---

## Also considered, not planned

- **`find_method(query)`** — a substring/fuzzy search over the workspace's services,
  returning matches instead of the whole list. Real value once someone points grpcview at a
  large API, and it is another four-surface RPC (UI quick-open, CLI `--grep`, VS Code
  command palette). Deferred because `services` in a `get_workspace` response is adequate at
  the scale grpcview is used at today, and because `describe_method` is the harder blocker.
  If we get here, it belongs to the shared layer, not the MCP layer.
- **Trimming `services` out of mutation responses** in the shim, keeping it only on
  `get_workspace`. Cheap, and the obvious follow-up if large targets turn out to hurt. Held
  back so v1 has exactly one payload rule (`descriptor_set` is stripped) rather than a
  per-tool table of them.
- **MCP `resources` for the request tree.** Plausibly nicer than `get_workspace` for
  browsing, but resource support is thinner across clients than tool support, and it would be
  a second surface to keep in sync. Non-goal.

## Out of scope

Anything in [phase 2](./phase-2-comments.md); an HTTP transport; per-method tools in any
form.
