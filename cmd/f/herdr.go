package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// herdr wraps the herdr CLI (JSON responses over its socket API).
type herdr struct{ disabled bool }

func newHerdr(cfg config) herdr { return herdr{disabled: cfg.NoHerdr} }

// insideHerdr reports whether f runs in a Herdr pane (and Herdr isn't disabled).
func insideHerdr(cfg config) bool {
	return os.Getenv("HERDR_ENV") == "1" && newHerdr(cfg).available()
}

func (h herdr) available() bool {
	if h.disabled {
		return false
	}
	if _, err := exec.LookPath("herdr"); err != nil {
		return false
	}
	return exec.Command("herdr", "status").Run() == nil
}

func (h herdr) call(out any, args ...string) error {
	s, err := sh("", "herdr", args...)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal([]byte(s), out)
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

func (h herdr) findWorkspace(label string) (string, error) {
	var resp struct {
		Result struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := h.call(&resp, "workspace", "list"); err != nil {
		return "", err
	}
	for _, w := range resp.Result.Workspaces {
		if w.Label == label {
			return w.ID, nil
		}
	}
	return "", nil
}

func (h herdr) closeWorkspace(id string) error { return h.call(nil, "workspace", "close", id) }

// agentName fits herdr's agent name rule: [a-z][a-z0-9_-]{0,31}.
func agentName(feature string) string {
	n := "f-" + strings.ReplaceAll(feature, ".", "-")
	if len(n) > 32 {
		n = n[:32]
	}
	return n
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func quoteAll(args []string) []string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return q
}

// openWorkspace focuses the feature's workspace, or creates it with
// tabs 1 chat (pi with piArgs) · 2 nvim · 3 shell.
func openWorkspace(cfg config, feature string, piArgs []string) error {
	h := newHerdr(cfg)
	if ws, err := h.findWorkspace(feature); err != nil {
		return err
	} else if ws != "" {
		if err := h.call(nil, "workspace", "focus", ws); err != nil {
			return err
		}
		ok("focused herdr workspace %s", feature)
		return nil
	}

	dir := cfg.featureDir(feature)
	var chat created
	if err := h.call(&chat, "workspace", "create", "--cwd", dir, "--label", feature, "--focus"); err != nil {
		return err
	}
	ws, chatTab, chatPane := chat.Result.Workspace.ID, chat.Result.Tab.ID, chat.Result.RootPane.ID
	if err := h.call(nil, "tab", "rename", chatTab, "chat"); err != nil {
		return err
	}

	var nvim created
	if err := h.call(&nvim, "tab", "create", "--workspace", ws, "--cwd", dir, "--label", "nvim"); err != nil {
		return err
	}
	if err := h.call(nil, "pane", "run", nvim.Result.RootPane.ID, "nvim ."); err != nil {
		return err
	}
	if err := h.call(nil, "tab", "create", "--workspace", ws, "--cwd", dir, "--label", "shell"); err != nil {
		return err
	}
	if err := h.call(nil, "tab", "focus", chatTab); err != nil {
		return err
	}

	start := append([]string{"agent", "start", agentName(feature), "--kind", "pi", "--pane", chatPane, "--"}, piArgs...)
	if err := h.call(nil, start...); err != nil {
		// Fall back to typing the command into the pane.
		if err2 := h.call(nil, "pane", "run", chatPane, "pi "+strings.Join(quoteAll(piArgs), " ")); err2 != nil {
			return fmt.Errorf("starting pi: %v; fallback: %v", err, err2)
		}
	}
	ok("opened herdr workspace %s (chat · nvim · shell)", feature)
	return nil
}
