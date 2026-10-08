package main

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/herdr"
	"github.com/seanturner026/pi/internal/model"
)

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// syncAll is the periodic sync (launchd, every 30 min): for every feature,
// refresh PRs/CI and beads progress (the picker stays accurate) and reconcile repos whose PR
// merged, without pushing. It skips the workspace you're in and features
// whose pi is running, and leaves pi a note about anything it changed. Logs
// one line per change to stdout (the launchd log).
func syncAll(ctx context.Context, d deps) error {
	focused := ""
	if !d.noHerdr {
		focused, _ = herdr.FocusedWorkspace(ctx, d.run) // "" if Herdr isn't running
	}
	stamp := time.Now().Format(time.DateTime)
	for _, name := range feature.List(d.layout) {
		switch {
		case name == focused:
			fmt.Printf("%s %s: skipped (you're in it)\n", stamp, name)
			continue
		case piWorking(ctx, d, name):
			fmt.Printf("%s %s: skipped (pi is running)\n", stamp, name)
			continue
		}
		if err := planSyncFeature(ctx, d, name); err != nil {
			fmt.Printf("%s %s: plan: %v\n", stamp, name, err)
		}
		before, _ := feature.LoadState(d.layout, name)
		lines, err := reconcileLines(ctx, d, name, false)
		if err != nil {
			fmt.Printf("%s %s: %v\n", stamp, name, err)
			continue
		}
		after, _ := feature.LoadState(d.layout, name)
		var notes []string
		for _, p := range newlyMerged(before.PRs, after.PRs) {
			n := fmt.Sprintf("%s #%d merged (%s): close its beads step (bd close <id> --reason %s)", p.Repo, p.Number, p.URL, p.URL)
			fmt.Printf("%s %s: %s\n", stamp, name, n)
			notes = append(notes, n)
		}
		for _, l := range lines {
			l = ansiCodes.ReplaceAllString(l, "")
			fmt.Printf("%s %s: %s\n", stamp, name, l)
			notes = append(notes, l)
		}
		if len(notes) > 0 {
			_ = feature.AddSyncNotes(d.layout, name, notes...)
		}
	}
	return nil
}

// newlyMerged lists PRs merged in after that weren't merged in before.
func newlyMerged(before, after []model.PR) []model.PR {
	was := map[string]bool{}
	for _, p := range before {
		was[p.URL] = p.Status == model.PRStatusMerged
	}
	var out []model.PR
	for _, p := range after {
		if merged, known := was[p.URL]; p.Status == model.PRStatusMerged && known && !merged {
			out = append(out, p)
		}
	}
	return out
}

// takeSyncNotes prints and clears the feature's queued notes (for the f
// extension, which shows them to pi with the next prompt).
func takeSyncNotes(d deps, explicit string) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	notes, err := feature.TakeSyncNotes(d.layout, name)
	for _, n := range notes {
		fmt.Println(n)
	}
	return err
}
