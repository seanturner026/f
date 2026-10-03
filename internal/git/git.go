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

// RemoveWorktree removes the worktree at path.
func RemoveWorktree(ctx context.Context, r Runner, repoDir, path string) error {
	if _, err := r.Run(ctx, repoDir, "git", "worktree", "remove", path); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
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
