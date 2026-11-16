package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "amap" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q, about %+v", m.ID, m.Version, about)
	}
	c := m.Contributes.Connectors
	if len(c) != 1 || c[0].ID != connectorID || !slices.Equal(c[0].Capabilities, []string{"status", "tools"}) ||
		!slices.Equal(c[0].Hosts, []string{"restapi.amap.com"}) {
		t.Fatalf("connectors %+v", c)
	}
	if !slices.Equal(m.Permissions.Network, []string{"restapi.amap.com"}) {
		t.Fatalf("network %v", m.Permissions.Network)
	}
	if !m.SettingsCheck || len(m.Secrets) != 1 || m.Secrets[0].Name != keySecret || !m.Secrets[0].Required || string(m.Secrets[0].Hint) != `"last4"` {
		t.Fatalf("settings_check %v, secrets %+v", m.SettingsCheck, m.Secrets)
	}
	var cmds map[string]string
	if err := json.Unmarshal(m.Runtime.Command, &cmds); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if cmds[p] != "bin/amap-"+p {
			t.Errorf("runtime.command[%s] = %q", p, cmds[p])
		}
	}
	if len(cmds) != 4 {
		t.Errorf("runtime.command %v", cmds)
	}
	sk := m.Contributes.Skills
	if len(sk) != 1 || sk[0].ID != "amap" {
		t.Fatalf("skills %+v", sk)
	}
	skill, err := os.ReadFile(filepath.Join(sk[0].Dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The skill names every tool, and the phone and saved-place tools the assistant reaches for first.
	for _, tool := range append(toolNames(), "phone_location_current", "list_places", "WGS-84") {
		if !strings.Contains(string(skill), tool) {
			t.Errorf("SKILL.md does not mention %s", tool)
		}
	}
}

func toolNames() []string {
	var out []string
	for _, t := range toolList() {
		out = append(out, t.Name)
	}
	return out
}

// startWithKey runs the program through the protocol with key as its secret.
func startWithKey(t *testing.T, a *app, key string) *exttest.Host {
	return exttest.StartWith(t, manifest, exttest.Serve(t, program(a)), func(p *wire.InitializeParams) {
		p.Secrets = map[string]string{keySecret: key}
		p.Extension = &wire.ExtensionInfo{ID: "amap", Version: about.Version, DataDir: t.TempDir()}
	})
}

func TestConformance(t *testing.T) {
	f := newFake(t)
	a := &app{logf: t.Logf, tune: tuneFor(f, newClock())}
	exttest.CheckConnectors(t, manifest, exttest.Serve(t, program(a)))
	if st := a.status(); st.State != wire.ConnectorStateNotConfigured || st.GuideURL != consoleURL {
		t.Fatalf("without a key: %+v", st)
	}

	b := &app{logf: t.Logf, tune: tuneFor(f, newClock())}
	h := startWithKey(t, b, testKey)
	var res wire.ConnectorCallResult
	if err := h.Call("connector.tools.call", wire.ConnectorCallParams{Contribution: connectorID, Name: "amap_geocode",
		Args: json.RawMessage(`{"address":"天安门"}`)}, &res); err != nil {
		t.Fatal(err)
	}
	wantIn(t, res.Text, "北京市东城区天安门广场")
	var st wire.ConnectorStatus
	if err := h.Call("connector.status", wire.ConnectorParams{Contribution: connectorID}, &st); err != nil || st.State != wire.ConnectorStateOk {
		t.Fatalf("status %+v %v", st, err)
	}
	err := h.Call("connector.tools.call", wire.ConnectorCallParams{Contribution: connectorID, Name: "amap_nope"}, &res)
	if err == nil {
		t.Fatal("an unknown tool ran")
	}
}

func TestTheKeyIsCheckedOnceAtStart(t *testing.T) {
	f := newFake(t)
	a := &app{logf: t.Logf, tune: tuneFor(f, newClock()), probe: true}
	startWithKey(t, a, testKey)
	waitFor(t, func() bool { return f.count("/v3/geocode/geo") == 1 })
	if q := f.last("/v3/geocode/geo"); q.Get("address") != checkAddress {
		t.Fatalf("query %v", q)
	}

	g := newFake(t)
	g.queue("/v3/geocode/geo", refusal("10001", "INVALID_USER_KEY"))
	b := &app{logf: t.Logf, tune: tuneFor(g, newClock()), probe: true}
	startWithKey(t, b, testKey)
	waitFor(t, func() bool { return b.status().State == wire.ConnectorStateError })
	if st := b.status(); !strings.Contains(st.Error, "不对或已经过期") {
		t.Fatalf("status %+v", st)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestSettingsCheck(t *testing.T) {
	f := newFake(t)
	a := &app{logf: t.Logf, tune: tuneFor(f, newClock())}
	h := startWithKey(t, a, "")
	check := func(key string) (bool, string) {
		t.Helper()
		var res struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := h.Call("settings.check", map[string]any{"config": map[string]any{}, "secrets": map[string]string{keySecret: key}}, &res); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Error, key) && key != "" {
			t.Fatalf("the words show the key: %q", res.Error)
		}
		return res.OK, res.Error
	}

	if ok, msg := check(testKey); !ok {
		t.Fatalf("a good key refused: %s", msg)
	}
	if q := f.last("/v3/geocode/geo"); q.Get("key") != testKey || q.Get("address") != checkAddress {
		t.Fatalf("query %v", q)
	}

	for code, words := range map[string]string{
		"10001": "不对或已经过期", "10009": "不是「Web服务」类型", "10007": "数字签名", "10005": "IP 白名单",
	} {
		f.queue("/v3/geocode/geo", refusal(code, "X"))
		if ok, msg := check(strings.Repeat("a", 32)); ok || !strings.Contains(msg, words) || !strings.Contains(msg, "控制台") {
			t.Errorf("%s: ok %v, %q", code, ok, msg)
		}
	}
	// A used-up allowance still means AMap knows the key.
	f.queue("/v3/geocode/geo", refusal("10003", "DAILY_QUERY_OVER_LIMIT"))
	if ok, msg := check(strings.Repeat("b", 32)); !ok {
		t.Fatalf("refused for the allowance: %s", msg)
	}

	before := f.count("")
	if ok, msg := check("abc def"); ok || !strings.Contains(msg, "空格") {
		t.Fatalf("ok %v, %q", ok, msg)
	}
	if ok, _ := check(""); !ok {
		t.Fatal("an empty key (a deletion) refused")
	}
	if f.count("") != before {
		t.Fatal("asked AMap for a key that cannot work")
	}

	f.srv.Close()
	if ok, msg := check(strings.Repeat("c", 32)); ok || !strings.Contains(msg, "连不上高德") {
		t.Fatalf("ok %v, %q", ok, msg)
	}

	// Checks spend nothing of the running key's counts and pause nothing.
	_, _, u := a.client.state()
	for _, g := range groups {
		if u.Counts[g] != 0 {
			t.Fatalf("counts %v", u.Counts)
		}
	}
	if len(u.Blocked) != 0 {
		t.Fatalf("blocked %v", u.Blocked)
	}
	var e *ext.Error
	if err := h.Call("settings.other", map[string]any{}, nil); err == nil || !asExtError(err, &e) || e.Code != wire.ErrorCodeUnknownMethod {
		t.Fatalf("settings.other: %v", err)
	}
}

func asExtError(err error, out **ext.Error) bool {
	e, ok := err.(*ext.Error)
	if ok {
		*out = e
	}
	return ok
}
