# The workspace daemon

Bazel's client/server model: a CLI verb connects to the workspace's server if running,
starts one if not, server exits after inactivity. Design:
[`docs/design/shipped/daemon.md`](../../docs/design/shipped/daemon.md). Payoff: the linked-
descriptor cache (`service/workspace`'s LRU 16) and the compiled QuickJS engine stay warm
between invocations.

- Registration file is a hint, never authority — `<cache>/grpcview/servers/<sha256 of abs
  root>.json`. Client checks pid-alive → connects → verifies the server reports the same
  workspace root.
- Port defaults `10000`, falls back on conflict; explicit `--port` in use is an error
  (`grpcview url` reveals the actual port). `ServerService` is separate from
  `WorkspaceService` on purpose — its RPCs would register as MCP tools.
- Startup is locked, never hangs — advisory `flock` covers check→spawn→wait→connect
  (`lock.go`). Version skew restarts the server, keyed on the binary (path+mtime+size),
  since `"dev"` links every unstamped build.
- Idle exit is a counter from the last request, armed only when nothing's in flight
  (`DefaultIdleTimeout` = 1h, `connect.go`). **Only an auto-spawned server idles out** — a
  hand-run `grpcview` runs forever.
- A **dial** failure allows retrying anything; an in-flight break allows retrying only
  reads. `--in-process` is bazel's `--batch`. Boundary is loopback + origin policy — no
  token.
