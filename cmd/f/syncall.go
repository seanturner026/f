package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/f/internal/beads"
	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/herdr"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/tracing"
)

// syncAll is the periodic sync (launchd, every 30 min): for every feature,
// syncAllParallel at a time, refresh PRs/CI and beads progress, and reconcile
// repos whose PR merged (never pushing) unless you're in it or its pi is
// running. pi gets a note about anything it changed. Logs one line per change
// to stdout (launchd log).
func syncAll(ctx context.Context, d deps) error {
	if idleTime(ctx, d.run) > syncAllInterval {
		return nil // away or asleep (launchd fires in dark wake): the first run after you're back catches up
	}
	focused := ""
	if !d.noHerdr {
		focused, _ = herdr.FocusedWorkspace(ctx, d.run) // "" if Herdr isn't running
	}
	stamp := time.Now().Format(time.DateTime)
	_ = feature.EmptyTrash(ctx, d.run, d.layout, feature.TrashRetention) // removed features past restoring
	_ = tracing.Trim(tracing.Path(), time.Now().Add(-tracing.Retention))
	d.run = &oneBD{Runner: d.run}
	ctx = beads.WithSnapshot(feature.WithRunOnce(ctx))
	var wg sync.WaitGroup
	slots := make(chan struct{}, syncAllParallel)
	for _, name := range feature.List(d.layout) {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			syncAllFeature(ctx, d, name, focused, stamp)
		})
	}
	wg.Wait()
	return nil
}

// syncAllParallel is how many features f sync --all syncs at once.
const syncAllParallel = 4

// oneBD runs bd commands one at a time: they share one embedded Dolt database.
type oneBD struct {
	feature.Runner
	mu sync.Mutex
}

func (o *oneBD) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "bd" {
		o.mu.Lock()
		defer o.mu.Unlock()
	}
	return o.Runner.Run(ctx, dir, name, args...)
}

// syncAllInterval is launchd's StartInterval for f sync --all (justfile).
const syncAllInterval = 30 * time.Minute

var hidIdle = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)

// idleTime is how long since the last keyboard or mouse input (0 if unknown).
func idleTime(ctx context.Context, r feature.Runner) time.Duration {
	out, err := r.Run(ctx, "", "ioreg", "-c", "IOHIDSystem", "-d", "4")
	if err != nil {
		return 0
	}
	return parseIdle(out)
}

func parseIdle(ioreg string) time.Duration {
	m := hidIdle.FindStringSubmatch(ioreg)
	if m == nil {
		return 0
	}
	ns, _ := strconv.ParseInt(m[1], 10, 64)
	return time.Duration(ns)
}

// syncAllFeature is syncAll's work for one feature, as its own span.
func syncAllFeature(ctx context.Context, d deps, name, focused, stamp string) {
	ctx, span := tracing.Start(ctx, "sync feature", attribute.String("feature", name))
	defer span.End()
	if err := planSyncFeature(ctx, d, name); err != nil {
		fmt.Printf("%s %s: plan: %v\n", stamp, name, err)
	}
	before, _ := feature.LoadState(d.layout, name)
	after, _, err := feature.RefreshPRs(ctx, d.run, d.layout, name)
	if err != nil {
		fmt.Printf("%s %s: %v\n", stamp, name, err)
		return
	}
	// Source clones move even while you're in the feature: they aren't its worktrees.
	for _, l := range feature.UpdateSources(ctx, d.run, d.layout, name, after.PRs) {
		fmt.Printf("%s %s: %s\n", stamp, name, l)
	}
	var lines []string
	switch {
	case name == focused:
		fmt.Printf("%s %s: refreshed; no resets (you're in it)\n", stamp, name)
	case piWorking(ctx, d, name):
		fmt.Printf("%s %s: refreshed; no resets (the agent is running)\n", stamp, name)
	default:
		results, err := feature.Reconcile(ctx, d.run, d.layout, name, after.PRs, false)
		if err != nil {
			fmt.Printf("%s %s: %v\n", stamp, name, err)
			return
		}
		lines = syncLines(results, false)
	}
	closed, linked := closeMergedSteps(ctx, d, name, after.PRs)
	notes := closed
	for _, l := range closed {
		fmt.Printf("%s %s: %s\n", stamp, name, l)
	}
	for _, p := range newlyMerged(before.PRs, after.PRs) {
		if linked[strings.TrimSuffix(p.URL, "/")] {
			continue // its step closes itself (above)
		}
		n := fmt.Sprintf("%s #%d merged (%s): close its beads step (bd close <id> --reason %s)", p.Repo, p.Number, p.URL, p.URL)
		fmt.Printf("%s %s: %s\n", stamp, name, n)
		notes = append(notes, n)
	}
	for _, l := range lines {
		fmt.Printf("%s %s: %s\n", stamp, name, l)
		notes = append(notes, l)
	}
	if len(notes) > 0 {
		_ = feature.AddSyncNotes(d.layout, name, notes...)
	}
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
