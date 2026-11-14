package feature_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/model"
)

func TestLockWork(t *testing.T) {
	t.Parallel()
	l := newLayout(t, "feat", "app")
	unlock, err := feature.LockWork(l, "feat", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := feature.LockWork(l, "feat", false); !errors.Is(err, feature.ErrBusy) {
		t.Errorf("second LockWork without waiting: err = %v, want ErrBusy", err)
	}
	unlock()
	again, err := feature.LockWork(l, "feat", false)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
}

func TestConcurrentWritersKeepEachOthersChanges(t *testing.T) {
	t.Parallel()
	l := newLayout(t, "feat", "app")
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := feature.AddComment(l, "feat", model.Comment{File: "app/x.go", Line1: i + 1, Note: fmt.Sprint(i)}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := feature.UpdateInfo(l, "feat", func(info *feature.Info) {
				info.Repos = append(info.Repos, fmt.Sprint(i))
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cs, _ := feature.LoadComments(l, "feat")
	ids := map[int]bool{}
	for _, c := range cs {
		ids[c.ID] = true
	}
	if len(cs) != n || len(ids) != n {
		t.Errorf("%d comments with %d distinct IDs, want %d each", len(cs), len(ids), n)
	}
	if info, _ := feature.LoadInfo(l, "feat"); len(info.Repos) != n {
		t.Errorf("feature.json kept %d of %d concurrent updates", len(info.Repos), n)
	}
}
