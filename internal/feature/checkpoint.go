package feature

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/f/internal/git"
	"github.com/seanturner026/f/internal/tracing"
)

const (
	// LastRunRef marks the code as it was before the agent's most recent run that
	// changed code. Per-worktree, so features never collide.
	LastRunRef = "refs/worktree/pi-last"
	// pendingRef is the snapshot taken when a run starts.
	pendingRef = "refs/worktree/pi-pending"
	// TurnsRef chains a snapshot after each run that changed code, and one of
	// your own edits between runs, so a review can show any of them alone.
	TurnsRef = "refs/worktree/f/turns"
)

// CheckpointBegin snapshots every repo in the feature as a run starts. The
// agent waits for it before each prompt, so repos are snapshotted concurrently.
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
			ctx, span := tracing.Start(ctx, "checkpoint begin repo", attribute.String("repo", repo))
			defer func() { tracing.End(span, errs[i]) }()
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

// CheckpointEnd runs when the agent finishes. For each repo whose code changed since
// CheckpointBegin, the run's starting snapshot becomes LastRunRef and the run
// joins TurnsRef, named after prompt; repos the run didn't touch keep their
// previous marker. Repos are checked concurrently. Returns the changed repos.
func CheckpointEnd(ctx context.Context, r Runner, l Layout, name, prompt string) ([]string, error) {
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
			ctx, span := tracing.Start(ctx, "checkpoint end repo", attribute.String("repo", repo))
			defer func() { tracing.End(span, errs[i]) }()
			changed[i], errs[i] = checkpointEndRepo(ctx, r, RepoDir(l, name, repo), prompt)
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

func checkpointEndRepo(ctx context.Context, r Runner, wt, prompt string) (bool, error) {
	before := git.TreeOf(ctx, r, wt, pendingRef)
	if before == "" {
		return false, nil // no run started here
	}
	defer git.DeleteRef(ctx, r, wt, pendingRef)
	now, err := git.SnapshotCommit(ctx, r, wt)
	if err != nil {
		return false, err
	}
	after := git.TreeOf(ctx, r, wt, now)
	if after == before {
		return false, nil
	}
	if err := recordTurn(ctx, r, wt, before, after, prompt); err != nil {
		return false, err
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

// recordTurn appends a run to TurnsRef: first your edits since the last run
// (if any), then the run itself, each a commit of the whole tree.
func recordTurn(ctx context.Context, r Runner, wt, before, after, prompt string) error {
	tip := git.RevParse(ctx, r, wt, TurnsRef)
	if tip == "" || git.TreeOf(ctx, r, wt, tip) != before {
		msg := "your edits between turns"
		if tip == "" {
			msg = "before the first turn" // the base the first turn is shown against
		}
		var err error
		if tip, err = git.CommitTree(ctx, r, wt, before, tip, msg); err != nil {
			return err
		}
	}
	turn, err := git.CommitTree(ctx, r, wt, after, tip, "turn: "+turnTitle(prompt))
	if err != nil {
		return err
	}
	return git.UpdateRef(ctx, r, wt, TurnsRef, turn)
}

// turnTitle is a prompt's first line, cut to fit a commit list.
func turnTitle(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	if line == "" {
		return "(no prompt)"
	}
	if r := []rune(line); len(r) > 72 {
		return string(r[:71]) + "…"
	}
	return line
}

// TurnsRange is the tuicr/git range of every recorded turn in wt ("" if none).
func TurnsRange(ctx context.Context, r Runner, wt string) string {
	root, err := r.Run(ctx, wt, "git", "rev-list", "--max-parents=0", TurnsRef)
	if err != nil || root == "" {
		return ""
	}
	return strings.Fields(root)[0] + ".." + TurnsRef
}
