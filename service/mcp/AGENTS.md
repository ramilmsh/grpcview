# The MCP server

`grpcview mcp` speaks MCP over stdio on the same binary — no HTTP endpoint, no auth.
`--timeout` doesn't apply — bypasses the per-verb timeout for a long-lived session.

- Tools are grpcview's own unary RPCs, registered at runtime from `WorkspaceService`'s
  descriptor via `protoc-gen-go-mcp` — not one tool per reflected target method.
- Streaming RPCs get two hand-registered tools (`streaming.go`): the handler **drains the
  stream**, returns `{messages, result, truncated}` under a frame/byte/deadline cap
  (`defaultCaps`: 200 frames / 256KB / 60s); the invoked call's gRPC status lives in
  `result`, never the tool's error channel.
- `service/mcp` is the one seam for payload rules: renames tool names, drops the output
  schema, strips every `descriptor_set`, defaults `collection`. An oversized response-body
  string is elided (`maxResponseStringBytes` = 8KB — a base64 descriptor set inside JSON
  inside `bytes` is exactly that shape). `history`/`services` are owned only by the RPCs
  that can change them (`get_collection` is `history`'s only read access).
- **`run_script` hands the calling agent arbitrary JS with `fetch` enabled** — a known,
  unmitigated exposure. Runs in the daemon, not the MCP child; writes serialize on one
  `Collection.mu` like every other verb.
