// Package tuicr reads and clears the review comments tuicr saves per checkout.
// tuicr keeps one session per view (working tree, a commit range, ...).
package tuicr

import (
	"context"
	"encoding/json"
	"fmt"
)

// Runner runs a command in dir and returns its stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// Session is one saved review of a checkout.
type Session struct {
	Slug     string `json:"slug"`
	Kind     string `json:"kind"` // "local", or a PR's
	Comments int    `json:"comment_count"`
}

// Comment is a draft review comment. Path is empty for a whole-review
// comment, StartLine zero for a whole-file one.
type Comment struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Side      string `json:"side"` // "new" or "old"
	Type      string `json:"comment_type"`
	Content   string `json:"content"`
}

// Sessions lists the local review sessions of the checkout in dir.
func Sessions(ctx context.Context, r Runner, dir string) ([]Session, error) {
	out, err := r.Run(ctx, dir, "tuicr", "review", "list", "--repo", dir)
	if err != nil {
		return nil, fmt.Errorf("tuicr review list: %w", err)
	}
	var all []Session
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		return nil, fmt.Errorf("tuicr review list: decode: %w", err)
	}
	var local []Session
	for _, s := range all {
		if s.Kind == "local" {
			local = append(local, s)
		}
	}
	return local, nil
}

// Comments returns the comments of session slug in dir.
func Comments(ctx context.Context, r Runner, dir, slug string) ([]Comment, error) {
	out, err := r.Run(ctx, dir, "tuicr", "review", "comments", "--session", slug, "--repo", dir)
	if err != nil {
		return nil, fmt.Errorf("tuicr review comments: %w", err)
	}
	var cs []Comment
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		return nil, fmt.Errorf("tuicr review comments: decode: %w", err)
	}
	return cs, nil
}

// DeleteComment deletes one comment of session slug.
func DeleteComment(ctx context.Context, r Runner, dir, slug, id string) error {
	if _, err := r.Run(ctx, dir, "tuicr", "review", "delete", "--session", slug, "--comment-id", id, "--repo", dir); err != nil {
		return fmt.Errorf("tuicr review delete: %w", err)
	}
	return nil
}
