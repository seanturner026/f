package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/seanturner026/f/internal/model"
)

func TestCollectFailures(t *testing.T) {
	prs := []model.PR{
		{Repo: "argocd", Status: model.PRStatusOpen, Checks: []model.Check{
			{Name: "lint", State: model.CheckPass},
			{Name: "build", State: model.CheckFail, URL: "https://github.com/o/argocd/actions/runs/1/job/22"},
			{Name: "atlantis/plan", State: model.CheckFail, URL: "https://atlantis/x"},
		}},
		{Repo: "tf", Status: model.PRStatusMerged, Checks: []model.Check{{Name: "old", State: model.CheckFail}}},
	}
	got := collectFailures(prs)
	if len(got) != 2 {
		t.Fatalf("got %d failures, want 2", len(got))
	}
	if got[0].repo != "o/argocd" || got[0].job != "22" {
		t.Errorf("actions job = %q %q", got[0].repo, got[0].job)
	}
	if got[1].job != "" {
		t.Errorf("non-Actions check got job %q", got[1].job)
	}
}

func TestCIPrompt(t *testing.T) {
	pr := model.PR{Repo: "argocd", Number: 412, Title: "Add ACK app", URL: "https://github.com/o/argocd/pull/412"}
	failures := []*ciFailure{
		{pr: pr, check: model.Check{Name: "Test / build", URL: "https://github.com/o/argocd/actions/runs/1/job/22"}, repo: "o/argocd", job: "22", log: "boom\n##[error]exit 1"},
		{pr: pr, check: model.Check{Name: "Diff / build", URL: "u2"}, repo: "o/argocd", job: "23", logErr: errors.New("HTTP 410")},
		{pr: pr, check: model.Check{Name: "atlantis/plan", URL: "https://atlantis/x"}},
	}
	got := ciPrompt(failures)
	for _, want := range []string{
		"Don't push until I OK it.",
		"## argocd #412 Add ACK app\nhttps://github.com/o/argocd/pull/412",
		"### ✗ Test / build",
		"`gh run view --job 22 --log-failed -R o/argocd`",
		"```\nboom\n##[error]exit 1\n```",
		"Couldn't get the log: HTTP 410",
		"Not a GitHub Actions job",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "## argocd #412") != 1 {
		t.Error("PR header should appear once")
	}
}
