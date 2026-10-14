package webfill

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"x-gateway/internal/model"
)

type tab struct {
	ID     int    `json:"tab_id"`
	Origin string `json:"origin"`
}
type wire struct {
	Type       string   `json:"type"`
	ID         string   `json:"id,omitempty"`
	CancelID   string   `json:"cancel_id,omitempty"`
	ProfileID  string   `json:"profile_id,omitempty"`
	BridgeID   string   `json:"bridge_id,omitempty"`
	Tabs       []tab    `json:"tabs,omitempty"`
	TabID      int      `json:"tab_id,omitempty"`
	Action     string   `json:"action,omitempty"`
	Selector   string   `json:"selector,omitempty"`
	Value      string   `json:"value,omitempty"`
	Document   Document `json:"document,omitempty"`
	DeadlineMS int64    `json:"deadline_ms,omitempty"`
	OK         bool     `json:"ok"`
}
type request struct {
	Operation string  `json:"operation"`
	Options   Options `json:"options,omitempty"`
}
type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *model.Error    `json:"error,omitempty"`
}

func bridgeDir(base string) (string, error) {
	if base == "" {
		return "", model.Err("INVALID_ARGUMENT", "缺少配置目录")
	}
	abs, e := filepath.Abs(base)
	if e != nil {
		return "", model.Err("INVALID_ARGUMENT", "配置目录无效")
	}
	if real, e := filepath.EvalSymlinks(abs); e == nil {
		abs = real
	}
	sum := sha256.Sum256([]byte(abs))
	// /tmp keeps socket names short even for macOS's 104-byte Unix path limit.
	dir := filepath.Join("/tmp", fmt.Sprintf("xweb-%d-%x", os.Getuid(), sum[:8]))
	if e = os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
		return "", model.Err("BRIDGE_UNAVAILABLE", "无法创建浏览器通信目录")
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return "", model.Err("UNSAFE_PERMISSIONS", "浏览器通信目录必须为私有目录")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return "", model.Err("UNSAFE_PERMISSIONS", "浏览器通信目录不属于当前用户")
	}
	return dir, nil
}
func socketPath(dir, id string) (string, error) {
	if len(id) != 16 {
		return "", model.Err("TARGET_NOT_FOUND", "浏览器target无效")
	}
	if _, e := hex.DecodeString(id); e != nil {
		return "", model.Err("TARGET_NOT_FOUND", "浏览器target无效")
	}
	return filepath.Join(dir, id+".sock"), nil
}
func inventory(tabs []tab) (map[int]string, error) {
	if len(tabs) > 1024 {
		return nil, model.Err("PROTOCOL_ERROR", "标签页库存过大")
	}
	out := make(map[int]string, len(tabs))
	for _, t := range tabs {
		origin, e := Origin(t.Origin)
		// Origin also accepts URLs; equality admits only a bare origin, without paths/query/userinfo.
		if e != nil || origin != t.Origin || t.ID <= 0 {
			return nil, model.Err("PROTOCOL_ERROR", "标签页库存无效")
		}
		if _, exists := out[t.ID]; exists {
			return nil, model.Err("PROTOCOL_ERROR", "标签页重复")
		}
		out[t.ID] = t.Origin
	}
	return out, nil
}

type nativeBridge struct {
	id, profileID, bridgeID string
	out                     io.Writer
	mu                      sync.Mutex
	send                    sync.Mutex
	tabs                    map[int]string
	busy                    map[int]bool
	pending                 map[string]chan wire
	clients                 map[net.Conn]bool
	closed                  chan struct{}
	once                    sync.Once
	seq                     atomic.Uint64
}

func (b *nativeBridge) close() {
	b.once.Do(func() {
		close(b.closed)
		b.mu.Lock()
		defer b.mu.Unlock()
		for c := range b.clients {
			c.Close()
		}
	})
}
func (b *nativeBridge) targets() []Target {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Target{}
	for id, origin := range b.tabs {
		out = append(out, Target{ID: "extension:" + b.id + ":" + strconv.Itoa(id), Origin: origin, Transport: "extension", ProfileID: b.profileID, BridgeID: b.bridgeID, TabID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TabID < out[j].TabID })
	return out
}

// NativeHost is Chrome-owned. It serves IPC only while its Native Messaging
// stdin remains connected; it never starts a separate daemon or stores actions.
func NativeHost(ctx context.Context, base string, in io.Reader, out io.Writer) error {
	dir, e := bridgeDir(base)
	if e != nil {
		return e
	}
	if closer, ok := in.(io.Closer); ok {
		stop := context.AfterFunc(ctx, func() { closer.Close() })
		defer stop()
	}
	first := make(chan struct {
		message wire
		err     error
	}, 1)
	go func() {
		data, e := readFrame(in)
		var hello wire
		if e == nil {
			e = json.Unmarshal(data, &hello)
		}
		first <- struct {
			message wire
			err     error
		}{hello, e}
	}()
	var hello wire
	select {
	case <-ctx.Done():
		return nil
	case result := <-first:
		if result.err != nil {
			return model.Err("BRIDGE_DISCONNECTED", "Chrome连接已关闭")
		}
		hello = result.message
	}
	if hello.Type != "hello" || hello.ProfileID == "" || hello.BridgeID == "" || len(hello.ProfileID) > 128 || len(hello.BridgeID) > 128 {
		return model.Err("PROTOCOL_ERROR", "Chrome握手无效")
	}
	tabs, e := inventory(hello.Tabs)
	if e != nil {
		return e
	}
	random := make([]byte, 8)
	if _, e = rand.Read(random); e != nil {
		return model.Err("BRIDGE_UNAVAILABLE", "无法生成浏览器连接ID")
	}
	id := hex.EncodeToString(random)
	path, _ := socketPath(dir, id)
	listener, e := net.Listen("unix", path)
	if e != nil {
		return model.Err("BRIDGE_UNAVAILABLE", "无法监听浏览器通信socket")
	}
	defer listener.Close()
	defer os.Remove(path)
	if e = os.Chmod(path, 0600); e != nil {
		return model.Err("BRIDGE_UNAVAILABLE", "无法保护浏览器通信socket")
	}
	b := &nativeBridge{id: id, profileID: hello.ProfileID, bridgeID: hello.BridgeID, out: out, tabs: tabs, busy: map[int]bool{}, pending: map[string]chan wire{}, clients: map[net.Conn]bool{}, closed: make(chan struct{})}
	defer b.close()
	readDone := make(chan error, 1)
	go func() {
		for {
			data, e := readFrame(in)
			if e != nil {
				readDone <- e
				return
			}
			var w wire
			if json.Unmarshal(data, &w) != nil {
				readDone <- errors.New("invalid frame")
				return
			}
			if w.Type == "tabs" {
				next, e := inventory(w.Tabs)
				if e != nil {
					readDone <- e
					return
				}
				b.mu.Lock()
				b.tabs = next
				b.mu.Unlock()
				continue
			}
			if w.Type != "result" {
				readDone <- errors.New("unexpected frame")
				return
			}
			b.mu.Lock()
			reply := b.pending[w.ID]
			b.mu.Unlock()
			if reply != nil {
				select {
				case reply <- w:
				default:
				}
			}
		}
	}()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			b.mu.Lock()
			b.clients[c] = true
			b.mu.Unlock()
			go func() {
				defer c.Close()
				defer func() { b.mu.Lock(); delete(b.clients, c); b.mu.Unlock() }()
				b.serve(ctx, c)
			}()
		}
	}()
	select {
	case <-ctx.Done():
		return nil
	case e := <-readDone:
		if errors.Is(e, io.EOF) {
			return nil
		}
		return model.Err("BRIDGE_DISCONNECTED", "Chrome连接已关闭")
	}
}
func (b *nativeBridge) serve(ctx context.Context, c net.Conn) {
	c.SetDeadline(time.Now().Add(20 * time.Second))
	data, e := readFrame(c)
	if e != nil {
		return
	}
	var r request
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return
	}
	clear(data)
	var value any
	switch r.Operation {
	case "targets":
		value = b.targets()
	case "fill":
		opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		// Detect a disconnected caller without treating it as a request to replay.
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			var one [1]byte
			_, e := c.Read(one[:])
			if e != nil {
				select {
				case <-stop:
				default:
					cancel()
				}
			}
		}()
		value, e = b.fill(opCtx, r.Options)
	default:
		e = model.Err("INVALID_ARGUMENT", "未知浏览器操作")
	}
	reply := response{OK: e == nil}
	if e != nil {
		var typed *model.Error
		if errors.As(e, &typed) {
			reply.Error = typed
		} else {
			reply.Error = model.Err("PAGE_OPERATION_FAILED", "页面操作失败")
		}
	} else {
		reply.Result, _ = json.Marshal(value)
	}
	data, _ = json.Marshal(reply)
	_ = writeFrame(c, data)
}
func parseTarget(id string) (string, int, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "extension" {
		return "", 0, model.Err("TARGET_NOT_FOUND", "需要targets返回的明确target_id")
	}
	n, e := strconv.Atoi(parts[2])
	if e != nil || n <= 0 || strconv.Itoa(n) != parts[2] {
		return "", 0, model.Err("TARGET_NOT_FOUND", "标签页ID无效")
	}
	if _, e = socketPath("", parts[1]); e != nil {
		return "", 0, e
	}
	return parts[1], n, nil
}
func (b *nativeBridge) fill(ctx context.Context, o Options) (Result, error) {
	result := Result{TargetID: o.TargetID}
	if e := validate(o); e != nil {
		return result, e
	}
	id, tabID, e := parseTarget(o.TargetID)
	if e != nil || id != b.id {
		return result, model.Err("TARGET_NOT_FOUND", "浏览器连接已变化，请重新查询targets")
	}
	b.mu.Lock()
	origin, exists := b.tabs[tabID]
	if !exists || origin != o.Origin {
		b.mu.Unlock()
		return result, model.Err("ORIGIN_MISMATCH", "标签页与凭据网站不一致")
	}
	if b.busy[tabID] {
		b.mu.Unlock()
		return result, model.Err("TARGET_BUSY", "该标签页正在填写")
	}
	b.busy[tabID] = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.busy, tabID); b.mu.Unlock() }()
	return run(ctx, &nativeDriver{bridge: b, tabID: tabID, origin: o.Origin}, o)
}

type nativeDriver struct {
	bridge *nativeBridge
	tabID  int
	origin string
}

func (d *nativeDriver) call(ctx context.Context, w wire) (wire, error) {
	b := d.bridge
	if ctx.Err() != nil {
		return wire{}, model.Err("CANCELLED", "填写已取消")
	}
	b.mu.Lock()
	origin, exists := b.tabs[d.tabID]
	b.mu.Unlock()
	if !exists || origin != d.origin {
		return wire{}, model.Err("ORIGIN_MISMATCH", "标签页网站已变化")
	}
	w.Type = "command"
	w.ID = strconv.FormatUint(b.seq.Add(1), 10)
	w.TabID = d.tabID
	deadline, _ := ctx.Deadline()
	w.DeadlineMS = deadline.UnixMilli()
	ch := make(chan wire, 1)
	b.mu.Lock()
	b.pending[w.ID] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, w.ID); b.mu.Unlock() }()
	data, _ := json.Marshal(w)
	b.send.Lock()
	e := writeFrame(b.out, data)
	clear(data)
	b.send.Unlock()
	if e != nil {
		return wire{}, model.Err("BRIDGE_DISCONNECTED", "Chrome连接已关闭")
	}
	select {
	case <-ctx.Done():
		cancel, _ := json.Marshal(wire{Type: "cancel", ID: "cancel-" + w.ID, CancelID: w.ID, TabID: d.tabID})
		b.send.Lock()
		_ = writeFrame(b.out, cancel)
		b.send.Unlock()
		return wire{}, model.Err("CANCELLED", "填写已取消，不自动重试")
	case <-b.closed:
		return wire{}, model.Err("BRIDGE_DISCONNECTED", "Chrome连接已关闭")
	case reply := <-ch:
		if !reply.OK {
			return wire{}, model.Err("PAGE_OPERATION_FAILED", "指定selector不可用或页面已变化")
		}
		return reply, nil
	}
}
func (d *nativeDriver) current(ctx context.Context) (Document, error) {
	r, e := d.call(ctx, wire{Action: "current"})
	return r.Document, e
}
func (d *nativeDriver) visible(ctx context.Context, s string) error {
	_, e := d.call(ctx, wire{Action: "visible", Selector: s})
	return e
}
func (d *nativeDriver) fill(ctx context.Context, s, v string, doc Document) error {
	_, e := d.call(ctx, wire{Action: "fill", Selector: s, Value: v, Document: doc})
	return e
}
func (d *nativeDriver) click(ctx context.Context, s string, doc Document) error {
	_, e := d.call(ctx, wire{Action: "click", Selector: s, Document: doc})
	return e
}
func bridgeRPC(ctx context.Context, path string, r request, out any) error {
	info, e := os.Lstat(path)
	if e != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return model.Err("BRIDGE_UNAVAILABLE", "浏览器连接不可用")
	}
	c, e := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if e != nil {
		return model.Err("BRIDGE_UNAVAILABLE", "浏览器连接不可用")
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		c.SetDeadline(deadline)
	}
	data, e := json.Marshal(r)
	if e == nil {
		e = writeFrame(c, data)
	}
	clear(data)
	if e != nil {
		return model.Err("BRIDGE_DISCONNECTED", "浏览器连接已关闭")
	}
	data, e = readFrame(c)
	if e != nil {
		return model.Err("BRIDGE_DISCONNECTED", "浏览器连接已关闭")
	}
	var reply response
	if json.Unmarshal(data, &reply) != nil {
		return model.Err("PROTOCOL_ERROR", "浏览器响应无效")
	}
	if !reply.OK {
		if reply.Error != nil {
			return reply.Error
		}
		return model.Err("PAGE_OPERATION_FAILED", "填写失败")
	}
	if out != nil && json.Unmarshal(reply.Result, out) != nil {
		return model.Err("PROTOCOL_ERROR", "浏览器响应无效")
	}
	return nil
}
func Targets(ctx context.Context, base string) ([]Target, error) {
	dir, e := bridgeDir(base)
	if e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil, model.Err("BRIDGE_UNAVAILABLE", "无法读取浏览器连接")
	}
	targets := []Target{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sock") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".sock")
		path, e := socketPath(dir, id)
		if e != nil {
			continue
		}
		child, cancel := context.WithTimeout(ctx, time.Second)
		var rows []Target
		e = bridgeRPC(child, path, request{Operation: "targets"}, &rows)
		cancel()
		if e == nil {
			targets = append(targets, rows...)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets, nil
}
func Fill(ctx context.Context, base string, o Options) (Result, error) {
	result := Result{TargetID: o.TargetID}
	if e := validate(o); e != nil {
		return result, e
	}
	dir, e := bridgeDir(base)
	if e != nil {
		return result, e
	}
	id, _, e := parseTarget(o.TargetID)
	if e != nil {
		return result, e
	}
	path, e := socketPath(dir, id)
	if e != nil {
		return result, e
	}
	e = bridgeRPC(ctx, path, request{Operation: "fill", Options: o}, &result)
	return result, e
}
