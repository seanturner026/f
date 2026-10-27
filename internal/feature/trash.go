package feature

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/seanturner026/f/internal/git"
)

// TrashRetention is how long a removed feature can be restored.
const TrashRetention = 14 * 24 * time.Hour

const trashStamp = "20060102-150405.000" // suffix of a trash entry: <name>-<stamp>

var (
	// ErrNotInTrash means there's no removed feature by that name to restore.
	ErrNotInTrash = errors.New("not in the trash")
	// ErrInTrash means a removed feature still holds the name (and its branch).
	ErrInTrash = errors.New("a removed feature by that name is in the trash")
	// ErrExists means a feature by that name already exists.
	ErrExists = errors.New("feature exists")
)

// Trashed is a removed feature waiting in the trash.
type Trashed struct {
	Name    string
	Dir     string
	Removed time.Time
}

// TrashDir holds removed features until they expire (EmptyTrash).
func TrashDir(l Layout) string { return filepath.Join(l.Features, ".trash") }

// Remove moves a feature into the trash, instantly: its directory is renamed,
// branches and uncommitted files included, and the trash entry's directory is
// returned. Relink it next. Restore brings it back; EmptyTrash deletes it for
// good after TrashRetention.
func Remove(l Layout, name string) (string, error) {
	if err := os.MkdirAll(TrashDir(l), 0o755); err != nil {
		return "", fmt.Errorf("remove %s: %w", name, err)
	}
	dst := filepath.Join(TrashDir(l), name+"-"+time.Now().Format(trashStamp))
	if err := os.Rename(Dir(l, name), dst); err != nil {
		return "", fmt.Errorf("remove %s: %w", name, err)
	}
	return dst, nil
}

// Relink re-links a trash entry's worktrees to their base clones (git
// worktree repair): until then, EmptyTrash's prune would drop git's records
// of them and Restore couldn't bring them back.
func Relink(ctx context.Context, r Runner, l Layout, dir string) error {
	repos, err := gitDirs(dir)
	if err != nil {
		return err
	}
	relink(ctx, r, l, dir, repos)
	return nil
}

// Restore moves feature name's newest trash entry back.
func Restore(ctx context.Context, r Runner, l Layout, name string) error {
	if Exists(l, name) {
		return fmt.Errorf("restore %q: %w", name, ErrExists)
	}
	t, ok := InTrash(l, name)
	if !ok {
		return fmt.Errorf("restore %q: %w", name, ErrNotInTrash)
	}
	if err := os.Rename(t.Dir, Dir(l, name)); err != nil {
		return fmt.Errorf("restore %s: %w", name, err)
	}
	repos, err := Repos(l, name)
	if err != nil {
		return err
	}
	relink(ctx, r, l, Dir(l, name), repos)
	for _, repo := range repos {
		if _, err := git.Head(ctx, r, RepoDir(l, name, repo)); err != nil {
			return fmt.Errorf("restore %s: %s's worktree can't be re-linked (removed before f kept trash?): %w", name, repo, err)
		}
	}
	return nil
}

// relink re-links the worktrees under dir to their base clones, concurrently.
func relink(ctx context.Context, r Runner, l Layout, dir string, repos []string) {
	var wg sync.WaitGroup
	for _, repo := range repos {
		src := l.RepoPath(repo)
		if src == "" {
			continue // base clone deleted: nothing to link to
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = git.RepairWorktree(ctx, r, src, filepath.Join(dir, repo)) }()
	}
	wg.Wait()
}

// Trash lists removed features, newest first.
func Trash(l Layout) []Trashed {
	entries, _ := os.ReadDir(TrashDir(l))
	var out []Trashed
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || len(n) <= len(trashStamp)+1 {
			continue
		}
		at, err := time.ParseInLocation(trashStamp, n[len(n)-len(trashStamp):], time.Local)
		if err != nil {
			continue
		}
		out = append(out, Trashed{Name: n[:len(n)-len(trashStamp)-1], Dir: filepath.Join(TrashDir(l), n), Removed: at})
	}
	slices.SortFunc(out, func(a, b Trashed) int { return b.Removed.Compare(a.Removed) })
	return out
}

// InTrash returns feature name's newest trash entry.
func InTrash(l Layout, name string) (Trashed, bool) {
	for _, t := range Trash(l) {
		if t.Name == name {
			return t, true
		}
	}
	return Trashed{}, false
}

// EmptyTrash deletes trash entries removed at least olderThan ago: their
// files, git's records of their worktrees, and their feature branch (unless
// a live feature or a newer trash entry has the name again).
func EmptyTrash(ctx context.Context, r Runner, l Layout, olderThan time.Duration) error {
	all := Trash(l)
	var errs []error
	for i, t := range all {
		if time.Since(t.Removed) < olderThan {
			continue
		}
		repos, _ := gitDirs(t.Dir)
		if err := os.RemoveAll(t.Dir); err != nil {
			errs = append(errs, err)
			continue
		}
		keepBranch := Exists(l, t.Name) || slices.ContainsFunc(all[:i], func(n Trashed) bool { return n.Name == t.Name })
		for _, repo := range repos {
			src := l.RepoPath(repo)
			if src == "" {
				continue
			}
			_ = git.PruneWorktrees(ctx, r, src)
			if !keepBranch {
				_ = git.DeleteBranch(ctx, r, src, t.Name)
			}
		}
	}
	return errors.Join(errs...)
}

// TrashAge is e.g. "3d ago" for a trash entry.
func TrashAge(t Trashed) string {
	d := time.Since(t.Removed)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
