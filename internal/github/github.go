// Package github looks up pull requests with the gh CLI.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/seanturner026/pi/internal/model"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// ErrNotInStack means the checkout isn't on a gh stack branch.
var ErrNotInStack = errors.New("not in a gh stack")

const prFields = "number,state,title,url,isDraft,headRefOid,headRefName,baseRefName,statusCheckRollup"

type ghPR struct {
	Number      int       `json:"number"`
	State       string    `json:"state"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	IsDraft     bool      `json:"isDraft"`
	HeadRefOid  string    `json:"headRefOid"`
	HeadRefName string    `json:"headRefName"`
	BaseRefName string    `json:"baseRefName"`
	Checks      []ghCheck `json:"statusCheckRollup"`
}

// ghCheck is one entry of statusCheckRollup: a CheckRun (GitHub Actions job,
// or another app's check) or a StatusContext (commit status).
type ghCheck struct {
	Typename string `json:"__typename"`
	// CheckRun
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`     // QUEUED, IN_PROGRESS, COMPLETED, ...
	Conclusion   string `json:"conclusion"` // SUCCESS, FAILURE, SKIPPED, ...
	DetailsURL   string `json:"detailsUrl"`
	// StatusContext
	Context   string `json:"context"`
	State     string `json:"state"` // SUCCESS, FAILURE, ERROR, PENDING, EXPECTED
	TargetURL string `json:"targetUrl"`
}

func (c ghCheck) toModel() model.Check {
	if c.Typename == "StatusContext" {
		return model.Check{Name: c.Context, State: statusState(c.State), URL: c.TargetURL}
	}
	name := c.Name
	if c.WorkflowName != "" {
		name = c.WorkflowName + " / " + c.Name
	}
	return model.Check{Name: name, State: checkRunState(c.Status, c.Conclusion), URL: c.DetailsURL}
}

func checkRunState(status, conclusion string) model.CheckState {
	if status != "COMPLETED" {
		return model.CheckPending
	}
	switch conclusion {
	case "SUCCESS":
		return model.CheckPass
	case "SKIPPED", "NEUTRAL":
		return model.CheckSkipped
	}
	return model.CheckFail // FAILURE, TIMED_OUT, CANCELLED, ACTION_REQUIRED, STARTUP_FAILURE, STALE
}

func statusState(state string) model.CheckState {
	switch state {
	case "SUCCESS":
		return model.CheckPass
	case "PENDING", "EXPECTED":
		return model.CheckPending
	}
	return model.CheckFail // FAILURE, ERROR
}

func (p ghPR) toModel(repo string, stackPos int) model.PR {
	status := model.PRStatusFromGitHub(p.State, p.IsDraft)
	var checks []model.Check // only active PRs' checks matter
	for _, c := range p.Checks {
		if status.IsActive() {
			checks = append(checks, c.toModel())
		}
	}
	return model.PR{
		Checks:  checks,
		Repo:    repo,
		Number:  p.Number,
		Title:   p.Title,
		URL:     p.URL,
		Status:  status,
		HeadOID: p.HeadRefOid,
		Head:    p.HeadRefName,
		Base:    p.BaseRefName,
		Stack:   stackPos,
	}
}

// ListPRs returns all PRs (any state) whose head is branch, for the repo
// checked out in dir. Repo is set to repo on each result.
func ListPRs(ctx context.Context, r Runner, dir, repo, branch string) ([]model.PR, error) {
	out, err := r.Run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "all", "--json", prFields)
	if err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}
	var raw []ghPR
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("list PRs: decode: %w", err)
	}
	prs := make([]model.PR, 0, len(raw))
	for _, p := range raw {
		prs = append(prs, p.toModel(repo, 0))
	}
	return prs, nil
}

type stackView struct {
	Branches []struct {
		Name string `json:"name"`
		PR   *struct {
			URL string `json:"url"`
		} `json:"pr"`
	} `json:"branches"`
}

// StackPRs returns the PRs of the gh stack checked out in dir, bottom first,
// with Stack set to each PR's position. ErrNotInStack if there's no stack (or
// gh-stack isn't installed).
func StackPRs(ctx context.Context, r Runner, dir, repo string) ([]model.PR, error) {
	out, err := r.Run(ctx, dir, "gh", "stack", "view", "--json")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotInStack, err)
	}
	var view stackView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return nil, fmt.Errorf("stack view: decode: %w", err)
	}
	var urls []string
	for _, b := range view.Branches {
		if b.PR != nil && b.PR.URL != "" {
			urls = append(urls, b.PR.URL)
		}
	}
	prs := make([]model.PR, len(urls))
	errs := make([]error, len(urls))
	var wg sync.WaitGroup
	for i, url := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := r.Run(ctx, dir, "gh", "pr", "view", url, "--json", prFields)
			if err != nil {
				errs[i] = fmt.Errorf("view %s: %w", url, err)
				return
			}
			var p ghPR
			if err := json.Unmarshal([]byte(out), &p); err != nil {
				errs[i] = fmt.Errorf("view %s: decode: %w", url, err)
				return
			}
			prs[i] = p.toModel(repo, i+1)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return prs, nil
}

// RepoPRs returns the repo's stack PRs if dir is on a gh stack, otherwise the
// PRs whose head is branch.
func RepoPRs(ctx context.Context, r Runner, dir, repo, branch string) ([]model.PR, error) {
	prs, err := StackPRs(ctx, r, dir, repo)
	if errors.Is(err, ErrNotInStack) {
		return ListPRs(ctx, r, dir, repo, branch)
	}
	return prs, err
}
