package feature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/seanturner026/f/internal/model"
)

// ContextDir holds per-feature state managed by f. It lives in the feature
// root (not a git repo), so it's local-only and goes away with the feature.
const ContextDir = ".context"

// ErrBadPRURL means a string isn't a GitHub pull request URL.
var ErrBadPRURL = errors.New("not a GitHub pull request URL")

// State is a feature's local state, stored in <feature>/.context/state.json.
type State struct {
	PRs            []model.PR `json:"prs"`
	PRsRefreshedAt time.Time  `json:"prs_refreshed_at,omitzero"`
	// MergedEarlier are merged PRs a refresh no longer finds (f sync dropped
	// their branches). Display only: sync must not act on them again.
	MergedEarlier []model.PR `json:"merged_earlier,omitempty"`
	// Flagged is the picker's "unread" mark (ctrl-u); opening the feature, or its agent doing anything, clears it.
	Flagged bool       `json:"flagged,omitempty"`
	Plan    model.Plan `json:"plan,omitzero"` // beads progress, synced after each turn
	// SyncNotes tell the agent what a background f sync changed (e.g. a repo reset to
	// main after its PR merged); the f mod hands them over on the next prompt.
	SyncNotes []string `json:"sync_notes,omitempty"`
	Setup     *Setup   `json:"setup,omitempty"` // repos still being added in the background
}

// AllPRs is PRs plus MergedEarlier: every PR the feature has had, for display.
func (s State) AllPRs() []model.PR {
	all := slices.Clone(s.PRs)
	for _, p := range s.MergedEarlier {
		if !slices.ContainsFunc(all, p.Same) { // a lookup can find it again
			all = append(all, p)
		}
	}
	return all
}

// SetFlagged sets (or, with toggle, flips) the feature's picker flag and
// returns its new value.
func SetFlagged(l Layout, name string, flagged, toggle bool) (bool, error) {
	s, err := UpdateState(l, name, func(s *State) {
		if toggle {
			flagged = !s.Flagged
		}
		s.Flagged = flagged
	})
	return s.Flagged, err
}

// AddSyncNotes queues notes for the agent.
func AddSyncNotes(l Layout, name string, notes ...string) error {
	_, err := UpdateState(l, name, func(s *State) { s.SyncNotes = append(s.SyncNotes, notes...) })
	return err
}

// TakeSyncNotes returns and clears the queued notes.
func TakeSyncNotes(l Layout, name string) ([]string, error) {
	var notes []string
	_, err := UpdateState(l, name, func(s *State) { notes, s.SyncNotes = s.SyncNotes, nil })
	return notes, err
}

// SavePlan records the feature's beads progress.
func SavePlan(l Layout, name string, p model.Plan) error {
	_, err := UpdateState(l, name, func(s *State) { s.Plan = p })
	return err
}

// StatePath is the feature's state file.
func StatePath(l Layout, name string) string {
	return filepath.Join(Dir(l, name), ContextDir, "state.json")
}

// LoadState reads the feature's state; a missing file is an empty State.
func LoadState(l Layout, name string) (State, error) {
	raw, err := os.ReadFile(StatePath(l, name))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("load state: %w", err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("load state: %w", err)
	}
	return s, nil
}

// UpdateState changes the feature's state under a lock: it re-reads the file,
// applies fn, and writes it back. Several writers run at once (the f mod's post-turn
// PR refresh and plan sync, the prs tab, launchd's f sync --all); without the
// lock each would write back its own stale copy and drop the others' fields.
func UpdateState(l Layout, name string, fn func(*State)) (State, error) {
	unlock, err := lockState(l, name)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	s, err := LoadState(l, name)
	if err != nil {
		return State{}, err
	}
	fn(&s)
	return s, SaveState(l, name, s)
}

// lockState takes an exclusive lock on the feature's state (a lock file next
// to state.json; flock is released if the process dies).
func lockState(l Layout, name string) (func(), error) {
	path := StatePath(l, name) + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("lock state: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock state: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock state: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// SaveState writes the feature's state atomically. Use UpdateState to change
// it: SaveState alone would overwrite concurrent changes.
func SaveState(l Layout, name string, s State) error {
	if err := writeJSON(StatePath(l, name), s); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

// writeJSON writes v as indented JSON to path atomically (temp file + rename).
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RefreshPRs looks up PRs live and saves them. For repos whose lookup failed,
// previously known PRs are kept.
func RefreshPRs(ctx context.Context, r Runner, l Layout, name string) (State, map[string]error, error) {
	live, errs, err := PRs(ctx, r, l, name) // network: outside the lock
	if err != nil {
		return State{}, nil, err
	}
	s, err := UpdateState(l, name, func(s *State) {
		for _, p := range s.PRs {
			// A failed repo can still return some PRs (e.g. the branch list, not the stack).
			if errs[p.Repo] != nil && !slices.ContainsFunc(live, p.Same) {
				live = append(live, p)
			}
		}
		for _, p := range s.PRs {
			if p.Status == model.PRStatusMerged && !slices.ContainsFunc(live, p.Same) && !slices.ContainsFunc(s.MergedEarlier, p.Same) {
				s.MergedEarlier = append(s.MergedEarlier, p)
			}
		}
		s.PRs, s.PRsRefreshedAt = live, time.Now()
	})
	if err != nil {
		return State{}, nil, err
	}
	return s, errs, nil
}

// RecordPR adds (or replaces) a PR in the feature's state without an API call.
func RecordPR(l Layout, name string, pr model.PR) error {
	_, err := UpdateState(l, name, func(s *State) {
		kept := s.PRs[:0]
		for _, p := range s.PRs {
			if !p.Same(pr) {
				kept = append(kept, p)
			}
		}
		s.PRs = append(kept, pr)
	})
	return err
}

// ParsePRURL turns https://github.com/<owner>/<repo>/pull/<n> into an open PR
// for <repo>. Title and head are unknown until the next refresh.
func ParsePRURL(raw string) (model.PR, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != "github.com" {
		return model.PR{}, fmt.Errorf("%q: %w", raw, ErrBadPRURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return model.PR{}, fmt.Errorf("%q: %w", raw, ErrBadPRURL)
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return model.PR{}, fmt.Errorf("%q: %w", raw, ErrBadPRURL)
	}
	return model.PR{
		Repo:   parts[1],
		Number: n,
		URL:    "https://github.com/" + strings.Join(parts, "/"),
		Status: model.PRStatusOpen,
	}, nil
}
