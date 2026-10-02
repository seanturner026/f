package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sh runs a command and returns trimmed stdout; stderr is included in errors.
func sh(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func git(dir string, args ...string) (string, error) { return sh(dir, "git", args...) }

func isGitDir(path string) bool { return exists(filepath.Join(path, ".git")) }

// baseRepos lists base clones (directories with .git) under cfg.Base.
func baseRepos(cfg config) ([]string, error) {
	entries, err := os.ReadDir(cfg.Base)
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, e := range entries {
		if e.IsDir() && isGitDir(filepath.Join(cfg.Base, e.Name())) {
			repos = append(repos, e.Name())
		}
	}
	return repos, nil
}

// featureRepos lists the repo worktrees inside a feature.
func featureRepos(cfg config, name string) ([]string, error) {
	entries, err := os.ReadDir(cfg.featureDir(name))
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, e := range entries {
		if e.IsDir() && isGitDir(filepath.Join(cfg.featureDir(name), e.Name())) {
			repos = append(repos, e.Name())
		}
	}
	sort.Strings(repos)
	return repos, nil
}

func defaultBranch(cfg config, repo string) string {
	ref, err := git(filepath.Join(cfg.Base, repo), "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	return strings.TrimPrefix(ref, "origin/")
}

// addWorktree creates <feature>/<repo> on branch <feature>, from the latest origin default branch.
func addWorktree(cfg config, feature, repo string) error {
	src := filepath.Join(cfg.Base, repo)
	dst := filepath.Join(cfg.featureDir(feature), repo)
	if !isGitDir(src) {
		return fmt.Errorf("no base clone at %s", src)
	}
	if exists(dst) {
		info("  %s: already present", repo)
		return nil
	}
	info("  %s: fetching", repo)
	if _, err := git(src, "fetch", "-q", "--prune", "origin"); err != nil {
		return err
	}
	base := "origin/" + defaultBranch(cfg, repo)
	if _, err := git(src, "show-ref", "-q", "--verify", "refs/heads/"+feature); err == nil {
		if _, err := git(src, "worktree", "add", "-q", dst, feature); err != nil {
			return err
		}
		ok("  %s: worktree on existing branch %s", repo, feature)
		return nil
	}
	if _, err := git(src, "worktree", "add", "-q", "--no-track", "-b", feature, dst, base); err != nil {
		return err
	}
	ok("  %s: worktree on new branch %s (from %s)", repo, feature, base)
	return nil
}

func removeWorktree(cfg config, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := git(filepath.Join(cfg.Base, repo), append(args, path)...)
	return err
}

func changedFiles(wt string) (int, error) {
	out, err := git(wt, "status", "--porcelain")
	if err != nil || out == "" {
		return 0, err
	}
	return len(strings.Split(out, "\n")), nil
}

func commitsAhead(wt, base string) (int, error) {
	out, err := git(wt, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

type pr struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	IsDraft bool   `json:"isDraft"`
	HeadOid string `json:"headRefOid"`
}

func listPRs(wt, branch string) ([]pr, error) {
	out, err := sh(wt, "gh", "pr", "list", "--head", branch, "--state", "all",
		"--json", "number,state,title,url,isDraft,headRefOid")
	if err != nil {
		return nil, err
	}
	var prs []pr
	return prs, json.Unmarshal([]byte(out), &prs)
}
