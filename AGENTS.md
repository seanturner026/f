# f repo

My Claude Code config plus `f`, the multi-repo feature workspace CLI. What each part is: README.md. How it's used day to day: GUIDE.md.

## Workflow

- Commit each change, then push (`git push`) right away; don't let commits pile up locally.
- Keep this file up to date: when a change adds a command, a file contract, or a convention an agent needs, update it in the same commit.
- User-facing changes (commands, flags, keys, behavior) update GUIDE.md, and `f`'s help text in `cmd/f/main.go` if it lists them.

## Verify

- `just check`: go fmt, build, vet, test, and `claude plugin validate claude/f`.

## Deploy

- After a merge, `f sync` (and `f sync --all`) fast-forwards source clones that are on their default branch and clean (`feature.UpdateSources`); for this repo that runs `post-merge`, so a merged feature PR deploys within one launchd sync.
- Every commit (or pull) on `main` deploys in the background (`.githooks/`, `just deploy`): `go install ./cmd/f`, and copies of `claude/` to `~/.claude/f` and `nvim/f.lua` into nvim. Commits on other branches deploy nothing; `just deploy` installs any checkout by hand. Log: `$TMPDIR/f-deploy.log`. `just` lists every recipe.
- Go changes need `go install ./cmd/f`.

## Conventions

- One owner per fact; copies are for display only, never for decisions: plan steps → beads (`state.json` `plan` is a cache), PRs merged or not → GitHub (`state.json` `prs` is a cache; `merged_earlier` keeps merged PRs whose branches sync dropped, for display only via `State.AllPRs`), code → the worktrees. A beads step linked to PRs (`external_ref`, space-separated URLs) is closed by `f` when GitHub says they all merged (`closeMergedSteps`).
- Nothing employer-specific in this repo (it's headed for a public repo): machine- or org-specific settings go in `~/.config/f/config.json` (`home`, `repos`, `agent_rules`) or `guard.json`.
- Base clones come from `Layout.Sources` (`~/.config/f/config.json` `repos`), anywhere on disk: find one with `Layout.RepoPath`, never `<Base>/<repo>`. `Base` is f's home (`.features`, trash, beads). The mod lists features and repos with the hidden `f features` / `f repos`.
- `f` is the only writer of a feature's `.context/`; the Claude Code mod (`claude/f/`) and nvim (`nvim/f.lua`) read it or call `f`.
- Change `state.json` with `feature.UpdateState` (it locks), never `SaveState` alone.
- `feature.json` (`feature.Info`), `state.json`, and `f prs --json` (`model.PR`) are mirrored by hand in `claude/` (the Claude Code mod and statusline) and `nvim/f.lua`: change them together.
- `f` starts Claude Code (`internal/agent`). `claude/` (the `f` mod, statusline, settings) is linked to `~/.claude/f`. Check the mod with `claude plugin validate claude/f`.
- Packages that shell out declare their own small `Runner` interface; `shell.Exec` implements it, tests use fakes. New external calls go through it: `deps.run` wraps it in `tracing.Traced`, so every command is a span.
- Tracing (`internal/tracing`): one root span per `f` run, named after the command (`f sync`, `f checkpoint begin`; `f picker`/`f open`/`f start` via `tracing.Rename`). Step spans have fixed names with variable parts as attributes (`add repo` + `repo`), so `f trace stats` can group them. `~/.cache/f/traces.jsonl`: one OTLP/JSON `TracesData` line per root span (hex IDs), appended with one `O_APPEND` write; `f sync --all` trims it to 7 days. Detached `f` children get `TRACEPARENT` (`tracing.ChildEnv`); callers set `F_CALLER` (`claude`, `nvim`, `launchd`; `f` for detached; else `shell`). Paths that leave without returning (`syscall.Exec`, `os.Exit`) call `tracing.Finish`/`Shutdown` first.
- `state.json` `setup` (`feature.Setup`): repos `f setup` is still adding in the background; `feature.json` already lists them, the mod holds a turn until it's gone (`f setup-wait`), nvim review tabs wait for the worktree. `f setup` queues sync notes before `EndSetup`, so the agent gets them with the turn it was holding.
- `feature.Remove` moves a feature into `.features/.trash/<name>-<stamp>` (no unmerged-work check: restore is the undo); the detached `f finish-remove` then re-links its worktrees there (`feature.Relink`, `git worktree repair`), closes the beads issue. The picker closes removed features' Herdr workspaces only as it exits (`f close-workspaces`): a close can move focus and take the popup with it. Branches are kept; only `EmptyTrash` (after `TrashRetention`) prunes worktrees and deletes branches.
- "Merged" means in the default branch by ancestry or by content (`git.InBase`: squash merges), never just `head_oid` ancestry. Force pushes go through `git.ForcePushBranch` (refuses the default branch and branches outside the feature).
