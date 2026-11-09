package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestConformance(t *testing.T) {
	a := testApp(t, `{"hub_url": "https://persona.example.com", "path": "/j/abc"}`, "")
	exttest.Check(t, manifest, exttest.Serve(t, program(a)), json.RawMessage(`{"hub_url": "https://persona.example.com", "path": "/j/abc"}`))
}

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Runtime     struct {
			Command map[string]string
		}
		Contributes struct {
			Connectors []struct {
				ID           string
				Name         string
				Capabilities []string
			}
			UI []struct{ ID, Title, Placement, Icon string }
		}
		Configuration struct {
			Properties map[string]struct {
				Default string
			}
		}
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "feishu-sidebar" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q, about %+v", m.ID, m.Version, about)
	}
	if c := m.Contributes.Connectors; len(c) != 1 || c[0].ID != connectorID || c[0].Name == "" ||
		!slices.Equal(c[0].Capabilities, []string{"status", "tools"}) {
		t.Fatalf("connectors %+v", c)
	}
	if u := m.Contributes.UI; len(u) != 1 || u[0].ID != pageUI || u[0].Title != "飞书侧边栏" ||
		u[0].Placement != wire.PlacementNavPage || u[0].Icon != "link" {
		t.Fatalf("ui %+v", u)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/feishu-sidebar-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	if len(m.Runtime.Command) != 4 {
		t.Errorf("runtime.command %v", m.Runtime.Command)
	}

	// The Config struct mirrors the configuration schema.
	var keys []string
	ct := reflect.TypeOf(Config{})
	for i := range ct.NumField() {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	slices.Sort(keys)
	got := make([]string, 0, len(m.Configuration.Properties))
	for k := range m.Configuration.Properties {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, keys) {
		t.Fatalf("configuration %v, Config %v", got, keys)
	}
	if m.Configuration.Properties["path"].Default != "/" {
		t.Fatalf("path default %q", m.Configuration.Properties["path"].Default)
	}
}

func code(err error) string {
	var e *ext.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestConnector(t *testing.T) {
	a := testApp(t, `{"hub_url": "https://persona.example.com", "path": "/"}`, "")
	call := func(method, params string) (map[string]any, error) {
		t.Helper()
		var raw json.RawMessage
		if params != "" {
			raw = json.RawMessage(params)
		}
		res, err := a.connector(context.Background(), method, raw)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(res)
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}

	st, err := call("status", `{"contribution": "feishu"}`)
	if err != nil || st["state"] != "ok" || st["account"] == "" {
		t.Fatalf("status: %v %v", st, err)
	}
	if _, err := call("status", `{"contribution": "other"}`); code(err) != wire.ErrorCodeNoContribution {
		t.Fatalf("another contribution: %v", err)
	}
	if _, err := call("sync", `{}`); code(err) != wire.ErrorCodeUnknownMethod {
		t.Fatalf("an unknown method: %v", err)
	}
	if _, err := call("status", `[`); err == nil {
		t.Fatal("params that are not JSON")
	}

	// Without an address: the status says so, and the tool refuses to build a wrong link.
	b := testApp(t, "", "")
	stRaw, err := b.connector(context.Background(), "status", json.RawMessage(`{"contribution": "feishu"}`))
	if err != nil || stRaw.(map[string]any)["state"] != "not_configured" {
		t.Fatalf("status without an address: %v %v", stRaw, err)
	}
	if _, err := b.connector(context.Background(), "tools.call", json.RawMessage(`{"name": "feishu_sidebar_link", "args": {}}`)); err == nil {
		t.Fatal("tools.call without an address answered")
	}

	list, err := call("tools.list", `{"contribution": "feishu"}`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range list["tools"].([]any) {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["input_schema"].(map[string]any)["type"] != "object" || tool["description"] == "" {
			t.Fatalf("tool %v", tool)
		}
	}
	if !slices.Equal(names, []string{"feishu_sidebar_link"}) {
		t.Fatalf("tools %v", names)
	}

	res, err := call("tools.call", `{"contribution": "feishu", "name": "feishu_sidebar_link", "args": {"path": "/j/abc"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if text := res["text"].(string); !strings.Contains(text, "applink.feishu.cn/client/web_url/open?mode=sidebar-semi&url=https%3A%2F%2Fpersona.example.com%2Fj%2Fabc") {
		t.Fatalf("tools.call: %v", text)
	}
	for _, params := range []string{`{"name": "nope"}`, `{"name": "feishu_sidebar_link", "args": "x"}`} {
		if _, err := call("tools.call", params); err == nil {
			t.Errorf("tools.call %s answered", params)
		}
	}
}
