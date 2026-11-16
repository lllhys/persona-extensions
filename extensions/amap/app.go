package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// toolBudget is how long one tool call may take; a route may need a few requests (each paced and timed out on its
// own).
var toolBudget = 25 * time.Second

// checkBudget is how long a settings check may take: the hub gives up after 10 seconds.
const checkBudget = 9 * time.Second

// consoleURL is where the user makes and copies a key.
const consoleURL = "https://console.amap.com/dev/key/app"

// keyGuide is how to put a working key in.
var keyGuide = []string{
	"打开高德开放平台的 控制台 › 应用管理 › 我的应用",
	"创建应用并添加 Key，服务平台选「Web服务」，不要开数字签名",
	"在 设置 › 扩展 › 高德地图 里填这把 Key",
}

// app is the running extension.
type app struct {
	logf   func(string, ...any)
	client *client
	probe  bool          // check the key once at start (off in tests that count requests)
	tune   func(*client) // tests: point the client at a fake, change its clock
}

func newApp() *app {
	return &app{logf: log.Printf, client: newClient("", "", log.Printf), probe: true}
}

func (a *app) init(ctx context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	key := strings.TrimSpace(in.Secrets[keySecret])
	dir := in.Info().DataDir
	if dir == "" {
		dir = os.Getenv("PERSONA_EXTENSION_DATA")
	}
	a.client = newClient(key, dir, a.logf)
	if a.tune != nil {
		a.tune(a.client)
	}
	if key != "" && a.probe {
		// A key saved before the extension ever ran (its first settings) was not checked: try it once now, so a
		// wrong one shows in the status rather than at the first question.
		go func() {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_, _ = a.geocode(ctx, checkAddress, "")
		}()
	}
	return nil, nil
}

// status answers connector.status from memory.
func (a *app) status() wire.ConnectorStatus {
	if a.client.key == "" {
		return wire.ConnectorStatus{State: wire.ConnectorStateNotConfigured, Error: "还没填高德 Key", Guide: keyGuide, GuideURL: consoleURL}
	}
	keyErr, lastOK, u := a.client.state()
	if keyErr != nil {
		return wire.ConnectorStatus{State: wire.ConnectorStateError, Error: keyWords[keyErr.code], Guide: keyGuide, GuideURL: consoleURL}
	}
	return wire.ConnectorStatus{State: wire.ConnectorStateOk, Account: usageLine(u), LastSync: lastOK}
}

// usageLine is this month's counts and any group that is paused, in words.
func usageLine(u usage) string {
	var b strings.Builder
	b.WriteString("本月已用：")
	for i, g := range groups {
		if i > 0 {
			b.WriteString(" · ")
		}
		fmt.Fprintf(&b, "%s %d 次", g.label(), u.Counts[g])
	}
	var paused []group
	for g := range u.Blocked {
		paused = append(paused, g)
	}
	sort.Slice(paused, func(i, j int) bool { return paused[i] < paused[j] })
	for _, g := range paused {
		fmt.Fprintf(&b, "；%s额度已用完，%s 恢复", g.label(), u.Blocked[g].In(beijing).Format("01-02 15:04"))
	}
	return b.String()
}

// connector answers the connector namespace.
func (a *app) connector(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p wire.ConnectorCallParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "参数读不懂"}
		}
	}
	if p.Contribution != "" && p.Contribution != connectorID {
		return nil, ext.NoContribution(p.Contribution)
	}
	switch method {
	case "status":
		return a.status(), nil
	case "tools.list":
		return wire.ConnectorTools{Tools: toolList()}, nil
	case "tools.call":
		ctx, cancel := context.WithTimeout(ctx, toolBudget)
		defer cancel()
		text, err := a.callTool(ctx, p.Name, p.Args)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = errSlow
			}
			return nil, err
		}
		return wire.ConnectorCallResult{Text: text}, nil
	}
	return nil, ext.UnknownMethod("connector." + method)
}

// settings answers settings.check: the hub asks before it saves a new key, and saves it only when AMap takes it. The
// check spends one geocoding request of the new key; nothing of it is cached or counted.
func (a *app) settings(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if method != "check" {
		return nil, ext.UnknownMethod("settings." + method)
	}
	var in struct {
		Secrets map[string]string `json:"secrets"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "设置读不懂"}
		}
	}
	key := strings.TrimSpace(in.Secrets[keySecret])
	if key == "" {
		return map[string]any{"ok": true}, nil
	}
	if strings.ContainsAny(key, " \t\r\n") || len(key) > 64 {
		return map[string]any{"ok": false, "error": "Key 里不该有空格或换行，也不会这么长：请从高德控制台重新复制那串 32 位的 Key"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, checkBudget)
	defer cancel()
	err := a.client.try(ctx, key)
	if err == nil {
		return map[string]any{"ok": true}, nil
	}
	var ae *amapError
	var msg string
	switch {
	case errors.As(err, &ae) && ae.kind() == kindKey:
		msg = keyWords[ae.code] + "。请在高德开放平台 控制台 › 应用管理 复制「Web服务」类型的 Key"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errSlow):
		msg = "高德没有及时回答，Key 没能试成，过一会儿再保存"
	default:
		msg = "Key 没能试成：" + err.Error()
	}
	return map[string]any{"ok": false, "error": strings.ReplaceAll(msg, key, "***")}, nil
}

// clip cuts s to n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
