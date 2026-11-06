package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestConformance(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "") // the site is the fake before anything is drawn
	exttest.Check(t, manifest, exttest.Serve(t, program(a)), json.RawMessage(`{"nodes": ["python"], "tz": "Asia/Shanghai"}`))
	if n := f.hit("/topics/hot.json"); n != 1 {
		t.Fatalf("the page read hot %d times", n)
	}
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
				ID                  string
				Capabilities, Hosts []string
			}
			UI []struct{ ID, Placement string }
		}
		Configuration struct {
			Properties map[string]struct {
				Default  string
				OneOf    []struct{ Const string } `json:"oneOf"`
				MaxItems int                      `json:"maxItems"`
			}
		}
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "v2ex" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q, about %+v", m.ID, m.Version, about)
	}
	site, _ := url.Parse(siteBase)
	web, _ := url.Parse(webBase)
	if c := m.Contributes.Connectors; len(c) != 1 || c[0].ID != connectorID || !slices.Equal(c[0].Capabilities, []string{"status", "tools"}) ||
		!slices.Contains(c[0].Hosts, site.Host) || !slices.Contains(c[0].Hosts, web.Host) {
		t.Fatalf("connectors %+v", c)
	}
	if u := m.Contributes.UI; len(u) != 1 || u[0].ID != pageUI || u[0].Placement != wire.PlacementPluginPage {
		t.Fatalf("ui %+v", u)
	}
	if !slices.Contains(m.Permissions.Network, site.Host) || !slices.Contains(m.Permissions.Network, web.Host) {
		t.Fatalf("network %v", m.Permissions.Network)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/v2ex-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	if len(m.Runtime.Command) != 4 {
		t.Errorf("runtime.command %v", m.Runtime.Command)
	}

	var keys []string
	ct := reflect.TypeOf(Config{})
	for i := range ct.NumField() {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if got := slices.Sorted(maps.Keys(m.Configuration.Properties)); !slices.Equal(got, keys) {
		t.Fatalf("configuration %v, Config %v", got, keys)
	}
	list := m.Configuration.Properties["list"]
	var choices, ids []string
	for _, c := range list.OneOf {
		choices = append(choices, c.Const)
	}
	for _, l := range lists {
		ids = append(ids, l.id)
	}
	if !slices.Equal(choices, ids) || list.Default != "hot" {
		t.Fatalf("list %v default %q, lists %v", choices, list.Default, ids)
	}
	if n := m.Configuration.Properties["nodes"].MaxItems; n != nodesMost {
		t.Fatalf("nodes maxItems %d, nodesMost %d", n, nodesMost)
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
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
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
	for _, params := range []string{`{"contribution": "v2ex"}`, ""} {
		if st, err := call("status", params); err != nil || st["state"] != "ok" || st["account"] == "" {
			t.Fatalf("status %s: %v %v", params, st, err)
		}
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

	list, err := call("tools.list", `{"contribution": "v2ex"}`)
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
	if !slices.Equal(names, []string{"v2ex_hot", "v2ex_topic"}) {
		t.Fatalf("tools %v", names)
	}
	res, err := call("tools.call", `{"contribution": "v2ex", "name": "v2ex_topic", "args": {"topic": "1001", "replies": 3}}`)
	if err != nil || !strings.Contains(res["text"].(string), goTopic.title) {
		t.Fatalf("tools.call: %v %v", res, err)
	}
	for _, params := range []string{`{"name": "nope"}`, `{"name": "v2ex_topic", "args": {}}`, `{"name": "v2ex_hot", "args": "x"}`} {
		if _, err := call("tools.call", params); err == nil {
			t.Errorf("tools.call %s answered", params)
		}
	}
}

// miniHub is the hub's side for one test: one call at a time, the program's calls answered with nothing.
type miniHub struct {
	t   *testing.T
	run exttest.Run
	sc  *bufio.Scanner
	n   int
}

func (h *miniHub) write(v any) {
	b, _ := json.Marshal(v)
	if _, err := h.run.ToProgram.Write(append(b, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *miniHub) call(method string, params, out any) {
	h.t.Helper()
	h.n++
	id := fmt.Sprintf("m%d", h.n)
	p, _ := json.Marshal(params)
	h.write(map[string]any{"id": id, "method": method, "params": json.RawMessage(p)})
	for h.sc.Scan() {
		var f struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *ext.Error      `json:"error"`
		}
		switch {
		case json.Unmarshal(h.sc.Bytes(), &f) != nil:
		case f.Method != "" && f.ID != "":
			h.write(map[string]any{"id": f.ID, "result": map[string]any{}})
		case f.ID == id && f.Error != nil:
			h.t.Fatalf("%s: %v", method, f.Error)
		case f.ID == id:
			if err := json.Unmarshal(f.Result, out); err != nil {
				h.t.Fatalf("%s: %v", method, err)
			}
			return
		}
	}
	h.t.Fatalf("%s: no answer", method)
}

// An app before UI version 3 knows no push: the SDK gives a topic's row an id of its own, and the topic is drawn in
// the list's place; back goes to the list it came from.
func TestOlderAppsOpenATopicAndGoBack(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	run := exttest.Serve(t, program(testApp(t, f, "")))
	sc := bufio.NewScanner(run.FromProgram)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	h := &miniHub{t: t, run: run, sc: sc}

	info := &wire.ExtensionInfo{ID: about.ID, Version: about.Version, Dir: t.TempDir(), DataDir: t.TempDir()}
	var res wire.InitializeResult
	h.call(wire.MethodInitialize, wire.InitializeParams{Protocol: ext.Protocol, Extension: info, Plugin: info,
		Hub: wire.HubInfo{Role: wire.HubRoleCenter, Timezone: "Asia/Shanghai", ExtensionPoints: map[string]int{}}}, &res)

	params := wire.UIParams{Contribution: pageUI, Placement: wire.PlacementPluginPage, Size: wire.SizeFull, UIVersion: 2, TZ: "Asia/Shanghai"}
	var v wire.View
	h.call("ui.render", params, &v)
	act := func(id string, values map[string]string) *wire.View {
		t.Helper()
		p := params
		p.State, p.Action, p.Values = v.State, id, values
		var r wire.ActionResult
		h.call("ui.action", p, &r)
		if r.View == nil {
			t.Fatalf("%s drew nothing", id)
		}
		return checked(t, r.View)
	}
	v = *act("list", map[string]string{"list": "latest"})
	says(t, &v, "## 最新 · 2 条")
	row := findRow(&v, goTopic.title)
	if row == nil || row.Action == nil || !strings.HasPrefix(row.Action.ID, "ext.push.") {
		t.Fatalf("the row for an older app: %+v", row)
	}
	v = *act(row.Action.ID, nil)
	if v.Title != goTopic.title || stateOf(t, v.State) != (pageState{List: "latest", Topic: 1001}) {
		t.Fatalf("opened %q, state %+v", v.Title, stateOf(t, v.State))
	}
	says(t, &v, "返回列表 →back")
	v = *act("back", nil)
	says(t, &v, "## 最新 · 2 条")
	if stateOf(t, v.State) != (pageState{List: "latest"}) {
		t.Fatalf("back to %+v", stateOf(t, v.State))
	}
	_ = run.ToProgram.Close()
	<-run.Exited
}
