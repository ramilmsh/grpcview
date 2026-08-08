#!/usr/bin/env bash
# Brings up the one server the `example` collection needs that grpcview is not, and holds until
# interrupted. Invoked as `bazel run //example:up`; the echo and CLI binary paths arrive as $1..$2,
# expanded by the target's `args`.
#
# The collection's other half — `Collections/`, grpcview reflecting itself — is served by
# grpcview's own workspace server, which any `grpcview` command starts on demand. Standing a
# second one up here would only fight it for port 10000.
set -euo pipefail

echo_bin=$1
cli_bin=$2
shift 2

echo_port=50055
isolated=0
tmp_state=""
echo_pid=""
shutdown_server=0

while [[ $# -gt 0 ]]; do
  case $1 in
    --isolated)
      isolated=1
      shift
      ;;
    --echo-port)
      echo_port=$2
      shift 2
      ;;
    *)
      echo "example:up: unknown argument $1" >&2
      exit 2
      ;;
  esac
done

cleanup() {
  [[ -n $echo_pid ]] && kill "$echo_pid" 2>/dev/null
  wait 2>/dev/null
  # Only a server this script started under a state directory it is about to delete. A developer's
  # own server is left alone: it is not this script's to stop.
  [[ $shutdown_server == 1 ]] && "$cli_bin" shutdown 2>/dev/null
  [[ -n $tmp_state ]] && rm -rf "$tmp_state"
  return 0
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# The server writes run history to a per-workspace state dir. A CI or throwaway run must not land
# in the developer's real one. GRPCVIEW_CONFIG_DIR moves grpcview's state and nothing else —
# overriding HOME would also move the output base of the bazel build below, into a temp dir that
# is then deleted.
#
# That state holds the resolve caches too, so an isolated run starts with none. The example's
# bazel source commits no descriptors — deliberately, it is what demonstrates the uncommitted
# mode — so it is built here. Trust lives in the same discarded state, so granting it covers only
# the throwaway copy, for a repo whose bazel targets the caller is already running.
#
# A GRPCVIEW_CONFIG_DIR already in the environment is used as-is and kept, so a CI job can export
# one, run this, and point its own `grpcview` invocations at the same state.
if [[ $isolated == 1 ]]; then
  if [[ -z ${GRPCVIEW_CONFIG_DIR:-} ]]; then
    tmp_state=$(mktemp -d)
    export GRPCVIEW_CONFIG_DIR="$tmp_state"
  fi
  echo "example:up: isolated state under $GRPCVIEW_CONFIG_DIR" >&2

  # --in-process, not the workspace server: nothing has to outlive this call, and a server started
  # here would be started before the trust it needs is granted.
  echo "example:up: building the example's bazel definition source" >&2
  "$cli_bin" --in-process trust
  "$cli_bin" --in-process sources refresh bazel://proto/echo/v1:echov1_proto --collection example
  shutdown_server=1
fi

# bazel run leaves the cwd in the runfiles tree; the CLI resolves its workspace from
# BUILD_WORKSPACE_DIRECTORY, which bazel already exports.
"$echo_bin" -port "$echo_port" &
echo_pid=$!

await_port() {
  local port=$1 name=$2
  for _ in $(seq 1 100); do
    if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
      exec 3>&-
      return 0
    fi
    sleep 0.1
  done
  echo "example:up: $name never came up on port $port" >&2
  exit 1
}

await_port "$echo_port" "the echo server"

# Starts one if none is running, and prints where it landed. The collection's reflection source
# names localhost:10000, so a fallen-back port is worth saying out loud rather than leaving as a
# dial failure later.
server_url=$("$cli_bin" url)

cat >&2 <<EOF
example:up: echo.v1.EchoService on 127.0.0.1:$echo_port
example:up: grpcview on $server_url (its own API, by reflection)
EOF

if [[ $server_url != "http://127.0.0.1:10000" ]]; then
  echo "example:up: warning — the collection's reflection source names localhost:10000, so the" >&2
  echo "example:up: Collections/ requests will not reach this server. Free that port and retry." >&2
fi

echo "example:up: ready — run \`grpcview script run smoke --collection example\`. Ctrl-C to stop." >&2

# Polled rather than `wait -n`, which macOS's system bash does not have.
while kill -0 "$echo_pid" 2>/dev/null; do
  sleep 1
done
echo "example:up: the echo server exited" >&2
exit 1
