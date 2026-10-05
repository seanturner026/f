package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/model"
)

// The PRs tab (`f prs --watch`) shows each repo's PRs and their checks, and
// keeps .context/state.json fresh for the picker. It only asks GitHub while
// there's something to wait for:
//
//   - checks running: every pollEvery
//   - just pushed or a new PR: every pollEvery for settleFor (checks take a
//     moment to show up on a new commit)
//   - otherwise: no GitHub calls; every tick it only looks for a push (the
//     remote-tracking refs in each worktree) or a state.json change (pi
//     recorded or refreshed PRs)
//
// r refreshes now (re-runs, merges); q quits.
const (
	watchTick = 5 * time.Second
	pollEvery = 60 * time.Second
	settleFor = 5 * time.Minute
)

// watchMode is what the watcher is waiting for.
type watchMode string

const (
	modeIdle     watchMode = "waiting for a push"
	modeRunning  watchMode = "checks running: refreshing every minute"
	modeSettling watchMode = "just pushed: refreshing every minute"
)

func currentMode(prs []model.PR, now, settleUntil time.Time) watchMode {
	if checksRunning(prs) {
		return modeRunning
	}
	if now.Before(settleUntil) {
		return modeSettling
	}
	return modeIdle
}

// checksRunning reports whether an active PR has a check still running (even
// if another already failed: its result is still to come).
func checksRunning(prs []model.PR) bool {
	for _, p := range prs {
		if p.Status.IsActive() && slices.ContainsFunc(p.Checks, func(c model.Check) bool { return c.State == model.CheckPending }) {
			return true
		}
	}
	return false
}

// shouldPoll reports whether it's time to ask GitHub again.
func shouldPoll(prs []model.PR, now, lastPoll, settleUntil time.Time) bool {
	return currentMode(prs, now, settleUntil) != modeIdle && now.Sub(lastPoll) >= pollEvery
}

// watchedRefs lists the remote-tracking refs whose moves mean a push: the
// feature branch in every repo, plus each active PR's head (gh stacks).
func watchedRefs(name string, repos []string, prs []model.PR) map[string][]string {
	refs := map[string][]string{}
	for _, repo := range repos {
		refs[repo] = []string{"refs/remotes/origin/" + name}
	}
	for _, p := range prs {
		ref := "refs/remotes/origin/" + p.Head
		if p.Status.IsActive() && p.Head != "" && !slices.Contains(refs[p.Repo], ref) {
			refs[p.Repo] = append(refs[p.Repo], ref)
		}
	}
	return refs
}

// remoteHeads resolves the watched refs ("" if a ref doesn't exist yet).
func remoteHeads(ctx context.Context, d deps, name string, repos []string, prs []model.PR) map[string]string {
	heads := map[string]string{}
	for repo, refs := range watchedRefs(name, repos, prs) {
		wt := filepath.Join(feature.Dir(d.layout, name), repo)
		for _, ref := range refs {
			sha, _ := d.run.Run(ctx, wt, "git", "rev-parse", "-q", "--verify", ref)
			heads[repo+" "+ref] = sha
		}
	}
	return heads
}

// pushed reports whether a ref watched before moved, including from "" (first
// push of a branch). Refs that are newly watched don't count.
func pushed(before, after map[string]string) bool {
	for k, v := range after {
		if old, ok := before[k]; ok && old != v {
			return true
		}
	}
	return false
}

func watchPRs(ctx context.Context, d deps, name string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	restore := cbreak()
	defer restore()
	keys := readKeys()

	repos, _ := feature.Repos(d.layout, name)
	state, _ := feature.LoadState(d.layout, name)
	var (
		errs        map[string]error
		lastPoll    time.Time
		settleUntil time.Time
		note        string
		stateMod    = modTime(feature.StatePath(d.layout, name))
		heads       = remoteHeads(ctx, d, name, repos, state.PRs)
	)
	refresh := func() {
		draw(watchView{name: name, repos: repos, state: state, errs: errs, mode: currentMode(state.PRs, time.Now(), settleUntil), note: "refreshing…"})
		s, e, err := feature.RefreshPRs(ctx, d.run, d.layout, name)
		lastPoll, errs, note = time.Now(), e, ""
		if err != nil {
			note = red(err.Error())
			return
		}
		state, stateMod = s, modTime(feature.StatePath(d.layout, name))
	}
	refresh()

	tick := time.NewTicker(watchTick)
	defer tick.Stop()
	for {
		now := time.Now()
		draw(watchView{name: name, repos: repos, state: state, errs: errs, mode: currentMode(state.PRs, now, settleUntil), note: note, now: now})
		select {
		case <-ctx.Done():
			return nil
		case k, ok := <-keys:
			switch {
			case !ok || k == 'q':
				return nil
			case k == 'r':
				refresh()
			}
			continue
		case <-tick.C:
		}

		// Someone else (pi) updated the state: take it; a new PR settles like a push.
		if m := modTime(feature.StatePath(d.layout, name)); !m.Equal(stateMod) {
			before := len(state.PRs)
			if s, err := feature.LoadState(d.layout, name); err == nil {
				state, stateMod = s, m
			}
			if len(state.PRs) > before {
				settleUntil, lastPoll = time.Now().Add(settleFor), time.Time{}
			}
		}
		if r, err := feature.Repos(d.layout, name); err == nil {
			repos = r // f add may have added one
		}
		after := remoteHeads(ctx, d, name, repos, state.PRs)
		if pushed(heads, after) {
			settleUntil, lastPoll = time.Now().Add(settleFor), time.Time{}
		}
		heads = after
		if shouldPoll(state.PRs, time.Now(), lastPoll, settleUntil) {
			refresh()
		}
	}
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// cbreak makes single keys readable without echo (output is unchanged) and
// returns a func that restores the terminal.
func cbreak() func() {
	saved, err := stty("-g")
	if err != nil {
		return func() {}
	}
	_, _ = stty("-icanon", "-echo", "min", "1")
	fmt.Print("\033[?25l") // hide cursor
	return func() {
		_, _ = stty(saved)
		fmt.Print("\033[?25h")
	}
}

func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func readKeys() <-chan byte {
	keys := make(chan byte)
	go func() {
		defer close(keys)
		buf := make([]byte, 1)
		for {
			if n, err := os.Stdin.Read(buf); err != nil || n == 0 {
				return
			}
			keys <- buf[0]
		}
	}()
	return keys
}

func draw(v watchView) {
	if v.now.IsZero() {
		v.now = time.Now()
	}
	fmt.Print("\033[H\033[2J" + renderWatch(v))
}

type watchView struct {
	name  string
	repos []string
	state feature.State
	errs  map[string]error
	mode  watchMode
	note  string // replaces the mode line (e.g. "refreshing…", an error)
	now   time.Time
}

var checkMark = map[model.CheckState]string{
	model.CheckFail:    red("✗"),
	model.CheckPending: color("36", "⋯"),
	model.CheckPass:    green("✓"),
	model.CheckSkipped: dim("–"),
}

// renderWatch draws the PRs tab: per repo its PRs, and for open ones the
// failing and running checks one per line, passed/skipped as a count.
func renderWatch(v watchView) string {
	var b strings.Builder
	status := dim(string(v.mode))
	if v.note != "" {
		status = v.note
	}
	ago := "never"
	if !v.state.PRsRefreshedAt.IsZero() {
		ago = v.now.Sub(v.state.PRsRefreshedAt).Round(time.Second).String() + " ago"
	}
	fmt.Fprintf(&b, "%s %s\n%s %s\n\n", bold(v.name), dim("· PRs · updated "+ago), status, dim("· r refresh · q quit"))

	byRepo := map[string][]model.PR{}
	for _, p := range v.state.PRs {
		byRepo[p.Repo] = append(byRepo[p.Repo], p)
	}
	for _, repo := range v.repos {
		fmt.Fprintln(&b, bold(repo))
		if err := v.errs[repo]; err != nil {
			fmt.Fprintf(&b, "  %s\n", red("gh: "+err.Error()))
		}
		if len(byRepo[repo]) == 0 {
			fmt.Fprintln(&b, dim("  no PR"))
		}
		for i, p := range byRepo[repo] {
			prefix := model.StackPrefix(byRepo[repo], i)
			if !p.Status.IsActive() {
				fmt.Fprintf(&b, "  %s%s\n", prefix, dim(fmt.Sprintf("#%d %s  %s", p.Number, p.Status, p.Title)))
				continue
			}
			fmt.Fprintf(&b, "  %s#%d %s  %s\n", prefix, p.Number, p.Status, p.Title)
			indent := "    " + strings.Repeat(" ", len([]rune(prefix)))
			b.WriteString(renderChecks(indent, p.Checks))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderChecks(indent string, checks []model.Check) string {
	if len(checks) == 0 {
		return indent + dim("no checks yet") + "\n"
	}
	var b strings.Builder
	counts := map[model.CheckState]int{}
	for _, state := range []model.CheckState{model.CheckFail, model.CheckPending} {
		for _, c := range checks {
			if c.State == state {
				fmt.Fprintf(&b, "%s%s %s", indent, checkMark[state], c.Name)
				if state == model.CheckFail && c.URL != "" {
					b.WriteString("  " + dim(c.URL))
				}
				b.WriteString("\n")
			}
		}
	}
	for _, c := range checks {
		counts[c.State]++
	}
	var done []string
	if n := counts[model.CheckPass]; n > 0 {
		done = append(done, fmt.Sprintf("%d passed", n))
	}
	if n := counts[model.CheckSkipped]; n > 0 {
		done = append(done, fmt.Sprintf("%d skipped", n))
	}
	if len(done) > 0 {
		mark := checkMark[model.CheckPass]
		if counts[model.CheckPass] == 0 {
			mark = checkMark[model.CheckSkipped]
		}
		fmt.Fprintf(&b, "%s%s %s\n", indent, mark, dim(strings.Join(done, " · ")))
	}
	return b.String()
}
