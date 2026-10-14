package webfill

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"x-gateway/internal/model"
)

type chromeFixture struct {
	conn        net.Conn
	write       sync.Mutex
	mu          sync.Mutex
	commands    []wire
	dropCurrent bool
	seen        chan wire
}

func (c *chromeFixture) send(w wire) error {
	c.write.Lock()
	defer c.write.Unlock()
	data, _ := json.Marshal(w)
	return writeFrame(c.conn, data)
}
func startBridge(t *testing.T, base string) (*chromeFixture, context.CancelFunc, <-chan error) {
	t.Helper()
	host, chrome := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NativeHost(ctx, base, host, host) }()
	c := &chromeFixture{conn: chrome, seen: make(chan wire, 32)}
	if e := c.send(wire{Type: "hello", ProfileID: "profile", BridgeID: "bridge", Tabs: []tab{{ID: 12, Origin: "https://example.test"}, {ID: 13, Origin: "https://second.test"}}}); e != nil {
		t.Fatal(e)
	}
	go func() {
		for {
			data, e := readFrame(chrome)
			if e != nil {
				return
			}
			var w wire
			if json.Unmarshal(data, &w) != nil {
				return
			}
			c.mu.Lock()
			c.commands = append(c.commands, w)
			drop := c.dropCurrent && w.Action == "current"
			c.mu.Unlock()
			c.seen <- w
			if drop {
				continue
			}
			if w.Type == "cancel" {
				_ = c.send(wire{Type: "result", ID: w.ID, OK: true})
				continue
			}
			_ = c.send(wire{Type: "result", ID: w.ID, OK: true, Document: Document{URL: "https://example.test/login?not-in-inventory", ID: "doc"}})
		}
	}()
	t.Cleanup(func() {
		cancel()
		chrome.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("native host did not stop")
		}
	})
	return c, cancel, done
}
func waitTargets(t *testing.T, base string) []Target {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		rows, e := Targets(ctx, base)
		cancel()
		if e == nil && len(rows) > 0 {
			return rows
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("bridge targets not ready")
	return nil
}
func TestNativeBridgeUsesLoadedExtensionWithoutFlowOrClear(t *testing.T) {
	base := t.TempDir()
	c, _, _ := startBridge(t, base)
	rows := waitTargets(t, base)
	if len(rows) != 2 || rows[0].ProfileID != "profile" || rows[0].BridgeID != "bridge" {
		t.Fatal(rows)
	}
	o := opts()
	o.TargetID = rows[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, e := Fill(ctx, base, o)
	if e != nil || !result.Filled || result.Submitted {
		t.Fatal(result, e)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	actions := []string{}
	for _, w := range c.commands {
		actions = append(actions, w.Action)
		if w.TabID != 12 {
			t.Fatal("wrong tab", w.TabID)
		}
	}
	if strings.Join(actions, ",") != "current,visible,visible,fill,fill" {
		t.Fatal(actions)
	}
	if c.commands[3].Document.ID != "doc" || c.commands[4].Document.ID != "doc" {
		t.Fatal("document binding missing")
	}
}
func TestNativeBridgeRejectsWrongOriginBeforeSendingValues(t *testing.T) {
	base := t.TempDir()
	c, _, _ := startBridge(t, base)
	rows := waitTargets(t, base)
	o := opts()
	o.TargetID = rows[1].ID
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, e := Fill(ctx, base, o)
	var typed *model.Error
	if !errors.As(e, &typed) || typed.Code != "ORIGIN_MISMATCH" {
		t.Fatal(e)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commands) != 0 {
		t.Fatal("wrong-origin command sent")
	}
}
func TestNativeInventoryRemovalAndStaleTarget(t *testing.T) {
	base := t.TempDir()
	c, _, _ := startBridge(t, base)
	rows := waitTargets(t, base)
	if e := c.send(wire{Type: "tabs", Tabs: []tab{{ID: 13, Origin: "https://second.test"}}}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		list, e := Targets(context.Background(), base)
		if e == nil && len(list) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inventory not updated")
		}
		time.Sleep(time.Millisecond)
	}
	o := opts()
	o.TargetID = rows[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := Fill(ctx, base, o); e == nil {
		t.Fatal("removed target accepted")
	}
}
func TestNativeDisconnectStopsPendingActionWithoutReplay(t *testing.T) {
	base := t.TempDir()
	c, _, _ := startBridge(t, base)
	rows := waitTargets(t, base)
	c.mu.Lock()
	c.dropCurrent = true
	c.mu.Unlock()
	o := opts()
	o.TargetID = rows[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, e := Fill(ctx, base, o); result <- e }()
	select {
	case <-c.seen:
	case <-ctx.Done():
		t.Fatal("no command")
	}
	c.conn.Close()
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("disconnect succeeded")
		}
	case <-ctx.Done():
		t.Fatal("pending action did not stop")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commands) != 1 || c.commands[0].Action != "current" {
		t.Fatal("action replayed", c.commands)
	}
}
func TestLongDataDirectoryHasShortPrivateSocket(t *testing.T) {
	base := filepath.Join(t.TempDir(), strings.Repeat("x", 220))
	if e := os.MkdirAll(base, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := bridgeDir(base)
	if e != nil {
		t.Fatal(e)
	}
	path, e := socketPath(dir, "0123456789abcdef")
	if e != nil || len(path) >= 104 {
		t.Fatal(path, e)
	}
	info, e := os.Stat(dir)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, e)
	}
	_, _, _ = startBridge(t, base)
	rows := waitTargets(t, base)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
}
func TestInventoryRejectsPathsQueriesAndDuplicates(t *testing.T) {
	for _, tabs := range [][]tab{{{ID: 1, Origin: "https://example.test/path"}}, {{ID: 1, Origin: "https://example.test?secret=bad"}}, {{ID: 1, Origin: "https://example.test"}, {ID: 1, Origin: "https://example.test"}}} {
		if _, e := inventory(tabs); e == nil {
			t.Fatal("invalid inventory accepted")
		}
	}
}
