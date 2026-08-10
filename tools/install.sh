#!/bin/sh
# Installs the grpcview binary from a published grpcview release.
#
#   gh release download --repo OWNER/REPO -p install.sh -O - | sh   (private repo)
#   curl -fsSL @BASE_URL@/install.sh | sh                           (public, or a bucket)
#
# The default is the newest published version, installed as `grpcview` into the
# first writable directory of $GRPCVIEW_BIN_DIR, /usr/local/bin, ~/.local/bin.
# Every download is checked against the SHA256SUMS published alongside it.
#
# A GitHub release is downloaded with `gh` by default. That is not a
# convenience: assets of a private or internal repository are reachable only
# through the release asset API, which is keyed by numeric asset id, and the
# /releases/download/ URLs 404 for everyone — a valid token does not help,
# because they want a browser session. `gh` speaks that API and already holds
# the credentials. Once the repository is public those plain URLs start working
# and --no-gh needs nothing installed.
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
# 0 opts out of `gh` for a GitHub release and downloads over plain HTTP, which
# only works if the repository is public.
USE_GH=${GRPCVIEW_INSTALL_USE_GH:-1}

usage() {
    cat >&2 <<'EOF'
usage: install.sh [options]

  --version VERSION   version to install (default: the newest published one)
  --bin-dir DIR       install into DIR instead of the first writable default
  --base-url URL      release root, e.g. https://github.com/OWNER/REPO/releases/download
  --no-gh             download over plain HTTP instead of with the GitHub CLI
                      (a GitHub release only serves those URLs when public)
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
    --no-gh)
        USE_GH=0
        shift
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

# OWNER/REPO when this is a GitHub release, which is what `gh --repo` wants and
# what tells the two transports apart. Empty for a bucket, which has no repo and
# no private-asset problem.
GH_REPO=
case "$BASE_URL" in
https://github.com/*/*/releases/download)
    GH_REPO=${BASE_URL#https://github.com/}
    GH_REPO=${GH_REPO%/releases/download}
    ;;
esac

# An `if`, not an `&&` chain: under `set -eu` a short-circuited AND-list at top
# level exits the script with the status of its failed first test.
use_gh=false
if [ -n "$GH_REPO" ] && [ "$USE_GH" != 0 ]; then
    use_gh=true
fi

need_gh() {
    cat >&2 <<EOF
error: $1

grpcview releases live in the GitHub repository $GH_REPO. While it is private,
its assets are reachable only through the GitHub release API, so downloading
them needs the GitHub CLI:

  macOS:          brew install gh
  Debian/Ubuntu:  sudo apt install gh
  other:          https://github.com/cli/cli#installation

then authenticate once with

  gh auth login

If $GH_REPO is public, --no-gh (or GRPCVIEW_INSTALL_USE_GH=0) downloads over
plain HTTP instead and needs no CLI and no account.
EOF
    exit 1
}

if [ "$use_gh" = true ]; then
    command -v gh >/dev/null 2>&1 ||
        need_gh "the GitHub CLI (gh) is not installed"
    # Checked up front rather than left to the first download, whose failure
    # would otherwise read as a missing asset.
    gh auth status >/dev/null 2>&1 ||
        need_gh "the GitHub CLI is installed but not authenticated"
fi

if command -v curl >/dev/null 2>&1; then
    fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
    fetch() { wget -qO "$2" "$1"; }
elif [ "$use_gh" != true ]; then
    die "need curl or wget"
fi

# One asset of the release being installed, by name.
get() {
    if [ "$use_gh" = true ]; then
        gh release download "$VERSION" --repo "$GH_REPO" \
            --pattern "$1" --output "$2" --clobber
    else
        fetch "$BASE_URL/$VERSION/$1" "$2"
    fi
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
    if [ "$use_gh" = true ]; then
        # The release marked Latest, which is the same release the published
        # `latest` asset names — asking the API is just one fewer download.
        VERSION=$(gh release view --repo "$GH_REPO" --json tagName --jq .tagName) ||
            die "cannot read the latest release of $GH_REPO"
    else
        fetch "$ROOT_URL/latest" "$tmp/latest" ||
            die "cannot read $ROOT_URL/latest"
        VERSION=$(tr -d ' \t\r\n' <"$tmp/latest")
    fi
    [ -n "$VERSION" ] || die "no latest version published"
fi

if [ "$list_only" = true ]; then
    echo "version: $VERSION"
    if [ "$use_gh" = true ]; then
        echo "asset:   $asset from release $VERSION of $GH_REPO (via gh)"
    else
        echo "asset:   $BASE_URL/$VERSION/$asset"
    fi
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

# Write beside the target and rename, so an upgrade is atomic and does not hit
# ETXTBSY from overwriting a running binary in place.
staged="$BIN_DIR/.grpcview.install.$$"
cp "$tmp/$asset" "$staged"
chmod 0755 "$staged"
mv -f "$staged" "$BIN_DIR/grpcview"

echo "installed $BIN_DIR/grpcview"
if [ "$use_gh" = true ]; then
    echo "uninstall: gh release download --repo $GH_REPO -p uninstall.sh -O - | sh"
else
    echo "uninstall: curl -fsSL $ROOT_URL/uninstall.sh | sh"
fi

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
