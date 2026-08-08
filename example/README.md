# The `example` collection

A collection that exercises most of grpcview end to end: two kinds of descriptor
source, TypeScript bodies and metadata, generators with an npm dependency,
folder-metadata inheritance, middleware, `gv.invoke` chaining, and a `gv.assert`
scenario that checks all of it.

Everything here is real: `smoke` passes against the servers below.

## Bring up the echo server first

```bash
bazel run //example:up
```

That starts `//service/echo/cmd` serving `echo.v1.EchoService` on
`127.0.0.1:50055` — moved by `--echo-port` — waits for the port, and holds until
Ctrl-C.

The other half needs nothing started. The `Collections/` requests point at
grpcview itself, and grpcview's own workspace server is what serves that: any
`grpcview` command starts one on demand, on `localhost:10000`, reflecting its own
`grpcview.v1.WorkspaceService`. `example:up` asks for it (`grpcview url`) so it is
already warm, and says so if it landed on a fallen-back port, which the
collection's pinned `reflection:localhost:10000` source would not reach.

The `Echo/` requests each carry `127.0.0.1:50055` as their target. The
`Collections/` requests carry none, so they fall back to the collection's first
reflection source — grpcview reflecting itself.

Add `--isolated` for a throwaway run — CI, or a check you do not want in your own
run history. It points `GRPCVIEW_CONFIG_DIR` at a temp directory, so the run
touches neither your history nor your resolve caches, and builds the collection's
bazel definition source into that empty state first. Export
`GRPCVIEW_CONFIG_DIR` yourself to keep the state and address it from the same
shell:

```bash
export GRPCVIEW_CONFIG_DIR=$(mktemp -d)
bazel run //example:up -- --isolated &
grpcview script run smoke --collection example   # exit 0 is 10 assertions passed
```

That `grpcview` shares the one server keyed under the same `GRPCVIEW_CONFIG_DIR`;
`--isolated` stops it on the way out, since it deletes that state directory when the
run ends. `grpcview shutdown` stops it by hand, and `--in-process` does a command's
work without a server at all — which is what `--isolated` uses for its own setup
calls, before trust has been granted.

## What's in it

```
Echo/                    folder metadata: two headers everything below inherits
  Unary (JSON)           a plain protojson body — valid JSON is valid TypeScript
  Unary (generators)     body and metadata both call the collection's generators
  Unary (params)         gv.request.params, with a default per field
  Unary (middleware)     trace-headers rewrites the message and stamps two headers
  Streaming/             folder metadata that spreads its parent's, then adds one
    ServerStream         gv.request.params.count responses, one per tick
    ClientStream
    BidiStream
Collections/             folder metadata: grpcview talking to itself
  ListCollections        no target; falls back to the reflection source
  DescribeMethod         gv.invoke("Collections/ListCollections") feeds its body

scripts/
  requestId              GENERATOR — a global any body or metadata script can call
  stamp                  GENERATOR — imports dayjs, bundled from the embedded allowlist
  trace-headers          MIDDLEWARE — attached to one request, runs after evaluation
  smoke                  SCENARIO — 10 gv.assert checks over the saved requests
```

## Feature map

| Feature | Where to look |
| --- | --- |
| Reflection source | `grpcview.json` → `reflection:localhost:10000`, descriptors committed to `descriptors/` |
| Bazel source (built from `.proto`) | `grpcview.json` → `bazel://proto/echo/v1:echov1_proto`, descriptors left uncommitted |
| Source priority | `grpcview sources ls --collection example` — each source's `serves`/`wins` counts |
| Doc comments survive the source | `Collections/DescribeMethod` returns `.proto` comments; the bazel source carries `source_code_info`, reflection strips it |
| protojson body | `Echo/Unary (JSON)` — no wrapper, no `{{ }}`, just JSON |
| TypeScript body | every other request — a bare object literal under a hidden `export default` wrapper |
| Generators | `requestId()` and `stamp()`, pulled in only where they are called |
| npm dependency | `stamp` imports `dayjs`, bundled by esbuild from the embedded allowlist — no `node_modules`, no network |
| A deterministic sandbox | the clock starts pinned at `2022-01-01T00:00:00Z` and `Math.random()` is seeded per instance, so two runs of `--dry-run` print the same body |
| Request metadata script | `Echo/Unary (generators)` — evaluated to `{ [key: string]: string[] }` |
| Folder metadata inheritance | `Echo/` sets it, `Echo/Streaming/` spreads `gv.metadata.inherit()`, the requests inherit without asking |
| Middleware | `Echo/Unary (middleware)` — runs last, on the already-serialised message |
| `gv.request.params` | `Echo/Unary (params)` and `Echo/Streaming/ServerStream` |
| `gv.invoke` chaining | `Collections/DescribeMethod` invokes another saved request and drills into its response |
| `gv.assert` scenario | `scripts/smoke` |
| Streaming method kinds | `Echo/Streaming/` — one request per kind |

## Running it

```bash
# every saved request, as the tree sees it
grpcview ls --collection example

# a saved request, by display-name path; exit code is the gRPC status
grpcview invoke "Echo/Unary (JSON)" --collection example

# per-run params
grpcview invoke "Echo/Unary (params)" --collection example --param message="from the CLI"

# resolve and evaluate everything, send nothing
grpcview invoke "Echo/Unary (generators)" --collection example --dry-run

# the scenario: silence is a pass, the first failed assertion throws
grpcview script run smoke --collection example
```

The same requests are addressable from the MCP server (`grpcview mcp` →
`invoke_saved`, `invoke_saved_streaming`, `describe_method`) and from the UI, which
is the point: one saved request, four surfaces. `run_script` there evaluates source
you pass inline — it cannot run a saved script by name, so `smoke` runs from the CLI
or the UI.

## Known gap

The three requests under `Echo/Streaming/` run from the UI, the CLI and the
`invoke_saved_streaming` MCP tool, but `gv.invoke` still rejects a streaming path
outright, so a scenario cannot drive one.
