// Package beads wraps the bd CLI (beads issue tracker). Commands run in the
// directory holding .beads/ (f's base dir), so every feature shares one db.
package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/seanturner026/f/internal/model"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// IssueType is a beads issue type f uses.
type IssueType string

const (
	TypeFeature IssueType = "feature"
	TypeTask    IssueType = "task"
)

type issue struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Parent       string `json:"parent"`
	ExternalRef  string `json:"external_ref"` // a step's PR URLs, space-separated (see LinkedPRs)
	Dependencies []struct {
		DependsOn string `json:"depends_on_id"`
		Type      string `json:"type"` // "blocks", "parent-child", ...
	} `json:"dependencies"`
}

// decodeIssues accepts bd's JSON: a list, or a single object.
func decodeIssues(out string) ([]issue, error) {
	var list []issue
	if err := json.Unmarshal([]byte(out), &list); err == nil {
		return list, nil
	}
	var one issue
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		return nil, fmt.Errorf("decode bd output: %w", err)
	}
	return []issue{one}, nil
}

// Create makes an issue and returns its ID.
func Create(ctx context.Context, r Runner, dir, title string, typ IssueType, description string) (string, error) {
	args := []string{"create", title, "--type", string(typ), "--json"}
	if description != "" {
		args = append(args, "--description", description)
	}
	out, err := r.Run(ctx, dir, "bd", args...)
	if err != nil {
		return "", fmt.Errorf("bd create: %w", err)
	}
	issues, err := decodeIssues(out)
	if err != nil || len(issues) == 0 || issues[0].ID == "" {
		return "", fmt.Errorf("bd create: no id in %q", out)
	}
	return issues[0].ID, nil
}

// SetDescription replaces an issue's description.
func SetDescription(ctx context.Context, r Runner, dir, id, description string) error {
	if _, err := r.Run(ctx, dir, "bd", "update", id, "--description", description); err != nil {
		return fmt.Errorf("bd update %s: %w", id, err)
	}
	return nil
}

// Close closes an issue with a reason.
func Close(ctx context.Context, r Runner, dir, id, reason string) error {
	if _, err := r.Run(ctx, dir, "bd", "close", id, "--reason", reason); err != nil {
		return fmt.Errorf("bd close %s: %w", id, err)
	}
	return nil
}

// Reopen reopens a closed issue.
func Reopen(ctx context.Context, r Runner, dir, id string) error {
	if _, err := r.Run(ctx, dir, "bd", "reopen", id); err != nil {
		return fmt.Errorf("bd reopen %s: %w", id, err)
	}
	return nil
}

// Note appends a note to an issue.
func Note(ctx context.Context, r Runner, dir, id, text string) error {
	if _, err := r.Run(ctx, dir, "bd", "note", id, text); err != nil {
		return fmt.Errorf("bd note %s: %w", id, err)
	}
	return nil
}

// Progress summarizes an issue's children (model.PlanOf): how many are
// closed, and the first that's ready to work on.
func Progress(ctx context.Context, r Runner, dir, id string) (model.Plan, error) {
	steps, err := Steps(ctx, r, dir, id)
	if err != nil {
		return model.Plan{}, err
	}
	return model.PlanOf(id, steps), nil
}

// Steps lists the issue's child steps with what each is blocked by (its
// "blocks" dependencies), in bd's order.
func Steps(ctx context.Context, r Runner, dir, id string) ([]model.Step, error) {
	if snap, ok := ctx.Value(snapshotKey{}).(*snapshot); ok {
		snap.once.Do(func() { snap.byParent, snap.err = allSteps(ctx, r, dir) })
		return snap.byParent[id], snap.err
	}
	out, err := r.Run(ctx, dir, "bd", "children", id, "--json")
	if err != nil {
		return nil, fmt.Errorf("bd children %s: %w", id, err)
	}
	children, err := decodeIssues(out)
	if err != nil {
		return nil, err
	}
	steps := make([]model.Step, 0, len(children))
	for _, c := range children {
		steps = append(steps, stepOf(c))
	}
	return steps, nil
}

type snapshotKey struct{}

type snapshot struct {
	once     sync.Once
	byParent map[string][]model.Step
	err      error
}

// WithSnapshot makes Steps under ctx read one list of every issue, taken on
// first use, instead of a bd call per issue (each takes seconds): for syncing
// every feature at once. Steps closed after it was taken still show open.
func WithSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, snapshotKey{}, &snapshot{})
}

// allSteps is every issue's steps by parent ID, in bd's order.
func allSteps(ctx context.Context, r Runner, dir string) (map[string][]model.Step, error) {
	out, err := r.Run(ctx, dir, "bd", "list", "--all", "--limit", "0", "--brief", "--json")
	if err != nil {
		return nil, fmt.Errorf("bd list: %w", err)
	}
	issues, err := decodeIssues(out)
	if err != nil {
		return nil, err
	}
	byParent := map[string][]model.Step{}
	for _, i := range issues {
		if i.Parent != "" {
			byParent[i.Parent] = append(byParent[i.Parent], stepOf(i))
		}
	}
	return byParent, nil
}

func stepOf(i issue) model.Step {
	s := model.Step{ID: i.ID, Title: i.Title, Status: model.StepStatus(i.Status), PRs: LinkedPRs(i.ExternalRef)}
	for _, d := range i.Dependencies {
		if d.Type == "blocks" {
			s.BlockedBy = append(s.BlockedBy, d.DependsOn)
		}
	}
	return s
}

// LinkedPRs parses a step's external ref: the URLs of its PRs, space-separated
// (pi links them: bd update <id> --external-ref "<url> <url>"). Anything that
// isn't a GitHub PR URL is ignored.
func LinkedPRs(ref string) []string {
	var urls []string
	for _, f := range strings.Fields(ref) {
		if strings.HasPrefix(f, "https://github.com/") && strings.Contains(f, "/pull/") {
			urls = append(urls, strings.TrimSuffix(f, "/"))
		}
	}
	return urls
}
