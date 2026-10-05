package feature_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/model"
	"github.com/seanturner026/pi/internal/shell"
)

// syncFixture is a repo "app" with an origin, a base clone, feature "feat"'s
// worktree, and a second clone ("github") that merges PRs into main.
type syncFixture struct {
	t                  *testing.T
	l                  feature.Layout
	origin, wt, github string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	root := t.TempDir()
	f := &syncFixture{t: t, l: feature.NewLayout(filepath.Join(root, "base")), origin: filepath.Join(root, "origin.git"), github: filepath.Join(root, "github")}
	git(t, root, "init", "-q", "--bare", "-b", "main", f.origin)
	git(t, root, "clone", "-q", f.origin, f.github)
	write(t, filepath.Join(f.github, "a.txt"), "one\n")
	git(t, f.github, "add", ".")
	git(t, f.github, "commit", "-q", "-m", "init")
	git(t, f.github, "push", "-q", "origin", "main")

	base := filepath.Join(f.l.Base, "app")
	git(t, root, "clone", "-q", f.origin, base)
	git(t, base, "remote", "set-head", "origin", "main")
	f.wt = filepath.Join(feature.Dir(f.l, "feat"), "app")
	git(t, base, "worktree", "add", "-q", "-b", "feat", f.wt, "origin/main")
	return f
}

// commit commits content to file in the worktree.
func (f *syncFixture) commit(file, content string) string {
	write(f.t, filepath.Join(f.wt, file), content)
	git(f.t, f.wt, "add", ".")
	git(f.t, f.wt, "commit", "-q", "-m", "change "+file)
	return git(f.t, f.wt, "rev-parse", "HEAD")
}

// merge merges branch feat (as pushed) into main on "GitHub" with method.
func (f *syncFixture) merge(method string) {
	git(f.t, f.wt, "push", "-q", "-f", "origin", "feat")
	g := f.github
	git(f.t, g, "fetch", "-q", "origin")
	git(f.t, g, "checkout", "-q", "main")
	git(f.t, g, "reset", "-q", "--hard", "origin/main")
	switch method {
	case "squash":
		git(f.t, g, "merge", "-q", "--squash", "origin/feat")
		git(f.t, g, "commit", "-q", "-m", "squashed")
	case "merge":
		git(f.t, g, "merge", "-q", "--no-ff", "-m", "merge", "origin/feat")
	case "rebase":
		git(f.t, g, "cherry-pick", "origin/main..origin/feat")
	}
	git(f.t, g, "push", "-q", "origin", "main")
}

// mainMoves adds an unrelated commit on main (someone else's PR).
func (f *syncFixture) mainMoves(file, content string) {
	g := f.github
	git(f.t, g, "fetch", "-q", "origin")
	git(f.t, g, "reset", "-q", "--hard", "origin/main")
	write(f.t, filepath.Join(g, file), content)
	git(f.t, g, "add", ".")
	git(f.t, g, "commit", "-q", "-m", "other "+file)
	git(f.t, g, "push", "-q", "origin", "main")
}

func (f *syncFixture) reconcile(prs ...model.PR) []feature.SyncResult {
	res, err := feature.Reconcile(context.Background(), shell.Exec{}, f.l, "feat", prs)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func merged(head string) model.PR {
	return model.PR{Repo: "app", Number: 1, Status: model.PRStatusMerged, HeadOID: head, Head: "feat"}
}

func onlyAction(t *testing.T, res []feature.SyncResult, want feature.SyncAction) {
	t.Helper()
	if want == feature.SyncNothing {
		if len(res) != 0 {
			t.Fatalf("results = %+v, want none", res)
		}
		return
	}
	if len(res) != 1 || res[0].Action != want {
		t.Fatalf("results = %+v, want one %q", res, want)
	}
}

func TestReconcileMergeMethods(t *testing.T) {
	for _, method := range []string{"squash", "merge", "rebase"} {
		t.Run(method, func(t *testing.T) {
			f := newSyncFixture(t)
			head := f.commit("b.txt", "pr\n")
			f.mainMoves("m.txt", "meanwhile\n") // main moved while the PR was open
			f.merge(method)
			onlyAction(t, f.reconcile(merged(head)), feature.SyncReset)
			if got, want := git(t, f.wt, "rev-parse", "HEAD"), git(t, f.wt, "rev-parse", "origin/main"); got != want {
				t.Errorf("HEAD = %s, want origin/main %s", got, want)
			}
			// Again: nothing to do, even after main moves on.
			onlyAction(t, f.reconcile(merged(head)), feature.SyncNothing)
			f.mainMoves("c.txt", "other\n")
			onlyAction(t, f.reconcile(merged(head)), feature.SyncNothing)
		})
	}
}

func TestReconcileNewCommitsRebased(t *testing.T) {
	f := newSyncFixture(t)
	head := f.commit("b.txt", "pr\n")
	f.merge("squash")
	f.commit("next.txt", "next pr\n")
	onlyAction(t, f.reconcile(merged(head)), feature.SyncRebased)
	if n := git(t, f.wt, "rev-list", "--count", "origin/main..HEAD"); n != "1" {
		t.Errorf("commits ahead of main = %s, want 1 (just the new one)", n)
	}
	if got := git(t, f.wt, "show", "HEAD:next.txt"); got != "next pr" {
		t.Errorf("new commit content = %q", got)
	}
}

func TestReconcileLeavesUnsafeRepos(t *testing.T) {
	t.Run("uncommitted changes", func(t *testing.T) {
		f := newSyncFixture(t)
		head := f.commit("b.txt", "pr\n")
		f.merge("squash")
		write(t, filepath.Join(f.wt, "wip.txt"), "wip\n")
		onlyAction(t, f.reconcile(merged(head)), feature.SyncNeeded)
		if got := git(t, f.wt, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved to %s", got)
		}
	})
	t.Run("conflicting new commit", func(t *testing.T) {
		f := newSyncFixture(t)
		head := f.commit("b.txt", "pr\n")
		f.merge("squash")
		f.mainMoves("a.txt", "theirs\n")
		mine := f.commit("a.txt", "mine\n")
		onlyAction(t, f.reconcile(merged(head)), feature.SyncNeeded)
		if got := git(t, f.wt, "rev-parse", "HEAD"); got != mine {
			t.Errorf("HEAD moved to %s, want untouched %s", got, mine)
		}
		if _, err := os.Stat(filepath.Join(git(t, f.wt, "rev-parse", "--git-dir"), "rebase-merge")); err == nil {
			t.Error("rebase left in progress")
		}
	})
}

func TestReconcileIgnoresUnmergedAndOtherBranches(t *testing.T) {
	f := newSyncFixture(t)
	head := f.commit("b.txt", "pr\n")
	open := merged(head)
	open.Status = model.PRStatusOpen
	other := merged(head)
	other.Head = "someone-else"
	onlyAction(t, f.reconcile(open, other), feature.SyncNothing)
}
