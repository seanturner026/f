package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func checkName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid feature name %q (use lowercase, digits, - . _)", name)
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

func requireFeature(cfg config, name string) error {
	if !exists(cfg.featureDir(name)) {
		return fmt.Errorf("no feature %q", name)
	}
	return nil
}

func cmdNew(cfg config, args []string) error {
	var name, prompt string
	var repos []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-r", "--repo", "-p", "--prompt":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", a)
			}
			i++
			if a == "-r" || a == "--repo" {
				repos = append(repos, args[i])
			} else {
				prompt = args[i]
			}
		default:
			if len(a) > 0 && a[0] == '-' {
				return fmt.Errorf("unknown flag %s", a)
			}
			if name != "" {
				return fmt.Errorf("unexpected %q (repos go after -r)", a)
			}
			name = a
		}
	}
	if name != "" {
		if err := checkName(name); err != nil {
			return err
		}
		if exists(cfg.featureDir(name)) {
			return fmt.Errorf("feature %q exists (use f add or f open)", name)
		}
	}

	// 1. What are we doing?
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

	// 2. Which repos?
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
			return fmt.Errorf("feature %q exists (use f add or f open)", n)
		}
		name = n
	}

	// 4. Build it.
	if err := os.MkdirAll(cfg.featureDir(name), 0o755); err != nil {
		return err
	}
	fmt.Println(bold("feature " + name))
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

func cmdAdd(cfg config, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	name, repos := args[0], args[1:]
	if err := requireFeature(cfg, name); err != nil {
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
	info("AGENTS.md updated; run /reload in pi")
	return nil
}

func cmdOpen(cfg config, args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	if err := requireFeature(cfg, args[0]); err != nil {
		return err
	}
	return openWorkspace(cfg, args[0], "")
}

func cmdLs(cfg config) error {
	entries, err := os.ReadDir(cfg.Features)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0) {
		info("no features")
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fmt.Println(bold(e.Name()))
		repos, err := featureRepos(cfg, e.Name())
		if err != nil {
			return err
		}
		for _, r := range repos {
			wt := filepath.Join(cfg.featureDir(e.Name()), r)
			changed, _ := changedFiles(wt)
			ahead, err := commitsAhead(wt, "origin/"+defaultBranch(cfg, r))
			aheadS := fmt.Sprint(ahead)
			if err != nil {
				aheadS = "?"
			}
			fmt.Printf("  %-28s %d changed, %s commits\n", r, changed, aheadS)
		}
	}
	return nil
}

func cmdPrs(cfg config, args []string) error {
	name := ""
	if len(args) > 0 {
		name = args[0]
	} else if name = cfg.featureFromCwd(); name == "" {
		return errors.New("usage: f prs <feature> (or run inside a feature)")
	}
	if err := requireFeature(cfg, name); err != nil {
		return err
	}
	repos, err := featureRepos(cfg, name)
	if err != nil {
		return err
	}
	for _, r := range repos {
		fmt.Println(bold(r))
		prs, err := listPRs(filepath.Join(cfg.featureDir(name), r), name)
		switch {
		case err != nil:
			fmt.Printf("  %s\n", red("gh: "+err.Error()))
		case len(prs) == 0:
			fmt.Println(dim("  no PR"))
		}
		for _, p := range prs {
			state := p.State
			if p.IsDraft {
				state = "DRAFT"
			}
			fmt.Printf("  #%d %-7s %s\n         %s\n", p.Number, state, p.Title, dim(p.URL))
		}
	}
	return nil
}

func cmdDone(cfg config, args []string) error {
	var name string
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		} else if name == "" {
			name = a
		} else {
			return errUsage
		}
	}
	if name == "" {
		return errUsage
	}
	if err := requireFeature(cfg, name); err != nil {
		return err
	}
	dir := cfg.featureDir(name)
	repos, err := featureRepos(cfg, name)
	if err != nil {
		return err
	}

	if !force {
		var problems []string
		for _, r := range repos {
			wt := filepath.Join(dir, r)
			if n, _ := changedFiles(wt); n > 0 {
				problems = append(problems, fmt.Sprintf("%s: %d uncommitted changes", r, n))
			}
			if n, err := unpushedCommits(wt, "origin/"+defaultBranch(cfg, r)); err == nil && n > 0 {
				problems = append(problems, fmt.Sprintf("%s: %d commits not pushed", r, n))
			}
		}
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Println("  " + p)
			}
			return errors.New("refusing; fix the above or use --force")
		}
	}

	for _, r := range repos {
		if err := removeWorktree(cfg, r, filepath.Join(dir, r), force); err != nil {
			return err
		}
		ok("  removed %s worktree (branch %s kept)", r, name)
	}
	_ = os.Remove(filepath.Join(dir, "AGENTS.md"))
	if err := os.Remove(dir); err != nil {
		info("  left %s (not empty)", dir)
	}
	if h := newHerdr(cfg); h.available() {
		if ws, _ := h.findWorkspace(name); ws != "" {
			if err := h.closeWorkspace(ws); err == nil {
				ok("  closed herdr workspace")
			}
		}
	}
	return nil
}
