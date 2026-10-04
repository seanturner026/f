# pi

My config for [pi](https://pi.dev) and the feature-workspace setup around it
(Herdr + nvim + beads). See [PLAN.md](PLAN.md).

## Install

```bash
./install.sh                    # link pi config + nvim module, go install ./cmd/f
herdr integration install pi    # herdr agent-state extension (not tracked)
```

## Layout

- `agent/`: pi config (settings, AGENTS.md, prompts, extensions, skills, themes)
- `cmd/f/`: `f`, the multi-repo feature workspace CLI (`f --help`)
- `mockups/`: HTML layout mockups

## f

`f` starts one pi session that works across several repos, each on its own git
worktree (`f -h` for commands).

- Features live in `~/code/features/.features/<name>/<repo>` (branch =
  name; override the base with `F_BASE`). Feature state is local-only in
  `<feature>/.context/` (`feature.json`, PRs, comments), written only by `f`.
- Inside Herdr each feature gets a workspace: `chat · nvim · <repo>… · shell`,
  each repo tab a live CodeDiff (`<leader>pr` toggles whole feature ↔ pi's last
  run). Outside Herdr, pi starts in the current terminal.
- Starting a feature offers to clean up merged ones.
- Plumbing used by pi (`agent/extensions/f.ts`) and nvim (`nvim/f.lua`):
  `f goal`, `f comment`, `f checkpoint`, `f prs --json/--add`.

