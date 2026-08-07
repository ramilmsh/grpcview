#!/usr/bin/env bash
# Brings up the two servers the `example` collection talks to and holds until interrupted.
# Invoked as `bazel run //example:up`; the echo, dev-server and CLI binary paths arrive as $1..$3,
# expanded by the target's `args`.
set -euo pipefail

echo_bin=$1
dev_bin=$2
cli_bin=$3
shift 3

echo_port=50055
dev_port=10000
isolated=0
tmp_state=""
echo_pid=""
dev_pid=""

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
    --dev-port)
      dev_port=$2
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
  [[ -n $dev_pid ]] && kill "$dev_pid" 2>/dev/null
  wait 2>/dev/null
  [[ -n $tmp_state ]] && rm -rf "$tmp_state"
  return 0
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# The dev server writes run history to a per-workspace state dir. A CI or throwaway run must not
# land in the developer's real one. GRPCVIEW_CONFIG_DIR moves grpcview's state and nothing else —
# overriding HOME would also move the output base of the bazel build below, into a temp dir that
# is then deleted.
#
# That state holds the resolve caches too, so an isolated run starts with none. The example's
# bazel source commits no descriptors — deliberately, it is what demonstrates the uncommitted
# mode — so it is built here, before the dev server reads the collection. The reflection source
# is left alone: it points at the dev server this script has not started yet. Trust lives in the
# same discarded state, so granting it covers only the throwaway copy, for a repo whose bazel
# targets the caller is already running.
#
# A GRPCVIEW_CONFIG_DIR already in the environment is used as-is and kept, so a CI job can export
# one, run this, and point its own `grpcview` invocations at the same state.
if [[ $isolated == 1 ]]; then
  if [[ -z ${GRPCVIEW_CONFIG_DIR:-} ]]; then
    tmp_state=$(mktemp -d)
    export GRPCVIEW_CONFIG_DIR="$tmp_state"
  fi
  echo "example:up: isolated state under $GRPCVIEW_CONFIG_DIR" >&2

  echo "example:up: building the example's bazel definition source" >&2
  "$cli_bin" trust
  "$cli_bin" sources refresh bazel://proto/echo/v1:echov1_proto --collection example
fi

# bazel run leaves the cwd in the runfiles tree; the dev server resolves its workspace from
# BUILD_WORKSPACE_DIRECTORY, which bazel already exports.
"$echo_bin" -port "$echo_port" &
echo_pid=$!
"$dev_bin" -port "$dev_port" &
dev_pid=$!

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
await_port "$dev_port" "the grpcview dev server"

cat >&2 <<EOF
example:up: echo.v1.EchoService on 127.0.0.1:$echo_port
example:up: grpcview on localhost:$dev_port (its own API, by reflection)
example:up: ready — run \`grpcview script run smoke --collection example\`. Ctrl-C to stop.
EOF

# Either child exiting means the pair is no longer up; do not outlive one. Polled rather than
# `wait -n`, which macOS's system bash does not have.
while kill -0 "$echo_pid" 2>/dev/null && kill -0 "$dev_pid" 2>/dev/null; do
  sleep 1
done
echo "example:up: a server exited; shutting the other one down" >&2
exit 1
