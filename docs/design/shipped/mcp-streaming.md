# MCP: streaming methods

**Status:** **Shipped** (on `trunk` 2026-08-08). All five phases landed as planned, verified
live against the example collection through a real stdio MCP server. Behavior is documented
in `AGENTS.md` §"The MCP server". Two claims below were wrong — see "What was wrong".

Promotes the `invoke_stream` want from [`../planned/roadmap.md`](../planned/roadmap.md)
§"The MCP server" into a plan. Today MCP is the only surface that cannot run a client-streaming,
server-streaming or bidi method: the UI can, the CLI can, `gv.invoke` can, and an agent gets
`Unimplemented`.

## The one wrong premise to clear first

**The plugin does not generate streaming tools.** `RegisterService`
(`pkg/gen/register.go:83-84`, `protoc-gen-go-mcp@v0.0.0-20260430225748`) skips every method
where `IsStreamingClient() || IsStreamingServer()` — the skip is unconditional and has no
option. What the plugin *does* give us is `gen.ToolForMethod(md, comment)`, which builds a
`runtime.Tool` from the method's input and output descriptors and never asks whether the
method streams. So the schema is still generated; only the registration and the handler are
hand-written. That is the whole shape of this work.

The backend needs nothing new. `workspace` already exports both streaming RPCs in the
send-func form an in-process caller wants — `InvokeStream` and `InvokeSavedStream`
(`service/workspace/invoke_saved.go:100,104`) — because the CLI needed exactly this: connect
cannot build a `*connect.ServerStream` outside a served request. `service/mcp` calls the same
two methods the CLI's `inProcess` binding does.

## Decisions

- **Two new tools, not auto-routing inside `invoke`.** `invoke_streaming` and
  `invoke_saved_streaming`. `InvokeRequest` carries one `body string` while
  `InvokeStreamRequest` carries `repeated string messages`; one tool covering both would need
  a hand-merged schema and a method-kind lookup before dispatch, and it would let an agent
  send a body shape the method cannot accept. Separate tools keep every schema derived from
  the proto. The cost is that an agent must know the method's kind — which it already can:
  `describe_method` returns `not_invocable_reason`, `create_request` returns the same string
  in `warnings`, and `grpcview ls` tags the row.
- **Run to completion, return everything, under three named caps.** A tool call is
  request/response; the only faithful collapse of a stream is to drain it. Unbounded, a
  server stream is a context bomb with no Ctrl-C — an agent cannot interrupt a tool call. So:
  a **frame cap**, a **byte cap** and a **deadline**, all three stated in the tool
  description and all three reported in the result when they bite. Proposed starting values:
  200 frames, 256 KB of message bytes, 60 s. Hitting a cap cancels the call's context so the
  RPC actually stops, and still returns everything collected so far.
- **Message frames go into the result as raw JSON, not base64.** `InvokeStreamingResponse.message`
  is `bytes` holding UTF-8 JSON, so `protojson` would base64 it — the same wart the unary
  `invoke` tool has today (verified live: it returns `"eyJtZXNzYWdlIjoi…"`). The streaming
  result is assembled by hand anyway, so it does not have to inherit that; the roadmap's
  `bytes` → `string` item is what brings `invoke` into line later, and it is out of scope
  here. Note the deliberate inconsistency in the tool description until then.
- **Registration routes through the existing shim.** The hand-built tool is handed to
  `shim.AddTool` under the plugin's generated name (`toolNamePrefix + "InvokeStreaming"`), so
  the rename map, `annotateSchema` and `defaultCollection` all apply unchanged and there is
  exactly one seam for payload rules, as `AGENTS.md` claims. `trimHeavyFields` does not apply:
  the handler returns a `CallToolResult` directly, and neither frame carries a
  `descriptor_set` or `history`.
- **No `record_history` opt-out for the ad-hoc form**, matching `InvokeStreaming`'s existing
  behaviour (it always records, and records only `messages[0]` as the body). The saved form
  inherits `SavedInvokeSpec.record_history`. Changing the ad-hoc asymmetry is a backend
  change and not part of this.

## Phase 1 — register the two tools

`service/mcp/dispatch.go`: keep one `rpcs` map so tool names stay in one place (both tests
read it), and give the entry a second, optional bind for the streaming shape. `newHandler`
keeps registering only the unary binds; a new loop registers the streaming ones.

`service/mcp/streaming.go` (new):

- A `streamer` interface with just `InvokeStream` and `InvokeSavedStream`, satisfied by
  `workspace.Workspace`. This is the test seam — the collector is testable without a
  workspace, a store or a live target.
- For each streaming RPC: `tool, _ := gen.ToolForMethod(md, comment(md))`, then
  `s.AddTool(tool, handler)`.
- The handler mirrors `register.go`'s body: `json.Marshal(request.Arguments)` →
  `protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal` into the concrete request
  message (`newMessage` already resolves those from the global registry) → call the streamer
  with a collecting send func → `mcpruntime.NewToolResultJSON(aggregate)`. Errors go through
  `mcpruntime.HandleError` like every other tool.

`service/mcp/BUILD.bazel`: add the new file to `srcs` (and the new test to `srcs` of the test
target). A new `.go` file that is not in `srcs` compiles and passes locally under `go test`
while being invisible to Bazel — see `AGENTS.md` §"Delegating to background agents".

## Phase 2 — the collector

One function, no I/O of its own:

```
collect(ctx, cap, func(send) error) (aggregate, error)
```

Result shape:

```json
{
  "messages": [ { … }, { … } ],
  "result":   { "status": {…}, "request_metadata": {…}, "response_metadata": {…}, "latency": "…", "timestamp": "…" },
  "truncated": { "reason": "frames|bytes|deadline", "frames": 200, "bytes": 262144 }
}
```

- `messages` holds each `message` frame's bytes spliced in as `json.RawMessage`. A frame that
  is not valid JSON is passed through as a string rather than dropped — it should never
  happen, and swallowing it would hide a backend bug.
- `result` is the protojson of the single terminal `Request.Response` frame. The invoked
  call's gRPC status lives *there*, not in the tool's error channel — same rule as the CLI's
  exit codes: a non-OK status is a successful tool call carrying a failed RPC.
- `truncated` is absent when nothing was dropped. When a cap bites, `result` may be absent
  because the terminal frame never arrived; say so rather than synthesizing one.
- The deadline is a `context.WithTimeout` around the call; the frame and byte caps cancel that
  same context from inside the send func, then return `nil` so the drain unwinds normally.

## Phase 3 — tests

- **`TestTotality` inverts for streaming.** It currently asserts a streaming method must
  *not* have a tool-name or dispatch entry; it becomes: every method has a tool name, unary
  methods have a unary bind, streaming methods have a streaming bind, and no method has both.
- **`TestCollectionPathReachesEveryCollectionField` stops skipping streaming methods.** Both
  new request messages carry `spec.collection` at depth 2, so `defaultCollection` reaches
  them — pin it.
- **Collector tests against a fake `streamer`**: frames then a result; a result with no
  frames; frames and no result; each of the three caps; a non-JSON frame; an error returned
  by the streamer.
- **Schema test**: `invoke_streaming`'s input schema has `spec` and `messages`, and the
  proto's field comments are present (proving the tool went through `shim.AddTool` rather
  than around it).
- Run per-test with `--nocache_test_results` when checking these landed.

## Phase 4 — every surface that currently says "you cannot run this"

The claim is now false in three places and misleading in a fourth:

- `service/workspace/describe.go:57` — `streamingNotInvocable` reads "streaming: invoke and
  invoke_saved reject this method with Unimplemented; it can be authored and described, but
  not run". It reaches `describe_method`'s `not_invocable_reason`, `create_request`'s
  `warnings` and `grpcview ls`'s row tag. It was already half-wrong (the CLI runs these
  today); rewrite it to name what *does* run the method on each surface. Tests assert the
  string in `service/cli/ls_test.go` and `service/workspace/describe_test.go`.
- `service/workspace/invoke.go:80` — the `Unimplemented` from `invokeUnary` should name the
  streaming tool/verb, since that error is what an agent hits first.
- `AGENTS.md` §"The MCP server" — the "Streaming RPCs get no tool" bullet, and the
  "MCP exposes no tool for them" clause in §"Verify through MCP or the CLI, not the browser".
- `docs/design/shipped/mcp.md` — "Streaming RPCs get no tool" is under "What held". It held
  until now; add the amendment rather than editing the history.
- `docs/design/planned/roadmap.md` — drop the `invoke_stream` bullet, since it is this doc.

## Phase 5 — verify live, then commit

`bazel run //example:up` brings up both targets, and the example collection already has a
saved request per streaming kind (`example/tree/echo/streaming/{serverstream,clientstream,bidistream}`).
Through the real MCP server:

- `invoke_saved_streaming` on each of the three saved requests.
- `invoke_streaming` ad-hoc on `echo.v1.EchoService/ServerStream` and `/BidiStream`, the
  latter with several `messages`, at least one of them a TypeScript body rather than JSON —
  `resolvePreSend` evaluates every message, and that path has no MCP coverage today.
- One deliberate cap hit, to see `truncated` and confirm the RPC actually stops.

Commit per phase on trunk, as with the other tracks.

## What was wrong

- **`gv.invoke` cannot run a streaming method either.** The opening paragraph lists it beside
  the UI and the CLI as a surface that can. `scriptInvoker` rejects a streaming target
  outright (`gvinvoke_test.go:329` pins it); what it *can* do is run from inside a streaming
  request's body. So MCP was the third surface to gain this, not the fourth, and the example
  collection's bidi body — which said so all along — was right.
- **`truncated` reports what was kept, not the caps.** The sketched shape shows
  `{"frames": 200, "bytes": 262144}`, i.e. the caps echoed back. Those are already in the tool
  description; what an agent cannot otherwise know is how much survived, so the fields carry
  the kept counts. When the frame cap bites the two coincide, which is why the sketch read
  either way.
- **The terminal frame is kept after a cap bites**, where phase 2 expected `result` to be
  absent. Cancelling the context makes `streamInvoke` finish its switch and send the terminal
  frame carrying the `Canceled` status, so it arrives after all — verified live: a
  1000-message server stream capped at 200 frames returned `truncated` *and* a `result` with
  `code: 1, "context canceled"` at 24 s instead of the full 120 s. Reporting it beats
  dropping a frame the backend really sent. `result` is still absent when the drain ends
  without one.

## Not in scope

- `bytes` → `string` for the four JSON-bearing fields (roadmap; would remove the base64 wart
  from unary `invoke` and make this doc's third decision unnecessary).
- Incremental delivery of frames to the agent. MCP has progress notifications, but a tool
  *result* is single-shot and the go-sdk's progress channel is not a data channel.
- A `record_history` opt-out for ad-hoc streaming invokes.
- Capping history growth store-side (roadmap).
