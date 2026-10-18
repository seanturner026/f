package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

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
			prs: state.PRs, plan: state.Plan,
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
// ENTER returns the chosen feature; ctrl-x removes one, alt-x cleans up all
// merged ones, then the picker reopens with a status line.
func pickFeature(ctx context.Context, d deps) (string, error) {
	msg := ""
	for {
		rows := pickerRows(ctx, d)
		if len(rows) == 0 {
			if msg != "" {
				fmt.Println(msg)
			}
			return "", nil
		}
		titles, lines := pickerTable(rows)
		header := []string{dim("⏎ open · ctrl-x remove · alt-x clean merged"), "", titles}
		if msg != "" {
			header = append([]string{msg}, header...)
		}
		// Full-screen (fast in Herdr's popup); ctrl-space closes it, like it opened.
		// The cursor starts on the first feature you're not already in.
		out, err := runFzf(lines, "--ansi", "--delimiter=\t", "--with-nth=2..",
			"--expect=ctrl-x,alt-x", "--bind=ctrl-space:abort", "--prompt=feature> ",
			"--header="+strings.Join(header, "\n"), fmt.Sprintf("--bind=load:pos(%d)", firstOther(rows)))
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
			}
		case "alt-x":
			removed := cleanMerged(ctx, d, false)
			msg = "nothing merged to clean up"
			if len(removed) > 0 {
				msg = "removed " + strings.Join(removed, ", ")
			}
		}
	}
}

// firstOther is the 1-based fzf position of the first row that isn't the
// current workspace.
func firstOther(rows []pickerRow) int {
	for i, r := range rows {
		if !r.current {
			return i + 1
		}
	}
	return 1
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

// removeFeature removes a feature and its Herdr workspace. If it has unmerged
// work, it lists it and asks first. Returns a status line for the picker.
func removeFeature(ctx context.Context, d deps, name string) string {
	// Saved PRs first (no network). Only if that finds unmerged work, check
	// GitHub live before asking: maybe a merge the saved state hasn't seen.
	state, _ := feature.LoadState(d.layout, name)
	known := state.PRs
	if known == nil {
		known = []model.PR{}
	}
	problems, err := feature.Unmerged(ctx, d.run, d.layout, name, known)
	if err == nil && len(problems) > 0 {
		problems, err = feature.Unmerged(ctx, d.run, d.layout, name, nil)
	}
	if err != nil {
		return red(fmt.Sprintf("%s: %v", name, err))
	}
	force := false
	if len(problems) > 0 {
		// Confirm inside fzf: reading the terminal directly after fzf exits is
		// unreliable in Herdr's popup.
		const keep, remove = "keep it", "remove anyway (this work is lost)"
		header := name + " has unmerged work:\n  " + strings.Join(problems, "\n  ")
		choice, err := runFzf([]string{keep, remove}, "--prompt=remove? ", "--header="+header,
			"--header-first", "--no-sort", "--bind=ctrl-space:abort")
		if err != nil || len(choice) == 0 || choice[0] != remove {
			return "kept " + name
		}
		force = true
	}
	// The beads issue is closed (or noted) in the background: bd takes ~0.5s.
	// Read what it needs now, before the feature's files are gone.
	issue := issueUpdateInBackground(d, name, force, problems)
	if err := feature.Remove(ctx, d.run, d.layout, name); err != nil {
		return red(err.Error())
	}
	if !d.noHerdr {
		_ = herdr.CloseWorkspace(ctx, d.run, name)
	}
	startDetached("empty-trash") // the files go in the background
	issue()
	return green("removed " + name)
}
