// Package git wraps the git commands f needs for worktrees and branches.
package git

import (
	"context"
	"errors"
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

// CurrentBranch returns the checked-out branch ("" if HEAD is detached).
func CurrentBranch(ctx context.Context, r Runner, wt string) string {
	out, err := r.Run(ctx, wt, "git", "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// Checkout checks out rev: a branch, or with detach any commit (detached HEAD).
func Checkout(ctx context.Context, r Runner, wt, rev string, detach bool) error {
	args := []string{"checkout", "-q", rev}
	if detach {
		args = []string{"checkout", "-q", "--detach", rev}
	}
	if _, err := r.Run(ctx, wt, "git", args...); err != nil {
		return fmt.Errorf("checkout %s: %w", rev, err)
	}
	return nil
}

// Commits lists the commits reachable from tip but not from any of exclude,
// oldest first.
func Commits(ctx context.Context, r Runner, wt, tip string, exclude ...string) ([]string, error) {
	args := []string{"rev-list", "--reverse", tip}
	for _, e := range exclude {
		args = append(args, "^"+e)
	}
	out, err := r.Run(ctx, wt, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("rev-list: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Unpushed lists the commits on rev that are neither on base nor on any
// remote-tracking branch, oldest first.
func Unpushed(ctx context.Context, r Runner, wt, rev, base string) ([]string, error) {
	out, err := r.Run(ctx, wt, "git", "rev-list", "--reverse", rev, "--not", base, "--remotes")
	if err != nil {
		return nil, fmt.Errorf("rev-list: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// InBase reports whether everything rev changed since it forked from base is
// already in base's history: each file it touched has the same content at rev
// as at one of refs (base itself, or a squash-merge commit in base's history,
// which also covers files that main changed again later). This is how a
// squash-merged branch is recognized: its commits never become ancestors of base.
func InBase(ctx context.Context, r Runner, wt, base, rev string, refs []string) bool {
	mb, err := r.Run(ctx, wt, "git", "merge-base", base, rev)
	if err != nil {
		return false
	}
	files, err := r.Run(ctx, wt, "git", "diff", "--no-renames", "--name-only", mb, rev)
	if err != nil {
		return false
	}
	if files == "" {
		return true
	}
	for _, ref := range refs {
		args := append([]string{"diff", "--quiet", "--no-renames", ref, rev, "--"}, strings.Split(files, "\n")...)
		if _, err := r.Run(ctx, wt, "git", args...); err == nil {
			return true
		}
	}
	return false
}

// MergedPrefix returns how many of commits (oldest first, on top of base's
// history) are already in base by content (see InBase): the commits after
// them were added since the merge.
func MergedPrefix(ctx context.Context, r Runner, wt, base string, refs, commits []string) int {
	for k := len(commits); k > 0; k-- {
		if InBase(ctx, r, wt, base, commits[k-1], refs) {
			return k
		}
	}
	return 0
}

// CherryPick replays commits onto HEAD. On a conflict it aborts (HEAD is left
// as it was) and returns the error.
func CherryPick(ctx context.Context, r Runner, wt string, commits ...string) error {
	args := append([]string{"cherry-pick", "--allow-empty"}, commits...)
	if _, err := r.Run(ctx, wt, "git", args...); err != nil {
		_, _ = r.Run(ctx, wt, "git", "cherry-pick", "--abort")
		return fmt.Errorf("cherry-pick: %w", err)
	}
	return nil
}

// SetBranch moves local branch to commit, only if it's still at old.
func SetBranch(ctx context.Context, r Runner, wt, branch, commit, old string) error {
	if _, err := r.Run(ctx, wt, "git", "update-ref", "refs/heads/"+branch, commit, old); err != nil {
		return fmt.Errorf("move %s: %w", branch, err)
	}
	return nil
}

// DeleteLocalBranch deletes local branch, only if it's still at old.
func DeleteLocalBranch(ctx context.Context, r Runner, wt, branch, old string) error {
	if _, err := r.Run(ctx, wt, "git", "update-ref", "-d", "refs/heads/"+branch, old); err != nil {
		return fmt.Errorf("delete %s: %w", branch, err)
	}
	return nil
}

// ErrProtectedBranch means a force push was refused: the branch is the
// default branch, or not one of the feature's.
var ErrProtectedBranch = errors.New("refusing to force-push")

// ForcePushBranch pushes local branch to the same name on origin with
// --force-with-lease (it fails if origin moved since the last fetch). It only
// pushes branches named prefix or prefix-*, and never the default branch.
func ForcePushBranch(ctx context.Context, r Runner, wt, branch, prefix string) error {
	if branch == "" || branch == DefaultBranch(ctx, r, wt) || (branch != prefix && !strings.HasPrefix(branch, prefix+"-")) {
		return fmt.Errorf("%w %q", ErrProtectedBranch, branch)
	}
	ref := "refs/heads/" + branch
	if _, err := r.Run(ctx, wt, "git", "push", "-q", "--force-with-lease="+ref, "origin", ref+":"+ref); err != nil {
		return fmt.Errorf("push %s: %w", branch, err)
	}
	return nil
}

// Unpicked returns the commits on head (since it forked from upstream) whose
// change isn't on upstream as an equivalent patch (git cherry's "+" lines).
// A squash merge of a one-commit PR is the same patch, so it doesn't count.
func Unpicked(ctx context.Context, r Runner, wt, upstream, head string) (map[string]bool, error) {
	out, err := r.Run(ctx, wt, "git", "cherry", upstream, head)
	if err != nil {
		return nil, fmt.Errorf("cherry: %w", err)
	}
	plus := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if sha, ok := strings.CutPrefix(l, "+ "); ok {
			plus[sha] = true
		}
	}
	return plus, nil
}

// RepairWorktree re-links a worktree that was moved by hand to path (run in
// its base clone), so git finds it there and its branch stays checked out.
func RepairWorktree(ctx context.Context, r Runner, repoDir, path string) error {
	if _, err := r.Run(ctx, repoDir, "git", "worktree", "repair", path); err != nil {
		return fmt.Errorf("worktree repair: %w", err)
	}
	return nil
}
