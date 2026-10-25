package feature

import (
	"os"
	"path/filepath"
	"slices"
)

// Setup tracks repos being added in the background (f setup), so the
// workspace can open before their worktrees exist. It lives in state.json
// while anything is pending; pi waits on it before a run (agent/extensions/f.ts).
type Setup struct {
	Repos   []string `json:"repos"`   // every repo being added
	Pending []string `json:"pending"` // not ready yet
}

// BeginSetup records repos as being set up (joining a setup already running).
func BeginSetup(l Layout, name string, repos []string) error {
	if err := os.MkdirAll(Dir(l, name), 0o755); err != nil {
		return err
	}
	_, err := UpdateState(l, name, func(s *State) {
		if s.Setup == nil {
			s.Setup = &Setup{}
		}
		for _, r := range repos {
			if !slices.Contains(s.Setup.Repos, r) {
				s.Setup.Repos = append(s.Setup.Repos, r)
			}
			if !slices.Contains(s.Setup.Pending, r) {
				s.Setup.Pending = append(s.Setup.Pending, r)
			}
		}
	})
	return err
}

// FinishSetup marks repo as no longer pending (ready or failed). Setup stays
// until EndSetup, so pi keeps waiting until the notes about it are queued.
func FinishSetup(l Layout, name, repo string) error {
	_, err := UpdateState(l, name, func(s *State) {
		if s.Setup != nil {
			s.Setup.Pending = slices.DeleteFunc(s.Setup.Pending, func(r string) bool { return r == repo })
		}
	})
	return err
}

// EndSetup clears Setup if nothing is pending (another f add may still be).
func EndSetup(l Layout, name string) error {
	_, err := UpdateState(l, name, func(s *State) {
		if s.Setup != nil && len(s.Setup.Pending) == 0 {
			s.Setup = nil
		}
	})
	return err
}

// InstructionFile is repo's instructions for agents, relative to the feature
// root ("argocd/CLAUDE.md"), or "" if it has none (or isn't set up yet).
func InstructionFile(l Layout, name, repo string) string {
	for _, c := range []string{"AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(RepoDir(l, name, repo), c)); err == nil {
			return repo + "/" + c
		}
	}
	return ""
}
