// Package model holds the domain types shared across f's packages.
package model

import (
	"fmt"
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
