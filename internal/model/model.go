// Package model holds the domain types shared across f's packages.
package model

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
