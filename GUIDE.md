# Operator guide

How to run the pi + Herdr + nvim + beads setup day to day. The README covers
what each part is; this covers how to use it.

## The pieces

| Piece | Job |
|---|---|
| `f` | Creates and owns features: worktrees, branches, `AGENTS.md`, `.context/`, Herdr layout, cleanup, beads issue |
| Herdr | Terminals: one workspace per feature, agent status |
| pi | The agent. Extensions: `f.ts` (goal, checkpoints, PRs, plan), `guard.ts` (credentials), `repo-instructions/` (must read a repo's CLAUDE.md/AGENTS.md in full before touching it), `compact-read.ts` (one-line reads), `copy-code.ts` (`/code`) |
| nvim | Review: one live CodeDiff tab per repo, comments for pi (`nvim/f.lua`) |
| beads (`bd`) | The plan: one issue per feature, child issues for steps |

A feature is `~/code/features/.features/<name>/`: one worktree per
repo (branch `<name>`), `AGENTS.md`, and `.context/` (local state, written only
by `f`).

## Daily flow

1. **Start**: from any Herdr shell (`ctrl+b t` popup, or a shell tab):
   `f <name>` → pick repos (TAB selects, filter clears) → Herdr opens the
   workspace, pi waiting. Type your first prompt; it becomes the goal and the
   beads issue description. Use `@` to reference files across repos.
2. **Direction**: pi asks questions, then proposes. Approve or steer.
3. **Review**: `opt+3…` repo tabs show a live CodeDiff of the whole feature.
   `<leader>pr` toggles to *just pi's last run*. Comment with `<leader>pc`,
   send them all with `<leader>ps`. Small fixes: edit the right-hand side
   directly and tell pi ("I edited X").
4. **PRs**: pi opens them; they show in pi's footer with CI (`✓` `⋯` `✗`), and the
   `prs` tab lists each PR's failing/running checks. `!!f prs -c` copies
   `url: title` lines (stacks numbered 1/3, 2/3, …).
5. **Switch**: `ctrl+space` → picker, ordered by what needs you.
6. **Merged**: the repo resets to main by itself (on open, `r` in prs, `/sync`, or tell
   pi "merged"), so more work in the feature starts a fresh PR.
7. **Done**: when PRs merge, the next `f <new>` offers cleanup, or `alt-x` in
   the picker. The beads issue closes with the PR links.

Big multi-step work (e.g. ACK across tf/argocd/prod/ops): start one
feature with all the repos, ask pi to **plan it in beads** (child issues +
dependencies), then let it work through `bd ready`. Progress shows in the
picker (`3/5 done · next: …`).

## Keys

### Herdr

| Key | Does |
|---|---|
| `ctrl+space` | Feature picker (press again to close) |
| `cmd+ctrl+c/v/p/t` | Tab: chat · nvim · prs · shell (WezTerm → ctrl+alt → `f tab`) |
| `cmd+ctrl+a s e r g` | Repo tabs 1–5 (d/f are taken by macOS). Each tab label starts with its key (`a:argocd`) |
| `opt+1…9` | Tab: `1` chat · `2` nvim · `3…` repos · then prs · last shell |
| `ctrl+b t` | Popup shell (`exit` / `ctrl+d` closes it) |
| `ctrl+b w` | Herdr's own workspace list |
| `ctrl+b b` | Toggle the sidebar (hidden by default) |
| `ctrl+b shift+d` | Close this workspace (pi's session is kept) |
| `ctrl+b q` | Detach (everything keeps running; `herdr` reattaches) |

### PRs tab (`f prs --watch`)

| Key | Does |
|---|---|
| `r` | Refresh now (a re-run on GitHub, a merge) |
| `f` | Fix CI: send failing checks + logs to this feature's pi (same as `/ci`) |
| `q` | Quit (back to a shell; `f prs -w` restarts it) |

Each open PR lists its failing (with link) and running checks; passed and
skipped are a count. It asks GitHub every minute only while checks run or for
5 minutes after a push or new PR; otherwise it just watches for pushes. What it
learns shows in the picker too.

### Picker (`ctrl+space` or bare `f`)

| Key | Does |
|---|---|
| `enter` | Jump to the feature (recreates its workspace if closed) |
| `ctrl-x` | Remove the feature: worktrees, branches, workspace. Asks if work would be lost |
| `alt-x` | Clean up every merged feature |
| `esc` / `ctrl-space` | Close |

Columns: `●` marks the workspace you're in (listed first; the cursor starts on the next one) ·
FEATURE · PI status · CI of open PRs (`✓`, `⋯ running`, `✗ <repos>`) · PRS ·
PLAN (beads steps).

Order: **needs you** (pi asked something, or CI failing) → **done** (finished,
unseen) → **working** → **idle** → **closed** (no pi running).

### pi

| Key / command | Does |
|---|---|
| `/tree` then `ctrl+u` | List just your prompts; pick one to jump/branch |
| `ctrl+alt+up/down` | Jump between prompts in the transcript |
| `!!f prs -c` | Copy PR links (`!!` = run without sending output to the model) |
| `/prs` | Refresh PRs, open the open ones in the browser |
| `/code [n]` | Copy code block(s) from the last reply exactly as written (no wrap/indent); several: pick one or all. `/copy` (ctrl+x) copies the whole reply |
| `/f <name> [repo…]` | Open a feature or start one without leaving chat (tab completes names and repos; asks for repos if new and none given) |
| `/sync` | After a PR merges: reset its repo to main (rebase newer commits onto it); also runs when you open the feature and on `r` in the prs tab |
| `/ci` | Send failing checks + failed-job logs to pi: it finds the cause and proposes a fix, no push until you OK |
| `/remember [what]` | Save a preference as a rule in `agent/AGENTS.md` |
| `/reload` | Pick up changed extensions, rules, keybindings |
| `ctrl+d` (empty editor) / `/quit` | Quit pi (session is saved; `f <name>` continues it) |

### nvim (review tabs and anywhere in a feature)

| Key | Does |
|---|---|
| `]f` / `[f` | Next / previous file (CodeDiff) |
| `]c` / `[c` | Next / previous change |
| `<leader>b` | Toggle the file list; `<leader>e` focuses it |
| `gc` | Compact: only changes + context |
| `t` | Side-by-side ↔ inline |
| `gf` | Open the file in a normal tab |
| `g?` | All CodeDiff keys |
| `<leader>pr` | Whole feature ↔ since pi's last run |
| `<leader>pc` | Comment on line/selection (normal or visual) |
| `<leader>pl` | List pending comments (quickfix) |
| `<leader>ps` | Send comments to pi |
| `:PiClear` | Discard pending comments |
| `:PiReview [repo]` | Reopen a repo's CodeDiff (no repo: all, as nvim tabs) |

Comment on the **right** side of a diff (the real file). Comments are saved in
`.context/comments.json`, so they survive closing nvim.

## f commands

| Command | Does |
|---|---|
| `f <name>` | Start a feature, or reopen it (continues pi's session) |
| `f` | Picker |
| `f add [repo…]` | Add repos to this feature (+ Herdr tabs, before shell) |
| `f prs [-r] [-c] [-n] [-w]` | PRs: from saved state; `-r` refresh live, `-c` copy, `-n` don't open browser, `-w` live view (the prs tab) |
| `f sync --all` | What launchd runs every 30 min: PR/CI refresh + post-merge reset for every feature except the one you're in or a busy pi; never pushes; pi gets a note with its next prompt. Log: `~/Library/Logs/f-sync.log` |
| `f sync` | Same as `/sync`. Leaves repos with uncommitted changes or conflicts alone (says so) |
| `f ci fix` | Send failing checks + failed-job logs to the feature's pi (clipboard if pi isn't reachable) |
| `f goal [text]` | Show / set the goal |
| `f comment list\|send\|clear` | Review comments (nvim uses these) |

`f -h` for the summary. Feature names can't be subcommand names.

## Beads

- One db for everything: `~/code/features/.beads`.
- Each feature's issue id is in `.context/feature.json`; its `AGENTS.md` tells
  pi how to plan steps under it.
- Useful: `bd ready` (what's unblocked), `bd list --status closed` (what you
  finished), `bd show <id>`, `bd children <id>`.
- Removing a feature with open steps or unmerged work leaves its issue open
  with a note, so nothing silently disappears.

## Guard

pi can't use admin AWS profiles or production kube contexts. At start it gets
filtered copies of `~/.aws/config`, `~/.aws/credentials`, and the kubeconfig
(in `~/.cache/f/guard/`), default profile from `~/.config/f/guard.json`.
Commands naming a denied profile/context/prod EKS cluster, or pointing back at
the real files, are blocked with a reason.

- Allowed now: profiles without `admin`/`prod` (plus `team-eks-admin`),
  contexts without `prod` (dev, staging, minikube).
- Change rules: edit `~/.config/f/guard.json`, then `/reload` pi.
- SSO expired? Run `aws sso login` yourself (pi's filtered config shares the
  login).

## Setup on a new machine

1. Clone this repo and run `./install.sh` (links pi config + nvim module,
   installs `f`).
2. `herdr integration install pi`.
3. nvim `init.lua`: `require("f").setup()`.
4. Herdr config (in dotfiles): `ctrl+space` popup running `f`, sidebar hidden.
5. Base clones in `~/code/features/` (one dir per repo).
6. `cd ~/code/features && bd metrics off && bd init --prefix f --skip-agents --skip-hooks`.
7. `~/.config/f/guard.json` with machine-specific allow/default profile.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Workspace has an old tab layout | `ctrl+b shift+d` to close it, then `f <name>` |
| pi bash says "guard not set up" | `f creds` in a shell shows the error; fix, `/reload` |
| A command is blocked by guard | Read the reason; it's intended. Run it yourself if you really mean it |
| pi says "Read `<repo>/CLAUDE.md` in full before working in <repo>/" | Intended: it reads the file (paging to the end), then retries. Again after a compaction |
| `<leader>ps` failed | No pi in that feature; comments are kept and the message is on your clipboard |
| `<leader>pr` says no last run | pi hasn't changed code in that repo since the guard/checkpoints started |
| Repo tab says "no changes vs origin/main yet" | Nothing to diff yet; it checks every 3s and switches to CodeDiff once there is |
| Picker status looks stale | pi status is live from Herdr; PRs and CI refresh every 30 min (launchd), after each pi run, and in the prs tab; `f prs -r` forces |
| Extension changes not active | `/reload` in pi |
