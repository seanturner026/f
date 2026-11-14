package feature_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/seanturner026/f/internal/feature"
	"github.com/seanturner026/f/internal/model"
)

func TestTuicrComments(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tuicr"); err != nil {
		t.Skip("tuicr not installed") // TuicrComments skips repos without it
	}
	l := newLayout(t, "feat", "app")
	dir := feature.RepoDir(l, "feat", "app")
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := fakeRunner{
		"tuicr review list": `[{"slug":"s1","kind":"local","comment_count":5},{"slug":"empty","kind":"local","comment_count":0},{"slug":"pr","kind":"pr","comment_count":2}]`,
		"tuicr review comments --session s1": `[
			{"id":"c1","path":"x.go","start_line":2,"end_line":3,"side":"new","comment_type":"none","content":"rename"},
			{"id":"c2","path":"x.go","start_line":1,"end_line":1,"side":"old","comment_type":"issue","content":"why gone?"},
			{"id":"c3","path":"x.go","start_line":null,"end_line":null,"side":null,"comment_type":"none","content":"split it"},
			{"id":"c4","path":null,"start_line":null,"end_line":null,"side":null,"comment_type":"none","content":"overall fine"}]`,
	}
	got, sessions, err := feature.TuicrComments(context.Background(), run, l, "feat")
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Comment{
		{File: "app/x.go", Line1: 2, Line2: 3, Code: "two\nthree", Lang: "go", Note: "rename"},
		{File: "app/x.go", Line1: 1, Line2: 1, Lang: "go", Note: "(on the removed lines) [issue] why gone?"},
		{File: "app/x.go", Lang: "go", Note: "split it"},
		{File: "app", Note: "overall fine"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("comments =\n%+v\nwant\n%+v", got, want)
	}
	if want := []feature.ReviewSession{{Dir: dir, Slug: "s1", IDs: []string{"c1", "c2", "c3", "c4"}}}; !reflect.DeepEqual(sessions, want) {
		t.Errorf("sessions = %+v, want %+v (only local ones with comments)", sessions, want)
	}
}
