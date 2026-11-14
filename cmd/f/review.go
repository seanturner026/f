package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/git"
	"github.com/seanturner026/f/internal/herdr"
	"github.com/seanturner026/f/internal/model"
)

// reviewView is one thing a review tab can show, as tuicr's range arguments.
type reviewView struct {
	title  string
	args   []string
	live   bool     // includes the work tree: waits for changes
	follow []string // refs that restart the view when they move (tuicr resolves its range once)
}

// reviewOutcome is why tuicr stopped.
type reviewOutcome int

const (
	reviewQuit      reviewOutcome = iota // you quit: offer the other views
	reviewNoChanges                      // nothing to review yet: wait
	reviewMoved                          // HEAD or the base moved: start again
)

// reviewPoll is how often a visible live review checks whether HEAD or the
// base moved, and how often a waiting one looks for changes.
const reviewPoll = 3 * time.Second

// review runs tuicr for one repo in its Herdr tab: the whole feature first;
// quitting tuicr offers each open PR of the stack and the agent's turns. tuicr
// is paused while its tab isn't on screen, so idle tabs cost nothing.
func review(ctx context.Context, d deps, repo string) error {
	name, err := resolve(d, "")
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("tuicr"); err != nil {
		return errors.New("tuicr not installed (brew install tuicr)")
	}
	dir := feature.RepoDir(d.layout, name, repo)
	vis := watchVisibility(ctx)
	view := reviewViews(ctx, d, name, repo, dir)[0]
	for ctx.Err() == nil {
		if _, err := os.Stat(dir); err != nil {
			waitScreen(ctx, vis, repo+": f is still setting up this worktree.")
			continue
		}
		outcome := runTuicr(ctx, d, dir, view, vis)
		resetTerminal()
		switch outcome {
		case reviewNoChanges:
			waitScreen(ctx, vis, repo+": no changes to review yet. This tab opens the review once there are.")
		case reviewQuit:
			next, ok, err := pickReviewView(reviewViews(ctx, d, name, repo, dir), view)
			if err != nil || !ok {
				return err // no view picked: back to the shell
			}
			view = next
		}
	}
	return ctx.Err()
}

// reviewViews lists the views of repo: the whole feature, each open PR of
// its stack (bottom first), and the agent's turns once there are any.
func reviewViews(ctx context.Context, d deps, name, repo, dir string) []reviewView {
	base := "origin/" + git.DefaultBranch(ctx, d.run, dir)
	views := []reviewView{{title: "whole feature (vs " + base + ")", args: []string{"-r", base + "...HEAD", "-w"}, live: true, follow: []string{base, "HEAD"}}}
	if feature.TurnsRange(ctx, d.run, dir) != "" {
		views = append(views, reviewView{title: "last turn (follows new turns)",
			args: []string{"-r", feature.TurnsRef + "~1.." + feature.TurnsRef}, follow: []string{feature.TurnsRef}})
	}
	state, _ := feature.LoadState(d.layout, name)
	var prs []model.PR
	for _, p := range state.PRs {
		if p.Repo == repo && p.Status.IsActive() && p.Head != "" && p.Base != "" {
			prs = append(prs, p)
		}
	}
	slices.SortFunc(prs, func(a, b model.PR) int { return cmp.Or(cmp.Compare(a.Stack, b.Stack), cmp.Compare(a.Number, b.Number)) })
	current := git.CurrentBranch(ctx, d.run, dir)
	ref := func(branch string) string {
		if git.RevParse(ctx, d.run, dir, "refs/heads/"+branch) != "" {
			return branch
		}
		return "origin/" + branch
	}
	for i, p := range prs {
		v := reviewView{title: fmt.Sprintf("%d/%d #%d %s", i+1, len(prs), p.Number, p.Title)}
		if p.Head == current {
			v.title += "  (checked out, live)"
			v.args, v.live, v.follow = []string{"-r", ref(p.Base) + "...HEAD", "-w"}, true, []string{ref(p.Base), "HEAD"}
		} else {
			v.args = []string{"-r", ref(p.Base) + "..." + ref(p.Head)}
		}
		views = append(views, v)
	}
	if turns := feature.TurnsRange(ctx, d.run, dir); turns != "" {
		views = append(views, reviewView{title: "agent turns (pick one or more in tuicr's commit list)", args: []string{"-r", turns}})
	}
	return views
}

// pickReviewView asks which view to open next (fzf); ok is false to leave.
func pickReviewView(views []reviewView, current reviewView) (reviewView, bool, error) {
	lines := make([]string, 0, len(views)+1)
	for i, v := range views {
		mark := "  "
		if slices.Equal(v.args, current.args) {
			mark = "● "
		}
		lines = append(lines, fmt.Sprintf("%d\t%s%s", i, mark, v.title))
	}
	lines = append(lines, "-1\t  leave the review (shell)")
	out, err := runFzf(lines, "--delimiter=\t", "--with-nth=2..", "--prompt=review> ", "--header=⏎ open · esc back to "+current.title)
	if err != nil {
		return reviewView{}, false, err
	}
	if len(out) == 0 {
		return current, true, nil // esc: the same view again
	}
	var i int
	fmt.Sscan(strings.SplitN(out[0], "\t", 2)[0], &i)
	if i < 0 || i >= len(views) {
		return reviewView{}, false, nil
	}
	return views[i], true, nil
}

// runTuicr runs tuicr on view until you quit it, there's nothing to review, or
// (live views) HEAD or the base moves. While its tab is hidden it's stopped.
func runTuicr(ctx context.Context, d deps, dir string, view reviewView, vis *visibility) reviewOutcome {
	args := view.args
	if view.live && !rangeHasCommits(ctx, d, dir, view) {
		args = []string{"-w"} // tuicr finds nothing in an empty range, even with -w
	}
	var stderr tail
	cmd := exec.CommandContext(ctx, "tuicr", args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, red("tuicr: "+err.Error()))
		return reviewNoChanges
	}
	pid := cmd.Process.Pid
	unsubscribe := vis.onChange(func(visible bool) {
		sig := syscall.SIGCONT
		if !visible {
			sig = syscall.SIGSTOP
		}
		_ = syscall.Kill(pid, sig)
	})
	defer unsubscribe()

	done := make(chan struct{})
	var moved atomic.Bool
	if len(view.follow) > 0 {
		start := reviewMarks(ctx, d, dir, view)
		go func() {
			for {
				select {
				case <-done:
					return
				case <-time.After(reviewPoll):
				}
				if vis.visible() && reviewMarks(ctx, d, dir, view) != start {
					moved.Store(true)
					_ = syscall.Kill(pid, syscall.SIGTERM)
					return
				}
			}
		}()
	}
	err := cmd.Wait()
	close(done)
	switch {
	case moved.Load():
		return reviewMoved
	case err != nil && strings.Contains(string(stderr), "No changes to review"):
		return reviewNoChanges
	}
	return reviewQuit
}

// tail keeps the last few KB written to it: enough for tuicr's exit error,
// without growing for as long as a review stays open.
type tail []byte

func (t *tail) Write(p []byte) (int, error) {
	*t = append(*t, p...)
	if len(*t) > 4096 {
		*t = (*t)[len(*t)-4096:]
	}
	return len(p), nil
}

// rangeHasCommits reports whether a live view's base...HEAD has any commits.
func rangeHasCommits(ctx context.Context, d deps, dir string, view reviewView) bool {
	base, _, _ := strings.Cut(view.args[1], "...")
	n, err := d.run.Run(ctx, dir, "git", "rev-list", "--count", base+"..HEAD")
	return err != nil || strings.TrimSpace(n) != "0" // unsure: let tuicr decide
}

// resetTerminal undoes the terminal modes tuicr turns on, which it can't undo
// itself when f stops it to restart: the kitty keyboard protocol (fzf would
// read arrows as text like "1;1:1B"), mouse reporting, bracketed paste, its screen.
func resetTerminal() {
	// One pop at a time too: Herdr passes those on to the outer terminal (WezTerm).
	fmt.Print(strings.Repeat("\x1b[<1u", 10) + "\x1b[<10u\x1b[=0;1u\x1b[>4;0m" + // keyboard protocols
		"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l" + // mouse
		"\x1b[?2004l\x1b[?1049l\x1b[?25h") // bracketed paste, main screen, cursor
}

// reviewMarks is where the refs a view follows point now.
func reviewMarks(ctx context.Context, d deps, dir string, view reviewView) string {
	marks := make([]string, len(view.follow))
	for i, ref := range view.follow {
		marks[i] = git.RevParse(ctx, d.run, dir, ref)
	}
	return strings.Join(marks, " ")
}

// waitScreen shows text, then returns after reviewPoll once the tab is visible
// (a hidden tab waits without checking anything).
func waitScreen(ctx context.Context, vis *visibility, text string) {
	fmt.Print("\033[H\033[2J\n  " + text + "\n")
	select {
	case <-ctx.Done():
	case <-time.After(reviewPoll):
	}
	vis.waitVisible(ctx)
}

// visibility tracks whether this pane is on screen (always, outside Herdr).
type visibility struct {
	mu        sync.Mutex
	shown     bool
	listeners map[int]func(bool)
	next      int
	changed   chan struct{}
}

func watchVisibility(ctx context.Context) *visibility {
	v := &visibility{shown: true, listeners: map[int]func(bool){}, changed: make(chan struct{})}
	if pane := os.Getenv("HERDR_PANE_ID"); pane != "" {
		go herdr.WatchFocus(ctx, herdr.DefaultSocket(), pane, v.set)
	}
	return v
}

func (v *visibility) set(shown bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.shown = shown
	for _, fn := range v.listeners {
		fn(shown)
	}
	close(v.changed)
	v.changed = make(chan struct{})
}

func (v *visibility) visible() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.shown
}

// onChange calls fn with the current state and on every change, until the
// returned func is called.
func (v *visibility) onChange(fn func(bool)) func() {
	v.mu.Lock()
	defer v.mu.Unlock()
	id := v.next
	v.next++
	v.listeners[id] = fn
	fn(v.shown)
	return func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		delete(v.listeners, id)
	}
}

func (v *visibility) waitVisible(ctx context.Context) {
	for {
		v.mu.Lock()
		shown, changed := v.shown, v.changed
		v.mu.Unlock()
		if shown {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}
