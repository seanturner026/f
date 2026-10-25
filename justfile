set quiet := true

repo := justfile_directory()
launchd_label := "com.seanturner026.f-sync"
plist := home_directory() / "Library/LaunchAgents" / launchd_label + ".plist"
sync_log := home_directory() / "Library/Logs/f-sync.log"

[private]
default:
    just --list --alias-style left --list-heading ''

# install
# ------------------------------------------------------------------------------

alias i := install
alias b := install-f
alias d := deploy

[doc("Everything: deploy, Herdr plugin, launchd sync, git hooks (deploy on commits to main)")]
[group("install")]
install: deploy herdr-plugin launchd hooks

[doc("Install this checkout: f, plus copies of the Claude Code mod/statusline/settings and nvim's f.lua")]
[group("install")]
deploy: install-f
    #!/usr/bin/env bash
    set -euo pipefail
    # Copies, not symlinks: the installed version is what this checkout had when
    # deployed, whatever any clone has checked out later.
    copy() { # <source> <target>
      local src=$1 target=$2
      [ -L "$target" ] && rm "$target" # earlier installs symlinked into a clone
      mkdir -p "$(dirname "$target")"
      if [ -d "$src" ]; then
        # The engine writes the mod's .claude-plugin/types/ itself.
        rsync -a --delete --exclude '.claude-plugin/types/' "$src/" "$target/"
      else
        cp "$src" "$target"
      fi
    }
    copy "{{ repo }}/claude" "$HOME/.claude/f"
    copy "{{ repo }}/nvim/f.lua" "$HOME/.config/nvim/lua/f.lua"
    echo "deployed $(git -C "{{ repo }}" rev-parse --short HEAD) (Claude Code mod, statusline, nvim f.lua; restart nvim)"

[doc("Link the Herdr plugin (the f picker popup, ctrl+space)")]
[group("install")]
herdr-plugin:
    if command -v herdr >/dev/null; then herdr plugin link "{{ repo }}/herdr-plugin" >/dev/null && echo "linked herdr plugin f"; fi

[doc("Deploy after every commit or pull on main (git hooks in .githooks/)")]
[group("install")]
hooks:
    git -C "{{ repo }}" config core.hooksPath .githooks && echo "git hooks: .githooks (deploy on main)"

[doc("go install ./cmd/f")]
[group("install")]
install-f:
    cd "{{ repo }}" && go install ./cmd/f && echo "installed f -> $(go env GOPATH)/bin/f"

[doc("Herdr's agent-state hooks for Claude Code (busy/idle marks, picker order)")]
[group("install")]
herdr-integration:
    herdr integration install claude

# sync (launchd: f sync --all every 30 min)
# ------------------------------------------------------------------------------

[doc("Write and (re)load the launchd job: f sync --all every 30 min")]
[group("sync")]
launchd:
    #!/usr/bin/env bash
    set -euo pipefail
    # Run via /bin/sh: macOS tracks a background item by its program's signature,
    # and go install re-signs f each build ("Background Items Added" every time).
    mkdir -p "$(dirname "{{ plist }}")"
    cat > "{{ plist }}" <<PLIST
    <?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
    <plist version="1.0">
    <dict>
      <key>Label</key><string>{{ launchd_label }}</string>
      <key>ProgramArguments</key>
      <array><string>/bin/sh</string><string>-c</string><string>exec f sync --all</string></array>
      <key>EnvironmentVariables</key>
      <dict><key>PATH</key><string>$(go env GOPATH)/bin:$(dirname "$(command -v go)"):$(dirname "$(command -v just)"):$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string><key>F_CALLER</key><string>launchd</string></dict>
      <key>StartInterval</key><integer>1800</integer>
      <key>StandardOutPath</key><string>{{ sync_log }}</string>
      <key>StandardErrorPath</key><string>{{ sync_log }}</string>
    </dict>
    </plist>
    PLIST
    launchctl bootout "gui/$(id -u)/{{ launchd_label }}" 2>/dev/null || true
    launchctl bootstrap "gui/$(id -u)" "{{ plist }}" && echo "loaded launchd {{ launchd_label }} (every 30 min)"

[doc("Run the launchd sync now instead of waiting for the next 30 min")]
[group("sync")]
sync-now:
    launchctl kickstart -k "gui/$(id -u)/{{ launchd_label }}" && echo "started; just sync-log to follow"

[doc("Follow the launchd sync's log")]
[group("sync")]
sync-log:
    tail -f "{{ sync_log }}"

[confirm("Stop the 30-min f sync --all and remove its launchd job?")]
[doc("Unload and remove the launchd job")]
[group("sync")]
launchd-remove:
    launchctl bootout "gui/$(id -u)/{{ launchd_label }}" 2>/dev/null || true
    rm -f "{{ plist }}" && echo "removed {{ plist }}"

# dev
# ------------------------------------------------------------------------------

alias t := test
alias c := check

[doc("go fmt")]
[group("dev")]
fmt:
    cd "{{ repo }}" && go fmt ./...

[doc("go build + go vet")]
[group("dev")]
vet:
    cd "{{ repo }}" && go build ./... && go vet ./...

[doc("go test")]
[group("dev")]
test:
    cd "{{ repo }}" && go test ./...

[doc("Validate the Claude Code mod (claude/f)")]
[group("dev")]
validate-mod:
    claude plugin validate "{{ repo }}/claude/f"

[doc("What AGENTS.md's Verify runs: fmt, build, vet, tests, mod validation")]
[group("dev")]
check: fmt vet test validate-mod

# trace
# ------------------------------------------------------------------------------

[doc("Open f's traces in Jaeger (Docker), last `since` (e.g. 1h, 72h)")]
[group("trace")]
trace since="24h":
    f trace --since {{ since }}

[doc("f's traces as text: per-span p50/p95/max, slowest runs")]
[group("trace")]
trace-stats since="24h":
    f trace stats --since {{ since }}

[doc("Stop the Jaeger container")]
[group("trace")]
trace-stop:
    f trace stop
