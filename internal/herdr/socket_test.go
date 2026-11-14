package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/seanturner026/f/internal/herdr"
)

func TestSubscribe(t *testing.T) {
	// Unix socket paths are short-limited: t.TempDir() under /var/folders can be too long.
	dir, err := os.MkdirTemp("/tmp", "herdr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan map[string]any, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var req map[string]any
		_ = json.Unmarshal(line, &req)
		got <- req
		conn.Write([]byte(`{"id":"x","result":{"type":"subscription_started"}}` + "\n"))
		conn.Write([]byte(`{"event":"tab.focused","data":{}}` + "\n"))
		conn.Write([]byte(`{"event":"pane.focused","data":{}}` + "\n"))
	}()

	var events []string
	err = herdr.Socket{Path: path}.Subscribe(context.Background(), []string{"tab.focused", "pane.focused"}, func(kind string) {
		events = append(events, kind)
	})
	if err == nil {
		t.Error("Subscribe returned nil when Herdr closed the stream; want an error so callers resubscribe")
	}
	if want := []string{"tab.focused", "pane.focused"}; !reflect.DeepEqual(events, want) {
		t.Errorf("events = %v, want %v (the acknowledgement isn't one)", events, want)
	}
	req := <-got
	subs, _ := json.Marshal(req["params"])
	if req["method"] != "events.subscribe" || string(subs) != `{"subscriptions":[{"type":"tab.focused"},{"type":"pane.focused"}]}` {
		t.Errorf("request = %v", req)
	}
}
