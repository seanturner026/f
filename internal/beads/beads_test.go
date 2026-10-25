package beads_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seanturner026/f/internal/beads"
	"github.com/seanturner026/f/internal/model"
)

// fakeRunner answers by bd subcommand; "ERR" fails. It records the last args.
type fakeRunner struct {
	out  map[string]string
	last []string
}

func (f *fakeRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	f.last = args
	out, ok := f.out[args[0]]
	if !ok || out == "ERR" {
		return "", errors.New("fail")
	}
	return out, nil
}

var ctx = context.Background()

func TestCreate(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    string
		wantErr bool
	}{
		{name: "object", out: `{"id":"f-a1b","title":"x"}`, want: "f-a1b"},
		{name: "list", out: `[{"id":"f-c2d"}]`, want: "f-c2d"},
		{name: "no id", out: `{}`, wantErr: true},
		{name: "bd fails", out: "ERR", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRunner{out: map[string]string{"create": tt.out}}
			got, err := beads.Create(ctx, r, "/base", "install-ack", beads.TypeFeature, "goal")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if !tt.wantErr && !strings.Contains(strings.Join(r.last, " "), "--type feature") {
				t.Errorf("args = %v", r.last)
			}
		})
	}
}

func TestProgress(t *testing.T) {
	tests := []struct {
		name    string
		out     map[string]string
		want    model.Plan
		wantErr bool
	}{
		{name: "no children", out: map[string]string{"children": "[]"}, want: model.Plan{Issue: "f-1"}},
		{
			name: "some done, next ready",
			out: map[string]string{
				"children": `[{"id":"f-1.1","status":"closed"},{"id":"f-1.2","title":"upgrade webapp","status":"open"},{"id":"f-1.3","title":"cut over","status":"open"}]`,
			},
			want: model.Plan{Issue: "f-1", Total: 3, Done: 1, Next: "upgrade webapp"},
		},
		{
			name: "next skips a step waiting on an open one",
			out: map[string]string{
				"children": `[{"id":"f-1.1","title":"cut over","status":"open","dependencies":[{"depends_on_id":"f-1.2","type":"blocks"}]},{"id":"f-1.2","title":"upgrade webapp","status":"open"}]`,
			},
			want: model.Plan{Issue: "f-1", Total: 2, Next: "upgrade webapp"},
		},
		{
			name: "all done",
			out:  map[string]string{"children": `[{"id":"f-1.1","status":"closed"}]`},
			want: model.Plan{Issue: "f-1", Total: 1, Done: 1},
		},
		{name: "bd fails", out: map[string]string{"children": "ERR"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := beads.Progress(ctx, &fakeRunner{out: tt.out}, "/base", "f-1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLinkedPRs(t *testing.T) {
	got := beads.LinkedPRs("https://github.com/o/r/pull/1  https://github.com/o/r/pull/2/ gh-9 https://linear.app/x")
	if strings.Join(got, " ") != "https://github.com/o/r/pull/1 https://github.com/o/r/pull/2" {
		t.Errorf("LinkedPRs = %q", got)
	}
	if beads.LinkedPRs("") != nil {
		t.Error("empty ref: want no PRs")
	}
}
