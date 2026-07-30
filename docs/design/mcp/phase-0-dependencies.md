# Phase 0 — three new Go modules in an offline workspace

**Prereqs:** none. **Unblocks:** everything. See [`README.md`](./README.md) for the track
overview.

This is the single biggest feasibility risk in the track, so it gets its own phase rather
than a bullet inside phase 1. **Read the verdict, then the caveat.**

## What must be added

The generated code imports only `pkg/runtime`; the plugin binary needs the rest. Verified
against the module's own `BUILD.bazel` files, the build closure is:

| Consumer | Needs |
|---|---|
| generated `.pb.mcp.go` | `protoc-gen-go-mcp/pkg/runtime`, plus `connectrpc.com/connect`, `google.golang.org/grpc`, `protobuf/encoding/protojson` — **all three already in `go.mod`** |
| `pkg/runtime` | `github.com/redpanda-data/common-go/api/errors`, `genproto/googleapis/rpc/status`, `grpc/codes`, `grpc/status`, `protojson`, `protoreflect` |
| `pkg/runtime/gosdk` | `github.com/modelcontextprotocol/go-sdk/mcp` |
| `cmd/protoc-gen-go-mcp` (exec-only) | `pkg/generator` → `pkg/gen` → `buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go`, `genproto/googleapis/api/annotations` |

So **three new direct modules**:

```
github.com/redpanda-data/protoc-gen-go-mcp v0.0.0-20260430225748-67e0bd25a988
github.com/modelcontextprotocol/go-sdk     v1.6.1
github.com/redpanda-data/common-go/api     v0.0.0-20260707080359-4ebcf3adda3a  // indirect
```

plus the indirect closure they drag in: `google/jsonschema-go`, `yosida95/uritemplate/v3`,
`golang-jwt/jwt/v5`, `segmentio/encoding`, `segmentio/asm`, `golang.org/x/oauth2`,
`google.golang.org/genproto/googleapis/api`, and the protovalidate gen-go module.

**Version choice:** take go-sdk **v1.6.1**, matching `/Users/r/dev/core/go.mod`. The
plugin's own `go.mod` pins v1.4.1, but core builds the `gosdk` adapter against v1.6.1
today, and the three APIs we touch are unchanged across both (`Server.AddTool`,
`Server.Run`, `StdioTransport` — all present in v1.6.1). Do not pick a version core has not
already proven.

**Go directive:** `protoc-gen-go-mcp`'s `go.mod` says `go 1.26.1` while grpcview's says
`go 1.25.0`. The directive must be bumped (`go_sdk.download` is already 1.26.5, so the
toolchain is there).

## Feasibility verdict

**Doable offline on this machine; needs the network once on any other.**

Every module above — and every one of their zips — is already in
`~/go/pkg/mod/cache/download`, at the exact versions core resolved, because the reference
implementation was built here. Verified by listing `@v/*.zip` for each path.

The caveat is real and must not be papered over: **that is a property of this laptop, not of
the repo.** A clean checkout, a fresh CI runner, or a colleague gets nothing from it. There
is no way to make a *new* module dependency offline-portable in this workspace short of
vendoring, so the honest framing is: this phase is a **one-time, network-adjacent
prerequisite**, and everything after it is offline-green like the rest of the repo.

## Steps

1. **Edit `go.mod` by hand.** `AGENTS.md` forbids `go mod tidy`, and it is not needed:
   add the three requires plus the indirect block, **copying the exact versions from
   `/Users/r/dev/core/go.mod`**. Copying matters beyond convenience — MVS would otherwise
   select `segmentio/asm v1.1.3` (what go-sdk requires), which is *not* in the cache, where
   core's resolved `v1.2.1` is. Matching core's resolution keeps every selected version
   inside the cache.
2. **Append the `go.sum` lines**, likewise copied verbatim from `/Users/r/dev/core/go.sum`
   (grep each module path; take both the `h1:` and `/go.mod h1:` lines). This is what
   replaces `go mod tidy`.
3. **Add to `use_repo(go_deps, …)` in `MODULE.bazel`**, alphabetically:
   `com_github_modelcontextprotocol_go_sdk`,
   `com_github_redpanda_data_common_go_api`,
   `com_github_redpanda_data_protoc_gen_go_mcp`, and
   `org_golang_google_genproto_googleapis_api` +
   `build_buf_gen_go_bufbuild_protovalidate_protocolbuffers_go` (the plugin's exec-only
   deps). `go_deps.from_file(go_mod = "//:go.mod")` picks the versions up from step 1.
4. **Mirror core's gazelle override** for the plugin module:
   ```python
   go_deps.gazelle_override(
       build_file_generation = "clean",
       path = "github.com/redpanda-data/protoc-gen-go-mcp",
   )
   ```
   The module ships its own `BUILD.bazel` files (we read them above); `"clean"` is what core
   uses to have gazelle regenerate them rather than trust them.
5. **Populate the Bazel repository cache once**, on a run with the network available:
   `bazel fetch //...` (or the first `bazel build`). If the module cache is being used as the
   proxy, pass it explicitly rather than relying on ambient state:
   `--repo_env=GOPROXY=file:///Users/r/go/pkg/mod/cache/download --repo_env=GOFLAGS=-mod=mod --repo_env=GONOSUMDB=* --repo_env=GONOSUMCHECK=1`.
   Once the repo cache holds them, ordinary `GOPROXY=off` builds are green again.

## Verify

- `bazel build @com_github_redpanda_data_protoc_gen_go_mcp//cmd/protoc-gen-go-mcp` — the
  plugin binary builds under the exec configuration.
- `bazel build @com_github_redpanda_data_protoc_gen_go_mcp//pkg/runtime/gosdk` — the
  adapter compiles against the chosen go-sdk version. This is the compile that fails if the
  version pick is wrong.
- `bazel build //...` and `bazel test //...` still pass with **no** `.bazelrc` change and no
  ambient `GOPROXY` override, proving nothing regressed to needing the network.

## Fallback if the deps cannot be added

Ranked, best first:

1. **Vendor `pkg/runtime` + `pkg/runtime/gosdk`** (two files, ~200 lines, Apache-2.0) into
   `third_party/` and hand-write the 15 tool registrations. Loses codegen — a new RPC then
   needs a hand-edit — but removes two of the three modules and the plugin entirely. This is
   the fallback to take if only the *plugin* is the problem: the runtime is small enough to
   copy, the generator is not.
2. **Hand-roll against the go-sdk alone.** Keeps one new module
   (`modelcontextprotocol/go-sdk`, which has no protobuf deps at all) and writes tool
   schemas by hand. Cheapest dependency footprint, most ongoing maintenance.
3. **Don't ship it.** The track is additive; nothing else depends on it.

Do **not** fall back to writing our own protoc plugin.

## Out of scope

Bumping any existing dependency; touching the `.bazelrc`; adding `rules_proto_grpc` (the
reference needs it for its `proto_plugin`/output-directory dance —
[phase 1](./phase-1-server.md) shows why grpcview does not).
