package feature

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/seanturner026/pi/internal/git"
	"github.com/seanturner026/pi/internal/github"
	"github.com/seanturner026/pi/internal/model"
)

// SyncAction is what Reconcile did to a repo after its PR merged.
type SyncAction string

const (
	SyncNothing SyncAction = ""             // no merged PR in this branch's history, or already synced
	SyncReset   SyncAction = "reset"        // nothing new since the merged PR: branch reset to the default branch
	SyncRebased SyncAction = "rebased"      // newer commits replayed onto the default branch
	SyncNeeded  SyncAction = "needs sync"   // couldn't do it safely (uncommitted changes, conflicts): see Detail
	SyncStack   SyncAction = "stack synced" // a stack of 2+ branches: gh stack sync rebased and pushed it
)

// SyncResult is Reconcile's outcome for one repo.
type SyncResult struct {
	Repo   string
	Action SyncAction
	PR     model.PR // the merged PR it acted on
	Detail string
}

// Reconcile brings each repo whose PR merged back onto its default branch, so
// the next PR starts fresh (and the review diff stops showing merged work).
// It works for squash, merge-commit and rebase merges: a PR counts as merged
// into this branch when GitHub's head commit for it is in the branch's history.
//
//   - clean, nothing after the merged head: reset to origin/<default>
//   - commits after it: rebase just those onto origin/<default> (abort on conflict)
//   - uncommitted changes: leave it, SyncNeeded
//
// prs are the feature's PRs (refresh them first). The branch keeps its name, so
// the next push needs --force-with-lease if the old remote branch still exists.
//
// Stacks of two or more branches are left to `gh stack sync` (it rebases the
// rest of the stack and pushes), and only when stackSync is set: background
// syncs shouldn't push.
func Reconcile(ctx context.Context, r Runner, l Layout, name string, prs []model.PR, stackSync bool) ([]SyncResult, error) {
	repos, err := Repos(l, name)
	if err != nil {
		return nil, err
	}
	var results []SyncResult
	for _, repo := range repos {
		wt := filepath.Join(Dir(l, name), repo)
		if branches, err := github.StackBranches(ctx, r, wt); err == nil && len(branches) > 1 {
			if stackSync && hasMergedPR(prs, repo) {
				res := SyncResult{Repo: repo, Action: SyncStack, Detail: "gh stack sync: rebased onto main and pushed"}
				if err := github.StackSync(ctx, r, wt); err != nil {
					res.Action, res.Detail = SyncNeeded, err.Error()
				}
				results = append(results, res)
			}
			continue
		}
		res := reconcileRepo(ctx, r, l, name, repo, prs)
		if res.Action != SyncNothing {
			results = append(results, res)
		}
	}
	return results, nil
}

func reconcileRepo(ctx context.Context, r Runner, l Layout, name, repo string, prs []model.PR) SyncResult {
	wt := filepath.Join(Dir(l, name), repo)
	res := SyncResult{Repo: repo}
	head, err := git.Head(ctx, r, wt)
	if err != nil {
		return res
	}
	// The merged PR (of this branch) closest to HEAD.
	best, bestAhead := model.PR{}, -1
	for _, p := range prs {
		if p.Repo != repo || p.Status != model.PRStatusMerged || p.HeadOID == "" || (p.Head != "" && p.Head != name) {
			continue
		}
		if !git.IsAncestor(ctx, r, wt, p.HeadOID, head) {
			continue // not in this branch's history (e.g. already synced past it)
		}
		ahead, err := git.CommitsAhead(ctx, r, wt, p.HeadOID)
		if err == nil && (bestAhead < 0 || ahead < bestAhead) {
			best, bestAhead = p, ahead
		}
	}
	if bestAhead < 0 {
		return res
	}
	res.PR = best
	if err := git.Fetch(ctx, r, wt); err != nil {
		res.Action, res.Detail = SyncNeeded, err.Error()
		return res
	}
	base := "origin/" + git.DefaultBranch(ctx, r, filepath.Join(l.Base, repo))
	baseSHA := git.RevParse(ctx, r, wt, base)
	if baseSHA == "" || head == baseSHA {
		return res
	}
	if n, err := git.ChangedFiles(ctx, r, wt); err != nil || n > 0 {
		res.Action, res.Detail = SyncNeeded, fmt.Sprintf("%d uncommitted change(s); commit or stash, then f sync", n)
		return res
	}
	inMain := git.IsAncestor(ctx, r, wt, head, baseSHA)
	if bestAhead > 0 && inMain {
		return res // already synced: after a merge-commit merge, HEAD is main itself
	}
	if bestAhead == 0 {
		if err := git.ResetHard(ctx, r, wt, base); err != nil {
			res.Action, res.Detail = SyncNeeded, err.Error()
			return res
		}
		res.Action, res.Detail = SyncReset, "reset to "+base
	} else {
		if err := git.RebaseOnto(ctx, r, wt, base, best.HeadOID); err != nil {
			res.Action, res.Detail = SyncNeeded, fmt.Sprintf("rebasing %d newer commit(s) onto %s conflicts; rebase by hand", bestAhead, base)
			return res
		}
		res.Action, res.Detail = SyncRebased, fmt.Sprintf("%d newer commit(s) rebased onto %s", bestAhead, base)
	}
	git.DeleteRef(ctx, r, wt, LastRunRef) // "since pi's last run" would compare against pre-merge code
	return res
}
