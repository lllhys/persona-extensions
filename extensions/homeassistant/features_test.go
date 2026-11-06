package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A richer fake home for the newer tools, modelled on a real one: a ceiling light whose device also has an auxiliary
// light switch (a plain switch in /api/states, device_class switch; only the registry ties it to the light) and a
// "power-on default" switch (entity_category config), a Xiaomi speaker of xiaomi_home with only media_player's
// services, a vacuum's map as the only camera, a gateway's do-not-disturb text entity, no automations.

type wsFake struct {
	mu       sync.Mutex
	entities []map[string]any
	devices  []map[string]any
	metas    []map[string]any
	stats    map[string][]map[string]any
	prefs    any // energy/get_prefs; nil: not configured
	asked    []map[string]any
	subs     []func(map[string]any) // push an event to each subscription
}

type richHA struct {
	*fakeHA
	ws       *wsFake
	services string
	logbook  string
	autos    map[string]map[string]any // automation config id → config
	posted   []string                  // "METHOD path body"
	picture  []byte
	logQuery []string
}

const auxSwitch = "switch.aimore_cn_833735102_230915_auxiliary_light_p_4_4"
const speaker = "media_player.xiaomi_cn_867462624_l05c"
const mapCamera = "camera.x40_pro_zeng_qiang_ban_map"
const dndText = "text.lumi_cn_710374297_mcn001_effective_time_p_6_2"

func newRichHA(t *testing.T) *richHA {
	f := newFakeHA(t)
	r := &richHA{fakeHA: f, ws: &wsFake{stats: map[string][]map[string]any{}}, autos: map[string]map[string]any{}}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, s := range []State{
		{EntityID: "light.ceiling", State: "on", Attributes: map[string]any{"friendly_name": "客厅吸顶灯", "supported_color_modes": []any{"onoff"}}},
		{EntityID: auxSwitch, State: "on", Attributes: map[string]any{"friendly_name": "吸顶灯  辅光开关", "device_class": "switch"}},
		{EntityID: "switch.ceiling_power_default", State: "off", Attributes: map[string]any{"friendly_name": "吸顶灯 厂商自定义 通电默认状态"}},
		{EntityID: "switch.living_plug", State: "on", Attributes: map[string]any{"friendly_name": "客厅插座"}},
		{EntityID: "switch.living_hidden", State: "on", Attributes: map[string]any{"friendly_name": "藏起来的开关"}},
		{EntityID: speaker, State: "idle", Attributes: map[string]any{"friendly_name": "小爱音箱"}},
		{EntityID: mapCamera, State: "idle", Attributes: map[string]any{"friendly_name": "X40 Pro 地图"}},
		{EntityID: dndText, State: "22:00-07:00", Attributes: map[string]any{"friendly_name": "网关 勿扰时间段"}},
		{EntityID: "sensor.bath_humidity", State: "60", Attributes: map[string]any{"friendly_name": "浴室湿度", "unit_of_measurement": "%", "device_class": "humidity"}},
		{EntityID: "sensor.grid_import", State: "1234.5", Attributes: map[string]any{"friendly_name": "电网用电", "unit_of_measurement": "kWh"}},
	} {
		s.LastChanged, s.LastUpdated = at, at
		f.state[s.EntityID] = s
	}
	f.areas = `[["living","客厅",["light.living","light.ceiling","` + auxSwitch + `","switch.ceiling_power_default","switch.living_plug","switch.living_hidden","switch.fan_plug","` + speaker + `","climate.living_ac","sensor.living_temp","select.ac_mode","sensor.fridge_freezer"]],` +
		`["bedroom","卧室",["light.bed","lock.front","sensor.bath_humidity"]],["kitchen","厨房",["binary_sensor.leak","` + mapCamera + `","` + dndText + `"]],["away","别处",["light.other_home"]]]`
	hidden := "user"
	r.ws.entities = []map[string]any{
		{"entity_id": "light.ceiling", "device_id": "dev_ceiling", "platform": "xiaomi_home"},
		{"entity_id": auxSwitch, "device_id": "dev_ceiling", "platform": "xiaomi_home", "entity_category": nil},
		{"entity_id": "switch.ceiling_power_default", "device_id": "dev_ceiling", "platform": "xiaomi_home", "entity_category": "config"},
		{"entity_id": "switch.living_plug", "device_id": "dev_plug", "platform": "xiaomi_home"},
		{"entity_id": "switch.living_hidden", "device_id": "dev_other", "hidden_by": hidden},
		{"entity_id": "switch.fan_plug", "device_id": "dev_fan_plug"},
		{"entity_id": speaker, "device_id": "dev_speaker", "platform": "xiaomi_home"},
		{"entity_id": dndText, "device_id": "dev_gateway", "platform": "xiaomi_home"},
		{"entity_id": "light.living", "device_id": "dev_living"},
	}
	r.ws.devices = []map[string]any{{"id": "dev_ceiling", "name": "吸顶灯"}, {"id": "dev_plug", "name": "插座"}, {"id": "dev_speaker", "name": "小爱音箱"}}
	r.services = `[{"domain":"light","services":{"turn_on":{},"turn_off":{}}},{"domain":"switch","services":{"turn_on":{},"turn_off":{}}},` +
		`{"domain":"media_player","services":{"play_media":{},"volume_set":{},"media_play_pause":{}}},{"domain":"text","services":{"set_value":{}}},` +
		`{"domain":"automation","services":{"trigger":{},"turn_on":{},"turn_off":{}}},{"domain":"script","services":{"turn_on":{},"turn_off":{}}},` +
		`{"domain":"scene","services":{"turn_on":{}}},{"domain":"lock","services":{"lock":{},"unlock":{}}}]`
	img := image.NewRGBA(image.Rect(0, 0, 3200, 1600))
	for x := range 3200 {
		img.Set(x, x%1600, color.RGBA{255, 0, 0, 255})
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	r.picture = buf.Bytes()
	f.more = r.serve
	return r
}

func (r *richHA) authed(req *http.Request) bool {
	return req.Header.Get("Authorization") == "Bearer "+r.token
}

func (r *richHA) serve(w http.ResponseWriter, req *http.Request) bool {
	p := req.URL.Path
	switch {
	case p == "/api/websocket":
		r.serveWS(w, req)
		return true
	case !r.authed(req):
		return false
	case p == "/api/services" && req.Method == http.MethodGet:
		fmt.Fprint(w, r.services)
		return true
	case strings.HasPrefix(p, "/api/logbook/"):
		r.mu.Lock()
		r.logQuery = append(r.logQuery, req.URL.RawQuery)
		r.mu.Unlock()
		fmt.Fprint(w, r.logbook)
		return true
	case strings.HasPrefix(p, "/api/camera_proxy/"):
		if strings.TrimPrefix(p, "/api/camera_proxy/") != mapCamera {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(r.picture)
		return true
	case strings.HasPrefix(p, "/api/config/automation/config/"):
		cid := strings.TrimPrefix(p, "/api/config/automation/config/")
		r.mu.Lock()
		defer r.mu.Unlock()
		var body bytes.Buffer
		_, _ = body.ReadFrom(req.Body)
		r.posted = append(r.posted, req.Method+" "+cid+" "+body.String())
		switch req.Method {
		case http.MethodGet:
			c, ok := r.autos[cid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Resource not found"}`)
				return true
			}
			_ = json.NewEncoder(w).Encode(c)
		case http.MethodPost:
			var c map[string]any
			_ = json.Unmarshal(body.Bytes(), &c)
			r.autos[cid] = c
			fmt.Fprint(w, `{"result":"ok"}`)
		case http.MethodDelete:
			delete(r.autos, cid)
			fmt.Fprint(w, `{"result":"ok"}`)
		}
		return true
	}
	return false
}

func (r *richHA) serveWS(w http.ResponseWriter, req *http.Request) {
	c, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx := req.Context()
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		_ = c.Write(ctx, websocket.MessageText, b)
	}
	send(map[string]any{"type": "auth_required", "ha_version": "2026.10.0"})
	_, b, err := c.Read(ctx)
	if err != nil {
		return
	}
	var auth map[string]any
	_ = json.Unmarshal(b, &auth)
	if auth["access_token"] != r.token {
		send(map[string]any{"type": "auth_invalid", "message": "Invalid access token"})
		return
	}
	send(map[string]any{"type": "auth_ok"})
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		id := m["id"]
		ok := func(res any) { send(map[string]any{"id": id, "type": "result", "success": true, "result": res}) }
		fail := func(code, msg string) {
			send(map[string]any{"id": id, "type": "result", "success": false, "error": map[string]string{"code": code, "message": msg}})
		}
		r.ws.mu.Lock()
		r.ws.asked = append(r.ws.asked, m)
		ws := r.ws
		switch m["type"] {
		case "config/entity_registry/list":
			ok(ws.entities)
		case "config/device_registry/list":
			ok(ws.devices)
		case "recorder/get_statistics_metadata":
			ok(ws.metas)
		case "recorder/statistics_during_period":
			out := map[string]any{}
			for _, sid := range m["statistic_ids"].([]any) {
				if rows, has := ws.stats[sid.(string)]; has {
					out[sid.(string)] = rows
				}
			}
			ok(out)
		case "energy/get_prefs":
			if ws.prefs == nil {
				fail("not_found", "No prefs")
			} else {
				ok(ws.prefs)
			}
		case "subscribe_events":
			ws.subs = append(ws.subs, func(ev map[string]any) { send(map[string]any{"id": id, "type": "event", "event": ev}) })
			ok(nil)
		default:
			fail("unknown_command", "Unknown command.")
		}
		r.ws.mu.Unlock()
	}
}

// push sends a state_changed to every subscription.
func (r *richHA) push(entity string, old, new *State) {
	r.ws.mu.Lock()
	subs := append([]func(map[string]any){}, r.ws.subs...)
	r.ws.mu.Unlock()
	ev := map[string]any{"event_type": "state_changed", "data": map[string]any{"entity_id": entity, "old_state": old, "new_state": new}}
	for _, s := range subs {
		s(ev)
	}
}

func (r *richHA) asked(typ string) []map[string]any {
	r.ws.mu.Lock()
	defer r.ws.mu.Unlock()
	var out []map[string]any
	for _, m := range r.ws.asked {
		if m["type"] == typ {
			out = append(out, m)
		}
	}
	return out
}

func richApp(t *testing.T, r *richHA, cfg Config) *app {
	t.Helper()
	a := newTestApp(t, r.fakeHA, cfg)
	a.tz, _ = time.LoadLocation("Asia/Shanghai")
	a.dataDir = t.TempDir()
	a.watch.load(a.dataDir)
	t.Cleanup(func() { h, _, _ := a.client(); h.closeWS() })
	return a
}

func callT(t *testing.T, a *app, name string, args any) string {
	t.Helper()
	out, err := connector(t, a, "tools.call", map[string]any{"contribution": id, "name": name, "args": args})
	if err != nil {
		return "ERR " + err.Error()
	}
	return out["text"].(string)
}

func describe(t *testing.T, a *app, name string, args any) (string, error) {
	t.Helper()
	out, err := connector(t, a, "actions.describe", map[string]any{"contribution": id, "name": name, "args": args})
	if err != nil {
		return "", err
	}
	return out["summary"].(string), nil
}

func doAction(t *testing.T, a *app, name string, args any, rid string) (string, error) {
	t.Helper()
	out, err := connector(t, a, "actions.do", map[string]any{"contribution": id, "name": name, "args": args, "request_id": rid})
	if err != nil {
		return "", err
	}
	return out["text"].(string), nil
}

// ---- 3. by area ----

func TestCallByArea(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})
	args := map[string]any{"area": "客厅", "domain": "switch", "service": "turn_off"}

	// nothing read yet: describe may not reach out, so it asks for a look first
	if _, err := describe(t, a, callAction, args); err == nil || !strings.Contains(err.Error(), "homeassistant_search") {
		t.Fatalf("a cold describe: %v", err)
	}
	contains(t, callT(t, a, id+"_search", map[string]any{"area": "客厅", "domain": "switch", "include_unavailable": true}), "客厅插座", "辅光开关")

	card, err := describe(t, a, callAction, args)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, card, "对「客厅」的 1 个开关执行：关闭（switch.turn_off）", "- 「客厅插座」（switch.living_plug，现在开）", "不操作：",
		"「吸顶灯 辅光开关」（"+auxSwitch+"）：是「客厅吸顶灯」（light.ceiling）的附属开关",
		"（switch.ceiling_power_default）：设备的配置项", "（switch.living_hidden）：在 Home Assistant 里隐藏了", "（switch.fan_plug）：离线")
	text, err := doAction(t, a, callAction, args, "area1")
	if err != nil || f(r).lastCall() != "switch.turn_off [switch.living_plug] {}" || !strings.Contains(text, "客厅插座：关") {
		t.Fatalf("do: %q %v %q", text, err, f(r).lastCall())
	}

	// lights: both, a light's own device siblings do not matter
	card, _ = describe(t, a, callAction, map[string]any{"area": "living", "domain": "light", "service": "turn_off"})
	contains(t, card, "的 2 个灯执行", "light.living", "light.ceiling")

	// what the card showed must be what runs: the registry changes between the card and the tap
	args2 := map[string]any{"area": "客厅", "domain": "light", "service": "turn_on"}
	if _, err := describe(t, a, callAction, args2); err != nil {
		t.Fatal(err)
	}
	h, _, _ := a.client()
	h.mu.Lock()
	h.reg.Entities["light.ceiling"] = RegEntity{EntityID: "light.ceiling", EntityCategory: "config"}
	h.mu.Unlock()
	if _, err := doAction(t, a, callAction, args2, "area2"); err == nil || !strings.Contains(err.Error(), "不一样了") {
		t.Fatalf("a changed batch ran: %v", err)
	}

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"area": "客厅", "service": "turn_off"}, "要写 domain"},
		{map[string]any{"area": "书房", "domain": "light", "service": "turn_off"}, "没有区域「书房」"},
		{map[string]any{"area": "别处", "domain": "light", "service": "turn_off"}, "没有区域「别处」"},
		{map[string]any{"area": "卧室", "domain": "switch", "service": "turn_off"}, "没有能操作的开关"},
		{map[string]any{"area": "客厅", "domain": "light", "entity_id": "light.living", "service": "turn_off"}, "只写一个"},
		{map[string]any{"area": "卧室", "domain": "lock", "service": "unlock"}, "门锁和警戒要用"},
	} {
		if _, err := describe(t, a, callAction, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.args, c.want, err)
		}
	}
	if _, err := describe(t, a, secureAction, map[string]any{"area": "卧室", "domain": "lock", "service": "unlock"}); err == nil ||
		!strings.Contains(err.Error(), "不能按区域") {
		t.Errorf("locks by area: %v", err)
	}
}

func f(r *richHA) *fakeHA { return r.fakeHA }

// Without the registry (a token that may not read it) a call by area is refused rather than guessed.
func TestCallByAreaNoRegistry(t *testing.T) {
	r := newRichHA(t)
	r.ws.entities = nil
	a := richApp(t, r, Config{})
	r.mu.Lock()
	r.fakeHA.token = "good-token"
	r.mu.Unlock()
	h, _, _ := a.client()
	if _, err := a.home(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	h.closeWS()
	h.mu.Lock()
	h.base = h.base + "/nowhere" // the WebSocket does not answer
	h.mu.Unlock()
	if _, err := describe(t, a, callAction, map[string]any{"area": "客厅", "domain": "switch", "service": "turn_off"}); err == nil ||
		!strings.Contains(err.Error(), "实体注册表") {
		t.Fatalf("no registry: %v", err)
	}
}

// ---- 2. automations ----

func TestAutomations(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})
	got := callT(t, a, id+"_automations", nil)
	contains(t, got, "## 自动化（0 个）\n（没有）", "## 脚本（0 个）", "## 场景（0 个）")

	cfg := map[string]any{"id": "1700000000001", "alias": "夜灯", "triggers": []any{map[string]any{"trigger": "time", "at": "22:00:00"}},
		"actions": []any{map[string]any{"action": "light.turn_on", "target": map[string]any{"entity_id": "light.bed"}}}, "mode": "single"}
	r.mu.Lock()
	r.autos["1700000000001"] = cfg
	r.state["automation.night"] = State{EntityID: "automation.night", State: "on", Attributes: map[string]any{"friendly_name": "夜灯", "id": "1700000000001", "last_triggered": "2026-01-02T14:00:00+00:00"}}
	r.state["automation.yaml_one"] = State{EntityID: "automation.yaml_one", State: "off", Attributes: map[string]any{"friendly_name": "YAML 的"}}
	r.state["script.goodnight"] = State{EntityID: "script.goodnight", State: "off", Attributes: map[string]any{"friendly_name": "晚安"}}
	r.mu.Unlock()
	h, _, _ := a.client()
	h.mu.Lock()
	h.statesAt = time.Time{}
	h.mu.Unlock()
	got = callT(t, a, id+"_automations", map[string]any{"kind": "all"})
	contains(t, got, "## 自动化（2 个）", "- 夜灯（automation.night）：开着，配置 id 1700000000001，上次 01-02 22:00", "YAML 的（automation.yaml_one）：停用了，没有配置 id", "晚安（script.goodnight）")
	if _, err := h.Services(context.Background()); err != nil {
		t.Fatal(err)
	}

	// run: trigger, disable, a script with variables
	card, err := describe(t, a, runAction, map[string]any{"entity_id": "automation.night", "op": "trigger"})
	if err != nil || !strings.Contains(card, "手动触发自动化「夜灯」（automation.night，automation.trigger），不检查它的条件") {
		t.Fatalf("run card: %q %v", card, err)
	}
	if _, err := doAction(t, a, runAction, map[string]any{"entity_id": "automation.night", "op": "trigger"}, "run1"); err != nil ||
		f(r).lastCall() != `automation.trigger [automation.night] {"skip_condition":true}` {
		t.Fatalf("trigger: %v %q", err, f(r).lastCall())
	}
	if _, err := doAction(t, a, runAction, map[string]any{"entity_id": "script.goodnight", "variables": map[string]any{"room": "卧室"}}, "run2"); err != nil ||
		f(r).lastCall() != `script.turn_on [script.goodnight] {"variables":{"room":"卧室"}}` {
		t.Fatalf("script: %v %q", err, f(r).lastCall())
	}
	for args, want := range map[string]string{
		`{"entity_id":"automation.night","op":"run"}`:                   "op 只能是 disable、enable、trigger",
		`{"entity_id":"light.living","op":"run"}`:                       "只能是自动化",
		`{"entity_id":"scene.x","variables":{"a":1}}`:                   "只有脚本能带 variables",
		`{"entity_id":"automation.night","op":"enable","variables":{}}`: "",
	} {
		_, err := describe(t, a, runAction, json.RawMessage(args))
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", args, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", args, want, err)
		}
	}
	if _, err := describe(t, a, runAction, map[string]any{"entity_id": "automation.night", "op": "trigger"}); err != nil {
		t.Fatal(err)
	}
	b := newTestApp(t, r.fakeHA, Config{Writable: []string{"light"}})
	if _, err := describe(t, b, runAction, map[string]any{"entity_id": "automation.night", "op": "trigger"}); err == nil || !strings.Contains(err.Error(), "不在扩展设置的可操作类型里") {
		t.Fatalf("automation not writable: %v", err)
	}

	// update: the config must be read first; the card has what changes and the whole new config
	newCfg := map[string]any{"alias": "夜灯", "triggers": []any{map[string]any{"trigger": "time", "at": "23:00:00"}},
		"actions": []any{map[string]any{"action": "light.turn_on", "target": map[string]any{"entity_id": "light.bed"}}}, "mode": "single"}
	upd := map[string]any{"op": "update", "id": "automation.night", "config": newCfg}
	if _, err := describe(t, a, editAction, upd); err == nil || !strings.Contains(err.Error(), "_automation_config") {
		t.Fatalf("an update before reading: %v", err)
	}
	contains(t, callT(t, a, id+"_automation_config", map[string]any{"automation": "automation.night"}), `"at": "22:00:00"`, "配置 id 1700000000001")
	contains(t, callT(t, a, id+"_automation_config", map[string]any{"automation": "automation.yaml_one"}), "写在 YAML 里")
	card, err = describe(t, a, editAction, upd)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, card, "【修改自动化】「夜灯」（配置 id 1700000000001）", "-       \"at\": \"22:00:00\",\n+       \"at\": \"23:00:00\",", "改完后的完整配置：", `"id": "1700000000001"`)
	if strings.Contains(card, `- "alias"`) {
		t.Errorf("an unchanged line as a change:\n%s", card)
	}
	if _, err := doAction(t, a, editAction, upd, "edit1"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	at := r.autos["1700000000001"]["triggers"].([]any)[0].(map[string]any)["at"]
	r.mu.Unlock()
	if at != "23:00:00" {
		t.Fatalf("not updated: %v", at)
	}

	// changed in Home Assistant after the card: refused
	contains(t, callT(t, a, id+"_automation_config", map[string]any{"automation": "1700000000001"}), "23:00:00")
	upd2 := map[string]any{"op": "update", "id": "1700000000001", "config": cfg}
	if _, err := describe(t, a, editAction, upd2); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.autos["1700000000001"]["description"] = "别人改过"
	r.mu.Unlock()
	if _, err := doAction(t, a, editAction, upd2, "edit2"); err == nil || !strings.Contains(err.Error(), "被改过") {
		t.Fatalf("a stale update ran: %v", err)
	}

	// create: an id of its own, the whole config on the card; delete shows what goes
	create := map[string]any{"op": "create", "config": map[string]any{"alias": "漏水关阀", "triggers": []any{map[string]any{"trigger": "state", "entity_id": "binary_sensor.leak", "to": "on"}},
		"actions": []any{map[string]any{"action": "valve.close_valve", "target": map[string]any{"entity_id": "valve.main"}}}}}
	card, err = describe(t, a, editAction, create)
	if err != nil || !strings.Contains(card, "【新建自动化】「漏水关阀」") || !strings.Contains(card, `"binary_sensor.leak"`) {
		t.Fatalf("create card: %q %v", card, err)
	}
	if _, err := doAction(t, a, editAction, create, "edit3"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	n := len(r.autos)
	r.mu.Unlock()
	if n != 2 {
		t.Fatalf("create: %d automations", n)
	}
	if _, err := describe(t, a, editAction, map[string]any{"op": "create", "config": map[string]any{"alias": "x", "triggers": []any{}}}); err == nil || !strings.Contains(err.Error(), "actions") {
		t.Fatalf("an automation without actions: %v", err)
	}
	contains(t, callT(t, a, id+"_automation_config", map[string]any{"automation": "1700000000001"}), "别人改过")
	card, _ = describe(t, a, editAction, map[string]any{"op": "delete", "id": "1700000000001"})
	contains(t, card, "【删除自动化】「夜灯」", "删掉后不能恢复", "别人改过")
	if _, err := doAction(t, a, editAction, map[string]any{"op": "delete", "id": "1700000000001"}, "edit4"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	_, still := r.autos["1700000000001"]
	r.mu.Unlock()
	if still {
		t.Fatal("not deleted")
	}
}

func TestLineDiff(t *testing.T) {
	got := lineDiff("a\nb\nc\nd\ne\nf\ng", "a\nb\nc\nX\ne\nf\ng\nh")
	want := "  b\n  c\n- d\n+ X\n  e\n  f\n  …\n  f\n  g\n+ h"
	if got == want {
		return
	}
	contains(t, got, "- d\n+ X", "+ h")
	if strings.Contains(got, "- a") || lineDiff("x", "x") != "" {
		t.Errorf("diff:\n%s", got)
	}
}

// ---- 4. logbook ----

func TestLogbook(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})
	var rows []string
	for i := range 12 { // a temperature that changes all the time
		rows = append(rows, fmt.Sprintf(`{"when":"2026-01-02T0%d:%02d:00+00:00","name":"客厅温度","state":"%d","entity_id":"sensor.living_temp"}`, 1+i/6, i*5%60, 20+i))
	}
	rows = append(rows,
		`{"when":"2026-01-02T00:30:00+00:00","name":"客厅灯","state":"on","entity_id":"light.living","context_entity_id":"automation.night","context_name":"夜灯"}`,
		`{"when":1767315600.5,"name":"厨房漏水","state":"on","entity_id":"binary_sensor.leak"}`,
		`{"when":"2026-01-02T02:00:00+00:00","name":"别处的灯","state":"on","entity_id":"light.other_home"}`,
		`{"when":"2026-01-01T23:59:00+00:00","name":"Home Assistant","message":"started"}`)
	r.logbook = "[" + strings.Join(rows, ",") + "]"
	got := callT(t, a, id+"_logbook", map[string]any{"start": "2026-01-02", "end": "2026-01-03"})
	contains(t, got, "全屋 01-02 00:00 到 01-03 00:00 的日志（Asia/Shanghai）", "变化太频繁的合成了一行",
		"## 01-02 周五", "07:59 Home Assistant started", "08:30 客厅灯 → 开（客厅），由自动化「夜灯」",
		"09:00 客厅温度 变化了 12 次（到 01-02 10:55，最后是31 °C）", "厨房漏水 → 有水（厨房）")
	if strings.Contains(got, "别处") || strings.Count(got, "客厅温度") != 1 {
		t.Errorf("hidden or noisy:\n%s", got)
	}
	got = callT(t, a, id+"_logbook", map[string]any{"area": "厨房", "hours": 48})
	contains(t, got, "厨房 ", "厨房漏水")
	if strings.Contains(got, "客厅灯") || strings.Contains(got, "started") {
		t.Errorf("area filter:\n%s", got)
	}
	r.mu.Lock()
	q := r.logQuery[len(r.logQuery)-1]
	r.mu.Unlock()
	if !strings.Contains(q, "end_time=") {
		t.Errorf("query %q", q)
	}
	contains(t, callT(t, a, id+"_logbook", map[string]any{"hours": 800}), "ERR 最多查 31 天")
	contains(t, callT(t, a, id+"_logbook", map[string]any{"start": "昨天"}), "ERR 「昨天」不是时间")
	contains(t, callT(t, a, id+"_logbook", map[string]any{"area": "书房"}), "ERR 没有区域")
	r.logbook = "[]"
	contains(t, callT(t, a, id+"_logbook", map[string]any{"entity_id": "light.living"}), "客厅灯 ", "这段时间没有记录")
}

// ---- 5. statistics and energy ----

func TestStatistics(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})
	day := func(d int) int64 { return time.Date(2025, 12, d, 16, 0, 0, 0, time.UTC).UnixMilli() } // midnight in Shanghai
	r.ws.metas = []map[string]any{
		{"statistic_id": "sensor.bath_humidity", "has_mean": true, "has_sum": false, "statistics_unit_of_measurement": "%"},
		{"statistic_id": "sensor.grid_import", "has_mean": false, "mean_type": 0, "has_sum": true, "statistics_unit_of_measurement": "kWh"},
	}
	r.ws.stats["sensor.bath_humidity"] = []map[string]any{{"start": day(1), "mean": 61.234, "min": 50.0, "max": 80.0}, {"start": day(2), "mean": 55.0, "min": 45.0, "max": 70.0}}
	r.ws.stats["sensor.grid_import"] = []map[string]any{{"start": day(1), "change": 10.5}, {"start": day(2), "change": 12.25}, {"start": day(3), "change": 8.0}}
	got := callT(t, a, id+"_statistics", map[string]any{"entity_id": []string{"sensor.bath_humidity", "sensor.grid_import", "sensor.living_temp"}, "period": "day", "days": 90})
	contains(t, got, "## 浴室湿度（sensor.bath_humidity）", "12-02 周二：平均 61.23，最低 50，最高 80 %", "整段：平均 58.12，最低 45，最高 80 %",
		"## 电网用电（sensor.grid_import）", "12-03 周三：12.25 kWh", "合计 30.75 kWh，平均每天 10.25 kWh，最多的是 12-03 周三（12.25 kWh）",
		"客厅温度（sensor.living_temp）\nHome Assistant 没有它的长期统计")
	asked := r.asked("recorder/statistics_during_period")
	if len(asked) != 2 || asked[0]["period"] != "day" || fmt.Sprint(asked[0]["types"]) != "[mean min max]" || fmt.Sprint(asked[1]["types"]) != "[change]" {
		t.Fatalf("asked %v", asked)
	}
	from, _ := time.Parse(time.RFC3339, asked[0]["start_time"].(string))
	to, _ := time.Parse(time.RFC3339, asked[0]["end_time"].(string))
	if d := to.Sub(from); d != 90*24*time.Hour {
		t.Fatalf("span %v", d)
	}
	contains(t, callT(t, a, id+"_statistics", map[string]any{"entity_id": "sensor.grid_import", "period": "month"}), "按月")
	contains(t, callT(t, a, id+"_statistics", map[string]any{"entity_id": "sensor.grid_import", "period": "hour", "days": 30}), "ERR 按小时最多 14 天")
	contains(t, callT(t, a, id+"_statistics", map[string]any{"entity_id": "sensor.x", "period": "year"}), "ERR period 要是")
	contains(t, callT(t, a, id+"_statistics", nil), "ERR entity_id 要有 1 到 5 个")

	// energy: not configured, then a grid and two devices
	contains(t, callT(t, a, id+"_energy", nil), "还没配置能源")
	r.ws.mu.Lock()
	r.ws.prefs = map[string]any{"energy_sources": []any{map[string]any{"type": "grid", "flow_from": []any{map[string]any{"stat_energy_from": "sensor.grid_import"}}, "flow_to": []any{}}},
		"device_consumption": []any{map[string]any{"stat_consumption": "sensor.ac_energy"}, map[string]any{"stat_consumption": "sensor.fridge_energy", "name": "冰箱"}}}
	r.ws.metas = append(r.ws.metas, map[string]any{"statistic_id": "sensor.ac_energy", "has_sum": true, "statistics_unit_of_measurement": "kWh"},
		map[string]any{"statistic_id": "sensor.fridge_energy", "has_sum": true, "statistics_unit_of_measurement": "kWh"})
	r.ws.stats["sensor.ac_energy"] = []map[string]any{{"start": day(1), "change": 4.0}, {"start": day(2), "change": 5.0}}
	r.ws.stats["sensor.fridge_energy"] = []map[string]any{{"start": day(1), "change": 1.0}}
	r.ws.mu.Unlock()
	got = callT(t, a, id+"_energy", map[string]any{"period": "week", "days": 60})
	contains(t, got, "按周", "- 从电网用电（sensor.grid_import）：30.75 kWh", "## 各设备\n- sensor.ac_energy（sensor.ac_energy）：9 kWh\n- 冰箱（sensor.fridge_energy）：1 kWh",
		"## 每周的从电网用电", "12-02 起的一周：10.5 kWh")
}

// ---- 6. speakers ----

func TestSay(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})
	args := map[string]any{"entity_id": speaker, "text": "该吃饭了"}
	if _, err := describe(t, a, sayAction, args); err == nil || !strings.Contains(err.Error(), "_speakers") {
		t.Fatalf("cold: %v", err)
	}
	// the real speaker: xiaomi_home, media_player's services only, no TTS engine
	got := callT(t, a, id+"_speakers", nil)
	contains(t, got, "小爱音箱（"+speaker+"）：空闲，在客厅，xiaomi_home 集成", "不支持播报：xiaomi_miot：没有服务 xiaomi_miot.intelligent_speaker",
		"没有播报的 notify 实体", "没有 text.…play_text… 实体", "没有 TTS 引擎")
	_, err := describe(t, a, sayAction, args)
	if err == nil || !strings.Contains(err.Error(), "「小爱音箱」（"+speaker+"）不支持播报") || !strings.Contains(err.Error(), "- TTS：Home Assistant 里没有 TTS 引擎") {
		t.Fatalf("an unsupported speaker: %v", err)
	}
	if strings.Contains(err.Error(), dndText) {
		t.Fatalf("the gateway's text entity taken for the speaker's: %v", err)
	}

	setServices := func(s string) {
		r.services = s
		h, _, _ := a.client()
		h.mu.Lock()
		h.servAt = time.Time{}
		h.mu.Unlock()
		callT(t, a, id+"_speakers", nil)
	}
	addState := func(s State) {
		r.mu.Lock()
		r.state[s.EntityID] = s
		r.mu.Unlock()
		h, _, _ := a.client()
		h.mu.Lock()
		h.statesAt = time.Time{}
		h.mu.Unlock()
	}

	// a TTS engine: tts.speak first, play_media with a media-source URL after it
	addState(State{EntityID: "tts.google_translate_zh_cn", State: "unknown", Attributes: map[string]any{}})
	r.mu.Lock()
	r.state["tts.google_translate_zh_cn"] = State{EntityID: "tts.google_translate_zh_cn", State: "2026-01-01T00:00:00", Attributes: map[string]any{}}
	r.mu.Unlock()
	setServices(r.services[:len(r.services)-1] + `,{"domain":"tts","services":{"speak":{}}}]`)
	card, err := describe(t, a, sayAction, args)
	if err != nil || !strings.Contains(card, "在「小爱音箱」（"+speaker+"）上念：「该吃饭了」") || !strings.Contains(card, "tts.speak（tts.google_translate_zh_cn）") {
		t.Fatalf("tts: %q %v", card, err)
	}
	card, _ = describe(t, a, sayAction, map[string]any{"entity_id": speaker, "text": "该吃饭了", "method": "media_source"})
	contains(t, card, "media_player.play_media")
	if _, err := doAction(t, a, sayAction, map[string]any{"entity_id": speaker, "text": "该吃饭了", "method": "media_source"}, "say1"); err != nil {
		t.Fatal(err)
	}
	if got := f(r).lastCall(); !strings.HasPrefix(got, "media_player.play_media ["+speaker+"] ") || !strings.Contains(got, `media-source://tts/tts.google_translate_zh_cn?message=%E8%AF%A5`) {
		t.Fatalf("play_media: %q", got)
	}

	// xiaomi_home's play-text notify entity on the speaker's device comes before TTS
	addState(State{EntityID: "notify.xiaomi_cn_867462624_l05c_play_text_a_5_1", State: "unknown", Attributes: map[string]any{"friendly_name": "小爱音箱 播放文本"}})
	r.ws.mu.Lock()
	r.ws.entities = append(r.ws.entities, map[string]any{"entity_id": "notify.xiaomi_cn_867462624_l05c_play_text_a_5_1", "device_id": "dev_speaker", "platform": "xiaomi_home"})
	r.ws.mu.Unlock()
	h, _, _ := a.client()
	h.mu.Lock()
	h.regAt = time.Time{}
	h.mu.Unlock()
	setServices(r.services[:len(r.services)-1] + `,{"domain":"notify","services":{"send_message":{}}}]`)
	card, _ = describe(t, a, sayAction, args)
	contains(t, card, "xiaomi_home 的播报实体，notify.send_message（notify.xiaomi_cn_867462624_l05c_play_text_a_5_1）")
	if _, err := doAction(t, a, sayAction, args, "say2"); err != nil {
		t.Fatal(err)
	}
	if got := f(r).lastCall(); got != `notify.send_message [notify.xiaomi_cn_867462624_l05c_play_text_a_5_1] {"message":"[\"该吃饭了\"]"}` {
		t.Fatalf("notify: %q", got)
	}

	// xiaomi_miot's service only for its own entities
	setServices(r.services[:len(r.services)-1] + `,{"domain":"xiaomi_miot","services":{"intelligent_speaker":{}}}]`)
	contains(t, callT(t, a, id+"_speakers", nil), "这台音箱是 xiaomi_home 集成的")
	addState(State{EntityID: "media_player.xiaomi_lx06_ab12", State: "playing", Attributes: map[string]any{"friendly_name": "小爱音箱 Pro"}})
	r.ws.mu.Lock()
	r.ws.entities = append(r.ws.entities, map[string]any{"entity_id": "media_player.xiaomi_lx06_ab12", "device_id": "dev_lx06", "platform": "xiaomi_miot"})
	r.ws.mu.Unlock()
	h.mu.Lock()
	h.regAt = time.Time{}
	h.mu.Unlock()
	callT(t, a, id+"_speakers", nil)
	card, _ = describe(t, a, sayAction, map[string]any{"entity_id": "media_player.xiaomi_lx06_ab12", "text": "你好"})
	contains(t, card, "xiaomi_miot 的小爱播报，xiaomi_miot.intelligent_speaker")

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"entity_id": speaker, "text": " "}, "不能是空的"},
		{map[string]any{"entity_id": speaker, "text": strings.Repeat("字", sayMost+1)}, "最多念"},
		{map[string]any{"entity_id": dndText, "text": "x"}, "音箱要写"},
		{map[string]any{"entity_id": speaker, "text": "x", "method": "xiaomi_miot"}, "不能用 xiaomi_miot 播报；能用：xiaomi_home、tts、media_source"},
		{map[string]any{"entity_id": speaker, "text": "x", "method": "loud"}, "method 要是"},
	} {
		if _, err := describe(t, a, sayAction, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.args, c.want, err)
		}
	}
}

// ---- 7. camera ----

func TestCamera(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})
	out, err := connector(t, a, "tools.call", map[string]any{"contribution": id, "name": cameraTool, "args": map[string]any{"entity_id": mapCamera}})
	if err != nil {
		t.Fatal(err)
	}
	text := out["text"].(string)
	contains(t, text, "「X40 Pro 地图」（"+mapCamera+"）", "1600×800", "image/jpeg", "这是设备画的地图", "图片也存在 ")
	imgs := out["images"].([]any)
	img := imgs[0].(map[string]any)
	raw, err := base64.StdEncoding.DecodeString(img["data"].(string))
	if err != nil || img["mime_type"] != "image/jpeg" {
		t.Fatalf("image: %v %v", img["mime_type"], err)
	}
	dec, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil || dec.Bounds().Dx() != 1600 {
		t.Fatalf("decoded: %v %v", err, dec)
	}
	files, _ := filepath.Glob(filepath.Join(a.dataDir, "snapshots", "*"))
	if len(files) != 1 {
		t.Fatalf("files %v", files)
	}
	if st, _ := os.Stat(files[0]); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	contains(t, callT(t, a, cameraTool, nil), "ERR 要写 entity_id：有 X40 Pro 地图（"+mapCamera+"）")
	contains(t, callT(t, a, cameraTool, map[string]any{"entity_id": "light.living"}), "ERR 「light.living」不是摄像头")
	contains(t, callT(t, a, cameraTool, map[string]any{"entity_id": "camera.door"}), "ERR 没有 camera.door；有：X40 Pro 地图")

	// a home without cameras
	b := newTestApp(t, newFakeHA(t), Config{})
	contains(t, callT(t, b, cameraTool, map[string]any{"entity_id": "camera.door"}), "ERR Home Assistant 里没有摄像头")
}

func TestKeepSnapshots(t *testing.T) {
	a := newApp(t.Logf)
	a.dataDir = t.TempDir()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range snapshotMost + 5 {
		if _, err := a.keepSnapshot(mapCamera, at.Add(time.Duration(i)*time.Second), []byte("x"), "image/png"); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(a.dataDir, "snapshots", "*"))
	if len(files) != snapshotMost || !strings.HasSuffix(files[len(files)-1], "000024.png") {
		t.Fatalf("%d files, last %v", len(files), files[len(files)-1])
	}
}

// ---- 1. watches ----

func TestParseRule(t *testing.T) {
	for line, want := range map[string]string{
		"binary_sensor.front_door =on for=1m":                              "当binary_sensor.front_door变成 on，持续 1m0s → 推送到手机",
		"sensor.bath_humidity >75 for=10m do=assistant message=开一下排风扇 谢谢":  "状态高于 75，持续 10m0s → 叫助手处理，附言：开一下排风扇 谢谢",
		"binary_sensor@厨房 class=moisture =on level=time-sensitive name=漏水": "「漏水」当「厨房」里的传感器（binary_sensor）（moisture 类）变成 on → 推送到手机（时间敏感",
		"switch.* offline":                        "当switch.*离线，持续 10m0s → 推送到手机",
		"*@客厅 offline cooldown=2h":                "当「客厅」里的实体离线，持续 10m0s → 推送到手机；同一个实体 2h0m0s 内只提醒一次",
		"climate.ac attr=current_temperature >30": "属性 current_temperature高于 30",
		"light.x":                     "ERR 一条要写成",
		"light.x =on for=soon":        "ERR 「soon」不是时长",
		"light.x =on color=red":       "ERR 不认识「color」",
		"light.x >10 <5":              "ERR above 要小于 below",
		"Light.X =on":                 "不是实体 id",
		"light.x =on do=email":        "ERR do 要是",
		"sensor.t >abc":               "ERR 「abc」不是数",
		"light.x hello":               "ERR 不认识「hello」",
		"binary_sensor.door from=off": "从 off",
	} {
		r, err := parseRule(line)
		got := r.describe()
		if err != nil {
			got = "ERR " + err.Error()
		}
		if !strings.Contains(got, want) {
			t.Errorf("%q: want %q, got %q", line, want, got)
		}
	}
	a, _ := parseRule("light.x =on")
	b, _ := parseRule("light.x  =on")
	if a.ID != b.ID || a.ID == "" {
		t.Errorf("ids %q %q", a.ID, b.ID)
	}
}

// fakeHub is the hub's API: what was notified and asked.
type fakeHub struct {
	srv      *httptest.Server
	mu       sync.Mutex
	notified []notice
	asked    []string
}

func newFakeHub(t *testing.T) *fakeHub {
	h := &fakeHub{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pext_test" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/notify":
			var n notice
			_ = json.NewDecoder(r.Body).Decode(&n)
			h.notified = append(h.notified, n)
			fmt.Fprint(w, `{"sent":true}`)
		case "/api/v1/assistant/conversations":
			var b struct{ Prompt string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			h.asked = append(h.asked, b.Prompt)
			fmt.Fprint(w, `{"session":{"id":"s1"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.notified), len(h.asked)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func st(id, state string, attrs map[string]any) *State {
	if attrs == nil {
		attrs = map[string]any{}
	}
	return &State{EntityID: id, State: state, Attributes: attrs}
}

func TestWatch(t *testing.T) {
	batchFor, holdUnit = 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { batchFor, holdUnit = 3*time.Second, time.Second })
	r := newRichHA(t)
	hub := newFakeHub(t)
	a := richApp(t, r, Config{Watches: []string{
		"binary_sensor@厨房 class=moisture =on level=time-sensitive name=漏水",
		"sensor.bath_humidity >75 for=20 do=both message=开一下排风扇",
		"switch.* offline for=5",
		"nonsense",
	}})
	a.hub = &hubAPI{url: hub.srv.URL, token: "pext_test", scopes: []string{"notify", "assistant.ask"}, http: http.DefaultClient}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watch.run(ctx)
	waitFor(t, "the subscription", func() bool { return len(r.asked("subscribe_events")) == 1 })
	waitFor(t, "connected", func() bool { a.watch.mu.Lock(); defer a.watch.mu.Unlock(); return a.watch.connected })
	got := callT(t, a, id+"_watches", nil)
	contains(t, got, "家里的提醒规则 3 条，正在实时盯着", "「漏水」当「厨房」里的传感器", "设置里的", "读不懂：设置里第 4 条（nonsense）")

	// a leak: at once, time-sensitive, a tap opens the entity
	leak := map[string]any{"friendly_name": "厨房漏水", "device_class": "moisture"}
	r.push("binary_sensor.leak", st("binary_sensor.leak", "off", leak), st("binary_sensor.leak", "on", leak))
	waitFor(t, "the leak's notification", func() bool { n, _ := hub.counts(); return n == 1 })
	hub.mu.Lock()
	n := hub.notified[0]
	hub.mu.Unlock()
	if n.Title != "家里：漏水" || !strings.Contains(n.Body, "厨房漏水（binary_sensor.leak）：有水") || n.Level != "time-sensitive" || n.UI != pageUI || n.State == "" {
		t.Fatalf("notice %+v", n)
	}
	// still on, again on: nothing new; within the cooldown: nothing either
	r.push("binary_sensor.leak", st("binary_sensor.leak", "on", leak), st("binary_sensor.leak", "on", leak))
	r.push("binary_sensor.leak", st("binary_sensor.leak", "on", leak), st("binary_sensor.leak", "off", leak))
	r.push("binary_sensor.leak", st("binary_sensor.leak", "off", leak), st("binary_sensor.leak", "on", leak))
	time.Sleep(50 * time.Millisecond)
	if n, _ := hub.counts(); n != 1 {
		t.Fatalf("%d notifications", n)
	}

	// humidity above 75 for 20 (×10ms): a dip below cancels it, staying above fires both ways
	hum := map[string]any{"friendly_name": "浴室湿度", "unit_of_measurement": "%"}
	r.push("sensor.bath_humidity", st("sensor.bath_humidity", "60", hum), st("sensor.bath_humidity", "80", hum))
	r.push("sensor.bath_humidity", st("sensor.bath_humidity", "80", hum), st("sensor.bath_humidity", "70", hum))
	time.Sleep(300 * time.Millisecond)
	if n, q := hub.counts(); n != 1 || q != 0 {
		t.Fatalf("a dip did not cancel: %d %d", n, q)
	}
	r.push("sensor.bath_humidity", st("sensor.bath_humidity", "70", hum), st("sensor.bath_humidity", "82", hum))
	r.push("sensor.bath_humidity", st("sensor.bath_humidity", "82", hum), st("sensor.bath_humidity", "85", hum))
	waitFor(t, "the humidity's", func() bool { n, q := hub.counts(); return n == 2 && q == 1 })
	hub.mu.Lock()
	prompt, body := hub.asked[0], hub.notified[1].Body
	hub.mu.Unlock()
	contains(t, prompt, "Home Assistant 的提醒规则触发了", "浴室湿度（sensor.bath_humidity）：85 %", "用户给这条规则的要求：开一下排风扇")
	if strings.Contains(body, "开一下排风扇") {
		t.Errorf("the assistant's instruction in the notification: %q", body)
	}

	// Home Assistant restarting: many switches offline at once, one notification after the hold
	for _, e := range []string{"switch.living_plug", auxSwitch, "switch.ceiling_power_default"} {
		r.push(e, st(e, "on", nil), st(e, "unavailable", nil))
	}
	r.push("switch.living_plug", st("switch.living_plug", "unavailable", nil), st("switch.living_plug", "on", nil)) // back before the hold
	waitFor(t, "the offline one", func() bool { n, _ := hub.counts(); return n == 3 })
	hub.mu.Lock()
	n = hub.notified[2]
	hub.mu.Unlock()
	if n.Title != "家里：2 个设备" || strings.Contains(n.Body, "living_plug") || !strings.Contains(n.Body, "离线") {
		t.Fatalf("offline notice %+v", n)
	}
	contains(t, callT(t, a, id+"_watches", nil), "## 最近触发", "推送了，叫了助手（对话 s1）")

	// the assistant's rules: a card, then kept in the data directory
	add := map[string]any{"op": "add", "name": "门开了", "entity_id": "binary_sensor.*_door", "to": "on", "for_seconds": 0}
	card, err := describe(t, a, watchAction, add)
	if err != nil || !strings.Contains(card, "新建家里的提醒规则 w") || !strings.Contains(card, "「门开了」当binary_sensor.*_door变成 on → 推送到手机") {
		t.Fatalf("watch card: %q %v", card, err)
	}
	text, err := doAction(t, a, watchAction, add, "w1")
	if err != nil || !strings.Contains(text, "已加上规则") {
		t.Fatalf("add: %q %v", text, err)
	}
	raw, err := os.ReadFile(filepath.Join(a.dataDir, "watches.json"))
	if err != nil || !strings.Contains(string(raw), "binary_sensor.*_door") {
		t.Fatalf("saved: %s %v", raw, err)
	}
	door := map[string]any{"friendly_name": "入户门"}
	r.push("binary_sensor.front_door", st("binary_sensor.front_door", "off", door), st("binary_sensor.front_door", "on", door))
	waitFor(t, "the door's", func() bool { n, _ := hub.counts(); return n == 4 })
	if _, err := describe(t, a, watchAction, add); err == nil || !strings.Contains(err.Error(), "已经有这条规则") {
		t.Fatalf("a rule twice: %v", err)
	}
	var rid string
	for _, x := range a.watch.rules() {
		if x.Source == "assistant" {
			rid = x.ID
		}
	}
	// a new run reads them back
	b := newApp(t.Logf)
	b.watch.load(a.dataDir)
	if len(b.watch.rules()) != 1 || b.watch.rules()[0].ID != rid {
		t.Fatalf("reloaded %v", b.watch.rules())
	}
	settingsID := a.watch.rules()[0].ID
	if _, err := describe(t, a, watchAction, map[string]any{"op": "remove", "id": settingsID}); err == nil || !strings.Contains(err.Error(), "设置的「提醒规则」里删") {
		t.Fatalf("removing a settings rule: %v", err)
	}
	if _, err := doAction(t, a, watchAction, map[string]any{"op": "remove", "id": rid}, "w2"); err != nil {
		t.Fatal(err)
	}
	if len(a.watch.rules()) != 3 {
		t.Fatalf("after remove %d", len(a.watch.rules()))
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"op": "add", "to": "on"}, "要写看哪些实体"},
		{map[string]any{"op": "add", "entity_id": "light.x"}, "要写条件"},
		{map[string]any{"op": "add", "entity_id": "light.x", "to": "on", "do": "sms"}, "do 要是"},
		{map[string]any{"op": "remove", "id": "nope"}, "没有规则 nope"},
		{map[string]any{"op": "edit"}, "op 要是"},
	} {
		if _, err := describe(t, a, watchAction, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.args, c.want, err)
		}
	}
}

// A hub that did not grant the scopes: the firing is recorded with why it did not go out.
func TestWatchWithoutScopes(t *testing.T) {
	batchFor = 10 * time.Millisecond
	t.Cleanup(func() { batchFor = 3 * time.Second })
	r := newRichHA(t)
	a := richApp(t, r, Config{Watches: []string{"binary_sensor.leak =on"}})
	a.hub = &hubAPI{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watch.run(ctx)
	waitFor(t, "the subscription", func() bool { return len(r.asked("subscribe_events")) == 1 })
	contains(t, callT(t, a, id+"_watches", nil), "注意：hub 没给扩展「给你的设备发通知」这项权限")
	r.push("binary_sensor.leak", st("binary_sensor.leak", "off", nil), st("binary_sensor.leak", "on", nil))
	waitFor(t, "the record", func() bool { return strings.Contains(callT(t, a, id+"_watches", nil), "推送失败") })
}

func TestWSAuth(t *testing.T) {
	r := newRichHA(t)
	h := newHA(r.srv.URL, "wrong", false)
	if err := h.WS(context.Background(), "config/device_registry/list", nil, nil); err != errWSAuth {
		t.Fatalf("got %v", err)
	}
	h = newHA(r.srv.URL, r.token, false)
	defer h.closeWS()
	if err := h.WS(context.Background(), "no/such", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown_command") {
		t.Fatalf("got %v", err)
	}
	var devs []RegDevice
	if err := h.WS(context.Background(), "config/device_registry/list", nil, &devs); err != nil || len(devs) != 3 {
		t.Fatalf("devices %v %v", devs, err)
	}
}

// ---- the page, with the registry ----

// TestPageDevices: the page on a home like the user's, where a ceiling light's device also has an auxiliary light
// switch and a power-on default (config) switch. The overview names what is on and leaves the settings out; the area
// is a row per device, the settings folded into it; a light and a plug switch from their rows; the open light shows
// its device's settings folded; 动态 and 自动化 work, with no automations at all too.
func TestPageDevices(t *testing.T) {
	r := newRichHA(t)
	a := richApp(t, r, Config{})

	v, _ := a.render(context.Background(), req(nil, "", nil))
	got := check(t, v)
	contains(t, got, "row 客厅|客厅灯、吸顶灯、插座 等 4 个开着 · 27.5°|", "[4 开着 positive]")
	if strings.Contains(got, "辅光") || strings.Contains(got, "通电默认") {
		t.Errorf("a device's settings counted as on:\n%s", got)
	}

	res, st := act(t, a, pageState{}, "area:living", nil)
	got = texts(res.View)
	contains(t, got, "## 灯 · 2", "row 吸顶灯|另有 2 项|", "→do:light.ceiling|turn_off", "row 客厅灯|亮度 50%|",
		"## 开关 · 1", "row 插座||", "→do:switch.living_plug|turn_off", "## 播放器 · 1", "row 小爱音箱||", "→open:"+speaker,
		"还有 1 个离线的")
	for _, no := range []string{"辅光", "通电默认", "藏起来的开关", "客厅温度", "空调风模式"} {
		if strings.Contains(got, no) {
			t.Errorf("%s has a row of its own:\n%s", no, got)
		}
	}
	act(t, a, st, "do:switch.living_plug|turn_off", nil)
	if f(r).lastCall() != "switch.turn_off [switch.living_plug] {}" {
		t.Fatalf("the plug's row: %q", f(r).lastCall())
	}

	// the light opened: its device's settings folded into one row, unfolded on a tap
	res, st = act(t, a, st, "open:light.ceiling", nil)
	got = texts(res.View)
	contains(t, got, "breadcrumb ||||| /家→home /客厅→back /吸顶灯", "row 「吸顶灯」的设置和其他项 · 2|吸顶灯 辅光开关、吸顶灯 厂商自定义 通电默认状态|", "→sub")
	res, _ = act(t, a, st, "sub", nil)
	contains(t, texts(res.View), "## 「吸顶灯」的设置和其他项 · 2", "row 吸顶灯 厂商自定义 通电默认状态|设置 · 开关|", "→open:switch.ceiling_power_default", "收起")

	// 动态: the last two hours, newest first, a noisy sensor one row, hidden areas left out
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	rows := []string{
		`{"when":"` + at(30) + `","name":"客厅吸顶灯","state":"on","entity_id":"light.ceiling","context_entity_id":"automation.night","context_name":"夜灯"}`,
		`{"when":"` + at(5) + `","name":"厨房漏水","state":"on","entity_id":"binary_sensor.leak"}`,
		`{"when":"` + at(3) + `","name":"别处的灯","state":"on","entity_id":"light.other_home"}`,
	}
	for i := range 12 {
		rows = append(rows, fmt.Sprintf(`{"when":"%s","name":"客厅温度","state":"%d","entity_id":"sensor.living_temp"}`, at(60-i*4), 20+i))
	}
	r.logbook = "[" + strings.Join(rows, ",") + "]"
	res, st = act(t, a, pageState{}, "tab", map[string]string{"tab": "log"})
	got = texts(res.View)
	contains(t, got, `[tabs tab="log"`, "## 最近 2 小时", "row 厨房漏水 → 有水|", "· 厨房|", "→open:binary_sensor.leak",
		"row 客厅吸顶灯 → 开|", "· 客厅 · 由自动化「夜灯」|", "row 客厅温度 变化了 12 次|", "最后是31 °C")
	if strings.Contains(got, "别处") || strings.Index(got, "厨房漏水") > strings.Index(got, "客厅吸顶灯") || res.View.Search != nil {
		t.Errorf("hidden, out of order, or a search box:\n%s", got)
	}
	if res, back := act(t, a, st, "open:binary_sensor.leak", nil); !strings.Contains(texts(res.View), "/动态→back") || back.Tab != "log" {
		t.Errorf("from 动态:\n%s", texts(res.View))
	}
	r.logbook = "[]"
	res, _ = act(t, a, st, "refresh", nil)
	contains(t, texts(res.View), "empty 最近 2 小时家里没有变化")

	// 自动化: none yet (the user's home), then an automation, a script and a scene
	res, st = act(t, a, pageState{}, "tab:auto", nil)
	contains(t, texts(res.View), "empty 还没有自动化、脚本和场景")
	r.mu.Lock()
	for _, s := range []State{
		{EntityID: "automation.night", State: "on", Attributes: map[string]any{"friendly_name": "夜灯", "id": "1", "last_triggered": at(30)}},
		{EntityID: "script.movie", State: "off", Attributes: map[string]any{"friendly_name": "看电影"}},
		{EntityID: "scene.relax", State: at(90), Attributes: map[string]any{"friendly_name": "放松"}},
	} {
		r.state[s.EntityID] = s
	}
	r.mu.Unlock()
	res, st = act(t, a, st, "refresh", nil)
	got = texts(res.View)
	contains(t, got, "## 自动化 · 1", "row 夜灯|上次 ", "[开着 positive] →open:automation.night", "## 脚本 · 1",
		"→do:script.movie|turn_on (confirm 运行脚本「看电影」？)", "## 场景 · 1", "→do:scene.relax|turn_on (confirm 执行场景「放松」？)")
	act(t, a, st, "do:script.movie|turn_on", nil)
	if f(r).lastCall() != "script.turn_on [script.movie] {}" {
		t.Fatalf("script: %q", f(r).lastCall())
	}
	res, st = act(t, a, st, "open:automation.night", nil)
	contains(t, texts(res.View), "/自动化→back", "button 停用||||| →svc:turn_off (confirm 停用「夜灯」？)", "→svc:trigger (confirm 触发一次「夜灯」？)")
	act(t, a, st, "svc:trigger", nil)
	if f(r).lastCall() != "automation.trigger [automation.night] {}" {
		t.Fatalf("trigger: %q", f(r).lastCall())
	}
	if _, home := act(t, a, st, "tab", map[string]string{"tab": "home"}); home.Tab != "" || home.Entity != "" {
		t.Errorf("back home: %+v", home)
	}
}
