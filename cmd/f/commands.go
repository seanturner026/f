package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Subcommand names can't be feature names, or `f <name>` would be ambiguous.
var reserved = map[string]bool{"add": true, "prs": true, "gc": true, "help": true, "completion": true}

func checkName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid feature name %q (use lowercase, digits, - . _)", name)
	}
	if reserved[name] {
		return fmt.Errorf("%q is a subcommand; pick another name", name)
	}
	return nil
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// checkRepos verifies every repo has a base clone, before anything is created.
func checkRepos(cfg config, repos []string) error {
	for _, r := range repos {
		if !isGitDir(filepath.Join(cfg.Base, r)) {
			return fmt.Errorf("no base clone %q in %s", r, cfg.Base)
		}
	}
	return nil
}

// start creates a feature (repos → prompt → name), or reopens an existing one.
func start(cfg config, name string, repos []string, prompt string) error {
	if name != "" {
		if err := checkName(name); err != nil {
			return err
		}
		if exists(cfg.featureDir(name)) {
			return openWorkspace(cfg, name, "")
		}
	}

	// 1. Which repos?
	if len(repos) == 0 {
		picked, err := pickRepos(cfg, nil)
		if err != nil {
			return err
		}
		repos = picked
	}
	if len(repos) == 0 {
		return errors.New("no repos selected; aborted")
	}
	if err := checkRepos(cfg, repos); err != nil {
		return err
	}

	// 2. What are we doing?
	if prompt == "" {
		p, err := editPrompt()
		if err != nil {
			return err
		}
		prompt = p
	}
	if prompt == "" {
		return errors.New("empty prompt; aborted")
	}

	// 3. Name it: pi suggests, you confirm.
	if name == "" {
		info("naming…")
		n, err := askName(suggestName(prompt))
		if err != nil {
			return err
		}
		if err := checkName(n); err != nil {
			return err
		}
		if exists(cfg.featureDir(n)) {
			return fmt.Errorf("feature %q already exists; run `f %s` to reopen it", n, n)
		}
		name = n
	}

	// 4. Build it.
	if err := os.MkdirAll(cfg.featureDir(name), 0o755); err != nil {
		return err
	}
	fmt.Println(bold(name))
	for _, r := range repos {
		if err := addWorktree(cfg, name, r); err != nil {
			return err
		}
	}
	if err := writeAgentsMD(cfg, name, prompt); err != nil {
		return err
	}
	return openWorkspace(cfg, name, prompt)
}

func add(cfg config, explicit string, repos []string) error {
	name, err := cfg.resolveFeature(explicit)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		have, err := featureRepos(cfg, name)
		if err != nil {
			return err
		}
		if repos, err = pickRepos(cfg, have); err != nil {
			return err
		}
		if len(repos) == 0 {
			return errors.New("no repos selected")
		}
	}
	if err := checkRepos(cfg, repos); err != nil {
		return err
	}
	for _, r := range repos {
		if err := addWorktree(cfg, name, r); err != nil {
			return err
		}
	}
	if err := writeAgentsMD(cfg, name, ""); err != nil {
		return err
	}
	info("AGENTS.md updated; /reload in pi to pick it up")
	return nil
}

func prs(cfg config, explicit string) error {
	name, err := cfg.resolveFeature(explicit)
	if err != nil {
		return err
	}
	repos, err := featureRepos(cfg, name)
	if err != nil {
		return err
	}
	for _, r := range repos {
		fmt.Println(bold(r))
		list, err := listPRs(filepath.Join(cfg.featureDir(name), r), name)
		switch {
		case err != nil:
			fmt.Printf("  %s\n", red("gh: "+err.Error()))
		case len(list) == 0:
			fmt.Println(dim("  no PR"))
		}
		for _, p := range list {
			state := p.State
			if p.IsDraft {
				state = "DRAFT"
			}
			fmt.Printf("  #%d %-7s %s\n         %s\n", p.Number, state, p.Title, dim(p.URL))
		}
	}
	return nil
}

// gc removes features where every repo is clean and either untouched or has a
// merged PR with nothing unpushed, and at least one PR merged overall.
func gc(cfg config, yes bool) error {
	var done []string
	for _, name := range features(cfg) {
		ready, reason := collectible(cfg, name)
		if ready {
			done = append(done, name)
			fmt.Printf("%s %s\n", green("✓"), name)
		} else {
			fmt.Printf("%s %s %s\n", dim("·"), name, dim(reason))
		}
	}
	if len(done) == 0 {
		return nil
	}
	if !yes {
		fmt.Printf("remove %d feature(s) (worktrees, branches, herdr workspace)? [y/N] ", len(done))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return nil
		}
	}
	h := newHerdr(cfg)
	herdrUp := h.available()
	for _, name := range done {
		if err := removeFeature(cfg, name); err != nil {
			return err
		}
		if herdrUp {
			if ws, _ := h.findWorkspace(name); ws != "" {
				_ = h.closeWorkspace(ws)
			}
		}
		ok("removed %s", name)
	}
	return nil
}

func collectible(cfg config, name string) (bool, string) {
	repos, err := featureRepos(cfg, name)
	if err != nil {
		return false, err.Error()
	}
	merged := 0
	for _, r := range repos {
		wt := filepath.Join(cfg.featureDir(name), r)
		if n, _ := changedFiles(wt); n > 0 {
			return false, fmt.Sprintf("(%s: uncommitted changes)", r)
		}
		if ahead, _ := commitsAhead(wt, "origin/"+defaultBranch(cfg, r)); ahead == 0 {
			continue // untouched repo
		}
		list, err := listPRs(wt, name)
		if err != nil {
			return false, fmt.Sprintf("(%s: gh: %v)", r, err)
		}
		if !anyMerged(list) {
			return false, fmt.Sprintf("(%s: not merged)", r)
		}
		if n, err := unpushedCommits(wt, "origin/"+defaultBranch(cfg, r)); err == nil && n > 0 {
			return false, fmt.Sprintf("(%s: %d commits not pushed)", r, n)
		}
		merged++
	}
	if merged == 0 {
		return false, "(no merged PRs yet)"
	}
	return true, ""
}

func anyMerged(list []pr) bool {
	for _, p := range list {
		if p.State == "MERGED" {
			return true
		}
	}
	return false
}

func removeFeature(cfg config, name string) error {
	dir := cfg.featureDir(name)
	repos, err := featureRepos(cfg, name)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if err := removeWorktree(cfg, r, filepath.Join(dir, r), false); err != nil {
			return err
		}
		_, _ = git(filepath.Join(cfg.Base, r), "branch", "-D", name)
	}
	_ = os.Remove(filepath.Join(dir, "AGENTS.md"))
	return os.Remove(dir)
}
