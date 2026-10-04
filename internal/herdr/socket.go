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
