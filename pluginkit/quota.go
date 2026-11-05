package pluginkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The quota extension point (the host's quota contract): the report a quota plugin answers with, and sending
// requests to the launcher's gateway through the hub. The plugin never sees the key: it writes KeyPlaceholder where the
// key goes, and whoever holds the key puts it in.

// KeyPlaceholder stands for the launcher's key.
const KeyPlaceholder = "${launcher_key}"

// Report is one key's quota (Persona's server/internal/quota.Report, on the wire).
type Report struct {
	Gateway     string    `json:"gateway,omitempty"`
	GatewayName string    `json:"gateway_name,omitempty"`
	KeyName     string    `json:"key_name,omitempty"`
	Usage       []Usage   `json:"usage,omitempty"`
	Budget      []Window  `json:"budget,omitempty"`
	Pools       []Pool    `json:"pools,omitempty"`
	Note        string    `json:"note,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
}

type Usage struct {
	Label    string   `json:"label"`
	Requests int64    `json:"requests"`
	Tokens   int64    `json:"tokens"`
	CostUSD  *float64 `json:"cost_usd,omitempty"`
}

type Pool struct {
	Name    string         `json:"name"`
	State   string         `json:"state,omitempty"`
	Detail  string         `json:"detail,omitempty"`
	Windows []Window       `json:"windows,omitempty"`
	Models  []ModelWindows `json:"models,omitempty"`
}

type ModelWindows struct {
	Model   string   `json:"model"`
	Windows []Window `json:"windows"`
}

type Window struct {
	Label     string     `json:"label"`
	UsedRatio *float64   `json:"used_ratio,omitempty"`
	Used      *float64   `json:"used,omitempty"`
	Limit     *float64   `json:"limit,omitempty"`
	Unit      string     `json:"unit,omitempty"`
	ResetsAt  *time.Time `json:"resets_at,omitempty"`
	Exhausted bool       `json:"exhausted,omitempty"`
}

// QuotaQuery is quota.query's parameters.
type QuotaQuery struct {
	Contribution string `json:"contribution"`
	QueryID      string `json:"query_id"`
	BaseURL      string `json:"base_url"`
	TZ           string `json:"tz"`
}

// Response is what the gateway answered (the key taken out).
type Response struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// Send sends a request to the launcher's gateway through the hub (quota.http): path is under the gateway's root.
func (q QuotaQuery) Send(ctx context.Context, hub *Hub, method, path string, headers map[string]string, body string) (Response, error) {
	var r Response
	err := hub.Call(ctx, "quota.http", map[string]any{"query_id": q.QueryID, "method": method, "url": path, "headers": headers, "body": body}, &r)
	return r, err
}

// JSON decodes a 2xx answer, or says what the gateway said.
func (r Response) JSON(out any) error {
	if r.Status/100 != 2 {
		var e struct {
			Error   any    `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(r.Body), &e)
		msg := e.Message
		if m, ok := e.Error.(map[string]any); ok {
			if s, ok := m["message"].(string); ok {
				msg = s
			}
		} else if s, ok := e.Error.(string); ok && msg == "" {
			msg = s
		}
		if msg == "" {
			msg = http.StatusText(r.Status)
		}
		return fmt.Errorf("HTTP %d：%s", r.Status, msg)
	}
	return json.Unmarshal([]byte(r.Body), out)
}

// Cookies are the cookies a response set, as a Cookie header's value.
func (r Response) Cookies() string {
	var out []string
	for k, vs := range r.Headers {
		if !strings.EqualFold(k, "Set-Cookie") {
			continue
		}
		for _, v := range vs {
			if c, _, _ := strings.Cut(v, ";"); c != "" {
				out = append(out, strings.TrimSpace(c))
			}
		}
	}
	return strings.Join(out, "; ")
}

// Dedupe keeps one window per label (the one that says most) and drops windows that say nothing.
func Dedupe(ws []Window) []Window {
	score := func(w Window) int {
		n := 0
		if w.UsedRatio != nil {
			n += 2
		}
		if w.Limit != nil {
			n++
		}
		return n
	}
	var out []Window
	at := map[string]int{}
	for _, w := range ws {
		if score(w) == 0 && !w.Exhausted {
			continue
		}
		if i, ok := at[w.Label]; ok {
			if score(w) > score(out[i]) {
				out[i] = w
			}
			continue
		}
		at[w.Label] = len(out)
		out = append(out, w)
	}
	return out
}

var windowOrder = map[string]int{"1 小时": 0, "5 小时": 1, "5 小时额度": 1, "每天": 2, "每周": 3, "每周额度": 3, "每月": 4, "每月额度": 4}

// SortWindows puts the short windows first.
func SortWindows(ws []Window) {
	sort.SliceStable(ws, func(i, j int) bool {
		a, okA := windowOrder[ws[i].Label]
		b, okB := windowOrder[ws[j].Label]
		if !okA {
			a = 9
		}
		if !okB {
			b = 9
		}
		return a < b
	})
}
