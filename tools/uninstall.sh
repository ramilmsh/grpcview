#!/bin/sh
# Removes the grpcview binary, and with --purge its local state directory too.
#
#   curl -fsSL @BASE_URL@/uninstall.sh | sh
#
# tools/release.sh substitutes @BASE_URL@ above at upload time, the same as it
# does for install.sh. Nothing here fetches anything, so an unsubstituted copy
# still runs fine.
#
# Without --purge this only deletes binaries. The state directory is
# $XDG_CONFIG_HOME/grpcview (Linux) or ~/Library/Application Support/grpcview
# (macOS) — the same os.UserConfigDir the binary itself uses. It holds the
# workspace trust list, cached descriptor blobs, and run history. Collections,
# requests, and scripts live in your repositories, not there, so they survive a
# purge; run history does not.
#
# POSIX sh, like install.sh: this runs under whatever /bin/sh is on the machine.
set -eu

BIN_DIR=${GRPCVIEW_BIN_DIR:-}
purge=false
assume_yes=false
dry_run=false
force=false

usage() {
    cat >&2 <<'EOF'
usage: uninstall.sh [options]

  --purge         also delete the state directory (trust list, descriptor
                  cache, run history) — repositories are never touched
  --bin-dir DIR   only look for the binary in DIR
  --dry-run       print what would be deleted, delete nothing
  --force         delete symlinks, and a state directory that does not look
                  like grpcview's
  --yes           skip the confirmation prompt
EOF
    exit 2
}

while [ $# -gt 0 ]; do
    case "$1" in
    --purge)
        purge=true
        shift
        ;;
    --bin-dir)
        BIN_DIR=${2:-}
        shift 2
        ;;
    --dry-run)
        dry_run=true
        shift
        ;;
    --force)
        force=true
        shift
        ;;
    --yes | -y)
        assume_yes=true
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

# Newline-separated, because the paths may contain spaces and POSIX sh has no
# arrays. Every loop over these sets IFS to a newline.
NL='
'
targets=
skipped=

add_target() {
    case "$NL$targets$NL" in
    *"$NL$1$NL"*) return 0 ;;
    esac
    targets="${targets:+$targets$NL}$1"
}

add_skipped() {
    skipped="${skipped:+$skipped$NL}$1"
}

consider() {
    candidate=$1
    [ -e "$candidate" ] || [ -L "$candidate" ] || return 0
    # A symlink is how another tool — Homebrew, Bazel, a dotfile manager — owns
    # a name in bin. Removing it would break something this script did not
    # install, so it takes --force.
    if [ -L "$candidate" ] && [ "$force" != true ]; then
        add_skipped "$candidate (symlink; --force to remove)"
        return 0
    fi
    if [ -d "$candidate" ]; then
        add_skipped "$candidate (directory)"
        return 0
    fi
    add_target "$candidate"
}

if [ -n "$BIN_DIR" ]; then
    consider "${BIN_DIR%/}/grpcview"
else
    for dir in /usr/local/bin "$HOME/.local/bin" /opt/homebrew/bin "$HOME/bin"; do
        consider "$dir/grpcview"
    done
    # Whatever the shell would actually run, which is the copy the user cares
    # about even if it lives somewhere this script would not have guessed.
    on_path=$(command -v grpcview 2>/dev/null || true)
    case "$on_path" in
    /*) consider "$on_path" ;;
    esac
fi

state_root=
if [ "$purge" = true ]; then
    case "$(uname -s)" in
    Darwin) config_dir="$HOME/Library/Application Support" ;;
    *) config_dir=${XDG_CONFIG_HOME:-$HOME/.config} ;;
    esac
    case "$config_dir" in
    /*) ;;
    *) die "config directory $config_dir is not an absolute path" ;;
    esac

    candidate="$config_dir/grpcview"
    # Guards against a purge widening to something catastrophic if HOME or
    # XDG_CONFIG_HOME is empty or odd. The path must be a *nested* directory
    # literally named grpcview, and must not be the home directory itself.
    case "$candidate" in
    */grpcview) ;;
    *) die "refusing to delete $candidate: not a grpcview state directory" ;;
    esac
    case "$candidate" in
    / | /grpcview | "$HOME" | "$HOME"/)
        die "refusing to delete $candidate"
        ;;
    esac

    if [ -d "$candidate" ]; then
        if [ -e "$candidate/trust.json" ] || [ -d "$candidate/workspaces" ] || [ "$force" = true ]; then
            state_root=$candidate
        else
            die "$candidate has no trust.json or workspaces/; --force to delete it anyway"
        fi
    else
        echo "note: no state directory at $candidate"
    fi
fi

if [ -z "$targets" ] && [ -z "$state_root" ]; then
    echo "nothing to remove"
    if [ -n "$skipped" ]; then
        printf 'skipped:\n' >&2
        printf '  %s\n' "$skipped" >&2
    fi
    exit 0
fi

echo "will delete:"
if [ -n "$targets" ]; then
    IFS=$NL
    for t in $targets; do
        echo "  $t"
    done
    unset IFS
fi
[ -n "$state_root" ] && echo "  $state_root/ (trust list, descriptor cache, run history)"
if [ -n "$skipped" ]; then
    echo "skipped:"
    IFS=$NL
    for s in $skipped; do
        echo "  $s"
    done
    unset IFS
fi
echo

if [ "$dry_run" = true ]; then
    echo "dry run: nothing deleted"
    exit 0
fi

if [ "$assume_yes" != true ]; then
    # stdin is the curl pipe when this script is piped to sh, so the prompt has
    # to come from the terminal. With no terminal there is nobody to confirm to,
    # and this deletes things, so it stops rather than assuming yes. The open is
    # tested in a subshell because /dev/tty can pass `[ -r ]` and still fail to
    # open ("Device not configured"), and a failed redirection on the main shell
    # would kill the script with a raw error instead of this message.
    if ! (: </dev/tty) 2>/dev/null; then
        die "not interactive; re-run with --yes to confirm"
    fi
    printf 'proceed? [y/N] ' >/dev/tty
    read -r reply </dev/tty || reply=
    case "$reply" in
    y* | Y*) ;;
    *) die "aborted" ;;
    esac
fi

failed=false
if [ -n "$targets" ]; then
    IFS=$NL
    for t in $targets; do
        if rm -f "$t"; then
            echo "removed $t"
        else
            echo "error: could not remove $t (try as root)" >&2
            failed=true
        fi
    done
    unset IFS
fi

if [ -n "$state_root" ]; then
    if rm -rf "$state_root"; then
        echo "removed $state_root/"
    else
        echo "error: could not remove $state_root/" >&2
        failed=true
    fi
fi

[ "$failed" != true ] || exit 1

if [ "$purge" != true ]; then
    echo "state kept; re-run with --purge to delete it too"
fi
