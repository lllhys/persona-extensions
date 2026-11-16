// Package hub is the extension's view of the hub's API: the machine list, one machine's token usage, the gateway
// account list and a notification. It uses the token the hub handed the extension, whose scopes are machines.read and
// notify; the gateway list is an extension method, so it goes through the process protocol instead.
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

// Machine is one computer the hub lists.
type Machine struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

// Series is one machine's usage over a range, as GET /api/v1/machines/{id}/usage answers it.
type Series struct {
	MachineID string  `json:"machine_id"`
	Range     string  `json:"range"`
	Step      int     `json:"step"`
	Points    []Point `json:"points"`
}

// Point is one step of a series. Step is the step's length in seconds, copied from the series, so a point knows its
// own minutes.
type Point struct {
	T    int64 `json:"t"`
	Step int   `json:"-"`
	In   int64 `json:"in"`
	CW   int64 `json:"cache_write"`
	CR   int64 `json:"cache_read"`
	Out  int64 `json:"out"`
	Req  int   `json:"req"`
	Gap  bool  `json:"gap,omitempty"`
}

// Share is one model's or launcher's sums, used only to split a point back into minutes.
type Share struct {
	Name string `json:"name"`
	In   int64  `json:"in"`
	CW   int64  `json:"cache_write"`
	CR   int64  `json:"cache_read"`
	Out  int64  `json:"out"`
	Req  int    `json:"req"`
}

// GatewayAccounts is the answer of usage.gateway_accounts.
type GatewayAccounts struct {
	Sources  []GatewaySource  `json:"sources"`
	Accounts []GatewayAccount `json:"accounts"`
}

// GatewaySource is one usage source.
type GatewaySource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Error    string `json:"error"`
	Listed   int    `json:"listed"`
	Complete bool   `json:"complete"`
}

// GatewayAccount is one account of a gateway.
type GatewayAccount struct {
	Source   string          `json:"source"`
	Name     string          `json:"name"`
	Provider string          `json:"provider"`
	Status   string          `json:"status"`
	Stale    bool            `json:"stale"`
	Windows  []GatewayWindow `json:"windows"`
}

// GatewayWindow is one quota window.
type GatewayWindow struct {
	Kind      string   `json:"kind"`
	UsedRatio *float64 `json:"used_ratio"`
}

// Client talks to the hub.
type Client struct {
	base, token string
	http        *http.Client
	call        func(ctx context.Context, method string, out any) error // the extension methods; nil when there is no hub
}

// New returns a client for the hub at base, authorised by token. call runs an extension method such as
// usage.gateway_accounts; nil when the program has no hub connection (the assistant's server never has one).
func New(base, token string, call func(ctx context.Context, method string, out any) error) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
		call:  call,
	}
}

// NewFromHub returns a client on the connection the hub gave the program and the token it granted.
func NewFromHub(h *ext.Hub, base, token string) *Client {
	return New(base, token, func(ctx context.Context, method string, out any) error {
		return h.Call(ctx, method, map[string]any{}, out)
	})
}

// Usable reports whether the client can call the hub's HTTP API.
func (c *Client) Usable() bool { return c != nil && c.base != "" && c.token != "" }

// Machines lists the computers.
func (c *Client) Machines(ctx context.Context) ([]Machine, error) {
	var out struct {
		Machines []Machine `json:"machines"`
	}
	if err := c.get(ctx, "/api/v1/machines", &out); err != nil {
		return nil, err
	}
	return out.Machines, nil
}

// Usage reads one machine's usage over a range (1h, 24h or 7d). model and launcher narrow it; launcher "-" is the
// usage with no launcher.
func (c *Client) Usage(ctx context.Context, id, rng, model, launcher string) (Series, error) {
	q := url.Values{"range": {rng}}
	if model != "" {
		q.Set("model", model)
	}
	if launcher != "" {
		q.Set("launcher", launcher)
	}
	var out struct {
		Usage Series `json:"usage"`
	}
	err := c.get(ctx, "/api/v1/machines/"+url.PathEscape(id)+"/usage?"+q.Encode(), &out)
	for i := range out.Usage.Points {
		out.Usage.Points[i].Step = out.Usage.Step
	}
	return out.Usage, err
}

// Shares reads one machine's per-model and per-launcher sums over a range.
func (c *Client) Shares(ctx context.Context, id, rng string) (models, launchers []Share, err error) {
	var out struct {
		Usage struct {
			Models    []Share `json:"models"`
			Launchers []Share `json:"launchers"`
		} `json:"usage"`
	}
	err = c.get(ctx, "/api/v1/machines/"+url.PathEscape(id)+"/usage?range="+url.QueryEscape(rng), &out)
	return out.Usage.Models, out.Usage.Launchers, err
}

// Pull reads one machine's usage over rng and returns it as minute buckets, one per model and launcher. It asks for
// the range once to learn the models and launchers, then once per pair, so each bucket's totals are the hub's own
// numbers for that pair. launcher "-" in a share's name is the usage with no launcher.
func (c *Client) Pull(ctx context.Context, id, rng string) ([]store.Bucket, error) {
	models, launchers, err := c.Shares(ctx, id, rng)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, nil
	}
	if len(launchers) == 0 {
		launchers = []Share{{Name: "-"}}
	}
	var out []store.Bucket
	for _, m := range models {
		for _, l := range launchers {
			s, err := c.Usage(ctx, id, rng, m.Name, l.Name)
			if err != nil {
				return nil, err
			}
			name := l.Name
			if name == "-" {
				name = ""
			}
			out = append(out, bucketsOf(s, m.Name, name)...)
		}
	}
	return out, nil
}

// GatewayAccounts reads the gateway account list. ok is false when the hub has no such method or refused it, so the
// caller hides that part of the page instead of showing an error.
func (c *Client) GatewayAccounts(ctx context.Context) (GatewayAccounts, bool) {
	var out GatewayAccounts
	if c == nil || c.call == nil {
		return out, false
	}
	if err := c.call(ctx, "usage.gateway_accounts", &out); err != nil {
		return GatewayAccounts{}, false
	}
	if len(out.Accounts) == 0 && len(out.Sources) == 0 {
		return out, false
	}
	return out, true
}

// Notify sends one notification. A hub that answers sent=false (a muted device) is not an error.
func (c *Client) Notify(ctx context.Context, title, body, id string) error {
	return c.post(ctx, "/api/v1/notify", map[string]string{
		"title": title, "body": body, "level": "active", "id": id,
	})
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPost, path, body, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if !c.Usable() {
		return errors.New("没有 hub 地址或令牌")
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 hub：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("hub 回了 HTTP %d：%s", resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// bucketsOf turns one filtered series into minute buckets of one model and launcher. A point covers step seconds,
// so its tokens are spread evenly over its minutes; the first minute keeps the remainder and the requests, and the
// minutes add back to the point.
func bucketsOf(s Series, model, launcher string) []store.Bucket {
	var out []store.Bucket
	for _, p := range s.Points {
		if p.Gap || pointTotal(p) == 0 {
			continue
		}
		minutes := p.Step / 60
		if minutes < 1 {
			minutes = 1
		}
		for k := 0; k < minutes; k++ {
			b := store.Bucket{
				Minute:     p.T + int64(k*60),
				Model:      model,
				Launcher:   launcher,
				Input:      part(p.In, minutes, k),
				CacheWrite: part(p.CW, minutes, k),
				CacheRead:  part(p.CR, minutes, k),
				Output:     part(p.Out, minutes, k),
			}
			if k == 0 {
				b.Requests = p.Req
			}
			if b.Total() == 0 && b.Requests == 0 {
				continue
			}
			out = append(out, b)
		}
	}
	return out
}

// part is one minute's share of a point's total. The first minute keeps the remainder.
func part(total int64, minutes, minute int) int64 {
	if total == 0 {
		return 0
	}
	each := total / int64(minutes)
	if minute == 0 {
		return total - int64(minutes-1)*each
	}
	return each
}

func pointTotal(p Point) int64 { return p.In + p.CW + p.CR + p.Out }
