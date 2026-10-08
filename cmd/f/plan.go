package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/seanturner026/pi/internal/beads"
	"github.com/seanturner026/pi/internal/feature"
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

// closeIssue closes the feature's issue (call before the feature is removed).
// If bd won't close it (e.g. plan steps still open), it notes why and leaves
// it open; the returned text says so ("" when closed or there's no issue).
func closeIssue(ctx context.Context, d deps, name string) string {
	info, _ := feature.LoadInfo(d.layout, name)
	if info.Issue == "" {
		return ""
	}
	state, _ := feature.LoadState(d.layout, name)
	reason := "feature removed (no changes)"
	if urls := prURLs(state.PRs, model.PRStatusMerged); len(urls) > 0 {
		reason = "merged: " + strings.Join(urls, " ")
	}
	if err := beads.Close(ctx, d.run, d.layout.Base, info.Issue, reason); err != nil {
		_ = beads.Note(ctx, d.run, d.layout.Base, info.Issue, "f feature "+name+" removed; issue left open: "+err.Error())
		return fmt.Sprintf("issue %s left open (plan steps still open?)", info.Issue)
	}
	return ""
}

// keepIssueOpen notes on the issue that the feature was removed with
// unmerged work, and leaves it open to come back to.
func keepIssueOpen(ctx context.Context, d deps, name string, problems []string) {
	info, _ := feature.LoadInfo(d.layout, name)
	if info.Issue == "" {
		return
	}
	_ = beads.Note(ctx, d.run, d.layout.Base, info.Issue, "f feature removed with unmerged work: "+strings.Join(problems, "; "))
}

func prURLs(prs []model.PR, status model.PRStatus) []string {
	var urls []string
	for _, p := range prs {
		if p.Status == status {
			urls = append(urls, p.URL)
		}
	}
	return urls
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
