package main

import (
	"context"
	"fmt"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/github"
	"github.com/seanturner026/f/internal/model"
)

// prsReview prints the unresolved review threads on the feature's open PRs
// as one prompt (nothing if there are none); the mod's /fprr sends it.
func prsReview(ctx context.Context, d deps, explicit string) error {
	name, err := resolve(d, explicit)
	if err != nil {
		return err
	}
	state, err := feature.LoadState(d.layout, name)
	if err != nil {
		return err
	}
	var reviews []model.PRReview
	for _, p := range state.PRs {
		if !p.Status.IsActive() {
			continue
		}
		threads, err := github.ReviewThreads(ctx, d.run, feature.RepoDir(d.layout, name, p.Repo), p)
		if err != nil {
			return err
		}
		if len(threads) > 0 {
			reviews = append(reviews, model.PRReview{PR: p, Threads: threads})
		}
	}
	if len(reviews) > 0 {
		fmt.Print(model.ReviewMessage(reviews))
	}
	return nil
}
