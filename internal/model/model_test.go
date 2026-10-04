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
			name: "stack drawn as tree, other repo flat",
			prs: []model.PR{
				{Repo: "a", URL: "u1", Title: "bottom", Stack: 1},
				{Repo: "a", URL: "u2", Title: "middle", Stack: 2},
				{Repo: "a", URL: "u3", Title: "top", Stack: 3},
				{Repo: "b", URL: "u4", Title: "flat"},
			},
			want: "u1: bottom\n├── u2: middle\n└── u3: top\nu4: flat\n",
		},
		{
			name: "merged bottom filtered out: tree starts at lowest present",
			prs:  []model.PR{{Repo: "a", URL: "u2", Title: "middle", Stack: 2}, {Repo: "a", URL: "u3", Title: "top", Stack: 3}},
			want: "u2: middle\n└── u3: top\n",
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
