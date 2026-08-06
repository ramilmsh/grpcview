# grpcview — CLI surface: implementation plan

**Status:** **Shipped** (all nine phases, on `trunk` 2026-08-03). Behavior is documented
in `AGENTS.md` §"The CLI"; this doc is kept for the decisions behind it, not as a
worklist. Everything below is written in the present tense about the tree as it stood on
**2026-08-03** (third-party claims 2026-07-30) — read its `file.go:line` citations as the
premise of a decision, not as a description of trunk.

**What this is.** A hand-written, task-shaped CLI as subcommands of the existing
`//service/cmd` binary, built on cobra, driven by the generated Connect client. Its
reason to exist is one verb: **run a saved request from a shell, with an exit code that
reflects the gRPC status.** Everything else is in service of that. Design reference:
**kubectl** (§3) — hand-written verbs over a runtime-discovered schema, structured input
from files and stdin, never per-field flags.

**Breaking changes are free.** There are no users and no collections to preserve. Where
an existing shape is in the way — the proto, the `-port` flag, a stored body's form —
change it and state the break in one line. No compat aliases, no migrations, no
deprecation windows. This is also license to break something purely to simplify.

**Reading order.** §1 what it's for → §2 why the primary verb needs a new RPC → §4
decisions → §5 surface → §6 architecture → §8 phases. §4 is the part to disagree with;
everything after it is mechanical.

**Companions.** [`request-body-contract.md`](../request-body-contract.md) is
**authoritative** on what a body may be — it declares anything in `docs/design/**` that
contradicts it stale. The one piece of it this plan depended on shipped here as **C0.2**:
the bare-object wrap now happens in `resolveInvokeBody`
(`service/workspace/invoke.go:359`), not only in the browser.
[`vscode/README.md`](../active/vscode/README.md) and
[`mcp/README.md`](../planned/mcp/README.md) are later surfaces over the same RPCs; C0, C1a and C2b
are written so they can build on them rather than fork.

---

## 1. What a grpcview CLI is for

The workspace is authored in a GUI (web UI today, VS Code next), and much of its
distinguishing content is **TypeScript** — metadata scripts, generators, middleware.
Nobody is going to author a generator at a shell prompt, and nobody needs a shell to
rename a folder. What is missing is a way to **run** — either what was already authored,
or a one-off call whose body the caller already has:

| Use case | Verb | Who |
|---|---|---|
| Run a saved request in CI / a git hook / a Makefile | `grpcview invoke <path>` | scripts |
| Run a saved request with parameters, assert on status | `grpcview invoke <path> --param k=v` | scripts |
| Call a method ad hoc with a body you already have | `grpcview invoke <svc>/<method> -f body.json`, or piped on stdin | scripts, humans |
| See what's in a collection without opening a UI | `grpcview ls`, `grpcview get` | humans, agents |
| Learn a message's shape without the UI | `grpcview describe <svc>/<method>` | humans, agents |
| Check a target/schema is reachable from a box with no GUI | `grpcview sources refresh` | humans |
| Smoke-test a script change | `grpcview script run <name>` | humans |

The third row is only viable because of
[the body contract](../request-body-contract.md): **a request body is protojson;
TypeScript is an authoring affordance layered over it.** A shell user pipes protojson and
is never asked to wrap it in `export default () => ({ … })`.

That costs exactly one backend branch, and **the branch does not exist yet** — the wrap
lives only in the browser. Valid JSON is a valid TS *expression*, not a valid TS
*module*: a bare `{ … }` at statement position parses as a block. The contract assigns
the fix to `resolveInvokeBody`; this plan schedules it as **C0.2** and treats it as a
hard prereq of both C1a and C1, because without it the CLI's headline verb misparses
every body it is handed.

**Where the CLI lives: subcommands on the one binary.** `grpcview` with no subcommand
keeps serving the UI + API. This is forced, not chosen: the single self-contained binary
is a defining trait, the embedded UI is 26.9 MB of the 49.6 MB binary (verified
`bazel-bin/service/cmd/index.html` and `.../cmd_/cmd`), and a second CLI binary would be
~20 MB of duplicated Go.

**Why not a generated CLI.** Settled, not open. gapic-generator-go's
`cmd/protoc-gen-go_cli` was **deleted upstream** on 2026-06-26 (PR #1767, "mostly
defunct… not actively maintained"). The surviving generator,
`NathanBaulch/protoc-gen-cobra`, needs grpc-go service stubs this repo does not generate
(§6.2), has had no commit since 2023-03, and emits a mirror in which a failed RPC **exits
0**, because grpcview reports target failures inside the response rather than as a
Connect error (D9). A 1:1 mirror of 17 CRUD RPCs is not the surface anyone wants anyway;
the verb that matters (§2) is not an RPC at all.

---

## 2. The primary verb does not exist as an RPC

**What the UI does today.** Verified
`ui/src/features/workspace/RequestWorkspace.tsx:251-280`: pressing Send calls `Invoke`
with `{workspaceName, path, itemName, service, method, body, metadataScript, target}`,
where `body` and `metadataScript` are the **live editor buffers**. `path`/`itemName` are
along for the ride so the server can record history and fold in ancestor-folder metadata
— they do **not** select the body.

**A shell caller has no editor buffer.** To run "the request saved at `Auth/Login`" it
would have to `Get` the whole workspace, walk the tree, pull `draftBody` and
`draftMetadataScript` out of the item and echo them back to `Invoke` — a read-modify-send
round trip that reimplements resolution on the client and races anyone editing in the UI.

**That path already exists in Go, just not over the API.**
`Collection.ResolveRequest(ctx, parent, name)` (`service/store/fs.go:427`) followed by
`invokeUnary(ctx, invokeSpec{…})` (`service/workspace/invoke.go:96`) is exactly what
`gv.invoke` runs for script-to-script calls (`service/workspace/gvinvoke.go:98-145`). It
resolves the saved body, metadata script, middleware chain, folder-inherited metadata and
target server-side, and it accepts `params`.

**So C1a adds `InvokeSaved`** — the same pair, exposed as an RPC. Not new behavior; the
existing `gv.invoke` machinery given a public door.

Two consequences that also can't come from a generated mirror:

- **Exit codes.** `InvokeResponse` carries the target's gRPC status *inside*
  `Request.Response.status` (verified `proto/grpcview/v1/workspace.proto:149-156`).
  Turning "status 13" into "exit 1" is hand-written mapping, and it is the single most
  important line in the feature.
- **`params`.** `invokeSpec.params` exists (`invoke.go:66-78`) but `InvokeRequest` has no
  `params` field, so today only `gv.invoke` can parameterize a run.

---

## 3. Modeled on kubectl

kubectl is the largest, most-used API CLI in existence and is **entirely hand-written** —
`k8s.io/kubectl/pkg/cmd` holds ~40 per-verb packages, and `k8s.io/code-generator` ships 11
generators of which **none** emits a CLI (verified against both repos). Its genericity
comes from runtime discovery, not codegen. Same shape as grpcview: a fixed verb set over a
schema resolved at runtime by reflection.

Copied: structured input from files and stdin, never per-field flags (D12); content
decides, not the file suffix; a `describe` verb over the runtime schema (C2b); a few
curated ergonomic flags (`--param`, `--target`) rather than a mirror; `-o` for output
shaping; `--dry-run` on `invoke` to print the resolved request without sending.

One deliberate divergence: kubectl's verb set is fixed because Kubernetes has a uniform
REST verb grammar over any resource. gRPC has none — every method is its own verb. The
resource-shaped part of grpcview is the *collection*, and that is what
`ls`/`get`/`request`/`folder` address.

---

## 4. Decisions

**D1 — Hand-written, task-shaped verbs on the existing binary.** §1.

**D2 — Use cobra.** For ~8 verbs with sub-verbs it buys persistent flags, grouped help,
`grpcview completion`, and nested command trees (`sources add`, `request mv`) that stdlib
`flag` makes you hand-roll. **The dependency is available offline here**, which is what
changed the answer — verified 2026-08-03:

| module | in the module cache | version in `/Users/r/dev/core/go.mod` |
|---|---|---|
| `github.com/spf13/cobra` | yes (`.zip` + `.ziphash`) | v1.10.2 (`:74`) |
| `github.com/spf13/pflag` | yes | v1.0.10 (`:281`, indirect) |
| `github.com/inconshreveable/mousetrap` | yes | v1.1.0 (`:242`, indirect) |
| `go.yaml.in/yaml/v3`, `cpuguy83/go-md2man/v2` | `.mod` present (graph only) | — |

`go.sum` lines can be copied verbatim from `/Users/r/dev/core/go.sum`. Stated honestly:
**cache contents are a property of this laptop, not of the repo.** C0.0 owns it.

**D3 — The CLI is a client of `WorkspaceService`, and the default binding is
in-process.**

- grpcview's own API is **Connect**. The repo generates a Connect client and handler from
  `service.proto` and nothing else — no grpc-go service stubs (§6.2).
- The handler, `workspace.Workspace`, is a **plain Go struct** over `store.Store` + the
  scripting engine (`service/workspace/workspace.go:33-36`) — no server, no port, no
  HTTP. It and the generated client have identical method sets (§6.2), so the CLI codes
  against one interface and picks the implementation:

  | mode | how | when |
  |---|---|---|
  | **in-process** (default) | `workspace.New(ctx)`, called directly | CI, a container with nothing running, a git hook |
  | **remote** (`--server addr`) | `grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, addr)` | a UI is already open and you want one process touching the collection |

  No third mode, no autodetection: dial-if-reachable was dropped because "which process
  wrote my history" should not depend on whether a server happened to be up.
- Separately, and confusingly close by: grpcview **does** use grpc-go internally to dial
  the *user's* target servers (`invoke.go` `resolveMethod`). In-process mode means the CLI
  process does that dialing itself.

The cost is that two processes can write the same collection directory without a lock
(risk 1, §9).

**D4 — Add `InvokeSaved` (unary) and `InvokeSavedStreaming` (server-streaming) RPCs.** §2.
The name is deliberately the less elegant option: the saved form arguably deserves to be
`Invoke` with the ad-hoc one renamed `InvokeMethod`, and breaking the proto is free — but
that rename touches the UI's Send path, which this track does not own. Adding an RPC here
and renaming there keeps the two independently landable.

**D5 — `--protocopt=--include_source_info` lands standalone (C0.1), and is *not* a CLI
blocker.** Bazel strips `SourceCodeInfo`, so generated Go carries **zero** proto comments
(Appendix A). Nothing in C0–C3 reads one — a hand-written CLI writes its own `--help`
strings. It stays in the plan because it is ten minutes and the bug is silent.

**D6 — All collection addressing goes through one resolver.** A single `collectionFlags`
value (`--workspace`, default `"default"`, matching `ui/src/lib/workspace-query.ts:38`) is
registered once as a **persistent flag** and produces the `workspace_name` field at
exactly one call site. Do not sprinkle `WorkspaceName:` literals across verbs; when the
field is eventually replaced by a directory, one file changes.

**D7 — CLI runs record history by default; `--no-history` opts out.** A CLI invoke is a
real user-initiated run, unlike `gv.invoke`'s fan-out (which passes `recordHistory=false`,
`gvinvoke.go:135`). A CI loop that does not want 1000 history entries passes the flag.

**D8 — Output contract: stdout is data, stderr is everything else.** `-o body` (default)
prints the response message as one line of JSON; `-o json` prints the whole
`Request.Response` for `jq`; `-o raw` prints the response bytes unchanged. Streaming
prints NDJSON — one message per line — with the terminal frame on stderr unless `-o json`.
Latency, status text and warnings are **always** stderr. No colors, no TTY detection, no
pager.

**`-o` is per-command, not persistent** — each verb registers it with its own value set
(`invoke`: `body|json|raw`; `ls`: `text|json`; `describe`: `proto|json`), exactly as
kubectl does. One persistent flag with three disjoint value sets is a validation problem
with no upside.

**D9 — Exit codes.**

| Code | Meaning | Examples |
|---|---|---|
| `0` | the invoked call returned `status.code == 0` | anything that worked; `--dry-run` |
| `1` | the invoked call returned a **non-OK gRPC status** | `NOT_FOUND`, `PERMISSION_DENIED`, target returned `13` |
| `2` | **grpcview's own** failure — nothing was invoked | unknown verb or flag, unknown request path, ambiguous path, no target configured, body/metadata would not evaluate, dial failure, unreachable schema |

The 1-vs-2 line is exactly the Connect-error-vs-`status`-in-payload line the backend
already draws, so it needs no new classification logic: a Connect error → 2, a returned
`Request.Response` with a non-zero `status.code` → 1. Verified that the split holds where
it matters: `resolveMethod` failure (dial, unknown method) returns a Go error from
`invokeUnary` (`invoke.go:99-101`) → Connect error → 2, while a target's non-OK status
comes back inside `out.Status` with a nil error (`gvinvoke.go:96-98`) → 1.

**D10 — Verb vocabulary matches the existing system.** The product already says *invoke*,
*request*, *folder*, *source*, *script*; the CLI says the same words. There is no `call`
verb.

| CLI | RPC |
|---|---|
| `grpcview invoke <path>` | `InvokeSaved` |
| `grpcview invoke <svc>/<method> -f` | `Invoke` |
| `grpcview get` | `Get` |
| `grpcview describe <svc>/<method>` | `DescribeMethod` (C2b) |
| `grpcview sources add` | `AddDescriptorSource` |
| `grpcview script run` | `RunScript` |

**D11 — No raw 1:1 escape hatch verb.** A `grpcview rpc <Method> -d '{json}'` was proposed
and dropped: it mirrors the CRUD surface the UI owns and needs a 17-arm switch to stay in
sync with the proto. Reconsider only if a concrete script needs an RPC C2/C3 did not
cover.

**D12 — No per-field flags, ever.** `PodSpec` has hundreds of fields and kubectl has no
`--containers-0-image`. Structured input arrives as `-f body.json`, `-f body.ts`, `-f -`,
or a bare pipe.

**D13 — Client-streaming and bidi send every message up-front, following the existing
convention.** `InvokeStreamRequest.messages` is `repeated string`, "in send order", and
the proto states the rule (`service.proto:240-244`): unary and server-streaming targets
expect exactly one; **client-streaming and bidi receive all of them composed up-front —
there is no live interleave.** The CLI reads NDJSON on stdin (one protojson message per
line), collects it, and sends it as `messages[]`.

The trap worth naming: the proto calls the up-front limit "a deliberate v1 limit of *the
browser transport*", and a CLI has no such transport constraint. That is an invitation to
make the CLI the one surface with true incremental streaming. **Don't** — it is a wire
protocol rewrite for one client, and it would make the CLI and the UI disagree about what
a client-streaming run even is. The CLI inherits the limit. NDJSON in / NDJSON out is a
shell-shaped *interface* over the existing *protocol*, not a change to it.

---

## 5. Surface specification

```
grpcview                                  serve the UI + API (default; today's behavior)
grpcview serve    [--port 10000]
grpcview version
grpcview completion bash|zsh|fish         (free, from cobra)

grpcview invoke   <request-path> | <service>/<method>
                  [-f <file>|-] [--param k=v]... [--params-file f.json]
                  [--target host:port] [--tls] [--metadata k=v]...
                  [--metadata-file f] [--no-history] [--dry-run]
                  [-o body|json|raw]

grpcview ls       [<folder-path>] [-o text|json]
grpcview get
grpcview describe <service>/<method> [-o proto|json]

grpcview sources  ls | add <addr>|<file.binpb> | refresh [<id>] | rm <id> | reorder <id>...
grpcview request  create <path> --service S --method M [-f body] | rm <path> | mv <path> <new-parent> [--before <name>]
grpcview folder   create <path>
grpcview script   ls | run <name>|- [--kind generator|middleware|scenario]
```

**Persistent flags** on the root command: `--workspace` (D6), `--server <addr>` (D3),
`--timeout` (default 30s, applied as a `context.WithTimeout`). `-o` is per-command (D8).

**Breaking change, intentional:** today's invocation is Go-flag style, `grpcview -port
10000`. pflag parses `-port` as the shorthand cluster `-p -o -r -t` and errors. The flag
becomes `--port`. No alias.

**Paths.** A `<request-path>` is display names joined by `/`, exactly as `splitInvokePath`
(`gvinvoke.go:151`) already parses them for `gv.invoke`: the last segment is the item, the
leading ones are the parent folders. Reuse that function. A display name containing a
literal `/` is unreachable this way — an accepted v1 gap `gv.invoke` already accepts.

**`invoke`'s two argument forms**, discriminated by whether the argument resolves to a
saved request:

```console
$ grpcview invoke Auth/Login --param tenant=acme
{"user":{"id":"u_91","email":"a@b.c"},"token":"eyJ..."}
$ echo $status
0
$ grpcview invoke Auth/Login --param tenant=nope
grpcview: Auth/Login: NOT_FOUND: tenant "nope" does not exist   (12ms)
$ echo $status
1
$ cat body.json | grpcview invoke user.v1.UserService/GetUser
```

Resolution order is **saved-request first, then `service/method`**. An argument matching
both is **ambiguous and must error with exit 2** — never guess.

**How ambiguity is detected** (the plan previously asserted the rule without a
mechanism): `invoke` fetches the `Get` snapshot once, up front, and resolves the argument
against **both** interpretations — a tree path, and a `service/method` in the merged
services list. Catching `NotFound` from `InvokeSaved` cannot work, because a miss on one
interpretation says nothing about the other. The cost is one extra RPC per invoke; in the
default in-process binding that is a filesystem read.

`-f` on a saved request *overrides* the stored body; `--target` overrides the stored
target.

**Error text format** — one line, stderr, always prefixed `grpcview: `:

```
grpcview: <path-or-method>: <CODE>: <message>   (<latency>)     # exit 1
grpcview: <what failed>: <cause>                                # exit 2
```

**`--dry-run`** (stdout, exit 0) prints the resolved request as JSON: target, service,
method, the *evaluated* bodies, the *evaluated* metadata. Produced **server-side** (C1a),
because evaluation is server-side — the CLI cannot run QuickJS itself.

**Param values.** `--param k=v` parses `v` as JSON and falls back to the literal string,
so `--param n=3` is a number and `--param n=three` is a string. Repeatable
(`StringArrayVar`). `--params-file` merges a JSON object; explicit `--param` wins. Params
reach the body as `gv.request.params`.

---

## 6. Architecture

### 6.1 Package layout

```
service/cli/
  root.go       newRootCmd(), persistent flags, exit-code mapping, Main()
  invoke.go     C1
  body.go       the -f / stdin reader (shared by invoke and request create)
  ls.go get.go  C2
  sources.go    C2 (ls) + C3 (add/rm/refresh/reorder)
  describe.go   C2b
  write.go      C3 (request/folder/script verbs)
  client.go     the Client interface + the two bindings + the streaming adapter
  *_test.go     table tests: (args, stdin) → (stdout, stderr, exit code)
```

Every verb is a function of (args, stdin, `Client`) → (stdout, stderr, exit code), so the
whole CLI is table-testable in one package with a fake `Client` and `bytes.Buffer`
streams. Assert on **all three** outputs: a verb that writes diagnostics to stdout is
exactly the bug D8 exists to prevent, and only an empty-stdout assertion catches it.

`service/cli` **must not import `//service`.** The UI embed lives in `//service/cmd`, and
a `cli → service → workspace` edge would drag 26.9 MB of `embedsrcs` into every CLI test.
Instead `Main` receives a `serve` closure:

```go
package cli

// Streams are the process's stdio, injected so every verb is table-testable
// without touching os.Stdout.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// ServeOptions is what the serve verb (and the bare, subcommand-less invocation)
// hands back to the caller that owns the HTTP server and the UI embed.
type ServeOptions struct{ Port int }

// Main builds the command tree, executes it, and returns the process exit code (D9).
func Main(ctx context.Context, args []string, s Streams, serve func(context.Context, ServeOptions) error) int
```

**Cobra specifics worth deciding once, in `root.go`:**

- The root command has **both** subcommands and its own `RunE` (bare `grpcview` serves the
  UI). That works but needs `Args: cobra.ArbitraryArgs` plus an explicit unknown-verb
  check, or cobra accepts `grpcview typoe` silently as root args.
- `SilenceUsage: true` and `SilenceErrors: true` on the root; `Main` prints the error
  itself in the D8 format and maps it to an exit code. Cobra's default (usage dump on
  every runtime error) is wrong for a CLI whose errors are gRPC statuses.
- Exit codes travel as a typed error: `RunE` returns `statusError{code int}` for a non-OK
  gRPC status; anything else is exit 2; a cobra flag-parse error is exit 2.
- `cmd.SetIn/SetOut/SetErr` from `Streams` so tests never touch the process's stdio.
- Every verb's `RunE` closes over a `func() (Client, error)` rather than a live client, so
  unit tests do not construct a workspace (which compiles the QuickJS engine — risk 4).

### 6.2 One interface, two bindings

The repo generates **Connect** Go code and only that: `go_proto_library` uses
`@rules_go//proto:go_proto` + `//tools:connect_go_proto_compiler`, and the output contains
`*.pb.go` + `*.connect.go` with no `grpc.ClientConnInterface` anywhere (verified). That is
the whole content of "no grpc-go stubs" — a statement about *our* generated code, not
about the CLI's role.

`WorkspaceServiceClient` and `WorkspaceServiceHandler` have identical method sets for all
16 unary RPCs and differ only in the streaming one — `*connect.ServerStreamForClient[T]`
vs `*connect.ServerStream[T]` + `error`. So:

```go
// service/cli/client.go
type Client interface {
	Get(context.Context, *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error)
	Invoke(context.Context, *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error)
	InvokeSaved(context.Context, *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error)
	// … the ~8 others the verbs need
}

// remote:      grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, addr)
// in-process:  workspace.Workspace (value receiver, verified workspace.go:33)
```

Only the streaming method needs bridging — a ~15-line adapter per binding onto a common
`func(frame *grpcviewv1.InvokeStreamResponse) error` callback.

### 6.3 Where argv dispatch goes, and the second main

Verified: `service.Run` owns argv today — `flag.IntVar(&port, "port", …); flag.Parse()` at
`service/service.go:34-36` — and **two** binaries call it: `//service/cmd`
(`main.go:20`, embeds the real UI) and `//service/cmd/dev` (`main.go:16`, serves a
`<h1>dummy</h1>` placeholder for Vite).

- `flag.Parse()` must leave `service.Run`; it cannot own argv once subcommands exist.
  `Run` grows an explicit `service.Options{Port int}` parameter.
- **Both** mains change together or `//service/cmd/dev` stops compiling. `dev` stays
  serve-only. **Note the regression this causes:** `dev/main.go` parses no flags of its
  own and inherits `-port` from `service.Run`. Removing `flag.Parse()` silently drops it,
  so `dev` needs its own two-line `flag.IntVar`/`Parse` to keep the Vite workflow working.
- `grpcview version` needs a stamped variable. `tools/workspace_status.sh` already emits
  `STABLE_VERSION_TAG` and `STABLE_COMMIT_SHA`, and **nothing consumes them — there is no
  `x_defs` anywhere in the repo** (verified). C0 adds
  `x_defs = {"…/service/cli.version": "{STABLE_VERSION_TAG}"}` to the `go_binary`.

  **`.bazelrc` sets `--workspace_status_command` but not `--stamp`**, and rules_go
  substitutes `{VAR}` only when stamping is enabled — unstamped, the binary is expected to
  embed the literal `{STABLE_VERSION_TAG}`, not a helpful default. This is the one C0 step
  whose behavior is asserted rather than measured: **build it both ways and look at the
  output before choosing.** If the literal does leak, the fix is a `strings.HasPrefix(version, "{")`
  → `"dev"` guard in Go, not `build --stamp` in `.bazelrc`, which would cost remote-cache
  hits on every stamped target on every commit.

### 6.4 Build registration

`bazel run //:gazelle` (root `BUILD.bazel:7`) generates the Go rules, so new files are
registered by running it — **and then reading the diff**. A new `.go` file that never made
it into `srcs` compiles locally and silently vanishes from the Bazel build, and tests then
"pass" without running. Every phase's verify step therefore ends with a
`--nocache_test_results` run naming the new test *by name*.

---

## 7. Current state (verified 2026-08-03)

| Fact | Where | Consequence |
|---|---|---|
| 17 RPCs on `WorkspaceService`, 16 unary + `InvokeStreaming` | `proto/grpcview/v1/service.proto:354-399` | C1a makes it 19 |
| UI's Send sends the editor buffer, not the saved body | `RequestWorkspace.tsx:251-280` | §2 — the premise of C1a |
| **The bare-object wrap is frontend-only**; `resolveInvokeBody` hands the body straight to an engine that needs `export default` | `ui/src/features/workspace/body-wrapper.ts:14-16`, `service/workspace/invoke.go:478`, `service/scripting/profiles.go:196` | **C0.2** — blocks C1a and C1 |
| `flag.Parse()` inside `Run`; two callers | `service/service.go:34-36`, `service/cmd/main.go:20`, `service/cmd/dev/main.go:16` | C0 touches both; `dev` loses `-port` |
| No `x_defs` anywhere; `.bazelrc` has no `--stamp` | whole repo, `.bazelrc` | §6.3's open question |
| `Store.Open(ctx, name)` → `<base>/<name>`; UI always sends `"default"` | `service/store/store.go:136`, `ui/src/lib/workspace-query.ts:38` | the CLI's `--workspace` default |
| `Collection.ResolveRequest` returns **sentinels** (`ErrItemNotFound`, `ErrNotARequest`), not Connect codes; `gv.invoke` wraps them with `fmt.Errorf` | `service/store/fs.go:426-427`, `gvinvoke.go:120` | C1a must map them (`connect.CodeOf` gives `Unknown` today) |
| `invokeUnary(ctx, invokeSpec{…})`, with `params` and `recordHistory` | `service/workspace/invoke.go:66-96` | C1a wraps this; no new invoke logic |
| `invokeUnary`'s streaming guard is **`Unimplemented`**, not `FailedPrecondition` | `invoke.go:106` | C1a step 6 reuses it verbatim |
| `streamInvoke` passes `params=nil` at three sites | `invoke.go:270`, `:278`, `:285` | C1a threads a real `params` through; localized |
| `messages` is `repeated`, composed up-front, no live interleave | `service.proto:240-244` | D13; `ResolvedRequest.body` must be repeated too |
| `InvokeRequest` has no `params` field | `service.proto` | D4's premise |
| `RunScript` takes inline `source`, not a saved script name | `RunScriptRequest`, `service.proto:314-318` | `script run <name>` must load the source first |
| `Server.TLS` is an empty message | `proto/grpcview/v1/workspace.proto:20` | `--tls` is one bool mapped by hand |
| `protoprint` ships in `jhump/protoreflect@v1.18.0` (already required) | `go.mod:9`; 83 files in the cached zip | C2b needs **no** new module |
| `.bazelrc` has no `--include_source_info` | `.bazelrc` (whole file read) | C0.1 still open (not a blocker — D5) |
| Test harness for a real gRPC target exists | `service/workspace/invoke_streaming_test.go:24` (`startEchoServer`) | C1a/C1 reuse it verbatim |

---

## 8. Phased plan

House rules: every phase ends green on `bazel test //...` (which builds as well as tests)
and is exercised against the real binary with an isolated `HOME`. Bare `go` commands are
never used. Every phase ends with `bazel run //:gazelle` and a read of the diff (§6.4).

**Minimum shippable unit: C0.0 + C0.2 + C0 + C1a + C1.** That is `grpcview invoke` with
correct exit codes, which is the entire reason the feature exists. C0.1 is *not* in the
MSU — it blocks nothing here (D5). Everything after C1 is additive and independently
droppable.

| Phase | Ships | Size | Prereqs |
|---|---|---|---|
| C0.0 | cobra in `go.mod`/`MODULE.bazel`, offline-green | ½ day | none |
| C0.1 | proto comments reach Go plugins | 10 min | none |
| C0.2 | the body sniff moves to the backend | ½ day | none |
| C0 | argv dispatch, root command, `serve`/`version`, both bindings | ½ day | C0.0 |
| C1a | `InvokeSaved` + `InvokeSavedStreaming` RPCs | ½–1 day | C0.2 |
| C1 | `grpcview invoke` | 1–1½ days | C0, C0.2, C1a |
| C2 | `ls`, `get`, `sources ls` | ½ day | C0 |
| C2b | `describe` | ½–1 day | C0 |
| C3 | write verbs | 1 day | C0, C2 |

C0, C1a and C2b are the phases a later MCP or VS Code surface would build on; none of them
should be forked.

### C0.0 — cobra, added offline

**Goal.** `github.com/spf13/cobra` available to `//service/cli` with `bazel build //...`
still green under `GOPROXY=off`.

**Files.** `go.mod`, `go.sum`, `MODULE.bazel`.

**Steps.** Follow [`phase-0-dependencies.md`](../planned/mcp/phase-0-dependencies.md) — the
*procedure* is the same one, though the modules are unrelated:
1. Hand-edit `go.mod`: `github.com/spf13/cobra v1.10.2` direct; `spf13/pflag v1.0.10` and
   `inconshreveable/mousetrap v1.1.0` indirect. **Copy the exact versions from
   `/Users/r/dev/core/go.mod`** (`:74`, `:281`, `:242`) so MVS selects versions that are
   in the cache.
2. Append the `go.sum` lines copied verbatim from `/Users/r/dev/core/go.sum` (both the
   `h1:` and `/go.mod h1:` line per module, including the graph-only `go.yaml.in/yaml/v3`
   and `cpuguy83/go-md2man/v2` entries). This is what replaces `go mod tidy`.
3. Add `com_github_spf13_cobra`, `com_github_spf13_pflag`,
   `com_github_inconshreveable_mousetrap` to `use_repo(go_deps, …)` in `MODULE.bazel`,
   alphabetically.
4. Populate the Bazel repository cache once
   (`--repo_env=GOPROXY=file:///Users/r/go/pkg/mod/cache/download`).

**Verify.** `bazel build @com_github_spf13_cobra//:cobra`; `bazel test //...` green with
**no** ambient `GOPROXY` override; `git diff` touches only the three files.

**Out of scope.** viper (persistent flags plus env is enough), cobra's `doc` package,
bumping any existing dependency.

### C0.1 — `--include_source_info` (standalone, ten minutes)

**Goal.** Make proto comments survive into generated Go. Not a CLI blocker (D5); a
repo-level bug this track happened to find. Measurements in [Appendix A](#appendix-a--the-proto-comment-contract).

**Files.** `.bazelrc` (one line).

**Steps.**
1. Add `common --protocopt=--include_source_info`.
2. Re-run the Appendix A probes: the descriptor set should grow from 9,257 B to ~36,983 B
   and `"returns the workspace snapshot"` should appear in `service.connect.go`.
3. Re-check the TypeScript path, which already had comments:
   `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy` must produce **no** source-tree
   diff.

**Verify.** `bazel test //...`; the probes above.

### C0.2 — the body sniff moves to the backend

**Goal.** `resolveInvokeBody` accepts a bare protojson object, so a shell pipe, an `-f
body.json`, and a stored bare-object body all evaluate. **Specified by
[`request-body-contract.md`](../request-body-contract.md) §"The structural change: the
sniff belongs to the backend"; scheduled here because C1a and C1 hard-depend on it.**

**Why it is a blocker and not a nicety.** Verified three ways: the wrap exists only in the
browser (`ui/src/features/workspace/body-wrapper.ts`); `resolveInvokeBody`
(`invoke.go:478`) passes the body straight to `RunRequestBody`, which needs `export
default` (`profiles.go:196` → `runGenerator`); and `body-wrapper.ts:14-16` documents
exactly why a bare `{ … }` cannot be handed over unwrapped — at statement position it
parses as a **block**, not an object literal. So today both a piped body and a stored
bare-object body read back by `InvokeSaved` misparse server-side. Without this, the CLI's
headline verb does not work.

**Files.** `service/workspace/invoke.go` (+ test).

**Steps.**
1. In `resolveInvokeBody`, before the engine call:
   `if !hasDefaultExport(body) { body = "export default async () => (\n" + body + "\n)" }`.
   One branch at the one choke point all three call sites funnel through — unary `Invoke`
   (`:117`), `InvokeStreaming` (`:270`), and `gv.invoke`'s re-entry.
2. Wrap **before** `transitiveGenerators(body, allGens)` runs, or the two disagree about
   what the body's source text is. Call-site detection is textual (`calledNames`), so the
   wrap is transparent to it — but only if both see the same string.
3. `hasDefaultExport` is a regex; per the contract's own caveat it can false-positive on
   `export default` inside a string literal. Accepted.
4. Leave the UI alone. `wrap`/`isCanonical`/`migrateBodyToTs` stay exactly as they are —
   they stop being what makes invoke work and become purely the Monaco view concern the
   contract says they should be.

**Tests.** `{"a":1}` evaluates to `{"a":1}`; an already-canonical module evaluates
unchanged (idempotence); a bare object calling a saved generator still composes; `[1,2]`
and `42` still fail with "expected the body to return an object"; the `emptyTSBody`
default (`invoke.go:48`) is untouched.

**Verify.** `bazel test //service/workspace:workspace_test --nocache_test_results
--test_filter=TestResolveInvokeBody`; then, in the browser against the real binary, send an
existing request and confirm nothing changed.

**Out of scope.** Any UI change. Any change to what a body *may* contain — this phase only
moves where the wrap happens.

### C0 — argv dispatch + the two bindings

**Goal.** Prove the two structural assumptions before writing any verb: (1) the one binary
dispatches subcommands without disturbing today's zero-arg behavior; (2) one interface is
satisfiable both in-process and over Connect.

**Prereqs.** C0.0.

**Files.** New: `service/cli/{root.go,client.go,root_test.go,BUILD.bazel}`. Edited:
`service/service.go`, `service/cmd/main.go`, `service/cmd/dev/main.go`,
`service/cmd/BUILD.bazel`.

**Steps.**
1. `service.Run(ctx, indexPage, service.Options{Port int})` — delete the
   `flag.IntVar`/`flag.Parse()` pair at `service.go:34-36`. Update both mains, and give
   `dev` its own `-port` flag (§6.3).
2. `newRootCmd` per §6.1: persistent flags, `SilenceUsage`/`SilenceErrors`, the `RunE`
   that serves, `Args: cobra.ArbitraryArgs` plus an explicit unknown-verb error (exit 2).
   Subcommands: `serve` (`--port`), `version`, and cobra's generated `completion`.
3. `Main` executes the tree and maps the returned error to an exit code (D9).
4. `service/cmd/main.go` becomes `os.Exit(cli.Main(ctx, os.Args[1:], streams, serveFn))`,
   where `serveFn` opens the embedded `index.html` and calls `service.Run`.
5. Declare `cli.Client` with two methods only (`Get`, `Invoke`) and assert both bindings
   satisfy it at compile time (`var _ Client = …`). Confirm no adapter is needed for unary
   methods.
6. Add `x_defs` for the version stamp, and resolve the stamping question in §6.3 by
   building both ways.

**Tests.** `root_test.go`: unknown verb → exit 2, usage on stderr, nothing on stdout;
`version` → exit 0, one stdout line; `serve --port 1` → the injected closure receives
`ServeOptions{Port: 1}`; no args → same closure with the default; a bad flag value → exit
2 without a usage dump.

**Verify.** Run the binary with no args under an isolated `HOME` and confirm the UI still
serves on :10000; `grpcview version` prints something other than `{STABLE_VERSION_TAG}`;
`grpcview serve --port 10111`; `bazel run //service/cmd/dev` still starts and still honors
`-port`; binary size +≤1 MB.

**Out of scope.** Any real verb, output formatting.

### C1a — `InvokeSaved` / `InvokeSavedStreaming`

**Goal.** Make "run the saved request at this path, with these params" a first-class RPC
(§2), and let `--dry-run` report *evaluated* bodies (which only the server can produce).

**Prereqs.** C0.2 — otherwise a stored bare-object body misparses the first time this RPC
reads one back.

**Files.** `proto/grpcview/v1/service.proto`; new `service/workspace/invoke_saved.go` +
`invoke_saved_test.go`; `service/workspace/invoke.go` (thread `params` through
`streamInvoke`); `service/workspace/gvinvoke.go` (call the shared helper).

**Proto:**

```proto
// InvokeSaved runs the saved request at path/item_name using its own stored body,
// metadata script, middleware and target, and returns the same Request.Response
// shape Invoke does. params reaches the body as gv.request.params.
rpc InvokeSaved(InvokeSavedRequest) returns (InvokeSavedResponse);
rpc InvokeSavedStreaming(InvokeSavedRequest) returns (stream InvokeStreamResponse);

message InvokeSavedRequest {
  string                 workspace_name = 1;
  repeated string        path           = 2;   // parent folders, display names
  string                 item_name      = 3;
  google.protobuf.Struct params         = 4;   // gv.request.params for this run
  optional Server        target         = 5;   // overrides the saved target
  // messages, when non-empty, override the saved body for this run only (the CLI's
  // -f / stdin). Send order, same rule as InvokeStreamRequest.messages: unary and
  // server-streaming take exactly one; client-streaming and bidi take all of them
  // composed up-front.
  repeated string        messages       = 6;
  // record_history defaults to true; a fan-out caller sets it false.
  optional bool          record_history = 7;
  // dry_run resolves and evaluates everything, sends nothing, returns only `resolved`.
  bool                   dry_run        = 8;
}

message InvokeSavedResponse {
  Request.Response         response = 1;
  optional ResolvedRequest resolved = 2;   // dry_run only
}

// ResolvedRequest is what a dry run reports: everything the server would have sent,
// post-evaluation and post-middleware, with nothing dialed.
message ResolvedRequest {
  string                 service  = 1;
  string                 method   = 2;
  Server                 target   = 3;
  repeated string        messages = 4;   // evaluated JSON, post-middleware, in send order
  google.protobuf.Struct metadata = 5;   // evaluated, post-middleware
}
```

`messages` is repeated in both messages because the whole pipeline is: `resolveInvokeBody`
(`:478`) and `applyRequestMiddleware` (`middleware.go:57`) both carry `[]string`, and
`InvokeStreamRequest.messages` is repeated (D13). A scalar would silently show one of N on
a client-streaming dry run.

**Steps.**
1. Write the messages and RPCs with verb-first, self-contained comments whose first line
   stays under ~100 chars (Appendix A.2).
2. `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy` (required after any `.proto`
   edit).
3. Handler: `Collection.ResolveRequest` (`fs.go:427`) → `invokeUnary(invokeSpec{…})` with
   `params: req.GetParams().AsMap()` and `recordHistory` defaulting **true** (D7). This is
   `scriptInvoker` (`gvinvoke.go:98-145`) minus the depth cap and the JSON envelope —
   **factor the shared middle into one helper** and have `gv.invoke` call it with
   `recordHistory=false`, rather than copying it.
4. **Map the store's sentinels to Connect codes** — this does not exist today.
   `ErrItemNotFound` → `NotFound`, `ErrNotARequest` → `FailedPrecondition`. `gv.invoke`
   currently wraps both with `fmt.Errorf` (`gvinvoke.go:120`), so `connect.CodeOf` returns
   `Unknown`; the shared helper from step 3 is the right place to fix it for both callers.
5. Streaming: thread a `params map[string]any` argument through `streamInvoke` (currently
   `nil` at `invoke.go:270`, `:278`, `:285`) and route `InvokeSavedStreaming` to it.
   Existing callers pass `nil` — no behavior change.
6. `dry_run`: stop after the shared pre-send steps (`resolveInvokeBody`,
   `resolveInvokeMetadata`, `applyRequestMiddleware`) and return `ResolvedRequest` with an
   unset `response`. No dial, so a dry run works with the target down.
7. `InvokeSaved` against a streaming method reuses `invokeUnary`'s **existing
   `Unimplemented` guard verbatim** (`invoke.go:106`) — do not invent a second code for the
   same condition. C1 picks the right RPC from the resolved method kind, so this is a
   fallback, not a normal path.

**Tests.** `invoke_saved_test.go`, using `startEchoServer` (`invoke_streaming_test.go:24`):
a saved unary request runs and returns OK; `params` reaches the body (assert on an echoed
field derived from `gv.request.params`); unknown path → `NotFound` and a path naming a
folder → `FailedPrecondition` (both assert on `connect.CodeOf`, so both fail before step
4); a non-zero-status target → `(response, nil)` with the status inside, **never** a
Connect error (the invariant D9 depends on); `record_history=false` leaves history
untouched while the default appends one entry; `dry_run` returns `resolved` and never
dials; a client-streaming saved request with N `messages` sends all N in order; `gv.invoke`
still behaves identically after the refactor in step 3.

**Verify.** `bazel test //service/workspace:workspace_test --nocache_test_results
--test_filter=TestInvokeSaved`; `bazel test //...`.

**Out of scope.** Frontend changes of any kind — this phase adds an RPC and touches no UI.

### C1 — `grpcview invoke`

**Goal.** Run a saved request by display-name path, from a script, with a meaningful exit
code — plus the ad-hoc `<svc>/<method> -f body.json` form.

**Prereqs.** C0, C0.2, C1a.

**Files.** New `service/cli/invoke.go`, `service/cli/body.go`,
`service/cli/invoke_test.go`. Edited `service/cli/client.go`.

**Steps.**
1. Fetch the `Get` snapshot once and resolve the positional argument against both
   interpretations (§5). Parse the path form with `splitInvokePath` (`gvinvoke.go:151`) —
   reuse, do not reimplement. Both matching → exit 2 with a message naming both.
2. Flags: `--param` (repeatable), `--params-file`, `--target`, `--tls`, `--metadata`
   (repeatable), `--metadata-file`, `--no-history`, `--dry-run`, `-o`. `--timeout` and
   `--workspace` are inherited.
3. `-f <file>|-` and bare stdin supply the body. **Pass the bytes through unchanged** — no
   parsing, wrapping or reformatting; C0.2 made that the backend's job at one seam, so `-f`
   behaves identically for protojson and for a TS module. Bare stdin applies only when
   stdin is not a terminal and no `-f` was given.
4. For a client-streaming or bidi method, stdin is **NDJSON: one protojson message per
   line** (D13). Collect the lines, preserve order, send them as `messages[]`. A single
   JSON object with no newlines is one message, so the unary and cs forms are the same code
   path. Blank lines are skipped; a line that is not a JSON object is exit 2 naming the
   line number.
5. Dispatch: saved + unary → `InvokeSaved`; saved + streaming → `InvokeSavedStreaming`;
   ad-hoc → `Invoke` / `InvokeStreaming` with the messages from `-f`/stdin.
6. Render per D8, exit per D9. Streaming: NDJSON to stdout, exit from the terminal frame's
   status.
7. `--dry-run` prints `ResolvedRequest` as indented JSON, exit 0.

**Tests.** Table tests over `Main` with a fake `Client`: OK status → stdout JSON, empty
stderr, exit 0; non-OK status → exit 1 in the D9 format; Connect error → exit 2; ambiguous
path → exit 2 with neither invoke RPC called; `--param n=3` arrives as a JSON number and
`--param n=three` as a string; `-f -` reaches the request byte-identical (including a
multi-line TS module, which must **not** be split on newlines — the NDJSON split applies
only to the cs/bd path); three NDJSON lines become three ordered `messages`; `--dry-run`
never calls the invoke RPCs; streaming frames render one line each.

**Verify.** `bazel test //service/cli:cli_test --nocache_test_results`; then the real
binary with an isolated `HOME` and `bazel run //service/echo/cmd -- -port 50055`: create a
request in the UI, `grpcview invoke Echo/Unary` → stdout is JSON only, `$status` 0; **pipe
a bare `{"message":"hi"}` and confirm it sends** (the C0.2 payoff); dead port → exit 2;
target error → exit 1; `grpcview invoke Echo/ServerStream` prints N lines; `grpcview invoke
Echo/Unary --dry-run` prints the evaluated body with the target down.

**Out of scope.** Write verbs. Assertions on the response (a scenario feature). True
incremental client streaming (D13).

### C2 — read verbs: `ls`, `get`, `sources ls`

**Goal.** Make a collection legible from a shell — and to an agent that has a shell but no
other surface.

**Prereqs.** C0.

**Files.** New `service/cli/{ls.go,get.go,sources.go}` + tests.

**Steps.**
1. `ls [<folder-path>]` walks the `Get` snapshot and prints one line per item, grep-able
   and `invoke`-pasteable:

   ```
   Auth/                                   folder
   Auth/Login          auth.v1.AuthService/Login
   Echo/Unary          echo.v1.EchoService/Unary        [2 middleware]
   ```

   `-o json` prints the subtree as JSON. A folder path that does not exist → exit 2.
2. `get` prints the whole `GetResponse` as protojson.
3. `sources ls` prints id / kind / priority / count of won services / error — the Sources
   view as text, **including a shadowed source being visibly shadowed**, since that is what
   the UI shows and a text dump usually loses.

**Tests.** Golden-output tests against a fixed fake snapshot: a nested folder, a request
with middleware, a source with a resolve error, a shadowed source.

**Verify.** `bazel test //service/cli:cli_test --nocache_test_results`; eyeball `sources
ls` against the UI's Sources view for the same workspace.

**Out of scope.** Colors, TTY detection, pagers (permanently — D8).

### C2b — `grpcview describe`

**Goal.** Print a method's input and output shape from the merged descriptor set, so a
shell user can write a body without opening the UI. Without it, C1's ad-hoc `-f` form
requires knowing the field names already.

**Prereqs.** C0.

**Files.** New `service/workspace/describe.go` (+ test), new `service/cli/describe.go` (+
test).

**The output format decision — a descriptor is itself a protobuf message, so JSON output
is its protojson, not a shape we invent.** `-o json` emits the protojson of a
`FileDescriptorSet` containing the method's `MethodDescriptorProto` plus the
`DescriptorProto`s of its input, output and every transitively referenced message and
enum. That is (a) exactly what the descriptor API already gives us, with zero mapping code
and therefore zero mapping bugs, (b) round-trippable into any protobuf library, and (c)
already schema-documented by `descriptor.proto`. An invented "flat field list" is prettier
and is a lossy re-encoding of a standard format — do not build one. `-o proto` stays the
human view.

**Steps.**
1. Helper:
   `func (w Workspace) DescribeMethod(ctx context.Context, workspaceName, service, method string) (*desc.MethodDescriptor, string, error)`
   — resolve through the workspace's **cached merged descriptor set** (the one `Get`
   already serves via `mergeSources`, `sources.go:237`), **not** by dialing. The second
   return is the id of the source that won the service.
2. `-o proto` (default) renders with `protoprint`
   (`jhump/protoreflect@v1.18.0/desc/protoprint` — already required, verified present in
   the cached zip; no new module): the input message's `.proto` text plus transitively
   referenced types, then the output message.
3. `-o json` emits the protojson `FileDescriptorSet` described above.
4. Print the winning source id on stderr. Comments appear only when the descriptor carries
   `source_code_info` — a `buf build` upload does, reflection does not — so `describe` must
   **say which source it read**, or an empty-comment result looks like a bug rather than a
   property of reflection.

**Tests.** Helper: unknown service → `NotFound`; unknown method → `NotFound` naming the
service; a method whose input references a nested message pulls that message in; the
returned source id matches the priority winner when two sources define the same service.
CLI: `-o json` unmarshals cleanly into a `FileDescriptorSet` and contains the input message
(assert by round-trip, not by string match).

**Verify.** `bazel test //service/... --nocache_test_results`; against the echo server,
`grpcview describe echo.v1.EchoService/Unary`; confirm comments appear for a `buf build`
upload source and are absent for a reflection-only source.

**Out of scope.** Rendering a *filled-in* example body (guessing values). `explain`-style
dotted-path drilling — add only if the flat output proves unwieldy.

### C3 — write verbs (only the ones with a scripting story)

**Goal.** Cover the automatable mutations, no more.

**Prereqs.** C0, C2 (shares the snapshot helpers).

**Files.** New `service/cli/write.go` (+ test); `service/cli/sources.go` grows the mutating
subcommands.

| Verb | RPC | Notes |
|---|---|---|
| `sources add <addr>` | `AddDescriptorSource` (`reflection`) | argument that parses as `host:port` |
| `sources add <file.binpb>` | `AddDescriptorSource` (`descriptor_set` + `file_name`) | argument that stats as a file; `file_name` is the basename, which is the upload's identity |
| `sources refresh [<id>]` | `RefreshDescriptorSource` | no id → all, in priority order |
| `sources rm <id>` | `RemoveDescriptorSource` | |
| `sources reorder <id>...` | `ReorderDescriptorSources` | full order, as the RPC expects |
| `folder create <path>` | `CreateFolder` | |
| `request create <path> --service S --method M [-f body]` | `CreateRequest` (+ `UpdateRequest` when `-f`) | reuses C1's body reader |
| `request rm <path>` | `DeleteRequest` | |
| `request mv <path> <new-parent> [--before <name>]` | `MoveItem` | reparent and reorder are the same RPC |
| `script ls` | `Get` (scripts section) | |
| `script run <name>\|-` | read the source, then `RunScript` | **`RunScriptRequest` takes inline `source`, not a name** (verified `service.proto:314-318`) — `<name>` means "load that saved script's source and kind from the snapshot, then send it"; `-` reads source from stdin and `--kind` selects the profile |

**Non-goal, explicitly.** `--draft-body` / `--draft-metadata-script` as *inline string*
flags. Authoring TypeScript is an editor's job. But `request create` accepts `-f`: with
plain protojson a valid body (C0.2), "seed this request from a JSON file" is an obvious
scripting need and costs nothing beyond reusing C1's reader. Per-field flags stay out
permanently (D12).

**Tests.** One table test per verb over a fake `Client`, asserting the exact request
message built — this is where argument→field mapping bugs live. Plus `sources add`
disambiguation (address vs file) both ways, and `script run <name>` on an unknown name →
exit 2 without calling `RunScript`.

**Verify.** `bazel test //service/cli:cli_test --nocache_test_results`; run each verb
against an isolated-`HOME` store and confirm the UI (which reloads per RPC) shows the
result; confirm `sources add` of an unreachable target **lists the source with its error**
rather than failing the command.

**Out of scope.** `script create`/`update`/`rm` (editor work). Anything that edits
TypeScript.

---

## 9. Risks and non-goals

**Non-goals.** A second binary. A generated 1:1 mirror of the RPCs in any form (D11) —
anyone needing a raw call against a self-reflecting server can use a generic reflection
client such as `buf curl` or `grpcurl`; neither is a dependency of this project or of this
plan. True incremental client streaming (D13). A CLI for authoring TypeScript. An
interactive TUI. Colors/pagers. viper. `google.api`/GAPIC annotations on grpcview's proto.

**Risks.**

1. **Cross-process store writes have no lock.** `Collection.mu` is an in-process
   `sync.Mutex` (`store.go:158`) and there is no file lock, so an in-process `grpcview
   invoke` (which appends history — a write) racing a running UI server on the same
   directory is last-write-wins per file. *Resolution:* accept it (D3). `--server` is the
   opt-out; `--no-history` removes the only write `invoke` performs. If a lost update ever
   bites, the fix is one advisory lock in the store that benefits every surface.
2. **C0.2 changes behavior for every existing caller, including the UI.** It is a backend
   branch on the shared invoke path, so a bug there breaks Send, not just the CLI. Mitigated
   by the idempotence test (an already-canonical body must be untouched) and by a browser
   check in the phase's verify step. This is the one phase whose blast radius exceeds its
   size.
3. **Scripting-engine startup cost in-process.** `workspace.New` compiles the QuickJS
   module (~660 KiB) on every CLI invocation (`workspace.go:47`). Fine for a one-shot
   `invoke`; a loop of 100 pays it 100 times. If it shows up: use `--server` for the loop,
   or compile the engine lazily. Measure before optimizing.
4. **A new dependency in an offline workspace.** C0.0 is feasible *on this machine* because
   the cache holds cobra at core's versions. A clean CI runner needs one network-adjacent
   fetch. If that is unacceptable, the fallback is stdlib `flag` — roughly one extra day
   spread across C0–C3 and a worse `--help`.
5. **Two owners of argv.** Any later surface that wants a subcommand needs the same
   dispatch. C0 is deliberately tiny so it can be built on rather than forked.

**Open questions** — none blocks C0:

- **Does `x_defs` leak the literal `{STABLE_VERSION_TAG}` unstamped?** Asserted, not
  measured. §6.3; resolve during C0 by building both ways.
- **History on by default?** D7 says yes with an opt-out; the counter-argument is that CI
  runs are not interesting history and will evict real entries. Revisit after C1.
- **Does `InvokeSaved` need a per-run metadata override?** C1 wires `--metadata` for the
  ad-hoc form; for a saved request the metadata script owns it. Deferred.

---

## Appendix A — the proto-comment contract

### A.1 Proto comments never reach any Go plugin here (verified)

`.bazelrc` has no `--include_source_info`, so the Go path receives a `proto_library`
descriptor set carrying no `SourceCodeInfo`, and a plugin can only emit what it is given.
Probes, 2026-07-30 (`.bazelrc` re-checked 2026-08-03, still absent):

| probe | default build | with `--protocopt=--include_source_info` |
|---|---|---|
| `grpcviewv1_proto-descriptor-set.proto.bin` size | 9,257 B | **36,983 B** |
| `"returns the workspace snapshot"` in that set | 0 | **1** |
| `"copy of google.rpc.Status"` in `workspace.pb.go` | 0 | **1** |
| `"file_name identifies a descriptor_set upload"` in `service.pb.go` | 0 | **1** |
| `"returns the workspace snapshot"` in `service.connect.go` | 0 | **2** |
| the flag appears in the protoc action's command line (`bazel aquery`) | 0 | **1** |

So `type DescriptorSource struct` has **no doc comment at all** in generated Go, and
`service.connect.go` documents `Get` as the synthesized `// Get calls
grpcview.v1.WorkspaceService.Get.`

**The TypeScript path is different, and that is why this went unnoticed.** The committed
`proto/grpcview/v1/workspace_pb.d.ts` *does* carry the comments — `ts_proto_library` feeds
protoc-gen-es source info while `go_proto_library` does not. (Verified by output; the
mechanism is **unverified**.) Anyone checking "do our comments survive codegen?" by opening
the `.d.ts` gets a false green.

**Consequence.** Any Go generator that reads comments emits empty strings, silently. The
fix is C0.1. Nothing in this plan consumes them (D5), which is why C0.1 is not in the MSU.

### A.2 Comment house style

[`phase-2-comments.md`](../planned/mcp/phase-2-comments.md) holds the nine-rule house style and
a per-RPC rewrite table. Use it rather than writing a second one. Verb-first, no leading
identifier, first line self-contained and under ~100 chars.

Two notes specific to this track:

1. **A hand-written CLI does not inherit proto comments.** Cobra `--help` strings are Go
   string literals someone types. Where a flag maps 1:1 to a proto field, copy the field
   comment's **first sentence** so the two do not drift — that is a discipline, not a
   mechanism, and it is the only sense in which field comments are "the CLI's flag help".
2. **New RPC comments (C1a) are read by humans in `--help` and by tooling in generated
   Go.** Keep line 1 survivable under truncation to one terminal line. If both this track
   and a later one ever need the same slicing ("first sentence", "first paragraph"), it
   belongs in one small Go package both import — two implementations diverge within a
   month.
