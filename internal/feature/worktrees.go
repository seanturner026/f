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
