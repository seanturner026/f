package feature_test

import (
	"context"
	"errors"
	"fmt"
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

// ghFake answers gh calls (a stack of branches, bottom first; none: not a
// stack) and records them; git runs for real.
type ghFake struct {
	stack []string
	calls []string
}

func (g *ghFake) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name != "gh" {
		return shell.Exec{}.Run(ctx, dir, name, args...)
	}
	call := strings.Join(args, " ")
	g.calls = append(g.calls, call)
	if call == "stack view --json" {
		if g.stack == nil {
			return "", errors.New("not a stack")
		}
		var b strings.Builder
		b.WriteString(`{"branches":[`)
		for i, n := range g.stack {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"name":%q}`, n)
		}
		b.WriteString("]}")
		return b.String(), nil
	}
	return "", nil
}

func (f *syncFixture) reconcile(prs ...model.PR) []feature.SyncResult {
	return f.reconcileWith(&ghFake{}, false, prs...)
}

func (f *syncFixture) reconcileWith(gh *ghFake, push bool, prs ...model.PR) []feature.SyncResult {
	res, err := feature.Reconcile(context.Background(), gh, f.l, "feat", prs, push)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func merged(head string) model.PR {
	return model.PR{Repo: "app", Number: 1, Status: model.PRStatusMerged, HeadOID: head, Head: "feat"}
}

// branch starts branch at the worktree's HEAD and checks it out (gh stack add).
func (f *syncFixture) branch(name string) {
	git(f.t, f.wt, "checkout", "-q", "-b", name)
}

// squash pushes branch and squash-merges it into main the way GitHub does:
// one new commit on main, remote branch deleted. Returns the merge commit.
func (f *syncFixture) squash(branch string) string {
	git(f.t, f.wt, "push", "-q", "-f", "origin", branch)
	g := f.github
	git(f.t, g, "fetch", "-q", "origin")
	git(f.t, g, "checkout", "-q", "main")
	git(f.t, g, "reset", "-q", "--hard", "origin/main")
	git(f.t, g, "merge", "-q", "--squash", "origin/"+branch)
	git(f.t, g, "commit", "-q", "-m", "squashed "+branch)
	git(f.t, g, "push", "-q", "origin", "main")
	git(f.t, g, "push", "-q", "origin", "--delete", branch)
	return git(f.t, g, "rev-parse", "HEAD")
}

// summary is "repo action: branch action, ..." for comparing results.
func summary(res []feature.SyncResult) string {
	var parts []string
	for _, r := range res {
		s := fmt.Sprintf("%s %s:", r.Repo, r.Action)
		for _, b := range r.Branches {
			s += fmt.Sprintf(" %s=%s", b.Branch, b.Action)
			if b.New > 0 {
				s += fmt.Sprintf("(%d)", b.New)
			}
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func wantSummary(t *testing.T, res []feature.SyncResult, want string) {
	t.Helper()
	if got := summary(res); got != want {
		t.Fatalf("results = %q, want %q (%+v)", got, want, res)
	}
}

func TestReconcileMergeMethods(t *testing.T) {
	for _, method := range []string{"squash", "merge", "rebase"} {
		t.Run(method, func(t *testing.T) {
			f := newSyncFixture(t)
			head := f.commit("b.txt", "pr\n")
			f.mainMoves("m.txt", "meanwhile\n") // main moved while the PR was open
			f.merge(method)
			wantSummary(t, f.reconcile(merged(head)), "app synced: feat=reset")
			if got, want := git(t, f.wt, "rev-parse", "HEAD"), git(t, f.wt, "rev-parse", "origin/main"); got != want {
				t.Errorf("HEAD = %s, want origin/main %s", got, want)
			}
			// Again: nothing to do, even after main moves on.
			wantSummary(t, f.reconcile(merged(head)), "")
			f.mainMoves("c.txt", "other\n")
			wantSummary(t, f.reconcile(merged(head)), "")
		})
	}
}

func TestReconcileRewrittenHead(t *testing.T) {
	// GitHub rewrote the branch when it merged: the saved head isn't local.
	f := newSyncFixture(t)
	f.commit("b.txt", "pr\n")
	f.squash("feat")
	wantSummary(t, f.reconcile(merged("bff7b50bff7b50bff7b50bff7b50bff7b50bff7b")), "app synced: feat=reset")
}

func TestReconcileNewCommitsKept(t *testing.T) {
	f := newSyncFixture(t)
	head := f.commit("b.txt", "pr\n")
	f.squash("feat")
	f.commit("next.txt", "next pr\n")
	wantSummary(t, f.reconcile(merged(head)), "app synced: feat=kept(1)")
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
		f.squash("feat")
		write(t, filepath.Join(f.wt, "wip.txt"), "wip\n")
		wantSummary(t, f.reconcile(merged(head)), "app needs sync:")
		if got := git(t, f.wt, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved to %s", got)
		}
	})
	t.Run("conflicting new commit", func(t *testing.T) {
		f := newSyncFixture(t)
		head := f.commit("b.txt", "pr\n")
		f.squash("feat")
		f.mainMoves("a.txt", "theirs\n")
		mine := f.commit("a.txt", "mine\n")
		wantSummary(t, f.reconcile(merged(head)), "app needs sync:")
		if got := git(t, f.wt, "rev-parse", "HEAD"); got != mine {
			t.Errorf("HEAD moved to %s, want untouched %s", got, mine)
		}
		if got := git(t, f.wt, "symbolic-ref", "--short", "HEAD"); got != "feat" {
			t.Errorf("checked out %q, want feat", got)
		}
		if _, err := os.Stat(filepath.Join(git(t, f.wt, "rev-parse", "--git-dir"), "CHERRY_PICK_HEAD")); err == nil {
			t.Error("cherry-pick left in progress")
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
	wantSummary(t, f.reconcile(open, other), "")
}

func stackPR(n int, head, base string, status model.PRStatus, mergeOID string) model.PR {
	return model.PR{Repo: "app", Number: n, Status: status, Head: head, Base: base, HeadOID: "rewritten-by-github", MergeOID: mergeOID}
}

func TestReconcileStack(t *testing.T) {
	stack := []string{"feat", "feat-staging", "feat-production"}

	t.Run("all merged", func(t *testing.T) {
		f := newSyncFixture(t)
		f.commit("b.txt", "bottom\n")
		f.branch("feat-staging")
		f.commit("c.txt", "staging\n")
		f.branch("feat-production")
		f.commit("d.txt", "production\n")
		m1, m2, m3 := f.squash("feat"), f.squash("feat-staging"), f.squash("feat-production")
		f.mainMoves("d.txt", "changed again on main\n") // content check must use the merge commit
		gh := &ghFake{stack: stack}
		wantSummary(t, f.reconcileWith(gh, true,
			stackPR(1, "feat", "main", model.PRStatusMerged, m1),
			stackPR(2, "feat-staging", "feat", model.PRStatusMerged, m2),
			stackPR(3, "feat-production", "feat-staging", model.PRStatusMerged, m3),
		), "app synced: feat=reset feat-staging=deleted feat-production=deleted")
		if got, want := git(t, f.wt, "rev-parse", "HEAD"), git(t, f.wt, "rev-parse", "origin/main"); got != want {
			t.Errorf("HEAD = %s, want origin/main %s", got, want)
		}
		if got := git(t, f.wt, "symbolic-ref", "--short", "HEAD"); got != "feat" {
			t.Errorf("checked out %q, want feat", got)
		}
		if out := git(t, f.wt, "branch", "--list", "feat-*"); out != "" {
			t.Errorf("merged branches still there: %q", out)
		}
		if got := strings.Join(gh.calls, "|"); !strings.Contains(got, "stack unstack --local|stack init feat") {
			t.Errorf("gh calls = %q, want the stack re-initialized at feat", got)
		}
	})

	t.Run("bottom merged", func(t *testing.T) {
		f := newSyncFixture(t)
		f.commit("b.txt", "bottom\n")
		f.branch("feat-staging")
		f.commit("c.txt", "staging\n")
		git(t, f.wt, "push", "-q", "origin", "feat-staging")
		m1 := f.squash("feat")
		gh := &ghFake{stack: stack[:2]}
		wantSummary(t, f.reconcileWith(gh, true,
			stackPR(1, "feat", "main", model.PRStatusMerged, m1),
			stackPR(2, "feat-staging", "feat", model.PRStatusOpen, ""),
		), "app synced: feat=reset feat-staging=rebased")
		if n := git(t, f.wt, "rev-list", "--count", "origin/main..feat-staging"); n != "1" {
			t.Errorf("feat-staging is %s commits ahead of main, want 1 (its own)", n)
		}
		if got, want := git(t, f.wt, "rev-parse", "origin/feat-staging"), git(t, f.wt, "rev-parse", "feat-staging"); got != want {
			t.Errorf("origin/feat-staging = %s, want pushed %s", got, want)
		}
		if got := git(t, f.wt, "symbolic-ref", "--short", "HEAD"); got != "feat-staging" {
			t.Errorf("checked out %q, want feat-staging (as before)", got)
		}
		if got := strings.Join(gh.calls, "|"); !strings.Contains(got, "pr edit 2 --base main") {
			t.Errorf("gh calls = %q, want #2 retargeted to main", got)
		}
		if got := strings.Join(gh.calls, "|"); strings.Contains(got, "stack init") {
			t.Errorf("gh calls = %q: nothing deleted, the stack needs no re-init", got)
		}
	})

	t.Run("background leaves stacks alone", func(t *testing.T) {
		f := newSyncFixture(t)
		f.commit("b.txt", "bottom\n")
		f.branch("feat-staging")
		top := f.commit("c.txt", "staging\n")
		m1 := f.squash("feat")
		wantSummary(t, f.reconcileWith(&ghFake{stack: stack[:2]}, false,
			stackPR(1, "feat", "main", model.PRStatusMerged, m1)), "")
		if got := git(t, f.wt, "rev-parse", "HEAD"); got != top {
			t.Errorf("HEAD moved to %s", got)
		}
	})
}

func TestUnmergedSquashMerged(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *syncFixture) []model.PR
		want  string
	}{
		{
			name: "squash-merged, remote branch deleted",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "pr\n")
				return []model.PR{stackPR(1, "feat", "main", model.PRStatusMerged, f.squash("feat"))}
			},
		},
		{
			name: "squash-merged stack, checked out at the top",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "bottom\n")
				f.branch("feat-staging")
				f.commit("c.txt", "staging\n")
				m1, m2 := f.squash("feat"), f.squash("feat-staging")
				f.mainMoves("c.txt", "changed again\n")
				return []model.PR{stackPR(1, "feat", "main", model.PRStatusMerged, m1), stackPR(2, "feat-staging", "feat", model.PRStatusMerged, m2)}
			},
		},
		{
			// Saved before PRs recorded merge_oid, and main changed the file
			// since: content differs, but it's the same patch (git cherry).
			name: "one-commit squash, no merge commit saved, main moved on",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "pr\n")
				f.squash("feat")
				f.mainMoves("b.txt", "changed again\n")
				return []model.PR{stackPR(1, "feat", "main", model.PRStatusMerged, "")}
			},
		},
		{
			name: "commit after the merge",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "pr\n")
				m := f.squash("feat")
				f.commit("c.txt", "new\n")
				return []model.PR{stackPR(1, "feat", "main", model.PRStatusMerged, m)}
			},
			want: "app: 1 commit(s) not pushed or merged",
		},
		{
			name: "unmerged, pushed",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "pr\n")
				git(f.t, f.wt, "push", "-q", "origin", "feat")
				return []model.PR{stackPR(1, "feat", "main", model.PRStatusOpen, "")}
			},
		},
		{
			name: "unmerged, not pushed",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "pr\n")
				f.commit("c.txt", "pr\n")
				return []model.PR{}
			},
			want: "app: 2 commit(s) not pushed or merged",
		},
		{
			name: "dirty",
			setup: func(f *syncFixture) []model.PR {
				f.commit("b.txt", "pr\n")
				m := f.squash("feat")
				write(f.t, filepath.Join(f.wt, "wip.txt"), "wip\n")
				return []model.PR{stackPR(1, "feat", "main", model.PRStatusMerged, m)}
			},
			want: "app: 1 uncommitted change(s)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSyncFixture(t)
			prs := tt.setup(f)
			git(t, f.wt, "fetch", "-q", "--prune", "origin")
			got, err := feature.Unmerged(context.Background(), &ghFake{}, f.l, "feat", prs)
			if err != nil {
				t.Fatal(err)
			}
			if s := strings.Join(got, "|"); s != tt.want {
				t.Errorf("Unmerged = %q, want %q", s, tt.want)
			}
		})
	}
}

func TestFinishedSquashMerged(t *testing.T) {
	f := newSyncFixture(t)
	f.commit("b.txt", "pr\n")
	m := f.squash("feat")
	git(t, f.wt, "fetch", "-q", "--prune", "origin")
	pr := stackPR(1, "feat", "main", model.PRStatusMerged, m)
	if _, err := feature.UpdateState(f.l, "feat", func(s *feature.State) { s.PRs = []model.PR{pr} }); err != nil {
		t.Fatal(err)
	}
	if !feature.Finished(context.Background(), &ghFake{}, f.l, "feat") {
		t.Error("squash-merged feature not finished")
	}
	f.commit("c.txt", "more\n")
	if feature.Finished(context.Background(), &ghFake{}, f.l, "feat") {
		t.Error("finished with a commit after the merge")
	}
}

func TestRemoveRestoreExpire(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	head := f.commit("b.txt", "work\n")
	write(t, filepath.Join(f.wt, "dirty.txt"), "uncommitted\n") // Remove doesn't refuse: callers check Unmerged
	base := filepath.Join(f.l.Base, "app")
	run := shell.Exec{}

	if err := feature.Remove(ctx, run, f.l, "feat"); err != nil {
		t.Fatal(err)
	}
	if feature.Exists(f.l, "feat") || len(feature.List(f.l)) != 0 {
		t.Fatal("feature still listed")
	}
	if _, ok := feature.InTrash(f.l, "feat"); !ok {
		t.Fatal("not in the trash")
	}
	if out := git(t, base, "branch", "--list", "feat"); out == "" {
		t.Error("branch deleted on remove: restore would lose it")
	}
	if out := git(t, base, "worktree", "list"); !strings.Contains(out, ".trash") {
		t.Errorf("worktree not re-linked into the trash:\n%s", out)
	}

	if err := feature.Restore(ctx, run, f.l, "feat"); err != nil {
		t.Fatal(err)
	}
	if got := git(t, f.wt, "rev-parse", "HEAD"); got != head {
		t.Errorf("restored HEAD = %s, want %s", got, head)
	}
	if got := git(t, f.wt, "status", "--porcelain"); got != "?? dirty.txt" {
		t.Errorf("restored status = %q, want the uncommitted file back", got)
	}
	if err := feature.Restore(ctx, run, f.l, "feat"); !errors.Is(err, feature.ErrExists) {
		t.Errorf("restore over a live feature: err = %v", err)
	}

	if err := feature.Remove(ctx, run, f.l, "feat"); err != nil {
		t.Fatal(err)
	}
	if err := feature.EmptyTrash(ctx, run, f.l, feature.TrashRetention); err != nil {
		t.Fatal(err)
	}
	if _, ok := feature.InTrash(f.l, "feat"); !ok {
		t.Fatal("fresh removal emptied before it expired")
	}
	if err := feature.EmptyTrash(ctx, run, f.l, 0); err != nil {
		t.Fatal(err)
	}
	if len(feature.Trash(f.l)) != 0 {
		t.Error("expired entry still in the trash")
	}
	if out := git(t, base, "worktree", "list"); strings.Contains(out, "feat") {
		t.Errorf("worktree still registered:\n%s", out)
	}
	if out := git(t, base, "branch", "--list", "feat"); out != "" {
		t.Errorf("branch still there after expiry: %q", out)
	}
	if err := feature.Restore(ctx, run, f.l, "feat"); !errors.Is(err, feature.ErrNotInTrash) {
		t.Errorf("restore after expiry: err = %v", err)
	}
}
