package feature

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

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
	dst := RepoDir(l, name, repo)
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
		_ = github.StackInit(ctx, r, dst, name) // PRs are always a stack (AGENTS.md)
		return AddedExistingBranch, "", nil
	}
	base = baseRef(ctx, r, l, repo)
	if err := git.AddWorktree(ctx, r, src, dst, name, base); err != nil {
		return "", "", fmt.Errorf("add %s: %w", repo, err)
	}
	_ = github.StackInit(ctx, r, dst, name) // PRs are always a stack (AGENTS.md)
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
			results[i], failures[i] = github.RepoPRs(ctx, r, RepoDir(l, name, repo), repo, name)
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

// Finished reports whether a feature's work has all landed: every repo is
// clean, and every repo with commits has them in a merged PR (by its saved
// PRs: no network, so it's instant; a merge the saved state hasn't seen just
// means the feature isn't offered for cleanup yet). Repos are checked
// concurrently.
func Finished(ctx context.Context, r Runner, l Layout, name string) bool {
	repos, err := Repos(l, name)
	if err != nil {
		return false
	}
	state, _ := LoadState(l, name)
	if !slices.ContainsFunc(state.PRs, func(p model.PR) bool { return p.Status == model.PRStatusMerged }) {
		return false // nothing merged yet: can't be finished, skip the git checks
	}
	type result struct{ done, merged bool }
	results := make([]result, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wt := RepoDir(l, name, repo)
			if n, err := git.ChangedFiles(ctx, r, wt); err != nil || n > 0 {
				return // dirty: not done
			}
			base := baseRef(ctx, r, l, repo)
			if ahead, err := git.CommitsAhead(ctx, r, wt, base); err == nil && ahead == 0 {
				// untouched, or synced after its PR merged (f sync)
				results[i] = result{done: true, merged: hasMergedPR(state.PRs, repo)}
				return
			}
			var prs []model.PR
			for _, p := range state.PRs {
				if p.Repo == repo {
					prs = append(prs, p)
				}
			}
			if mergedAtHead(ctx, r, wt, prs) {
				results[i] = result{done: true, merged: true}
			}
		}()
	}
	wg.Wait()
	merged := 0
	for _, res := range results {
		if !res.done {
			return false
		}
		if res.merged {
			merged++
		}
	}
	return merged > 0
}

func hasMergedPR(prs []model.PR, repo string) bool {
	for _, p := range prs {
		if p.Repo == repo && p.Status == model.PRStatusMerged {
			return true
		}
	}
	return false
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

// Unmerged describes work in the feature that removing it would lose:
// uncommitted changes, and commits not contained in a merged PR. Empty means
// it's safe to remove.
//
// known are PRs to check against (e.g. from state.json, no network); nil looks
// them up live with gh. Known PRs can only be stale in the safe direction: a
// merge they haven't seen makes work look unmerged, never the reverse.
func Unmerged(ctx context.Context, r Runner, l Layout, name string, known []model.PR) ([]string, error) {
	repos, err := Repos(l, name)
	if err != nil {
		return nil, err
	}
	// Each repo may need gh calls: check them concurrently, report in order.
	found := make([][]string, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() { defer wg.Done(); found[i] = unmergedIn(ctx, r, l, name, repo, known) }()
	}
	wg.Wait()
	var problems []string
	for _, p := range found {
		problems = append(problems, p...)
	}
	return problems, nil
}

func unmergedIn(ctx context.Context, r Runner, l Layout, name, repo string, known []model.PR) []string {
	var problems []string
	wt := RepoDir(l, name, repo)
	if !git.IsRepo(filepath.Join(l.Base, repo)) {
		return nil // base clone deleted: the worktree's history is gone with it
	}
	if n, err := git.ChangedFiles(ctx, r, wt); err != nil {
		return []string{fmt.Sprintf("%s: %v", repo, err)}
	} else if n > 0 {
		problems = append(problems, fmt.Sprintf("%s: %d uncommitted change(s)", repo, n))
	}
	base := baseRef(ctx, r, l, repo)
	ahead, err := git.CommitsAhead(ctx, r, wt, base)
	if err != nil || ahead == 0 {
		return problems
	}
	var prs []model.PR
	if known != nil {
		for _, p := range known {
			if p.Repo == repo {
				prs = append(prs, p)
			}
		}
	} else if prs, err = github.RepoPRs(ctx, r, wt, repo, name); err != nil {
		prs = nil
	}
	if !mergedAtHead(ctx, r, wt, prs) {
		problems = append(problems, fmt.Sprintf("%s: %d commit(s) not in a merged PR", repo, ahead))
	}
	return problems
}

// Remove deletes a feature's worktrees, branches, and directory, fast: the
// directory is renamed into .features/.trash (instant, whatever its size),
// git forgets the worktrees (worktree prune) and drops the branch, and
// EmptyTrash deletes the files later (in the background). Check Unmerged
// first: Remove doesn't refuse dirty worktrees.
func Remove(ctx context.Context, r Runner, l Layout, name string) error {
	repos, err := Repos(l, name)
	if err != nil {
		return err
	}
	trash := TrashDir(l)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	dst := filepath.Join(trash, name+"-"+time.Now().Format("20060102-150405.000"))
	if err := os.Rename(Dir(l, name), dst); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	var wg sync.WaitGroup
	for _, repo := range repos {
		src := filepath.Join(l.Base, repo)
		if !git.IsRepo(src) {
			continue // base clone deleted: nothing to unregister
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = git.PruneWorktrees(ctx, r, src) // the worktree's directory is gone now
			_ = git.DeleteBranch(ctx, r, src, name)
		}()
	}
	wg.Wait()
	return nil
}

// TrashDir holds removed features until EmptyTrash deletes them.
func TrashDir(l Layout) string { return filepath.Join(l.Features, ".trash") }

// EmptyTrash deletes removed features' files.
func EmptyTrash(l Layout) error { return os.RemoveAll(TrashDir(l)) }
