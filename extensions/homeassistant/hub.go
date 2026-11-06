package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// The hub's own API, for the watches (watch.go): a notification on the user's phone (POST /api/v1/notify, scope
// notify) and a new conversation of the assistant (POST /api/v1/assistant/conversations, scope assistant.ask), with
// the token the hub gives in initialize (only those scopes, new each start).

type hubAPI struct {
	url, token string
	scopes     []string
	http       *http.Client
}

func (h *hubAPI) can(scope string) bool {
	return h != nil && h.url != "" && h.token != "" && slices.Contains(h.scopes, scope)
}

// errNoScope is a hub that did not grant scope: an older hub, or the user did not accept the permission.
func errNoScope(scope string) error {
	return fmt.Errorf("hub 没给扩展「%s」这项权限（hub 太旧，或者安装 / 升级时没同意）", map[string]string{
		"notify": "给你的设备发通知", "assistant.ask": "给 Persona 开对话"}[scope])
}

func (h *hubAPI) post(ctx context.Context, scope, path string, body, out any) error {
	if !h.can(scope) {
		return errNoScope(scope)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.url, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	c := h.http
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 hub：%w", unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("hub 回了 HTTP %d：%s", resp.StatusCode, clip(oneLine(string(raw)), 200))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// notice is a notification for the phone.
type notice struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Level string `json:"level,omitempty"` // active, time-sensitive
	ID    string `json:"id,omitempty"`    // a newer one with the same id replaces it on the phone
	UI    string `json:"ui,omitempty"`    // the page a tap opens
	State string `json:"state,omitempty"`
}

func (h *hubAPI) notify(ctx context.Context, n notice) error {
	var out struct {
		Sent   bool   `json:"sent"`
		Reason string `json:"reason"`
	}
	if err := h.post(ctx, "notify", "/api/v1/notify", n, &out); err != nil {
		return err
	}
	if !out.Sent {
		return errors.New("hub 没发出通知：" + orDash(out.Reason))
	}
	return nil
}

// ask opens a conversation of the assistant with prompt; it answers the conversation's id.
func (h *hubAPI) ask(ctx context.Context, prompt string) (string, error) {
	var out struct {
		Session json.RawMessage `json:"session"`
	}
	if err := h.post(ctx, "assistant.ask", "/api/v1/assistant/conversations", map[string]any{"prompt": prompt}, &out); err != nil {
		return "", err
	}
	var s struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out.Session, &s) == nil && s.ID != "" {
		return s.ID, nil
	}
	var str string
	_ = json.Unmarshal(out.Session, &str)
	return str, nil
}
