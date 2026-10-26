package feature

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"github.com/seanturner026/f/internal/git"
	"github.com/seanturner026/f/internal/github"
	"github.com/seanturner026/f/internal/model"
	"github.com/seanturner026/f/internal/tracing"
)

// SyncAction is what Reconcile did to a repo after a PR merged.
type SyncAction string

const (
	SyncNothing SyncAction = ""           // nothing merged that isn't already behind the default branch
	SyncDone    SyncAction = "synced"     // branches reset, deleted or rebased: see Branches
	SyncNeeded  SyncAction = "needs sync" // couldn't do it safely (uncommitted changes, conflicts): see Detail
)

// BranchAction is what Reconcile did to one branch of a repo's stack.
type BranchAction string

const (
	BranchReset   BranchAction = "reset"   // merged, nothing new: now at the default branch
	BranchDeleted BranchAction = "deleted" // merged, nothing new, not the feature branch: local branch deleted
	BranchKept    BranchAction = "kept"    // merged, then committed on: just the new commits replayed onto the default branch
	BranchRebased BranchAction = "rebased" // not merged: replayed onto what's left below it
)

// BranchSync is Reconcile's outcome for one branch.
type BranchSync struct {
	Branch     string
	Action     BranchAction
	PR         int  // its merged PR (0 if not merged)
	New        int  // BranchKept: commits added after the merge
	Pushed     bool // force-pushed (--force-with-lease)
	Retargeted int  // open PR whose base was moved to the default branch (0 if none)
}

// SyncResult is Reconcile's outcome for one repo.
type SyncResult struct {
	Repo     string
	Action   SyncAction
	Base     string // e.g. "origin/main"
	Branches []BranchSync
	Detail   string // SyncNeeded: why; SyncDone: what went wrong after the branches moved ("" if nothing)
}

// Reconcile brings each repo's stack (or single feature branch) back onto its
// default branch after PRs merged, so the next PR starts fresh. Merged is
// decided by content, not ancestry: a squash merge puts a new commit on main,
// so the branch's own commits never become its ancestors (see git.InBase).
// Per branch, bottom first:
//
//   - merged, nothing after: the feature branch is reset to origin/<default>,
//     other branches are deleted
//   - merged, then committed on: only the new commits are replayed (kept)
//   - not merged: replayed onto what's left below it, if that changed (rebased)
//
// Uncommitted changes or a conflict leave every branch as it was (SyncNeeded).
//
// prs are the feature's PRs (refresh them first). With push, rebased branches
// that exist on origin are force-pushed (--force-with-lease), open PRs based
// on a merged branch are moved to the default branch, and the gh stack is
// re-initialized without the deleted branches. Without push (background
// syncs), stacks of two or more branches are left alone.
func Reconcile(ctx context.Context, r Runner, l Layout, name string, prs []model.PR, push bool) ([]SyncResult, error) {
	repos, err := Repos(l, name)
	if err != nil {
		return nil, err
	}
	// Repos are independent: reconcile them concurrently, report in order.
	found := make([]SyncResult, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		if !hasMergedPR(prs, repo) {
			continue // nothing merged here: nothing to do (and no gh call)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, span := tracing.Start(ctx, "reconcile repo", attribute.String("repo", repo))
			found[i] = reconcileRepo(ctx, r, l, name, repo, prs, push)
			span.SetAttributes(attribute.String("sync.action", string(found[i].Action)))
			span.End()
		}()
	}
	wg.Wait()
	var results []SyncResult
	for _, res := range found {
		if res.Action != SyncNothing {
			results = append(results, res)
		}
	}
	return results, nil
}

// stackBranch is one branch's plan.
type stackBranch struct {
	name   string
	old    string    // its commit now
	merged *model.PR // its merged PR whose content is in main (nil if none)
	fresh  []string  // commits to replay (not in main)
}

func reconcileRepo(ctx context.Context, r Runner, l Layout, name, repo string, prs []model.PR, push bool) SyncResult {
	wt := RepoDir(l, name, repo)
	res := SyncResult{Repo: repo, Base: baseRef(ctx, r, l, repo)}
	names, err := github.StackBranches(ctx, r, wt)
	if err != nil || len(names) == 0 {
		names = []string{name}
	}
	if len(names) > 1 && !push {
		return res // background: rewriting a stack means pushing it
	}
	if err := fetchRepo(ctx, r, repo, wt); err != nil {
		res.Action, res.Detail = SyncNeeded, err.Error()
		return res
	}
	baseSHA := git.RevParse(ctx, r, wt, res.Base)
	if baseSHA == "" {
		return res
	}
	refs := []string{res.Base}
	for _, p := range prs {
		if p.Repo == repo && p.MergeOID != "" && git.IsAncestor(ctx, r, wt, p.MergeOID, baseSHA) {
			refs = append(refs, p.MergeOID)
		}
	}

	// Plan, without touching anything.
	var stack []stackBranch
	var missed []string // merged PRs whose commits are on a branch but not recognized in base
	changes, below := false, res.Base
	for _, b := range names {
		sb := stackBranch{name: b, old: git.RevParse(ctx, r, wt, "refs/heads/"+b)}
		if sb.old == "" {
			continue // in the stack's records, not a local branch (any more)
		}
		own, err := git.Commits(ctx, r, wt, sb.old, res.Base, below)
		if err != nil {
			res.Action, res.Detail = SyncNeeded, err.Error()
			return res
		}
		below = sb.old
		sb.fresh = own
		if merged := mergedPRs(prs, repo, name, b); len(merged) > 0 {
			if k := git.MergedPrefix(ctx, r, wt, refs, own); k > 0 {
				sb.merged, sb.fresh, changes = merged[0], own[k:], true
			} else if i := slices.IndexFunc(merged, func(p *model.PR) bool { return p.HeadOID == sb.old }); i >= 0 && len(own) == 0 && sb.old != baseSHA {
				sb.merged, changes = merged[i], true // merged with a merge commit: behind main, catch up
			} else {
				missed = append(missed, unrecognized(ctx, r, wt, res.Base, baseSHA, sb.old, merged)...)
			}
		}
		stack = append(stack, sb)
	}
	if !changes {
		if len(missed) > 0 {
			res.Action, res.Detail = SyncNeeded, strings.Join(missed, "; ")
		}
		return res
	}
	if n, err := git.ChangedFiles(ctx, r, wt); err != nil || n > 0 {
		res.Action, res.Detail = SyncNeeded, fmt.Sprintf("%d uncommitted change(s); commit or stash, then f sync", n)
		return res
	}

	// Build the new tips on a detached HEAD; branches move only if all of it worked.
	orig := git.CurrentBranch(ctx, r, wt)
	origHead, _ := git.Head(ctx, r, wt)
	restore := func() { _ = git.Checkout(ctx, r, wt, cmp.Or(orig, origHead), orig == "") }
	if err := git.Checkout(ctx, r, wt, baseSHA, true); err != nil {
		res.Action, res.Detail = SyncNeeded, err.Error()
		return res
	}
	tips := make([]string, len(stack))
	tip, moved := baseSHA, false // moved: something below the current branch changed
	for i, sb := range stack {
		switch {
		case sb.merged == nil && !moved:
			tip = sb.old // untouched, and nothing below it changed
			if err := git.Checkout(ctx, r, wt, tip, true); err != nil {
				restore()
				res.Action, res.Detail = SyncNeeded, err.Error()
				return res
			}
		case len(sb.fresh) > 0:
			if err := git.CherryPick(ctx, r, wt, sb.fresh...); err != nil {
				restore()
				res.Action, res.Detail = SyncNeeded, fmt.Sprintf("replaying %d commit(s) of %s onto %s conflicts; rebase by hand", len(sb.fresh), sb.name, res.Base)
				return res
			}
			tip, _ = git.Head(ctx, r, wt)
		}
		tips[i] = tip
		moved = moved || tip != sb.old
	}

	// Move the branches. Detached HEAD, clean tree: nothing is checked out under them.
	var problems []string
	var kept []string
	for i, sb := range stack {
		bs := BranchSync{Branch: sb.name}
		if sb.merged != nil {
			bs.PR = sb.merged.Number
		}
		switch {
		case sb.merged != nil && len(sb.fresh) == 0 && sb.name != name:
			bs.Action = BranchDeleted
			if err := git.DeleteLocalBranch(ctx, r, wt, sb.name, sb.old); err != nil {
				problems = append(problems, err.Error())
				kept = append(kept, sb.name)
				continue
			}
		case tips[i] == sb.old:
			kept = append(kept, sb.name)
			continue // untouched
		default:
			bs.Action = BranchRebased
			if sb.merged != nil {
				bs.Action, bs.New = BranchKept, len(sb.fresh)
				if len(sb.fresh) == 0 {
					bs.Action = BranchReset
				}
			}
			kept = append(kept, sb.name)
			if err := git.SetBranch(ctx, r, wt, sb.name, tips[i], sb.old); err != nil {
				problems = append(problems, err.Error())
				continue
			}
		}
		res.Branches = append(res.Branches, bs)
	}
	if !slices.Contains(kept, orig) {
		orig = name
	}
	if err := git.Checkout(ctx, r, wt, orig, false); err != nil {
		problems = append(problems, err.Error())
	}
	git.DeleteRef(ctx, r, wt, LastRunRef) // "since pi's last run" would compare against pre-merge code

	if push {
		problems = append(problems, publish(ctx, r, wt, name, repo, prs, stack, &res, kept)...)
	}
	res.Action, res.Detail = SyncDone, strings.Join(append(problems, missed...), "; ")
	return res
}

// unrecognized describes merged PRs whose head is still on branch (at tip) but
// not in base, though no commit of it was recognized as merged by content.
func unrecognized(ctx context.Context, r Runner, wt, base, baseSHA, tip string, merged []*model.PR) []string {
	var out []string
	for _, p := range merged {
		if p.HeadOID != "" && git.IsAncestor(ctx, r, wt, p.HeadOID, tip) && !git.IsAncestor(ctx, r, wt, p.HeadOID, baseSHA) {
			out = append(out, fmt.Sprintf("#%d merged, but its commits aren't recognized as in %s; reset the branch by hand", p.Number, base))
		}
	}
	return out
}

// publish pushes rebased branches, moves open PRs off merged bases, and
// re-records the stack (kept: the branches left, bottom first). Returns what failed.
func publish(ctx context.Context, r Runner, wt, name, repo string, prs []model.PR, stack []stackBranch, res *SyncResult, kept []string) []string {
	var problems []string
	mergedBase := map[string]bool{}
	for _, sb := range stack {
		if sb.merged != nil {
			mergedBase[sb.name] = true
		}
	}
	defaultBranch := strings.TrimPrefix(res.Base, "origin/")
	for i, bs := range res.Branches {
		if bs.Action != BranchRebased || git.RevParse(ctx, r, wt, "refs/remotes/origin/"+bs.Branch) == "" {
			continue // kept branches' PR merged: the new commits need a new PR (gh stack submit)
		}
		if err := git.ForcePushBranch(ctx, r, wt, bs.Branch, name); err != nil {
			problems = append(problems, err.Error())
		} else {
			res.Branches[i].Pushed = true
		}
		for _, p := range prs {
			if p.Repo == repo && p.Head == bs.Branch && p.Status.IsActive() && mergedBase[p.Base] {
				if err := github.RetargetPR(ctx, r, wt, p.Number, defaultBranch); err != nil {
					problems = append(problems, err.Error())
				} else {
					res.Branches[i].Retargeted = p.Number
				}
			}
		}
	}
	if len(stack) > 1 && len(kept) < len(stack) {
		if err := github.StackReinit(ctx, r, wt, kept); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return problems
}

// mergedPRs returns branch's merged PRs. A PR without a head name (older
// state) belongs to the feature branch.
func mergedPRs(prs []model.PR, repo, name, branch string) []*model.PR {
	var out []*model.PR
	for i, p := range prs {
		if p.Repo == repo && p.Status == model.PRStatusMerged && (p.Head == branch || (p.Head == "" && branch == name)) {
			out = append(out, &prs[i])
		}
	}
	return out
}
