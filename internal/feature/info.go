package feature

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Info describes a feature for everything outside f (the f mod, nvim):
// <feature>/.context/feature.json. f is its only writer; readers use it to
// detect a feature and find its repos without re-deriving the layout.
type Info struct {
	Name  string   `json:"name"`
	Root  string   `json:"root"`
	Repos []string `json:"repos"`
	Goal  string   `json:"goal,omitempty"`
	Issue string   `json:"issue,omitempty"` // beads issue for this feature
}

// InfoPath is the feature's info file.
func InfoPath(l Layout, name string) string {
	return filepath.Join(Dir(l, name), ContextDir, "feature.json")
}

// LoadInfo reads the feature's info; a missing file is an empty Info.
func LoadInfo(l Layout, name string) (Info, error) {
	raw, err := os.ReadFile(InfoPath(l, name))
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, fmt.Errorf("load feature info: %w", err)
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}, fmt.Errorf("load feature info: %w", err)
	}
	return info, nil
}

// SetIssue records the feature's beads issue and regenerates AGENTS.md.
func SetIssue(l Layout, name, issue string) (Info, error) {
	info, err := LoadInfo(l, name)
	if err != nil {
		return Info{}, err
	}
	info.Issue = issue
	if err := writeJSON(InfoPath(l, name), info); err != nil {
		return Info{}, fmt.Errorf("save feature info: %w", err)
	}
	return Sync(l, name, "")
}

// Sync refreshes the feature's info (repos from disk, plus any still being set
// up; goal if non-empty, otherwise the existing one) and regenerates AGENTS.md.
func Sync(l Layout, name, goal string) (Info, error) {
	info, err := LoadInfo(l, name)
	if err != nil {
		return Info{}, err
	}
	repos, err := Repos(l, name)
	if err != nil {
		return Info{}, err
	}
	if state, _ := LoadState(l, name); state.Setup != nil {
		for _, r := range state.Setup.Repos {
			if !slices.Contains(repos, r) {
				repos = append(repos, r)
			}
		}
		slices.Sort(repos)
	}
	info.Name, info.Root, info.Repos = name, Dir(l, name), repos
	switch {
	case goal != "":
		info.Goal = goal
	case info.Goal == "":
		info.Goal = ExistingGoal(AgentsMDPath(l, name)) // migrate older features
	}
	if err := writeJSON(InfoPath(l, name), info); err != nil {
		return Info{}, fmt.Errorf("save feature info: %w", err)
	}
	if err := writeAgentsMD(l, info); err != nil {
		return Info{}, err
	}
	return info, nil
}
