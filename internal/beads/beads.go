// Package beads wraps the bd CLI (beads issue tracker). Commands run in the
// directory holding .beads/ (f's base dir), so every feature shares one db.
package beads

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

// Note appends a note to an issue.
func Note(ctx context.Context, r Runner, dir, id, text string) error {
	if _, err := r.Run(ctx, dir, "bd", "note", id, text); err != nil {
		return fmt.Errorf("bd note %s: %w", id, err)
	}
	return nil
}

// Progress summarizes an issue's children: how many are closed, and the first
// one that's ready to work on (no open blockers).
func Progress(ctx context.Context, r Runner, dir, id string) (model.Plan, error) {
	out, err := r.Run(ctx, dir, "bd", "children", id, "--json")
	if err != nil {
		return model.Plan{}, fmt.Errorf("bd children %s: %w", id, err)
	}
	children, err := decodeIssues(out)
	if err != nil {
		return model.Plan{}, err
	}
	plan := model.Plan{Issue: id, Total: len(children)}
	for _, c := range children {
		if c.Status == "closed" {
			plan.Done++
		}
	}
	if plan.Done < plan.Total {
		out, err := r.Run(ctx, dir, "bd", "ready", "--parent", id, "--json", "--limit", "1")
		if err != nil {
			return model.Plan{}, fmt.Errorf("bd ready %s: %w", id, err)
		}
		if ready, err := decodeIssues(out); err == nil && len(ready) > 0 {
			plan.Next = ready[0].Title
		}
	}
	return plan, nil
}

// Steps lists the issue's child steps with what each is blocked by (its
// "blocks" dependencies), in bd's order.
func Steps(ctx context.Context, r Runner, dir, id string) ([]model.Step, error) {
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
		s := model.Step{ID: c.ID, Title: c.Title, Status: model.StepStatus(c.Status)}
		for _, d := range c.Dependencies {
			if d.Type == "blocks" {
				s.BlockedBy = append(s.BlockedBy, d.DependsOn)
			}
		}
		steps = append(steps, s)
	}
	return steps, nil
}
