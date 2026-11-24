# Operator guide

How to run the Claude Code + Herdr + nvim + beads setup day to day. The README covers
what each part is; this covers how to use it.

## The pieces

| Piece | Job |
|---|---|
| `f` | Creates and owns features: worktrees, branches, `AGENTS.md`, `.context/`, Herdr layout, cleanup, beads issue |
| Herdr | Terminals: one workspace per feature, agent status |
| Claude Code | The agent. The `f` mod in `claude/f/`: guard, goal, checkpoints, PRs, plan, the `/f` commands |
| tuicr | Review: one tab per repo (`f review`): the whole feature, one PR of a stack, or the agent's turns |
| nvim | Editing, with comments for the agent (`nvim/f.lua`) |
| beads (`bd`) | The plan: one issue per feature, child issues for steps |

A feature is `<home>/.features/<name>/` (`home` in `~/.config/f/config.json`): one worktree per
repo (branch `<name>`), `AGENTS.md`, and `.context/` (local state, written only
by `f`).

## Daily flow

1. **Start**: from any Herdr shell (`ctrl+b t` popup, or a shell tab):
   `f <name>` → pick repos (TAB selects, filter clears) → Herdr opens the
   workspace at once, the agent waiting, while worktrees are fetched in the background
   (repo tabs say "setting up"). Type your first prompt; if you send it before
   they're ready, the agent shows "setting up worktrees… (n/m)" and starts when they
   are. It becomes the goal and the beads issue description. Use `@` to
   reference files across repos. Started from a feature's shell tab, that feature
   goes back to its chat tab.
2. **Direction**: the agent asks questions, then proposes. Approve or steer.
3. **Review**: `opt+3…` repo tabs show the whole feature in tuicr, live. Quit tuicr
   (`:q`) to pick another view: one PR of the stack, or the agent's turns (each
   turn's changes on their own, your edits between turns separate). Comment in
   tuicr (`c`, `v`), then send them with `/fsr [text]` from chat. `e` opens a file in
   your editor; tell the agent if you changed something ("I edited X").
4. **PRs**: the agent opens them; they show in the statusline with CI (`✓` `⋯` `✗`), and the
   `prs` tab lists each PR's failing/running checks. `/fprc` copies
   `url: title` lines (stacks numbered 1/3, 2/3, …).
5. **Switch**: `ctrl+space` → picker, ordered by what needs you.
6. **Merged**: the repo resets to main by itself (on open, `r` in prs, `/fsync`, or tell
   the agent "merged"), so more work in the feature starts a fresh PR. Squash merges are
   recognized by content: merged stack branches are reset or deleted, the rest rebased
   onto main and pushed. A merged PR whose commits it can't find in main shows as `✗`.
7. **Done**: when PRs merge, `ctrl-x` the feature in the picker. The beads
   issue closes with the PR links. It doesn't ask: removed features go to the trash
   with their branches and uncommitted files, so `f restore <name>` within 14 days
   undoes it.

Big multi-step work (e.g. ACK across tf/argocd/prod/ops): start one
feature with all the repos, ask the agent to **plan it in beads** (child issues +
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
| `ctrl+b shift+d` | Close this workspace (the agent's session is kept) |
| `ctrl+b q` | Detach (everything keeps running; `herdr` reattaches) |

### PRs tab (`f prs --watch`)

| Key | Does |
|---|---|
| `r` | Refresh now (a re-run on GitHub, a merge) |
| `f` | Fix PRs: send merge conflicts and failing checks + logs to this feature's agent (same as `/fci`) |
| `q` | Quit (back to a shell; `f prs -w` restarts it) |

Each open PR lists its failing and running checks; passed and skipped are a
count. PR numbers, titles and check names are links: cmd+click opens them. It
asks GitHub every minute only while checks run or for 5 minutes after a push or
new PR; otherwise it just watches for pushes. What it learns shows in the
picker too.

### Picker (`ctrl+space` or bare `f`)

| Key | Does |
|---|---|
| `enter` | Jump to the feature (recreates its workspace if closed) |
| `ctrl-x` | Remove the feature (to the trash for 14 days: `f restore <name>`) and close its workspace once the picker exits; instant, no questions, so remove several and then pick one |
| `ctrl-n` | New feature: type its name in the search box first. Arrows (or `ctrl-j`/`ctrl-k`) still move the cursor |
| `ctrl-u` | Mark the feature unread (or read again): AGENT shows `unread`, sorted with "needs you". Opening it, or the agent doing anything (working, done, needs you), marks it read |
| `ctrl-l` | Move the feature to the backlog (or back): backlog features are listed under a divider, below the rest, in the same order. Nothing else changes: its agent and PRs keep going |
| `ctrl-o` / `ctrl-y` | Open the feature's open PRs in the browser / copy them (like `/fprc`), without leaving the picker |
| `ctrl-s` | Sync the feature in the background (as `f sync` does after a merge) |
| `ctrl-t` | Trash: pick a removed feature to restore (it reappears in the picker) |
| `?` / `ctrl-/` | Show/hide these keys in a panel beside the list |
| `esc` / `ctrl-space` | Close |

Columns: `●` marks the workspace you're in (listed first, with the cursor on it) ·
FEATURE · AGENT status (or `unread`) · CI of open PRs (`✓`, `⋯ running`, `✗ <repos>`; `conflict <repos>` first when one conflicts with its base, sorted with "needs you") · PRS ·
PLAN (beads steps).

Order: **needs you** (the agent asked something, or CI failing) → **done** (finished,
unseen) → **working** → **idle** → **closed** (no agent running).

### Claude Code

`f` starts Claude Code with the `f` mod, statusline and settings from `claude/`
(`F_CLAUDE_DIR` overrides `~/.claude/f`). It starts in auto mode, and the
feature's `CLAUDE.md` imports its AGENTS.md and each repo's instructions.
The statusline shows the feature's PRs with CI, and `conflicts` on one that conflicts with its base (GitHub's `mergeable`; it can take a refresh to appear after main moves).

`/fprs`, `/fprc` and `/fplan` run at once, even mid-turn; the rest wait for the turn
to end. `/fci`, `/fsr` and `/fprr` send a message to the agent; the rest never reach
the model.

| Command | Does |
|---|---|
| `/fprs` | Open the open PRs in the browser, at once from the saved list, then refresh it from GitHub |
| `/fprc` | Copy the feature's open PRs as `url: title` lines (stacks numbered 1/3, 2/3, …) |
| `/fprr [text]` | Send your text plus the unresolved GitHub review threads on the feature's open PRs (with the diff context); the agent fixes them, you resolve the threads |
| `/fsr [text]` | Send your text plus the pending review comments (tuicr's in every repo, and nvim's) as one message, then delete those |
| `/fci` | Send merge conflicts and failing checks + failed-job logs to the agent: it rebases or fixes, and pushes (asks first only for behavior changes or judgment calls) |
| `/fsync` | After a PR merges: merged branches are reset to main (the feature branch) or deleted (stack branches), commits added after a merge are kept, the rest of the stack is rebased onto main, force-pushed (with lease) and its PRs moved to base `main`; one line per branch. Also runs (without pushing; stacks wait) when you open the feature and on `r` in the prs tab |
| `/fplan` | The feature's beads plan as a dependency tree, in a sidebar that updates as the plan changes; opens at once with the last plan, then refreshes. Again to hide |
| `/fcode [n]` | Copy code block(s) from the last reply exactly as written (no wrap/indent); several: pick one or all |
| `/fslack` | Copy the last reply formatted for Slack, just its quoted (`>`) draft if it has one: `*bold*`, headings as bold lines, `•` bullets, links as `text (url)`; code untouched |

Features are opened and started from the picker (`ctrl+space`), not from chat.
Herdr's busy/idle marks need `herdr integration install claude`. Herdr plays its Done sound when Claude finishes. Mouse selection copies when
**Copy on select** is on in `/config`; hold Shift for Herdr's own selection.

### Review tabs (tuicr)

Each repo tab runs `f review <repo>`: tuicr on the whole feature (committed and
uncommitted), refreshed as the agent edits. A tab you aren't looking at is paused
(no CPU) and catches up when you switch to it. It restarts by itself when HEAD or
main moves, and waits ("no changes yet") until there's something to show.

| Key | Does |
|---|---|
| `j`/`k`, `{`/`}`, `[`/`]` | Lines, files, hunks (one stream of every file) |
| `c` / `v` / `C` | Comment on a line / a selection / the file |
| `r` / `R` | Mark the file / hunk reviewed |
| `e` | Open the file in your editor |
| `:diff` / `:set wrap!` | Side-by-side ↔ unified / line wrap |
| `?` | All tuicr keys |
| `:q` | Pick another view: whole feature · **last turn** (just the agent's latest turn; follows each new one) · each open PR of the stack (`n/N`; the checked-out one is live) · all the agent's turns (choose one or more in tuicr's commit list) · leave |

Send comments with `/fsr [text]` in chat (or `f comment send`).

### nvim (anywhere in a feature)

| Key | Does |
|---|---|
| `<leader>pc` | Comment on line/selection (normal or visual) |
| `<leader>pl` | List pending comments (quickfix) |
| `<leader>pp` | Pick pending comments (Snacks): ⏎ jumps to one, Tab selects several, ctrl-x deletes them and the list stays open |
| `<leader>ps` | Send comments (nvim's and tuicr's) to the agent |
| `:FClear` | Discard pending comments |

nvim comments are saved in
`.context/comments.json`, so they survive closing nvim.

## f commands

| Command | Does |
|---|---|
| `f <name>` | Start a feature, or reopen it (continues the agent's session; restarts any of its tabs that are back at a bare shell) |
| `f resume` | After quitting WezTerm/Herdr: Herdr restores the layout as bare shells; this restarts the agent (`--continue`), nvim, review, and prs in every feature workspace. Shell tabs and anything still running are left alone. Needs `[session] resume_agents_on_restore = false` in Herdr's config: Herdr's own resume starts a bare `claude --resume` (no statusline, no f mod) first |
| `f` | Picker |
| `f add [repo…]` | Add repos to this feature (+ Herdr tabs, before shell). From your shell in Herdr it returns at once and sets them up in the background (the agent gets them with your next prompt); run by the agent it waits |
| `f prs [-r] [-c] [-n] [-w]` | PRs: from saved state; `-r` refresh live, `-c` copy open ones, `-n` don't open browser, `-w` live view (the prs tab) |
| `f sync --all` | What launchd runs every 30 min (skipped after 30 min without keyboard/mouse input, e.g. asleep): PR/CI and plan refresh for every feature; post-merge resets wait for the one you're in or a busy agent; never pushes; the agent gets a note with its next prompt. Log: `~/Library/Logs/f-sync.log` |
| `f sync` | Same as `/fsync`. Also fast-forwards the source clone of each repo with a merged PR (only if it's on its default branch with no local changes), so clones stay current and the `f` repo redeploys itself. Leaves repos with uncommitted changes or conflicts alone (says so). Never pushes the default branch or branches outside the feature |
| `f ci fix` | Send merge conflicts and failing checks + failed-job logs to the feature's agent (clipboard if it isn't reachable) |
| `f restore [name]` | Bring back a removed feature (worktrees, branches, uncommitted files), reopen its beads issue and open it. No name: list the trash. Kept 14 days; `f sync --all` deletes older ones, branches included. A name in the trash can't be reused until then |
| `f trace [--since 24h]` | Where `f` spends time: starts Jaeger in Docker (`f-jaeger`) if needed, sends it the traces, opens http://localhost:16686. Jaeger keeps nothing on disk, so each run re-sends. `f trace stop` stops it |
| `f trace stats [--since 24h]` | The same as text, no Docker (agents can run it): count, p50, p95, max per span, then the 10 slowest runs with their slowest steps |
| `f goal [text]` | Show / set the goal |
| `f comment list\|send\|delete\|clear` | Review comments: nvim's (list, delete by ID, clear) and, for send, tuicr's too; `send --print` is `/fsr` |

`f -h` for the summary. Feature names can't be subcommand names.

## Beads

- One db for everything: `<home>/.beads`.
- Each feature's issue id is in `.context/feature.json`; its `AGENTS.md` tells
  the agent how to plan steps under it.
- Useful: `bd ready` (what's unblocked), `bd list --status closed` (what you
  finished), `bd show <id>`, `bd children <id>`.
- Removing a feature with open steps leaves its issue open with a note, so
  nothing silently disappears.

## Guard

The agent can't use admin AWS profiles or production kube contexts. At start it gets
filtered copies of `~/.aws/config`, `~/.aws/credentials`, and the kubeconfig
(in `~/.cache/f/guard/`), default profile from `~/.config/f/guard.json`.
Commands naming a denied profile/context/prod EKS cluster, or pointing back at
the real files, are blocked with a reason.

- Allowed now: profiles without `admin`/`prod` (plus any `allow_profiles` in `guard.json`),
  contexts without `prod` (dev, staging, minikube).
- Change rules: edit `~/.config/f/guard.json`, then restart the chat tab.
- SSO expired? Run `aws sso login` yourself (the agent's filtered config shares the
  login).

## Setup on a new machine

1. Clone this repo and run `just install` (installs `f`, copies the Claude Code
   config and nvim module, loads the launchd sync, and turns on the git hooks
   that redeploy after each commit or pull on `main`).
2. `just herdr-integration`.
3. Claude Code `/config`: turn **Copy on select** on.
4. nvim `init.lua`: `require("f").setup()`. `brew install tuicr`, and in `~/.config/tuicr/config.toml` (in dotfiles): `wrap = true`, `diff_watch_interval_ms = 3000`.
5. Herdr config (in dotfiles): `ctrl+space` popup running `f`, sidebar hidden.
6. `~/.config/f/config.json`: `home` (where features live, default `~/code/features`) and
   `repos` (the clones you can pick: paths, or `dir/*`), e.g.
   `{"home": "~/code/features", "repos": ["~/code/github/me/f", "~/code/github/me/dotfiles"]}`.
   Optional `agent_rules`: extra lines for every feature's AGENTS.md (e.g. how to read your CI's logs).
   Optional `no_stack`: repos whose CI breaks on stacked PRs (e.g. Terraform). Their branches still build on each other, but every PR targets the default branch, so later PRs include the earlier ones' diffs.
7. `cd <home> && bd metrics off && bd init --prefix f --skip-agents --skip-hooks && rm -rf .git .gitignore && git config --global beads.role maintainer`.
   The db is Dolt, not git; a git repo at `<home>` would hide feature worktrees from Claude Code's `@` picker.
8. `~/.config/f/guard.json` with machine-specific allow/default profile.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Tabs are bare shells after a restart (no agent, no review) | `f resume` (all features) or `f <name>` (one) |
| Workspace has an old tab layout | `ctrl+b shift+d` to close it, then `f <name>` |
| Bash says "guard not set up" | `f creds` in a shell shows the error; fix, then restart the chat tab |
| A command is blocked by guard | Read the reason; it's intended. Run it yourself if you really mean it |
| `<leader>ps` failed | No agent in that feature; comments are kept and the message is on your clipboard |
| No "agent turns" view | Turns are recorded from runs that changed code in that repo, from now on |
| Repo tab says "no changes to review yet" | Nothing to diff yet; it checks every 3s while you're looking and opens tuicr once there is |
| Picker status looks stale | Agent status is live from Herdr; PRs and CI refresh every 30 min (launchd), after each agent turn, and in the prs tab; `f prs -r` forces |
| Mod changes not active | Claude Code reloads the `f` mod on save (`/reload-plugins` forces) |
