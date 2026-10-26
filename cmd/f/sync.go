package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/herdr"
	"github.com/seanturner026/f/internal/tracing"
)

// syncFeature refreshes PRs and reconciles repos whose PR merged (reset or
// rebase onto the default branch; see feature.Reconcile). With background, it
// does nothing while the feature's pi is running (it would pull files from
// under it) and prints nothing.
func syncFeature(ctx context.Context, d deps, explicit string, background bool) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	if background && piWorking(ctx, d, name) {
		return nil
	}
	lines, err := reconcileLines(ctx, d, name, !background) // only an explicit sync pushes stacks
	if err != nil || background {
		return err
	}
	if len(lines) == 0 {
		info("nothing to sync")
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

// reconcileLines refreshes PRs, runs Reconcile, and describes the results for display.
func reconcileLines(ctx context.Context, d deps, name string, stackSync bool) ([]string, error) {
	ctx = feature.WithRunOnce(ctx)
	state, _, err := feature.RefreshPRs(ctx, d.run, d.layout, name)
	if err != nil {
		return nil, err
	}
	results, err := feature.Reconcile(ctx, d.run, d.layout, name, state.PRs, stackSync)
	if err != nil {
		return nil, err
	}
	closed, _ := closeMergedSteps(ctx, d, name, state.PRs)
	lines := append(syncLines(results, true), closed...)
	return append(lines, feature.UpdateSources(ctx, d.run, d.layout, name, state.PRs)...), nil
}

// syncLines describes Reconcile's results, one line per branch marked ✓, and
// one per repo marked ✗ for what needs you. Colored marks are for display;
// notes stored for pi stay plain.
func syncLines(results []feature.SyncResult, colored bool) []string {
	done, needed := "✓", "✗"
	if colored {
		done, needed = green(done), red(needed)
	}
	var lines []string
	for _, r := range results {
		for _, b := range r.Branches {
			lines = append(lines, fmt.Sprintf("%s %s: %s", done, r.Repo, branchLine(b, r.Base)))
		}
		if r.Detail != "" {
			lines = append(lines, fmt.Sprintf("%s %s: %s", needed, r.Repo, r.Detail))
		}
	}
	return lines
}

// branchLine is e.g. "app-ui (#12 merged): kept (2 new commits), replayed onto origin/main".
func branchLine(b feature.BranchSync, base string) string {
	s := b.Branch
	if b.PR != 0 {
		s += fmt.Sprintf(" (#%d merged)", b.PR)
	}
	switch b.Action {
	case feature.BranchReset:
		s += ": reset to " + base
	case feature.BranchDeleted:
		s += ": deleted"
	case feature.BranchKept:
		s += fmt.Sprintf(": kept (%d new commit%s), replayed onto %s", b.New, plural(b.New), base)
	case feature.BranchRebased:
		s += ": rebased"
		if b.Pushed {
			s += " and pushed"
		}
		if b.Retargeted != 0 {
			s += fmt.Sprintf(", #%d now based on %s", b.Retargeted, strings.TrimPrefix(base, "origin/"))
		}
	}
	if b.Action == feature.BranchRebased && !b.Pushed {
		s += " (next push: --force-with-lease)"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func piWorking(ctx context.Context, d deps, name string) bool {
	if d.noHerdr {
		return false
	}
	agents, _ := herdr.AgentStatuses(ctx, d.run)
	return agents[feature.Dir(d.layout, name)] == herdr.AgentWorking
}

// syncInBackground starts `f sync --background` for name.
func syncInBackground(ctx context.Context, name string) {
	startDetached(ctx, "sync", "-f", name, "--background")
}

// startDetached runs f with args in its own session, so it outlives the
// picker's popup.
func startDetached(ctx context.Context, args ...string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, args...)
	cmd.Env = tracing.ChildEnv(ctx) // its spans join this trace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Start()
}
