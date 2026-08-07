# One daemon per workspace

**Status:** Planning only. **Not started.** Depends only on sub-phase 1a of
[`phase-1-workspace.md`](../shipped/vscode/phase-1-workspace.md) — it needs a workspace root
to key on and nothing else. Research on the discovery mechanism is closed:
[`server-discovery-and-naming.md`](../research/server-discovery-and-naming.md).

This was Decision 10 of phase 1. It is its own document because it is its own concern: the
daemon serves the CLI, the browser and the VS Code extension equally, and phase 1 is about
addressing, not lifecycle. Two things phase 1 keeps rather than defers: the **loopback bind
and the CORS narrowing** (a live defect, phase 1 Decision 12), and the **no-lock wart**,
which this document is what actually fixes.

## Goal

Copy bazel's client/server model whole, not just its port file: **the CLI connects to the
workspace's server if one is running, starts one if not, and the server exits after a few
hours of inactivity.** The port file is the rendezvous; the lifecycle is the actual design.

This is what makes "one workspace, one writer" true rather than aspirational — every
surface (CLI, browser, extension, MCP) ends up talking to the same process, so the store's
in-process `Collection.mu` is once again the *only* serialization anyone needs.

Second payoff, which phase 1's in-memory descriptor model makes larger than it looks: the
linked-descriptor cache (phase 1, Decision 9) is **per process**, so a daemon keeps it warm
between CLI invocations. Today every in-process `grpcview invoke` re-links descriptors from
disk *and* recompiles the QuickJS engine in `workspace.New`. Warm, an invoke is a map
lookup plus a dial.

Third payoff, and the one with a correctness bug already paid for it: **the collection
listing**. `store.List` rescans the workspace on every call (~130ms on a 5k-directory
monorepo) because it has no way to know the tree is unchanged. It used to memoize to
`collections.json` keyed on the workspace root's own mtime — which a collection created
*below* the root never changes, so a hand-written `grpcview.json` or one arriving on a `git
checkout` was invisible until something unrelated touched the root. That cache was removed
rather than patched: no cheap fingerprint of "the set of `grpcview.json` files" exists,
since computing one is the scan itself, and an explicit `InvalidateList()` cannot cover a
writer that is not grpcview. A daemon can do what a one-shot process cannot — **hold the
listing in memory and invalidate it from filesystem events** (fsevents on macOS, inotify on
Linux), watching the same tree the scan walks with the same prune rules. That is both
faster than the memo ever was and correct for writers grpcview never sees. The same watcher
is what `grpcview.json` edits made outside the app want anyway.

## The rendezvous

One server per workspace means several at once, and a fixed `10000`
(`service/cli/root.go:18`) then collides. Bazel solves the same problem the same way, and
it is worth copying rather than inventing: the server writes down where it is, and the
client reads it.

- **A registration file per workspace root, in machine-local user state.** Not inside the
  workspace — a read-only or network-mounted checkout breaks it, and a file left by
  *another machine* whose pid happens to be alive locally is worse than no file. Not bare
  `/tmp` either: it is mode 1777 and shared between users. `os.UserCacheDir()/grpcview/
  servers/<sha256 of the absolute workspace root>.json`, `0600` inside a `0700` directory
  — which is the shape of bazel's output base (per-user root, keyed by a hash of the
  workspace path). This is the one thing that belongs in `UserCacheDir()` rather than the
  durable state root phase 1 Decision 6 introduces: a registration is genuinely disposable,
  and losing it costs one restart.
- **Contents:** port, pid, the absolute workspace root, and the executable's identity
  (path + mtime + size, or its hash — see version skew below).
- **The file is a hint, never an authority.** Check pid alive → connect → verify the server
  reports *the same workspace root*. Pid reuse and hash collisions both die at that check.
  Anything that fails it is stale: unlink and fall back.
- **Never signal a pid the connect has not vouched for.** Bazel's macOS liveness check is a
  bare `killpg(pid, 0)` carrying a source TODO admitting it "might accidentally kill an
  unrelated process if the server died and the PID got reused" — a defence it only has on
  Linux. macOS is our primary platform, so invert the order instead of copying the gap:
  `grpcview shutdown` and the version-skew restart ask the server to exit **over the wire**,
  after identity is verified, and a signal is the last resort for a process that answered and
  then refused to leave. That is strictly better than a sysctl-based pid check, because the
  connect already proves what the pid cannot.
- **Bind first, publish second.** A workspace server binds `:0` by default — no preferred
  port, no fallback walk, nothing that makes the outcome depend on start order — and
  `--port <n>` pins one when a caller needs it. Read the real port back off `l.Addr()`, then
  write the file. Remove the file on shutdown; crash-left files are covered by the liveness
  check.

## Two RPCs this needs, and one restructure

The design above depends on surface that does not exist. Both are additions to
`service.proto`, to the `Client` interface (`service/cli/client.go:16`), and to the
in-process binding, which must either implement them or reject them explicitly.

- **`ServerInfo`** → the absolute workspace root, the pid, and the executable identity.
  This is what makes "verify the server reports the same workspace root" possible; without
  it a client has no way to tell a live server for *this* workspace from a live server for
  another. It also answers `--server <addr>`: today a collection id resolved against *your*
  root can be silently interpreted against a different root's, because nothing lets the
  client check.
- **`Shutdown`** → graceful exit. Needed by `grpcview shutdown` and by the version-skew
  restart, and it is what lets the design avoid signalling a pid.

**`service.Run` has no shutdown path at all today.** It calls `http.ListenAndServe`
(`service/service.go:83`) with no `*http.Server` to `Shutdown`, and never observes the `ctx`
it was handed. Bind-then-publish (`net.Listen` + `http.Serve`), draining in-flight requests,
and an idle timer are a restructure of that function, not a flag on it. Phase 5's
"requires a backend change" note only mentions reporting the chosen port; this is the rest
of it.

## Connect, or start one

**The CLI's binding rule changes — and it is a safety improvement, not a regression.**
`AGENTS.md` records that "dial the local server if one happens to be listening" was
deliberately rejected, so that *which process wrote my history* never depends on ambient
state. That was written when there was nothing to identify a server *by*: any listener on
10000 might belong to a different workspace. A registration keyed by workspace root and
verified after connect removes the ambiguity — and starting one when none exists removes
the *conditional* the original rule was actually objecting to. The answer stops being
"wire if a server happens to be up, local otherwise" and becomes **always the wire, to a
server that is always this workspace's**. New rule:

1. `--server <addr>` pins an explicit server.
2. `--in-process` does the work in the CLI process and starts nothing (bazel's `--batch`).
   The escape hatch for CI, for a read-only checkout, and for debugging.
3. Otherwise: take the lock, read the registration, verify it, and **dial it or spawn it**.

- **The startup race needs a lock — a *rendezvous* lock, not a command lock.** Two CLI
  invocations that both find no registration must not both spawn, so take an advisory file
  lock (`flock`, via `golang.org/x/sys` — present in the module graph but `// indirect`
  (`go.mod:25`), so the require block moves and gazelle runs) around *check → spawn → wait →
  connect*, and **release it once connected**. Bazel's is the same shape: taken before
  probing, dropped once the first request is in flight. Holding it for the command's duration
  instead would serialize every concurrent invocation, which is a performance bug wearing a
  correctness costume. With this in place, "two processes can write one collection directory
  without a lock" (`AGENTS.md`, "The CLI") stops being an accepted risk on the default path.
- **Spawning is a detached self-exec:** `os/exec` on `os.Executable()` with
  `serve --workspace <abs root> --idle-timeout <d>`, a new session (`Setsid`), stdio to
  `<cache>/grpcview/servers/<hash>.log`, then `Process.Release()`.
- **A server that fails to start must not look like a hang.** Poll for the registration and a
  successful connect, bounded (~10s) — but **also hand the child one end of a socketpair and
  watch for EOF**, which is bazel's trick: the pipe closes when the process dies, so a crashed
  daemon is detected in milliseconds instead of at the timeout, and the failure path dumps the
  tail of that log to stderr and exits 2. Startup failure is the single most likely way this
  feature turns into a bad day; both halves are cheap.
- **`cwd` never crosses the wire, and must not.** Phase 1 Decision 11's cwd-based collection
  resolution is a *client* concern: the CLI resolves the collection to an id and sends the
  id, so the daemon — whose cwd is whatever shell first spawned it — has no say. Spawn with
  the absolute root and never let the server resolve a relative path.
- **`grpcview url` and `grpcview open`** read the registration; `grpcview shutdown` stops
  the workspace's server (bazel has all three). With `:0` you can no longer guess the URL,
  so the tool has to answer the question.

## Dying quietly

- **`--idle-timeout`, defaulting to bazel's 3 hours.** Reset by activity, and *only armed
  when nothing is in flight* — a counter, not a timestamp. A server-streaming invoke that
  runs past the deadline must not be killed mid-stream, which a naive last-request-time
  timer would do.
- **Only an auto-spawned server idles out.** A hand-run `grpcview` (someone's UI is open)
  keeps running until they stop it: an explicit invocation gets an explicit lifetime. The
  spawning client passes the flag; nobody else does.
- **Shutdown is graceful and unlinks its own registration:** stop accepting, drain
  in-flight, unlink, exit.
- **The close race is the client's to absorb.** A client can read a registration
  microseconds before the server unlinks it, then get `ECONNREFUSED`. That is not an error:
  treat it as staleness — unlink and retry the whole lock/spawn sequence **once**, then fail
  for real. Without that single retry this design produces a rare, unreproducible failure
  at exactly the 3-hour mark.
- **Version skew restarts the server — but not by *version*.** `service/cli/root.go:16`
  links `version = "dev"` for every unstamped build (and *empty* for an untagged `--stamp`
  one), so a version-string compare would miss exactly the case that matters: the rebuild you
  just did. Put the **executable's identity** in the registration — `os.Executable()` plus its
  mtime and size, or its hash — and restart on any change. Bazel reaches the same conclusion
  by a different route, keying its server on an `install` symlink holding `md5(binary)`. A
  daemon serving last hour's code is the expensive kind of trap: everything *works*, just not
  as written.
- **The in-memory descriptor cache needs a bound.** Phase 1 Decision 9 makes it the
  authority for every read, keyed by collection id with no eviction. One linked descriptor
  set per collection ever served, held for three hours, is a slow leak in a monorepo. Bound
  it or make it an LRU — this is where that stops being theoretical.

## What a daemon must not break

- **The dev flow is the one caller that pins a port.** `ui/src/lib/client.ts:4` hardcodes
  `http://127.0.0.1:10000` whenever `import.meta.env.PROD` is false, so `//service/cmd/dev`
  passes `--port 10000` and stays out of the registry path entirely. Production is unaffected:
  the UI is served from the same origin it talks to (`window.location.origin`).
- **Origin churn costs nothing today, and that is worth keeping.** `ui/src` uses no
  `localStorage`, no `sessionStorage`, and no zustand `persist`, so a different port every
  run loses no user state. The day the UI persists anything per-origin is the day a random
  port starts silently discarding it.
- **The daemon's environment is frozen at spawn, and one thing already depends on it.**
  A long-lived server inherits the env of whichever shell first started it, so
  `exec.Command("bazel", …)` for a bazel source resolves `PATH` as that shell had it —
  `bazel` working in your terminal but not in grpcview is a confusing, correct-looking
  failure. Report the resolved binary and the reason on a lookup failure, and let
  `bazel.command` in the manifest take an absolute path. Bazel's own answer is to send the
  client's env with every command; we do not need that yet, because nothing else reads the
  environment: there is no `os.Environ`, `os.Getenv`, or `process.env` anywhere in
  `service/scripting/`. That is worth re-checking before adding one, since a daemon turns
  "reads the environment" into "reads a *stale* environment".

## Opening the browser, and the static URL we are not building yet

**The port is random by default; `--port <n>` pins one.** Nothing in the design depends on a
well-known port, and the flag covers every case that wants predictability — including the dev
flow, which just becomes `--port 10000` on `//service/cmd/dev` rather than an exception to the
rule.

**A random port means the tool has to open the browser**, since the user can no longer guess
the URL:

- **`grpcview` and `grpcview serve` open a browser on launch**; `grpcview open` does the
  connect-or-spawn and opens one against a server that may already be running. `grpcview url`
  prints the URL to **stdout** so it stays scriptable (`open "$(grpcview url)"`); `open` names
  what it launched on stderr, since the launch is the action and the URL is not its output.
- **An auto-spawned server never opens anything.** `grpcview invoke` that happens to start a
  daemon must not pop a browser tab. This is the same explicit-vs-auto split that governs the
  idle timeout, and it wants to be one predicate, not two: an explicitly launched server opens
  a browser and lives until stopped; an auto-spawned one is silent and idles out.
- **`--no-open`**, and degrade rather than fail: no `DISPLAY`, a headless box, or an SSH
  session means print the URL and carry on. Launching needs no dependency — `open` and
  `xdg-open` behind a `GOOS` switch is smaller than importing a browser package. (No Windows;
  the repo is shedding it.)

**Deferred: a URL that survives a restart.** A pinned tab still breaks when the port changes,
and that cannot be fixed without *something* on a fixed port: a URL's port is syntactic, no
name can supply one (see below), and omitting it means 80/443, which need root. Three options
were weighed — a dedicated redirector process on a well-known port (`/w/<name>` → `302`, able
to spawn a server that is not running); the same role riding on whichever server first binds
that port; or no fixed port at all. **We took the third for now.** Revisit only if the pinned
tab turns out to matter, and note that the VS Code extension becoming the primary UI would
retire the question rather than answer it — the extension reads the registration file and never
needs a URL a human can type. If it is ever revisited, the security work is the part to not
underestimate: a fixed-port listener that can spawn is reachable by cross-origin GETs from any
page you visit, so it needs a `Host` check (DNS rebinding), no spawning on GET, and spawning
restricted to already-registered workspaces.

**MCP is exempt, and it is worth saying why** — it looks like the same problem and is not. An
MCP server is launched by its client over stdio, so it has no port to publish and nothing to
discover about itself. What it does need is to be a *client* of the workspace daemon — same
registry, same connect-or-spawn — so that an agent's writes and the UI's writes go through one
process. [`mcp/phase-1-server.md`](./mcp/phase-1-server.md) planned the opposite ("no new
flags", the collection is `os.UserConfigDir()`); that is the correction this hands it.

## No token, and what that leaves

Considered and **dropped for now.** The idea was for the server to mint a random token at bind
time, write it into the `0600` registration file, and reject requests that do not echo it — so
that a TCP connection, which has no peer identity, carries a filesystem permission check
instead.

Why it is not worth building yet:

- **The browser cannot read a file.** The UI is served by the same process it talks to, so the
  server would have to inject the token into the HTML it already writes by hand
  (`service/service.go:65-69`). That means handing the token to anything that can `GET /`, so
  it is hygiene rather than a boundary.
- **What it actually buys is narrow:** rejecting *other local accounts* on a shared machine,
  and a seatbelt if the loopback bind ever regresses. On a single-user laptop, nothing.
- **It does not defeat the attack usually cited for it.** With the wildcard CORS fixed, a
  cross-origin page cannot read any response, token or not. And DNS rebinding, which does
  defeat origin policy, defeats the token too — the page becomes same-origin, fetches `/`, and
  reads the token out of the HTML. The fix for that is a `Host` header check, not a bearer
  token.

So the boundary is **loopback + origin policy**, delivered by phase 1 Decision 12, plus a
`Host` check here. Say that plainly rather than implying more. If a token does land later, the
order is: interceptor → CLI and extension send it → browser injection, with the same-origin
caveat written down.

The properly-scoped version of this problem is a **unix socket** for the CLI: mode bits are
OS-enforced, `LOCAL_PEERCRED`/`SO_PEERCRED` gives the connecting process's uid unforgeably,
there is nothing on disk to leak into an HTML page, and the socket path is derivable from the
workspace hash. The browser cannot speak to one, so the TCP listener exists either way and a
socket is a *second* transport serving only the CLI — see the open question below.

## Why not a registered name

Researched separately and **closed**:
[`server-discovery-and-naming.md`](../research/server-discovery-and-naming.md).
Registering a name at startup (mDNS/DNS-SD, `.local`, a loopback alias) does not replace
the registration file, for three reasons that compound:

1. **No browser learns a port from a name.** The WHATWG URL parser takes the port
   syntactically, with no DNS step, and SRV-record support is `RESOLVED WONTFIX` in Firefox;
   Chromium's own `net/dns/README.md` says its mDNS client serves "only non-address
   requests". So a name solves reachability and not collision — you would still need the
   registration file to learn the port, i.e. option B is option A plus a multicast stack.
2. **A `.local` origin is not a Secure Context**, so it silently breaks the app.
   `ResponsePane.tsx:106` calls `navigator.clipboard?.writeText` — on a non-trustworthy
   origin `navigator.clipboard` is undefined and the `?.` swallows it, so "copy response"
   becomes a button that does nothing, with no error anywhere. Verified in the code.
3. **Its one real capability is the one we do not want.** mDNS exists to make a service
   findable *from other machines*; this server is local-only, so advertising buys nothing and
   costs a dev tool holding request history and credentials being enumerable on the segment.
   It also trips the macOS 15+ local-network privacy gate, which a plain CLI binary has no
   supported way to be prompted for — it just fails, as "no route to host".

A stable *URL* is a fair want, and it is the one thing a name plausibly offers — but it cannot
carry the port, so it would sit *on top of* the registration file rather than replacing it. If
the question is ever reopened, a redirect from a fixed port is the mechanism browsers honor
unconditionally; a name is not.

## Verify

- A cold `grpcview ls` starts a server and returns; a second one reuses it (assert on the
  pid in the registration, not on timing).
- Two workspaces at once: each CLI finds *its* own, never the other's, and `grpcview url`
  prints the right one.
- `SIGKILL` the server, then run a verb: it must start a fresh one, not dial a dead port
  or hang.
- A hand-edited registration naming a live process with the *wrong* workspace root is
  rejected, not trusted — the assertion `ServerInfo` exists for.
- Race: N concurrent `grpcview ls` from cold (`seq 8 | xargs -P8 …`) start **exactly one**
  server — the assertion the spawn lock exists for.
- `--idle-timeout 5s` and confirm it exits; then the same with a server-stream invoke
  running longer than the timeout, which must survive to completion.
- Rebuild the binary and rerun: the old daemon is replaced, and the new behavior is what
  answers. Verifying this by hand once is worth more than a unit test, because the failure
  mode is silence.
- `grpcview` opens a browser and the app works on a random port; `grpcview invoke` in another
  workspace opens **nothing**. `--no-open` and a `DISPLAY`-less environment both print the URL
  instead of failing.
- **Two invokes against a warm daemon do no store I/O on the second** — the payoff of the
  in-memory descriptor cache surviving between calls (phase 1, Decision 9).
- **A collection created by another process is listed by the daemon without a restart** —
  `mkdir` + a hand-written `grpcview.json` deep in the tree, then `grpcview collections ls`.
  This is the exact bug the mtime-keyed memo shipped with, so a watcher that reintroduces it
  is worse than no cache: assert on a hand-made collection, never one grpcview created.
- `bazel run //ui:dev` against `//service/cmd/dev --port 10000` still works — the one flow
  that needs a fixed port, now getting it from the flag rather than from a default.

## Open questions

- **A unix socket for the CLI, TCP for the browser?** The one option the naming research
  leaves genuinely open, and local-only strengthens it: a socket has no port to collide, and
  its mode bits plus peer credentials are real OS-enforced access control rather than a token.
  But the browser cannot speak to one, and the browser is the product — so the TCP loopback
  listener exists either way and a socket is a *second* transport serving only the CLI, not a
  replacement for the first. It would let the CLI stop reading the port (the socket path is
  derivable from the workspace hash), while the registration file stays for the URL, the pid,
  and executable identity. Worth it only if loopback proves insufficient in practice.
- **Does the extension supervise its own server or use connect-or-spawn?**
  [`phase-5-extension.md`](../active/vscode/phase-5-extension.md) sketches both. Reading the
  registration is less code and shares a process with the user's CLI, which is the whole
  point; spawning its own gives it a lifecycle it controls.
