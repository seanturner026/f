# pi

My config for [pi](https://pi.dev) and the feature-workspace setup around it
(Herdr + nvim + beads). **Day-to-day use: [GUIDE.md](GUIDE.md).** Design history: [PLAN.md](PLAN.md).

## Install

```bash
./install.sh                    # link pi config + nvim module, go install ./cmd/f
herdr integration install pi    # herdr agent-state extension (not tracked)
```

## Layout

- `agent/`: pi config (settings, AGENTS.md, prompts, extensions, skills, themes)
- `cmd/f/`: `f`, the multi-repo feature workspace CLI (`f --help`)
- `mockups/`: HTML layout mockups
- `docs/separation-of-concerns.excalidraw`: who owns what (Herdr, f, beads, pi, nvim)

## f

`f` starts one pi session that works across several repos, each on its own git
worktree (`f -h` for commands).

- Features live in `~/code/features/.features/<name>/<repo>` (branch =
  name; override the base with `F_BASE`). Feature state is local-only in
  `<feature>/.context/` (`feature.json`, PRs, comments), written only by `f`.
- Inside Herdr each feature gets a workspace: `chat · nvim · <repo>… · prs · shell`,
  each repo tab a live CodeDiff (`<leader>pr` toggles whole feature ↔ pi's last
  run); `prs` is `f prs --watch` (PRs + checks, polls GitHub only while checks
  run or just after a push). Outside Herdr, pi starts in the current terminal.
- Merged features are cleaned up from the picker (`alt-x`).
- Plumbing used by pi (`agent/extensions/f.ts`) and nvim (`nvim/f.lua`):
  `f goal`, `f comment`, `f checkpoint`, `f prs --json/--add`.

## Guard

pi never uses admin AWS profiles or production kube contexts
(`agent/extensions/guard.ts`, logic in `internal/guard`):

- At start, `f creds` writes filtered copies of `~/.aws/config`,
  `~/.aws/credentials`, and the kubeconfig to `~/.cache/f/guard/` and points
  pi's environment at them. Every command and script pi runs (aws, kubectl,
  terraform, boto3, ...) only sees allowed profiles/contexts.
- Before each bash command, `f guard check` blocks naming a denied
  profile/context/EKS cluster, or pointing back at the real files.
- Rules: generic defaults (deny profiles `*admin*`, `*prod*`; contexts
  `*prod*`) plus machine-specific `~/.config/f/guard.json`, e.g.
  `{"allow_profiles": ["team-eks-admin"], "default_profile": "team-read-only"}`.

## Beads

One beads db for all features: `~/code/features/.beads` (local, metrics off).

- Every feature gets a beads issue (`f <name>`; older features on next reopen),
  id in `.context/feature.json`. The goal (first prompt) is its description.
- Multi-step work: pi plans steps as child issues with dependencies and works
  them in `bd ready` order (instructions in the feature's AGENTS.md).
- The picker shows progress (`3/5 done · next: …`), synced after each pi run.
- Cleanup closes the issue (with merged PR links). If plan steps are still
  open, or the feature was removed with unmerged work, it stays open with a note.
- History: `bd list --status closed`.

