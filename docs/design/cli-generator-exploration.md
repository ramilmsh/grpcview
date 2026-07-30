# grpcview — CLI surface: generator exploration

**Status:** Exploration + recommendation, and now also the plan of record for the CLI.
**Nothing implemented.** No dependency has been added; every third-party claim below
was checked against source or the live repo on **2026-07-30** and is marked verified /
unverified.

**Design reference: kubectl** (§2b) — hand-written verbs over a runtime-discovered
schema, structured input from files and stdin, never per-field flags.

**Companions:** [`vscode/README.md`](./vscode/README.md) (surface #3),
[`mcp/README.md`](./mcp/README.md) (surface #4),
[`request-body-contract.md`](./request-body-contract.md) (what a body may be, shared by
all four surfaces — this is what makes `-f`/stdin viable). The MCP plan landed while
this was being written; §5 defers to it on the comment convention and §5.0 hands it a
build-layer blocker it does not yet cover. Three shared prerequisites are called out
below: argv dispatch (§2), `--protocopt=--include_source_info` (§5.0), and an
`InvokeSaved` RPC (§7).

---

## 1. Verdict

**Hand-write a small, task-shaped CLI as subcommands of the existing
`//service/cmd` binary, driven by the generated Connect client interface. Do not
add a CLI generator.** The generator the user named — gapic-generator-go's
`cmd/protoc-gen-go_cli` — **no longer exists**: it was deleted from the repo on
2026-06-26 as "mostly defunct … not actively maintained" (verified below), so that
option is not a trade-off, it is a dead end. The remaining generator,
`NathanBaulch/protoc-gen-cobra`, is a genuinely good tool that loses here for three
independent reasons, any one of which would be enough: (a) it needs **grpc-go
service stubs**, which this repo deliberately does not generate — it has Connect
clients only — so adopting it means a third generated client surface for one
16-RPC service; (b) it requires **~7 new Go modules** (cobra, pflag, mapstructure,
strcase, mousetrap, the generator's own runtime, yaml) in a workspace whose
defining build invariant is `GOPROXY=off`, and the generator module is **not in
the local module cache**, so the add cannot be done offline at all; (c) its output
is a **1:1 mechanical mirror** of the proto, which for this proto produces
`--metadata-fields`, base64 descriptor uploads, whole TypeScript modules as single
shell arguments, **no way to set TLS at all** (verified: it silently drops empty
messages, and `Server.TLS` is empty), and — decisively — `grpcview invoke` that
**exits 0 when the invoked RPC fails**, because grpcview reports target failures
inside the response, not as a Connect error. A CLI that can't fail a CI job on a
failed call is not a CLI.

**Runner-up 1 — `protoc-gen-cobra`.** Lost on the offline dependency wall and on
the 1:1 surface being the wrong shape (§2, §4.2). Would have won if grpcview's API
were large, growing, and flag-shaped.

**Runner-up 2 — do nothing; use `buf curl`.** Verified: `buf` 1.61.0 is *already*
in this workspace (`bazel-out/bazel_env-opt/bin/bazel_env/bin/buf`) and the server
already reflects itself, so the entire mechanical 1:1 surface exists today at zero
cost. This is the correct answer for the raw path and the reason a generated
mirror has **negative** value: it would ship 20 MB of generated Go to duplicate a
tool already on the box. It loses as the *whole* answer only because it cannot do
the one thing a grpcview CLI is for — run a **saved** request by name (§2).

**Side-discovery, and the most actionable thing in this document:** the
"comments are the shared contract" premise is **currently broken at the build
layer**. Verified — Bazel's proto pipeline strips `SourceCodeInfo` before any Go
plugin runs, so grpcview's generated `.pb.go`/`.connect.go` contain **zero** proto
comments today. A one-line `.bazelrc` change (`common
--protocopt=--include_source_info`) restores them end to end, verified by probe.
Every comment-consuming generator — including the MCP one the sibling plan is
evaluating — silently emits **empty** descriptions until that flag is set. Land it
independently of both tracks; see
[§5.0](#50-blocker-proto-comments-never-reach-any-go-plugin-here-verified).

---

## 2. What a grpcview CLI is actually for

Decide this before comparing generators, because it eliminates most of them.

The workspace is authored in a GUI (web UI today, VS Code next), and much of its
distinguishing content is **TypeScript** — metadata scripts, generators, middleware.
Nobody is going to author a generator at a shell prompt, and nobody needs a shell to
rename a folder. What is missing is a way to **run** — either what was already
authored, or a one-off call whose body the caller already has:

| Use case | Verb | Who |
|---|---|---|
| Run a saved request in CI / a git hook / a Makefile | `grpcview call <path>` | scripts |
| Run a saved request with parameters, assert on status | `grpcview call <path> --param k=v` | scripts |
| Call a method ad hoc with a body you already have | `grpcview call <svc>/<method> -f body.json`, or piped on stdin | scripts, humans |
| See what's in a collection without opening a UI | `grpcview ls`, `grpcview get` | humans, agents |
| Check a target/schema is reachable from a box with no GUI | `grpcview sources refresh` | humans |
| Smoke-test a script change | `grpcview script run <name>` | humans |
| Anything else | `buf curl` | — |

The third row is only viable because of
[the body contract](./request-body-contract.md): a shell user pipes protojson and is
never asked to wrap it in `export default () => ({ … })`. Without that, an ad-hoc CLI
call would demand the caller speak TypeScript to send an object they already have,
and the row would collapse back into "use `buf curl`". Note there is no conversion
involved — valid JSON is valid TS, so the piped bytes are handed to the same evaluation
path a hand-authored module takes.

**The call:** **hand-written, task-shaped — not a generated 1:1 mirror, and not
"generated core + hand-written verbs".** The reasons are specific, not aesthetic:

1. **The primary verb does not exist as an RPC.** "Run the saved request at path
   `Auth/Login`" is `store.ResolveRequest(ctx, parent, name)` followed by
   `invokeUnary(invokeSpec{…})` — verified in `service/workspace/gvinvoke.go:98-145`
   (`scriptInvoker`, with `splitInvokePath` at `:151`),
   which is exactly what `gv.invoke` does today. The public `Invoke` RPC takes
   `service`/`method`/`body` **from the editor** (that is its whole point: "a send
   never depends on a prior UpdateRequest landing first"). So a generated mirror of
   `Invoke` gives you a command that requires you to paste the body you were trying
   to avoid pasting. No generator can invent the verb that matters.
2. **Exit codes.** `InvokeResponse` carries the target's gRPC status *inside*
   `Request.Response.status` (see the `InvokeStreamResponse` comment: "A gRPC-status
   failure of the invoked call is reported in the terminal frame's status, NOT as a
   Connect stream error"). Every generated CLI maps transport success → exit 0.
   Turning "status 13" into "exit 1" is hand-written code, and it is the single most
   important line in the whole feature.
3. **A 16-RPC API is small.** Verified: 16 RPCs in `service.proto`. Twelve of them
   are workspace CRUD that a shell user will never type. The generated leverage is
   over a surface nobody wants.
4. **The mirror already exists for free** (`buf curl` + self-reflection), so the
   generated option's real contribution is Cobra help text — bought with 7 modules
   and an offline-build regression.
5. **The tooling asymmetry points one way.** For the MCP surface a *maintained*
   generator exists that consumes plain protos — `redpanda-data/protoc-gen-go-mcp`,
   which the reference implementation at `/Users/r/dev/core/gateways/admin/mcp/` uses
   and which [`mcp/README.md`](./mcp/README.md) adopts, alongside
   `modelcontextprotocol/go-sdk` — and **both are already in this machine's Go
   module cache** (verified: go-sdk v1.6.1, protoc-gen-go-mcp
   v0.0.0-20260430225748). For the CLI surface the analogous generator is deleted
   upstream, its replacement is unmaintained, and **its module is not in the
   cache**. Generate the surface that has a live generator; hand-write the one that
   doesn't.
6. **Churn direction is wrong.** VS Code phase 1 will **delete `workspace_name`
   from every RPC** ([`vscode/phase-1-collection-dir.md`](./vscode/phase-1-collection-dir.md)).
   A generated mirror re-emits 16 commands' flags on every such change; a
   hand-written CLI with 6 verbs touches 6 call sites. In a repo where "breaking any
   contract you like is perfectly fine", generated 1:1 surfaces are a *liability*,
   not an asset — they make the proto load-bearing for UX.

**Where the CLI lives: subcommands on the one binary.** `grpcview` with no
subcommand keeps serving the UI + API (today's behavior). This is forced, not
chosen: the single self-contained binary is a defining trait, the UI is 26 MB of
the 47 MB binary (verified `bazel-bin/service/cmd/cmd_/cmd`), and a second CLI
binary would be ~20 MB of duplicated Go. The MCP plan wants `grpcview mcp` on the
same binary for the same reason. **Both plans therefore share one prerequisite:
argv dispatch.** Today `service.Run` calls `flag.Parse()` on the global flag set
for `-port` (`service/service.go:34-36`), so the first thing either track does is
move argument parsing up into `main.go`. Whoever lands first owns it (§6, C0).

---

## 2b. Modeled on kubectl

kubectl is the design reference, chosen deliberately: it is the largest, most-used
API CLI in existence and it is **entirely hand-written** — `k8s.io/kubectl/pkg/cmd`
holds ~40 per-verb packages, and `k8s.io/code-generator` ships 11 generators of
which **none** emits a CLI (verified against both repos). Its genericity comes from
runtime discovery — the discovery API, RESTMapper, and OpenAPI — not from codegen.
That is the same shape as grpcview: a fixed verb set over a schema resolved at
runtime by reflection.

Four kubectl decisions to copy, each with its grpcview counterpart:

1. **Structured input comes from files and stdin, never from flags.** `PodSpec` has
   hundreds of fields and there is no `--containers-0-image`. So: `-f body.json`,
   `-f body.ts`, `-f -`, or a bare pipe — and **no per-field flags, ever**. This is
   the single most important rule here and it is also why `protoc-gen-cobra` was
   rejected in §4: a generated CLI's whole value proposition is the field flags
   kubectl refuses to have.
2. **Content decides, not the file suffix.** `kubectl -f` accepts YAML or JSON by
   looking at the bytes. grpcview needs even less: valid JSON is valid TS, so a body is
   either a module or an expression and one backend seam handles both — `-f body.ts`
   holding plain protojson works, and so does `-f body.json` holding a module.
3. **A `describe`/`explain` verb over the runtime schema.** `kubectl explain
   deployments.spec.replicas` walks the live OpenAPI document. grpcview's analogue is
   `grpcview describe <svc>/<method>` over the merged descriptor set — the same
   command the MCP plan needs as `describe_method` ([phase 3](./mcp/phase-3-gaps.md)),
   which means it is one implementation serving two surfaces.
4. **A few curated ergonomic verbs, not a mirror.** kubectl hand-maps only the
   hottest fields (`scale --replicas`, `set image`, `label`). grpcview's equivalent
   is `--param k=v` and `--target`; everything deeper goes in the body file.

One place grpcview must **diverge**: kubectl's verb set is fixed because Kubernetes
has a uniform REST verb grammar (`get`/`apply`/`delete` over any resource). gRPC has
no such grammar — every method is its own verb, so there is no `kubectl get` to
generalize. That asymmetry is precisely why a 1:1 generated mirror yields 16
unrelated commands sharing no shape, and why collapsing to `call` + `describe` +
`ls` is the better trade. The resource-shaped part of grpcview is the *collection*
(folders and requests), and that is what `ls`/`get` address.

Two kubectl conventions worth adopting for free, since they cost nothing and users
already know them: `-o json|yaml|jsonpath=…` for output shaping, and
`--dry-run` on `call` to print the resolved request (target, evaluated body,
evaluated metadata) without sending it. The latter is the CLI's answer to "what did
my generators actually produce", which today requires the UI.

---

## 3. Two bindings, one interface (why hand-writing is cheap here)

The generated Connect client interface and the handler are the *same shape* —
verified in the generated `service.connect.go`: `WorkspaceServiceClient` and
`WorkspaceServiceHandler` have identical method sets for all 15 unary RPCs, and
differ only in `InvokeStreaming` (`*connect.ServerStreamForClient[T]` vs
`*connect.ServerStream[T]` + `error`).

`workspace.New(ctx)` is a plain struct over `store.Store` + the scripting engine
(`service/workspace/workspace.go:42-58`) — **no server, no port, no HTTP**. So the
CLI can be written once against an interface satisfied two ways:

- **in-process** — `workspace.New(ctx)` directly; no server needed, which is the
  point for CI (`grpcview call X` in a container with nothing running);
- **remote** — `grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, addr)`
  against a running instance.

That is ~20 lines of adapter (only `InvokeStreaming` needs bridging) and **zero new
modules** — `connect`, `protojson` and stdlib `flag` are already in `go.mod`.

**The one hazard this raises** (and it is a real one, not a footnote): the store's
serialization is an in-process `sync.Mutex` per collection
(`service/store/store.go:108,149`) with **no file lock**. An in-process `grpcview
call` writes — it appends history — so running it while a UI server holds the same
directory is unsynchronized last-write-wins. Recommendation in §7.

---

## 4. Options

| Option | Input needs | New Go modules | Offline in this repo | Bazel effort | Maintenance risk | Verdict |
|---|---|---|---|---|---|---|
| A. gapic `protoc-gen-go_cli` | GAPIC surface + `google.api` annots + service yaml | ~15 (gapic tree) | **n/a — tool deleted upstream** | high | **terminal** | dead |
| B. `protoc-gen-cobra` | grpc-go service stubs | **7+** | **blocked** (module absent from cache) | medium | unmaintained since 2023-03 | no |
| C. Hand-written on Connect client | none | **0** | fine | trivial | ours | **yes** |
| D. `buf curl` | none (reflection) | **0** | **already installed** | none | buf's | yes, as the raw path |
| E. Runtime descriptor-driven CLI | comment-carrying descriptor set | 0 | fine | small genrule | ours (a generator by hand) | no — no co-sponsor (§4.5) |

### 4.1 Option A — gapic-generator-go `cmd/protoc-gen-go_cli` — **dead**

**Verified 2026-07-30, and this is the headline finding.** The plugin was removed
from `googleapis/gapic-generator-go`:

- `gh api repos/googleapis/gapic-generator-go/contents/cmd` → **`protoc-gen-go_gapic`
  only**. `internal/gencli` is likewise gone.
- The deleting commit: **`chore: remove gencli from gapic-generator-go (#1767)`,
  merged 2026-06-26**, −3809 lines across 32 files. PR body: *"This PR removes the
  mostly defunct gencli plugin from this repo, which has fallen into disuse and is
  not actively maintained. The main historical consumer of this has been the gapic
  showcase…"*
- Last release that still contains it: **v0.62.0 (2026-06-24)** — one month old and
  now a permanently frozen fork point.

Even before the deletion it was the wrong tool. Its own README (still readable at
the pre-removal ref) states: *"The generated CLIs depend on both a generated Go
gRPC library and a Go gapic generated by the neighboring protoc-gen-go_gapic
plugin."* So the input requirement is not "plain gRPC protos" — it is a **full
GAPIC client surface**: `google.api.default_host`, Google-Cloud API-design
conventions (paged list methods, LRO `google.longrunning.operation_info`),
optionally a gRPC ServiceConfig/service YAML. grpcview's proto has none of that and
should not grow it. Output shape was one Cobra binary using **cobra + viper +
pflag**, flags flattened per input field, `--from_file` for a whole JSON payload,
global `--address/--insecure/--token/--api_key`, `[ROOT]_[SERVICE]_[VALUE]` env
vars, and **no bidi streaming** ("Three of four RPC call types are supported").
Dependency weight: the gapic runtime tree (`cloud.google.com/go/iam`,
`longrunning`, `gax-go`, `genproto`, oauth2, …) — the heaviest option on the table,
pinned to a deleted plugin. **No further evaluation warranted.**

### 4.2 Option B — `NathanBaulch/protoc-gen-cobra`

Verified state: fork of `tetratelabs/protoc-gen-cobra` (itself a fork of
`fiorix/…`), 46 stars, **no releases**, last commit **2023-03-08**, last repo push
2024-02-15, `go 1.18`, pinned to grpc v1.53 / protobuf v1.28.1 / cobra v1.6.1 /
viper v1.15. Not archived, but three years without a commit while
protobuf-go, grpc-go and cobra all moved. It is a *well-built* generator — I read
`cobra.go` line by line — and if this repo already had cobra and grpc-go stubs it
would be a defensible choice. It does not.

**Input requirement (blocker 1).** Generated code calls `pb.NewBankClient(cc)` and
`client.RoundTrip(...)` over a `*grpc.ClientConn`. Verified: this repo generates
**no grpc-go stubs** — `go_proto_library` uses `@rules_go//proto:go_proto` +
`//tools:connect_go_proto_compiler`, and the output dir contains only
`*.pb.go` + `*.connect.go` with no `grpc.ClientConnInterface` anywhere. Adopting
it means adding `@rules_go//proto:go_grpc` to the compilers list: a third client
surface (Connect for the UI, gRPC for the CLI) for one service, plus grpc-go server
interfaces nobody implements.

**Offline feasibility (blocker 2) — this is the hard one.** The generated code
imports the generator module's *runtime* packages, so the module is a build-time
**and** link-time dependency: `client` → pflag, grpc/credentials, iocodec, naming;
`iocodec` → mitchellh/mapstructure, protojson, ptypes; `naming` → iancoleman/strcase;
generated file → spf13/cobra. Minimum new modules: **NathanBaulch/protoc-gen-cobra,
spf13/cobra, spf13/pflag, mitchellh/mapstructure, iancoleman/strcase,
inconshreveable/mousetrap** (+ `gopkg.in/yaml.v3` if the YAML codec is linked).
In *this* workspace adding a module means, in order: a `require` line in `go.mod`;
**`go.sum` hashes**; a `use_repo(go_deps, …)` entry in `MODULE.bazel`; a gazelle
run. There is no vendor directory and `MODULE.bazel.lock` records no `go_deps`
results (verified: 0 matches) — **`go.sum` is the pinning mechanism**, 79 lines
covering exactly today's graph. Writing those hashes is what `go mod tidy` does,
and `go mod tidy` is banned here *and* cannot fetch anything with `GOPROXY=off`.
The only offline escape is the local module cache, and I checked it:

| module | in `~/go/pkg/mod/cache/download`? |
|---|---|
| spf13/cobra | yes (v1.9.1, v1.10.2) |
| spf13/pflag | yes (v1.0.7, v1.0.10) |
| mitchellh/mapstructure | yes (v1.5.0) |
| iancoleman/strcase | yes (v0.3.0) |
| **spf13/viper** | **no** |
| **NathanBaulch/protoc-gen-cobra** | **no** |

So: **adding this generator offline is impossible.** It requires a deliberate,
one-off `GOPROXY`-on step to populate the cache and write `go.sum` — i.e. breaking
the invariant AGENTS.md states as a property of the repo ("Offline builds are
green; a command that wants the network is a bug in the command"). Worth naming
because the MCP plan will hit the same wall for its SDK (no MCP Go SDK is in the
cache either) — see §6 C0b.

**Output shape.** One `cobra.Command` per service, RPCs as subcommands; flags are
`LowerKebab` of the *Go* field path joined by spaces; env vars `UpperSnake` and
**on by default**; global `-s/--server-addr` (default `localhost:8080`), `--timeout
10s`, `--tls*`, `-f/--request-file` (`-` = stdin), `-i/--request-format`,
`-o/--response-format json|prettyjson|xml|prettyxml`. Verified from
`client/options.go` + `DefaultConfig`. Sketches for grpcview, derived from reading
`cobra.go`'s `walkField`/`flagFormat` against `service.proto` (**flag names
inferred from the code, not observed from a run — unverified end-to-end**):

```
# Invoke
grpcview workspace-service invoke \
  --workspace-name default --path Auth,Login --item-name Login \
  --service auth.v1.AuthService --method Login \
  --body 'export default (): RequestMessage => ({ tenant: "acme", user: uuid() })' \
  --metadata-fields 'authorization=["Bearer eyJ..."]' \
  --metadata-script 'export default (): Metadata => ({ ...gv.metadata.inherit() })' \
  --target-address 127.0.0.1:50051
      # no --target-tls exists. see below.

# UpdateRequest
grpcview workspace-service update-request \
  --workspace-name default --path Auth --item-name Login \
  --draft-body '…entire TS module…' --name Login2 \
  --update-middleware=true --middleware auth-header,retry \
  --update-target=true --target-address 127.0.0.1:50051

# AddDescriptorSource
grpcview workspace-service add-descriptor-source \
  --workspace-name default --reflection --reflection-address 127.0.0.1:50051
grpcview workspace-service add-descriptor-source \
  --workspace-name default --file-name buf_image.binpb \
  --descriptor-set "$(base64 < buf_image.binpb)"
```

How it handles this proto's awkward cases — all read out of `cobra.go`:

- **`google.protobuf.Struct metadata`** — `Struct` is not in `knownTypes`, is not a
  list and is not a map, so `walkField` **recurses into it**, hits its single
  `map<string, Value> fields`, and emits `flag.MapVar(…, ParseStringE,
  ParseMessageE[*structpb.Value], cfg.FlagNamer("Metadata Fields"), …)` → a flag
  literally named **`--metadata-fields`**, taking `key=<protojson value>`. The WKT's
  internal field name leaks into the UX; `ParseMessageE` is protojson so values
  work, but `--metadata-fields 'authorization=["Bearer x"]'` is a JSON-in-a-flag
  puzzle for what the UI calls "metadata".
- **TLS is unreachable.** `Server.TLS` is an **empty message**; `walkMessage`
  returns no code for it, and `walkField` emits a nested message's flags only
  `if len(subCode) > 0`. So `--target-address` exists and `--target-tls` **does
  not**, in both `Invoke.target` and `AddDescriptorSource.reflection`. You cannot
  add a TLS reflection source or invoke a TLS target from the generated CLI at all.
  Escape hatch: `-f request.json`. This is the clearest illustration of why a
  mechanical mirror of *someone else's* proto conventions is fragile.
- **Multi-line TypeScript as a flag value** — `body`, `draft_body`,
  `metadata_script`, `draft_metadata_script` become plain `string` flags. A
  multi-line TS module with quotes and `${}` in one shell argument is
  actively hostile; `-f request.json` moves the problem to escaping newlines inside
  a JSON string; the least-bad mitigation is the env-var path
  (`WORKSPACE_SERVICE_INVOKE_BODY`), which is on by default. A hand-written CLI
  writes `--body-file body.ts` instead and the problem disappears.
- **Set-flags** — `optional bool update_middleware` → `flag.BoolPointerVar` →
  `--update-middleware=true`. It *works*, and it is exactly the "mechanical, not
  good" surface the brief predicted: the user must know a proto3 presence
  workaround to clear a list.
- **Oneofs** — `bytes descriptor_set` → **base64** flag value; the message member
  `reflection` → a marker `--reflection` bool plus `--reflection-address`, wired by
  a post-set hook. Uploading a `buf` image via `$(base64 …)` is a 200 KB argv.
- **`repeated string path`** → `StringSliceVar` (CSV): request display names
  containing a comma need pflag quoting.
- **Its one real advantage doesn't work here.** The usage string for every flag is
  `cleanComments(fld.Comments.Leading)` — the proto field comment. Under this
  workspace's proto pipeline those comments do not exist by the time a Go plugin
  runs (§5), so the generated CLI would ship **`--body string` with no help text at
  all**, for all 16 commands. Fixable (one `.bazelrc` line), but worth noting that
  the feature being bought is the feature that is currently broken.
- **`InvokeStreaming`** — supported (server streams print one response per line;
  client streams concatenate JSON documents). Bidi is not, which is fine: grpcview's
  own RPC is server-streaming by design.

**Maintenance risk.** A 2023-vintage, release-less, single-maintainer fork chain
(`fiorix` → `tetratelabs` → `NathanBaulch`) sitting on the *build path* of the only
binary the project ships. Compare with what it buys: help text for 16 commands
nobody was going to type.

### 4.3 Option C — hand-written, task-shaped CLI — **recommended**

Zero new modules: stdlib `flag` (already the parsing style in `service.Run`),
`connect`, `protojson`. Cobra is *not* needed for six verbs, and choosing stdlib
`flag` keeps the offline invariant intact — the decision that makes this option
free rather than merely cheap.

```
grpcview                      serve the UI + API (default; today's behavior)
grpcview serve   [--port 10000] [--dir .]
grpcview call    <request-path> [--param k=v]... [--target host:port] [--tls]
                 [--metadata k=v]... [-o json|body|raw] [--timeout 30s]
grpcview ls      [<folder-path>]            list the collection tree
grpcview get     [-o json]                  the whole workspace snapshot
grpcview sources ls | add <addr>|<file.binpb> | refresh <id> | rm <id> | reorder <id>...
grpcview script  run <name|-> [--kind generator|middleware|scenario]
grpcview rpc     <Method> [-d '{json}'|-d @file|-d -]   raw 1:1 escape hatch
grpcview mcp                                (owned by the MCP plan)
```

```console
$ grpcview call Auth/Login --param tenant=acme
{"user":{"id":"u_91","email":"a@b.c"},"token":"eyJ..."}
$ echo $status
0
$ grpcview call Auth/Login --param tenant=nope
grpcview: Auth/Login: NOT_FOUND: tenant "nope" does not exist   (12ms)
$ echo $status
1
```

`call` is `ResolveRequest` + `invokeUnary` — the code `gv.invoke` already runs —
with the status mapped to an exit code and the response body on stdout. Nested
`--param` reaches the body as `gv.request.params`, giving CI the same
parameterization scripts already have.

Awkward cases, for symmetry with §4.2: **TS bodies** — never a flag; `call` uses
the *saved* body, and the raw escape hatch takes `-d @file`. **`Struct` metadata** —
`--metadata k=v` (repeatable), assembled into the Struct in Go; the WKT never
surfaces. **TLS** — `--tls`, one bool, because it is hand-written. **Set-flags** —
absent by construction: `sources reorder a b c` and `request middleware set …`
express intent, and the handler fills `update_*` in. **Streaming** — `call` on a
streaming request prints one JSON per line and exits on the terminal frame's status
(the existing `invokeUnary` guard rejects streaming targets, so `call` routes
streaming to `InvokeStreaming`; ~15 lines).

Costs, stated honestly: help text is hand-written, so it can drift from the proto
comments (mitigated in §5 by making the *comment* the source and the CLI's `Long`
text a copy that lives beside it, or by Option E's artifact); and every new RPC
that should be scriptable needs a hand-written verb (at ~1 per quarter, this is
noise).

### 4.4 Option D — `buf curl` (the raw path, already paid for)

Verified working *today* with no changes: `buf` 1.61.0 is on the `bazel_env` PATH,
and the server registers `grpcreflect.NewStaticReflector("grpcview.v1.WorkspaceService")`
so it reflects itself.

```console
$ buf curl --protocol connect -d '{"workspaceName":"default"}' \
    http://localhost:10000/grpcview.v1.WorkspaceService/Get
```

This *is* the 1:1 mirror, for free, with `-d @file`, `-d -`, streaming, headers,
`--schema` for when reflection is off. Keep it: document it in the README as the
raw path, and let `grpcview rpc` (C4) exist only as a convenience for people who
don't have `buf`. **Any generated-mirror option must beat this baseline, and none
does.**

### 4.5 Option E — runtime descriptor-driven CLI (the "generated help without a generator" hybrid)

Build the command tree at runtime from an embedded `FileDescriptorSet` +
`dynamicpb`, taking help text from `SourceCodeInfo`. Three verified facts make this
possible and bound its cost (see §5 for why they matter beyond this option):

- **Bazel's `proto_library` descriptor set has no comments by default.** Probed
  `bazel-bin/proto/grpcview/v1/grpcviewv1_proto-descriptor-set.proto.bin` (9,257 B)
  for `"returns the workspace snapshot"` → **0 matches**.
- **One flag fixes it.** `bazel build --protocopt=--include_source_info
  //proto/grpcview/v1:grpcviewv1_proto` → the same descriptor set becomes
  **36,983 B** and the probe **matches**. This is the cheapest route to the
  artifact: no new rule, no genrule, no toolchain. Caveat: the per-target set
  carries only that target's own files, so a set usable with `dynamicpb` needs the
  transitive deps too — either protobuf's `proto_descriptor_set` or `buf build`.
- **`buf build` is the alternative** and gives a transitive image directly: verified
  146,168 B with imports, **47,451 B with `--exclude-imports`**, comments present in
  both, using the `buf` 1.61.0 already on the `bazel_env` PATH.

**But writing the flag↔field-path mapper, the JSON assembler and the help renderer
is writing a generator by hand** — the very work Option B would have done — for a
16-RPC internal API.

**Rejected outright, and the artifact has no other sponsor.** I initially expected
the MCP server to need this embedded image and so to co-fund it. It does not:
[`mcp/README.md`](./mcp/README.md) gets tool descriptions from `protogen` at *build*
time (once §5.0's flag lands) and input schemas from `protoreflect` at *runtime*, and
its phase-3 `DescribeMethod` runs `protoprint` over the **workspace's** descriptor
set — the one the user's own sources produced — not over a build-time embed of
grpcview's own protos. So the only remaining consumer would be `grpcview rpc --help`,
which is not worth a genrule and 47 KB. **What survives from this option is the
probe, not the plan:** the discovery that the flag exists and what it fixes (§5.0).

---

## 5. The shared comment contract (CLI ↔ MCP ↔ devs)

**The convention itself is now owned by the MCP track.**
[`mcp/phase-2-comments.md`](./mcp/README.md) landed while this doc was being
written and contains a nine-rule house style plus a per-RPC rewrite table. **Adopt
it; do not fork it.** §5.1 records where I withdraw a competing proposal, §5.2 what
the CLI adds on top. §5.0 is a build-layer blocker that plan does not yet cover and
that silently defeats its phases 1 and 2.

### 5.0 Blocker: proto comments never reach any Go plugin here (verified)

The MCP plan's Fact 4 is that `gen.ToolForMethod` takes `meth.Comments.Leading` as
the entire tool description. **In this workspace that string is empty.** Verified
2026-07-30:

| probe | default build | with `--protocopt=--include_source_info` |
|---|---|---|
| `grpcviewv1_proto-descriptor-set.proto.bin` size | 9,257 B | **36,983 B** |
| `"returns the workspace snapshot"` in that set | 0 | **1** |
| `"copy of google.rpc.Status"` in `workspace.pb.go` | 0 | **1** |
| `"file_name identifies a descriptor_set upload"` in `service.pb.go` | 0 | **1** |
| `"returns the workspace snapshot"` in `service.connect.go` | 0 | **2** |
| the flag appears in the protoc action's command line (`bazel aquery`) | 0 | **1** |

So today `type DescriptorSource struct` in the generated Go has **no doc comment at
all** and `service.connect.go` documents `Get` as the synthesized `// Get calls
grpcview.v1.WorkspaceService.Get.` The Go path receives the `proto_library`
descriptor set, which carries no `SourceCodeInfo`, and a plugin can only emit what
it is given.

**The TypeScript path is different, and that is why this went unnoticed.** The
committed `proto/grpcview/v1/workspace_pb.d.ts` *does* carry the comments
("`Status is a copy of google.rpc.Status, this is a hack…`" is right there in the
JSDoc), so `ts_proto_library` feeds protoc-gen-es source info while
`go_proto_library` does not. (Verified by output; the mechanism — presumably
protoc-gen-es being run against sources rather than `--descriptor_set_in` — is
**unverified**.) Anyone checking "do our comments survive codegen?" by opening the
`.d.ts` gets a false green.

**Consequences:**

1. **Every comment-consuming Go generator emits empty text here** —
   `protoc-gen-cobra`'s flag usage strings (§4.2) and `protoc-gen-go-mcp`'s tool
   descriptions. A generated MCP server built today ships 15 tools with **blank
   descriptions**, and the failure is silent: it builds, it registers, the model just
   cannot tell the tools apart. `mcp/phase-2-comments.md` would then be a rewrite of
   strings that never leave the repo.
2. **The fix is one `.bazelrc` line** and it belongs to neither track: `common
   --protocopt=--include_source_info`. Costs: descriptor sets grow (~4× for this
   package), one proto-action cache invalidation, and it is global so
   `ts_proto_library` should be re-checked (it already has comments, so the expected
   outcome is "no change"). **Recommendation: land it standalone, before either
   track starts.**
3. **`mcp/phase-2-comments.md`'s verify step needs one more line:** after the
   rewrite, grep a rewritten sentence in the generated `service.connect.go`, not just
   in the `.d.ts` — the `.d.ts` was never the surface at risk.

### 5.1 Where I withdraw a competing proposal

I had drafted a three-tier convention with the `Name ` prefix kept (for Go doc
idiom) and a `Rationale:` marker that renderers would cut. **The MCP plan's
resolution is better on both points and I withdraw mine:**

- **Verb-first, no leading identifier** (their rule 1). Their argument wins: the
  least-equipped reader is a model that never sees the Go identifier, and in the
  generated Connect interface the method name is on the next line anyway. It
  deliberately breaks godoc convention for RPC comments only, keeping `Name does X`
  for messages and fields — which is exactly the right seam, because those never
  reach MCP. A `stripSymbol` helper is then unnecessary: strictly less machinery
  than my version.
- **Rationale leaves the proto entirely** (their rule 5 + "What moves out of the
  proto"), rather than living behind a marker both renderers must cut. Also strictly
  less machinery, and it puts the design argument where this repo already keeps
  design arguments — `AGENTS.md` and `docs/design/`.

What survives from my side is the audit agreement: `// Get returns the workspace
snapshot` (a stub that hides that `Get` returns tree + sources + services + scripts
*and creates the collection if absent*) and `InvokeStreaming`'s design archaeology
plus its unfollowable "see `InvokeStreamRequest`" are the two clearest offenders,
and both plans independently reached that conclusion.

### 5.2 What the CLI adds to the convention

The MCP plan's rule set is written for one consumer that reads RPC comments only.
Two additions, neither in conflict:

1. **Field comments are the CLI's flag help, so they are not "developer-only".**
   The MCP plan's rule — *"a field comment is developer-only; anything an agent must
   know has to be restated in the RPC comment"* — is right about MCP and slightly
   over-stated about the field comment's audience: it also lands in
   `--<flag>`'s usage string (for any generated CLI) and in a hand-written `--help`.
   **The CLI's ask: the field comment's *first sentence* must stand alone as a
   one-liner**, with the prose after it. `UpdateRequestRequest.update_middleware`
   already satisfies this by accident ("middleware patches the request's
   attached-middleware list" reads fine as a one-liner; the proto3-presence
   explanation follows) — proof the rule fits the existing style rather than
   fighting it.
2. **The RPC comment's line 1 must survive truncation to one terminal line.** Their
   rule 2 (self-contained, < ~100 chars) already delivers this; it is worth stating
   as a *CLI* requirement so nobody relaxes it later on MCP-only grounds — MCP
   tolerates a 700-character description, a `--help` listing does not.

**One renderer, one place.** Whatever slicing both surfaces need (first sentence,
first paragraph) belongs in one small Go package that both import. Two
implementations of "first sentence" diverge within a month and then the proto has
two incompatible readers.

---

## 6. Phased plan (Option C)

House rules: every phase ends green on `bazel build //service/cmd` and
`bazel test //...`, and is exercised against the real binary with an isolated
`HOME` (see the project memory note on verification). Bare `go` commands are never
used.

**Interlock.** C0 is shared with the MCP track, which needs the same argv dispatch
for `grpcview mcp` ([`mcp/README.md`](./mcp/README.md) Decision 4). Whoever gets
there first lands C0 and the other builds on it — two implementations of
`switch os.Args[1]` is the one outcome to avoid. C0b is dropped in favour of
`mcp/phase-0-dependencies.md`.

### C0 — spike: argv dispatch + the two bindings (½ day, throwaway-able)

**Goal.** Prove the two structural assumptions before writing any verb: (1) the one
binary can dispatch subcommands without disturbing today's zero-arg behavior;
(2) one interface is satisfiable both in-process and over Connect.

**Files.** `.bazelrc`, `service/cmd/main.go`, `service/service.go` (move
`flag.Parse` out), new `service/cli/cli.go` + `service/cli/BUILD.bazel`.

**Steps.**
0. Land `common --protocopt=--include_source_info` in `.bazelrc` (§5.0) and confirm
   `bazel build //...` + `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy`
   stay green. Trivial, unblocks both this track and MCP, and is the one change here
   that is worth doing even if the CLI is never built.
1. `main.go`: `switch os.Args[1]` → `serve` (default, incl. no args) | `version`.
   Keep `--port`/`--dir` on `serve` via a `flag.NewFlagSet`, and delete the global
   `flag.Parse()` from `service.Run` (it must not own argv once subcommands exist).
2. Declare `type client interface { … }` with two RPCs only (`Get`, `Invoke`) and
   satisfy it twice: `workspace.Workspace` directly, and
   `grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, addr)`. Confirm both
   typecheck with no adapter for unary methods.
3. Add the new `.go` files to `BUILD.bazel` `srcs` **by hand and verify** — a known
   trap here is a new file that compiles locally but was never registered.

**Verify.** `bazel build //service/cmd`; run the binary with no args (UI still
serves on :10000, per the isolated-`HOME` recipe); `grpcview version`;
`grpcview serve --port 10111`; binary size unchanged ±0.1 MB.

**Out of scope.** Any real verb, output formatting, MCP.

### C0b — the offline module-add drill — **superseded**

Drop this phase. [`mcp/phase-0-dependencies.md`](./mcp/README.md) already owns
"get new Go modules into an offline Bazel workspace" as a real prerequisite for a
track that actually needs three of them, and its README states the network caveat
honestly ("everything needed is in *this* machine's Go module cache… that is a
machine-local fact, not a repo-portable one"). The CLI needs **zero** new modules,
so it should consume that phase's findings, not duplicate the experiment. The one
thing worth asking of it: have it record the recipe in `AGENTS.md` rather than only
in the phase doc, since the phase doc gets deleted when the work lands.

### C1 — `grpcview call <path>` (the reason this feature exists)

**Prereqs.** C0. Sequence **after** VS Code phase 1 if possible (collection = cwd)
so `call` needs no `--workspace`; otherwise carry `--dir`.

**Goal.** Run a saved request by display-name path, from a script, with a
meaningful exit code. `call` takes **two argument forms**, discriminated by whether
the argument resolves to a saved request:

```
grpcview call Auth/Login                      # saved request, body from the store
grpcview call user.v1.UserService/GetUser -f body.json
cat body.json | grpcview call user.v1.UserService/GetUser
```

The second form is the kubectl-shaped one and it exists only because
[the body contract](./request-body-contract.md) accepts plain protojson — the caller
pipes the object they already have. Resolution order is saved-request first, then
`service/method`; a `service/method` that also names a saved request is ambiguous and
must error rather than guess.

**Files.** `service/cli/call.go`; `service/workspace/` (expose the
`ResolveRequest` + `invokeUnary` pair to the CLI, or add one thin
`InvokeSaved` RPC — decision in §7); `proto/grpcview/v1/service.proto` only if the
RPC route is chosen.

**Steps.**
1. Path parsing identical to `gv.invoke`'s (`splitInvokePath`): split on `/` into
   parent path + item name. Reuse it; do not write a second parser.
2. `--param k=v` (repeatable, `v` parsed as JSON then falling back to string) →
   `invokeSpec.params`, so the saved body sees `gv.request.params`.
3. `--target host:port`, `--tls`, `--metadata k=v` (repeatable; also
   `--metadata-file` taking the same two forms as a body), `--timeout`.
3b. **`-f <file>|-` and bare stdin** supply the body. Read the bytes and pass them
   through unchanged — the CLI does **not** parse, wrap, or reformat them; the wrap is
   the backend's job at one seam, so `-f` works identically for protojson and for a TS
   module (they are the same form as far as the CLI is concerned — valid JSON is valid
   TS). `-f` on a saved request *overrides* the stored body. Never add per-field flags
   (§2b rule 1).
3c. **`--dry-run`** prints the resolved target, the evaluated body and the evaluated
   metadata, then exits 0 without sending. This is the CLI's answer to "what did my
   generators actually produce", which today needs the UI.
4. Output: `-o body` (default; the response message as JSON, one line) /
   `-o json` (the whole `Request.Response`, for `jq`) / `-o raw`. Diagnostics and
   latency to **stderr**, never stdout.
5. Exit code: `0` on `status.code == 0`, `1` on a gRPC-status failure (message +
   code to stderr), `2` on grpcview's own failure (unknown path, no target,
   un-evaluable body). Streaming requests: one JSON per line, exit from the terminal
   frame.

**Verify.** `bazel test //service/...`; then, against the real binary with an
isolated `HOME` plus `//service/echo/cmd -port 50055`: create a request in the UI,
`grpcview call Echo/Unary`, confirm stdout is JSON-only and `$status` is 0; break
the target and confirm exit 1; `grpcview call Echo/ServerStream` prints N lines.

**Out of scope.** Any write verb; assertions/expectations (a scenario feature).

### C2 — read verbs: `ls`, `get`, `sources ls`

**Goal.** Make a collection legible from a shell (and to an agent that has a shell
but no MCP).

**Files.** `service/cli/{ls,get,sources}.go`.

**Steps.** `ls` prints the tree with request paths one per line (grep-able,
`call`-pasteable) and `--json` for structure; `get` is the raw snapshot;
`sources ls` prints id / kind / priority / `won_service_names` count / error —
i.e. the Sources view as text, including a shadowed source being visibly shadowed.

**Verify.** `bazel test //service/...`; eyeball against the UI's Sources view for
the same workspace; a source with a resolve error shows its reason.

**Out of scope.** Colors, TTY detection, pagers.

### C2b — `grpcview describe <svc>/<method>` (the `kubectl explain` analogue)

**Goal.** Print a method's input and output message shape from the merged descriptor
set, so a shell user (or an agent with a shell) can write a body without opening the
UI. Without this, the ad-hoc `call … -f` form from C1 requires you to already know the
field names, which is the same gap [`mcp/phase-3-gaps.md`](./mcp/phase-3-gaps.md)
found for the MCP surface.

**Files.** `service/cli/describe.go`, plus whatever shared helper the MCP track's
`describe_method` uses — **one implementation, two surfaces.** Coordinate: whoever
lands first writes the helper in `service/workspace/`, and the other calls it.

**Steps.** Resolve the method through the same descriptor path the UI uses; render
with `protoprint` (already a dependency, already offline). Default output is the
`.proto` text of the input message plus its transitively referenced types;
`-o json` emits a field list for machine consumption. Include the field's proto
comments when the descriptor carries `source_code_info` (an upload does, reflection
does not — the [definition-sources](../../AGENTS.md) precedence rule decides which,
and `describe` should say which source it read so an empty-comment result is
explicable rather than mysterious).

**Verify.** `bazel test //service/...`; `grpcview describe echo.v1.EchoService/Unary`
against the echo server; confirm comments appear for a `buf build` upload source and
are absent for a reflection-only source.

**Out of scope.** Rendering a *filled-in* example body (guessing values); `explain`-style
dotted-path drilling — add only if the flat output proves unwieldy.

### C3 — write verbs (only the ones with a scripting story)

**Goal.** Cover the automatable mutations, no more: `sources add|refresh|rm|reorder`,
`request create|rm|mv`, `script run`.

**Non-goal, explicitly.** `--draft-body` / `--draft-metadata-script` as *inline string*
flags. Authoring TypeScript is an editor's job (that is the VS Code track). But
`request create|update` should accept `-f` for the body, same as `call` — once bodies
are files ([VS Code phase 2](./vscode/phase-2-body-files.md)) and plain protojson is a
valid body, "seed this request from a JSON file" is an obvious scripting need and costs
nothing beyond reusing C1's reader. Per-field flags for message contents remain out,
permanently (§2b rule 1).

**Verify.** `bazel test //service/...`; run each verb against an isolated-`HOME`
store and confirm the UI (already reloading per RPC) shows the result; confirm
`sources add` of an unreachable target lists the source with the error rather than
failing.

### C4 — raw escape hatch `grpcview rpc <Method> -d …`

**Goal.** Close the 1:1 gap without a generator: `protojson` in, `protojson` out,
over the generated Connect client. ~40 lines, one `switch` over 16 method names (or
`protoregistry` + `dynamicpb` if that switch ever annoys anyone).

**Steps.** `-d '{json}' | -d @file | -d -`; unknown method → list the 16;
`--help` prints the method's proto comment **if** the MCP track has landed the
comment-carrying descriptor artifact (§4.5), else just the method list. Document
`buf curl` alongside it as the zero-install alternative.

**Verify.** `grpcview rpc Get -d '{"workspaceName":"default"}'` output matches
`buf curl` byte for byte on the same store.

---

## 7. Non-goals, risks, open questions

**Non-goals.** A second binary. grpc-go service stubs. Cobra/viper (stdlib `flag`
is sufficient for 6–10 verbs, and it is what the codebase already uses). A
generated 1:1 mirror in any form — `buf curl` owns that. A CLI for authoring
TypeScript. An interactive TUI. `google.api` / GAPIC annotations on grpcview's
proto: nothing in the four-surface plan needs them, and adopting them to satisfy a
generator that no longer exists would be pure loss.

**Risks.**

1. **Cross-process store writes have no lock.** `Collection.mu` is an in-process
   `sync.Mutex` (`store.go:149`) and there is no file lock, so an in-process
   `grpcview call` (which appends history — a write) racing a running UI server on
   the same directory is last-write-wins per file. *Resolution:* **run in-process by
   default and accept it**, matching [`mcp/README.md`](./mcp/README.md) Decision 4
   ("the shared state is the *directory*, not the process") and the exposure the VS
   Code track already accepts for external editors. Three surfaces sharing one
   concurrency model is worth more than the CLI being individually safer. `--server
   <addr>` stays available as an opt-in for the interactive case and for risk 4's
   loop; if a real lost update ever bites, the fix is one advisory lock in the store
   that benefits all three, not per-surface cleverness. *(This reverses an earlier
   draft of this doc that recommended dial-when-reachable; the MCP plan's argument is
   better, and divergence here would be its own bug.)*
2. **Proto churn from VS Code phase 1.** Deleting `workspace_name` from all 16 RPCs
   touches every CLI call site. Cheap for 6 verbs, which is part of why C is
   recommended — but it argues for sequencing C1 *after* phase 1, or for accepting
   one mechanical follow-up commit.
3. **Two owners of argv.** CLI and MCP both need subcommand dispatch and both want
   to land first. C0 is deliberately tiny so either track can own it; the other
   must not fork it.
4. **Scripting-engine startup cost in-process.** `workspace.New` compiles the
   QuickJS module (~660 KiB) on every CLI invocation. Fine for a one-shot `call`;
   a loop of 100 `grpcview call`s pays it 100 times. If that shows up, `call`
   should dial (see risk 1) or the engine should be lazily compiled.
5. **Hand-written help drifts from proto comments.** Accepted, and note that the
   drift is *smaller* than it looks: the CLI's verbs are task-shaped, so most of its
   help text has no proto counterpart to drift from. Where a verb does mirror an RPC,
   §5's convention keeps the two readable side by side. The alternative (a generator)
   costs more than the drift.

**Open questions (need a decision).**

- **`InvokeSaved` — the one proto change worth proposing, and it is cross-plan.**
  `invokeSpec.params` exists but the public `Invoke` RPC has no `params` field —
  today only `gv.invoke` can set it. Add `params` to `InvokeRequest`, **or** add a
  small `InvokeSaved` RPC taking `{path, item_name, params}` that does the
  `ResolveRequest` + `invokeUnary` server-side? **Recommendation: add `InvokeSaved`.**
  It makes `call` a three-line command, and it is a *direct partial fix for
  [`mcp/README.md`](./mcp/README.md) Decision 6's hard gap*: their step 3 fails
  because an agent cannot learn an input message's fields and so cannot write a body.
  With `InvokeSaved` (tool: `call_request`) an agent can run a request a human already
  authored — body, metadata script, middleware and folder inheritance included —
  **without knowing any field names at all**, which is the highest-value agent action
  in the product and needs no schema layer. Their `DescribeMethod` (phase 3) is still
  needed for authoring *new* requests; these two are complements, not alternatives.
  Both plans should land this once.
- **Verb naming should match the MCP tool names.** Their rename map is
  `get_workspace` / `add_source` / `invoke` / `run_script`; the CLI's `get` / `sources
  add` / `rpc Invoke` / `script run` should read as the same vocabulary in a different
  grammar. Worth 10 minutes of alignment before C2, and worth deciding whether the
  CLI's saved-request verb and MCP's tool share a name (`call` vs `call_request`).
- **`grpcview describe <service/method>`** — the MCP plan's phase 3 `DescribeMethod`
  RPC explicitly lists the CLI as a consumer. Fold it into C2 when it lands rather
  than inventing a second shape.
- **Who lands `--protocopt=--include_source_info`** (§5.0)? It is one `.bazelrc`
  line, it is a prerequisite for MCP's tool descriptions and for any comment-derived
  help, and it should not wait for either plan to start. **Recommendation: land it
  standalone, now, independent of both tracks.**
- **Does `grpcview rpc --help` need per-method text at all**, or is listing the 16
  method names enough? With §4.5's embedded image rejected, the honest answer is
  "names are enough, and `buf curl --schema` covers the rest" — but confirm before C4
  rather than growing an artifact for it later.
- **Does anything about the CLI need to be true before VS Code phase 1**, or can
  the two proceed independently after C0?
