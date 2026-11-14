package herdr

import (
	"context"
	"time"
)

// PaneVisible reports whether pane is on screen: focused in its tab, the tab
// focused in its workspace, and the workspace focused. It asks over the socket:
// every review tab asks on every focus change, so no processes are started.
func PaneVisible(ctx context.Context, c Caller, pane string) (bool, error) {
	var p struct {
		Pane struct {
			Focused   bool   `json:"focused"`
			Tab       string `json:"tab_id"`
			Workspace string `json:"workspace_id"`
		} `json:"pane"`
	}
	if err := c.Call(ctx, "pane.get", map[string]string{"pane_id": pane}, &p); err != nil || !p.Pane.Focused {
		return false, err
	}
	var t struct {
		Tab struct {
			Focused bool `json:"focused"`
		} `json:"tab"`
	}
	if err := c.Call(ctx, "tab.get", map[string]string{"tab_id": p.Pane.Tab}, &t); err != nil || !t.Tab.Focused {
		return false, err
	}
	var w struct {
		Workspace struct {
			Focused bool `json:"focused"`
		} `json:"workspace"`
	}
	if err := c.Call(ctx, "workspace.get", map[string]string{"workspace_id": p.Pane.Workspace}, &w); err != nil {
		return false, err
	}
	return w.Workspace.Focused, nil
}

// focusEvents are the events after which a pane may have come into or out of view.
var focusEvents = []string{"workspace.focused", "tab.focused", "pane.focused"}

// WatchFocus calls fn with whether pane is visible, now and whenever that
// changes, until ctx ends. While Herdr can't be asked it reports visible, so
// nothing stays paused, and resubscribes every few seconds.
func WatchFocus(ctx context.Context, s Socket, pane string, fn func(visible bool)) {
	var last *bool
	report := func(v bool) {
		if last == nil || *last != v {
			last = &v
			fn(v)
		}
	}
	check := func() {
		v, err := PaneVisible(ctx, s, pane)
		report(v || err != nil)
	}
	for ctx.Err() == nil {
		check()
		_ = s.Subscribe(ctx, focusEvents, func(string) { check() })
		if ctx.Err() != nil {
			return
		}
		report(true)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}
