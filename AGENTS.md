# pi repo

My pi config plus `f`, the multi-repo feature workspace CLI. What each part is: README.md. How it's used day to day: GUIDE.md.

## Workflow

- Commit each change, then push (`git push`) right away; don't let commits pile up locally.
- Keep this file up to date: when a change adds a command, a file contract, or a convention an agent needs, update it in the same commit.
- User-facing changes (commands, flags, keys, behavior) update GUIDE.md, and `f`'s help text in `cmd/f/main.go` if it lists them.

## Verify

- Go: `go build ./... && go vet ./... && go test ./...` (run `go fmt ./...` first).
- Extensions: `node --test agent/extensions/repo-instructions/gate.test.ts`.

## Deploy

- `install.sh` symlinks `agent/` into `~/.pi/agent` and `nvim/f.lua` into nvim, so edits are live after `/reload` in pi (or restarting nvim).
- Go changes need `go install ./cmd/f`.
- `agent/AGENTS.md` is my global pi rules (every pi session), not instructions for this repo.

## Conventions

- `f` is the only writer of a feature's `.context/`; pi (`agent/extensions/`) and nvim (`nvim/f.lua`) read it or call `f`.
- Change `state.json` with `feature.UpdateState` (it locks), never `SaveState` alone.
- `feature.json` (`feature.Info`), `state.json`, and `f prs --json` (`model.PR`) are mirrored by hand in `agent/extensions/f.ts`, `agent/extensions/repo-instructions/index.ts`, and `nvim/f.lua`: change them together.
- Packages that shell out declare their own small `Runner` interface; `shell.Exec` implements it, tests use fakes.
- "Merged" means in the default branch by ancestry or by content (`git.InBase`: squash merges), never just `head_oid` ancestry. Force pushes go through `git.ForcePushBranch` (refuses the default branch and branches outside the feature).
