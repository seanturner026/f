package feature_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/shell"
)

func TestUpdateSourcesFastForwardsCleanClone(t *testing.T) {
	f := newSyncFixture(t)
	base := filepath.Join(f.l.Base, "app")
	// The clone's post-merge hook fires on the fast-forward, as on a pull (the f repo deploys this way).
	hook := filepath.Join(base, ".git", "hooks", "post-merge")
	write(t, hook, "#!/bin/sh\ntouch \"$(git rev-parse --show-toplevel)/../hook-ran\"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	head := f.commit("b.txt", "two\n")
	f.merge("squash")

	lines := feature.UpdateSources(context.Background(), shell.Exec{}, f.l, "feat", []model.PR{merged(head)})
	if len(lines) != 1 {
		t.Fatalf("lines = %q, want one", lines)
	}
	if got, want := git(t, base, "rev-parse", "HEAD"), git(t, f.github, "rev-parse", "HEAD"); got != want {
		t.Errorf("source clone at %s, want origin's main %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(f.l.Base, "hook-ran")); err != nil {
		t.Errorf("post-merge hook didn't run: %v", err)
	}
}

func TestUpdateSourcesLeavesDirtyClone(t *testing.T) {
	f := newSyncFixture(t)
	base := filepath.Join(f.l.Base, "app")
	before := git(t, base, "rev-parse", "HEAD")
	head := f.commit("b.txt", "two\n")
	f.merge("squash")
	write(t, filepath.Join(base, "scratch.txt"), "mine\n") // someone's working here

	if lines := feature.UpdateSources(context.Background(), shell.Exec{}, f.l, "feat", []model.PR{merged(head)}); len(lines) != 0 {
		t.Errorf("lines = %q, want none", lines)
	}
	if got := git(t, base, "rev-parse", "HEAD"); got != before {
		t.Errorf("dirty source clone moved to %s", got)
	}
}
