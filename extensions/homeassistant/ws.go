package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Home Assistant's WebSocket API (https://developers.home-assistant.io/docs/api/websocket): what the REST API does
// not have. The registries (entity_category, the device of an entity), the recorder's long-term statistics, the
// energy settings, and state changes as they happen (the watches, watch.go).
//
// One connection carries the commands (dialed when first needed, again after it drops); the watches hold their own.

var errWSAuth = errors.New("Home Assistant 的 WebSocket 不认这个令牌")

// wsMsg is one message from Home Assistant: a command's result, or an event of a subscription.
type wsMsg struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Event   json.RawMessage `json:"event"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// wsConn is one authenticated connection: commands by id, their answers to whoever waits, events to the
// subscription's handler.
type wsConn struct {
	c *websocket.Conn

	mu      sync.Mutex
	next    int
	pending map[int]chan wsMsg
	subs    map[int]func(json.RawMessage)
	err     error // why it closed
	done    chan struct{}
}

// wsURL is the API's address: ws(s)://host[:port][/path]/api/websocket.
func (h *ha) wsURL() string {
	u := h.base
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/api/websocket"
}

// dialWS connects and signs in with the token.
func (h *ha) dialWS(ctx context.Context) (*wsConn, error) {
	if !h.configured() {
		return nil, errNotConfigured
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, h.wsURL(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: h.http.Transport}})
	if err != nil {
		return nil, fmt.Errorf("连不上 Home Assistant 的 WebSocket（%s）：%w", h.host(), unwrapURL(err))
	}
	c.SetReadLimit(64 << 20) // the entity registry of a big home is a few MB
	read := func() (wsMsg, error) {
		var m wsMsg
		_, b, err := c.Read(dctx)
		if err == nil {
			err = json.Unmarshal(b, &m)
		}
		return m, err
	}
	m, err := read()
	if err == nil && m.Type == "auth_required" {
		b, _ := json.Marshal(map[string]string{"type": "auth", "access_token": h.token})
		if err = c.Write(dctx, websocket.MessageText, b); err == nil {
			m, err = read()
		}
	}
	switch {
	case err != nil:
		c.CloseNow()
		return nil, fmt.Errorf("Home Assistant 的 WebSocket 没有应答：%w", err)
	case m.Type == "auth_invalid":
		c.CloseNow()
		return nil, errWSAuth
	case m.Type != "auth_ok":
		c.CloseNow()
		return nil, fmt.Errorf("Home Assistant 的 WebSocket 回了 %q", m.Type)
	}
	w := &wsConn{c: c, pending: map[int]chan wsMsg{}, subs: map[int]func(json.RawMessage){}, done: make(chan struct{})}
	go w.readLoop()
	return w, nil
}

func (w *wsConn) readLoop() {
	var err error
	for {
		var b []byte
		if _, b, err = w.c.Read(context.Background()); err != nil {
			break
		}
		var m wsMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		w.mu.Lock()
		switch m.Type {
		case "result":
			if ch, ok := w.pending[m.ID]; ok {
				delete(w.pending, m.ID)
				ch <- m
			}
		case "event":
			if fn := w.subs[m.ID]; fn != nil {
				w.mu.Unlock()
				fn(m.Event)
				continue
			}
		}
		w.mu.Unlock()
	}
	w.mu.Lock()
	w.err = err
	for id, ch := range w.pending {
		close(ch)
		delete(w.pending, id)
	}
	w.mu.Unlock()
	close(w.done)
}

func (w *wsConn) closed() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *wsConn) close() { w.c.Close(websocket.StatusNormalClosure, "") }

// send sends one command (typ with params) and waits for its result; on subscribes fn gets its events.
func (w *wsConn) send(ctx context.Context, typ string, params map[string]any, out any, on func(json.RawMessage)) error {
	ch := make(chan wsMsg, 1)
	w.mu.Lock()
	if w.closed() {
		w.mu.Unlock()
		return errors.New("Home Assistant 的 WebSocket 断了")
	}
	w.next++
	id := w.next
	w.pending[id] = ch
	if on != nil {
		w.subs[id] = on
	}
	w.mu.Unlock()
	msg := map[string]any{}
	for k, v := range params {
		msg[k] = v
	}
	msg["id"], msg["type"] = id, typ
	b, err := json.Marshal(msg)
	if err == nil {
		err = w.c.Write(ctx, websocket.MessageText, b)
	}
	if err != nil {
		w.forget(id)
		return fmt.Errorf("发给 Home Assistant 的 WebSocket 出错：%w", err)
	}
	select {
	case m, ok := <-ch:
		switch {
		case !ok:
			return errors.New("Home Assistant 的 WebSocket 断了")
		case !m.Success:
			w.forget(id)
			if m.Error != nil {
				if m.Error.Code == "unauthorized" {
					return fmt.Errorf("%s：这个令牌的用户不是管理员（%s）", typ, m.Error.Message)
				}
				return fmt.Errorf("Home Assistant 不做 %s：%s（%s）", typ, clip(m.Error.Message, 200), m.Error.Code)
			}
			return fmt.Errorf("Home Assistant 不做 %s", typ)
		case out != nil:
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("Home Assistant 的 %s 读不懂：%w", typ, err)
			}
		}
		return nil
	case <-ctx.Done():
		w.forget(id)
		return ctx.Err()
	}
}

func (w *wsConn) forget(id int) {
	w.mu.Lock()
	delete(w.pending, id)
	delete(w.subs, id)
	w.mu.Unlock()
}

// WS runs one command on the shared connection (dialing it when there is none or it dropped).
func (h *ha) WS(ctx context.Context, typ string, params map[string]any, out any) error {
	h.wsMu.Lock()
	w := h.wsc
	if w == nil || w.closed() {
		var err error
		if w, err = h.dialWS(ctx); err != nil {
			h.wsMu.Unlock()
			h.note(err)
			return err
		}
		h.wsc = w
	}
	h.wsMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := w.send(cctx, typ, params, out, nil)
	if err == nil {
		h.note(nil)
	}
	return err
}

// closeWS ends the shared connection (a new configuration replaces the client).
func (h *ha) closeWS() {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	if h.wsc != nil {
		h.wsc.close()
		h.wsc = nil
	}
}
