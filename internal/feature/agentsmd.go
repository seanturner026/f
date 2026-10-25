package feature

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// GoalPlaceholder is shown until a goal is set (`f goal`; pi's f extension
// sets it from the first prompt).
const GoalPlaceholder = "_Not set yet: the first chat prompt becomes the goal._"

// legacyPlaceholder is what older AGENTS.md files used.
const legacyPlaceholder = "_TBD: set from the first prompt._"

var agentsTmpl = template.Must(template.New("agents").Parse(`# Feature: {{.Name}}

## Goal

{{.Goal}}

## Repos

This session works across these git worktrees, all on branch ` + "`{{.Name}}`" + `:

{{range .Repos}}- ` + "`{{.Dir}}/`" + `{{if .Instructions}} (instructions: ` + "`{{.Instructions}}`" + `){{end}}
{{end}}
## Rules

- Before reviewing, answering questions about, or changing anything in a repo (its PRs included), read its instructions file listed above (if any) in full with the read tool, and follow it. Tool calls on a repo are blocked until you have.
- Run git commands inside the relevant repo directory; this root is not a git repo.
- Open PRs ready for review, not as drafts: ` + "`gh stack submit --open`" + ` (with ` + "`--auto`" + ` it makes drafts otherwise), ` + "`gh pr create`" + ` without ` + "`--draft`" + `. Draft only if I ask, or say why first (e.g. it can't merge yet).
- When a PR has merged (I'll say so), run ` + "`f sync`" + `, not ` + "`gh stack sync`" + ` (squash merges leave merged commits it can't recognize): it resets merged branches to main or deletes them, keeps commits added after a merge, rebases the rest onto main, force-pushes them (with lease), and re-inits the stack.
- PR checks that run on Depot CI: see why they failed with ` + "`depot ci diagnose --run <id>`" + ` or ` + "`depot ci logs <run-or-job-id>`" + ` (IDs are in the check's link); ` + "`gh run view`" + ` can't read them.
- PRs are always a gh stack per repo, starting at branch ` + "`{{.Name}}`" + ` (f already ran ` + "`gh stack init {{.Name}}`" + `; run it if ` + "`gh stack view`" + ` says there's no stack). One change: commit on ` + "`{{.Name}}`" + `. More: ` + "`gh stack add {{.Name}}-<topic>`" + ` for each next PR. Open/update PRs with ` + "`gh stack submit --open`" + `. Follow the repo's instructions on merge order.
- ` + "`.context/`" + ` holds feature state managed by ` + "`f`" + ` (e.g. ` + "`.context/state.json`" + ` lists this feature's PRs). Read it when useful; don't edit it.
{{if .Issue}}
## Plan (beads)

This feature is beads issue ` + "`{{.Issue}}`" + `. For multi-step work, plan the steps as child issues and work through them:

- Create steps: ` + "`bd create \"<step>\" --parent {{.Issue}} --description \"<repos, what, how to verify>\"`" + `
- Order them: ` + "`bd dep add <later-id> <earlier-id>`" + ` (later depends on earlier)
- Next step: ` + "`bd ready --parent {{.Issue}}`" + `.
- When you open a step's PR, link it: ` + "`bd update <id> --external-ref \"<PR url> [<PR url>...]\"`" + ` (every PR of the step, space-separated; it replaces, so include earlier ones, e.g. staging and production). f closes the step when they've all merged; don't close linked steps yourself. Close steps without a PR by hand: ` + "`bd close <id> --reason \"<what was done>\"`" + `.
- One-off changes don't need child issues. Run ` + "`bd prime`" + ` for the full bd workflow.
{{end}}`))

type agentsRepo struct{ Dir, Instructions string }

// AgentsMDPath is the feature's generated AGENTS.md.
func AgentsMDPath(l Layout, name string) string { return filepath.Join(Dir(l, name), "AGENTS.md") }

// writeAgentsMD renders the feature's AGENTS.md from its info.
func writeAgentsMD(l Layout, info Info) error {
	goal := info.Goal
	if goal == "" {
		goal = GoalPlaceholder
	}
	repos := make([]agentsRepo, 0, len(info.Repos))
	for _, r := range info.Repos {
		repos = append(repos, agentsRepo{Dir: r, Instructions: InstructionFile(l, info.Name, r)})
	}
	f, err := os.Create(AgentsMDPath(l, info.Name))
	if err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	defer f.Close()
	if err := agentsTmpl.Execute(f, map[string]any{"Name": info.Name, "Goal": goal, "Repos": repos, "Issue": info.Issue}); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return writeClaudeMD(l, info.Name, repos)
}

// writeClaudeMD points Claude Code, which reads only CLAUDE.md, at the
// feature's AGENTS.md and every repo's instructions (loaded up front).
func writeClaudeMD(l Layout, name string, repos []agentsRepo) error {
	lines := []string{"@AGENTS.md"}
	for _, r := range repos {
		if r.Instructions != "" {
			lines = append(lines, "@"+r.Instructions)
		}
	}
	if err := os.WriteFile(filepath.Join(Dir(l, name), "CLAUDE.md"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("write CLAUDE.md: %w", err)
	}
	return nil
}

// ExistingGoal returns the "## Goal" section of an AGENTS.md ("" if missing
// or still a placeholder). Used to migrate features from before feature.json.
func ExistingGoal(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, after, found := strings.Cut(string(raw), "## Goal\n")
	if !found {
		return ""
	}
	goal, _, _ := strings.Cut(after, "\n## ")
	if goal = strings.TrimSpace(goal); goal == GoalPlaceholder || goal == legacyPlaceholder {
		return ""
	}
	return goal
}
