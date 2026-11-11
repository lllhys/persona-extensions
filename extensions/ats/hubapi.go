package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The parts of the hub the dispatcher drives: its computers and their agent sessions over the hub's REST API (with
// the token of the scopes the extension was granted), and the extension's own program on a computer over
// nodes.call.

type hubMachine struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

// hubSession is what the dispatcher reads of a session in the hub's list.
type hubSession struct {
	SessionID           string `json:"session_id"`
	State               string `json:"state"` // needs_you | working | unread | idle | offline | unknown
	Starting            bool   `json:"starting"`
	StartError          string `json:"start_error"`
	TurnError           string `json:"turn_error"`
	TurnErrorKind       string `json:"turn_error_kind"`
	PendingInteractions int    `json:"pending_interactions"`
	BackgroundWorking   bool   `json:"background_working"`
	UpdatedAt           string `json:"updated_at"`
}

type sessionRequest struct {
	MachineID      string `json:"machine_id"`
	Cwd            string `json:"cwd"`
	AgentType      string `json:"agent_type"`
	Prompt         string `json:"prompt"`
	PermissionMode string `json:"permission_mode"`
	Model          string `json:"model,omitempty"`
	TabName        string `json:"tab_name,omitempty"`
	Async          bool   `json:"async"`
}

type hubAPI interface {
	Machines(ctx context.Context) ([]hubMachine, error)
	CreateSession(ctx context.Context, req sessionRequest) (string, error)
	// Session returns nil when the hub no longer lists the session.
	Session(ctx context.Context, machineID, sessionID string) (*hubSession, error)
	LastReply(ctx context.Context, machineID, sessionID string) (string, error)
	Interrupt(ctx context.Context, machineID, sessionID string) error
	AttachTodo(ctx context.Context, todoID, machineID, sessionID string) error
}

// nodeCaller calls the extension's program on a computer.
type nodeCaller interface {
	Call(ctx context.Context, machine, method string, params, out any) error
}

type hubClient struct {
	mu     sync.Mutex
	url    string
	token  string
	client *http.Client
}

func newHubClient(hubURL, token string) *hubClient {
	return &hubClient{url: strings.TrimRight(hubURL, "/"), token: token, client: &http.Client{Timeout: 20 * time.Second}}
}

func (c *hubClient) do(ctx context.Context, method, path string, body, out any) error {
	c.mu.Lock()
	base, token := c.url, c.token
	c.mu.Unlock()
	if base == "" || token == "" {
		return fmt.Errorf("the hub gave no API address or token (permissions.hub not granted)")
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Code == "" {
			e.Code = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		return fmt.Errorf("%s %s: %s %s", method, strings.SplitN(path, "?", 2)[0], e.Code, firstLinePreview(e.Error, 200))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *hubClient) Machines(ctx context.Context) ([]hubMachine, error) {
	var out struct {
		Machines []hubMachine `json:"machines"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/machines", nil, &out)
	return out.Machines, err
}

func (c *hubClient) CreateSession(ctx context.Context, req sessionRequest) (string, error) {
	var out struct {
		Session hubSession `json:"session"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/sessions", req, &out); err != nil {
		return "", err
	}
	if out.Session.SessionID == "" {
		return "", fmt.Errorf("the hub started a session without an id")
	}
	return out.Session.SessionID, nil
}

func (c *hubClient) Session(ctx context.Context, machineID, sessionID string) (*hubSession, error) {
	var out struct {
		Sessions []hubSession `json:"sessions"`
	}
	q := url.Values{"machine_id": {machineID}, "session_id": {sessionID}}
	if err := c.do(ctx, http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Sessions {
		if out.Sessions[i].SessionID == sessionID {
			return &out.Sessions[i], nil
		}
	}
	return nil, nil
}

func sessionPath(machineID, sessionID string) string {
	return "/api/v1/sessions/" + url.PathEscape(machineID) + "/" + url.PathEscape(sessionID)
}

// LastReply is the text the agent ended its latest turn with.
func (c *hubClient) LastReply(ctx context.Context, machineID, sessionID string) (string, error) {
	var out struct {
		Items []struct {
			Role string `json:"role"`
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, sessionPath(machineID, sessionID)+"/items?turns=1", nil, &out); err != nil {
		return "", err
	}
	for i := len(out.Items) - 1; i >= 0; i-- {
		if it := out.Items[i]; it.Role == "assistant" && it.Kind == "text" {
			return it.Text, nil
		}
	}
	return "", nil
}

func (c *hubClient) Interrupt(ctx context.Context, machineID, sessionID string) error {
	return c.do(ctx, http.MethodPost, sessionPath(machineID, sessionID)+"/interrupt", nil, nil)
}

func (c *hubClient) AttachTodo(ctx context.Context, todoID, machineID, sessionID string) error {
	path := "/api/v1/todos/" + url.PathEscape(todoID) + "/agents/" + url.PathEscape(machineID) + "/" + url.PathEscape(sessionID)
	return c.do(ctx, http.MethodPut, path, nil, nil)
}

// rpcNodes calls the node program through the hub's nodes.call.
type rpcNodes struct{ hub *ext.Hub }

func (n rpcNodes) Call(ctx context.Context, machine, method string, params, out any) error {
	return n.hub.Call(ctx, "nodes.call", map[string]any{"machine": machine, "method": method, "params": params}, out)
}
