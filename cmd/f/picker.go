package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/herdr"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/tracing"
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

var ciStyle = map[model.CIState]func(string) string{
	model.CINone:    dim,
	model.CIPassing: green,
	model.CIPending: func(s string) string { return color("36", s) }, // cyan
	model.CIFailing: red,
}

type pickerRow struct {
	name    string
	current bool // the workspace you're in (under the popup)
	status  featureStatus
	prs     []model.PR
	plan    model.Plan
}

// pickerRows gathers each feature's pi status and whether it's the current
// workspace (from Herdr, if available) and saved PRs and plan, ordered by what
// needs attention.
func pickerRows(ctx context.Context, d deps) []pickerRow {
	// If Herdr isn't up these fail: every feature reads "closed", none current.
	var agents map[string]herdr.AgentStatus
	current := ""
	if !d.noHerdr {
		agents, _ = herdr.AgentStatuses(ctx, d.run)
		current, _ = herdr.FocusedWorkspace(ctx, d.run)
	}
	var rows []pickerRow
	for _, name := range feature.List(d.layout) {
		s, running := agents[feature.Dir(d.layout, name)]
		state, _ := feature.LoadState(d.layout, name)
		rows = append(rows, pickerRow{
			name: name, current: name == current, status: toFeatureStatus(s, running),
			prs: state.AllPRs(), plan: state.Plan,
		})
	}
	// The workspace you're in first (where you are), then what needs you.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].current != rows[j].current {
			return rows[i].current
		}
		return attention(rows[i]) < attention(rows[j])
	})
	return rows
}

// attention ranks a row: lower needs you sooner. Failing CI counts as much as
// pi asking you something.
func attention(r pickerRow) int {
	rank := statusRank[r.status]
	if state, _ := model.CI(r.prs); state == model.CIFailing {
		rank = min(rank, statusRank[statusNeedsYou])
	}
	return rank
}

// pickFeature is the control center: features ordered by what needs you.
// ENTER returns the chosen feature; ctrl-x removes one, then the picker
// reopens with a status line.
func pickFeature(ctx context.Context, d deps) (string, error) {
	msg := ""
	var removed []string
	// Closing a workspace can move Herdr's focus and take the popup with it.
	defer func() {
		if len(removed) > 0 && !d.noHerdr {
			startDetached(ctx, append([]string{"close-workspaces"}, removed...)...)
		}
	}()
	for {
		rows := pickerRows(ctx, d)
		if len(rows) == 0 {
			if msg != "" {
				fmt.Println(msg)
			}
			return "", nil
		}
		titles, lines := pickerTable(rows)
		header := []string{dim("⏎ open · ctrl-x remove"), "", titles}
		if msg != "" {
			header = append([]string{msg}, header...)
		}
		// Full-screen (fast in Herdr's popup); ctrl-space closes it, like it opened.
		// The cursor starts on the top row: the feature you're in.
		_, wait := tracing.Start(ctx, "picker wait") // you choosing, not f working
		out, err := runFzf(lines, "--ansi", "--delimiter=\t", "--with-nth=2..",
			"--expect=ctrl-x", "--bind=ctrl-space:abort", "--prompt=feature> ",
			"--header="+strings.Join(header, "\n"))
		wait.End()
		if err != nil || len(out) == 0 {
			return "", err
		}
		key, name := out[0], ""
		if len(out) > 1 {
			name, _, _ = strings.Cut(out[1], "\t")
		}
		switch key {
		case "":
			return name, nil
		case "ctrl-x":
			if name != "" {
				msg = removeFeature(ctx, d, name)
				removed = append(removed, name)
			}
		}
	}
}

// pickerColumn is a column of the picker table, in display order.
type pickerColumn int

const (
	colFeature pickerColumn = iota
	colPi
	colCI
	colPRs
	colPlan
	numColumns
)

var columnTitle = [numColumns]string{colFeature: "FEATURE", colPi: "PI", colCI: "CI", colPRs: "PRS", colPlan: "PLAN"}

const (
	currentMark = "● " // marks the workspace you're in
	noValue     = "—"
)

// pickerTable renders rows as aligned columns: the title line (for fzf's
// header) and one line per row, each prefixed with the feature name and a tab
// (hidden by --with-nth; it's what the picker returns).
func pickerTable(rows []pickerRow) (string, []string) {
	cells := make([][numColumns]string, len(rows))
	var width [numColumns]int
	for c, t := range columnTitle {
		width[c] = len(t)
	}
	for i, r := range rows {
		cells[i] = [numColumns]string{
			colFeature: r.name,
			colPi:      string(r.status),
			colCI:      orNone(model.CISummary(r.prs)),
			colPRs:     orNone(model.PRSummary(r.prs)),
			colPlan:    orNone(model.PlanSummary(r.plan)),
		}
		for c, s := range cells[i] {
			width[c] = max(width[c], utf8.RuneCountInString(s))
		}
	}
	// pad pads before styling, so escape codes don't break alignment; the last
	// column isn't padded.
	pad := func(c pickerColumn, s string) string {
		if c == numColumns-1 {
			return s
		}
		return s + strings.Repeat(" ", width[c]-utf8.RuneCountInString(s)+2)
	}

	var titles strings.Builder
	titles.WriteString("  ")
	for c, t := range columnTitle {
		titles.WriteString(pad(pickerColumn(c), t))
	}

	lines := make([]string, len(rows))
	for i, r := range rows {
		mark := "  "
		if r.current {
			mark = green(currentMark)
		}
		ci, _ := model.CI(r.prs)
		style := [numColumns]func(string) string{
			colFeature: bold,
			colPi:      statusStyle[r.status],
			colCI:      ciStyle[ci],
			colPRs:     func(s string) string { return s },
			colPlan:    dim,
		}
		var b strings.Builder
		b.WriteString(r.name + "\t" + mark)
		for c, s := range cells[i] {
			if s == noValue {
				b.WriteString(dim(pad(pickerColumn(c), s)))
			} else {
				b.WriteString(style[c](pad(pickerColumn(c), s)))
			}
		}
		lines[i] = b.String()
	}
	return dim(titles.String()), lines
}

func orNone(s string) string {
	if s == "" {
		return noValue
	}
	return s
}

// removeFeature moves a feature to the trash without asking (f restore brings
// it back); `f finish-remove` does the slow rest detached. Returns a status line.
func removeFeature(ctx context.Context, d deps, name string) string {
	args := finishRemoveArgs(d, name) // reads the feature's files before they move
	dir, err := feature.Remove(d.layout, name)
	if err != nil {
		return red(err.Error())
	}
	startDetached(ctx, append(args, "--dir", dir)...)
	return green("removed "+name) + dim(" (f restore "+name+" within 14 days)")
}
