// Package github looks up pull requests with the gh CLI.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/seanturner026/pi/internal/model"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// ErrNotInStack means the checkout isn't on a gh stack branch.
var ErrNotInStack = errors.New("not in a gh stack")

const prFields = "number,state,title,url,isDraft,headRefOid,headRefName,baseRefName,mergeCommit,statusCheckRollup"

type ghPR struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	IsDraft     bool   `json:"isDraft"`
	HeadRefOid  string `json:"headRefOid"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	Checks []ghCheck `json:"statusCheckRollup"`
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
	merge := ""
	if p.MergeCommit != nil {
		merge = p.MergeCommit.OID
	}
	return model.PR{
		MergeOID: merge,
		Checks:   checks,
		Repo:     repo,
		Number:   p.Number,
		Title:    p.Title,
		URL:      p.URL,
		Status:   status,
		HeadOID:  p.HeadRefOid,
		Head:     p.HeadRefName,
		Base:     p.BaseRefName,
		Stack:    stackPos,
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

// viewStack runs `gh stack view` in dir. ErrNotInStack if there's no stack (or
// gh-stack isn't installed).
func viewStack(ctx context.Context, r Runner, dir string) (stackView, error) {
	out, err := r.Run(ctx, dir, "gh", "stack", "view", "--json")
	if err != nil {
		return stackView{}, fmt.Errorf("%w: %w", ErrNotInStack, err)
	}
	var view stackView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return stackView{}, fmt.Errorf("stack view: decode: %w", err)
	}
	return view, nil
}

// StackPRs returns the PRs of the gh stack checked out in dir, bottom first,
// with Stack set to each PR's position. ErrNotInStack if there's no stack (or
// gh-stack isn't installed).
func StackPRs(ctx context.Context, r Runner, dir, repo string) ([]model.PR, error) {
	view, err := viewStack(ctx, r, dir)
	if err != nil {
		return nil, err
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

// PRStatus looks up one PR's status by URL (any repo).
func PRStatus(ctx context.Context, r Runner, dir, url string) (model.PRStatus, error) {
	out, err := r.Run(ctx, dir, "gh", "pr", "view", url, "--json", "state,isDraft")
	if err != nil {
		return "", fmt.Errorf("view %s: %w", url, err)
	}
	var p struct {
		State   string `json:"state"`
		IsDraft bool   `json:"isDraft"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return "", fmt.Errorf("view %s: decode: %w", url, err)
	}
	return model.PRStatusFromGitHub(p.State, p.IsDraft), nil
}

// RepoPRs returns the repo's stack PRs if dir is on a gh stack, otherwise the
// PRs whose head is branch.
func RepoPRs(ctx context.Context, r Runner, dir, repo, branch string) ([]model.PR, error) {
	// Both lookups at once (each is a ~1s gh call); use the stack's if it has PRs.
	type res struct {
		prs []model.PR
		err error
	}
	listed := make(chan res, 1)
	go func() {
		prs, err := ListPRs(ctx, r, dir, repo, branch)
		listed <- res{prs, err}
	}()
	prs, err := StackPRs(ctx, r, dir, repo)
	if errors.Is(err, ErrNotInStack) || (err == nil && len(prs) == 0) {
		// No stack, or none of its branches submitted yet (a plain gh pr create).
		l := <-listed
		return l.prs, l.err
	}
	return prs, err
}

// StackBranches lists the branches of the gh stack checked out in dir, bottom
// first. ErrNotInStack if there's no stack.
func StackBranches(ctx context.Context, r Runner, dir string) ([]string, error) {
	view, err := viewStack(ctx, r, dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, b := range view.Branches {
		names = append(names, b.Name)
	}
	return names, nil
}

// StackInit starts a gh stack at branch (adopting it) in dir. Best effort:
// without gh-stack it fails and the repo just has no stack.
func StackInit(ctx context.Context, r Runner, dir, branch string) error {
	_, err := r.Run(ctx, dir, "gh", "stack", "init", branch)
	return err
}

// StackReinit replaces the local stack tracking with branches (bottom first),
// e.g. after merged branches were dropped. GitHub's side is left alone.
func StackReinit(ctx context.Context, r Runner, dir string, branches []string) error {
	_, _ = r.Run(ctx, dir, "gh", "stack", "unstack", "--local") // no stack: nothing to drop
	if _, err := r.Run(ctx, dir, "gh", append([]string{"stack", "init"}, branches...)...); err != nil {
		return fmt.Errorf("gh stack init: %w", err)
	}
	return nil
}

// RetargetPR changes PR number's base branch.
func RetargetPR(ctx context.Context, r Runner, dir string, number int, base string) error {
	if _, err := r.Run(ctx, dir, "gh", "pr", "edit", fmt.Sprint(number), "--base", base); err != nil {
		return fmt.Errorf("retarget #%d: %w", number, err)
	}
	return nil
}

// jobURL matches a GitHub Actions job link: .../<owner>/<repo>/actions/runs/<run>/job/<job>.
var jobURL = regexp.MustCompile(`github\.com/([^/]+/[^/]+)/actions/runs/\d+/job/(\d+)`)

// JobRef extracts "<owner>/<repo>" and the job ID from an Actions job URL.
func JobRef(url string) (repo, job string, ok bool) {
	m := jobURL.FindStringSubmatch(url)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// FailedJobLog returns the log of a failed Actions job's failed steps.
func FailedJobLog(ctx context.Context, r Runner, dir, url string) (string, error) {
	repo, job, ok := JobRef(url)
	if !ok {
		return "", fmt.Errorf("%s: not a GitHub Actions job", url)
	}
	out, err := r.Run(ctx, dir, "gh", "run", "view", "--job", job, "--log-failed", "-R", repo)
	if err != nil {
		return "", fmt.Errorf("job log: %w", err)
	}
	return out, nil
}

// logTimestamp is the timestamp Actions puts before each log message.
var logTimestamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z ?`)

const maxLogLine = 400 // characters kept per log line

// LogExcerpt trims a `gh run view --log-failed` log to what explains the
// failure: it drops the job/step columns and timestamps, cuts after the last
// "##[error]" line (what follows is post-job cleanup), and keeps the last
// maxLines lines, each at most maxLogLine characters.
func LogExcerpt(raw string, maxLines int) string {
	var lines []string
	lastError := -1
	for _, l := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if parts := strings.SplitN(l, "\t", 3); len(parts) == 3 {
			l = parts[2] // job \t step \t message
		}
		l = logTimestamp.ReplaceAllString(l, "")
		if r := []rune(l); len(r) > maxLogLine {
			l = string(r[:maxLogLine]) + "…"
		}
		if strings.Contains(l, "##[error]") {
			lastError = len(lines)
		}
		lines = append(lines, l)
	}
	if lastError >= 0 {
		lines = lines[:lastError+1]
	}
	if len(lines) > maxLines {
		lines = append([]string{fmt.Sprintf("… (%d earlier lines)", len(lines)-maxLines)}, lines[len(lines)-maxLines:]...)
	}
	return strings.Join(lines, "\n")
}
