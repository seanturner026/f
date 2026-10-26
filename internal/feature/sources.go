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
	moved := make([]string, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		src := l.RepoPath(repo)
		if src == "" || !hasMergedPR(prs, repo) {
			continue
		}
		wg.Go(func() {
			moved[i] = once(ctx, "update source "+repo, func() string { return updateSource(ctx, r, repo, src) })
		})
	}
	wg.Wait()
	var lines []string
	for i, branch := range moved {
		if branch != "" {
			lines = append(lines, repos[i]+": source clone fast-forwarded to origin/"+branch)
		}
	}
	return lines
}

// updateSource fast-forwards src's default branch; returns it if src moved.
func updateSource(ctx context.Context, r Runner, repo, src string) string {
	ctx, span := tracing.Start(ctx, "update source", attribute.String("repo", repo))
	defer span.End()
	branch := git.DefaultBranch(ctx, r, src)
	if branch == "" || git.CurrentBranch(ctx, r, src) != branch {
		return "" // someone's working in the clone: leave it
	}
	if n, err := git.ChangedFiles(ctx, r, src); err != nil || n > 0 {
		return ""
	}
	if err := fetchRepo(ctx, r, repo, src); err != nil {
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

// fetchRepo fetches repo from dir (its source clone or a worktree of it: they
// share refs), once per run (see WithRunOnce).
func fetchRepo(ctx context.Context, r Runner, repo, dir string) error {
	return once(ctx, "fetch "+repo, func() error { return git.Fetch(ctx, r, dir) })
}

type runOnceKey struct{}

// WithRunOnce makes work that's the same for every feature (fetching a repo,
// updating its source clone) happen once for all syncs that share ctx.
func WithRunOnce(ctx context.Context) context.Context {
	return context.WithValue(ctx, runOnceKey{}, &sync.Map{})
}

// once runs f once per key within ctx's run (concurrent callers wait for the
// first), or every time without WithRunOnce.
func once[T any](ctx context.Context, key string, f func() T) T {
	m, ok := ctx.Value(runOnceKey{}).(*sync.Map)
	if !ok {
		return f()
	}
	v, _ := m.LoadOrStore(key, sync.OnceValue(f))
	return v.(func() T)()
}
