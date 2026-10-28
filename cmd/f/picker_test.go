package main

import (
	"regexp"
	"testing"

	"github.com/seanturner026/f/internal/model"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestPickerTable(t *testing.T) {
	rows := []pickerRow{
		{name: "install-ack", status: statusIdle, prs: []model.PR{{Status: model.PRStatusMerged}}},
		{name: "webapp", current: true, status: statusWorking, prs: []model.PR{{Repo: "argocd", Status: model.PRStatusOpen, Checks: []model.Check{{State: model.CheckFail}}}}},
	}
	titles, lines := pickerTable(rows)

	want := []string{
		"  FEATURE      PI       CI        PRS       PLAN",
		"install-ack\t  install-ack  idle     —         1 merged  —",
		"webapp\t● webapp       working  ✗ argocd  1 open    —",
	}
	got := append([]string{titles}, lines...)
	for i := range want {
		if g := ansi.ReplaceAllString(got[i], ""); g != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, g, want[i])
		}
	}
}

func TestAttention(t *testing.T) {
	failing := []model.PR{{Repo: "argocd", Status: model.PRStatusOpen, Checks: []model.Check{{State: model.CheckFail}}}}
	idleFailing := pickerRow{status: statusIdle, prs: failing}
	if got, want := attention(idleFailing), statusRank[statusNeedsYou]; got != want {
		t.Errorf("idle with failing CI: attention = %d, want %d (needs you)", got, want)
	}
	if got, want := attention(pickerRow{status: statusIdle}), statusRank[statusIdle]; got != want {
		t.Errorf("idle: attention = %d, want %d", got, want)
	}
	if got, want := attention(pickerRow{status: statusClosed, flagged: true}), statusRank[statusNeedsYou]; got != want {
		t.Errorf("closed but flagged: attention = %d, want %d (needs you)", got, want)
	}
}
