#!/bin/sh
set -eu

BASE_URL=${GRPCVIEW_INSTALL_BASE_URL:-@BASE_URL@}
VERSION=${GRPCVIEW_VERSION:-latest}
BIN_DIR=${GRPCVIEW_BIN_DIR:-}

usage() {
    cat >&2 <<'EOF'
usage: install.sh [options]

  --version VERSION   version to install (default: the newest published one)
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

case "$BASE_URL" in
http://* | https://*) ;;
*) die "no release URL; pass --base-url or set GRPCVIEW_INSTALL_BASE_URL" ;;
esac
BASE_URL=${BASE_URL%/}

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

get() {
    fetch "$BASE_URL/$VERSION/$1" "$2"
}

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
    [ -n "$VERSION" ] || die "no latest version published"
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
    BIN_DIR=${BIN_DIR:-$HOME/.local/bin}
fi

echo "==> grpcview $VERSION ($os/$arch) -> $BIN_DIR/grpcview"

get "$asset" "$tmp/$asset" ||
    die "cannot download $asset of $VERSION"
get SHA256SUMS "$tmp/SHA256SUMS" ||
    die "cannot download SHA256SUMS of $VERSION"

expected=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$tmp/SHA256SUMS")
[ -n "$expected" ] || die "SHA256SUMS has no entry for $asset"

actual=$(sha256 "$tmp/$asset")
[ "$expected" = "$actual" ] ||
    die "checksum mismatch for $asset: expected $expected, got $actual"

mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
[ -w "$BIN_DIR" ] || die "$BIN_DIR is not writable; re-run with --bin-dir DIR, or as root"

staged="$BIN_DIR/.grpcview.install.$$"
cp "$tmp/$asset" "$staged"
chmod 0755 "$staged"
mv -f "$staged" "$BIN_DIR/grpcview"

echo "installed $BIN_DIR/grpcview"
echo "uninstall: curl -fsSL $ROOT_URL/uninstall.sh | sh"

path_hint() {
    echo "note: $BIN_DIR is not on PATH; add it with" >&2
    case "${SHELL##*/}" in
    fish)
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
