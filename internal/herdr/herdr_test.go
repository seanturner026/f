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
