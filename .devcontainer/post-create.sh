#!/usr/bin/env bash
set -eu


sudo mkdir -p /home/vscode/.history
sudo touch /home/vscode/.history/.bash_history

sudo chown -R vscode:vscode "$HOME"

if command -v npm >/dev/null 2>&1; then
  npm_prefix="$(npm config get prefix)"
  [ -d "$npm_prefix" ] && sudo chown -R vscode:vscode "$npm_prefix"
fi

bashrc="$HOME/.bashrc"
hook_line='eval "$(direnv hook bash)"'
history_line='PROMPT_COMMAND="history -a; ${PROMPT_COMMAND:-}"'

grep -qxF "$hook_line" "$bashrc" 2>/dev/null || echo "$hook_line" >>"$bashrc"
grep -qxF "$history_line" "$bashrc" 2>/dev/null || echo "$history_line" >>"$bashrc"

bazel run //:bazel_env &>/dev/null || true

direnv allow
