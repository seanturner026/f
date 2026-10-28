package feature_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/model"
)

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		name, in string
		want     model.PR
		wantErr  bool
	}{
		{
			name: "pr url",
			in:   "https://github.com/foat/argocd/pull/2903",
			want: model.PR{Repo: "argocd", Number: 2903, URL: "https://github.com/foat/argocd/pull/2903", Status: model.PRStatusOpen},
		},
		{
			name: "trailing slash and whitespace",
			in:   " https://github.com/o/infra/pull/7/ \n",
			want: model.PR{Repo: "infra", Number: 7, URL: "https://github.com/o/infra/pull/7", Status: model.PRStatusOpen},
		},
		{name: "issue url", in: "https://github.com/o/r/issues/7", wantErr: true},
		{name: "not github", in: "https://gitlab.com/o/r/pull/7", wantErr: true},
		{name: "files tab", in: "https://github.com/o/r/pull/7/files", wantErr: true},
		{name: "not a number", in: "https://github.com/o/r/pull/x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := feature.ParsePRURL(tt.in)
			if tt.wantErr {
				if !errors.Is(err, feature.ErrBadPRURL) {
					t.Fatalf("err = %v, want ErrBadPRURL", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadStateMissing(t *testing.T) {
	l := newLayout(t, "feat", "argocd")
	s, err := feature.LoadState(l, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PRs) != 0 || !s.PRsRefreshedAt.IsZero() {
		t.Errorf("want empty state, got %+v", s)
	}
}

func TestRecordPR(t *testing.T) {
	a := model.PR{Repo: "argocd", Number: 1, URL: "u1", Status: model.PRStatusOpen}
	b := model.PR{Repo: "infra", Number: 2, URL: "u2", Status: model.PRStatusOpen}
	aMerged := model.PR{Repo: "argocd", Number: 1, URL: "u1", Status: model.PRStatusMerged}
	tests := []struct {
		name   string
		record []model.PR
		want   []model.PR
	}{
		{name: "first", record: []model.PR{a}, want: []model.PR{a}},
		{name: "second repo appended", record: []model.PR{a, b}, want: []model.PR{a, b}},
		{name: "same PR replaced", record: []model.PR{a, b, aMerged}, want: []model.PR{b, aMerged}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t, "feat", "argocd")
			for _, p := range tt.record {
				if err := feature.RecordPR(l, "feat", p); err != nil {
					t.Fatal(err)
				}
			}
			s, err := feature.LoadState(l, "feat")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.PRs, tt.want) {
				t.Errorf("got %+v, want %+v", s.PRs, tt.want)
			}
		})
	}
}

// perRepoRunner answers gh per worktree dir suffix; "ERR" fails.
type perRepoRunner map[string]string

func (f perRepoRunner) Run(_ context.Context, dir, _ string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "stack" {
		return "", errors.New("exit status 2: not in a stack")
	}
	for suffix, out := range f {
		if len(dir) >= len(suffix) && dir[len(dir)-len(suffix):] == suffix {
			if out == "ERR" {
				return "", errors.New("fail")
			}
			return out, nil
		}
	}
	return "[]", nil
}

func TestRefreshPRs(t *testing.T) {
	cached := model.PR{Repo: "infra", Number: 9, URL: "old", Status: model.PRStatusOpen}
	tests := []struct {
		name       string
		runner     perRepoRunner
		wantRepos  []string
		wantErrFor string
	}{
		{
			name:      "live replaces cache",
			runner:    perRepoRunner{"/argocd": `[{"number":1,"state":"OPEN","url":"u"}]`, "/infra": `[]`},
			wantRepos: []string{"argocd"},
		},
		{
			name:       "failed repo keeps cached PRs",
			runner:     perRepoRunner{"/argocd": `[{"number":1,"state":"OPEN","url":"u"}]`, "/infra": "ERR"},
			wantRepos:  []string{"argocd", "infra"},
			wantErrFor: "infra",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t, "feat", "argocd", "infra")
			if err := feature.RecordPR(l, "feat", cached); err != nil {
				t.Fatal(err)
			}
			s, errs, err := feature.RefreshPRs(context.Background(), tt.runner, l, "feat")
			if err != nil {
				t.Fatal(err)
			}
			var repos []string
			for _, p := range s.PRs {
				repos = append(repos, p.Repo)
			}
			if !reflect.DeepEqual(repos, tt.wantRepos) {
				t.Errorf("repos = %v, want %v", repos, tt.wantRepos)
			}
			if tt.wantErrFor != "" && errs[tt.wantErrFor] == nil {
				t.Errorf("want error for %s, got %v", tt.wantErrFor, errs)
			}
			if s.PRsRefreshedAt.IsZero() {
				t.Error("refreshed_at not set")
			}
			saved, _ := feature.LoadState(l, "feat")
			if !reflect.DeepEqual(saved.PRs, s.PRs) {
				t.Errorf("saved %+v, returned %+v", saved.PRs, s.PRs)
			}
		})
	}
}

func TestUpdateStateConcurrentWritersKeepEachOthersFields(t *testing.T) {
	l := newLayout(t, "feat", "argocd")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = feature.SavePlan(l, "feat", model.Plan{Issue: "f-1", Total: 5, Done: i % 5})
		}()
		go func() {
			defer wg.Done()
			_ = feature.RecordPR(l, "feat", model.PR{Repo: "argocd", Number: i, URL: "u"})
		}()
	}
	wg.Wait()
	s, err := feature.LoadState(l, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if s.Plan.Total != 5 {
		t.Errorf("plan lost: %+v", s.Plan)
	}
	if len(s.PRs) != 20 {
		t.Errorf("PRs = %d, want 20 (no update lost)", len(s.PRs))
	}
}

func TestRefreshPRsKeepsMergedHistory(t *testing.T) {
	l := newLayout(t, "feat", "argocd")
	merged := model.PR{Repo: "argocd", Number: 3, URL: "u3", Status: model.PRStatusMerged}
	if err := feature.RecordPR(l, "feat", merged); err != nil {
		t.Fatal(err)
	}
	// f sync dropped #3's branch: the live lookup now only finds #4.
	run := perRepoRunner{"/argocd": `[{"number":4,"state":"OPEN","url":"u4"}]`}
	for range 2 { // a second refresh mustn't add #3 again
		if _, _, err := feature.RefreshPRs(context.Background(), run, l, "feat"); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := feature.LoadState(l, "feat")
	if len(s.PRs) != 1 || s.PRs[0].Number != 4 {
		t.Errorf("PRs = %+v, want only #4 (sync decides from these)", s.PRs)
	}
	var all []int
	for _, p := range s.AllPRs() {
		all = append(all, p.Number)
	}
	if !reflect.DeepEqual(all, []int{4, 3}) {
		t.Errorf("AllPRs = %v, want [4 3]", all)
	}
}

func TestSetFlagged(t *testing.T) {
	l := newLayout(t, "feat", "argocd")
	for i, want := range []bool{true, false} {
		if got, err := feature.SetFlagged(l, "feat", false, true); err != nil || got != want {
			t.Fatalf("toggle %d = %v, %v; want %v", i, got, err, want)
		}
	}
	if _, err := feature.SetFlagged(l, "feat", true, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := feature.LoadState(l, "feat"); !s.Flagged {
		t.Error("flag not saved")
	}
}
