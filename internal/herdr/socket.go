package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
)

// Caller makes a raw socket API request, for methods the herdr CLI doesn't
// expose (e.g. tab.move).
type Caller interface {
	Call(ctx context.Context, method string, params, out any) error
}

// Socket talks to Herdr's socket API (newline-delimited JSON).
type Socket struct{ Path string }

// DefaultSocket uses $HERDR_SOCKET_PATH, or ~/.config/herdr/herdr.sock.
func DefaultSocket() Socket {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return Socket{Path: p}
	}
	home, _ := os.UserHomeDir()
	return Socket{Path: filepath.Join(home, ".config", "herdr", "herdr.sock")}
}

var requestID atomic.Int64

// Call sends one request and decodes its result into out (if non-nil).
func (s Socket) Call(ctx context.Context, method string, params, out any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", s.Path)
	if err != nil {
		return fmt.Errorf("herdr socket: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req := map[string]any{"id": fmt.Sprintf("f-%d", requestID.Add(1)), "method": method, "params": params}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr %s: decode: %w", method, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("herdr %s: %s: %s", method, resp.Error.Code, resp.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("herdr %s: decode result: %w", method, err)
		}
	}
	return nil
}

// Subscribe streams events of the given kinds (e.g. "pane.focused"), calling
// fn with each event's kind, until ctx ends or Herdr closes the connection.
func (s Socket) Subscribe(ctx context.Context, kinds []string, fn func(kind string)) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", s.Path)
	if err != nil {
		return fmt.Errorf("herdr socket: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	subs := make([]map[string]string, len(kinds))
	for i, k := range kinds {
		subs[i] = map[string]string{"type": k}
	}
	req := map[string]any{"id": fmt.Sprintf("f-%d", requestID.Add(1)), "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs}}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("herdr events.subscribe: %w", err)
	}
	in := bufio.NewReader(conn)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("herdr events: %w", err)
		}
		var msg struct {
			Event string    `json:"event"`
			Error *struct{} `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("herdr events.subscribe: %s", line)
		}
		if msg.Event != "" { // the first reply only acknowledges the subscription
			fn(msg.Event)
		}
	}
}
