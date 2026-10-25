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

# Claude Code (F_AGENT=claude): settings, statusline and the f mod → ~/.claude/f
link "$repo/claude" "$HOME/.claude/f"

# nvim: f (require("f").setup() in init.lua)
link "$repo/nvim/f.lua" "$HOME/.config/nvim/lua/f.lua"

# Herdr plugin: the f picker popup (bound to ctrl+space in herdr/config.toml)
command -v herdr >/dev/null && herdr plugin link "$repo/herdr-plugin" >/dev/null && echo "linked herdr plugin f"

# f (feature workspace CLI) → $(go env GOPATH)/bin/f
(cd "$repo" && go install ./cmd/f) && echo "installed f -> $(go env GOPATH)/bin/f"

# launchd: f sync --all every 30 min (PR/CI refresh for the picker; repos reset
# after their PR merged; never pushes). Log: ~/Library/Logs/f-sync.log
# Run via /bin/sh: macOS tracks a background item by its program's signature,
# and go install re-signs f each build ("Background Items Added" every time).
label=com.seanturner026.f-sync
plist="$HOME/Library/LaunchAgents/$label.plist"
mkdir -p "$(dirname "$plist")"
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array><string>/bin/sh</string><string>-c</string><string>exec f sync --all</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>$(go env GOPATH)/bin:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string></dict>
  <key>StartInterval</key><integer>1800</integer>
  <key>StandardOutPath</key><string>$HOME/Library/Logs/f-sync.log</string>
  <key>StandardErrorPath</key><string>$HOME/Library/Logs/f-sync.log</string>
</dict>
</plist>
PLIST
launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$plist" && echo "loaded launchd $label (every 30 min)"
