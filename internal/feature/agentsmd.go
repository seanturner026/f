package feature

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// GoalPlaceholder is replaced with the first prompt by the pi extension
// agent/extensions/feature-goal.ts. Keep the two in sync.
const GoalPlaceholder = "_TBD: set from the first prompt._"

var agentsTmpl = template.Must(template.New("agents").Parse(`# Feature: {{.Name}}

## Goal

{{.Goal}}

## Repos

This session works across these git worktrees, all on branch ` + "`{{.Name}}`" + `:

{{range .Repos}}- ` + "`{{.Dir}}/`" + `{{if .Instructions}} (instructions: ` + "`{{.Instructions}}`" + `){{end}}
{{end}}
## Rules

- Before changing anything in a repo, read its instructions file listed above (if any) and follow it.
- Run git commands inside the relevant repo directory; this root is not a git repo.
- One PR per repo, from branch ` + "`{{.Name}}`" + `.
- ` + "`.context/`" + ` holds feature state managed by ` + "`f`" + ` (e.g. ` + "`.context/state.json`" + ` lists this feature's PRs). Read it when useful; don't edit it.
`))

type agentsRepo struct{ Dir, Instructions string }

// AgentsMDPath is the feature's generated AGENTS.md.
func AgentsMDPath(l Layout, name string) string { return filepath.Join(Dir(l, name), "AGENTS.md") }

// WriteAgentsMD (re)generates the feature's AGENTS.md. An empty goal keeps the
// existing goal, or the placeholder.
func WriteAgentsMD(l Layout, name, goal string) error {
	path := AgentsMDPath(l, name)
	if goal == "" {
		goal = ExistingGoal(path)
	}
	if goal == "" {
		goal = GoalPlaceholder
	}
	names, err := Repos(l, name)
	if err != nil {
		return err
	}
	repos := make([]agentsRepo, 0, len(names))
	for _, r := range names {
		ar := agentsRepo{Dir: r}
		for _, c := range []string{"AGENTS.md", "CLAUDE.md"} {
			if _, err := os.Stat(filepath.Join(Dir(l, name), r, c)); err == nil {
				ar.Instructions = r + "/" + c
				break
			}
		}
		repos = append(repos, ar)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	defer f.Close()
	if err := agentsTmpl.Execute(f, map[string]any{"Name": name, "Goal": goal, "Repos": repos}); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

// ExistingGoal returns the "## Goal" section of an AGENTS.md ("" if missing
// or still the placeholder).
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
	if goal = strings.TrimSpace(goal); goal == GoalPlaceholder {
		return ""
	}
	return goal
}
