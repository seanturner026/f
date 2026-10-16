package herdr_test

import (
	"context"
	"errors"
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

func TestFocusedWorkspace(t *testing.T) {
	tests := []struct {
		name, list, want string
	}{
		{name: "focused", list: `{"result":{"workspaces":[{"label":"pi"},{"label":"webapp","focused":true}]}}`, want: "webapp"},
		{name: "none", list: `{"result":{"workspaces":[{"label":"pi"}]}}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := herdr.FocusedWorkspace(context.Background(), fakeRunner{out: tt.list})
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
		{name: "two repos", repos: []string{"argocd", "infra"}, want: []string{"v:nvim", "a:argocd", "s:infra", "p:prs", "t:shell"}},
		{name: "no repos", repos: nil, want: []string{"v:nvim", "p:prs", "t:shell"}},
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

func TestAgentStatuses(t *testing.T) {
	list := `{"result":{"agents":[
		{"agent":"pi","agent_status":"blocked","cwd":"/f/a"},
		{"agent":"pi","agent_status":"working","cwd":"/f/b"},
		{"agent":"claude","agent_status":"idle","cwd":"/f/c"}]}}`
	got, err := herdr.AgentStatuses(context.Background(), fakeRunner{out: list})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		cwd  string
		want herdr.AgentStatus
	}{
		{cwd: "/f/a", want: herdr.AgentBlocked},
		{cwd: "/f/b", want: herdr.AgentWorking},
		{cwd: "/f/c", want: ""}, // not pi
	}
	for _, tt := range tests {
		t.Run(tt.cwd, func(t *testing.T) {
			if got[tt.cwd] != tt.want {
				t.Errorf("got %q, want %q", got[tt.cwd], tt.want)
			}
		})
	}
}

type promptRunner struct {
	list   string
	prompt []string
}

func (p *promptRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	if args[0] == "agent" && args[1] == "prompt" {
		p.prompt = args[2:]
		return "{}", nil
	}
	return p.list, nil
}

func TestPromptPi(t *testing.T) {
	list := `{"result":{"agents":[{"agent":"pi","cwd":"/f/a","pane_id":"w1:p1"},{"agent":"pi","cwd":"/f/b","pane_id":"w2:p1"}]}}`
	tests := []struct {
		name, dir, wantPane string
		wantErr             error
	}{
		{name: "finds pi by cwd", dir: "/f/b", wantPane: "w2:p1"},
		{name: "no pi there", dir: "/f/c", wantErr: herdr.ErrNoPi},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &promptRunner{list: list}
			err := herdr.PromptPi(context.Background(), r, tt.dir, "hello")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantPane != "" && (len(r.prompt) != 2 || r.prompt[0] != tt.wantPane || r.prompt[1] != "hello") {
				t.Errorf("prompt args = %v", r.prompt)
			}
		})
	}
}

// tabsRunner answers workspace list, tab create, and tab list for AddRepoTab.
type tabsRunner struct{ tabs string }

func (r tabsRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return `{"result":{"workspaces":[{"workspace_id":"w1","label":"feat"}]}}`, nil
	case "tab create":
		return `{"result":{"tab":{"tab_id":"new"},"root_pane":{"pane_id":"p"}}}`, nil
	case "tab list":
		return r.tabs, nil
	}
	return "{}", nil
}

type moveCaller struct{ index any }

func (c *moveCaller) Call(_ context.Context, method string, params, _ any) error {
	if method == "tab.move" {
		c.index = params.(map[string]any)["insert_index"]
	}
	return nil
}

func TestAddRepoTabPlacement(t *testing.T) {
	tab := func(labels ...string) string {
		var parts []string
		for _, l := range labels {
			parts = append(parts, `{"tab_id":"`+l+`","label":"`+l+`"}`)
		}
		return `{"result":{"tabs":[` + strings.Join(parts, ",") + `]}}`
	}
	tests := []struct {
		name string
		tabs string
		want any
	}{
		{name: "before prs", tabs: tab("c:chat", "v:nvim", "a:argocd", "p:prs", "t:shell"), want: 3},
		{name: "before shell (older workspace)", tabs: tab("chat", "nvim", "argocd", "shell"), want: 3},
		{name: "neither: stays last", tabs: tab("chat"), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &moveCaller{}
			if _, err := herdr.AddRepoTab(context.Background(), tabsRunner{tabs: tt.tabs}, c, "feat", "/f", "infra"); err != nil {
				t.Fatal(err)
			}
			if c.index != tt.want {
				t.Errorf("insert_index = %v, want %v", c.index, tt.want)
			}
		})
	}
}

type focusRunner struct {
	tabs    string
	focused string
}

func (r *focusRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return `{"result":{"workspaces":[{"workspace_id":"w1"},{"workspace_id":"w2","focused":true}]}}`, nil
	case "tab list":
		return r.tabs, nil
	case "tab focus":
		r.focused = args[2]
	}
	return "{}", nil
}

func TestFocusTab(t *testing.T) {
	keyed := `{"result":{"tabs":[{"tab_id":"t1","label":"c:chat"},{"tab_id":"t2","label":"v:nvim"},{"tab_id":"t3","label":"a:argocd"},{"tab_id":"t4","label":"s:tf"},{"tab_id":"t5","label":"p:prs"},{"tab_id":"t6","label":"t:shell"}]}}`
	legacy := `{"result":{"tabs":[{"tab_id":"t1","label":"chat"},{"tab_id":"t2","label":"nvim"},{"tab_id":"t3","label":"argocd"},{"tab_id":"t4","label":"tf"},{"tab_id":"t5","label":"shell"}]}}`
	tests := []struct {
		name, tabs, key, want string
	}{
		{name: "chat", tabs: keyed, key: "c", want: "t1"},
		{name: "repo s", tabs: keyed, key: "s", want: "t4"},
		{name: "prs", tabs: keyed, key: "p", want: "t5"},
		{name: "missing repo key", tabs: keyed, key: "e", want: ""},
		{name: "legacy shell by name", tabs: legacy, key: "t", want: "t5"},
		{name: "legacy repo by position", tabs: legacy, key: "s", want: "t4"},
		{name: "legacy prs missing", tabs: legacy, key: "p", want: ""},
		{name: "unknown key", tabs: keyed, key: "z", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &focusRunner{tabs: tt.tabs}
			if err := herdr.FocusTab(context.Background(), r, tt.key); err != nil {
				t.Fatal(err)
			}
			if r.focused != tt.want {
				t.Errorf("focused %q, want %q", r.focused, tt.want)
			}
		})
	}
}

type pickerRunner struct{ opens, failFirst int }

func (r *pickerRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	r.opens++
	if r.opens <= r.failFirst {
		return "", errors.New("ui_busy: a popup pane is already open")
	}
	return "{}", nil
}

type closeCaller struct{ closed bool }

func (c *closeCaller) Call(_ context.Context, method string, _, _ any) error {
	c.closed = c.closed || method == "popup.close"
	return nil
}

func TestOpenPicker(t *testing.T) {
	r, c := &pickerRunner{}, &closeCaller{}
	if err := herdr.OpenPicker(context.Background(), r, c); err != nil || r.opens != 1 || c.closed {
		t.Errorf("normal open: err=%v opens=%d closed=%v", err, r.opens, c.closed)
	}
	r, c = &pickerRunner{failFirst: 1}, &closeCaller{}
	if err := herdr.OpenPicker(context.Background(), r, c); err != nil || r.opens != 2 || !c.closed {
		t.Errorf("stale popup: err=%v opens=%d closed=%v", err, r.opens, c.closed)
	}
}

// reviveRunner answers tab/pane lists for one workspace; panes in busy run
// something, the rest sit at a bare shell. It records what gets started.
type reviveRunner struct {
	busy    map[string]bool
	started []string
}

func (r *reviveRunner) Run(_ context.Context, _, _ string, args ...string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "tab list":
		return `{"result":{"tabs":[
			{"tab_id":"t1","label":"c:chat"},{"tab_id":"t2","label":"v:nvim"},
			{"tab_id":"t3","label":"a:argocd"},{"tab_id":"t4","label":"gateway"},
			{"tab_id":"t5","label":"p:prs"},{"tab_id":"t6","label":"t:shell"},
			{"tab_id":"t7","label":"scratch"},{"tab_id":"t8","label":"s:infra"}]}}`, nil
	case "pane list":
		return `{"result":{"panes":[
			{"pane_id":"p1","tab_id":"t1"},{"pane_id":"p2","tab_id":"t2"},
			{"pane_id":"p3","tab_id":"t3"},{"pane_id":"p4","tab_id":"t4"},
			{"pane_id":"p5","tab_id":"t5"},{"pane_id":"p6","tab_id":"t6"},
			{"pane_id":"p7","tab_id":"t7"},{"pane_id":"p8","tab_id":"t8"},{"pane_id":"p9","tab_id":"t8"}]}}`, nil
	case "pane process-info":
		fg := "1"
		if r.busy[args[3]] {
			fg = "2"
		}
		return `{"result":{"process_info":{"shell_pid":1,"foreground_process_group_id":` + fg + `}}}`, nil
	case "pane run":
		r.started = append(r.started, args[2]+" "+args[3])
	case "agent start":
		r.started = append(r.started, args[6]+" "+strings.Join(args[8:], " "))
	}
	return "{}", nil
}

func TestReviveWorkspace(t *testing.T) {
	repos := []string{"argocd", "gateway", "infra"}
	piArgs := []string{"--name", "feat", "--continue"}
	tests := []struct {
		name string
		busy []string
		want []string
	}{
		{
			name: "everything stopped: f's single-pane tabs restart, shell and unknown stay",
			want: []string{
				"p1 --name feat --continue",
				"p2 nvim",
				"p3 nvim -c 'PiReview argocd'",
				"p4 nvim -c 'PiReview gateway'",
				"p5 f prs --watch",
			},
		},
		{
			name: "running tabs left alone",
			busy: []string{"p1", "p3", "p4", "p5"},
			want: []string{"p2 nvim"},
		},
		{name: "all running", busy: []string{"p1", "p2", "p3", "p4", "p5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &reviveRunner{busy: map[string]bool{}}
			for _, p := range tt.busy {
				r.busy[p] = true
			}
			n, err := herdr.ReviveWorkspace(context.Background(), r, "w1", "feat", repos, piArgs)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(tt.want) || strings.Join(r.started, "|") != strings.Join(tt.want, "|") {
				t.Errorf("n=%d started %q, want %q", n, r.started, tt.want)
			}
		})
	}
}
