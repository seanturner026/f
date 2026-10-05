package main

import (
	"regexp"
	"testing"

	"github.com/seanturner026/pi/internal/model"
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
}

func TestFirstOther(t *testing.T) {
	tests := []struct {
		name    string
		current []bool
		want    int
	}{
		{name: "current first", current: []bool{true, false}, want: 2},
		{name: "current later", current: []bool{false, true}, want: 1},
		{name: "only current", current: []bool{true}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rows []pickerRow
			for _, c := range tt.current {
				rows = append(rows, pickerRow{current: c})
			}
			if got := firstOther(rows); got != tt.want {
				t.Errorf("firstOther(%v) = %d, want %d", tt.current, got, tt.want)
			}
		})
	}
}
