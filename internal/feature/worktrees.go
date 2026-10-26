package feature

import (
	"context"
	"fmt"
	"os"
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/f/internal/git"
	"github.com/seanturner026/f/internal/github"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/tracing"
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
	ctx, span := tracing.Start(ctx, "add repo", attribute.String("repo", repo))
	defer func() { span.SetAttributes(attribute.String("add.result", string(result))); tracing.End(span, err) }()
	src := l.RepoPath(repo)
	dst := RepoDir(l, name, repo)
	if src == "" {
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
	if l.RepoPath(repo) == "" {
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
		if n := unsaved(ctx, r, wt, base, rev, prs); n > 0 {
			problems = append(problems, fmt.Sprintf("%s: %d commit(s) not pushed or merged", repo, n))
			break
		}
	}
	return problems
}

// unsaved counts rev's commits that removing the feature would lose: not on
// any remote branch, and not in base by ancestry, as an equivalent patch (git
// cherry), or by content after a squash merge. Cheapest checks first: most
// revs need two git calls.
func unsaved(ctx context.Context, r Runner, wt, base, rev string, prs []model.PR) int {
	if rev == "" {
		return 0
	}
	commits, err := git.Unpushed(ctx, r, wt, rev, base)
	if err != nil {
		return 1
	}
	if len(commits) == 0 || mergedAt(ctx, r, wt, rev, prs) {
		return 0
	}
	plus, err := git.Unpicked(ctx, r, wt, base, rev)
	if err != nil {
		return len(commits)
	}
	var left []string
	for _, c := range commits {
		if plus[c] {
			left = append(left, c)
		}
	}
	if len(left) == 0 {
		return 0
	}
	// By content, for squash merges of several commits: the merged ones are a
	// prefix of the branch's own commits.
	own, err := git.Commits(ctx, r, wt, rev, base)
	if err != nil {
		return len(left)
	}
	k := git.MergedPrefix(ctx, r, wt, mainRefs(ctx, r, wt, base, prs), own)
	merged := map[string]bool{}
	for _, c := range own[:k] {
		merged[c] = true
	}
	n := 0
	for _, c := range left {
		if !merged[c] {
			n++
		}
	}
	return n
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
