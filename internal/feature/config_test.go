package feature

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeRepo makes a directory that looks like a checkout (git.IsRepo: has .git).
func fakeRepo(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigRepoSources(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("F_BASE", "")
	fakeRepo(t, filepath.Join(userHome, "work", "argocd"))
	fakeRepo(t, filepath.Join(userHome, "work", "gateway"))
	dotfiles := fakeRepo(t, filepath.Join(userHome, "personal", "dotfiles"))
	cfg := `{"home": "~/features", "repos": ["~/work/*", "~/personal/dotfiles", "~/missing/*"]}`
	if err := os.MkdirAll(filepath.Join(userHome, ".config", "f"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".config", "f", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(userHome, "features"); l.Base != want {
		t.Errorf("home = %s, want %s", l.Base, want)
	}
	repos, err := BaseRepos(l)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"argocd", "dotfiles", "gateway"}; !slices.Equal(repos, want) {
		t.Errorf("repos = %v, want %v", repos, want)
	}
	if got := l.RepoPath("dotfiles"); got != dotfiles {
		t.Errorf("RepoPath(dotfiles) = %s, want %s", got, dotfiles)
	}
	if got := l.RepoPath("nope"); got != "" {
		t.Errorf("RepoPath(nope) = %q, want empty", got)
	}

	// Two repos with the same name: an error, not a silent pick.
	fakeRepo(t, filepath.Join(userHome, "personal", "gateway"))
	l.Sources = append(l.Sources, filepath.Join(userHome, "personal", "gateway"))
	if _, err := BaseRepos(l); err == nil {
		t.Error("duplicate repo names: want an error")
	}
}

func TestDefaultLayoutWithoutConfig(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("F_BASE", "")
	l, err := DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(userHome, "code", "features"); l.Base != want {
		t.Errorf("home = %s, want %s", l.Base, want)
	}
	if want := []string{filepath.Join(l.Base, "*")}; !slices.Equal(l.Sources, want) {
		t.Errorf("sources = %v, want %v", l.Sources, want)
	}
}
