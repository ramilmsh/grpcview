# The MCP server — decisions, including the ones that were wrong

**Landed 2026-08-06.** What the server *does* is `AGENTS.md` §"The MCP server"; this doc
exists for the decisions behind it, and in particular for the three the original four-phase
plan got wrong, so they are not re-attempted.

The plan was `planned/mcp/{README,phase-0-dependencies,phase-1-server,phase-2-comments,phase-3-gaps}.md`,
written against a 15-RPC `WorkspaceService`. By the time it was built the service had 26
RPCs, the CLI track had already taken over argv, and `DescribeMethod` had already shipped.
Those docs are deleted rather than moved: too much of their text described a proto that no
longer exists to be read safely.

## What held

- **Tools are grpcview's own RPCs, not the reflected target's methods.** One tool per
  reflected method would reintroduce the JSON-schema-per-target layer this repo deleted,
  make the tool list unbounded and mutable mid-session, and duplicate grpcurl. The cost —
  no per-method input schema for `invoke` — is paid by `describe_method` returning the
  shape as `.proto` text from one fixed-size tool.
- **Streaming RPCs get no tool.** MCP tool calls are request/response; the plugin's
  `RegisterService` skips streaming methods anyway. `invoke` rejects them legibly.
  *Amended 2026-08-08 by [`mcp-streaming.md`](./mcp-streaming.md): it held until an agent was
  actually blocked by that `Unimplemented`. `RegisterService` still skips them, but
  `gen.ToolForMethod` does not, so `invoke_streaming` and `invoke_saved_streaming` are
  hand-registered with the plugin's own schema and drain the stream under three caps.*
- **stdio only, in-process, no auth.** The transport is a pipe to a child process the user
  launched. No HTTP endpoint, no port, no CORS, no bearer tokens.
- **The shim is the one seam for payload rules,** and the rename map's totality is a test
  rather than a hope — adding an RPC without naming it fails `bazel test`.
- **Stripping the descriptor set is load-bearing.** Unstripped it is megabytes of base64 on
  every collection-returning call.

## What was wrong

**1. The plugin's protoc codegen cannot work under rules_go.** Phase 1 specified a
`go_proto_compiler` + a second `go_proto_library` with `package_suffix=mcp`. Built, and it
fails: rules_go passes the `go_proto_library` `importpath` to protoc as the proto's
`go_package`, and `protoc-gen-go-mcp` then appends `package_suffix` *again*
(`pkg/generator/generator.go:349-366`), so the generated file imports its own package:

```
compilepkg: missing strict dependencies:
  .../grpcviewv1mcp/service.pb.mcp.go: import of "codeberg.org/…/v1/grpcviewv1mcp"
```

Setting `importpath` to the base package instead fixes the import and breaks the declared
output path, which is why the upstream reference needs `rules_proto_grpc`'s output-directory
dance. The fix was to stop generating: `pkg/gen.RegisterService` registers the same tools
from a live `protoreflect.ServiceDescriptor` at startup. That removes the compiler rule, the
second `go_proto_library` and a 1.7 MB generated file, and it exposes the `CommentProvider`
and `NewMessage` hooks the static path has no equivalent for — `NewMessage` resolving
through `protoregistry.GlobalTypes` is what lets the handler receive concrete `grpcviewv1`
types and dispatch to the workspace handler with no adapter.

**2. Phase 2 — rewriting every RPC comment into ~700 characters of prose — was rejected
outright.** Its premise was sound: the generated JSON Schema gives no field any
`description` and marks nothing `required`, so the tool description looked like the only
channel. Its conclusion did not follow. Length was never the requirement; *coverage of what
the schema cannot say* was. Two moves get that without inflating the proto:

- **The schema carries the field docs.** `runtime.Tool.RawInputSchema` is plain
  `json.RawMessage` on the struct the shim already rewrites, so `annotateSchema` walks it
  and writes each field's real `.proto` comment into that field's `description`.
- **Both descriptions and field docs come from an embedded descriptor set that retains
  `source_code_info`.** Generated Go protobuf strips that section, so
  `protoregistry.GlobalFiles` carries no comments at all. Bazel's `proto_library` output
  keeps it, so `service/mcp` embeds it via a genrule and loads it with `protodesc`.

RPC comments therefore stay terse, in the repo's existing style. What phase 2 was *right*
about is that some RPCs had **no** comment at all — 12 of 24 tools shipped with an empty
description on the first smoke test, and `InvokeRequest.body`, the argument that decides
whether `invoke` works, was undocumented. Those were fixed with one or two lines each, not
paragraphs.

**3. "`services` can be huge" named the wrong field.** The plan's open question was whether
the shim should also trim `services` from mutation responses. Measured against the repo's
own `example` collection, `get_collection` returns 168 KB of which `services` is 8.4 KB
(4%) and **`history` is 158 KB (99% of `item`)**, across 15 saved requests. Any future
payload trimming should start at `history`.

## Leftovers

In `planned/roadmap.md`: `bytes`→`string` for the four JSON-text fields, `invoke_stream`,
`find_method`, and the history-payload question above.
