package feature

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/seanturner026/pi/internal/git"
)

const (
	// LastRunRef marks the code as it was before pi's most recent run that
	// changed code. Per-worktree, so features never collide.
	LastRunRef = "refs/worktree/pi-last"
	// pendingRef is the snapshot taken when a run starts.
	pendingRef = "refs/worktree/pi-pending"
)

// CheckpointBegin snapshots every repo in the feature as a run starts.
func CheckpointBegin(ctx context.Context, r Runner, l Layout, name string) error {
	repos, err := Repos(l, name)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		wt := filepath.Join(Dir(l, name), repo)
		commit, err := git.SnapshotCommit(ctx, r, wt)
		if err != nil {
			return fmt.Errorf("checkpoint %s: %w", repo, err)
		}
		if err := git.UpdateRef(ctx, r, wt, pendingRef, commit); err != nil {
			return fmt.Errorf("checkpoint %s: %w", repo, err)
		}
	}
	return nil
}

// CheckpointEnd runs when pi finishes. For each repo whose code changed since
// CheckpointBegin, the run's starting snapshot becomes LastRunRef; repos the
// run didn't touch keep their previous marker. Returns the changed repos.
func CheckpointEnd(ctx context.Context, r Runner, l Layout, name string) ([]string, error) {
	repos, err := Repos(l, name)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, repo := range repos {
		wt := filepath.Join(Dir(l, name), repo)
		before := git.TreeOf(ctx, r, wt, pendingRef)
		if before == "" {
			continue // no run started here
		}
		now, err := git.SnapshotCommit(ctx, r, wt)
		if err != nil {
			return changed, fmt.Errorf("checkpoint %s: %w", repo, err)
		}
		if git.TreeOf(ctx, r, wt, now) != before {
			commit, err := r.Run(ctx, wt, "git", "rev-parse", pendingRef)
			if err != nil {
				return changed, fmt.Errorf("checkpoint %s: %w", repo, err)
			}
			if err := git.UpdateRef(ctx, r, wt, LastRunRef, commit); err != nil {
				return changed, fmt.Errorf("checkpoint %s: %w", repo, err)
			}
			// New files show up as untracked in diff tools whatever the base
			// revision; intent-to-add makes "since last run" diffs exact.
			if err := git.IntentToAdd(ctx, r, wt); err != nil {
				return changed, fmt.Errorf("checkpoint %s: %w", repo, err)
			}
			changed = append(changed, repo)
		}
		git.DeleteRef(ctx, r, wt, pendingRef)
	}
	return changed, nil
}
