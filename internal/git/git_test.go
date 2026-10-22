package git_test

import (
	"context"
	"errors"
	"testing"

	"github.com/seanturner026/pi/internal/git"
)

type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(context.Context, string, string, ...string) (string, error) {
	return f.out, f.err
}

var ctx = context.Background()

func TestDefaultBranch(t *testing.T) {
	tests := []struct {
		name   string
		runner fakeRunner
		want   string
	}{
		{name: "from origin HEAD", runner: fakeRunner{out: "origin/develop"}, want: "develop"},
		{name: "falls back to main", runner: fakeRunner{err: errors.New("no ref")}, want: "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := git.DefaultBranch(ctx, tt.runner, "/repo"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChangedFiles(t *testing.T) {
	tests := []struct {
		name    string
		runner  fakeRunner
		want    int
		wantErr bool
	}{
		{name: "clean", runner: fakeRunner{out: ""}, want: 0},
		{name: "two changes", runner: fakeRunner{out: " M a.go\n?? b.go"}, want: 2},
		{name: "git fails", runner: fakeRunner{err: errors.New("boom")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := git.ChangedFiles(ctx, tt.runner, "/wt")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCommitsAhead(t *testing.T) {
	tests := []struct {
		name    string
		runner  fakeRunner
		want    int
		wantErr bool
	}{
		{name: "count", runner: fakeRunner{out: "3"}, want: 3},
		{name: "not a number", runner: fakeRunner{out: "x"}, wantErr: true},
		{name: "git fails", runner: fakeRunner{err: errors.New("boom")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := git.CommitsAhead(ctx, tt.runner, "/wt", "origin/main")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestForcePushBranch(t *testing.T) {
	r := fakeRunner{out: "origin/main"} // default branch main; every command succeeds
	tests := []struct {
		name, branch string
		wantErr      bool
	}{
		{name: "feature branch", branch: "feat"},
		{name: "stack branch", branch: "feat-staging"},
		{name: "default branch", branch: "main", wantErr: true},
		{name: "another feature's branch", branch: "featx", wantErr: true},
		{name: "empty", branch: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := git.ForcePushBranch(ctx, r, "/wt", tt.branch, "feat")
			if gotErr := errors.Is(err, git.ErrProtectedBranch); gotErr != tt.wantErr {
				t.Errorf("err = %v, want protected: %v", err, tt.wantErr)
			}
		})
	}
	if err := git.ForcePushBranch(ctx, r, "/wt", "main", "main"); !errors.Is(err, git.ErrProtectedBranch) {
		t.Errorf("feature named like the default branch: err = %v, want refused", err)
	}
}
