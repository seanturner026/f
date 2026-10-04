package feature

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/seanturner026/pi/internal/git"
	"github.com/seanturner026/pi/internal/github"
	"github.com/seanturner026/pi/internal/model"
)

// AddResult says what AddRepo did.
type AddResult string

const (
	AddedNewBranch      AddResult = "new branch"
	AddedExistingBranch AddResult = "existing branch"
	AlreadyPresent      AddResult = "already present"
)

// AddRepo adds a worktree for repo to feature name, on branch name. A new
// branch starts from the latest origin default branch, which is returned as base.
func AddRepo(ctx context.Context, r Runner, l Layout, name, repo string) (result AddResult, base string, err error) {
	src := filepath.Join(l.Base, repo)
	dst := filepath.Join(Dir(l, name), repo)
	if !git.IsRepo(src) {
		return "", "", fmt.Errorf("%q: %w", repo, ErrNoBaseClone)
	}
	if _, err := os.Stat(dst); err == nil {
		return AlreadyPresent, "", nil
	}
	if err := os.MkdirAll(Dir(l, name), 0o755); err != nil {
		return "", "", fmt.Errorf("add repo: %w", err)
	}
	if err := git.Fetch(ctx, r, src); err != nil {
		return "", "", fmt.Errorf("add %s: %w", repo, err)
	}
	if git.BranchExists(ctx, r, src, name) {
		if err := git.AddWorktree(ctx, r, src, dst, name, ""); err != nil {
			return "", "", fmt.Errorf("add %s: %w", repo, err)
		}
		return AddedExistingBranch, "", nil
	}
	base = "origin/" + git.DefaultBranch(ctx, r, src)
	if err := git.AddWorktree(ctx, r, src, dst, name, base); err != nil {
		return "", "", fmt.Errorf("add %s: %w", repo, err)
	}
	return AddedNewBranch, base, nil
}

// PRs looks up PRs in every repo, concurrently: the repo's gh stack if it's on
// one, otherwise PRs for the feature branch.
// Per-repo lookup failures are returned in errs, keyed by repo.
func PRs(ctx context.Context, r Runner, l Layout, name string) (prs []model.PR, errs map[string]error, err error) {
	repos, err := Repos(l, name)
	if err != nil {
		return nil, nil, err
	}
	results := make([][]model.PR, len(repos))
	failures := make([]error, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], failures[i] = github.RepoPRs(ctx, r, filepath.Join(Dir(l, name), repo), repo, name)
		}()
	}
	wg.Wait()
	errs = map[string]error{}
	for i, repo := range repos {
		if failures[i] != nil {
			errs[repo] = failures[i]
		}
		prs = append(prs, results[i]...)
	}
	return prs, errs, nil
}

// Finished reports whether a feature is done: every repo is clean and either
// untouched or has a merged PR containing its HEAD, and at least one merged.
// Uses the merged PR's head commit, so it works after the remote branch is deleted.
func Finished(ctx context.Context, r Runner, l Layout, name string) bool {
	repos, err := Repos(l, name)
	if err != nil {
		return false
	}
	merged := 0
	for _, repo := range repos {
		wt := filepath.Join(Dir(l, name), repo)
		if n, err := git.ChangedFiles(ctx, r, wt); err != nil || n > 0 {
			return false
		}
		base := "origin/" + git.DefaultBranch(ctx, r, filepath.Join(l.Base, repo))
		if ahead, err := git.CommitsAhead(ctx, r, wt, base); err == nil && ahead == 0 {
			continue // untouched repo
		}
		prs, err := github.RepoPRs(ctx, r, wt, repo, name)
		if err != nil || !mergedAtHead(ctx, r, wt, prs) {
			return false
		}
		merged++
	}
	return merged > 0
}

func mergedAtHead(ctx context.Context, r Runner, wt string, prs []model.PR) bool {
	head, err := git.Head(ctx, r, wt)
	if err != nil {
		return false
	}
	for _, p := range prs {
		if p.Status != model.PRStatusMerged || p.HeadOID == "" {
			continue
		}
		if p.HeadOID == head || git.IsAncestor(ctx, r, wt, head, p.HeadOID) {
			return true
		}
	}
	return false
}

// Remove deletes a feature's worktrees, branches, and directory.
func Remove(ctx context.Context, r Runner, l Layout, name string) error {
	repos, err := Repos(l, name)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		src := filepath.Join(l.Base, repo)
		if err := git.RemoveWorktree(ctx, r, src, filepath.Join(Dir(l, name), repo)); err != nil {
			return fmt.Errorf("remove %s: %w", name, err)
		}
		_ = git.DeleteBranch(ctx, r, src, name)
	}
	if err := os.RemoveAll(Dir(l, name)); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	return nil
}
