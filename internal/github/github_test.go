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
