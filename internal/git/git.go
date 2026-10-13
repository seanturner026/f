// Package git wraps the git commands f needs for worktrees and branches.
package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// IsRepo reports whether path is a git checkout (a clone or a worktree).
func IsRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// DefaultBranch returns the remote default branch of a clone ("main" if unknown).
func DefaultBranch(ctx context.Context, r Runner, repoDir string) string {
	ref, err := r.Run(ctx, repoDir, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	return strings.TrimPrefix(ref, "origin/")
}

// Fetch fetches and prunes origin.
func Fetch(ctx context.Context, r Runner, repoDir string) error {
	if _, err := r.Run(ctx, repoDir, "git", "fetch", "-q", "--prune", "origin"); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	return nil
}

// BranchExists reports whether a local branch exists.
func BranchExists(ctx context.Context, r Runner, repoDir, branch string) bool {
	_, err := r.Run(ctx, repoDir, "git", "show-ref", "-q", "--verify", "refs/heads/"+branch)
	return err == nil
}

// AddWorktree checks out branch at path. With base set, it creates the branch
// from base (untracked); otherwise the branch must already exist.
func AddWorktree(ctx context.Context, r Runner, repoDir, path, branch, base string) error {
	args := []string{"worktree", "add", "-q", path, branch}
	if base != "" {
		args = []string{"worktree", "add", "-q", "--no-track", "-b", branch, path, base}
	}
	if _, err := r.Run(ctx, repoDir, "git", args...); err != nil {
		return fmt.Errorf("add worktree: %w", err)
	}
	return nil
}

// PruneWorktrees drops git's records of worktrees whose directories are gone.
func PruneWorktrees(ctx context.Context, r Runner, repoDir string) error {
	if _, err := r.Run(ctx, repoDir, "git", "worktree", "prune"); err != nil {
		return fmt.Errorf("worktree prune: %w", err)
	}
	return nil
}

// DeleteBranch force-deletes a local branch.
func DeleteBranch(ctx context.Context, r Runner, repoDir, branch string) error {
	if _, err := r.Run(ctx, repoDir, "git", "branch", "-D", branch); err != nil {
		return fmt.Errorf("delete branch: %w", err)
	}
	return nil
}

// ChangedFiles counts uncommitted changes (including untracked files).
func ChangedFiles(ctx context.Context, r Runner, wt string) (int, error) {
	out, err := r.Run(ctx, wt, "git", "status", "--porcelain")
	if err != nil {
		return 0, fmt.Errorf("status: %w", err)
	}
	if out == "" {
		return 0, nil
	}
	return len(strings.Split(out, "\n")), nil
}

// CommitsAhead counts commits on HEAD that aren't on base.
func CommitsAhead(ctx context.Context, r Runner, wt, base string) (int, error) {
	out, err := r.Run(ctx, wt, "git", "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, fmt.Errorf("rev-list: %w", err)
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("rev-list: %w", err)
	}
	return n, nil
}

// Head returns the commit at HEAD.
func Head(ctx context.Context, r Runner, wt string) (string, error) {
	out, err := r.Run(ctx, wt, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse: %w", err)
	}
	return out, nil
}

// IsAncestor reports whether commit a is an ancestor of (or equal to) b.
func IsAncestor(ctx context.Context, r Runner, wt, a, b string) bool {
	_, err := r.Run(ctx, wt, "git", "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// SnapshotCommit records the full working tree (tracked changes and untracked,
// non-ignored files) as a commit on top of HEAD, without touching the index or
// any branch. It uses a throwaway index file.
func SnapshotCommit(ctx context.Context, r Runner, wt string) (string, error) {
	tmp, err := os.CreateTemp("", "f-index-*")
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	index := tmp.Name()
	tmp.Close()
	os.Remove(index) // git wants to create it
	defer os.Remove(index)
	env := "GIT_INDEX_FILE=" + index
	if _, err := r.Run(ctx, wt, "env", env, "git", "read-tree", "HEAD"); err != nil {
		return "", fmt.Errorf("snapshot: read-tree: %w", err)
	}
	if _, err := r.Run(ctx, wt, "env", env, "git", "add", "-A"); err != nil {
		return "", fmt.Errorf("snapshot: add: %w", err)
	}
	tree, err := r.Run(ctx, wt, "env", env, "git", "write-tree")
	if err != nil {
		return "", fmt.Errorf("snapshot: write-tree: %w", err)
	}
	commit, err := r.Run(ctx, wt, "git", "commit-tree", tree, "-p", "HEAD", "-m", "f snapshot")
	if err != nil {
		return "", fmt.Errorf("snapshot: commit-tree: %w", err)
	}
	return commit, nil
}

// TreeOf returns the tree object of rev ("" if rev doesn't exist).
func TreeOf(ctx context.Context, r Runner, wt, rev string) string {
	tree, err := r.Run(ctx, wt, "git", "rev-parse", "-q", "--verify", rev+"^{tree}")
	if err != nil {
		return ""
	}
	return tree
}

// UpdateRef points ref at commit.
func UpdateRef(ctx context.Context, r Runner, wt, ref, commit string) error {
	if _, err := r.Run(ctx, wt, "git", "update-ref", ref, commit); err != nil {
		return fmt.Errorf("update-ref %s: %w", ref, err)
	}
	return nil
}

// DeleteRef removes ref (no error if it doesn't exist).
func DeleteRef(ctx context.Context, r Runner, wt, ref string) {
	_, _ = r.Run(ctx, wt, "git", "update-ref", "-d", ref)
}

// IntentToAdd marks untracked, non-ignored files as intent-to-add (git add -N),
// so diffs against a revision treat them like any other change. Tracked files
// and the staged content are unaffected.
func IntentToAdd(ctx context.Context, r Runner, wt string) error {
	if _, err := r.Run(ctx, wt, "git", "add", "--intent-to-add", "--", "."); err != nil {
		return fmt.Errorf("intent-to-add: %w", err)
	}
	return nil
}

// ResetHard moves the checked-out branch to rev, discarding local changes.
func ResetHard(ctx context.Context, r Runner, wt, rev string) error {
	if _, err := r.Run(ctx, wt, "git", "reset", "--hard", rev); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	return nil
}

// RebaseOnto replays the commits after upstream onto newBase. On a conflict
// it aborts (the branch is left as it was) and returns the error.
func RebaseOnto(ctx context.Context, r Runner, wt, newBase, upstream string) error {
	if _, err := r.Run(ctx, wt, "git", "rebase", "--onto", newBase, upstream); err != nil {
		_, _ = r.Run(ctx, wt, "git", "rebase", "--abort")
		return fmt.Errorf("rebase: %w", err)
	}
	return nil
}

// RevParse resolves rev to a commit ("" if it doesn't resolve).
func RevParse(ctx context.Context, r Runner, wt, rev string) string {
	out, err := r.Run(ctx, wt, "git", "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return out
}
