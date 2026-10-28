// Package feature manages feature workspaces: one directory per feature holding
// a git worktree per repo (branch = feature name) plus a generated AGENTS.md.
//
//	<Sources>                          base clones (anywhere: Config.Repos)
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

	"github.com/seanturner026/f/internal/git"
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
	// ErrNoBaseClone means no repo source has a clone of that name.
	ErrNoBaseClone = errors.New("no base clone")
)

// Layout locates base clones and feature directories.
type Layout struct {
	Base     string   // f's home: .features (and its trash), the beads db
	Features string   // feature directories
	Sources  []string // base clones: a repo's path, or dir/* for every repo in dir
	// AgentRules are machine-specific rules for every feature's AGENTS.md (Config).
	AgentRules []string
}

// NewLayout returns the layout rooted at base, offering the repos in it.
func NewLayout(base string) Layout {
	return Layout{Base: base, Features: filepath.Join(base, ".features"), Sources: []string{filepath.Join(base, "*")}}
}

// RepoPath is repo's base clone, or "" if no source has it.
func (l Layout) RepoPath(repo string) string {
	clones, _ := l.baseClones()
	return clones[repo]
}

// baseClones maps each base clone's name (its directory name) to its path.
// Two sources with the same name is an error; the first is kept.
func (l Layout) baseClones() (map[string]string, error) {
	clones := map[string]string{}
	var errs []error
	add := func(path string) {
		name := filepath.Base(path)
		if prev, ok := clones[name]; ok && prev != path {
			errs = append(errs, fmt.Errorf("two repos named %q: %s and %s", name, prev, path))
			return
		}
		clones[name] = path
	}
	for _, src := range l.Sources {
		if dir, ok := strings.CutSuffix(src, string(filepath.Separator)+"*"); ok {
			names, _ := gitDirs(dir) // a missing source dir offers nothing
			for _, n := range names {
				add(filepath.Join(dir, n))
			}
		} else if git.IsRepo(src) {
			add(src)
		}
	}
	return clones, errors.Join(errs...)
}

// Dir is the directory of feature name.
func Dir(l Layout, name string) string { return filepath.Join(l.Features, name) }

// RepoDir is repo's worktree in feature name.
func RepoDir(l Layout, name, repo string) string { return filepath.Join(Dir(l, name), repo) }

// baseRef is where repo's feature branches start: origin's default branch, as
// its base clone knows it.
func baseRef(ctx context.Context, r Runner, l Layout, repo string) string {
	return "origin/" + git.DefaultBranch(ctx, r, l.RepoPath(repo))
}

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

// BaseRepos lists the base clones' names, sorted.
func BaseRepos(l Layout) ([]string, error) {
	clones, err := l.baseClones()
	if err != nil {
		return nil, fmt.Errorf("base repos: %w", err)
	}
	repos := make([]string, 0, len(clones))
	for name := range clones {
		repos = append(repos, name)
	}
	sort.Strings(repos)
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
		if l.RepoPath(r) == "" {
			return fmt.Errorf("%q (repos come from %s): %w", r, strings.Join(l.Sources, ", "), ErrNoBaseClone)
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
