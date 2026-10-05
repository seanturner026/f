// Package herdr drives Herdr workspaces through its CLI (JSON over its socket API).
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/seanturner026/pi/internal/shell"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// ErrUnavailable means Herdr isn't installed or its server isn't running.
var ErrUnavailable = errors.New("herdr unavailable")

// Inside reports whether the current process runs in Herdr: a pane sets
// HERDR_ENV; popups and custom commands only get HERDR_SOCKET_PATH.
func Inside() bool { return os.Getenv("HERDR_ENV") == "1" || os.Getenv("HERDR_SOCKET_PATH") != "" }

// Available reports whether the Herdr server answers.
func Available(ctx context.Context, r Runner) bool {
	_, err := r.Run(ctx, "", "herdr", "status")
	return err == nil
}

// AgentName fits Herdr's agent name rule: [a-z][a-z0-9_-]{0,31}.
func AgentName(feature string) string {
	n := "f-" + strings.ReplaceAll(feature, ".", "-")
	if len(n) > 32 {
		n = n[:32]
	}
	return n
}

func call(ctx context.Context, r Runner, out any, args ...string) error {
	s, err := r.Run(ctx, "", "herdr", args...)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(s), out); err != nil {
		return fmt.Errorf("herdr %s: decode: %w", strings.Join(args, " "), err)
	}
	return nil
}

type created struct {
	Result struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		Tab struct {
			ID string `json:"tab_id"`
		} `json:"tab"`
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	} `json:"result"`
}

type workspace struct {
	ID      string `json:"workspace_id"`
	Label   string `json:"label"`
	Focused bool   `json:"focused"`
}

func listWorkspaces(ctx context.Context, r Runner) ([]workspace, error) {
	var resp struct {
		Result struct {
			Workspaces []workspace `json:"workspaces"`
		} `json:"result"`
	}
	if err := call(ctx, r, &resp, "workspace", "list"); err != nil {
		return nil, fmt.Errorf("workspace list: %w", err)
	}
	return resp.Result.Workspaces, nil
}

// FindWorkspace returns the ID of the workspace with label, or "".
func FindWorkspace(ctx context.Context, r Runner, label string) (string, error) {
	workspaces, err := listWorkspaces(ctx, r)
	if err != nil {
		return "", fmt.Errorf("find workspace: %w", err)
	}
	for _, w := range workspaces {
		if w.Label == label {
			return w.ID, nil
		}
	}
	return "", nil
}

// FocusedWorkspace returns the label of the focused workspace (the one under a
// popup), or "".
func FocusedWorkspace(ctx context.Context, r Runner) (string, error) {
	workspaces, err := listWorkspaces(ctx, r)
	if err != nil {
		return "", fmt.Errorf("focused workspace: %w", err)
	}
	for _, w := range workspaces {
		if w.Focused {
			return w.Label, nil
		}
	}
	return "", nil
}

// CloseWorkspace closes the workspace with label, if it exists.
func CloseWorkspace(ctx context.Context, r Runner, label string) error {
	id, err := FindWorkspace(ctx, r, label)
	if err != nil || id == "" {
		return err
	}
	if err := call(ctx, r, nil, "workspace", "close", id); err != nil {
		return fmt.Errorf("close workspace: %w", err)
	}
	return nil
}

// OpenResult says whether OpenWorkspace focused an existing workspace or created one.
type OpenResult string

const (
	OpenFocused OpenResult = "focused"
	OpenCreated OpenResult = "created"
)

// The PRs tab: each repo's PRs and checks, kept fresh (f prs --watch).
const (
	prsTab     = "prs"
	prsCommand = "f prs --watch"
)

// Tab keys: cmd+ctrl+<key> focuses the tab (WezTerm → ctrl+alt → `f tab
// <key>`), and each label starts with its key ("a:argocd") as a reminder.
const (
	keyChat  = "c"
	keyNvim  = "v"
	keyPRs   = "p"
	keyShell = "t"
)

// repoKeys are the keys of the first repo tabs, in order; later repos get none.
var repoKeys = []string{"a", "s", "e", "r", "g"}

// tabLabel is "<key>:<name>", or just name without a key.
func tabLabel(key, name string) string {
	if key == "" {
		return name
	}
	return key + ":" + name
}

// splitLabel returns a tab label's key and name ("a:argocd" → "a", "argocd");
// labels from before keys have no key.
func splitLabel(label string) (key, name string) {
	if k, n, ok := strings.Cut(label, ":"); ok && len(k) == 1 {
		return k, n
	}
	return "", label
}

// reviewCommand runs nvim on one repo's live CodeDiff (:PiReview in nvim/f.lua).
func reviewCommand(repo string) string { return "nvim -c " + shell.Quote("PiReview "+repo) }

// OpenWorkspace focuses the workspace labelled label, or creates it in dir with
// tabs: chat (pi with piArgs) · nvim · one review tab per repo · prs · shell.
func OpenWorkspace(ctx context.Context, r Runner, label, dir string, repos, piArgs []string) (OpenResult, error) {
	if id, err := FindWorkspace(ctx, r, label); err != nil {
		return "", err
	} else if id != "" {
		if err := call(ctx, r, nil, "workspace", "focus", id); err != nil {
			return "", fmt.Errorf("focus workspace: %w", err)
		}
		return OpenFocused, nil
	}

	var chat created
	if err := call(ctx, r, &chat, "workspace", "create", "--cwd", dir, "--label", label, "--focus"); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	ws, chatTab, chatPane := chat.Result.Workspace.ID, chat.Result.Tab.ID, chat.Result.RootPane.ID
	if err := call(ctx, r, nil, "tab", "rename", chatTab, tabLabel(keyChat, "chat")); err != nil {
		return "", fmt.Errorf("rename tab: %w", err)
	}
	if err := tabRunning(ctx, r, ws, dir, tabLabel(keyNvim, "nvim"), "nvim"); err != nil {
		return "", err
	}
	for i, repo := range repos {
		key := ""
		if i < len(repoKeys) {
			key = repoKeys[i]
		}
		if err := tabRunning(ctx, r, ws, dir, tabLabel(key, repo), reviewCommand(repo)); err != nil {
			return "", err
		}
	}
	if err := tabRunning(ctx, r, ws, dir, tabLabel(keyPRs, prsTab), prsCommand); err != nil {
		return "", err
	}
	if err := call(ctx, r, nil, "tab", "create", "--workspace", ws, "--cwd", dir, "--label", tabLabel(keyShell, "shell")); err != nil {
		return "", fmt.Errorf("create shell tab: %w", err)
	}
	if err := call(ctx, r, nil, "tab", "focus", chatTab); err != nil {
		return "", fmt.Errorf("focus chat tab: %w", err)
	}

	start := append([]string{"agent", "start", AgentName(label), "--kind", "pi", "--pane", chatPane, "--"}, piArgs...)
	if err := call(ctx, r, nil, start...); err != nil {
		// Fall back to typing the command into the pane.
		cmd := "pi " + strings.Join(shell.QuoteAll(piArgs), " ")
		if err2 := call(ctx, r, nil, "pane", "run", chatPane, cmd); err2 != nil {
			return "", fmt.Errorf("start pi: %w (fallback: %w)", err, err2)
		}
	}
	return OpenCreated, nil
}

// AddRepoTab adds a review tab for repo to the workspace labelled label, if it
// exists, placed after the other repo tabs: before the prs or shell tab (or
// last if there's neither).
func AddRepoTab(ctx context.Context, r Runner, c Caller, label, dir, repo string) (bool, error) {
	ws, err := FindWorkspace(ctx, r, label)
	if err != nil || ws == "" {
		return false, err
	}
	tabs, err := listTabs(ctx, r, ws)
	if err != nil {
		return true, err
	}
	used := map[string]bool{}
	for _, t := range tabs {
		k, _ := splitLabel(t.Label)
		used[k] = true
	}
	key := ""
	for _, k := range repoKeys {
		if !used[k] {
			key = k
			break
		}
	}
	tabID, err := tabRunningID(ctx, r, ws, dir, tabLabel(key, repo), reviewCommand(repo))
	if err != nil {
		return true, err
	}
	for i, t := range tabs {
		if _, name := splitLabel(t.Label); name == prsTab || name == "shell" {
			if err := c.Call(ctx, "tab.move", map[string]any{"tab_id": tabID, "insert_index": i}, nil); err != nil {
				return true, fmt.Errorf("move %s tab: %w", repo, err)
			}
			break
		}
	}
	return true, nil
}

type tab struct {
	ID    string `json:"tab_id"`
	Label string `json:"label"`
}

func listTabs(ctx context.Context, r Runner, ws string) ([]tab, error) {
	var list struct {
		Result struct {
			Tabs []tab `json:"tabs"`
		} `json:"result"`
	}
	if err := call(ctx, r, &list, "tab", "list", "--workspace", ws); err != nil {
		return nil, fmt.Errorf("list tabs: %w", err)
	}
	return list.Result.Tabs, nil
}

// tabRunning creates a tab labelled tabLabel in workspace ws and runs cmd in it.
func tabRunning(ctx context.Context, r Runner, ws, dir, tabLabel, cmd string) error {
	_, err := tabRunningID(ctx, r, ws, dir, tabLabel, cmd)
	return err
}

func tabRunningID(ctx context.Context, r Runner, ws, dir, tabLabel, cmd string) (string, error) {
	var tab created
	if err := call(ctx, r, &tab, "tab", "create", "--workspace", ws, "--cwd", dir, "--label", tabLabel); err != nil {
		return "", fmt.Errorf("create %s tab: %w", tabLabel, err)
	}
	if err := call(ctx, r, nil, "pane", "run", tab.Result.RootPane.ID, cmd); err != nil {
		return "", fmt.Errorf("start %s tab: %w", tabLabel, err)
	}
	return tab.Result.Tab.ID, nil
}

// AgentStatus is a Herdr agent's lifecycle state.
type AgentStatus string

const (
	AgentWorking AgentStatus = "working" // running a turn
	AgentBlocked AgentStatus = "blocked" // waiting at an approval or question
	AgentDone    AgentStatus = "done"    // finished a turn you haven't looked at
	AgentIdle    AgentStatus = "idle"    // ready for input, already seen
	AgentUnknown AgentStatus = "unknown"
)

// Agent is a running pi in a Herdr pane.
type Agent struct {
	Pane   string
	Status AgentStatus
	Cwd    string
}

// PiAgents lists running pi agents.
func PiAgents(ctx context.Context, r Runner) ([]Agent, error) {
	var resp struct {
		Result struct {
			Agents []struct {
				Agent  string      `json:"agent"`
				Status AgentStatus `json:"agent_status"`
				Cwd    string      `json:"cwd"`
				Pane   string      `json:"pane_id"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := call(ctx, r, &resp, "agent", "list"); err != nil {
		return nil, fmt.Errorf("agent list: %w", err)
	}
	var out []Agent
	for _, a := range resp.Result.Agents {
		if a.Agent == "pi" {
			out = append(out, Agent{Pane: a.Pane, Status: a.Status, Cwd: a.Cwd})
		}
	}
	return out, nil
}

// AgentStatuses returns the status of each running pi agent, keyed by its cwd.
func AgentStatuses(ctx context.Context, r Runner) (map[string]AgentStatus, error) {
	agents, err := PiAgents(ctx, r)
	if err != nil {
		return nil, err
	}
	out := map[string]AgentStatus{}
	for _, a := range agents {
		out[a.Cwd] = a.Status
	}
	return out, nil
}

// ErrNoPi means no pi is running in the given directory.
var ErrNoPi = errors.New("no pi running there")

// PromptPi submits text to the pi running in dir (found by cwd, since Herdr
// agent names are dropped when pi restarts).
func PromptPi(ctx context.Context, r Runner, dir, text string) error {
	agents, err := PiAgents(ctx, r)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.Cwd == dir {
			if err := call(ctx, r, nil, "agent", "prompt", a.Pane, text); err != nil {
				return fmt.Errorf("prompt pi: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("%s: %w", dir, ErrNoPi)
}

// legacyNames finds tabs by name in workspaces made before tab keys.
var legacyNames = map[string]string{keyChat: "chat", keyNvim: "nvim", keyPRs: prsTab, keyShell: "shell"}

// FocusTab focuses the focused workspace's tab for key: the tab labelled
// "<key>:…". Older workspaces without keys: by name, or for a repo key the
// nth tab that isn't chat/nvim/prs/shell. A missing tab is a no-op.
func FocusTab(ctx context.Context, r Runner, key string) error {
	workspaces, err := listWorkspaces(ctx, r)
	if err != nil {
		return err
	}
	ws := ""
	for _, w := range workspaces {
		if w.Focused {
			ws = w.ID
		}
	}
	if ws == "" {
		return nil
	}
	tabs, err := listTabs(ctx, r, ws)
	if err != nil {
		return err
	}
	if id := findTab(tabs, key); id != "" {
		return call(ctx, r, nil, "tab", "focus", id)
	}
	return nil
}

func findTab(tabs []tab, key string) string {
	keyed := false
	for _, t := range tabs {
		k, _ := splitLabel(t.Label)
		if k == key {
			return t.ID
		}
		keyed = keyed || k != ""
	}
	if keyed {
		return "" // a workspace with keys just has no such tab
	}
	if name, ok := legacyNames[key]; ok {
		for _, t := range tabs {
			if t.Label == name {
				return t.ID
			}
		}
		return ""
	}
	n := slices.Index(repoKeys, key) + 1 // 0: not a repo key
	roles := slices.Collect(maps.Values(legacyNames))
	for _, t := range tabs {
		if slices.Contains(roles, t.Label) {
			continue
		}
		if n--; n == 0 {
			return t.ID
		}
	}
	return ""
}
