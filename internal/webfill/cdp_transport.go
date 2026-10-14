package webfill

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"x-gateway/internal/model"
)

type cdpReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type cdpClient struct {
	conn    net.Conn
	send    sync.Mutex
	mu      sync.Mutex
	pending map[uint64]chan cdpReply
	closed  chan struct{}
	once    sync.Once
	seq     atomic.Uint64
}

func connectCDP(ctx context.Context, ep string) (*cdpClient, error) {
	if e := endpoint(ep); e != nil {
		return nil, e
	}
	if strings.HasPrefix(ep, "http:") {
		req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(ep, "/")+"/json/version", nil)
		if e != nil {
			return nil, model.Err("BROWSER_UNAVAILABLE", "invalid CDP endpoint")
		}
		hc := http.Client{Timeout: 5 * time.Second}
		res, e := hc.Do(req)
		if e != nil {
			return nil, model.Err("BROWSER_UNAVAILABLE", "cannot connect to CDP")
		}
		defer res.Body.Close()
		var v struct {
			URL string `json:"webSocketDebuggerUrl"`
		}
		if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&v) != nil || endpoint(v.URL) != nil {
			return nil, model.Err("BROWSER_UNAVAILABLE", "invalid CDP endpoint response")
		}
		ep = v.URL
	}
	u, err := url.Parse(ep)
	if err != nil || !strings.HasPrefix(u.Path, "/devtools/browser/") || strings.TrimPrefix(u.Path, "/devtools/browser/") == "" || strings.Contains(strings.TrimPrefix(u.Path, "/devtools/browser/"), "/") {
		return nil, model.Err("BROWSER_UNAVAILABLE", "CDP endpoint is not a browser WebSocket")
	}
	conn, reader, _, e := ws.Dial(ctx, ep)
	if e != nil {
		return nil, model.Err("BROWSER_UNAVAILABLE", "cannot attach CDP socket")
	}
	c := &cdpClient{conn: conn, pending: map[uint64]chan cdpReply{}, closed: make(chan struct{})}
	go c.read(reader)
	return c, nil
}
func (c *cdpClient) Close() { c.once.Do(func() { close(c.closed); _ = c.conn.Close() }) }
func (c *cdpClient) read(reader *bufio.Reader) {
	defer c.Close()
	var input io.ReadWriter = c.conn
	if reader != nil {
		input = struct {
			io.Reader
			io.Writer
		}{reader, c.conn}
	}
	for {
		b, _, e := wsutil.ReadServerData(input)
		if e != nil {
			return
		}
		var r cdpReply
		if json.Unmarshal(b, &r) != nil {
			return
		}
		if r.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.pending[r.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- r:
			default:
			}
		}
	}
}
func (c *cdpClient) call(ctx context.Context, method string, params any, session string, out any) error {
	if ctx.Err() != nil {
		return model.Err("CANCELLED", "browser operation cancelled")
	}
	id := c.seq.Add(1)
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if session != "" {
		request["sessionId"] = session
	}
	b, _ := json.Marshal(request)
	c.send.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	e := wsutil.WriteClientMessage(c.conn, ws.OpText, b)
	c.send.Unlock()
	if e != nil {
		return model.Err("BROWSER_UNAVAILABLE", "CDP transport disconnected")
	}
	select {
	case <-ctx.Done():
		return model.Err("CANCELLED", "browser operation cancelled")
	case <-c.closed:
		return model.Err("BROWSER_UNAVAILABLE", "CDP transport disconnected")
	case r := <-ch:
		if len(r.Error) > 0 {
			return model.Err("PAGE_OPERATION_FAILED", "browser page operation failed")
		}
		if out != nil && json.Unmarshal(r.Result, out) != nil {
			return model.Err("PAGE_OPERATION_FAILED", "invalid CDP response")
		}
		return nil
	}
}
