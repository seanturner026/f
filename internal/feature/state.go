package feature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/seanturner026/pi/internal/model"
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
	Plan           model.Plan `json:"plan,omitzero"` // beads progress, synced after each pi run
	// SyncNotes tell pi what a background f sync changed (e.g. a repo reset to
	// main after its PR merged); the f extension hands them over on the next prompt.
	SyncNotes []string `json:"sync_notes,omitempty"`
}

// AddSyncNotes queues notes for pi.
func AddSyncNotes(l Layout, name string, notes ...string) error {
	s, err := LoadState(l, name)
	if err != nil {
		return err
	}
	s.SyncNotes = append(s.SyncNotes, notes...)
	return SaveState(l, name, s)
}

// TakeSyncNotes returns and clears the queued notes.
func TakeSyncNotes(l Layout, name string) ([]string, error) {
	s, err := LoadState(l, name)
	if err != nil || len(s.SyncNotes) == 0 {
		return nil, err
	}
	notes := s.SyncNotes
	s.SyncNotes = nil
	return notes, SaveState(l, name, s)
}

// SavePlan records the feature's beads progress.
func SavePlan(l Layout, name string, p model.Plan) error {
	s, err := LoadState(l, name)
	if err != nil {
		return err
	}
	s.Plan = p
	return SaveState(l, name, s)
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

// SaveState writes the feature's state atomically.
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
	s, err := LoadState(l, name)
	if err != nil {
		return State{}, nil, err
	}
	live, errs, err := PRs(ctx, r, l, name)
	if err != nil {
		return State{}, nil, err
	}
	for _, p := range s.PRs {
		if errs[p.Repo] != nil {
			live = append(live, p)
		}
	}
	s.PRs, s.PRsRefreshedAt = live, time.Now()
	if err := SaveState(l, name, s); err != nil {
		return State{}, nil, err
	}
	return s, errs, nil
}

// RecordPR adds (or replaces) a PR in the feature's state without an API call.
func RecordPR(l Layout, name string, pr model.PR) error {
	s, err := LoadState(l, name)
	if err != nil {
		return err
	}
	kept := s.PRs[:0]
	for _, p := range s.PRs {
		if p.Repo != pr.Repo || p.Number != pr.Number {
			kept = append(kept, p)
		}
	}
	s.PRs = append(kept, pr)
	return SaveState(l, name, s)
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
