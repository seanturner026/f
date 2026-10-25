package main

import (
	"strings"
	"testing"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/model"
)

func TestNewlyMerged(t *testing.T) {
	before := []model.PR{
		{URL: "a", Status: model.PRStatusOpen},
		{URL: "b", Status: model.PRStatusMerged},
		{URL: "c", Status: model.PRStatusOpen},
	}
	after := []model.PR{
		{URL: "a", Status: model.PRStatusMerged}, // newly merged
		{URL: "b", Status: model.PRStatusMerged}, // already merged
		{URL: "c", Status: model.PRStatusOpen},
		{URL: "d", Status: model.PRStatusMerged}, // unknown before: pi just recorded it, skip
	}
	got := newlyMerged(before, after)
	if len(got) != 1 || got[0].URL != "a" {
		t.Errorf("newlyMerged = %+v, want just a", got)
	}
}

func TestSyncLines(t *testing.T) {
	tests := []struct {
		name string
		res  feature.SyncResult
		want []string
	}{
		{
			name: "stack",
			res: feature.SyncResult{Repo: "app", Action: feature.SyncDone, Base: "origin/main", Branches: []feature.BranchSync{
				{Branch: "feat", Action: feature.BranchReset, PR: 12},
				{Branch: "feat-a", Action: feature.BranchDeleted, PR: 13},
				{Branch: "feat-b", Action: feature.BranchKept, PR: 14, New: 2},
				{Branch: "feat-c", Action: feature.BranchRebased, Pushed: true, Retargeted: 15},
				{Branch: "feat-d", Action: feature.BranchRebased},
			}},
			want: []string{
				"✓ app: feat (#12 merged): reset to origin/main",
				"✓ app: feat-a (#13 merged): deleted",
				"✓ app: feat-b (#14 merged): kept (2 new commits), replayed onto origin/main",
				"✓ app: feat-c: rebased and pushed, #15 now based on main",
				"✓ app: feat-d: rebased (next push: --force-with-lease)",
			},
		},
		{
			name: "needs you",
			res:  feature.SyncResult{Repo: "app", Action: feature.SyncNeeded, Detail: "1 uncommitted change(s)"},
			want: []string{"✗ app: 1 uncommitted change(s)"},
		},
		{
			name: "done, push failed",
			res: feature.SyncResult{Repo: "app", Action: feature.SyncDone, Base: "origin/main", Detail: "push feat-c: rejected",
				Branches: []feature.BranchSync{{Branch: "feat-c", Action: feature.BranchRebased}}},
			want: []string{"✓ app: feat-c: rebased (next push: --force-with-lease)", "✗ app: push feat-c: rejected"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncLines([]feature.SyncResult{tt.res}, false)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("syncLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}
