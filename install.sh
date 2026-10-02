#!/usr/bin/env bash
# Symlink tracked config into place. Existing non-symlink files are backed up.
set -euo pipefail
repo="$(cd "$(dirname "$0")" && pwd)"

link() { # <source> <target>
  local src=$1 target=$2
  mkdir -p "$(dirname "$target")"
  if [ -L "$target" ]; then rm "$target"
  elif [ -e "$target" ]; then mv "$target" "$target.bak.$(date +%s)"; echo "backed up $target"; fi
  ln -s "$src" "$target"; echo "linked $target -> $src"
}

# pi: every entry in ./agent → ~/.pi/agent (or $PI_CODING_AGENT_DIR)
pi_dir="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}"
for item in "$repo"/agent/* "$repo"/agent/.[!.]*; do
  [ -e "$item" ] || continue
  link "$item" "$pi_dir/$(basename "$item")"
done

# herdr
link "$repo/herdr/config.toml" "$HOME/.config/herdr/config.toml"

# nvim: pi_comment (require("pi_comment").setup() in init.lua)
link "$repo/nvim/pi_comment.lua" "$HOME/.config/nvim/lua/pi_comment.lua"

# f (feature workspace CLI) → $(go env GOPATH)/bin/f
(cd "$repo" && go install ./cmd/f) && echo "installed f -> $(go env GOPATH)/bin/f"
