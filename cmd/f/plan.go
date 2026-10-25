package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/seanturner026/pi/internal/beads"
	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/github"
	"github.com/seanturner026/pi/internal/model"
)

// ensureIssue gives the feature a beads issue if it has none yet (new
// features, and older ones the first time they're reopened). Best effort:
// without bd, features work as before.
func ensureIssue(ctx context.Context, d deps, name string) {
	info, err := feature.LoadInfo(d.layout, name)
	if err != nil || info.Issue != "" {
		return
	}
	id, err := beads.Create(ctx, d.run, d.layout.Base, name, beads.TypeFeature, info.Goal)
	if err != nil {
		fmt.Println(dim(fmt.Sprintf("beads: %v", err)))
		return
	}
	if _, err := feature.SetIssue(d.layout, name, id); err != nil {
		fmt.Println(dim("beads: " + err.Error()))
	}
}

// closeReason is a removed feature's close reason: its merged PRs, if any.
func closeReason(prs []model.PR) string {
	var urls []string
	for _, p := range prs {
		if p.Status == model.PRStatusMerged {
			urls = append(urls, p.URL)
		}
	}
	if len(urls) == 0 {
		return "feature removed (no changes)"
	}
	return "merged: " + strings.Join(urls, " ")
}

// closeOrNote closes the issue; if bd won't (e.g. plan steps still open), it
// notes why on the issue and leaves it open.
func closeOrNote(ctx context.Context, d deps, issue, name, reason string) (closed bool, err error) {
	if err := beads.Close(ctx, d.run, d.layout.Base, issue, reason); err != nil {
		return false, beads.Note(ctx, d.run, d.layout.Base, issue, "f feature "+name+" removed; issue left open: "+err.Error())
	}
	return true, nil
}

// planSync saves the feature's beads progress for the picker (pi runs it
// after each turn; bd is too slow to call while the picker opens).
func planSync(ctx context.Context, d deps) error {
	name, err := resolve(d, "")
	if err != nil {
		return err
	}
	return planSyncFeature(ctx, d, name)
}

// planShow prints the current feature's plan (/plan in pi).
func planShow(ctx context.Context, d deps) error {
	name, err := resolve(d, "")
	if err != nil {
		return err
	}
	info, err := feature.LoadInfo(d.layout, name)
	if err != nil {
		return err
	}
	if info.Issue == "" {
		fmt.Printf("%s has no beads issue\n", name)
		return nil
	}
	steps, err := beads.Steps(ctx, d.run, d.layout.Base, info.Issue)
	if err != nil {
		return err
	}
	fmt.Print(model.PlanView(name, info.Issue, steps))
	return nil
}

func planSyncFeature(ctx context.Context, d deps, name string) error {
	info, err := feature.LoadInfo(d.layout, name)
	if err != nil || info.Issue == "" {
		return err
	}
	plan, err := beads.Progress(ctx, d.run, d.layout.Base, info.Issue)
	if err != nil {
		return err
	}
	return feature.SavePlan(d.layout, name, plan)
}

// issueUpdateInBackground prepares the beads update for a feature about to be
// removed (reading its info and PRs now, while they exist) and returns a func
// that runs it detached (`f close-issue`): close it with its merged PRs, or,
// with force, note the unmerged work and leave it open.
func issueUpdateInBackground(d deps, name string, force bool, problems []string) func() {
	info, _ := feature.LoadInfo(d.layout, name)
	if info.Issue == "" {
		return func() {}
	}
	args := []string{"close-issue", "--issue", info.Issue, "--name", name}
	if force {
		args = append(args, "--note", "f feature removed with unmerged work: "+strings.Join(problems, "; "))
	} else {
		state, _ := feature.LoadState(d.layout, name)
		args = append(args, "--reason", closeReason(state.PRs))
	}
	return func() { startDetached(args...) }
}

// closeIssueCmd is `f close-issue`: close the issue with reason (if bd won't,
// e.g. plan steps still open, note why and leave it open), or just add note.
func closeIssueCmd(ctx context.Context, d deps, issue, name, reason, note string) error {
	if note != "" {
		return beads.Note(ctx, d.run, d.layout.Base, issue, note)
	}
	_, err := closeOrNote(ctx, d, issue, name, reason)
	return err
}

// closeMergedSteps closes the feature's open steps whose linked PRs have all
// merged (model.MergedSteps): GitHub decides when a linked step is done, so
// beads doesn't go stale when pi isn't around to close it. PRs the feature
// doesn't track (e.g. a merged stack branch f sync dropped) are looked up live.
// Returns a line per closed step, and every PR URL linked to a step.
func closeMergedSteps(ctx context.Context, d deps, name string, prs []model.PR) (lines []string, linked map[string]bool) {
	linked = map[string]bool{}
	info, err := feature.LoadInfo(d.layout, name)
	if err != nil || info.Issue == "" {
		return nil, linked
	}
	steps, err := beads.Steps(ctx, d.run, d.layout.Base, info.Issue)
	if err != nil {
		return nil, linked
	}
	known := map[string]model.PRStatus{}
	for _, p := range prs {
		known[strings.TrimSuffix(p.URL, "/")] = p.Status
	}
	for _, s := range steps {
		for _, u := range s.PRs {
			linked[u] = true
		}
	}
	status := func(url string) model.PRStatus {
		if st, ok := known[url]; ok {
			return st
		}
		st, _ := github.PRStatus(ctx, d.run, d.layout.Base, url) // "" on error: not merged
		known[url] = st
		return st
	}
	for _, s := range model.MergedSteps(steps, status) {
		reason := strings.Join(s.PRs, " ")
		if err := beads.Close(ctx, d.run, d.layout.Base, s.ID, reason); err != nil {
			lines = append(lines, fmt.Sprintf("couldn't close beads step %s (%s): %v", s.ID, s.Title, err))
			continue
		}
		lines = append(lines, fmt.Sprintf("closed beads step %s (%s): %s merged", s.ID, s.Title, reason))
	}
	if len(lines) > 0 {
		_ = planSyncFeature(ctx, d, name) // the picker's progress
	}
	return lines, linked
}
