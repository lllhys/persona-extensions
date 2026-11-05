package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// ---- the plugin protocol, as the hub speaks it ----

type wireMsg struct {
	ID     string           `json:"id"`
	Result json.RawMessage  `json:"result"`
	Error  *pluginkit.Error `json:"error"`
}

type pipeHub struct {
	w       io.WriteCloser
	n       int
	mu      sync.Mutex
	answers map[string]chan wireMsg
}

func startProtocol(t *testing.T, p *plugin) *pipeHub {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		_ = pluginkit.Serve(context.Background(), inR, outW, pluginkit.Plugin{ID: "linuxdo", Version: version, Manifest: manifest,
			Contributes: contributes(), Init: func(ip pluginkit.InitParams, _ *pluginkit.Hub) error { return p.configure(ip) }, Handlers: p.handlers()})
		close(done)
	}()
	h := &pipeHub{w: inW, answers: map[string]chan wireMsg{}}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			var m wireMsg
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			h.mu.Lock()
			if ch := h.answers[m.ID]; ch != nil {
				ch <- m
			}
			h.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the plugin did not stop within 5 s of stdin closing")
		}
		_ = outW.Close()
	})
	return h
}

func (h *pipeHub) call(t *testing.T, method string, params any) wireMsg {
	t.Helper()
	h.mu.Lock()
	h.n++
	id := fmt.Sprintf("h%d", h.n)
	ch := make(chan wireMsg, 1)
	h.answers[id] = ch
	h.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if _, err := h.w.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: no answer", method)
	}
	return wireMsg{}
}

func TestProtocol(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := newPlugin(t.Logf)
	p.site = newSite(f.srv.URL)
	h := startProtocol(t, p)

	if m := h.call(t, "ping", nil); m.Error == nil || m.Error.Code != pluginkit.CodeNotInitialized {
		t.Fatalf("ping before initialize: %+v", m)
	}
	m := h.call(t, "initialize", map[string]any{"protocol": 1, "config": map[string]any{"tz": "Asia/Shanghai", "hide_nsfw": true},
		"plugin": map[string]any{"id": "linuxdo", "version": version, "data_dir": t.TempDir()}, "active": map[string][]string{"connectors": {"linuxdo"}, "ui": {"hot"}}})
	if m.Error != nil {
		t.Fatalf("initialize: %+v", m.Error)
	}
	var init struct {
		ID          string          `json:"id"`
		Version     string          `json:"version"`
		Contributes json.RawMessage `json:"contributes"`
	}
	_ = json.Unmarshal(m.Result, &init)
	var want struct {
		Contributes json.RawMessage `json:"contributes"`
	}
	_ = json.Unmarshal(manifest, &want)
	if init.ID != "linuxdo" || init.Version != version || !jsonEqual(init.Contributes, want.Contributes) {
		t.Fatalf("initialize answered %s", m.Result)
	}

	m = h.call(t, "ui.render", map[string]any{"contribution": "hot", "state": ""})
	var v pluginkit.View
	if m.Error != nil || json.Unmarshal(m.Result, &v) != nil {
		t.Fatalf("ui.render: %s %+v", m.Result, m.Error)
	}
	checkView(t, v)
	if !strings.Contains(texts(v), "→topic:1001") {
		t.Fatalf("the page:\n%s", texts(v))
	}
	m = h.call(t, "ui.action", map[string]any{"contribution": "hot", "state": v.State, "action": "topic:1001"})
	var r pluginkit.ActionResult
	if m.Error != nil || json.Unmarshal(m.Result, &r) != nil || r.View == nil || !strings.Contains(texts(*r.View), "热门回复") {
		t.Fatalf("ui.action: %s %+v", m.Result, m.Error)
	}

	m = h.call(t, "connector.status", map[string]any{"contribution": "linuxdo"})
	var st connectorStatus
	if json.Unmarshal(m.Result, &st) != nil || st.State != "ok" || st.LastSync == nil {
		t.Fatalf("status: %s", m.Result)
	}
	m = h.call(t, "connector.tools.list", map[string]any{"contribution": "linuxdo"})
	var tl struct {
		Tools []Tool `json:"tools"`
	}
	_ = json.Unmarshal(m.Result, &tl)
	if len(tl.Tools) != 2 || tl.Tools[0].Name != "linuxdo_hot" || tl.Tools[1].Name != "linuxdo_topic" {
		t.Fatalf("tools: %s", m.Result)
	}
	m = h.call(t, "connector.tools.call", map[string]any{"contribution": "linuxdo", "name": "linuxdo_topic", "args": map[string]any{"topic": 1001}})
	if m.Error != nil || !strings.Contains(string(m.Result), "Claude 新模型实测") {
		t.Fatalf("tools.call: %s %+v", m.Result, m.Error)
	}
	for _, c := range []struct{ method, contribution string }{{"connector.status", "github"}, {"ui.render", "panel"}} {
		if m := h.call(t, c.method, map[string]any{"contribution": c.contribution}); m.Error == nil || m.Error.Code != pluginkit.CodeNoContribution {
			t.Fatalf("%s for %s: %+v", c.method, c.contribution, m)
		}
	}
	if m := h.call(t, "connector.inbox.collect", map[string]any{}); m.Error == nil || m.Error.Code != pluginkit.CodeUnknownMethod {
		t.Fatalf("a capability it has not: %+v", m)
	}
	var answered map[string]any
	_ = json.Unmarshal(manifest, &answered)
	delete(answered, "tags") // the market's: hubs from before them refuse the field (nwverse/persona#2424)
	answeredRaw, _ := json.Marshal(answered)
	if m := h.call(t, "manifest", nil); !jsonEqual(m.Result, answeredRaw) {
		t.Fatal("manifest is not persona-extension.json without its tags")
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// TestManifest holds persona-extension.json to the code: what it contributes is what it answers, its settings are Config.
func TestManifest(t *testing.T) {
	var m struct {
		ID          string `json:"id"`
		Contributes struct {
			Connectors []struct {
				ID           string   `json:"id"`
				Capabilities []string `json:"capabilities"`
			} `json:"connectors"`
			UI []struct {
				ID        string `json:"id"`
				Placement string `json:"placement"`
			} `json:"ui"`
		} `json:"contributes"`
		Configuration struct {
			Properties map[string]struct {
				OneOf []struct {
					Const string `json:"const"`
				} `json:"oneOf"`
			} `json:"properties"`
		} `json:"configuration"`
		Runtime struct {
			Command map[string]string `json:"command"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "linuxdo" || len(m.Contributes.Connectors) != 1 || len(m.Contributes.UI) != 1 ||
		m.Contributes.UI[0].ID != "hot" || m.Contributes.UI[0].Placement != "plugin_page" {
		t.Fatalf("manifest: %+v", m)
	}
	h := newPlugin(t.Logf).handlers()
	for capability, method := range map[string]string{"status": "connector.status", "tools": "connector.tools.list", "inbox": "connector.inbox.collect"} {
		if slices.Contains(m.Contributes.Connectors[0].Capabilities, capability) != (h[method] != nil) {
			t.Errorf("capability %s and %s disagree", capability, method)
		}
	}
	var keys []string
	ct := reflect.TypeOf(Config{})
	for i := 0; i < ct.NumField(); i++ {
		keys = append(keys, strings.Split(ct.Field(i).Tag.Get("json"), ",")[0])
	}
	for key := range m.Configuration.Properties {
		if !slices.Contains(keys, key) {
			t.Errorf("configuration %s is not in Config", key)
		}
	}
	var listIDs []string
	for _, l := range lists {
		listIDs = append(listIDs, l.id)
	}
	var offered []string
	for _, o := range m.Configuration.Properties["list"].OneOf {
		offered = append(offered, o.Const)
	}
	if !slices.Equal(listIDs, offered) {
		t.Errorf("the list setting offers %v, the page has %v", offered, listIDs)
	}
	for _, plat := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[plat] != "bin/linuxdo-"+plat {
			t.Errorf("runtime for %s: %q", plat, m.Runtime.Command[plat])
		}
	}
}
