package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/f/internal/agent"
	"github.com/seanturner026/f/internal/beads"
	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/herdr"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/shell"
	"github.com/seanturner026/f/internal/tracing"
)

// deps are what the actions need: where features live and how to run commands.
type deps struct {
	layout  feature.Layout
	run     feature.Runner
	socket  herdr.Caller
	noHerdr bool
}

func newDeps() (deps, error) {
	l, err := feature.DefaultLayout()
	if err != nil {
		return deps{}, err
	}
	return deps{layout: l, run: tracing.Traced{Runner: shell.Exec{}, Base: l.Base}, socket: herdr.DefaultSocket(), noHerdr: os.Getenv("F_NO_HERDR") != ""}, nil
}

func checkName(name string) error {
	// The CLI routes other subcommand names before start; the hidden help command doesn't.
	if name == "help" {
		return fmt.Errorf("%q is a subcommand; pick another name", name)
	}
	return feature.CheckName(name)
}

func resolve(d deps, explicit string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	name, err := feature.Resolve(d.layout, explicit, cwd)
	if err != nil {
		return "", err
	}
	return name, ensureInfo(d, name)
}

// ensureInfo writes .context/feature.json for features created before it existed.
func ensureInfo(d deps, name string) error {
	if _, err := os.Stat(feature.InfoPath(d.layout, name)); err == nil {
		return nil
	}
	_, err := feature.Sync(d.layout, name, "")
	return err
}

// refreshInfo rewrites feature.json and AGENTS.md (keeping the goal) when a
// feature is opened, so template and repo changes reach existing features.
func refreshInfo(d deps, name string) error {
	_, err := feature.Sync(d.layout, name, "")
	return err
}

// goal prints the feature's goal, or sets it (regenerating AGENTS.md).
func goal(ctx context.Context, d deps, explicit, text string) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	if text == "" {
		info, err := feature.LoadInfo(d.layout, name)
		if err != nil {
			return err
		}
		if info.Goal == "" {
			info.Goal = dim("(not set)")
		}
		fmt.Println(info.Goal)
		return nil
	}
	info, err := feature.Sync(d.layout, name, text)
	if err != nil {
		return err
	}
	if info.Issue != "" {
		return beads.SetDescription(ctx, d.run, d.layout.Base, info.Issue, text)
	}
	return nil
}

// start creates a feature, or reopens it if it exists. With no name, it picks
// an existing feature to reopen.
func start(ctx context.Context, d deps, name string, repos []string) error {
	if name == "" {
		if len(feature.List(d.layout)) == 0 {
			fmt.Print(help)
			return nil
		}
		tracing.Rename(ctx, "f picker")
		picked, err := pickFeature(ctx, d)
		if err != nil || picked == "" {
			return err
		}
		if err := refreshInfo(d, picked); err != nil {
			return err
		}
		ensureIssue(ctx, d, picked)
		syncInBackground(ctx, picked)
		return launch(ctx, d, picked, true)
	}
	if err := checkName(name); err != nil {
		return err
	}
	if feature.Exists(d.layout, name) {
		tracing.Rename(ctx, "f open")
		if err := refreshInfo(d, name); err != nil {
			return err
		}
		ensureIssue(ctx, d, name)
		syncInBackground(ctx, name)
		return launch(ctx, d, name, true)
	}

	if _, ok := feature.InTrash(d.layout, name); ok {
		return fmt.Errorf("%q: %w (its branch too): f restore %s, or pick another name", name, feature.ErrInTrash, name)
	}

	tracing.Rename(ctx, "f start")
	if len(repos) == 0 && !interactive() {
		// fzf would wait forever for a terminal that isn't there (pi's !f).
		return fmt.Errorf("new feature %q needs repos: f %s -r <repo> [-r <repo>...], or /f %s in chat (it asks)", name, name, name)
	}
	if len(repos) == 0 {
		all, err := feature.BaseRepos(d.layout)
		if err != nil {
			return err
		}
		if repos, err = pickRepos(all, nil); err != nil {
			return err
		}
	}
	if len(repos) == 0 {
		return errors.New("no repos selected; aborted")
	}
	if err := feature.CheckRepos(d.layout, repos); err != nil {
		return err
	}

	fmt.Println(bold(name))
	if !d.noHerdr && herdr.Inside() {
		// Open the workspace now; worktrees and the beads issue come in the
		// background (fetching takes seconds) while you write the first prompt.
		if err := setupInBackground(ctx, d, name, repos); err != nil {
			return err
		}
		return launch(ctx, d, name, false)
	}
	if err := addRepos(ctx, d, name, repos); err != nil {
		return err
	}
	if _, err := feature.Sync(d.layout, name, ""); err != nil {
		return err
	}
	ensureIssue(ctx, d, name)
	return launch(ctx, d, name, false)
}

// setupInBackground records repos as pending (so AGENTS.md, nvim and pi know
// them already) and starts `f setup` to add their worktrees.
func setupInBackground(ctx context.Context, d deps, name string, repos []string) error {
	if err := feature.BeginSetup(d.layout, name, repos); err != nil {
		return err
	}
	if _, err := feature.Sync(d.layout, name, ""); err != nil {
		return err
	}
	startDetached(ctx, append([]string{"setup", "-f", name}, repos...)...)
	return nil
}

// setup is `f setup`: add repos' worktrees (concurrently), then the beads
// issue, and tell pi what's ready (or failed) with its next prompt. pi waits
// for it before a run (state.json "setup").
func setup(ctx context.Context, d deps, name string, repos []string) error {
	errs := make([]error, len(repos))
	var wg sync.WaitGroup
	var agentsMD sync.Mutex // Sync rewrites AGENTS.md: one at a time
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, span := tracing.Start(ctx, "setup repo", attribute.String("repo", repo))
			defer func() { tracing.End(span, errs[i]) }()
			_, _, errs[i] = feature.AddRepo(ctx, d.run, d.layout, name, repo)
			if errs[i] == nil {
				agentsMD.Lock()
				_, errs[i] = feature.Sync(d.layout, name, "") // its instructions file, for AGENTS.md
				agentsMD.Unlock()
			}
			_ = feature.FinishSetup(d.layout, name, repo) // progress for pi's "setting up (n/m)"
		}()
	}
	wg.Wait()
	var ready, notes []string
	for i, repo := range repos {
		if errs[i] != nil {
			notes = append(notes, fmt.Sprintf("couldn't set up %s: %v (retry: f add %s)", repo, errs[i], repo))
			continue
		}
		if file := feature.InstructionFile(d.layout, name, repo); file != "" {
			ready = append(ready, fmt.Sprintf("%s/ (instructions: %s)", repo, file))
		} else {
			ready = append(ready, repo+"/")
		}
	}
	if len(ready) > 0 {
		notes = append([]string{"worktrees ready (AGENTS.md lists them): " + strings.Join(ready, ", ")}, notes...)
	}
	// Notes first, then end setup: pi stops waiting when setup ends and takes
	// the notes right after.
	err := feature.AddSyncNotes(d.layout, name, notes...)
	if endErr := feature.EndSetup(d.layout, name); err == nil {
		err = endErr
	}
	if _, syncErr := feature.Sync(d.layout, name, ""); err == nil { // drop failed repos from feature.json
		err = syncErr
	}
	ensureIssue(ctx, d, name) // bd create: ~0.8s, after pi is unblocked
	return err
}

// setupWait blocks until f setup has added the current feature's worktrees
// (state.json "setup" cleared): the Claude Code mod holds a turn on it. A
// crashed f setup must not block the agent forever, hence the timeout.
func setupWait(ctx context.Context, d deps) error {
	const timeout = 5 * time.Minute
	name, err := resolve(d, "")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if state, err := feature.LoadState(d.layout, name); err != nil || state.Setup == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("setup still running after %s", timeout)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func addRepos(ctx context.Context, d deps, name string, repos []string) error {
	info("  fetching %s", strings.Join(repos, ", "))
	type added struct {
		result feature.AddResult
		base   string
		err    error
	}
	// Repos are independent clones: fetch and add worktrees concurrently.
	out := make([]added, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, base, err := feature.AddRepo(ctx, d.run, d.layout, name, repo)
			out[i] = added{r, base, err}
		}()
	}
	wg.Wait()
	var errs []error
	for i, repo := range repos {
		switch a := out[i]; {
		case a.err != nil:
			errs = append(errs, a.err)
		case a.result == feature.AddedNewBranch:
			ok("  %s: worktree on new branch %s (from %s)", repo, name, a.base)
		case a.result == feature.AddedExistingBranch:
			ok("  %s: worktree on existing branch %s", repo, name)
		case a.result == feature.AlreadyPresent:
			info("  %s: already present", repo)
		}
	}
	return errors.Join(errs...)
}

// featureRepos is the feature's repos for its Herdr tabs: feature.json's list,
// which has repos f setup is still adding (their review tabs wait for them).
func featureRepos(d deps, name string) ([]string, error) {
	if info, err := feature.LoadInfo(d.layout, name); err == nil && len(info.Repos) > 0 {
		return info.Repos, nil
	}
	return feature.Repos(d.layout, name)
}

// launch runs the agent for the feature: in its own Herdr workspace when
// called from inside Herdr, otherwise right here in this terminal.
func launch(ctx context.Context, d deps, name string, reopen bool) error {
	dir := feature.Dir(d.layout, name)
	chat := agent.For(name, dir, reopen)
	// Inside Herdr, its server is up by definition: skip the status round trip.
	if !d.noHerdr && herdr.Inside() {
		repos, err := featureRepos(d, name)
		if err != nil {
			return err
		}
		_ = herdr.BackToChat(ctx, d.run)
		result, err := herdr.OpenWorkspace(ctx, d.run, name, dir, repos, chat)
		if err != nil {
			return err
		}
		ok("%s herdr workspace %s", result, name)
		return nil
	}
	if !interactive() {
		fmt.Printf("cd %s && %s %s\n", shell.Quote(dir), chat.Bin, strings.Join(shell.QuoteAll(chat.Args), " "))
		return nil
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	bin, err := exec.LookPath(chat.Bin)
	if err != nil {
		return err
	}
	tracing.Finish(ctx) // Exec replaces this process: write the spans first
	return syscall.Exec(bin, append([]string{chat.Bin}, chat.Args...), os.Environ())
}

// resume restarts pi, nvim, review, and prs in every open feature workspace
// where they stopped (after a Herdr restart its layout is back, bare shells).
func resume(ctx context.Context, d deps) error {
	if d.noHerdr || !(herdr.Inside() || herdr.Available(ctx, d.run)) {
		return herdr.ErrUnavailable
	}
	open, err := herdr.FeatureWorkspaces(ctx, d.run, feature.List(d.layout))
	if err != nil {
		return err
	}
	if len(open) == 0 {
		info("no feature workspaces open")
		return nil
	}
	type result struct {
		n   int
		err error
	}
	// pi takes a few seconds to start: do the workspaces concurrently.
	results := map[string]*result{}
	var wg sync.WaitGroup
	for name, ws := range open {
		res := &result{}
		results[name] = res
		wg.Add(1)
		go func() {
			defer wg.Done()
			repos, err := featureRepos(d, name)
			if err != nil {
				res.err = err
				return
			}
			res.n, res.err = herdr.ReviveWorkspace(ctx, d.run, ws, name, repos, agent.For(name, feature.Dir(d.layout, name), true))
		}()
	}
	wg.Wait()
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		switch res := results[name]; {
		case res.err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", name, res.err))
		case res.n > 0:
			ok("%s: restarted %d tab(s)", name, res.n)
		default:
			info("%s: already running", name)
		}
	}
	return errors.Join(errs...)
}

func add(ctx context.Context, d deps, explicit string, repos []string) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		have, err := feature.Repos(d.layout, name)
		if err != nil {
			return err
		}
		all, err := feature.BaseRepos(d.layout)
		if err != nil {
			return err
		}
		if repos, err = pickRepos(all, have); err != nil {
			return err
		}
		if len(repos) == 0 {
			return errors.New("no repos selected")
		}
	}
	if err := feature.CheckRepos(d.layout, repos); err != nil {
		return err
	}
	// You typing in Herdr: return now and set up in the background. pi running
	// f add (not a terminal) uses the repo right after, so it waits.
	lazy := interactive() && !d.noHerdr && herdr.Inside()
	if lazy {
		if err := setupInBackground(ctx, d, name, repos); err != nil {
			return err
		}
		info("setting up %s in the background; the agent gets it with your next prompt", strings.Join(repos, ", "))
	} else {
		if err := addRepos(ctx, d, name, repos); err != nil {
			return err
		}
		if _, err := feature.Sync(d.layout, name, ""); err != nil {
			return err
		}
		info("AGENTS.md updated")
	}
	if !d.noHerdr && herdr.Available(ctx, d.run) {
		for _, repo := range repos {
			if added, err := herdr.AddRepoTab(ctx, d.run, d.socket, name, feature.Dir(d.layout, name), repo); err != nil {
				return err
			} else if added {
				ok("added herdr tab %s", repo)
			}
		}
	}
	return nil
}

// prsOptions are the flags of `f prs`.
type prsOptions struct {
	refresh bool   // look up live (otherwise use .context/state.json)
	noOpen  bool   // don't open the browser
	asJSON  bool   // print JSON only
	addURL  string // record this PR URL and exit
	copy    bool   // copy "url: title" lines to the clipboard
}

// prsOutput is the --json shape of `f prs`.
type prsOutput struct {
	PRs         []model.PR        `json:"prs"`
	RefreshedAt time.Time         `json:"refreshed_at,omitzero"`
	Errors      map[string]string `json:"errors,omitempty"`
}

// prs shows the feature's PRs from its saved state (refreshing live if asked
// or if there's no state yet) and opens the active ones in the browser.
func prs(ctx context.Context, d deps, explicit string, opts prsOptions) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	if opts.addURL != "" {
		pr, err := feature.ParsePRURL(opts.addURL)
		if err != nil {
			return err
		}
		return feature.RecordPR(d.layout, name, pr)
	}

	state, err := feature.LoadState(d.layout, name)
	if err != nil {
		return err
	}
	var errs map[string]error
	if opts.refresh || state.PRsRefreshedAt.IsZero() {
		if state, errs, err = feature.RefreshPRs(ctx, d.run, d.layout, name); err != nil {
			return err
		}
	}

	if opts.asJSON {
		out := prsOutput{PRs: state.PRs, RefreshedAt: state.PRsRefreshedAt, Errors: map[string]string{}}
		for repo, e := range errs {
			out.Errors[repo] = e.Error()
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	if opts.copy {
		return copyPRs(state.PRs)
	}
	printPRs(d, name, state.PRs, errs)
	if !opts.refresh && errs == nil {
		info("as of %s ago (-r to refresh)", time.Since(state.PRsRefreshedAt).Round(time.Second))
	}
	if opts.noOpen {
		return nil
	}
	var urls []string
	for _, p := range state.PRs {
		if p.Status.IsActive() {
			urls = append(urls, p.URL)
		}
	}
	if len(urls) > 0 {
		if err := exec.Command("open", urls...).Run(); err != nil {
			return fmt.Errorf("open browser: %w", err)
		}
	}
	return nil
}

// copyPRs puts the active PRs (or all, if none are active) on the clipboard.
func copyPRs(prs []model.PR) error {
	var active []model.PR
	for _, p := range prs {
		if p.Status.IsActive() {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		active = prs
	}
	if len(active) == 0 {
		info("no PRs")
		return nil
	}
	text := model.PRClipboard(active)
	if err := copyToClipboard(text); err != nil {
		return err
	}
	fmt.Print(text)
	ok("copied %d PR(s)", len(active))
	return nil
}

func printPRs(d deps, name string, list []model.PR, errs map[string]error) {
	byRepo := map[string][]model.PR{}
	for _, p := range list {
		byRepo[p.Repo] = append(byRepo[p.Repo], p)
	}
	repos, _ := feature.Repos(d.layout, name)
	sort.Strings(repos)
	for _, repo := range repos {
		fmt.Println(bold(repo))
		switch {
		case errs[repo] != nil:
			fmt.Printf("  %s\n", red("gh: "+errs[repo].Error()))
		case len(byRepo[repo]) == 0:
			fmt.Println(dim("  no PR"))
		}
		for i, p := range byRepo[repo] {
			indent := model.StackPrefix(byRepo[repo], i)
			fmt.Printf("  %s#%d %-7s %s\n  %s       %s\n", indent, p.Number, p.Status, p.Title, strings.Repeat(" ", utf8.RuneCountInString(indent)), dim(p.URL))
		}
	}
}

// restore brings back a removed feature, reopens its beads issue, and opens
// it. With no name it lists the trash.
func restore(ctx context.Context, d deps, name string) error {
	if name == "" {
		trash := feature.Trash(d.layout)
		if len(trash) == 0 {
			info("trash is empty")
		}
		for _, t := range trash {
			fmt.Printf("%s  %s\n", t.Name, dim("removed "+feature.TrashAge(t)))
		}
		return nil
	}
	if err := feature.Restore(ctx, d.run, d.layout, name); err != nil {
		return err
	}
	if info, _ := feature.LoadInfo(d.layout, name); info.Issue != "" {
		_ = beads.Reopen(ctx, d.run, d.layout.Base, info.Issue) // fails if it was left open: fine
	}
	fmt.Println(green("restored " + name))
	return start(ctx, d, name, nil)
}
