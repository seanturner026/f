package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/model"
)

func openPR(checks ...model.CheckState) model.PR {
	p := model.PR{Repo: "argocd", Status: model.PRStatusOpen}
	for _, s := range checks {
		p.Checks = append(p.Checks, model.Check{Name: string(s), State: s})
	}
	return p
}

func TestShouldPoll(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-10*time.Second), now.Add(-2*time.Minute)
	tests := []struct {
		name        string
		prs         []model.PR
		lastPoll    time.Time
		settleUntil time.Time
		want        bool
	}{
		{name: "checks running, due", prs: []model.PR{openPR(model.CheckPending)}, lastPoll: old, want: true},
		{name: "checks running, just polled", prs: []model.PR{openPR(model.CheckPending)}, lastPoll: recent, want: false},
		{name: "one failed, one running: keep polling", prs: []model.PR{openPR(model.CheckFail, model.CheckPending)}, lastPoll: old, want: true},
		{name: "all done: no polling", prs: []model.PR{openPR(model.CheckPass, model.CheckFail)}, lastPoll: old, want: false},
		{name: "no PRs: no polling", lastPoll: old, want: false},
		{name: "just pushed, done checks: settle", prs: []model.PR{openPR(model.CheckPass)}, settleUntil: now.Add(time.Minute), want: true},
		{name: "settle over", prs: []model.PR{openPR(model.CheckPass)}, lastPoll: old, settleUntil: now.Add(-time.Second), want: false},
		{name: "merged PR pending checks ignored", prs: []model.PR{{Status: model.PRStatusMerged, Checks: []model.Check{{State: model.CheckPending}}}}, lastPoll: old, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldPoll(tt.prs, now, tt.lastPoll, tt.settleUntil); got != tt.want {
				t.Errorf("shouldPoll = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPushed(t *testing.T) {
	tests := []struct {
		name          string
		before, after map[string]string
		want          bool
	}{
		{name: "same", before: map[string]string{"a": "1"}, after: map[string]string{"a": "1"}, want: false},
		{name: "moved", before: map[string]string{"a": "1"}, after: map[string]string{"a": "2"}, want: true},
		{name: "first push", before: map[string]string{"a": ""}, after: map[string]string{"a": "1"}, want: true},
		{name: "newly watched ref", before: map[string]string{"a": "1"}, after: map[string]string{"a": "1", "b": "9"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pushed(tt.before, tt.after); got != tt.want {
				t.Errorf("pushed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWatchedRefs(t *testing.T) {
	prs := []model.PR{
		{Repo: "argocd", Status: model.PRStatusOpen, Head: "feat"},
		{Repo: "argocd", Status: model.PRStatusOpen, Head: "feat-2"},
		{Repo: "tf", Status: model.PRStatusMerged, Head: "old"},
	}
	got := watchedRefs("feat", []string{"argocd", "tf"}, prs)
	want := map[string][]string{
		"argocd": {"refs/remotes/origin/feat", "refs/remotes/origin/feat-2"},
		"tf":     {"refs/remotes/origin/feat"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("watchedRefs = %v, want %v", got, want)
	}
}

func TestRenderWatch(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	v := watchView{
		name:  "webapp",
		repos: []string{"argocd", "core", "tf"},
		state: feature.State{PRsRefreshedAt: now.Add(-12 * time.Second), PRs: []model.PR{
			{Repo: "argocd", Number: 412, Title: "Add ACK app", Status: model.PRStatusOpen, Checks: []model.Check{
				{Name: "Test / lint", State: model.CheckPass},
				{Name: "Test / build", State: model.CheckFail, URL: "https://ci/1"},
				{Name: "Diff / build", State: model.CheckPending},
				{Name: "Test / fmt", State: model.CheckPass},
				{Name: "deploy", State: model.CheckSkipped},
			}},
			{Repo: "tf", Number: 88, Title: "ACK IAM", Status: model.PRStatusMerged},
		}},
		errs: map[string]error{"core": errors.New("rate limited")},
		mode: modeRunning,
		now:  now,
	}
	want := `webapp · PRs · updated 12s ago
checks running: refreshing every minute · r refresh · f fix CI · q quit

argocd
  #412 open  Add ACK app
    ✗ Test / build  https://ci/1
    ⋯ Diff / build
    ✓ 2 passed · 1 skipped

core
  gh: rate limited
  no PR

tf
  #88 merged  ACK IAM

`
	if got := ansi.ReplaceAllString(renderWatch(v), ""); got != want {
		t.Errorf("renderWatch:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(renderWatch(watchView{repos: []string{"a"}, state: feature.State{PRs: []model.PR{{Repo: "a", Status: model.PRStatusOpen}}}}), "no checks yet") {
		t.Error("open PR without checks should say so")
	}
}
