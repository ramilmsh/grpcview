# The `example` collection

A collection that exercises most of grpcview end to end: two kinds of descriptor
source, TypeScript bodies and metadata, scripts imported by path, an npm
dependency, folder-metadata inheritance, middleware, per-run `params`, `invoke`
chaining, a streaming request, and an `assert` scenario that checks all of it.

It dogfoods grpcview against itself. Every request calls
`grpcview.v1.WorkspaceService` on grpcview's own workspace server, so there is
nothing to bring up: any `grpcview` command starts that server on demand, on
`localhost:10000`, reflecting its own API. No echo server, no second process.

Everything here is real: `smoke` passes, and its 13 assertions are the reason to
trust the rest of this file.

## Running it

```bash
# every saved request, as the tree sees it
grpcview ls --collection example

# a saved request, by display-name path; exit code is the gRPC status
grpcview invoke "Workspace/ListCollections" --collection example

# per-run params
grpcview invoke "Workspace/RunScript (params)" --collection example --param expression="6 * 7"

# resolve and evaluate everything, send nothing
grpcview invoke "Workspace/RunScript (generators)" --collection example --dry-run

# the streaming one: one frame per line, then the status
grpcview invoke "Workspace/Streaming/InvokeStreaming" --collection example

# scripts are addressed by path, extension included
grpcview script ls --collection example
grpcview script run scripts/smoke.ts --collection example
```

The same requests are addressable from the MCP server (`grpcview mcp` →
`invoke_saved`, `invoke_saved_streaming`, `describe_method`) and from the UI,
which is the point: one saved request, four surfaces.

For a throwaway run — CI, or a check you do not want in your own run history —
point `GRPCVIEW_CONFIG_DIR` at a temp directory. The run then touches neither
your history nor your resolve caches, and gets its own server:

```bash
export GRPCVIEW_CONFIG_DIR=$(mktemp -d)
grpcview script run scripts/smoke.ts --collection example   # exit 0 is 13 assertions passed
grpcview shutdown                                           # stops the server that run started
```

`--in-process` does a command's work without a server at all, which is how a
fresh state directory gets set up before trust has been granted.

## What's in it

```
Workspace/                 folder metadata: two headers everything below inherits
  ListCollections          no target; falls back to the collection's reflection source
  DescribeMethod (JSON)    a plain protojson body — valid JSON is valid TypeScript
  RunScript (params)       `params` from grpcview:request, with a default for the one param
  RunScript (generators)   body and metadata both import scripts from this collection
  RunScript (middleware)   ~/scripts/trace-headers.ts rewrites the body and stamps two headers
  Invoke (chained)         a body built by invoke(), asking grpcview to call itself
  Streaming/               folder metadata in expression form, spreading its parent's
    InvokeStreaming        grpcview streaming grpcview: two frames and a result

scripts/
  ids.ts                   exports requestId — imported by two bodies and the middleware
  stamp.ts                 imports dayjs, bundled from the embedded allowlist
  trace-headers.ts         middleware, attached to one request by its `~/` specifier
  smoke.ts                 the scenario — 13 assert checks over the saved requests
```

Three of the requests call `RunScript`, which evaluates a TypeScript scratchpad
inside grpcview's sandbox and answers with its last-expression value. That makes
the response a direct echo of whatever the body computed — a generated id, an
arithmetic expression from a param, a string a middleware rewrote — without any
second service to bounce it off.

## Imports, and the two grammars

A script has no name and no kind: it is a `.ts` file, and it is reached by
importing its path. `~/` resolves against the collection root, `@/` against the
workspace root, so `~/scripts/ids` and `@/example/scripts/ids` are the same file
named from two places. Nothing grpcview-specific is a global; `invoke`, `assert`,
`inherit` and `params` come from `grpcview:` modules.

A body written as a **module** — anything with `export default` — uses `import`
statements, and every request here except the JSON one does. A body written as an
**expression** is a single object literal, and an `import` statement cannot stand
in expression position, so it uses `require(…)` instead:
`Workspace/Streaming/`'s folder metadata is the example. Same resolver, different
grammar. The specifier must be a string literal either way — a computed one is
rejected before the bundle.

## Feature map

| Feature | Where to look |
| --- | --- |
| Reflection source | `grpcview.json` → `reflection:localhost:10000`, descriptors committed to `descriptors/` |
| Bazel source (built from `.proto`) | `grpcview.json` → `bazel://proto/grpcview/v1:grpcviewv1_proto`, descriptors left uncommitted |
| Source priority | `grpcview sources ls --collection example` — each source's `serves`/`wins` counts; the bazel source is first, so it wins both services |
| Doc comments survive the source | `Workspace/DescribeMethod (JSON)` returns `.proto` comments and `sourceId` names the bazel source; only its descriptor set carries `source_code_info`, reflection strips it |
| Target fallback | no request carries a target; each falls back to the collection's first *reflection* source, which is grpcview itself |
| protojson body | `Workspace/DescribeMethod (JSON)` — no wrapper, no `{{ }}`, just JSON |
| TypeScript body | every other request — a bare object literal under a hidden `export default` wrapper |
| Collection-relative import (`~/`) | `Workspace/RunScript (generators)` imports `~/scripts/ids` and `~/scripts/stamp` |
| Import from a middleware | `scripts/trace-headers.ts` imports `~/scripts/ids` — a middleware is an ordinary module |
| `require(…)` in expression position | `Workspace/Streaming/` folder metadata |
| npm dependency | `scripts/stamp.ts` imports `dayjs`, bundled by esbuild from the embedded allowlist — no `node_modules`, no network |
| A deterministic sandbox | the clock starts pinned at `2022-01-01T00:00:00Z` and `Math.random()` is seeded per instance, so two runs of `--dry-run` print the same body |
| Request metadata script | `Workspace/RunScript (generators)` — evaluated to `{ [key: string]: string[] }` |
| Folder metadata inheritance | `Workspace/` sets it, `Workspace/Streaming/` spreads `inherit()`, the requests inherit without asking |
| Middleware | `Workspace/RunScript (middleware)` — attached by specifier, runs last, on the outgoing message |
| `params` | `Workspace/RunScript (params)` |
| `invoke` chaining | `Workspace/Invoke (chained)` — a body that invokes another saved request, then has grpcview place a second call to itself |
| Streaming | `Workspace/Streaming/InvokeStreaming` — a server-streaming method whose frames are another grpcview stream's frames |
| `assert` scenario | `scripts/smoke.ts` |

## Known gaps

`invoke` rejects a streaming path outright, so `smoke` cannot drive
`Workspace/Streaming/InvokeStreaming`; the UI, the CLI and the
`invoke_saved_streaming` MCP tool all run it.
