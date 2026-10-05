package model_test

import (
	"testing"

	"github.com/seanturner026/pi/internal/model"
)

func TestPRStatusFromGitHub(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		isDraft bool
		want    model.PRStatus
	}{
		{name: "open", state: "OPEN", want: model.PRStatusOpen},
		{name: "draft", state: "OPEN", isDraft: true, want: model.PRStatusDraft},
		{name: "merged", state: "MERGED", want: model.PRStatusMerged},
		{name: "merged draft flag ignored", state: "MERGED", isDraft: true, want: model.PRStatusMerged},
		{name: "closed", state: "CLOSED", want: model.PRStatusClosed},
		{name: "unknown treated as open", state: "WEIRD", want: model.PRStatusOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.PRStatusFromGitHub(tt.state, tt.isDraft); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPRStatusIsActive(t *testing.T) {
	tests := []struct {
		status model.PRStatus
		want   bool
	}{
		{model.PRStatusOpen, true},
		{model.PRStatusDraft, true},
		{model.PRStatusMerged, false},
		{model.PRStatusClosed, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsActive(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPRSummary(t *testing.T) {
	tests := []struct {
		name string
		prs  []model.PR
		want string
	}{
		{name: "none", want: ""},
		{name: "ordered by status", prs: []model.PR{{Status: model.PRStatusMerged}, {Status: model.PRStatusOpen}, {Status: model.PRStatusMerged}}, want: "1 open · 2 merged"},
		{name: "draft and closed", prs: []model.PR{{Status: model.PRStatusClosed}, {Status: model.PRStatusDraft}}, want: "1 draft · 1 closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.PRSummary(tt.prs); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPRClipboard(t *testing.T) {
	tests := []struct {
		name string
		prs  []model.PR
		want string
	}{
		{name: "none", want: ""},
		{
			name: "one per line",
			prs:  []model.PR{{URL: "https://gh/a/pull/1", Title: "Add ACK"}, {URL: "https://gh/b/pull/2", Title: "Add IRSA"}},
			want: "https://gh/a/pull/1: Add ACK\nhttps://gh/b/pull/2: Add IRSA\n",
		},
		{
			name: "stack labelled by position, other repo flat",
			prs: []model.PR{
				{Repo: "a", URL: "u1", Title: "bottom", Stack: 1},
				{Repo: "a", URL: "u2", Title: "middle", Stack: 2},
				{Repo: "a", URL: "u3", Title: "top", Stack: 3},
				{Repo: "b", URL: "u4", Title: "flat"},
			},
			want: "1/3 u1: bottom\n2/3 u2: middle\n3/3 u3: top\nu4: flat\n",
		},
		{
			name: "merged bottom filtered out: numbered among what's left",
			prs:  []model.PR{{Repo: "a", URL: "u2", Title: "middle", Stack: 2}, {Repo: "a", URL: "u3", Title: "top", Stack: 3}},
			want: "1/2 u2: middle\n2/2 u3: top\n",
		},
		{
			name: "stack of one: no label",
			prs:  []model.PR{{Repo: "a", URL: "u1", Title: "only", Stack: 1}},
			want: "u1: only\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.PRClipboard(tt.prs); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommentsMessage(t *testing.T) {
	tests := []struct {
		name     string
		comments []model.Comment
		want     string
	}{
		{
			name:     "single line",
			comments: []model.Comment{{File: "a/x.go", Line1: 3, Line2: 3, Code: "x := 1", Lang: "go", Note: "rename x"}},
			want:     "Review comments:\n\n1. a/x.go:3\n```go\nx := 1\n```\n→ rename x\n",
		},
		{
			name: "range and numbering",
			comments: []model.Comment{
				{File: "a/x.go", Line1: 1, Line2: 1, Code: "a", Note: "n1"},
				{File: "b/y.tf", Line1: 2, Line2: 4, Code: "b\nc\nd", Lang: "terraform", Note: "n2"},
			},
			want: "Review comments:\n\n1. a/x.go:1\n```\na\n```\n→ n1\n\n2. b/y.tf:2-4\n```terraform\nb\nc\nd\n```\n→ n2\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.CommentsMessage(tt.comments); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestPlanSummary(t *testing.T) {
	tests := []struct {
		name string
		plan model.Plan
		want string
	}{
		{name: "no children", plan: model.Plan{Issue: "f-1"}, want: ""},
		{name: "with next", plan: model.Plan{Total: 5, Done: 3, Next: "upgrade webapp"}, want: "3/5 done · next: upgrade webapp"},
		{name: "all done", plan: model.Plan{Total: 2, Done: 2}, want: "2/2 done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.PlanSummary(tt.plan); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCISummary(t *testing.T) {
	pass := model.Check{Name: "lint", State: model.CheckPass}
	fail := model.Check{Name: "build", State: model.CheckFail}
	pending := model.Check{Name: "plan", State: model.CheckPending}
	skipped := model.Check{Name: "deploy", State: model.CheckSkipped}
	pr := func(repo string, status model.PRStatus, checks ...model.Check) model.PR {
		return model.PR{Repo: repo, Status: status, Checks: checks}
	}
	tests := []struct {
		name string
		prs  []model.PR
		want string
	}{
		{name: "no PRs", want: ""},
		{name: "no checks", prs: []model.PR{pr("argocd", model.PRStatusOpen)}, want: ""},
		{name: "passing", prs: []model.PR{pr("argocd", model.PRStatusOpen, pass, skipped)}, want: "✓"},
		{name: "pending", prs: []model.PR{pr("argocd", model.PRStatusOpen, pass, pending)}, want: "⋯ running"},
		{
			name: "failing beats pending, repos listed once",
			prs: []model.PR{
				pr("argocd", model.PRStatusOpen, fail, pending),
				pr("tf", model.PRStatusDraft, fail),
				pr("argocd", model.PRStatusOpen, fail),
			},
			want: "✗ argocd, tf",
		},
		{name: "merged PRs ignored", prs: []model.PR{pr("argocd", model.PRStatusMerged, fail)}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.CISummary(tt.prs); got != tt.want {
				t.Errorf("CISummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
