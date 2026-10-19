package main

import (
	"testing"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/model"
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
		want string
	}{
		{
			name: "reset",
			res:  feature.SyncResult{Repo: "app", Action: feature.SyncReset, PR: model.PR{Number: 12}, Detail: "reset to origin/main"},
			want: "✓ app: #12 merged, reset to origin/main (next push: --force-with-lease)",
		},
		{
			name: "needs you",
			res:  feature.SyncResult{Repo: "app", Action: feature.SyncNeeded, PR: model.PR{Number: 12}, Detail: "1 uncommitted change(s)"},
			want: "✗ app: #12 merged: 1 uncommitted change(s)",
		},
		{
			name: "stack",
			res:  feature.SyncResult{Repo: "app", Action: feature.SyncStack, Detail: "gh stack sync: rebased onto main and pushed"},
			want: "✓ app: gh stack sync: rebased onto main and pushed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncLines([]feature.SyncResult{tt.res}, false); len(got) != 1 || got[0] != tt.want {
				t.Errorf("syncLines = %q, want %q", got, tt.want)
			}
		})
	}
}
