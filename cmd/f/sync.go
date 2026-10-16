package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/herdr"
	"github.com/seanturner026/pi/internal/model"
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

// reconcileLines runs Reconcile and describes the results, one line per repo.
func reconcileLines(ctx context.Context, d deps, name string, stackSync bool) ([]string, error) {
	state, _, err := feature.RefreshPRs(ctx, d.run, d.layout, name)
	if err != nil {
		return nil, err
	}
	return reconcileWith(ctx, d, name, state.PRs, stackSync)
}

// reconcileWith reconciles against already refreshed PRs and describes the results.
func reconcileWith(ctx context.Context, d deps, name string, prs []model.PR, stackSync bool) ([]string, error) {
	results, err := feature.Reconcile(ctx, d.run, d.layout, name, prs, stackSync)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, r := range results {
		pr := fmt.Sprintf("#%d merged", r.PR.Number)
		switch r.Action {
		case feature.SyncNeeded:
			lines = append(lines, fmt.Sprintf("%s %s: %s: %s", red("✗"), r.Repo, pr, r.Detail))
		case feature.SyncStack:
			lines = append(lines, fmt.Sprintf("%s %s: %s", green("✓"), r.Repo, r.Detail))
		default:
			lines = append(lines, fmt.Sprintf("%s %s: %s, %s (next push: --force-with-lease)", green("✓"), r.Repo, pr, r.Detail))
		}
	}
	return lines, nil
}

func piWorking(ctx context.Context, d deps, name string) bool {
	if d.noHerdr {
		return false
	}
	agents, _ := herdr.AgentStatuses(ctx, d.run)
	return agents[feature.Dir(d.layout, name)] == herdr.AgentWorking
}

// syncInBackground starts `f sync --background` for name.
func syncInBackground(name string) { startDetached("sync", "-f", name, "--background") }

// startDetached runs f with args in its own session, so it outlives the
// picker's popup.
func startDetached(args ...string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = cmd.Start()
}
