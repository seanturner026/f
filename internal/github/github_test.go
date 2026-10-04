package github_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/seanturner026/pi/internal/github"
	"github.com/seanturner026/pi/internal/model"
)

type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(context.Context, string, string, ...string) (string, error) {
	return f.out, f.err
}

func TestListPRs(t *testing.T) {
	tests := []struct {
		name    string
		runner  fakeRunner
		want    []model.PR
		wantErr bool
	}{
		{
			name:   "no PRs",
			runner: fakeRunner{out: "[]"},
			want:   []model.PR{},
		},
		{
			name: "maps state and draft to status",
			runner: fakeRunner{out: `[
				{"number":1,"state":"OPEN","title":"a","url":"u1","isDraft":true,"headRefOid":"h1"},
				{"number":2,"state":"MERGED","title":"b","url":"u2","isDraft":false,"headRefOid":"h2"}]`},
			want: []model.PR{
				{Repo: "argocd", Number: 1, Title: "a", URL: "u1", Status: model.PRStatusDraft, HeadOID: "h1"},
				{Repo: "argocd", Number: 2, Title: "b", URL: "u2", Status: model.PRStatusMerged, HeadOID: "h2"},
			},
		},
		{name: "gh fails", runner: fakeRunner{err: errors.New("boom")}, wantErr: true},
		{name: "bad json", runner: fakeRunner{out: "nope"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := github.ListPRs(context.Background(), tt.runner, "/wt", "argocd", "feat")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// cmdRunner answers by the gh subcommand ("stack", "pr list", "pr view <url>").
type cmdRunner map[string]string

func (f cmdRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	key := args[0]
	switch {
	case args[0] == "pr" && args[1] == "view":
		key = "pr view " + args[2]
	case args[0] == "pr":
		key = "pr " + args[1]
	}
	out, ok := f[key]
	if !ok || out == "ERR" {
		return "", errors.New("fail: " + key)
	}
	return out, nil
}

func TestRepoPRs(t *testing.T) {
	stack := `{"trunk":"main","branches":[
		{"name":"auth","pr":{"number":41,"url":"u41","state":"MERGED"}},
		{"name":"api","pr":{"number":42,"url":"u42","state":"OPEN"}},
		{"name":"wip"}]}`
	tests := []struct {
		name    string
		runner  cmdRunner
		want    []model.PR
		wantErr bool
	}{
		{
			name: "stack: PRs bottom first with positions and titles",
			runner: cmdRunner{
				"stack":       stack,
				"pr view u41": `{"number":41,"state":"MERGED","title":"auth","url":"u41","headRefName":"auth","baseRefName":"main"}`,
				"pr view u42": `{"number":42,"state":"OPEN","title":"api","url":"u42","isDraft":true,"headRefName":"api","baseRefName":"auth"}`,
			},
			want: []model.PR{
				{Repo: "r", Number: 41, Title: "auth", URL: "u41", Status: model.PRStatusMerged, Head: "auth", Base: "main", Stack: 1},
				{Repo: "r", Number: 42, Title: "api", URL: "u42", Status: model.PRStatusDraft, Head: "api", Base: "auth", Stack: 2},
			},
		},
		{
			name:   "no stack: falls back to the feature branch",
			runner: cmdRunner{"stack": "ERR", "pr list": `[{"number":7,"state":"OPEN","title":"t","url":"u7","headRefName":"feat","baseRefName":"main"}]`},
			want:   []model.PR{{Repo: "r", Number: 7, Title: "t", URL: "u7", Status: model.PRStatusOpen, Head: "feat", Base: "main"}},
		},
		{
			name:    "stack PR lookup fails",
			runner:  cmdRunner{"stack": stack, "pr view u41": "ERR", "pr view u42": "ERR"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := github.RepoPRs(context.Background(), tt.runner, "/wt", "r", "feat")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
