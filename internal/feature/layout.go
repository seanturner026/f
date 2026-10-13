// Package feature manages feature workspaces: one directory per feature holding
// a git worktree per repo (branch = feature name) plus a generated AGENTS.md.
//
//	<Base>/<repo>                      base clones
//	<Base>/.features/<feature>/<repo>  worktrees
package feature

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/seanturner026/pi/internal/git"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

var (
	// ErrNotInFeature means the working directory isn't inside a feature.
	ErrNotInFeature = errors.New("not inside a feature (cd into one or pass -f <feature>)")
	// ErrNotFound means the named feature doesn't exist.
	ErrNotFound = errors.New("no such feature")
	// ErrInvalidName means a feature name has characters git or Herdr won't accept.
	ErrInvalidName = errors.New("invalid feature name (use lowercase, digits, - . _)")
	// ErrNoBaseClone means a repo isn't cloned under Base.
	ErrNoBaseClone = errors.New("no base clone")
)

// Layout locates base clones and feature directories.
type Layout struct {
	Base     string // base clones
	Features string // feature directories
}

// NewLayout returns the layout rooted at base.
func NewLayout(base string) Layout {
	return Layout{Base: base, Features: filepath.Join(base, ".features")}
}

// DefaultLayout uses $F_BASE, or ~/code/features.
func DefaultLayout() (Layout, error) {
	if base := os.Getenv("F_BASE"); base != "" {
		return NewLayout(base), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("default layout: %w", err)
	}
	return NewLayout(filepath.Join(home, "code", "features")), nil
}

// Dir is the directory of feature name.
func Dir(l Layout, name string) string { return filepath.Join(l.Features, name) }

// Exists reports whether feature name exists.
func Exists(l Layout, name string) bool {
	_, err := os.Stat(Dir(l, name))
	return err == nil
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// CheckName validates a feature name.
func CheckName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrInvalidName)
	}
	return nil
}

// List returns existing feature names, sorted.
func List(l Layout) []string {
	entries, _ := os.ReadDir(l.Features)
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") { // .trash: removed features
			names = append(names, e.Name())
		}
	}
	return names
}

// gitDirs lists subdirectories of dir that are git checkouts, sorted.
func gitDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, e := range entries {
		if e.IsDir() && git.IsRepo(filepath.Join(dir, e.Name())) {
			repos = append(repos, e.Name())
		}
	}
	sort.Strings(repos)
	return repos, nil
}

// BaseRepos lists the base clones.
func BaseRepos(l Layout) ([]string, error) {
	repos, err := gitDirs(l.Base)
	if err != nil {
		return nil, fmt.Errorf("base repos: %w", err)
	}
	return repos, nil
}

// Repos lists the repo worktrees in feature name.
func Repos(l Layout, name string) ([]string, error) {
	repos, err := gitDirs(Dir(l, name))
	if err != nil {
		return nil, fmt.Errorf("feature repos: %w", err)
	}
	return repos, nil
}

// CheckRepos verifies every repo has a base clone.
func CheckRepos(l Layout, repos []string) error {
	for _, r := range repos {
		if !git.IsRepo(filepath.Join(l.Base, r)) {
			return fmt.Errorf("%q in %s: %w", r, l.Base, ErrNoBaseClone)
		}
	}
	return nil
}

// Resolve returns explicit if set, otherwise the feature containing cwd.
func Resolve(l Layout, explicit, cwd string) (string, error) {
	name := explicit
	if name == "" {
		rel, err := filepath.Rel(l.Features, cwd)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return "", ErrNotInFeature
		}
		name = strings.Split(rel, string(filepath.Separator))[0]
	}
	if !Exists(l, name) {
		return "", fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	return name, nil
}
