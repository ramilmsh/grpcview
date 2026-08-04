# Phase 1 — generate the tools, rename them, serve them over stdio

**Prereqs:** [phase 0](./phase-0-dependencies.md). **Unblocks:** phases 2 and 3.
See [`README.md`](./README.md) for the decisions this phase implements.

## Goal

`grpcview mcp` speaks MCP over stdio and exposes 15 tools that call the workspace handler
in-process. No new proto, no HTTP, no auth.

## Step 1 — the Bazel wiring

grpcview does **not** need `rules_proto_grpc`, which the reference pulls in for its
`proto_plugin` + output-directory + extract dance. The seam already exists:
`//tools:connect_go_proto_compiler` is a plain `rules_go` `go_proto_compiler` wrapping an
arbitrary protoc plugin, and the mcp plugin fits the same mould.

**`tools/BUILD.bazel`** — a second compiler beside the connect one:

```python
# package_suffix=mcp (the plugin's default) puts the tools in a SEPARATE Go package,
# grpcviewv1mcp. The empty suffix that connect_go_proto_compiler uses is not available
# here: protoc-gen-go-mcp emits `type WorkspaceServiceClient interface`, which
# service.connect.go already declares in grpcviewv1 (service.connect.go:84).
go_proto_compiler(
    name = "mcp_go_proto_compiler",
    options = ["package_suffix=mcp"],
    plugin = "@com_github_redpanda_data_protoc_gen_go_mcp//cmd/protoc-gen-go-mcp",
    suffix = ".pb.mcp.go",
    visibility = ["//visibility:public"],
    deps = [
        "@com_connectrpc_connect//:connect",
        "@com_github_redpanda_data_protoc_gen_go_mcp//pkg/runtime",
        "@org_golang_google_grpc//:grpc",
        "@org_golang_google_protobuf//encoding/protojson",
    ],
)
```

**`proto/grpcview/v1/BUILD.bazel`** — a second `go_proto_library` over the *same*
`proto_library`:

```python
go_proto_library(
    name = "grpcviewv1mcp_go_proto",
    compilers = ["//tools:mcp_go_proto_compiler"],
    importpath = "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1/grpcviewv1mcp",
    proto = ":grpcviewv1_proto",
    visibility = ["//visibility:public"],
    deps = [":grpcview"],
)
```

Why this lines up, and the two things to watch:

- **Output paths match exactly.** `go_proto_compile` declares outputs at
  `<importpath>/<proto base><suffix>` (`rules_go/proto/compiler.bzl:140`), and the plugin
  with `package_suffix=mcp` writes to
  `codeberg.org/…/proto/grpcview/v1/grpcviewv1mcp/service.pb.mcp.go` — the same path,
  because `importpath` above names the nested package. No extract rule needed. (The
  existing connect output at
  `bazel-bin/proto/grpcview/v1/grpcviewv1_go_proto_/codeberg.org/…/service.connect.go`
  confirms rules_go uses import-path layout.)
- **Watch out: `workspace.proto` has no services**, so the plugin generates nothing for it
  and rules_go's builder writes a `// +build ignore` / `package ignore` stub for the missing
  declared output (`go/tools/builders/protoc.go:188`). Benign and invisible in the compiled
  package — but do not mistake it for a bug. *Fallback if it ever bites:* split
  `service.proto` into its own `proto_library`.
- **Watch out: `deps = [":grpcview"]` goes through `_go_proto_aspect`**, which requires the
  dep to carry `importpath` and `_go_context_data` (`proto/def.bzl:76`). A `go_library` has
  both, so this should work. *Fallback:* move the dep onto the compiler's own `deps`
  (that compiler has exactly one consumer, so there is no blast radius). It must be
  `:grpcview` and **not** `:grpcviewv1_go_proto` — `:grpcview` embeds the latter, so
  depending on both would link the same importpath twice.

**Regenerate/inspect:** `bazel build //proto/grpcview/v1:grpcviewv1mcp_go_proto`, then read
`bazel-bin/proto/grpcview/v1/grpcviewv1mcp_go_proto_/…/grpcviewv1mcp/service.pb.mcp.go`.
Nothing is committed — same as the other generated Go.

## Step 2 — `service/mcp/`, the whole surface

One new package, mirroring the reference's `gateways/admin/mcp/`. Files: `mcp.go`,
`mcp_test.go`, `BUILD.bazel`.

```go
package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "os"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    mcpruntime "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
    "github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime/gosdk"

    grpcviewv1mcp "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1/grpcviewv1mcp"
    "codeberg.org/ramilmsh/grpcview/service/workspace"
)

func Run(ctx context.Context) error {
    // stdout is the JSON-RPC channel. Anything logged there corrupts the session,
    // so every logger — including the one workspace.New hands the store — goes to stderr.
    slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

    ws, err := workspace.New(ctx)
    if err != nil {
        return fmt.Errorf("failed to initialize workspace handler: %w", err)
    }
    defer ws.Close(ctx)

    server, raw := gosdk.NewServer("grpcview", version)
    // ws already satisfies ConnectWorkspaceServiceClient: every unary handler method is
    // Method(ctx, *connect.Request[X]) (*connect.Response[Y], error). No adapter, no HTTP.
    grpcviewv1mcp.ForwardToConnectWorkspaceServiceClient(&shim{MCPServer: raw}, ws)

    return server.Run(ctx, &mcpsdk.StdioTransport{})
}
```

### The shim — one wrapper, three jobs

The reference already wraps `runtime.MCPServer` (for whitelisting); the same seam carries
all three of our changes, and there is no other place any of them can happen.

```go
type shim struct{ mcpruntime.MCPServer }

func (s *shim) AddTool(t mcpruntime.Tool, h mcpruntime.ToolHandler) {
    name, ok := toolNames[t.Name]
    if !ok {
        panic(fmt.Sprintf("mcp: generated tool %q has no short name", t.Name))
    }
    t.Name = name
    t.RawOutputSchema = nil
    s.MCPServer.AddTool(t, trimResponse(h))
}

var toolNames = map[string]string{
    grpcviewv1mcp.WorkspaceService_GetTool.Name:                      "get_workspace",
    grpcviewv1mcp.WorkspaceService_AddDescriptorSourceTool.Name:      "add_source",
    grpcviewv1mcp.WorkspaceService_RemoveDescriptorSourceTool.Name:   "remove_source",
    grpcviewv1mcp.WorkspaceService_RefreshDescriptorSourceTool.Name:  "refresh_source",
    grpcviewv1mcp.WorkspaceService_ReorderDescriptorSourcesTool.Name: "reorder_sources",
    grpcviewv1mcp.WorkspaceService_CreateFolderTool.Name:             "create_folder",
    grpcviewv1mcp.WorkspaceService_UpdateFolderTool.Name:             "update_folder",
    grpcviewv1mcp.WorkspaceService_CreateRequestTool.Name:            "create_request",
    grpcviewv1mcp.WorkspaceService_UpdateRequestTool.Name:            "update_request",
    grpcviewv1mcp.WorkspaceService_DeleteRequestTool.Name:            "delete_request",
    grpcviewv1mcp.WorkspaceService_InvokeTool.Name:                   "invoke",
    grpcviewv1mcp.WorkspaceService_RunScriptTool.Name:                "run_script",
    grpcviewv1mcp.WorkspaceService_CreateScriptTool.Name:             "create_script",
    grpcviewv1mcp.WorkspaceService_UpdateScriptTool.Name:             "update_script",
    grpcviewv1mcp.WorkspaceService_DeleteScriptTool.Name:             "delete_script",
}
```

**Job 1 — rename.** Keying off the generated `…Tool.Name` constants means a proto rename is
a *compile* error, and an RPC added without a name is caught by the test below (and, failing
that, panics at startup). This is what replaces the reference's whitelist and is why "no
filtering" is safe: coverage is enforced, not hoped for.

**Job 2 — drop the output schema.** Every workspace-returning tool (13 of 15 — everything
except `invoke` and `run_script`) carries an
identical, multi-kilobyte `Workspace` JSON schema — recursive `Item`/`Folder` expanded three
deep, plus `Service`/`Method`/`Message`, `DescriptorSource`/`Resolved`, `Script`. Dropping it
shrinks `tools/list` by an order of magnitude. It is also the *honest* move: job 3 mutates
the payload, so a declared schema describing the unmutated shape would be a lie the go-sdk
validates against.

**Job 3 — strip `workspace.descriptor_set`. This is load-bearing; without it the server is
unusable.** `Workspace.descriptor_set` is the merged `FileDescriptorSet` — hundreds of KB to
megabytes — and the generated handler marshals with
`protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}`
(`generator.go:249`), so **every one of those 13 tools returns the whole descriptor set as
base64**. A single `create_request` would dump it into the agent's context.

```go
func trimResponse(h mcpruntime.ToolHandler) mcpruntime.ToolHandler {
    return func(ctx context.Context, req *mcpruntime.CallToolRequest) (*mcpruntime.CallToolResult, error) {
        res, err := h(ctx, req)
        if err != nil || res == nil || res.IsError {
            return res, err
        }
        var doc map[string]any
        if json.Unmarshal([]byte(res.Text), &doc) != nil {
            return res, nil // not our JSON; pass through untouched
        }
        if ws, ok := doc["workspace"].(map[string]any); ok {
            delete(ws, "descriptor_set") // UseProtoNames:true — snake_case, not descriptorSet
        }
        out, err := json.Marshal(doc)
        if err != nil {
            return res, nil
        }
        return mcpruntime.NewToolResultJSON(out), nil
    }
}
```

The key is `descriptor_set`, not `descriptorSet` — guessing wrong makes this a silent no-op.
*Rejected alternative:* a typed wrapper implementing `ConnectWorkspaceServiceClient` that
clears the field on the message. Correct but 15 forwarding methods; this is one function.

### The test that keeps the surface total

`service/mcp/mcp_test.go` walks the service descriptor and asserts the map is total, so
"added an RPC, forgot to name it" fails at `bazel test` rather than at an agent's first
call:

```go
func TestToolNamesCoverEveryUnaryRPC(t *testing.T) {
    methods := grpcviewv1.File_proto_grpcview_v1_service_proto.
        Services().ByName("WorkspaceService").Methods()
    // every non-streaming method has a name; every name is unique, lower_snake, and
    // matches the go-sdk's [A-Za-z0-9_.-]{1,128} rule; streaming methods have none.
}
```

## Step 3 — the subcommand

**Superseded, and in the track's favor: argv dispatch already exists.** This step used to
hand-roll `os.Args[1] == "mcp"` ahead of `service.Run`'s own `flag.Parse()`. The CLI track
(`cli-generator-exploration.md` C0) has since removed `flag.Parse()` from `service.Run`
entirely and put a cobra tree in `service/cli`, so:

- **`service/cli/root.go`** — one `root.AddCommand(newMcpCmd(…))`, alongside `invoke`,
  `describe`, `ls`, `get`, `sources`, `request`, `folder` and `script`. No argv surgery, and
  `grpcview completion` learns the verb for free.
- **`service/cmd/main.go`** — nothing to change: it is already
  `os.Exit(cli.Main(ctx, os.Args[1:], streams, serveFn))`, and the `panic(err)` this step
  wanted replaced is gone.
- **`service/cmd/dev/main.go`** — nothing to change either; `dev` stays serve-only with its
  own `-port`. The fast loop is `bazel run //service/cmd -- mcp` once the verb exists.
- **Exit codes and streams** come from `cli.Main`'s existing contract (`statusError` → the
  process code; `Streams` for stdio), which is what an MCP client's log pane wants instead of
  a goroutine dump.
- `service/cli` **must not** import `//service`, so if `mcp.Run` ever needs the HTTP server
  it takes the same injected-closure route `serve` does.
- **No new flags** *today*: the collection is `os.UserConfigDir()/.grpcview`, the same one the
  server uses (`workspace.go:44`) — Decision 4's shared-directory model. This is the part
  [VS Code phase 1](../vscode/phase-1-workspace.md) invalidates, and by more than one flag.
  `grpcview mcp` must take `--workspace <path>`, resolve `collection` the way the other verbs
  do, and become a **client of the workspace daemon** through the same connect-or-spawn
  registry ([`../daemon.md`](../daemon.md)) — so an agent's writes and the UI's writes land in
  one process rather than two racing on a directory. MCP itself needs no port and no discovery
  *of* itself: it is launched by its client over stdio, which is the easy half.

## Files touched

| File | Change |
|---|---|
| `tools/BUILD.bazel` | `+ go_proto_compiler(name = "mcp_go_proto_compiler")` |
| `proto/grpcview/v1/BUILD.bazel` | `+ go_proto_library(name = "grpcviewv1mcp_go_proto")` |
| `service/mcp/mcp.go` | new — `Run`, `shim`, `toolNames`, `trimResponse` (~120 lines) |
| `service/mcp/mcp_test.go` | new — totality + name-shape test |
| `service/mcp/BUILD.bazel` | new — `go_library` + `go_test` |
| `service/cli/root.go` | one `root.AddCommand(newMcpCmd(…))`; the CLI track already owns argv |
| `AGENTS.md` | a short "MCP server" section: `grpcview mcp`, the shim's three jobs, the rename map as the source of truth for tool names |

## Verify

1. `bazel test //service/mcp/...` — the totality test.
2. `bazel build //...` and `bazel test //...` clean, still offline.
3. **Offline stdio smoke test** (no MCP client needed, scriptable):
   ```bash
   printf '%s\n' \
     '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
     '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
     '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
   | bazel-bin/service/cmd/cmd_/cmd mcp
   ```
   Assert: 15 tools, every name short, no `outputSchema`, and **no log line on stdout**.
4. **Real client, end to end.** Isolate `HOME` so the agent gets a scratch collection
   (the repo's standing verification pattern), start the echo server
   (`bazel run //service/echo/cmd`), then:
   ```
   claude mcp add grpcview -- /abs/path/to/bazel-bin/service/cmd/cmd_/cmd mcp
   ```
   Drive it: `get_workspace` → `add_source` at the echo server → `create_request` →
   `invoke` with `body = "export default () => ({ message: \"hi\" })"`. Confirm the echo
   response comes back and that a `create_request` response is **kilobytes, not megabytes**
   (job 3 working).
5. **Cross-surface check:** with the MCP server having created a request, start the UI
   server and confirm the request appears in the tree.

## Out of scope

The comment rewrite (phase 2 — the descriptions in this phase will be the current, poor
ones, which is fine because step 4 is how we *see* that they are poor); `DescribeMethod` and
`invoke_stream` (phase 3); any HTTP endpoint; trimming `services` from responses; a version
string wired to `tools/workspace_status.sh` (a const is fine).
