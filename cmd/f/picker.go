package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
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
	plan   model.Plan
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
		rows = append(rows, pickerRow{name: name, status: toFeatureStatus(s, running), prs: state.PRs, plan: state.Plan})
	}
	sort.SliceStable(rows, func(i, j int) bool { return statusRank[rows[i].status] < statusRank[rows[j].status] })
	return rows
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
		header := "ENTER open · ctrl-x remove · alt-x clean merged · ESC close"
		if msg != "" {
			header = msg + "\n" + header
		}
		// Full-screen (fast in Herdr's popup); ctrl-space closes it, like it opened.
		out, err := runFzf(pickerLines(rows), "--ansi", "--delimiter=\t", "--with-nth=2..",
			"--expect=ctrl-x,alt-x", "--bind=ctrl-space:abort", "--prompt=feature> ", "--header="+header)
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

func pickerLines(rows []pickerRow) []string {
	width := 0
	for _, r := range rows {
		width = max(width, len(r.name))
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		status := statusStyle[r.status](fmt.Sprintf("%-9s", r.status))
		var extra []string
		if s := model.PRSummary(r.prs); s != "" {
			extra = append(extra, "PRs: "+s)
		}
		if s := model.PlanSummary(r.plan); s != "" {
			extra = append(extra, s)
		}
		// name is the first tab-separated field; that's what we return.
		lines[i] = fmt.Sprintf("%s\t%-*s  %s  %s", r.name, width, r.name, status, dim(strings.Join(extra, " · ")))
	}
	return lines
}

// removeFeature removes a feature and its Herdr workspace. If it has unmerged
// work, it lists it and asks first. Returns a status line for the picker.
func removeFeature(ctx context.Context, d deps, name string) string {
	problems, err := feature.Unmerged(ctx, d.run, d.layout, name)
	if err != nil {
		return red(fmt.Sprintf("%s: %v", name, err))
	}
	force := false
	if len(problems) > 0 {
		fmt.Printf("%s has unmerged work:\n", bold(name))
		for _, p := range problems {
			fmt.Println("  " + p)
		}
		fmt.Print(red("remove anyway? this work is lost [y/N] "))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return "kept " + name
		}
		force = true
	}
	issueNote := ""
	if force {
		keepIssueOpen(ctx, d, name, problems)
	} else {
		issueNote = closeIssue(ctx, d, name)
	}
	if err := feature.Remove(ctx, d.run, d.layout, name, force); err != nil {
		return red(err.Error())
	}
	if !d.noHerdr {
		_ = herdr.CloseWorkspace(ctx, d.run, name)
	}
	if issueNote != "" {
		return green("removed "+name) + dim(" · "+issueNote)
	}
	return green("removed " + name)
}
