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
// clean, and every repo with commits has them in the default branch, by
// ancestry or by content (squash merges), and some PR merged (by its saved
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
			if head, err := git.Head(ctx, r, wt); err == nil && notInBase(ctx, r, wt, base, head, prs, false) == 0 {
				results[i] = result{done: true, merged: hasMergedPR(prs, repo)}
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

// mergedAt reports whether rev is (in) the head of a merged PR, by ancestry.
func mergedAt(ctx context.Context, r Runner, wt, head string, prs []model.PR) bool {
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
// uncommitted changes, and commits that are neither pushed nor merged (merged
// by content counts: squash merges). Empty means it's safe to remove.
//
// known are PRs to check against (e.g. from state.json, no network); nil looks
// them up live with gh (and fetches). Known PRs can only be stale in the safe
// direction: a merge they haven't seen makes work look unmerged, never the reverse.
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
	var prs []model.PR
	if known != nil {
		for _, p := range known {
			if p.Repo == repo {
				prs = append(prs, p)
			}
		}
	} else {
		_ = git.Fetch(ctx, r, wt) // live: a merge only shows up in a fresh origin/main
		prs, _ = github.RepoPRs(ctx, r, wt, repo, name)
	}
	// Remove loses the worktree's HEAD and the feature branch; other stack
	// branches stay in the base clone.
	base := baseRef(ctx, r, l, repo)
	head, _ := git.Head(ctx, r, wt)
	revs := []string{head}
	if b := git.RevParse(ctx, r, wt, "refs/heads/"+name); b != "" && b != head {
		revs = append(revs, b)
	}
	for _, rev := range revs {
		if n := notInBase(ctx, r, wt, base, rev, prs, true); n > 0 {
			problems = append(problems, fmt.Sprintf("%s: %d commit(s) not pushed or merged", repo, n))
			break
		}
	}
	return problems
}

// notInBase counts rev's commits that aren't in base: by ancestry, or by
// content after a squash merge. With pushedOK, commits on a remote branch
// don't count (removing the feature doesn't lose them).
func notInBase(ctx context.Context, r Runner, wt, base, rev string, prs []model.PR, pushedOK bool) int {
	if rev == "" {
		return 0
	}
	count := func(after string) int {
		if pushedOK {
			n, err := git.CountUnpushed(ctx, r, wt, rev, after)
			if err != nil {
				return 1
			}
			return n
		}
		c, err := git.Commits(ctx, r, wt, rev, after)
		if err != nil {
			return 1
		}
		return len(c)
	}
	n := count(base)
	if n == 0 || mergedAt(ctx, r, wt, rev, prs) {
		return 0
	}
	commits, err := git.Commits(ctx, r, wt, rev, base)
	if err != nil {
		return n
	}
	k := git.MergedPrefix(ctx, r, wt, base, mainRefs(ctx, r, wt, base, prs), commits)
	switch k {
	case 0:
		return n
	case len(commits):
		return 0
	}
	return count(commits[k-1]) // just the ones after the merge
}

// mainRefs are the revisions that show what's in base: base itself and the
// merge commits of prs that are in its history (see git.InBase).
func mainRefs(ctx context.Context, r Runner, wt, base string, prs []model.PR) []string {
	refs := []string{base}
	for _, p := range prs {
		if p.Status == model.PRStatusMerged && p.MergeOID != "" && git.IsAncestor(ctx, r, wt, p.MergeOID, base) {
			refs = append(refs, p.MergeOID)
		}
	}
	return refs
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
