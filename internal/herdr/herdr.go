// Package herdr drives Herdr workspaces through its CLI (JSON over its socket API).
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/seanturner026/pi/internal/shell"
)

// Runner runs a command in dir and returns trimmed stdout.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// ErrUnavailable means Herdr isn't installed or its server isn't running.
var ErrUnavailable = errors.New("herdr unavailable")

// Inside reports whether the current process runs in a Herdr pane.
func Inside() bool { return os.Getenv("HERDR_ENV") == "1" }

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

// FindWorkspace returns the ID of the workspace with label, or "".
func FindWorkspace(ctx context.Context, r Runner, label string) (string, error) {
	var resp struct {
		Result struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := call(ctx, r, &resp, "workspace", "list"); err != nil {
		return "", fmt.Errorf("find workspace: %w", err)
	}
	for _, w := range resp.Result.Workspaces {
		if w.Label == label {
			return w.ID, nil
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

// OpenWorkspace focuses the workspace labelled label, or creates it in dir with
// tabs 1 chat (pi with piArgs) · 2 nvim · 3 shell.
func OpenWorkspace(ctx context.Context, r Runner, label, dir string, piArgs []string) (OpenResult, error) {
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
	if err := call(ctx, r, nil, "tab", "rename", chatTab, "chat"); err != nil {
		return "", fmt.Errorf("rename tab: %w", err)
	}

	var nvim created
	if err := call(ctx, r, &nvim, "tab", "create", "--workspace", ws, "--cwd", dir, "--label", "nvim"); err != nil {
		return "", fmt.Errorf("create nvim tab: %w", err)
	}
	if err := call(ctx, r, nil, "pane", "run", nvim.Result.RootPane.ID, "nvim"); err != nil {
		return "", fmt.Errorf("start nvim: %w", err)
	}
	if err := call(ctx, r, nil, "tab", "create", "--workspace", ws, "--cwd", dir, "--label", "shell"); err != nil {
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
