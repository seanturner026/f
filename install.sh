#!/usr/bin/env bash
# Symlink tracked config from ./agent into ~/.pi/agent (or $PI_CODING_AGENT_DIR).
set -euo pipefail
src="$(cd "$(dirname "$0")/agent" && pwd)"
dest="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}"
mkdir -p "$dest"
for item in "$src"/* "$src"/.[!.]*; do
  [ -e "$item" ] || continue
  name="$(basename "$item")"
  target="$dest/$name"
  if [ -L "$target" ]; then
    rm "$target"
  elif [ -e "$target" ]; then
    mv "$target" "$target.bak.$(date +%s)"
    echo "backed up existing $target"
  fi
  ln -s "$item" "$target"
  echo "linked $target -> $item"
done
