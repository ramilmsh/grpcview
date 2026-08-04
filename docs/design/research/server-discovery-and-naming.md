# Server discovery & naming for the workspace model — research

Source: research agent + three scoped subagents (2026-08-04). Primary sources: bazel's own
C++/Java at `bazelbuild/bazel@master`, RFCs 6762/6763/9460, the WHATWG URL Standard, W3C Secure
Contexts, Chromium `net/dns/README.md` and `net/base/port_util.cc`, Jupyter/Delve/VS Code
source, plus a local bazel output base and the local `dns-sd(1)` man page. Claims marked
verified/inferred/unverified inline and summarised at the end. No code changed.

**Verdict up front: bind `:0` + a per-workspace registry file (option A). A registered name buys
nothing, because a name cannot carry a port and no browser will learn one from DNS-SD** — so
option B still needs the registry file, on top of a stale multicast stack, a macOS privacy gate
with no path for a CLI binary, a name that renames itself on conflict, and a LAN advertisement
of a tool holding credentials. §3 and §5 are load-bearing for the *decision*; **§1 is the part
that gets implemented** — the design has since moved to a full bazel-style daemon, so §1.1–§1.6
cover the startup race, detach/readiness, version skew, client env/cwd, and idle shutdown.

**Scope: macOS and Linux only.** Where a bazel mechanism differs between the two I say so — one
of its own defences is missing on macOS, which is our primary platform.

## 1. Bazel's mechanism, precisely

**Keying.** `StartupOptions::UpdateConfiguration` (`src/main/cpp/startup_options.cc`):
`output_user_root` defaults to `<default_output_root>/_<product>_<username>` — user-scoped by
*name in the path*, not permissions — and `output_base` to `<output_user_root>/<md5(workspace)>`.
Reproduced locally: `md5("/Users/r/tools/grpcview")` = `7717a4ce84e81730dda06fcee5c6232d`, exactly
the dir `bazel-out` points into. Plain MD5 of the absolute path, no salt, no trailing newline.

**The server directory.** `EnsureServerDir` (`blaze.cc:691`) creates `<output_base>/server` mode
**0700** — *"The server dir has the connection info - don't allow access by other users."*

| File | Written by | Contents |
|---|---|---|
| `server.pid.txt` | **client**, before the spawn (`blaze_util_posix.cc:414`, via `daemonize -p`) | decimal pid |
| `server.starttime` | client, after spawn | process start time, from `/proc` on Linux — **an empty no-op on macOS**, see below |
| `cmdline` | client (`blaze.cc:983`) | server argv, "used to validate the server running in this server_dir" |
| `server_info.rawproto` | **server** (`CommandServer.java:374`) | `ServerInfo{pid, address, request_cookie, response_cookie}` |
| `command_port`, `request_cookie`, `response_cookie` | server (`CommandServer.java:377-379`) | legacy plain-text mirrors of the same fields |
| `<output_base>/lock` | client | human-readable owner info; the lock itself is advisory `fcntl` |

The premise that the client reads the plain-text trio is **out of date**: they are still written,
but `BlazeServer::Connect` (`blaze.cc:1779`) reads *only* `server_info.rawproto`. The one
surviving client reference to `command_port` is a defensive `UnlinkPath` before starting a server
(`blaze.cc:964`) — vestigial. Copy the proto, not the trio.

**Ordering.** `server_info.rawproto` goes to `….tmp` then `renameTo` — *"Write then mv so the
user never sees incomplete contents."* Status files are written **after** `serve()` binds, so
their existence implies a listening socket. The pid file is the opposite, written by the client
*before* the spawn, hence *"there is no race here on startup since the server creates the pid
file strictly before it binds the socket."* Address is `ip:port`/`[v6]:port`; bazel binds `[::1]`
with a `127.0.0.1` fallback — **loopback only** — passing port 0 for kernel assignment.

**Liveness, three mechanisms.** (1) `VerifyServerProcess` compares the recorded
`server.starttime` against the live process — *"unique unless one can start more processes than
there are PIDs available within a single jiffy."* That is the Linux path. **On macOS —
our primary platform — it is bare `killpg(pid, 0) == 0`, with a TODO admitting the hole**: *"This
only checks for the process's existence, not whether its start time matches. Therefore this might
accidentally kill an unrelated process if the server died and the PID got reused."* So the one
platform we care most about is the one where bazel's own pid-reuse defence is missing; do not
inherit that, see §8 item 5.
(2) `PidFileWatcher` polls its own pid file every **3 s**; on mismatch it logs *"PID file deleted
or overwritten, exiting as quickly as possible"* and calls `Runtime.getRuntime().halt()`,
deliberately skipping shutdown hooks because *"Maybe it's another server… (that would delete
it)."* This is the anti-takeover interlock: whoever owns the pid file owns the output base.
(3) `ConnectOrDie` retries `Connect()` every **100 ms** up to `--local_startup_timeout_secs`,
aborting early if the spawned process dies.

**Cookies.** Two 16-byte `SecureRandom` values, hex-ish (`Integer.toHexString(b + 128)` — not
fixed-width, so length varies; a quirk, not a design). The client sends `request_cookie` on every
RPC and validates `response_cookie` on every response; the proto calls it *"a rudimentary form of
mutual authentication"*, compared with `MessageDigest.isEqual` *"using a constant-time comparison
in order to guard against timing attacks."* **They exist because a loopback TCP port has no
access control.** The 0700 dir keeps *other users* out; the cookie keeps a *same-user* stray
client off the wrong server. Copy both.

**Locking.** `<output_base>/lock`, advisory `fcntl` byte-range lock on byte 0, preferring
`F_OFD_SETLK` (*"POSIX locks can be lost 'accidentally' due to any close() on the lock file"*);
shared for the install base, **exclusive** for the output base. On
success it re-`fstat`s for `st_nlink > 0` to catch an unlink underneath it, then writes owner info
*"printed for human consumption… but not parsed otherwise."* 500 ms retry; `--noblock_for_lock`
exits instead.

### 1.1 The startup race, and what the lock actually covers

The lock **is** the answer to "two concurrent invocations both start a server". `RunLauncher`
acquires it *first*, before extraction, before `Connect()`, before any version check
(`blaze.cc:1542`). `<output_base>/lock`, `LockMode::kExclusive`, advisory `fcntl` byte-range on
byte 0, `F_OFD_SETLK` preferred. The whole
connect → version-check → maybe-kill → maybe-spawn sequence therefore runs under mutual
exclusion, so only one client can ever be in the business of starting a server.

The contents are pure UX: on exclusive acquisition the holder calls `WriteOwnerInformation(fd)`;
a waiter `pread`s the first 4 KB and prints *"Another command holds the output base lock: \n"* +
that text, then *"Waiting for it to complete..."*. The comment is explicit that this is
*"printed for human consumption when another client fails to take the lock, but not parsed
otherwise"* — so the file is not a registry, and correctness never depends on parsing it.

**The lock is a rendezvous, not a command-duration lock.** It is released as soon as the `Run`
RPC has been issued (`blaze.cc:2088`): *"Release the client-side locks, as the server may
outlive the client and must implement its own locking of the install and output bases. This may
result in two 'waiting for lock' messages, one emitted by client during server startup, and
another emitted by the server. This is harmless."* Command-level concurrency is a *server-side*
concern, expressed as the `RunRequest.block_for_lock` field — *"Tells the server whether or not
the client is willing to wait for any concurrent in-flight request to complete… If false and
there are in-flight requests then the server will return an error immediately."* Two layers, two
different jobs; do not conflate them.

### 1.2 Detach, and how a failed start is surfaced instead of hanging

The client never forks the server itself. It `posix_spawn`s a **separate tiny helper binary**,
`daemonize`, whose whole job is the detach (`blaze_util_posix.cc:408-508`). `daemonize`:

1. `pipe()`s a barrier, then `fork()`s.
2. The **child** blocks on `read(pid_done_fd)` before doing anything — *"This blocks execution
   until pid_done_fd receives a write… because the Bazel server process… requires the PID file to
   be present at startup time so we must wait until the parent process has created it."* Then
   `signal(SIGHUP, SIG_IGN)`, `setsid()`, `SetupStdio()`, `execv(server)`.
3. The **parent** (`daemonize` itself) writes the pid file, releases the barrier, and exits —
   orphaning the server to init. Client → `daemonize` → server, so the effect is a double fork.
4. `SetupStdio`: stdin ← `/dev/null`; stdout ← `open(log_path, O_WRONLY|O_CREAT|(append ?
   O_APPEND : O_TRUNC), 0666)`; stderr ← `dup(stdout)`. `log_path` is `jvm.out`. **Note the
   0666** — the log is protected only by the 0700 server dir, not its own mode. We should use
   0600 and not rely on the parent.
5. The client `waitpid`s the transient `daemonize` — *"Wait for daemonize to exit. This
   guarantees that the pid file exists."* — then reads the pid and writes `server.starttime`.

Readiness is polling, not a signal: `ConnectOrDie` calls `Connect()` every 100 ms until
`--local_startup_timeout_secs` (**default 120**, *"Give the server two minutes to start up.
That's enough to connect with a debugger"*), printing *"… still trying to connect …"* at most
every 10 s.

**The anti-hang mechanism is a socketpair, and it is the single best idea in here.** Before
spawning, the client creates `socketpair(AF_UNIX, SOCK_STREAM)`, closes the child's end in
itself, and holds the other end as a `SocketBlazeServerStartup`: *"Notifies the client about the
death of the server process by keeping a socket open in the server. If the server dies for any
reason, the socket will be closed, which can be detected by the client."* `IsStillAlive()` is a
non-blocking `poll()` — timeout means alive, `POLLHUP`/error means dead. Each poll iteration
checks it, and on death the client prints *"Server crashed during startup. Now printing
`<jvm.out>`"* and **dumps the daemon's log to the user's stderr** (`WriteFileToStderrOrDie`),
then exits `INTERNAL_ERROR`. If it was appending it only points at the path instead: *"Don't
dump the log if we were appending - the user should know where to find it, and who knows how much
content they may have accumulated."* Only if the process is still alive but silent past the
deadline do you get *"couldn't connect to server (pid) after N seconds."* So: crash → log on your
terminal in ~100 ms; genuine hang → a bounded, named timeout. Copy this wholesale.

In Go none of this needs a helper binary: `exec.Cmd` with `Setsid` in `SysProcAttr`, plus an
inherited `os.Pipe` whose read end the parent keeps and whose EOF is the death signal, reproduces
both the detach and the liveness channel directly.

Two further hardening details in `Connect()` worth copying: it **refuses a non-loopback address**
outright (the string must start `127.0.0.1:`, `[0:0:0:0:0:0:0:1]:` or `[::1]:` — *"Make sure that
we are being directed to localhost"*), which is the defence against a poisoned registry file; and
it validates the connection with a `Ping` carrying the cookie, deadline
`--connect_timeout_secs` (**default 30**), rejecting a mismatched response cookie.

### 1.3 Version skew — the stale-daemon trap

Two independent checks, both under the lock, both before any command is sent.

**Binary identity** — `EnsureCorrectRunningVersion` (`blaze.cc:1161`): `<output_base>/install` is
a *symlink* to the install base, which is `<output_user_root>/install/<md5(bazel binary)>`. If the
symlink is missing or does not resolve to the current install base, the running server *is a
different build* and is killed (`restart_reason = NEW_VERSION`); the client then re-points the
symlink *"so others know which installation is running"* and touches the install base's mtime for
GC tooling. **This is the mechanism we need**: version identity is a content hash of the binary,
recorded in the output base, checked on every invocation. A rebuilt binary is a different hash,
so a stale daemon cannot silently serve old code.

**Startup-option identity** — `KillRunningServerIfDifferentStartupOptions` (`blaze.cc:1103`):
compares `server/cmdline` (NUL-separated, deliberately *not* `/proc/$PID/cmdline` — *"No,
/proc/$PID/cmdline does not work, because it is limited to 4K"*) against the args it would use
now, as an order-insensitive `unordered_multiset` (order-insensitive because bazelrc line order
must not matter; multiset because a flag can legitimately repeat). Mismatch → kill, with a
diagnostic listing *"Only in old server:"* / *"Only in new server:"*. An allowlist of **volatile**
options is excluded from the comparison and so never forces a restart: `--option_sources=`,
`--max_idle_secs=`, `--connect_timeout_secs=`, `--local_startup_timeout_secs=`,
`--client_debug=`, `--preemptible=`, `-XX:HeapDumpPath=`. We will want the same idea — a small
set of "client-side only" flags that must not churn the daemon.

### 1.4 Client env and cwd — not in the proto

`RunRequest` has **no cwd field and no env field** (verified against the full message: `cookie`,
`arg`, `block_for_lock`, `quiet`, `client_description`, `invocation_policy`, `startup_options`,
`preemptible`, `command_extensions`). Both travel as ordinary **command arguments**, appended by
`OptionProcessor` (`option_processor.cc:599-603`):

```
// Pass the client environment to the server.
for (const string& env_var : env) { result.push_back("--client_env=" + env_var); }
result.push_back("--client_cwd=" + blaze_util::ConvertPath(cwd));
```

One `--client_env=K=V` per variable, plus one `--client_cwd=`. The daemon's *own* environment is
frozen at spawn time by `PrepareEnvironmentForJvm`, and the comment there is explicit that
*"Changes made to the environment in this function will not be part of '--client_env'"* — the
JVM-shaping env and the reported client env are deliberately different things. The server's own
cwd is also fixed at spawn: the client `GoToWorkspace()`s (chdir to the workspace root) before
spawning. `client_description` is separate and required — *"A simple description of the client for
reporting purposes"* — set to `"pid=<client pid>"`.

**Lesson for us:** a daemon must treat its own `os.Environ()` and `os.Getwd()` as meaningless and
take both from the request. Anything resolved relative to the process cwd (source paths,
`.env` lookup, relative descriptor-source paths) is a latent bug the moment the server outlives
the shell.

### 1.5 Idle shutdown, precisely

`--max_idle_secs` defaults to **`3 * 3600` = 10800 s (3 hours)**, or **15 s** under
`IsRunningWithinTest()` (`startup_options.cc:116`). The watcher thread is only started at all
`if (maxIdleSeconds > 0)`.

The timer is **not** "since the last command started" — it is "since the server became idle", and
it is re-armed on every busy→idle transition (`ServerWatcherRunnable.run`): when
`commandManager.isEmpty()` first becomes true it sets `shutdownTimeMillis = now + maxIdleSeconds`;
it only breaks out when `wasIdle && idle && now >= shutdownTimeMillis`. So **in-flight and
long-running commands count as activity** — a 4-hour build never trips a 3-hour idle timeout —
and while busy the thread blocks in `commandManager.waitForChange()` rather than polling.

Other shutdown paths, verified: (a) explicit `bazel shutdown`; (b) the client killing it for
version or startup-option skew (§1.3); (c) `PidFileWatcher` seeing its pid file change →
`Runtime.halt()`; (d) `--shutdown_on_low_sys_mem`, but *only* after **5 minutes** continuously
idle (`TIME_IDLE_BEFORE_MEMORY_CHECK`), re-checked every 5 s, where "low" on Linux means free RAM
below **both** 5 % and 1 GiB (`FREE_MEMORY_KB_ABSOLUTE_THRESHOLD = 1L << 20` KB), and on every
other platform means `SystemMemoryPressureMonitor.level() != NORMAL` — which is the macOS
specific: it defers to the OS memory-pressure signal rather than reading free RAM. **There is no
low-disk shutdown** — grepping the server package and client for disk/free-space conditions found
nothing. `--block_for_lock` is *not* a shutdown condition; it is the concurrency knob of §1.1.

### 1.6 Idle shutdown racing a connecting client

There is **no handshake, no drain protocol, and no "shutting down" status.** The server calls
`server.shutdown()` and its `ShutdownHooks` delete the status files; the client's defence is that
`Connect()` is a *validating* read: read `server_info.rawproto`, check the address is loopback,
check the pid, `VerifyServerProcess`, then `Ping` with the cookie under a 30 s deadline. Any step
failing returns `false`, which is indistinguishable from "no server" — and the client then goes
down the `StartServerAndConnect` path, which begins by calling
`EnsurePreviousServerProcessTerminated` to `SIGKILL` whatever the pid file still names, then spawns
fresh. The window is narrowed rather than closed: the status files are written *after* bind and
deleted by shutdown hooks, and `PidFileWatcher` makes a half-dead server `halt()` rather than
linger. Bazel's own header comment enumerates the residual cases it accepts, including *"The
server stopped accepting connections but hasn't quit yet and a new client comes around: the new
client will kill the server based on the PID file before a new server is started up."* The cost
is the honest one: a client can occasionally kill a server that was about to be fine, and — on
macOS, per §1's `VerifyServerProcess` TODO — could in principle kill an unrelated pid. Design for
"validate then take over", not for graceful handoff.

## 2. Registering a resolvable name, unprivileged

| Mechanism | Root? | Needs a daemon? | Via `getaddrinfo`? | Carries a port? |
|---|---|---|---|---|
| macOS `dns-sd -P name type domain port host IP` / `DNSServiceRegisterRecord` | no (inferred — IPC to `mDNSResponder`) | yes, but always running | yes | host record no; the SRV does |
| Linux `avahi-publish -a <name> <addr>` | no (inferred; only `SetHostName` is root-gated) | **yes — `avahi-daemon` + D-Bus** | only with `nss-mdns` in `nsswitch.conf` | no |
| `/etc/hosts` | **yes**, both | no | yes | **no** |
| `127.0.0.x` alias | Linux no (whole /8 on `lo`); **macOS yes** — `sudo ifconfig lo0 alias`, non-persistent | no | n/a | **no** |

**No name mechanism carries a port except SRV, and SRV is not a hostname** — RFC 6763 §4.1.3:
*"Because Service Instance Names are not host names, they are not constrained by the usual rules
for host names."* So the only option that even resolves everywhere is mDNS, and on Linux it needs
a daemon we do not control and an `nss-mdns` line in `nsswitch.conf` we cannot assume.

## 3. The decisive sub-question: what a browser does with such a name

**Resolution: yes, by delegating.** Chromium's `net/dns/README.md` says the built-in async
resolver is used only when *"The request hostname does not end in '.local'"*, and that *"For
hostnames ending in '.local' [the system resolver will always be used for address resolves]"*. So
`.local` in Chrome is exactly as good as `getaddrinfo` on that box: always on macOS,
**conditional on Linux**. Firefox keeps `.local` off DoH via
`network.trr.builtin-excluded-domains`, *"normally domains that are equal or end in localhost or
local"*. Chromium *does* ship an mDNS client — but the same README says *"mDNS is only used for
non-address requests when the request hostname ends in '.local'"*: it exists for Cast-style
discovery, never to turn a URL host into an address.

**Port: no. Four independent confirmations.** (a) WHATWG URL: a URL's port is *"either null or a
16-bit unsigned integer"* parsed from the URL-port string, defaulting to the scheme default —
there is no DNS step, the port is syntactic. (b) RFC 6763 §5: *"The SRV record for a service
gives the port number and target host name where the service may be found"* — for DNS-SD-aware
clients only. (c) Mozilla bug 14328, *"DNS: RFC 2782 not supported (SRV records)"*, filed 1999,
**RESOLVED WONTFIX**; no engine resolves SRV for http/https. (d) RFC 9460's `HTTPS`/`SVCB` RR does
define a `port` SvcParamKey, but §7.2 has clients *"SHALL use the authority endpoint's port
number"* absent it, the RFC targets public DNS and says nothing about `.local`, and Chrome's SVCB
coverage is partial. No shipping browser treats `port` from an unauthenticated mDNS record as a
redirect.

So `http://grpcview-myrepo.local/` means port 80 — root on Unix, and colliding across workspaces
anyway. You end at `http://grpcview-myrepo.local:53411/`, a registry-file problem wearing a hat.

**And a `.local` origin is strictly worse than a loopback one.** Secure Contexts' potentially-
trustworthy list covers `127.0.0.0/8`, `::1/128`, `localhost` and `*.localhost` — and **not**
`.local` (verified: it appears nowhere in the algorithm). Moving to `http://x.local:PORT` silently
drops the page out of secure-context status, which here breaks
`ui/src/features/workspace/ResponsePane.tsx:106` — `navigator.clipboard?.writeText(bodyText)`. The
optional chaining makes "copy response body" a silent no-op. Self-inflicted regression, zero gain.
(One hypothesis *refuted*: I expected a `.local` page fetching loopback to trip Private Network
Access. It does not — PNA restricts only *more public → more private*.)

## 4. Pure-Go mDNS/DNS-SD libraries

Every DNS-SD-capable pure-Go option is stale; the one actively maintained one does no DNS-SD.

| Library | Last push | Advertise/browse | Notes |
|---|---|---|---|
| `grandcat/zeroconf` | 2023-12-07, 903★ | both | de facto unmaintained; open panics (#118), unmerged crash fix (#113), multi-interface wrong IP (#43) |
| `hashicorp/mdns` | 2026-07-27 (Dependabot only), 1372★ | both | #56 does not interop with `avahi-browse`; #80 open and unfixed — a maintenance signal |
| `brutella/dnssd` | 2026-02-27, 237★ | both | #32 high CPU in read loop; #63 no re-query before TTL expiry |
| `libp2p/zeroconf/v2` | 2025-08-20, 27★ | both | fork of grandcat created *because* upstream looked unmaintained; itself now quiet |
| `pion/mdns` | 2026-08-03, 278★ | hostname only | **RFC 6762 only, no DNS-SD** — cannot register `_grpc._tcp`; built for WebRTC ICE |
| `holoplot/go-avahi` | 2026-07-07, 32★ | both | D-Bus client for an external `avahi-daemon`; Linux-only — disqualifying |
| `miekg/dns` | 2026-08-01, 8743★ | neither | unicast wire format only; no multicast, no group join, no DNS-SD |

cgo-free is satisfiable; *maintained* is not. The multi-interface and
IPv6 handling bugs (`grandcat#43`, `brutella#32`) become the live risks, and those are the ones
nobody is fixing.

## 5. Frictions

- **macOS firewall.** Apple only says the prompt fires *"when your Mac detects an attempt to
  connect to an app"*; the loopback carve-out is widely reported but **never documented by
  Apple**. An Apple DTS engineer confirmed the prompt keys off the binary's code-signing
  designated requirement, so an **unsigned Bazel-built binary is a new app every rebuild** and
  re-prompts. A forum report shows a multicast UDP bind triggering the same dialog — not
  TCP-only. **Moot for us today**: `service/service.go` binds `net.IPv4zero`, so we already
  trip it.
- **macOS 15+ "Local Network" privacy permission** — a *separate* gate on mDNS and direct
  local-network traffic, needing `NSLocalNetworkUsageDescription` (+`NSBonjourServices`) in an app
  bundle's Info.plist. Denied, it fails silently and misleadingly ("no route to host"). **A plain
  CLI binary has no supported path to be prompted or allow-listed, and Apple has confirmed there
  is no MDM control for it.** Close to disqualifying for option B on its own.
- **Corporate networks.** mDNS is link-local, TTL=1, never crosses a VLAN without a reflector;
  "wireless client isolation", standard on enterprise and guest SSIDs, blocks it *within* a subnet
  too. Cisco, Aruba and Meraki all sell Bonjour/mDNS gateways precisely because this breaks
  constantly. Expect advertising to be a no-op on corporate Wi-Fi.
- **Privacy.** DNS-SD is unauthenticated and unencrypted by design: anyone on the segment can
  passively enumerate advertised services and spoof responses. Advertising turns an IP/port scan
  into a directory listing of "here is a dev tool, its type, its port". The bug class is live —
  webpack-dev-server CVE-2025-30359/30360 and CVE-2026-6402 are all "bound to non-loopback + weak
  Origin validation". **We are already in it**: `service/service.go` binds `0.0.0.0` *and* sets
  `AllowedOrigins: []string{"*"}`, while the workspace holds request history, metadata and
  whatever credentials those requests carry. Binding loopback is worth doing whichever option
  wins; advertising is the opposite of the fix.

## 6. Prior art

Nobody registers a name. The two tools closest to our problem — Jupyter and Delve — both use
port-0-or-search plus an out-of-band channel for the answer.

| Tool | Pattern |
|---|---|
| Vite | scan-then-increment — *"if the port is already being used, Vite will automatically try the next available port"*; `strictPort: true` to fail instead |
| webpack-dev-server, `air`, ngrok, Puma/`rails s` | fixed port; webpack opts into a search with `port: 'auto'`, Puma just `raise`s on `EADDRINUSE` |
| `next dev` | scan-then-increment (`find_port()`, source only — undocumented) |
| **Jupyter Server** | scan-increment **+ registry file + pid-liveness pruning + optional unix socket** |
| **Delve** | **bind `:0` + stdout handoff** — default `--listen=127.0.0.1:0`, first stdout line *"API server listening at: `<addr>`"* is the documented contract; `unix:` prefix selects a UDS |
| Docker Desktop | UDS resolved via the CLI's active *context*, not a hardcoded path (macOS `~/.docker/run/docker.sock`; `/var/run/docker.sock` is only a compat symlink); `DOCKER_HOST` overrides |
| VS Code → language server | **client allocates the endpoint first** (`TransportKind: stdio\|ipc\|pipe\|socket`), *then* spawns and hands over the pipe name/port — no discovery problem at all |
| VS Code CLI ↔ live window | UDS named by random UUID (`$XDG_RUNTIME_DIR/vscode-ipc-<uuid>.sock`), path passed by env var (`VSCODE_IPC_HOOK`, `VSCODE_IPC_HOOK_CLI`) |
| Postman / Bruno | Postman ships a companion agent (purpose documented, port not); Bruno needs none — Electron requests from the trusted main process |

**Jupyter is the closest prior art.** It picks a port by search (`port_retries` default 50) rather
than `:0`, but *regardless of which port won* always writes `jpserver-<pid>.json` into the runtime
dir with `secure_write` (mode 600), containing `{url, hostname, port, sock, secure, base_url,
token, root_dir, password, pid, version}` — note the **token**, its cookie equivalent, in the same
600-mode file. Discovery is `list_running_servers()`: glob `jpserver-*.json`, `check_pid` each
(`os.kill(pid, 0)`; `ESRCH` dead, `EPERM` alive-but-not-ours), and
**unlink dead entries on the spot**. Self-healing on read: no reaper, no lock, simpler than
bazel's watcher. It keys by *pid* though — we want `md5(workspace)`, "the server for *this* repo".

**VS Code supplies both counter-examples.** For a server the client *spawns*, allocating the
endpoint client-side (LSP) or reading it off stdout (Delve) beats any registry file — no
staleness, lock or cleanup. For one it *did not* spawn, VS Code's env-var handoff has exactly the
failure a file registry avoids: a stale `VSCODE_IPC_HOOK_CLI` inherited by a new shell makes
`code <file>` fail `ENOENT` on a dead socket, because nothing prunes it the way `check_pid` does.
Both cases are ours — the extension will sometimes spawn and sometimes attach. Support both.

## 7. Unix domain sockets — the fourth option

**A browser cannot reach one, and this is settled.** `whatwg/url#577` "Addressing HTTP servers
over Unix domain sockets" is **Closed as not planned**: *"there is not currently an agreed-upon
way to address such a service in a URL."* Firefox bug 1688774 is open but a Mozilla networking
engineer is on record — *"I think getting this to actually work would be a bit difficult, for very
little benefit. I am inclined to WONTFIX this — similar to Chrome"* — with DNS-rebinding "confused
deputy" risk as the deeper reason. (A Chromium primary source could not be fetched: two attempts
returned a sign-in wall and an empty body.)

**Our own client can.** `net.Dial`/`Listen` accept `"unix"` ("the address must be a file system
path"), and connect-go's FAQ states *"under the hood Connect-Go is just using an `*http.Client`"*
— so a custom `Transport.DialContext` is all it takes, with no new dependency.

The real prize is auth: a unix socket can be identified, a loopback port cannot. Linux
`SO_PEERCRED` is reachable cgo-free (`x/sys/unix.GetsockoptUcred`); macOS has
`LOCAL_PEERCRED`/`GetsockoptXucred`, though `Xucred` carries no pid — so on macOS you learn the
*uid* of the peer but not which process it was. golang/go#41659 (open) is exactly about the
missing portable abstraction. Linux `unix(7)` confirms mode bits bite — *"connecting to a stream
socket object requires write permission on that socket"* — while noting *"on some systems (e.g.,
older BSDs), the socket permissions are ignored."* Whether current macOS honours socket mode is
**unresolved** after two attempts, which matters: if it does not, a 0600 socket in a 0700 directory
is protected by the *directory* only, exactly as bazel's 0666 `jvm.out` is.

**So "socket for the CLI, TCP for the browser" is plausibly right — but as a later refinement.**
It doubles the listener count and the platform matrix, and the cookie buys most of the security
for a fraction of the code. Ship TCP + cookie; keep UDS as the upgrade path for the VS Code
extension, where a peer-credential check is worth real money.

## 8. Verdict

A name would have to buy *discovery* or a *stable URL*. It buys neither. Discovery still needs the
port, so you still need the file. And the URL is stable only until mDNS conflict resolution
renames you: RFC 6762 §9 requires the loser of a probe to *"cease using the name, and
reconfigure"*, recommending *"appending the digit '2' to the name."* Two checkouts of one repo, or
a colleague on the same LAN, and `grpcview-api.local` becomes `grpcview-api-2.local`. A name that
renames itself is not a bookmark.

Origin stability costs us nothing today: `ui/src` uses no `localStorage`, `sessionStorage`,
`indexedDB` or persisted store (confirmed — the caller's grep reproduces), so a changing origin
loses no user state. Bookmarkability is the one honest loss, better answered by a `grpcview open`
verb that reads the registry and shells out to the browser.

Recommended shape — bazel where bazel is right, Jupyter where it is better:

1. `os.UserConfigDir()/.grpcview/run/<md5(abs workspace path)>/`, mode **0700**. The repo already
   roots state at `os.UserConfigDir()/.grpcview` (`service/workspace/workspace.go:35`).
2. Bind **loopback** on port **0**. That means replacing
   `http.ListenAndServe(net.TCPAddr{IP: net.IPv4zero, …}.String(), …)` in `service/service.go`
   with `net.Listen` + `l.Addr().(*net.TCPAddr).Port` + `http.Serve(l, …)` — the current call
   gives no way to learn the assigned port. Fix the `0.0.0.0` bind and the `*` CORS origin in the
   same change; that is the security win, independent of discovery.
3. After binding, write `server.json` as `.tmp` + rename:
   `{pid, start_time, address, cookie, workspace_path, version}`.
4. Exclusive advisory lock on the directory (`fcntl` `F_OFD_SETLK`, or `flock`), taken **before**
   probing for a live server and released **once the first request is in flight** — a rendezvous,
   not a command-duration lock (§1.1). Write the holder's `pid`/argv into it for the "another
   command is running" message, and never parse it back.
5. Staleness: **prune on read**, Jupyter-style — a client finding a dead pid unlinks the file and
   reports "no server" — plus bazel's start-time check, since bare `kill(pid, 0)` is the exact
   hole bazel's macOS `VerifyServerProcess` admits to — and macOS is our primary platform, so this
   is the one place we should be *better* than bazel, not a copy of it. Cgo-free in Go:
   `x/sys/unix.SysctlKinfoProc` (`KERN_PROC_PID` → `kinfo_proc.Proc.P_starttime`) on macOS,
   `/proc/<pid>/stat` field 22 on Linux.
6. A `cookie` header on every RPC, constant-time compared — the only thing between us and another
   local process driving the workspace. Have the *client* validate the recorded address is
   loopback before dialling (§1.2), so a tampered registry file cannot redirect us off-box.
7. Daemon specifics, all lifted from §1.1–§1.6: hold a pipe/socketpair open across the spawn so a
   crashed start is detected in ~100 ms and the daemon's log is dumped to the user's stderr rather
   than becoming a hang; record a **build identity** (the binary's content hash, or `version` +
   mtime for a dev build) and have the client kill-and-respawn on mismatch — a Go binary rebuilt
   by Bazel every few minutes makes the stale-daemon trap the *most likely* bug in the whole
   feature; carry the caller's cwd and environment **in every request** and treat the daemon's own
   `os.Getwd()`/`os.Environ()` as meaningless; arm an idle timer on each busy→idle transition (not
   per command, so long-running streams do not trip it); and expect no graceful handoff — a client
   that cannot ping validates, kills, and takes over.
8. Clients: `--server` with no value resolves via the registry file. The CLI's default path does
   not need it at all — `openClient` runs the workspace **in-process** unless `--server` is given
   (`service/cli/client.go:89`). The file's real consumer is the VS Code extension; when *it*
   spawns the server it should skip the file and read the address off stdout, Delve-style, so
   print `listening on http://127.0.0.1:<port>` as a stable first line.
9. **No UI change needed**: `ui/src/lib/client.ts:4` already uses
   `import.meta.env.PROD ? window.location.origin : "http://127.0.0.1:10000"`, so a kernel-assigned
   port works untouched in a real build; only the dev fallback names a port. And `:0` is safe for a
   browser target — Chromium's `kRestrictedPorts` (`net/base/port_util.cc`) tops out at **10080**,
   while ephemeral ranges sit far above it (verified locally: macOS
   `net.inet.ip.portrange.first` = 49152). Keep `--port` for people who want the bookmark.

If a name is ever wanted, add DNS-SD **later and additively** as a `_grpcview._tcp` advertisement
for tooling discovery only, never as the browser's address, and off by default.

## Verification status

**Verified from primary sources.** All of §1, read directly in `bazelbuild/bazel@master` on
2026-08-04 — file names, write order, `md5(workspace)` keying (reproduced locally, and `Md5Digest`
confirmed in `GetHashedBaseDir`), mode 0700, tmp+rename, cookie generation and constant-time
compare, `fcntl`/`F_OFD_SETLK` locking with the `st_nlink` check, the 3 s `PidFileWatcher`, the
100 ms client poll, the Linux start-time defence and the macOS TODO admitting its absence.
For §1.1–§1.6 specifically: lock acquisition at `blaze.cc:1542` and release at `:2088` with the
quoted comment; `WriteOwnerInformation`/`ReadOwnerInformation`; `RunRequest.block_for_lock`'s
documented semantics; `daemonize.cc`'s pipe barrier, `setsid`, `SetupStdio` (including the
literal `0666` log mode) and single-fork-plus-orphan structure; `waitpid` on the transient helper
with its "guarantees that the pid file exists" comment; `SocketBlazeServerStartup::IsStillAlive`'s
`poll()` and the "Server crashed during startup" / append-mode branches;
`--local_startup_timeout_secs` = 120 and `--connect_timeout_secs` = 30 from
`startup_options.cc:85-86`; `Connect()`'s loopback-prefix check and cookie-validating `Ping`;
`EnsureCorrectRunningVersion`'s `install` symlink comparison; `AreStartupOptionsDifferent`'s
multiset comparison, the `server/cmdline` read with its `/proc` 4 K comment, and the seven-entry
volatile-option allowlist in `IsVolatileArg`; the full `RunRequest` field list showing **no** cwd
or env field, against `option_processor.cc:599-603`'s `--client_env=`/`--client_cwd=`;
`max_idle_secs = IsRunningWithinTest() ? 15 : 3 * 3600`; `ServerWatcherRunnable.run`'s busy→idle
re-arm, `waitForChange`, the 5-minute `TIME_IDLE_BEFORE_MEMORY_CHECK`, the 5 s recheck, and both
low-memory checkers. RFC
6763 §4.1.3/§5; RFC 6762 §9 and port 5353/224.0.0.251; RFC 9460 §7.2. WHATWG URL port parsing;
`whatwg/url#577`; Firefox bug 1688774; Mozilla bug 14328 = WONTFIX. Secure Contexts' trustworthy-
host list excluding `.local`. Chromium `net/dns/README.md`; `kRestrictedPorts` max 10080.
`dns-sd(1) -P` (local man page); `avahi-publish -a`; `MulticastDNS=` in `resolved.conf(5)`;
nss-mdns's `nsswitch.conf` edit. Library dates via the GitHub API; enterprise mDNS blocking from
Cisco/Aruba/Meraki docs; webpack-dev-server CVEs. Jupyter `serverapp.py`; Delve's man page and
`ClientHowto.md`; Vite/webpack docs; Puma and Next.js source; five VS Code source files plus
`vscode-languageserver-node`'s `TransportKind`; Docker's socket-path and context docs. `net`
package docs; connect-go's FAQ; `unix(7)`; `x/sys/unix.GetsockoptUcred`. This repo:
`service/service.go`'s
`IPv4zero` + `*` CORS, `ui/src/lib/client.ts:4`,
`ui/src/features/workspace/ResponsePane.tsx:106`, `service/cli/client.go:89`, absence of web
storage in `ui/src`, macOS ephemeral range 49152–65535.

**Inferred.** That binding loopback avoids the macOS firewall prompt (widely reported, never
stated by Apple). That `dns-sd`/`avahi-publish` record registration needs no root — absence of a
privilege caveat plus the daemon-does-the-work model, not an explicit statement. That
mDNSResponder is always running and macOS `getaddrinfo` resolves `.local` by default. Firefox's
literal `builtin-excluded-domains` default string. `brutella/dnssd` being cgo-free. Chrome's exact
SVCB SvcParam coverage. The Linux ephemeral port range. Bruno needing no local agent.

On §1's negatives: **no low-disk shutdown condition exists** — a verified *absence* (grepping the
whole `lib/server` package and the client for disk/free-space terms found nothing), which is
weaker evidence than a positive quote; re-check before relying on it. The POSIX detach path was
read in full (`daemonize.cc` + `blaze_util_posix.cc`).

**Unverified / could not confirm.** Whether mDNS advertise+browse works with only `lo`/`lo0` up
and no network — plausible and reported, no primary source, and `grandcat/zeroconf#106` is open on
exactly this. Apple's TN3179 would not render as text after two attempts, so every macOS-15
Local-Network claim in §5 comes from Apple-engineer replies in forum threads *about* the note, not
the note. Whether
current macOS enforces unix-socket mode bits at `connect()` — two attempts, inconclusive, do
**not** assume either way. A Chromium primary source on `http+unix`. Postman's agent port.
Open-issue counts for `brutella/dnssd` and `libp2p/zeroconf` disagree between the GitHub API and
the rendered lists. Three Jupyter/pytest-xdist doc pages returned `HTTP 429` and Postman's
support article `HTTP 403`, so those rows come from source on GitHub instead — stronger anyway.
Finally, a live bazel `server/` dir was never *observed* with the runtime files present: the local
output bases held only `cmdline` and `jvm.out`, consistent with the shutdown hooks having deleted
the rest, and starting a server was out of scope. The §1 file set is verified from source, not
observation.
