package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/seanturner026/pi/internal/feature"
	"github.com/seanturner026/pi/internal/herdr"
	"github.com/seanturner026/pi/internal/model"
)

// featureStatus is what the picker shows for a feature's pi.
type featureStatus string

const (
	statusNeedsYou featureStatus = "needs you" // pi asked a question / wants approval
	statusDone     featureStatus = "done"      // pi finished a turn you haven't seen
	statusWorking  featureStatus = "working"
	statusIdle     featureStatus = "idle"
	statusClosed   featureStatus = "closed" // no pi running
)

// statusRank orders the picker: what needs you first.
var statusRank = map[featureStatus]int{statusNeedsYou: 0, statusDone: 1, statusWorking: 2, statusIdle: 3, statusClosed: 4}

var statusStyle = map[featureStatus]func(string) string{
	statusNeedsYou: func(s string) string { return color("33", s) }, // yellow
	statusDone:     green,
	statusWorking:  func(s string) string { return color("36", s) }, // cyan
	statusIdle:     dim,
	statusClosed:   dim,
}

func toFeatureStatus(s herdr.AgentStatus, running bool) featureStatus {
	switch {
	case !running:
		return statusClosed
	case s == herdr.AgentBlocked:
		return statusNeedsYou
	case s == herdr.AgentDone:
		return statusDone
	case s == herdr.AgentWorking:
		return statusWorking
	default:
		return statusIdle
	}
}

type pickerRow struct {
	name   string
	status featureStatus
	prs    []model.PR
}

// pickerRows gathers each feature's pi status (from Herdr, if available) and
// saved PRs, ordered by what needs attention.
func pickerRows(ctx context.Context, d deps) []pickerRow {
	// One herdr call; if Herdr isn't up it fails and every feature reads "closed".
	var agents map[string]herdr.AgentStatus
	if !d.noHerdr {
		agents, _ = herdr.AgentStatuses(ctx, d.run)
	}
	var rows []pickerRow
	for _, name := range feature.List(d.layout) {
		s, running := agents[feature.Dir(d.layout, name)]
		state, _ := feature.LoadState(d.layout, name)
		rows = append(rows, pickerRow{name: name, status: toFeatureStatus(s, running), prs: state.PRs})
	}
	sort.SliceStable(rows, func(i, j int) bool { return statusRank[rows[i].status] < statusRank[rows[j].status] })
	return rows
}

// pickFeature shows features with status and PRs; returns the chosen name.
func pickFeature(ctx context.Context, d deps) (string, error) {
	rows := pickerRows(ctx, d)
	width := 0
	for _, r := range rows {
		width = max(width, len(r.name))
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		status := statusStyle[r.status](fmt.Sprintf("%-9s", r.status))
		prs := ""
		if s := model.PRSummary(r.prs); s != "" {
			prs = dim("PRs: " + s)
		}
		// name is the first tab-separated field; that's what we return.
		lines[i] = fmt.Sprintf("%s\t%-*s  %s  %s", r.name, width, r.name, status, prs)
	}
	// Full-screen (fast in Herdr's popup); ctrl-space closes it, like it opened.
	picked, err := runFzf(lines, "--ansi", "--delimiter=\t", "--with-nth=2..",
		"--bind=ctrl-space:abort", "--prompt=feature> ", "--header=ENTER open · ESC/ctrl-space close")
	if err != nil || len(picked) == 0 {
		return "", err
	}
	name, _, _ := strings.Cut(picked[0], "\t")
	return name, nil
}
