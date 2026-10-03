package herdr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/seanturner026/pi/internal/herdr"
)

func TestAgentName(t *testing.T) {
	tests := []struct {
		name, feature, want string
	}{
		{name: "simple", feature: "install-ack", want: "f-install-ack"},
		{name: "dots become dashes", feature: "v1.2-release", want: "f-v1-2-release"},
		{name: "truncated to 32", feature: strings.Repeat("a", 40), want: "f-" + strings.Repeat("a", 30)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := herdr.AgentName(tt.feature); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeRunner struct{ out string }

func (f fakeRunner) Run(context.Context, string, string, ...string) (string, error) {
	return f.out, nil
}

func TestFindWorkspace(t *testing.T) {
	list := `{"result":{"workspaces":[{"workspace_id":"w1","label":"pi"},{"workspace_id":"w4","label":"install-ack"}]}}`
	tests := []struct {
		name, label, want string
	}{
		{name: "found", label: "install-ack", want: "w4"},
		{name: "missing", label: "nope", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := herdr.FindWorkspace(context.Background(), fakeRunner{out: list}, tt.label)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// recordingRunner records herdr calls and answers creates with fresh IDs.
type recordingRunner struct{ calls []string }

func (r *recordingRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	switch {
	case len(args) >= 2 && args[0] == "workspace" && args[1] == "list":
		return `{"result":{"workspaces":[]}}`, nil
	case len(args) >= 2 && args[1] == "create":
		return `{"result":{"workspace":{"workspace_id":"w1"},"tab":{"tab_id":"w1:t1"},"root_pane":{"pane_id":"w1:p1"}}}`, nil
	}
	return "{}", nil
}

func TestOpenWorkspaceTabOrder(t *testing.T) {
	tests := []struct {
		name  string
		repos []string
		want  []string
	}{
		{name: "two repos", repos: []string{"argocd", "infra"}, want: []string{"nvim", "argocd", "infra", "shell"}},
		{name: "no repos", repos: nil, want: []string{"nvim", "shell"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recordingRunner{}
			res, err := herdr.OpenWorkspace(context.Background(), r, "feat", "/f/feat", tt.repos, []string{"--name", "feat"})
			if err != nil {
				t.Fatal(err)
			}
			if res != herdr.OpenCreated {
				t.Errorf("result = %q", res)
			}
			var tabs []string
			for _, c := range r.calls {
				if strings.HasPrefix(c, "tab create") {
					tabs = append(tabs, c[strings.LastIndex(c, "--label ")+len("--label "):])
				}
			}
			if strings.Join(tabs, ",") != strings.Join(tt.want, ",") {
				t.Errorf("tabs = %v, want %v", tabs, tt.want)
			}
		})
	}
}
