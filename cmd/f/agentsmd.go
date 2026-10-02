package main

import (
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

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
`))

type agentsRepo struct{ Dir, Instructions string }

// writeAgentsMD (re)generates <feature>/AGENTS.md. An empty goal keeps the existing one.
func writeAgentsMD(cfg config, feature, goal string) error {
	dir := cfg.featureDir(feature)
	path := filepath.Join(dir, "AGENTS.md")
	if goal == "" {
		goal = existingGoal(path)
	}
	if goal == "" {
		goal = "_TBD: describe the goal here._"
	}
	names, err := featureRepos(cfg, feature)
	if err != nil {
		return err
	}
	var repos []agentsRepo
	for _, r := range names {
		ar := agentsRepo{Dir: r}
		for _, c := range []string{"AGENTS.md", "CLAUDE.md"} {
			if exists(filepath.Join(dir, r, c)) {
				ar.Instructions = r + "/" + c
				break
			}
		}
		repos = append(repos, ar)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return agentsTmpl.Execute(f, map[string]any{"Name": feature, "Goal": goal, "Repos": repos})
}

// existingGoal extracts the "## Goal" section from an existing AGENTS.md.
func existingGoal(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, after, found := strings.Cut(string(raw), "## Goal\n")
	if !found {
		return ""
	}
	goal, _, _ := strings.Cut(after, "\n## ")
	return strings.TrimSpace(goal)
}
