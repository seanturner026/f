package feature_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanturner026/f/internal/feature"
	gitpkg "github.com/seanturner026/f/internal/git"
	"github.com/seanturner026/f/internal/shell"
)

// gitRepo makes a real git repo with one commit at dir.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func lastRun(t *testing.T, wt string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", wt, "rev-parse", "-q", "--verify", feature.LastRunRef).Output()
	return string(out)
}

func TestCheckpoint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	ctx, r := context.Background(), shell.Exec{}
	l := feature.NewLayout(t.TempDir())
	wtA := filepath.Join(feature.Dir(l, "feat"), "a")
	wtB := filepath.Join(feature.Dir(l, "feat"), "b")
	for _, wt := range []string{wtA, wtB} {
		mustMkdir(t, wt)
		gitRepo(t, wt)
	}

	tests := []struct {
		name        string
		edit        func()
		wantChanged []string
		wantMoved   bool // did a's marker move?
	}{
		{name: "run with no code changes leaves no marker", edit: func() {}, wantChanged: nil},
		{
			name:        "new untracked file in a counts",
			edit:        func() { os.WriteFile(filepath.Join(wtA, "new.txt"), []byte("x"), 0o644) },
			wantChanged: []string{"a"},
			wantMoved:   true,
		},
		{name: "chat-only run keeps the marker", edit: func() {}, wantChanged: nil},
		{
			name:        "edit only in b leaves a's marker",
			edit:        func() { os.WriteFile(filepath.Join(wtB, "b.txt"), []byte("y"), 0o644) },
			wantChanged: []string{"b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := lastRun(t, wtA)
			if err := feature.CheckpointBegin(ctx, r, l, "feat"); err != nil {
				t.Fatal(err)
			}
			tt.edit()
			changed, err := feature.CheckpointEnd(ctx, r, l, "feat")
			if err != nil {
				t.Fatal(err)
			}
			if len(changed) != len(tt.wantChanged) || (len(changed) > 0 && changed[0] != tt.wantChanged[0]) {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if moved := lastRun(t, wtA) != before; moved != tt.wantMoved {
				t.Errorf("a marker moved = %v, want %v", moved, tt.wantMoved)
			}
		})
	}

	if lastRun(t, wtA) == "" {
		t.Fatal("no marker in a")
	}
}

func TestCheckpointDiffShowsOnlyLastRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t")
	}
	ctx, r := context.Background(), shell.Exec{}
	l := feature.NewLayout(t.TempDir())
	wt := filepath.Join(feature.Dir(l, "feat"), "a")
	mustMkdir(t, wt)
	gitRepo(t, wt)
	run := func(file string) {
		t.Helper()
		if err := feature.CheckpointBegin(ctx, r, l, "feat"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := feature.CheckpointEnd(ctx, r, l, "feat"); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name, create, want string
	}{
		{name: "first run", create: "x.txt", want: "x.txt"},
		{name: "second run shows only its own new file", create: "y.txt", want: "y.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run(tt.create)
			out, err := exec.Command("git", "-C", wt, "diff", "--name-only", feature.LastRunRef).Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(out)); got != tt.want {
				t.Errorf("diff since last run = %q, want %q", got, tt.want)
			}
		})
	}
}

// The snapshot starts from a copy of the real index (for speed): it must
// record the same tree as starting from HEAD would, and leave the index alone.
func TestSnapshotMatchesWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t")
	}
	wt := t.TempDir()
	gitRepo(t, wt)
	run := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = wt, append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	put := func(name, content string) {
		if err := os.WriteFile(filepath.Join(wt, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"edited", "deleted", "staged"} {
		put(f, "old\n")
	}
	put(".gitignore", "ignored\n")
	run(nil, "add", ".")
	run(nil, "commit", "-q", "-m", "files")
	put("edited", "new\n")
	os.Remove(filepath.Join(wt, "deleted"))
	put("untracked", "u\n")
	put("ignored", "i\n")
	put("staged", "staged\n")
	run(nil, "add", "staged")
	put("staged", "staged then edited\n")
	run(nil, "add", "-N", "untracked") // intent-to-add, as CheckpointEnd leaves it
	indexBefore := run(nil, "diff", "--cached", "--stat")

	snap, err := gitSnapshot(t, wt)
	if err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(t.TempDir(), "index")
	env := []string{"GIT_INDEX_FILE=" + idx}
	run(env, "read-tree", "HEAD")
	run(env, "add", "-A")
	if got, want := run(nil, "rev-parse", snap+"^{tree}"), run(env, "write-tree"); got != want {
		t.Errorf("snapshot tree %s, want %s (from HEAD)", got, want)
	}
	if got := run(nil, "diff", "--cached", "--stat"); got != indexBefore {
		t.Errorf("real index changed:\n%s\nwant\n%s", got, indexBefore)
	}
}

func gitSnapshot(t *testing.T, wt string) (string, error) {
	t.Helper()
	return gitpkg.SnapshotCommit(context.Background(), shell.Exec{}, wt)
}
