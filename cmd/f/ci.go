package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/github"
	"github.com/seanturner026/pi/internal/model"
)

const ciLogLines = 150 // per failed job

// ciFailure is one failed check with what we could get of its log.
type ciFailure struct {
	pr     model.PR
	check  model.Check
	repo   string // "<owner>/<repo>" of an Actions job; "" if not Actions
	job    string // Actions job ID; "" if not Actions
	log    string // excerpt; "" if unavailable
	logErr error
}

// ciFix refreshes the feature's PRs, collects failing checks (with failed job
// logs for GitHub Actions) and asks pi to find the cause and propose a fix
// without pushing. With print, it writes the prompt to stdout instead (/ci).
func ciFix(ctx context.Context, d deps, explicit string, print bool) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	if print {
		msg, n, err := ciFixPrompt(ctx, d, name)
		if err == nil && n > 0 {
			fmt.Print(msg)
		}
		return err // nothing printed: /ci says there's nothing failing
	}
	n, err := sendCIFix(ctx, d, name)
	switch {
	case err != nil:
		return err
	case n == 0:
		info("no failing checks")
	default:
		ok("sent %d failing check(s) to pi", n)
	}
	return nil
}

// ciFixPrompt refreshes PRs and builds the prompt; n is the number of failing checks.
func ciFixPrompt(ctx context.Context, d deps, name string) (msg string, n int, err error) {
	state, _, err := feature.RefreshPRs(ctx, d.run, d.layout, name)
	if err != nil {
		return "", 0, err
	}
	failures := collectFailures(state.PRs)
	if len(failures) == 0 {
		return "", 0, nil
	}
	fetchLogs(ctx, d, name, failures)
	return ciPrompt(failures), len(failures), nil
}

// sendCIFix sends the prompt to the feature's pi (via Herdr); if that fails
// the prompt is copied to the clipboard.
func sendCIFix(ctx context.Context, d deps, name string) (int, error) {
	msg, n, err := ciFixPrompt(ctx, d, name)
	if err != nil || n == 0 {
		return 0, err
	}
	if err := promptPiOrCopy(ctx, d, name, msg); err != nil {
		return n, fmt.Errorf("%w (prompt copied to clipboard)", err)
	}
	return n, nil
}

func collectFailures(prs []model.PR) []*ciFailure {
	var out []*ciFailure
	for _, p := range prs {
		if !p.Status.IsActive() {
			continue
		}
		for _, c := range p.Checks {
			if c.State != model.CheckFail {
				continue
			}
			f := &ciFailure{pr: p, check: c}
			f.repo, f.job, _ = github.JobRef(c.URL)
			out = append(out, f)
		}
	}
	return out
}

func fetchLogs(ctx context.Context, d deps, name string, failures []*ciFailure) {
	var wg sync.WaitGroup
	for _, f := range failures {
		if f.job == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := github.FailedJobLog(ctx, d.run, filepath.Join(feature.Dir(d.layout, name), f.pr.Repo), f.check.URL)
			if err != nil {
				f.logErr = err
				return
			}
			f.log = github.LogExcerpt(raw, ciLogLines)
		}()
	}
	wg.Wait()
}

// ciPrompt asks pi to diagnose and propose, grouped by PR.
func ciPrompt(failures []*ciFailure) string {
	var b strings.Builder
	b.WriteString("CI is failing on this feature's PRs. For each failure: find the cause (read the code and the log; " +
		"run the check locally if you can), then propose a fix. Don't push until I OK it.\n")
	var lastPR string
	for _, f := range failures {
		if key := f.pr.URL; key != lastPR {
			lastPR = key
			fmt.Fprintf(&b, "\n## %s #%d %s\n%s\n", f.pr.Repo, f.pr.Number, f.pr.Title, f.pr.URL)
		}
		fmt.Fprintf(&b, "\n### ✗ %s\n", f.check.Name)
		if f.check.URL != "" {
			fmt.Fprintf(&b, "%s\n", f.check.URL)
		}
		switch {
		case f.log != "":
			fmt.Fprintf(&b, "Failed steps, last %d lines (full: `gh run view --job %s --log-failed -R %s`):\n```\n%s\n```\n", ciLogLines, f.job, f.repo, f.log)
		case f.logErr != nil:
			fmt.Fprintf(&b, "Couldn't get the log: %v\n", f.logErr)
		default:
			b.WriteString("Not a GitHub Actions job: no log here; open the link.\n")
		}
	}
	return b.String()
}
