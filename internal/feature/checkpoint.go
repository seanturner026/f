package feature

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/seanturner026/pi/internal/git"
)

const (
	// LastRunRef marks the code as it was before pi's most recent run that
	// changed code. Per-worktree, so features never collide.
	LastRunRef = "refs/worktree/pi-last"
	// pendingRef is the snapshot taken when a run starts.
	pendingRef = "refs/worktree/pi-pending"
)

// CheckpointBegin snapshots every repo in the feature as a run starts. pi
// waits for it before each prompt, so repos are snapshotted concurrently.
func CheckpointBegin(ctx context.Context, r Runner, l Layout, name string) error {
	repos, err := Repos(l, name)
	if err != nil {
		return err
	}
	errs := make([]error, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wt := RepoDir(l, name, repo)
			commit, err := git.SnapshotCommit(ctx, r, wt)
			if err == nil {
				err = git.UpdateRef(ctx, r, wt, pendingRef, commit)
			}
			if err != nil {
				errs[i] = fmt.Errorf("checkpoint %s: %w", repo, err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// CheckpointEnd runs when pi finishes. For each repo whose code changed since
// CheckpointBegin, the run's starting snapshot becomes LastRunRef; repos the
// run didn't touch keep their previous marker. Repos are checked concurrently.
// Returns the changed repos.
func CheckpointEnd(ctx context.Context, r Runner, l Layout, name string) ([]string, error) {
	repos, err := Repos(l, name)
	if err != nil {
		return nil, err
	}
	changed := make([]bool, len(repos))
	errs := make([]error, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			changed[i], errs[i] = checkpointEndRepo(ctx, r, RepoDir(l, name, repo))
			if errs[i] != nil {
				errs[i] = fmt.Errorf("checkpoint %s: %w", repo, errs[i])
			}
		}()
	}
	wg.Wait()
	var out []string
	for i, repo := range repos {
		if changed[i] {
			out = append(out, repo)
		}
	}
	return out, errors.Join(errs...)
}

func checkpointEndRepo(ctx context.Context, r Runner, wt string) (bool, error) {
	before := git.TreeOf(ctx, r, wt, pendingRef)
	if before == "" {
		return false, nil // no run started here
	}
	defer git.DeleteRef(ctx, r, wt, pendingRef)
	now, err := git.SnapshotCommit(ctx, r, wt)
	if err != nil {
		return false, err
	}
	if git.TreeOf(ctx, r, wt, now) == before {
		return false, nil
	}
	commit, err := r.Run(ctx, wt, "git", "rev-parse", pendingRef)
	if err != nil {
		return false, err
	}
	if err := git.UpdateRef(ctx, r, wt, LastRunRef, commit); err != nil {
		return false, err
	}
	// New files show up as untracked in diff tools whatever the base
	// revision; intent-to-add makes "since last run" diffs exact.
	if err := git.IntentToAdd(ctx, r, wt); err != nil {
		return false, err
	}
	return true, nil
}
