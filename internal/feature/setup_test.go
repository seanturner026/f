package feature_test

import (
	"slices"
	"testing"

	"github.com/seanturner026/f/internal/feature"
)

func TestSetup(t *testing.T) {
	l := feature.NewLayout(t.TempDir())
	setup := func() *feature.Setup { s, _ := feature.LoadState(l, "feat"); return s.Setup }

	if err := feature.BeginSetup(l, "feat", []string{"argocd", "gateway"}); err != nil {
		t.Fatal(err)
	}
	if _, err := feature.Sync(l, "feat", ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := feature.LoadInfo(l, "feat"); !slices.Equal(info.Repos, []string{"argocd", "gateway"}) {
		t.Errorf("feature.json repos = %v, want the pending ones (no worktrees yet)", info.Repos)
	}
	_ = feature.BeginSetup(l, "feat", []string{"gateway", "infra"}) // f add while it runs
	if s := setup(); !slices.Equal(s.Repos, []string{"argocd", "gateway", "infra"}) || len(s.Pending) != 3 {
		t.Fatalf("setup = %+v", s)
	}

	for _, r := range []string{"argocd", "gateway"} {
		_ = feature.FinishSetup(l, "feat", r)
	}
	_ = feature.EndSetup(l, "feat")
	if s := setup(); s == nil || !slices.Equal(s.Pending, []string{"infra"}) {
		t.Fatalf("ended with infra pending: setup = %+v", s)
	}
	_ = feature.FinishSetup(l, "feat", "infra")
	if setup() == nil {
		t.Fatal("cleared before EndSetup: the agent would stop waiting before the notes")
	}
	_ = feature.EndSetup(l, "feat")
	if s := setup(); s != nil {
		t.Errorf("setup = %+v, want cleared", s)
	}
}
