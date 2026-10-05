package github_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
		{
			name: "maps checks",
			runner: fakeRunner{out: `[{"number":3,"state":"OPEN","url":"u3","statusCheckRollup":[
				{"__typename":"CheckRun","workflowName":"Test","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"d1"},
				{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"d2"},
				{"__typename":"CheckRun","name":"plan","status":"IN_PROGRESS","conclusion":""},
				{"__typename":"CheckRun","name":"deploy","status":"COMPLETED","conclusion":"SKIPPED"},
				{"__typename":"StatusContext","context":"atlantis/plan","state":"ERROR","targetUrl":"t1"},
				{"__typename":"StatusContext","context":"ext","state":"PENDING"}]}]`},
			want: []model.PR{{Repo: "argocd", Number: 3, URL: "u3", Status: model.PRStatusOpen, Checks: []model.Check{
				{Name: "Test / lint", State: model.CheckPass, URL: "d1"},
				{Name: "build", State: model.CheckFail, URL: "d2"},
				{Name: "plan", State: model.CheckPending},
				{Name: "deploy", State: model.CheckSkipped},
				{Name: "atlantis/plan", State: model.CheckFail, URL: "t1"},
				{Name: "ext", State: model.CheckPending},
			}}},
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

func TestJobRef(t *testing.T) {
	repo, job, ok := github.JobRef("https://github.com/foat/argocd/actions/runs/37045276732/job/110963")
	if !ok || repo != "foat/argocd" || job != "110963" {
		t.Errorf("JobRef = %q %q %v", repo, job, ok)
	}
	if _, _, ok := github.JobRef("https://ci.example.com/build/1"); ok {
		t.Error("non-Actions URL should not parse")
	}
}

func TestLogExcerpt(t *testing.T) {
	raw := strings.Join([]string{
		"build\tUNKNOWN STEP\t2026-10-02T18:16:38.2782033Z rendering",
		"build\tUNKNOWN STEP\t2026-10-02T18:16:43.0694428Z ❌ failed to get resources",
		"build\tUNKNOWN STEP\t2026-10-02T18:16:43.1260277Z ##[error]Process completed with exit code 1.",
		"build\tUNKNOWN STEP\t2026-10-02T18:16:43.1458004Z Post job cleanup.",
		"build\tUNKNOWN STEP\t2026-10-02T18:16:43.2466156Z [command]/usr/bin/git version",
	}, "\n")
	want := "rendering\n❌ failed to get resources\n##[error]Process completed with exit code 1."
	if got := github.LogExcerpt(raw, 10); got != want {
		t.Errorf("LogExcerpt =\n%s\nwant\n%s", got, want)
	}
	want2 := "… (1 earlier lines)\n❌ failed to get resources\n##[error]Process completed with exit code 1."
	if got := github.LogExcerpt(raw, 2); got != want2 {
		t.Errorf("LogExcerpt(2) =\n%s\nwant\n%s", got, want2)
	}
	if got := github.LogExcerpt("a\nb", 10); got != "a\nb" {
		t.Errorf("no error marker: keep all, got %q", got)
	}
}

func TestRepoPRsStackWithoutPRsFallsBack(t *testing.T) {
	r := cmdRunner{
		"stack":   `{"trunk":"main","branches":[{"name":"feat"}]}`,
		"pr list": `[{"number":5,"state":"OPEN","url":"u5","headRefName":"feat"}]`,
	}
	prs, err := github.RepoPRs(context.Background(), r, "/wt", "app", "feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 5 {
		t.Errorf("prs = %+v, want #5 from pr list", prs)
	}
}
