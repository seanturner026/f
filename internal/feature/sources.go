package feature

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/f/internal/git"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/tracing"
)

// UpdateSources fast-forwards the source clones of feature name's repos that
// had a PR merge, so the clones don't fall behind origin. Only a clone on its
// default branch with no local changes moves. Like a pull, the fast-forward
// runs the clone's post-merge hook: that's how the f repo redeploys itself.
// Returns one line per clone that moved.
func UpdateSources(ctx context.Context, r Runner, l Layout, name string, prs []model.PR) []string {
	repos, _ := Repos(l, name)
	var lines []string
	for _, repo := range repos {
		src := l.RepoPath(repo)
		if src == "" || !hasMergedPR(prs, repo) {
			continue
		}
		if branch := updateSource(ctx, r, repo, src); branch != "" {
			lines = append(lines, repo+": source clone fast-forwarded to origin/"+branch)
		}
	}
	return lines
}

// updateSource fast-forwards src's default branch; returns it if src moved.
func updateSource(ctx context.Context, r Runner, repo, src string) string {
	ctx, span := tracing.Start(ctx, "update source", attribute.String("repo", repo))
	defer span.End()
	defer lockRepo(repo)()
	branch := git.DefaultBranch(ctx, r, src)
	if branch == "" || git.CurrentBranch(ctx, r, src) != branch {
		return "" // someone's working in the clone: leave it
	}
	if n, err := git.ChangedFiles(ctx, r, src); err != nil || n > 0 {
		return ""
	}
	if err := git.Fetch(ctx, r, src); err != nil {
		return ""
	}
	before := git.RevParse(ctx, r, src, "HEAD")
	if err := git.FastForward(ctx, r, src, "origin/"+branch); err != nil {
		return "" // local commits: not a fast-forward
	}
	if git.RevParse(ctx, r, src, "HEAD") == before {
		return ""
	}
	return branch
}

var repoLocks sync.Map // repo name → *sync.Mutex

// lockRepo serializes fetches and source updates of repo within this process:
// f sync --all syncs features concurrently, and their worktrees share the
// base clone's refs. Returns the unlock.
func lockRepo(repo string) func() {
	m, _ := repoLocks.LoadOrStore(repo, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
