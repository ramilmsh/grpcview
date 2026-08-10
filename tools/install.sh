#!/bin/sh
# Installs the grpcview binary from a published grpcview release.
#
#   curl -fsSL @BASE_URL@/install.sh | sh
#
# The default is the version named by <base-url>/latest, installed as
# `grpcview` into the first writable directory of $GRPCVIEW_BIN_DIR,
# /usr/local/bin, ~/.local/bin. Every download is checked against the
# SHA256SUMS published alongside it.
#
# POSIX sh on purpose: this is piped into whatever /bin/sh the machine has, so
# it cannot assume bash.
set -eu

# tools/release.sh rewrites this when it uploads the script, so the copy in the
# bucket knows where the bucket is. Running the checked-in copy needs
# --base-url or $GRPCVIEW_INSTALL_BASE_URL instead.
BASE_URL=${GRPCVIEW_INSTALL_BASE_URL:-@BASE_URL@}
VERSION=${GRPCVIEW_VERSION:-latest}
BIN_DIR=${GRPCVIEW_BIN_DIR:-}

usage() {
    cat >&2 <<'EOF'
usage: install.sh [options]

  --version VERSION   version to install (default: whatever <base-url>/latest names)
  --bin-dir DIR       install into DIR instead of the first writable default
  --base-url URL      release root, e.g. https://github.com/OWNER/REPO/releases/download
  --list              print the resolved version and asset, install nothing
EOF
    exit 2
}

list_only=false

while [ $# -gt 0 ]; do
    case "$1" in
    --version)
        VERSION=${2:-}
        shift 2
        ;;
    --bin-dir)
        BIN_DIR=${2:-}
        shift 2
        ;;
    --base-url)
        BASE_URL=${2:-}
        shift 2
        ;;
    --list)
        list_only=true
        shift
        ;;
    -h | --help) usage ;;
    *)
        echo "unknown argument: $1" >&2
        usage
        ;;
    esac
done

die() {
    echo "error: $*" >&2
    exit 1
}

# Tested by scheme rather than against the placeholder: release.sh substitutes
# every occurrence of the placeholder, so a pattern naming it here would be
# rewritten into a pattern matching the real URL.
case "$BASE_URL" in
http://* | https://*) ;;
*) die "no release URL; pass --base-url or set GRPCVIEW_INSTALL_BASE_URL" ;;
esac
BASE_URL=${BASE_URL%/}

# Where the version pointer and the sibling scripts live. A bucket keeps them at
# the release root next to the version directories. GitHub has no release root —
# every object is an asset of some tag — but it serves the newest release's
# assets under a fixed /releases/latest/download/ path, so the same `latest`,
# `install.sh` and `uninstall.sh` are readable there. Per-version asset URLs are
# `<base>/<tag>/<asset>` under either layout, so only this root differs.
case "$BASE_URL" in
*/releases/download) ROOT_URL="${BASE_URL%/download}/latest/download" ;;
*) ROOT_URL=$BASE_URL ;;
esac

if command -v curl >/dev/null 2>&1; then
    fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
    fetch() { wget -qO "$2" "$1"; }
else
    die "need curl or wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
    die "need sha256sum or shasum"
fi

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

asset="grpcview_${os}_${arch}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ "$VERSION" = latest ]; then
    fetch "$ROOT_URL/latest" "$tmp/latest" ||
        die "cannot read $ROOT_URL/latest"
    VERSION=$(tr -d ' \t\r\n' <"$tmp/latest")
    [ -n "$VERSION" ] || die "$ROOT_URL/latest is empty"
fi

if [ "$list_only" = true ]; then
    echo "version: $VERSION"
    echo "asset:   $BASE_URL/$VERSION/$asset"
    exit 0
fi

if [ -z "$BIN_DIR" ]; then
    for candidate in /usr/local/bin "$HOME/.local/bin"; do
        if [ -w "$candidate" ]; then
            BIN_DIR=$candidate
            break
        fi
    done
    # Neither exists-and-is-writable, so fall back to the one this script may
    # create itself. /usr/local/bin needs root, ~/.local/bin does not.
    BIN_DIR=${BIN_DIR:-$HOME/.local/bin}
fi

echo "==> grpcview $VERSION ($os/$arch) -> $BIN_DIR/grpcview"

fetch "$BASE_URL/$VERSION/$asset" "$tmp/$asset" ||
    die "cannot download $BASE_URL/$VERSION/$asset"
fetch "$BASE_URL/$VERSION/SHA256SUMS" "$tmp/SHA256SUMS" ||
    die "cannot download $BASE_URL/$VERSION/SHA256SUMS"

expected=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$tmp/SHA256SUMS")
[ -n "$expected" ] || die "SHA256SUMS has no entry for $asset"

actual=$(sha256 "$tmp/$asset")
[ "$expected" = "$actual" ] ||
    die "checksum mismatch for $asset: expected $expected, got $actual"

mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
[ -w "$BIN_DIR" ] || die "$BIN_DIR is not writable; re-run with --bin-dir DIR, or as root"

# Write beside the target and rename, so an upgrade is atomic and does not hit
# ETXTBSY from overwriting a running binary in place.
staged="$BIN_DIR/.grpcview.install.$$"
cp "$tmp/$asset" "$staged"
chmod 0755 "$staged"
mv -f "$staged" "$BIN_DIR/grpcview"

echo "installed $BIN_DIR/grpcview"
echo "uninstall: curl -fsSL $ROOT_URL/uninstall.sh | sh"

# $SHELL is the user's login shell, which is what needs the PATH entry — not
# whatever /bin/sh is running this script. fish does not understand `export
# PATH=`, so printing POSIX syntax to a fish user is advice that silently does
# nothing.
path_hint() {
    echo "note: $BIN_DIR is not on PATH; add it with" >&2
    case "${SHELL##*/}" in
    fish)
        # Persists on its own via the fish_user_paths universal variable, so
        # there is no rc file to edit.
        echo "  fish_add_path $BIN_DIR" >&2
        ;;
    zsh)
        printf '  export PATH="%s:$PATH"    # add to ~/.zshrc to persist\n' "$BIN_DIR" >&2
        ;;
    bash)
        case "$(uname -s)" in
        Darwin) rc=~/.bash_profile ;;
        *) rc=~/.bashrc ;;
        esac
        printf '  export PATH="%s:$PATH"    # add to %s to persist\n' "$BIN_DIR" "$rc" >&2
        ;;
    tcsh | csh)
        printf '  setenv PATH "%s:$PATH"    # add to ~/.cshrc to persist\n' "$BIN_DIR" >&2
        ;;
    *)
        printf '  export PATH="%s:$PATH"\n' "$BIN_DIR" >&2
        ;;
    esac
}

resolved=$(command -v grpcview 2>/dev/null || true)
if [ "$resolved" != "$BIN_DIR/grpcview" ]; then
    if [ -n "$resolved" ]; then
        echo "note: \`grpcview\` on PATH is $resolved, which shadows the copy just installed" >&2
    else
        path_hint
    fi
fi
