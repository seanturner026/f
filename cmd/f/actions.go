package main

import (
	"bufio"
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

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/herdr"
	"github.com/seanturner026/pi/internal/model"
	"github.com/seanturner026/pi/internal/shell"
)

// Subcommand names can't be feature names, or `f <name>` would be ambiguous.
var reserved = map[string]bool{"creds": true, "guard": true, "add": true, "prs": true, "goal": true, "comment": true, "checkpoint": true, "help": true, "completion": true}

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
	return deps{layout: l, run: shell.Exec{}, socket: herdr.DefaultSocket(), noHerdr: os.Getenv("F_NO_HERDR") != ""}, nil
}

func checkName(name string) error {
	if reserved[name] {
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

// goal prints the feature's goal, or sets it (regenerating AGENTS.md).
func goal(d deps, explicit, text string) error {
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
	_, err = feature.Sync(d.layout, name, text)
	return err
}

// start creates a feature, or reopens it if it exists. With no name, it picks
// an existing feature to reopen.
func start(ctx context.Context, d deps, name string, repos []string, prompt string) error {
	if name == "" {
		if len(feature.List(d.layout)) == 0 {
			fmt.Print(help)
			return nil
		}
		picked, err := pickFeature(ctx, d)
		if err != nil || picked == "" {
			return err
		}
		if err := ensureInfo(d, picked); err != nil {
			return err
		}
		return launch(ctx, d, picked, "", true)
	}
	if err := checkName(name); err != nil {
		return err
	}
	if feature.Exists(d.layout, name) {
		if err := ensureInfo(d, name); err != nil {
			return err
		}
		return launch(ctx, d, name, "", true)
	}

	// Starting something new: offer to clean up finished features first.
	if len(repos) == 0 && interactive() {
		offerCleanup(ctx, d)
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
	if err := addRepos(ctx, d, name, repos); err != nil {
		return err
	}
	if _, err := feature.Sync(d.layout, name, prompt); err != nil {
		return err
	}
	return launch(ctx, d, name, prompt, false)
}

func addRepos(ctx context.Context, d deps, name string, repos []string) error {
	for _, repo := range repos {
		info("  %s: fetching", repo)
		result, base, err := feature.AddRepo(ctx, d.run, d.layout, name, repo)
		if err != nil {
			return err
		}
		switch result {
		case feature.AddedNewBranch:
			ok("  %s: worktree on new branch %s (from %s)", repo, name, base)
		case feature.AddedExistingBranch:
			ok("  %s: worktree on existing branch %s", repo, name)
		case feature.AlreadyPresent:
			info("  %s: already present", repo)
		}
	}
	return nil
}

// launch runs pi for the feature: in its own Herdr workspace when called from
// inside Herdr, otherwise right here in this terminal.
func launch(ctx context.Context, d deps, name, prompt string, reopen bool) error {
	args := []string{"--name", name}
	if reopen {
		args = append(args, "--continue")
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	dir := feature.Dir(d.layout, name)
	// Inside Herdr, its server is up by definition: skip the status round trip.
	if !d.noHerdr && herdr.Inside() {
		repos, err := feature.Repos(d.layout, name)
		if err != nil {
			return err
		}
		result, err := herdr.OpenWorkspace(ctx, d.run, name, dir, repos, args)
		if err != nil {
			return err
		}
		ok("%s herdr workspace %s", result, name)
		return nil
	}
	if !interactive() {
		fmt.Printf("cd %s && pi %s\n", shell.Quote(dir), strings.Join(shell.QuoteAll(args), " "))
		return nil
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	pi, err := exec.LookPath("pi")
	if err != nil {
		return err
	}
	return syscall.Exec(pi, append([]string{"pi"}, args...), os.Environ())
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
	if err := addRepos(ctx, d, name, repos); err != nil {
		return err
	}
	if _, err := feature.Sync(d.layout, name, ""); err != nil {
		return err
	}
	info("AGENTS.md updated; /reload in pi to pick it up")
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
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy to clipboard: %w", err)
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

// offerCleanup offers to remove finished features before starting a new one.
func offerCleanup(ctx context.Context, d deps) { cleanMerged(ctx, d, true) }

// cleanMerged finds finished features (checked in parallel) and removes them:
// worktrees, branches, and Herdr workspace. With ask, it confirms first.
func cleanMerged(ctx context.Context, d deps, ask bool) []string {
	names := feature.List(d.layout)
	finished := make([]bool, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() { defer wg.Done(); finished[i] = feature.Finished(ctx, d.run, d.layout, name) }()
	}
	wg.Wait()
	var done []string
	for i, name := range names {
		if finished[i] {
			done = append(done, name)
		}
	}
	if len(done) == 0 {
		return nil
	}
	if ask {
		fmt.Printf("merged: %s. clean up? [Y/n] ", strings.Join(done, ", "))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
			return nil
		}
	}
	var removed []string
	for _, name := range done {
		if err := feature.Remove(ctx, d.run, d.layout, name, false); err != nil {
			fmt.Printf("%s %v\n", red("cleanup:"), err)
			continue
		}
		if !d.noHerdr {
			_ = herdr.CloseWorkspace(ctx, d.run, name)
		}
		removed = append(removed, name)
		if ask {
			ok("removed %s", name)
		}
	}
	return removed
}
