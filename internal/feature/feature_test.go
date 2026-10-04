package feature_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seanturner026/pi/internal/feature"
)

// fakeRunner answers commands by their joined text ("git status --porcelain").
// Unknown commands fail.
type fakeRunner map[string]string

func (f fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	for prefix, out := range f {
		if strings.HasPrefix(key, prefix) {
			if out == "ERR" {
				return "", errors.New("fail")
			}
			return out, nil
		}
	}
	return "", errors.New("unexpected command: " + key)
}

// newLayout builds base clones and a feature with worktrees for repos.
func newLayout(t *testing.T, name string, repos ...string) feature.Layout {
	t.Helper()
	l := feature.NewLayout(t.TempDir())
	for _, r := range repos {
		mustMkdir(t, filepath.Join(l.Base, r, ".git"))
		mustMkdir(t, filepath.Join(feature.Dir(l, name), r))
		if err := os.WriteFile(filepath.Join(feature.Dir(l, name), r, ".git"), []byte("gitdir: x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCheckName(t *testing.T) {
	tests := []struct {
		name, in string
		wantErr  bool
	}{
		{name: "kebab", in: "install-ack"},
		{name: "dots and digits", in: "v1.2_fix"},
		{name: "uppercase", in: "Install", wantErr: true},
		{name: "leading dash", in: "-x", wantErr: true},
		{name: "slash", in: "a/b", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := feature.CheckName(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, feature.ErrInvalidName) {
				t.Errorf("want ErrInvalidName, got %v", err)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	l := newLayout(t, "feat", "argocd")
	tests := []struct {
		name, explicit, cwd, want string
		wantErr                   error
	}{
		{name: "explicit", explicit: "feat", want: "feat"},
		{name: "feature root", cwd: feature.Dir(l, "feat"), want: "feat"},
		{name: "inside a repo", cwd: filepath.Join(feature.Dir(l, "feat"), "argocd", "sub"), want: "feat"},
		{name: "outside", cwd: l.Base, wantErr: feature.ErrNotInFeature},
		{name: "features dir itself", cwd: l.Features, wantErr: feature.ErrNotInFeature},
		{name: "explicit missing", explicit: "nope", wantErr: feature.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := feature.Resolve(l, tt.explicit, tt.cwd)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSync(t *testing.T) {
	l := newLayout(t, "feat", "argocd", "ops")
	if err := os.WriteFile(filepath.Join(feature.Dir(l, "feat"), "argocd", "CLAUDE.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, goal, wantGoal string
	}{
		{name: "no goal yet", goal: "", wantGoal: ""},
		{name: "goal set", goal: "install ack", wantGoal: "install ack"},
		{name: "empty goal keeps existing", goal: "", wantGoal: "install ack"},
		{name: "new goal replaces", goal: "line one\nline two", wantGoal: "line one\nline two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := feature.Sync(l, "feat", tt.goal)
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := feature.LoadInfo(l, "feat")
			if info.Goal != tt.wantGoal || saved.Goal != tt.wantGoal {
				t.Errorf("goal = %q (saved %q), want %q", info.Goal, saved.Goal, tt.wantGoal)
			}
			if strings.Join(saved.Repos, ",") != "argocd,ops" || saved.Root != feature.Dir(l, "feat") {
				t.Errorf("info = %+v", saved)
			}
			raw, _ := os.ReadFile(feature.AgentsMDPath(l, "feat"))
			for _, want := range []string{"# Feature: feat", "`argocd/` (instructions: `argocd/CLAUDE.md`)", "- `ops/`\n"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("AGENTS.md missing %q", want)
				}
			}
			if tt.wantGoal != "" && !strings.Contains(string(raw), tt.wantGoal) {
				t.Errorf("AGENTS.md missing goal %q", tt.wantGoal)
			}
		})
	}
}

func TestSyncMigratesLegacyGoal(t *testing.T) {
	tests := []struct {
		name, agents, wantGoal string
	}{
		{name: "real goal kept", agents: "# Feature: feat\n\n## Goal\n\nship it\n\n## Repos\n", wantGoal: "ship it"},
		{name: "legacy placeholder is not a goal", agents: "# Feature: feat\n\n## Goal\n\n_TBD: set from the first prompt._\n\n## Repos\n", wantGoal: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t, "feat", "argocd")
			if err := os.WriteFile(feature.AgentsMDPath(l, "feat"), []byte(tt.agents), 0o644); err != nil {
				t.Fatal(err)
			}
			info, err := feature.Sync(l, "feat", "")
			if err != nil {
				t.Fatal(err)
			}
			if info.Goal != tt.wantGoal {
				t.Errorf("goal = %q, want %q", info.Goal, tt.wantGoal)
			}
		})
	}
}

func TestFinished(t *testing.T) {
	const merged = `[{"number":1,"state":"MERGED","title":"t","url":"u","isDraft":false,"headRefOid":"abc"}]`
	common := fakeRunner{
		"git symbolic-ref": "origin/main",
	}
	tests := []struct {
		name   string
		runner fakeRunner
		want   bool
	}{
		{
			name:   "merged at head",
			runner: fakeRunner{"git status": "", "git rev-list": "2", "gh pr list": merged, "git rev-parse HEAD": "abc"},
			want:   true,
		},
		{
			name: "head is ancestor of merged head",
			runner: fakeRunner{"git status": "", "git rev-list": "2", "gh pr list": merged, "git rev-parse HEAD": "old",
				"git merge-base --is-ancestor old abc": ""},
			want: true,
		},
		{
			name: "local commits beyond merged head",
			runner: fakeRunner{"git status": "", "git rev-list": "3", "gh pr list": merged, "git rev-parse HEAD": "new",
				"git merge-base": "ERR"},
			want: false,
		},
		{
			name:   "uncommitted changes",
			runner: fakeRunner{"git status": " M file.go"},
			want:   false,
		},
		{
			name:   "untouched repo only",
			runner: fakeRunner{"git status": "", "git rev-list": "0"},
			want:   false,
		},
		{
			name:   "open PR",
			runner: fakeRunner{"git status": "", "git rev-list": "1", "gh pr list": `[{"number":1,"state":"OPEN","url":"u","headRefOid":"abc"}]`},
			want:   false,
		},
		{
			name:   "gh fails",
			runner: fakeRunner{"git status": "", "git rev-list": "1", "gh pr list": "ERR"},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t, "feat", "argocd")
			for k, v := range common {
				tt.runner[k] = v
			}
			if got := feature.Finished(context.Background(), tt.runner, l, "feat"); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPRs(t *testing.T) {
	l := newLayout(t, "feat", "argocd", "infra")
	runner := fakeRunner{"gh pr list": `[{"number":7,"state":"OPEN","url":"u","isDraft":false}]`}
	prs, errs, err := feature.PRs(context.Background(), runner, l, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(prs) != 2 || prs[0].Repo != "argocd" || prs[1].Repo != "infra" {
		t.Errorf("prs = %+v", prs)
	}
}

func TestUnmerged(t *testing.T) {
	const merged = `[{"number":1,"state":"MERGED","url":"u","headRefOid":"abc"}]`
	tests := []struct {
		name   string
		runner fakeRunner
		want   []string
	}{
		{
			name:   "clean and untouched",
			runner: fakeRunner{"git status": "", "git rev-list": "0"},
			want:   nil,
		},
		{
			name:   "merged at head",
			runner: fakeRunner{"git status": "", "git rev-list": "2", "gh pr list": merged, "git rev-parse HEAD": "abc"},
			want:   nil,
		},
		{
			name:   "uncommitted",
			runner: fakeRunner{"git status": " M a\n?? b", "git rev-list": "0"},
			want:   []string{"argocd: 2 uncommitted change(s)"},
		},
		{
			name:   "commits without merged PR",
			runner: fakeRunner{"git status": "", "git rev-list": "3", "gh pr list": "[]", "git rev-parse HEAD": "abc"},
			want:   []string{"argocd: 3 commit(s) not in a merged PR"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t, "feat", "argocd")
			tt.runner["git symbolic-ref"] = "origin/main"
			got, err := feature.Unmerged(context.Background(), tt.runner, l, "feat")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
