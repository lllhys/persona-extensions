package remind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Scopes the hub grants an extension for the reminder.
const (
	ScopeNotify = "notify"
	ScopeTodos  = "todos.write"
)

// API is what the reminder needs from the hub. Hub implements it; tests use a fake.
type API interface {
	Can(scope string) bool
	Notify(ctx context.Context, n Notice) error
	CreateTodo(ctx context.Context, t Todo) (id string, err error)
	DoneTodo(ctx context.Context, id string) error // a 404 is not an error
}

// Notice is a notification on the user's phone.
type Notice struct{ Title, Body, ID, UI string }

// Todo is a to-do the hub creates for the user.
type Todo struct{ Title, Notes, DueDate string }

// Hub talks to the hub's API with the token the hub gave at start.
type Hub struct {
	base, token string
	scopes      []string
	http        *http.Client
}

// NewHub returns a client for the hub at base, authorised by token, with the scopes the hub granted.
func NewHub(base, token string, scopes []string) *Hub {
	return &Hub{
		base:   base,
		token:  token,
		scopes: scopes,
		http:   &http.Client{Timeout: 10 * time.Second, Transport: http.DefaultTransport},
	}
}

// Can reports whether the hub is configured and granted scope.
func (h *Hub) Can(scope string) bool {
	return h != nil && h.base != "" && h.token != "" && slices.Contains(h.scopes, scope)
}

var scopeNames = map[string]string{
	ScopeNotify: "给你的设备发通知",
	ScopeTodos:  "管理待办",
}

// errNoScope is a hub that did not grant scope: an older hub, or the user did not accept the permission.
func errNoScope(scope string) error {
	name := scopeNames[scope]
	if name == "" {
		name = scope
	}
	return fmt.Errorf("hub 没给扩展「%s」这项权限（hub 太旧，或者安装 / 升级时没同意）", name)
}

// httpError is a reply with a status of 300 or more.
type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("hub 回了 HTTP %d：%s", e.status, e.body)
}

// call sends one request for scope. A reply with a status of 300 or more is an *httpError; a reply with
// a body is decoded into out when out is not nil.
func (h *Hub) call(ctx context.Context, scope, method, path string, body, out any) error {
	if !h.Can(scope) {
		return errNoScope(scope)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.base, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 hub：%w", unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读 hub 的回复：%w", err)
	}
	if resp.StatusCode >= 300 {
		return &httpError{status: resp.StatusCode, body: clipBytes(oneLine(string(raw)), 200)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Notify sends a notification. A hub that answers sent=false is not an error.
func (h *Hub) Notify(ctx context.Context, n Notice) error {
	body := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Level string `json:"level"`
		ID    string `json:"id,omitempty"`
		UI    string `json:"ui,omitempty"`
	}{Title: n.Title, Body: n.Body, Level: "active", ID: n.ID, UI: n.UI}
	return h.call(ctx, ScopeNotify, http.MethodPost, "/api/v1/notify", body, nil)
}

// CreateTodo creates a to-do and returns the hub's id for it.
func (h *Hub) CreateTodo(ctx context.Context, t Todo) (string, error) {
	body := struct {
		Title   string `json:"title"`
		Notes   string `json:"notes"`
		DueDate string `json:"due_date"`
	}{Title: t.Title, Notes: t.Notes, DueDate: t.DueDate}
	var out struct {
		Todo struct {
			ID string `json:"id"`
		} `json:"todo"`
	}
	if err := h.call(ctx, ScopeTodos, http.MethodPost, "/api/v1/todos", body, &out); err != nil {
		return "", err
	}
	if out.Todo.ID == "" {
		return "", errors.New("hub 没回待办编号")
	}
	return out.Todo.ID, nil
}

// DoneTodo marks a to-do done. A to-do the hub no longer has (404) is not an error.
func (h *Hub) DoneTodo(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("待办编号是空的")
	}
	err := h.call(ctx, ScopeTodos, http.MethodPatch, "/api/v1/todos/"+url.PathEscape(id), map[string]bool{"done": true}, nil)
	var he *httpError
	if errors.As(err, &he) && he.status == http.StatusNotFound {
		return nil
	}
	return err
}

// unwrapURL drops the request URL from a transport error, so the message does not repeat the address.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// clipBytes cuts s to at most n bytes without splitting a UTF-8 sequence.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
