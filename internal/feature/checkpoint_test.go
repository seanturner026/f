package feature_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/shell"
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
