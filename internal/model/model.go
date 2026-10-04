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

// PRClipboard formats PRs one per line as "url: title".
func PRClipboard(prs []PR) string {
	var b strings.Builder
	for _, p := range prs {
		fmt.Fprintf(&b, "%s: %s\n", p.URL, p.Title)
	}
	return b.String()
}
