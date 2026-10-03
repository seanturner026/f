// Package github looks up pull requests with the gh CLI.
package github

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/seanturner026/pi/internal/model"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

type ghPR struct {
	Number     int    `json:"number"`
	State      string `json:"state"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	IsDraft    bool   `json:"isDraft"`
	HeadRefOid string `json:"headRefOid"`
}

// ListPRs returns all PRs (any state) whose head is branch, for the repo
// checked out in dir. Repo is set to repo on each result.
func ListPRs(ctx context.Context, r Runner, dir, repo, branch string) ([]model.PR, error) {
	out, err := r.Run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "all",
		"--json", "number,state,title,url,isDraft,headRefOid")
	if err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}
	var raw []ghPR
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("list PRs: decode: %w", err)
	}
	prs := make([]model.PR, 0, len(raw))
	for _, p := range raw {
		prs = append(prs, model.PR{
			Repo:    repo,
			Number:  p.Number,
			Title:   p.Title,
			URL:     p.URL,
			Status:  model.PRStatusFromGitHub(p.State, p.IsDraft),
			HeadOID: p.HeadRefOid,
		})
	}
	return prs, nil
}
