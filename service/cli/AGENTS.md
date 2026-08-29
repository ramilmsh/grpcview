# The CLI

`grpcview` with no subcommand serves the UI + API. Everything else is a cobra verb here, on
the same binary.

```
grpcview                     serve UI+API, open browser
grpcview serve [--port 10000] [--idle-timeout <d>] [--no-open]
grpcview url | open | shutdown
grpcview invoke <request-path>|<service>/<method>
grpcview describe <service>/<method>  [-o proto|json]
grpcview ls [<folder-path>]  [-o text|json]
grpcview get
grpcview sources ls|add|commit|refresh|rm|reorder
grpcview trust [--off]                allow sources that run a build
grpcview request create|rm|mv   grpcview folder create
grpcview script ls|run          grpcview completion bash|zsh|fish
grpcview init [dir] [--name]
grpcview collections ls      [-o text|json] [--refresh]
grpcview mcp
```

Runs a saved request from a shell, exit code reflects gRPC status. Every verb takes
`--workspace <root>`, `--collection <id>`, `--server <addr>`, `--in-process`. **Root
resolves in one place, `wsroot.Discover`:** explicit `--workspace`, else
`$BUILD_WORKSPACE_DIRECTORY`, else nearest `.git`, else cwd with a warning.

- `service/cli` must not import `//service` — that would drag the full server and its
  embedded UI bundle into every CLI test. One `Client` interface, wire default
  (`service/wire`, shared with `service/mcp`): local, pinned-remote, reconnecting-remote.
- **Exit codes are the contract** (`exitCode`, `root.go`): `0` = OK; `1` = other gRPC status
  (inside `Request.Response.status`, no error); `2` = grpcview's own failure, nothing
  invoked.
- Where you stand decides what you address, like git/bazel: no `--collection` → nearest
  collection at/above cwd bounded by root, else the workspace's only collection, else exit 2
  listing candidates.
- stdout is data, stderr is everything else. `-o body` (default) prints nothing on failure;
  `-o json` prints the whole `Response` either way; streaming prints NDJSON; a mutation
  prints nothing, exits 0.
- Structured input only, never per-field flags: bodies via `-f file`, `-f -`, or a bare
  pipe; stdin is NDJSON for client-streaming/bidi, one verbatim message otherwise.
  `invoke`'s argument resolves against both saved-request path and `service/method` off one
  `Get` snapshot — matching both is exit 2.
- `InvokeSaved`/`InvokeSavedStreaming` resolve saved body/metadata/middleware/target
  server-side, support `dry_run`; `describe` never dials, answering from the cached merged
  descriptor set.
- `sources add` reads kind from the argument: a path that stats as a file → upload;
  `//`/`@` prefix → bazel label; else → reflection address.
- One writer — every verb goes through this workspace's daemon by default; `--in-process`
  is the escape hatch to two writers on one directory.
