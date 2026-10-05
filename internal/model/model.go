// Package model holds the domain types shared across f's packages.
package model

import (
	"fmt"
	"slices"
	"strings"
)

// PRStatus is the lifecycle state of a pull request.
type PRStatus string

const (
	PRStatusDraft  PRStatus = "draft"
	PRStatusOpen   PRStatus = "open"
	PRStatusMerged PRStatus = "merged"
	PRStatusClosed PRStatus = "closed"
)

// PRStatusFromGitHub maps gh's state ("OPEN", "MERGED", "CLOSED") and draft
// flag to a PRStatus. Unknown states are treated as open.
func PRStatusFromGitHub(state string, isDraft bool) PRStatus {
	switch state {
	case "MERGED":
		return PRStatusMerged
	case "CLOSED":
		return PRStatusClosed
	}
	if isDraft {
		return PRStatusDraft
	}
	return PRStatusOpen
}

// IsActive reports whether the PR still needs attention (open or draft).
func (s PRStatus) IsActive() bool { return s == PRStatusOpen || s == PRStatusDraft }

// PR is a pull request for a feature branch in one repo.
type PR struct {
	Repo    string   `json:"repo"`
	Number  int      `json:"number"`
	Title   string   `json:"title,omitempty"`
	URL     string   `json:"url"`
	Status  PRStatus `json:"status"`
	HeadOID string   `json:"head_oid,omitempty"`
	Head    string   `json:"head,omitempty"`      // head branch
	Base    string   `json:"base,omitempty"`      // base branch
	Stack   int      `json:"stack_pos,omitempty"` // 1 = bottom of a gh stack; 0 = not stacked
	Checks  []Check  `json:"checks,omitempty"`    // CI checks on the head commit
}

// CheckState is a CI check's outcome.
type CheckState string

const (
	CheckPass    CheckState = "pass"
	CheckFail    CheckState = "fail"
	CheckPending CheckState = "pending"
	CheckSkipped CheckState = "skipped"
)

// Check is one CI check (a GitHub Actions job or a commit status) on a PR.
type Check struct {
	Name  string     `json:"name"`
	State CheckState `json:"state"`
	URL   string     `json:"url,omitempty"`
}

// CIState is the combined CI state of a feature's active PRs.
type CIState string

const (
	CINone    CIState = ""        // no active PRs with checks
	CIPassing CIState = "passing" // every check passed (or was skipped)
	CIPending CIState = "pending" // some still running, none failed
	CIFailing CIState = "failing" // at least one failed
)

// CI combines the checks of the active PRs: failing beats pending beats
// passing. failing lists the repos with a failed check, in PR order.
func CI(prs []PR) (state CIState, failing []string) {
	pending, seen := false, false
	for _, p := range prs {
		if !p.Status.IsActive() {
			continue
		}
		failed := false
		for _, c := range p.Checks {
			seen = true
			switch c.State {
			case CheckFail:
				failed = true
			case CheckPending:
				pending = true
			}
		}
		if failed && !slices.Contains(failing, p.Repo) {
			failing = append(failing, p.Repo)
		}
	}
	switch {
	case len(failing) > 0:
		return CIFailing, failing
	case pending:
		return CIPending, nil
	case seen:
		return CIPassing, nil
	}
	return CINone, nil
}

// CISummary is a short CI label for the picker: "" (nothing to show), "✓",
// "⋯ running", or "✗ <failing repos>".
func CISummary(prs []PR) string {
	state, failing := CI(prs)
	switch state {
	case CIFailing:
		return "✗ " + strings.Join(failing, ", ")
	case CIPending:
		return "⋯ running"
	case CIPassing:
		return "✓"
	}
	return ""
}

// prStatusOrder is the display order for PR summaries.
var prStatusOrder = []PRStatus{PRStatusOpen, PRStatusDraft, PRStatusMerged, PRStatusClosed}

// PRSummary counts PRs by status, e.g. "1 open · 2 merged" ("" if none).
func PRSummary(prs []PR) string {
	counts := map[PRStatus]int{}
	for _, p := range prs {
		counts[p.Status]++
	}
	var parts []string
	for _, s := range prStatusOrder {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	return strings.Join(parts, " · ")
}

// StackPrefix returns the tree prefix for prs[i]: "" for unstacked PRs and the
// lowest stacked PR of its repo, "├── " for the ones above it, and "└── " for
// the last of them (as in `tree`):
//
//	url: bottom
//	├── url: middle
//	└── url: top
func StackPrefix(prs []PR, i int) string {
	p := prs[i]
	if p.Stack == 0 {
		return ""
	}
	lowest, last := p.Stack, -1
	for j, q := range prs {
		if q.Repo == p.Repo && q.Stack > 0 {
			lowest = min(lowest, q.Stack)
			last = j
		}
	}
	switch {
	case p.Stack == lowest:
		return ""
	case i == last:
		return "└── "
	default:
		return "├── "
	}
}

// PRClipboard formats PRs one per line as "url: title", stacks as a tree
// (see StackPrefix).
func PRClipboard(prs []PR) string {
	var b strings.Builder
	for i, p := range prs {
		fmt.Fprintf(&b, "%s%s: %s\n", StackPrefix(prs, i), p.URL, p.Title)
	}
	return b.String()
}

// Comment is a review comment on a line range, waiting to be sent to pi.
type Comment struct {
	ID    int    `json:"id"`
	File  string `json:"file"` // relative to the feature root, e.g. "argocd/apps/x.yaml"
	Line1 int    `json:"line1"`
	Line2 int    `json:"line2"`
	Code  string `json:"code"` // the commented lines when the comment was made
	Lang  string `json:"lang,omitempty"`
	Note  string `json:"note"`
}

// CommentsMessage formats comments as one prompt for pi.
func CommentsMessage(comments []Comment) string {
	var b strings.Builder
	b.WriteString("Review comments:\n")
	for i, c := range comments {
		lines := fmt.Sprint(c.Line1)
		if c.Line2 != c.Line1 {
			lines = fmt.Sprintf("%d-%d", c.Line1, c.Line2)
		}
		fmt.Fprintf(&b, "\n%d. %s:%s\n```%s\n%s\n```\n→ %s\n", i+1, c.File, lines, c.Lang, c.Code, c.Note)
	}
	return b.String()
}

// Plan is the progress of a feature's beads issue: its child issues.
type Plan struct {
	Issue string `json:"issue"`
	Total int    `json:"total"`
	Done  int    `json:"done"`
	Next  string `json:"next,omitempty"` // first ready child
}

// PlanSummary is e.g. "3/5 done · next: upgrade webapp" ("" without children).
func PlanSummary(p Plan) string {
	if p.Total == 0 {
		return ""
	}
	s := fmt.Sprintf("%d/%d done", p.Done, p.Total)
	if p.Next != "" {
		s += " · next: " + p.Next
	}
	return s
}
