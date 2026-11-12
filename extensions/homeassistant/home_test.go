package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The overview (home.go) on synthetic homes, for an app of UI version 6 and for older ones.

// reqV is req for an app of UI version ver.
func reqV(ver int, state any, action string, values map[string]string) *ext.UIRequest {
	r := req(state, action, values)
	r.UIVersion = ver
	return r
}

// renderV draws the page for an app of UI version ver, checked as the hub checks it.
func renderV(t *testing.T, a *app, ver int, st any) (*wire.View, string) {
	t.Helper()
	v, err := a.render(context.Background(), reqV(ver, st, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	return v, check(t, v)
}

// actV is act for an app of UI version ver.
func actV(t *testing.T, a *app, ver int, st pageState, action string, values map[string]string) (*wire.ActionResult, pageState) {
	t.Helper()
	res, err := a.action(context.Background(), reqV(ver, st, action, values))
	if err != nil || res.View == nil {
		t.Fatalf("%s: %v", action, err)
	}
	check(t, res.View)
	var next pageState
	_ = json.Unmarshal([]byte(res.View.State), &next)
	return res, next
}

// titles are a view's section titles, in order, the untitled ones left out.
func titles(v *wire.View) []string {
	var out []string
	for _, s := range v.Sections {
		if s.Title != "" {
			out = append(out, s.Title)
		}
	}
	return out
}

// tilesOf are the tiles of the grid or carousel keyed key.
func tilesOf(v *wire.View, key string) []wire.Tile {
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Key == key {
				return it.Tiles
			}
		}
	}
	return nil
}

// put adds states to the fake home.
func (f *fakeHA) put(ss ...State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range ss {
		f.state[s.EntityID] = s
	}
}

// areasJSON writes areas as the fake's template answer: id, name, entities.
func areasJSON(areas [][3]any) string {
	b, _ := json.Marshal(areas)
	return string(b)
}

func TestHomeTiles(t *testing.T) {
	f := newFakeHA(t)
	a := newTestApp(t, f, Config{})
	v, got := renderV(t, a, 6, nil)
	contains(t, got, "stat 开着=2|1 灯 · 1 空调|positive", "stat 温度=27.5°|客厅|", "stat 需注意=2|1 报警 · 1 离线|danger",
		"row 漏水|厨房|||| [有水 danger] →open:binary_sensor.leak icon=droplet/red",
		"row 风扇插座|客厅|||| [离线 caution] →open:switch.fan_plug icon=power/gray",
		"tile 客厅灯|客厅|亮度 50% icon=light/amber active ctl(switch power:light.living true →ctl:power light.living) →open:light.living",
		"tile 空调|客厅 · 制冷|设定 26° icon=thermometer/blue active →open:climate.living_ac",
		"tile 卧室灯|卧室| icon=light/amber ctl(switch power:light.bed false →ctl:power light.bed) →open:light.bed",
		"row 客厅灯|客厅 · 亮度|||| →open:light.living icon=light/amber ctl(slider bri:light.living 50 1–100 step 1 % →ctl:bri light.living)",
		"row 空调|客厅 · 制冷 · 室温 27.5°|||| →open:climate.living_ac icon=thermometer/blue ctl(stepper temp:climate.living_ac 26 16–30 step 1 °C →ctl:temp climate.living_ac)",
		"tile 厨房|| icon=home/amber [有水 danger] →area:kitchen",
		"tile 客厅|1 灯、1 空调开着|27.5° icon=sofa/orange active [1 离线 caution] →area:living",
		"carousel 其他区域 · 1", "tile 卧室|2 个设备| icon=moon/indigo →area:bedroom")
	if want := []string{"需要注意 · 2", "设备 · 3", "调节 · 2", "区域 · 3"}; !slices.Equal(titles(v), want) {
		t.Errorf("sections %q, want %q:\n%s", titles(v), want, got)
	}
	if v.Sections[0].Items[0].Type != "stat_grid" {
		t.Errorf("the figures come first:\n%s", got)
	}
	// alarms before the busiest area; a climate without turn_on / turn_off has no switch
	if strings.Index(got, "tile 厨房") > strings.Index(got, "tile 客厅|") || strings.Contains(got, "power:climate") {
		t.Errorf("area order, or a switch a climate cannot take:\n%s", got)
	}
	for _, no := range []string{"别处", "冷冻", "入户门锁", "shell_command", "都关着", "segmented", "点按"} {
		if strings.Contains(got, no) {
			t.Errorf("%s on the overview:\n%s", no, got)
		}
	}

	// an area: its devices as tiles too, its dials, no tap mode
	res, _ := actV(t, a, 6, pageState{}, "area:living", nil)
	got = texts(res.View)
	contains(t, got, "breadcrumb ||||| /家→home /客厅", "## 设备 · 2", "tile 客厅灯||亮度 50% icon=light/amber active ctl(switch power:light.living true",
		"tile 空调|制冷|设定 26°", "## 调节 · 2", "row 客厅灯|亮度||||", "还有 1 个离线的")
	if strings.Contains(got, "点按设备") || strings.Contains(got, "|客厅 · ") {
		t.Errorf("a tap mode next to switches, or the area named in its own page:\n%s", got)
	}
	res, _ = actV(t, a, 6, pageState{Area: "living"}, "offline", nil)
	contains(t, texts(res.View), "## 设备 · 3", "tile 风扇插座|| icon=power/blue [离线 caution] →open:switch.fan_plug", "不列离线的")
}

func TestHomeControls(t *testing.T) {
	f := newFakeHA(t)
	f.put(
		State{EntityID: "cover.curtain", State: "open", Attributes: map[string]any{"friendly_name": "卧室窗帘", "device_class": "curtain",
			"current_position": 40.0, "supported_features": 15.0}},
		State{EntityID: "fan.desk", State: "on", Attributes: map[string]any{"friendly_name": "客厅风扇", "percentage": 60.0,
			"percentage_step": 20.0, "supported_features": 1.0}},
		State{EntityID: "climate.old_ac", State: "cool", Attributes: map[string]any{"friendly_name": "卧室空调", "temperature": 25.0,
			"min_temp": 16.0, "max_temp": 30.0}}, // it cannot say it turns on and off: no switch
		State{EntityID: "cover.garage", State: "closed", Attributes: map[string]any{"friendly_name": "车库门", "device_class": "garage",
			"current_position": 0.0, "supported_features": 15.0}}, // a slider would open it at a touch: none
	)
	f.mu.Lock()
	ac := f.state["climate.living_ac"]
	ac.Attributes["supported_features"] = 385.0 // target temperature, turn off, turn on
	f.mu.Unlock()
	f.areas = areasJSON([][3]any{{"living", "客厅", []string{"light.living", "climate.living_ac", "sensor.living_temp", "switch.fan_plug", "fan.desk"}},
		{"bedroom", "卧室", []string{"light.bed", "lock.front", "cover.curtain", "climate.old_ac", "cover.garage"}},
		{"away", "别处", []string{"light.other_home"}}})
	a := newTestApp(t, f, Config{})
	_, got := renderV(t, a, 6, nil)
	contains(t, got, "tile 空调|客厅 · 制冷|设定 26° icon=thermometer/blue active ctl(switch power:climate.living_ac true",
		"tile 窗帘|卧室 · 开着|开度 40% icon=window/indigo active →open:cover.curtain",
		"tile 风扇|客厅|风速 60% icon=fan/teal active ctl(switch power:fan.desk true",
		"row 风扇|客厅 · 风速|||| →open:fan.desk icon=fan/teal ctl(slider pct:fan.desk 60 0–100 step 20 %",
		"row 窗帘|卧室 · 开度|||| →open:cover.curtain icon=window/indigo ctl(slider pos:cover.curtain 40 0–100 step 1 %",
		"tile 卧室|1 窗帘、1 空调开着| icon=moon/indigo active →area:bedroom",
		"stat 开着=5|1 灯 · 1 风扇 · 1 窗帘 · 2 空调|positive", "tile 空调|卧室 · 制冷|设定 25° icon=thermometer/blue active →open:climate.old_ac\n",
		"tile 车库门|卧室 · 关着|开度 0% icon=car/indigo →open:cover.garage\n")
	for _, no := range []string{"power:cover", "power:climate.old_ac", "pos:cover.garage"} {
		if strings.Contains(got, no) {
			t.Errorf("%s drawn:\n%s", no, got)
		}
	}
	// the bedroom: its lock a tile without a switch
	_, room := renderV(t, a, 6, pageState{Area: "bedroom"})
	contains(t, room, "tile 入户门锁|已上锁| icon=lock/gray →open:lock.front\n", "row 窗帘|开度||||")

	ctl := func(kind, eid, value string) *wire.ActionResult {
		t.Helper()
		name, _ := controlOf(kind, eid)
		res, _ := actV(t, a, 6, pageState{}, "ctl:"+kind, map[string]string{"entity": eid, name: value})
		return res
	}
	for _, c := range []struct{ kind, eid, value, call, toast string }{
		{"power", "light.living", "false", "light.turn_off [light.living] {}", "好了：客厅灯：关"},
		{"power", "light.bed", "true", "light.turn_on [light.bed] {}", "好了：卧室灯：开"},
		{"bri", "light.living", "30", `light.turn_on [light.living] {"brightness_pct":30}`, "好了：客厅灯"},
		{"temp", "climate.living_ac", "24.5", `climate.set_temperature [climate.living_ac] {"temperature":24.5}`, "好了：客厅空调"},
		{"power", "climate.living_ac", "false", "climate.turn_off [climate.living_ac] {}", "好了：客厅空调"},
		{"pct", "fan.desk", "40", `fan.set_percentage [fan.desk] {"percentage":40}`, "好了：客厅风扇"},
		{"pos", "cover.curtain", "70", `cover.set_cover_position [cover.curtain] {"position":70}`, "好了：卧室窗帘"},
	} {
		res := ctl(c.kind, c.eid, c.value)
		if f.lastCall() != c.call || !strings.Contains(res.Toast, c.toast) {
			t.Errorf("%s %s=%s: called %q, toast %q", c.kind, c.eid, c.value, f.lastCall(), res.Toast)
		}
	}

	// what the page never draws, or out of range: nothing is called, the toast says why
	for _, c := range []struct{ kind, eid, value, toast string }{
		{"power", "lock.front", "false", "不认识的操作"},
		{"pos", "lock.front", "0", "不认识的操作"},
		{"power", "light.living", "maybe", "不认识的操作"},
		{"bri", "fan.desk", "50", "不认识的操作"},
		{"pos", "light.living", "10", "不认识的操作"},
		{"explode", "light.living", "1", "不认识的操作"},
		{"power", "shell_command.reboot", "true", "不认识的操作"},
		{"power", "Light Living", "true", "不认识的操作"},
		{"temp", "climate.living_ac", "99", "要在 16 到 30 之间"},
		{"temp", "light.living", "20", "不认识的操作"},
		{"bri", "light.living", "0", "要在 1 到 100 之间"},
		{"bri", "light.bed", "30", "不认识的操作"},           // on and off only
		{"power", "climate.old_ac", "false", "不认识的操作"}, // no turn on / off
		{"pos", "cover.garage", "50", "不认识的操作"},        // a garage door's position
		{"pct", "fan.desk", "NaN", "要在 0 到 100 之间"},
		{"power", "light.other_home", "false", errNoEntity.Error()},
		{"power", "light.nowhere", "true", errNoEntity.Error()},
	} {
		f.mu.Lock()
		f.calls = nil
		f.mu.Unlock()
		if res := ctl(c.kind, c.eid, c.value); f.lastCall() != "" || !strings.Contains(res.Toast, c.toast) {
			t.Errorf("%s %s=%s went through: %q, toast %q", c.kind, c.eid, c.value, f.lastCall(), res.Toast)
		}
	}

	// a forged entity page in a hidden area: its buttons call nothing
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	if res, _ := act(t, a, pageState{Entity: "light.other_home"}, "svc:turn_off", nil); f.lastCall() != "" || res.Toast != errNoEntity.Error() {
		t.Errorf("a hidden area's entity: %q %q", f.lastCall(), res.Toast)
	}

	// a domain the settings leave out: no switch or dial drawn, a forged one refused
	b := newTestApp(t, f, Config{Writable: []string{"climate"}})
	_, got = renderV(t, b, 6, nil)
	contains(t, got, "tile 卧室灯|卧室 · 开| icon=light/amber active →open:light.bed", "ctl(switch power:climate.living_ac false")
	if strings.Contains(got, "power:light") || strings.Contains(got, "bri:") || strings.Contains(got, "pos:") {
		t.Errorf("controls for what may not be written:\n%s", got)
	}
	name, _ := controlOf("power", "light.bed")
	res, _ := actV(t, b, 6, pageState{}, "ctl:power", map[string]string{"entity": "light.bed", name: "false"})
	if f.lastCall() != "" || !strings.Contains(res.Toast, "不在扩展设置的可操作类型里") {
		t.Errorf("a forged switch went through: %q %q", f.lastCall(), res.Toast)
	}
}

func TestHomeAlerts(t *testing.T) {
	f := newFakeHA(t)
	f.put(
		State{EntityID: "binary_sensor.balcony_door", State: "on", Attributes: map[string]any{"friendly_name": "阳台门", "device_class": "door"}},
		State{EntityID: "cover.garage", State: "open", Attributes: map[string]any{"friendly_name": "车库门", "device_class": "garage", "supported_features": 3.0}},
		State{EntityID: "sensor.remote_battery", State: "12", Attributes: map[string]any{"friendly_name": "遥控器电量", "device_class": "battery", "unit_of_measurement": "%"}},
		State{EntityID: "sensor.door_battery", State: "85", Attributes: map[string]any{"friendly_name": "门磁电量", "device_class": "battery", "unit_of_measurement": "%"}},
		State{EntityID: "switch.plug_a", State: "unavailable", Attributes: map[string]any{"friendly_name": "插座 A"}},
		State{EntityID: "switch.plug_b", State: "unavailable", Attributes: map[string]any{"friendly_name": "插座 B"}},
		State{EntityID: "weather.home", State: "partlycloudy", Attributes: map[string]any{"friendly_name": "天气", "temperature": 12.3, "humidity": 60.0}},
	)
	f.mu.Lock()
	lk := f.state["lock.front"]
	lk.State = "unlocked"
	f.state[lk.EntityID] = lk
	f.mu.Unlock()
	f.areas = areasJSON([][3]any{
		{"living", "客厅", []string{"light.living", "climate.living_ac", "sensor.living_temp", "switch.fan_plug", "switch.plug_a", "switch.plug_b"}},
		{"bedroom", "卧室", []string{"light.bed", "lock.front", "sensor.remote_battery", "sensor.door_battery"}},
		{"kitchen", "厨房", []string{"binary_sensor.leak"}}, {"balcony", "阳台", []string{"binary_sensor.balcony_door"}},
		{"garage", "车库", []string{"cover.garage"}}, {"away", "别处", []string{"light.other_home"}}})
	a := newTestApp(t, f, Config{})
	v, got := renderV(t, a, 6, nil)
	contains(t, got, "stat 开着=3|1 灯 · 1 车库门 · 1 空调|positive", "stat 室外=12.3°|多云 · 湿度 60%|",
		"stat 需注意=8|1 报警 · 2 门窗 · 1 门锁 · 1 电量 · 3 离线|danger", "## 需要注意 · 8",
		"row 漏水|厨房|||| [有水 danger] →open:binary_sensor.leak icon=droplet/red",
		"row 阳台门|阳台|||| [开着 caution] →open:binary_sensor.balcony_door icon=door/orange",
		"row 车库门|车库|||| [开着 caution] →open:cover.garage icon=car/orange",
		"row 入户门锁|卧室|||| [没锁 caution] →open:lock.front icon=unlock/orange",
		"row 遥控器电量|卧室|||| [电量 12% caution] →open:sensor.remote_battery icon=battery/orange",
		"row 3 个设备离线|风扇插座、插座 A、插座 B|||| [离线 caution] →lost icon=wifi/gray",
		"tile 厨房|| icon=home/amber [有水 danger] →area:kitchen",
		"tile 客厅|1 灯、1 空调开着|27.5° icon=sofa/orange active [3 离线 caution] →area:living",
		"tile 车库|1 车库门开着| icon=car/gray active [开着 caution] →area:garage",
		"tile 卧室|2 个设备| icon=moon/indigo [2 项 caution] →area:bedroom",
		"tile 阳台|| icon=leaf/green [开着 caution] →area:balcony")
	if strings.Contains(got, "门磁电量") || strings.Contains(got, "carousel") {
		t.Errorf("a full battery needs a look, or a quiet area where none is:\n%s", got)
	}
	if order := []string{"row 漏水", "row 阳台门", "row 车库门", "row 入户门锁", "row 遥控器电量", "row 3 个设备离线"}; !inOrder(got, order...) {
		t.Errorf("not %q in order:\n%s", order, got)
	}
	if b := tilesOf(v, "areas"); len(b) != 5 || b[0].Title != "厨房" {
		t.Errorf("the areas, an alarm first: %+v", b)
	}

	// the offline ones unfold, and fold again
	res, st := actV(t, a, 6, pageState{}, "lost", nil)
	contains(t, texts(res.View), "row 风扇插座|客厅|||| [离线 caution] →open:switch.fan_plug icon=power/gray", "row 插座 A|客厅|||| [离线 caution]",
		"row 插座 B|客厅|||| [离线 caution]", "row 收起离线的设备")
	if !st.Lost || st.Offline {
		t.Fatal("the offline devices did not unfold, or the search and areas list offline ones now")
	}
	if res, st = actV(t, a, 6, st, "lost", nil); st.Lost || !strings.Contains(texts(res.View), "row 3 个设备离线") {
		t.Errorf("offline did not fold:\n%s", texts(res.View))
	}

	// an app before UI version 6: the same rows, without icons; the areas as rows with their badges
	_, got = renderV(t, a, 5, nil)
	contains(t, got, "row 阳台门|阳台|||| [开着 caution] →open:binary_sensor.balcony_door\n", "row 车库|1 车库门开着|||| [开着 caution] →area:garage",
		"row 客厅|1 灯、1 空调开着|27.5°||| [3 离线 caution] →area:living")
	if strings.Contains(got, "icon=") {
		t.Errorf("icons for an app before UI version 6:\n%s", got)
	}
}

// inOrder says whether each of want is in got, one after another.
func inOrder(got string, want ...string) bool {
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			return false
		}
		at += i + len(w)
	}
	return true
}

// sparseHome is a home like most: eleven areas, a light in each and a plug in every other one, everything off but the
// study's light, and the study's air the only one read.
func sparseHome(t *testing.T) *fakeHA {
	f := newFakeHA(t)
	f.mu.Lock()
	f.state = map[string]State{}
	f.mu.Unlock()
	var areas [][3]any
	for i, n := range []struct{ id, name string }{{"living", "客厅"}, {"master", "主卧"}, {"second", "次卧"}, {"study", "书房"},
		{"kitchen", "厨房"}, {"dining", "餐厅"}, {"bath", "卫生间"}, {"balcony", "阳台"}, {"entry", "玄关"}, {"kids", "儿童房"},
		{"storage", "储物间"}} {
		light := State{EntityID: "light." + n.id, State: "off", Attributes: map[string]any{"friendly_name": n.name + "主灯",
			"supported_color_modes": []any{"brightness"}}}
		ents := []string{light.EntityID}
		if n.id == "study" {
			light.State, light.Attributes["brightness"] = "on", 200.0
			f.put(State{EntityID: "sensor.study_temp", State: "23.74", Attributes: map[string]any{"friendly_name": "书房温度",
				"device_class": "temperature", "unit_of_measurement": "°C"}},
				State{EntityID: "sensor.study_humidity", State: "38.2", Attributes: map[string]any{"friendly_name": "书房湿度",
					"device_class": "humidity", "unit_of_measurement": "%"}})
			ents = append(ents, "sensor.study_temp", "sensor.study_humidity")
		}
		f.put(light)
		if i%2 == 0 {
			plug := State{EntityID: "switch." + n.id + "_plug", State: "off", Attributes: map[string]any{"friendly_name": n.name + "插座", "device_class": "outlet"}}
			f.put(plug)
			ents = append(ents, plug.EntityID)
		}
		areas = append(areas, [3]any{n.id, n.name, ents})
	}
	f.areas = areasJSON(areas)
	return f
}

func TestHomeSparse(t *testing.T) {
	f := sparseHome(t)
	a := newTestApp(t, f, Config{})
	v, got := renderV(t, a, 6, nil)
	contains(t, got, "stat 开着=1|1 灯|positive", "stat 温度=23.7°|书房|", "stat 湿度=38%|书房|",
		"## 设备 · 17", "tile 主灯|书房|亮度 78% icon=light/amber active ctl(switch power:light.study true",
		"tile 插座|客厅| icon=plug/blue ctl(switch power:switch.living_plug false", "## 调节 · 1",
		"row 主灯|书房 · 亮度|||| →open:light.study icon=light/amber ctl(slider bri:light.study 78 1–100 step 1 %",
		"## 区域 · 11", "tile 书房|1 灯开着|23.7° · 38% icon=doc/blue active →area:study", "carousel 其他区域 · 10")
	if want := []string{"设备 · 17", "调节 · 1", "区域 · 11"}; !slices.Equal(titles(v), want) {
		t.Errorf("sections %q, want %q:\n%s", titles(v), want, got)
	}
	if n := len(tilesOf(v, "devices")); n != 17 || !tilesOf(v, "devices")[0].Active {
		t.Errorf("%d device tiles, the one on first:\n%s", n, got)
	}
	if lively := tilesOf(v, "areas"); len(lively) != 1 || lively[0].Title != "书房" {
		t.Errorf("the lively areas: %+v", lively)
	}
	var looks []string
	for _, tl := range tilesOf(v, "quiet") {
		looks = append(looks, tl.Title+"="+tl.Icon+"/"+tl.Color+" "+tl.Subtitle)
	}
	if want := []string{"客厅=sofa/orange 2 个设备", "主卧=moon/indigo 1 个设备", "次卧=moon/indigo 2 个设备", "厨房=home/amber 2 个设备",
		"餐厅=home/amber 1 个设备", "卫生间=droplet/teal 2 个设备", "阳台=leaf/green 1 个设备", "玄关=door/purple 2 个设备",
		"儿童房=star/pink 1 个设备", "储物间=folder/gray 2 个设备"}; !slices.Equal(looks, want) {
		t.Errorf("the quiet areas, in Home Assistant's order:\n%q\nwant\n%q", looks, want)
	}
	if strings.Contains(got, "都关着") || strings.Contains(got, "需要注意") || strings.Contains(got, "全部设备") {
		t.Errorf("a wall of 都关着, nothing to look at, or devices held back:\n%s", got)
	}

	// an app before UI version 6: the quiet areas are one row, unfolded on a tap
	_, got = renderV(t, a, 5, nil)
	contains(t, got, "row 书房|1 灯开着|23.7° · 38%||| [1 开着 positive] →area:study",
		"row 其他区域 · 10|都关着：客厅、主卧、次卧、厨房、餐厅、卫生间、阳台、玄关、儿童房、储物间|||| →rooms",
		"row 主灯|书房 · 亮度 78%|||| [开 positive] →do:light.study|turn_off")
	if n := strings.Count(got, "都关着"); n != 1 {
		t.Errorf("都关着 %d times:\n%s", n, got)
	}
	res, st := actV(t, a, 5, pageState{}, "rooms", nil)
	got = texts(res.View)
	contains(t, got, "row 客厅|2 个设备|||| →area:living", "row 儿童房|1 个设备|||| →area:kids", "row 收起没开东西的区域||||| →rooms")
	if !st.Rooms || strings.Contains(got, "其他区域") {
		t.Errorf("the quiet areas did not unfold:\n%s", got)
	}

	// an app of UI version 1: metrics for the figures, the tap mode a row
	_, got = renderV(t, a, 1, nil)
	contains(t, got, "metric ||1||开着|1 灯", "metric ||23.7°||温度|书房", "row 点设备是打开它||||| →pick")
}

func TestHomeLarge(t *testing.T) {
	f := newFakeHA(t)
	f.mu.Lock()
	f.state = map[string]State{}
	f.mu.Unlock()
	long := "light." + strings.Repeat("a", 130) // longer than an action id, a key or a control's name may be
	var areas [][3]any
	for i := range 40 {
		id := fmt.Sprintf("room%02d", i)
		var ents []string
		for j := range 4 {
			l := State{EntityID: fmt.Sprintf("light.%s_%d", id, j), State: "off", Attributes: map[string]any{
				"friendly_name": fmt.Sprintf("灯 %d-%d", i, j), "supported_color_modes": []any{"brightness"}}}
			if i < 5 && j == 0 {
				l.State, l.Attributes["brightness"] = "on", 128.0
			}
			f.put(l)
			ents = append(ents, l.EntityID)
		}
		f.put(State{EntityID: "switch." + id, State: "off", Attributes: map[string]any{"friendly_name": fmt.Sprintf("开关 %d", i)}})
		ents = append(ents, "switch."+id)
		if i%4 == 0 {
			f.put(State{EntityID: "sensor." + id + "_temp", State: fmt.Sprint(20 + i/4), Attributes: map[string]any{
				"device_class": "temperature", "unit_of_measurement": "°C"}})
			ents = append(ents, "sensor."+id+"_temp")
		}
		if i == 39 {
			f.put(State{EntityID: long, State: "on", Attributes: map[string]any{"friendly_name": "很长的灯", "brightness": 255.0,
				"supported_color_modes": []any{"brightness"}}})
			ents = append(ents, long)
		}
		areas = append(areas, [3]any{id, fmt.Sprintf("房间 %02d", i), ents})
	}
	f.areas = areasJSON(areas)
	a := newTestApp(t, f, Config{})

	v, got := renderV(t, a, 6, nil)
	devices := tilesOf(v, "devices")
	if len(devices) != quickMost {
		t.Fatalf("%d device tiles:\n%s", len(devices), got)
	}
	for i, tl := range devices {
		if tl.Active != (i < 6) {
			t.Errorf("tile %d %s: those on first", i, tl.Title)
		}
	}
	contains(t, got, "## 设备 · 201", "row 全部设备 · 201|现在列的是开着的和前几个|||| →more", "## 调节 · 6", "## 区域 · 40",
		"stat 开着=6|6 灯|positive", "stat 温度=24.5°|10 个区域 20–29°|")
	// 26 quiet areas, more than a carousel takes: one row, unfolding into tiles
	contains(t, got, "row 其他区域 · 26|都关着：房间 05、房间 06、房间 07、")
	if n := len(tilesOf(v, "areas")); n != 14 || tilesOf(v, "quiet") != nil {
		t.Errorf("%d lively area tiles, or the quiet ones unfolded", n)
	}
	if res, st := actV(t, a, 6, pageState{}, "rooms", nil); len(tilesOf(res.View, "quiet")) != 26 || !st.Rooms ||
		!strings.Contains(texts(res.View), "row 收起没开东西的区域||||| →rooms") {
		t.Errorf("the quiet areas did not unfold:\n%s", texts(res.View))
	}

	// the long id: a key and a name that fit, the entity in the action's values; its tile opens and switches it
	var lt *wire.Tile
	for i := range devices {
		if devices[i].Action.Values["entity"] == long {
			lt = &devices[i]
		}
	}
	if lt == nil || lt.Action.ID != "open" || len(lt.Key) > idMost || lt.Control == nil || len(lt.Control.Name) > idMost {
		t.Fatalf("the long id's tile: %+v", lt)
	}
	if _, st := actV(t, a, 6, pageState{}, "open", lt.Action.Values); st.Entity != long {
		t.Errorf("open by values: %+v", st)
	}
	res, _ := actV(t, a, 6, pageState{}, "ctl:power", map[string]string{"entity": long, lt.Control.Name: "false"})
	if want := fmt.Sprintf("light.turn_off [%s] {}", long); f.lastCall() != want || !strings.Contains(res.Toast, "很长的灯") {
		t.Errorf("the long id's switch: %q %q", f.lastCall(), res.Toast)
	}
	renderV(t, a, 6, pageState{Area: "room39"})
	renderV(t, a, 5, pageState{Area: "room39"})
	renderV(t, a, 3, pageState{Area: "room39"})

	// every device, up to a grid's 60, and back
	res, st := actV(t, a, 6, pageState{}, "more", nil)
	got = texts(res.View)
	if n := len(tilesOf(res.View, "devices")); n != tilesMost || !st.All {
		t.Errorf("全部设备: %d tiles", n)
	}
	contains(t, got, "## 设备 · 201 | 只列前 60 个，其余的在各区域里", "row 只看开着的和前几个||||| →more")
	if _, st = actV(t, a, 6, st, "more", nil); st.All {
		t.Error("more did not fold")
	}

	// an app before UI version 6: rows, the quiet areas folded into one (27: the long light was switched off above)
	_, got = renderV(t, a, 5, nil)
	contains(t, got, "## 设备 · 201", "row 全部设备 · 201", "## 区域 · 40", "row 其他区域 · 27|都关着：房间 05、房间 06、")
	res, _ = actV(t, a, 5, pageState{}, "rooms", nil)
	if n := strings.Count(texts(res.View), "→area:"); n != 40 {
		t.Errorf("%d area rows unfolded", n)
	}
}

func TestHomeNoAreas(t *testing.T) {
	f := newFakeHA(t)
	f.areas = "[]"
	a := newTestApp(t, f, Config{})
	_, got := renderV(t, a, 6, nil)
	contains(t, got, "stat 开着=3|2 灯 · 1 空调|positive", "stat 需注意=2|1 报警 · 1 离线|danger", "row 厨房漏水|传感器|||| [有水 danger]",
		"## 设备 · 4", "tile 客厅灯||亮度 50%", "tile 别处的灯||", "## 区域 · 0", "empty 还没有区域|||||在 Home Assistant 里给设备分好区域")
	_, got = renderV(t, a, 5, nil)
	contains(t, got, "row 客厅灯|亮度 50%|||| [开 positive] →do:light.living|turn_off", "empty 还没有区域")
}

// What the overview counts: a sounding siren is an alarm; a remote ("on" whenever reachable) is not a device that is
// on; devices in no area count, after the others, but not what Home Assistant keeps of a device it no longer has; a
// device whose state is unknown answers (not offline); a device's low battery reported twice is one alert.
func TestHomeEdges(t *testing.T) {
	r := newRichHA(t)
	f := r.fakeHA
	f.put(
		State{EntityID: "siren.hall", State: "on", Attributes: map[string]any{"friendly_name": "门厅警报器"}},
		State{EntityID: "remote.tv", State: "on", Attributes: map[string]any{"friendly_name": "电视遥控"}},
		State{EntityID: "light.loose", State: "on", Attributes: map[string]any{"friendly_name": "没分区的灯", "supported_color_modes": []any{"onoff"}}},
		State{EntityID: "switch.gone", State: "unavailable", Attributes: map[string]any{"friendly_name": "拆掉的插座", "restored": true}},
		State{EntityID: "cover.rf_blind", State: "unknown", Attributes: map[string]any{"friendly_name": "卧室百叶", "device_class": "blind"}},
		State{EntityID: "sensor.door_battery", State: "8", Attributes: map[string]any{"friendly_name": "门磁电量", "device_class": "battery", "unit_of_measurement": "%"}},
		State{EntityID: "binary_sensor.door_battery_low", State: "on", Attributes: map[string]any{"friendly_name": "门磁电量低", "device_class": "battery"}},
	)
	r.ws.entities = append(r.ws.entities, map[string]any{"entity_id": "sensor.door_battery", "device_id": "dev_door", "entity_category": "diagnostic"},
		map[string]any{"entity_id": "binary_sensor.door_battery_low", "device_id": "dev_door"})
	f.areas = areasJSON([][3]any{
		{"living", "客厅", []string{"light.living", "light.ceiling", auxSwitch, "switch.ceiling_power_default", "switch.living_plug", "switch.living_hidden",
			"switch.fan_plug", speaker, "climate.living_ac", "sensor.living_temp", "siren.hall", "remote.tv"}},
		{"bedroom", "卧室", []string{"light.bed", "lock.front", "cover.rf_blind", "sensor.door_battery", "binary_sensor.door_battery_low"}},
		{"kitchen", "厨房", []string{"binary_sensor.leak"}}, {"away", "别处", []string{"light.other_home"}}})
	a := richApp(t, r, Config{})
	v, got := renderV(t, a, 6, nil)
	contains(t, got, "stat 开着=6|3 灯 · 1 开关 · 1 空调 · 1 警报器|positive", "stat 需注意=4|2 报警 · 1 电量 · 1 离线|danger",
		"row 门厅警报器|客厅|||| [响着 danger] →open:siren.hall icon=bell/red", "row 门磁电量|卧室|||| [电量 8% caution]",
		"row 风扇插座|客厅|||| [离线 caution] →open:switch.fan_plug", "tile 百叶|卧室 · 未知| icon=window/indigo →open:cover.rf_blind",
		"tile 没分区的灯|| icon=light/amber active ctl(switch power:light.loose true")
	for _, no := range []string{"遥控", "拆掉的插座", "门磁电量低", "辅光", "藏起来的开关"} {
		if strings.Contains(got, no) {
			t.Errorf("%s on the overview:\n%s", no, got)
		}
	}
	// the device in no area goes after the areas' devices that are on, and before those off
	d := tilesOf(v, "devices")
	i := slices.IndexFunc(d, func(x wire.Tile) bool { return x.Title == "没分区的灯" })
	if i != 4 || !d[i-1].Active || i+1 >= len(d) || d[i+1].Active {
		t.Errorf("the device in no area at %d of %+v", i, d)
	}
	if n := alarmCount(a.cachedHome(), a.pageRegistry(context.Background())); n != 2 {
		t.Errorf("the tab's badge says %d alarms, the overview 2", n)
	}
	// the area's page counts as its tile does: the remote is not on
	_, room := renderV(t, a, 6, pageState{Area: "living"})
	contains(t, room, "stat 开着=5||positive")
	contains(t, got, "tile 客厅|2 灯、1 开关、1 空调、1 警报器开着|")
}

// Entity and area ids longer than an action id: every list still draws (the hub refuses a whole page for one id too
// long), and its rows act through the action's values.
func TestLongIDs(t *testing.T) {
	r := newRichHA(t)
	f := r.fakeHA
	auto, script, area := "automation."+strings.Repeat("b", 130), "script."+strings.Repeat("c", 130), strings.Repeat("d", 130)
	loc := req(nil, "", nil).Location()
	at := time.Now().In(loc).Add(-10 * time.Minute)
	f.put(State{EntityID: auto, State: "on", Attributes: map[string]any{"friendly_name": "很长的自动化", "id": "9", "last_triggered": at.Format(time.RFC3339)}},
		State{EntityID: script, State: "off", Attributes: map[string]any{"friendly_name": "很长的脚本"}})
	f.areas = areasJSON([][3]any{{"living", "客厅", []string{"light.living", "climate.living_ac"}}, {area, "很长的区域", []string{"light.bed"}}})
	r.logbook = `[{"when":"` + at.Format(time.RFC3339) + `","name":"很长的自动化","state":"on","entity_id":"` + auto + `"}]`
	a := richApp(t, r, Config{})

	res, st := act(t, a, pageState{}, "tab:auto", nil)
	var run *wire.Action
	for _, s := range res.View.Sections {
		for _, it := range s.Items {
			if it.Title == "很长的脚本" {
				run = it.Action
			}
		}
	}
	if run == nil || run.ID != "do" || run.Values["entity"] != script || run.Confirm == "" {
		t.Fatalf("the long script's row: %+v", run)
	}
	act(t, a, st, "do", run.Values)
	if want := fmt.Sprintf("script.turn_on [%s] {}", script); f.lastCall() != want {
		t.Errorf("the long script: %q", f.lastCall())
	}
	res, _ = act(t, a, pageState{Tab: "log"}, "log", map[string]string{"log": "all"})
	contains(t, texts(res.View), "row 很长的自动化")

	v, _ := renderV(t, a, 6, nil)
	tl := tilesOf(v, "quiet") // nothing on in it
	if i := slices.IndexFunc(tl, func(x wire.Tile) bool { return x.Title == "很长的区域" }); i < 0 || tl[i].Action.ID != "area" || tl[i].Action.Values["area"] != area {
		t.Fatalf("the long area's tile: %+v", tl)
	}
	_, st = actV(t, a, 6, pageState{}, "area", map[string]string{"area": area})
	if st.Area != area {
		t.Fatalf("the long area did not open: %+v", st)
	}
	renderV(t, a, 5, nil)
	renderV(t, a, 5, st)
	act(t, a, st, "open:light.bed", nil) // its entity's crumb back to the area
}

// More areas than a page lists: 60 tiles or rows, and a footer saying so.
func TestHomeManyAreas(t *testing.T) {
	f := newFakeHA(t)
	var areas [][3]any
	for i := range 70 {
		areas = append(areas, [3]any{fmt.Sprintf("a%02d", i), fmt.Sprintf("区域 %02d", i), []string{}})
	}
	f.areas = areasJSON(areas)
	a := newTestApp(t, f, Config{})
	for _, ver := range []int{6, 5} {
		_, got := renderV(t, a, ver, nil)
		contains(t, got, "## 区域 · 70 | 只列前 60 个")
		if n := strings.Count(got, "→area:"); n != 60 {
			t.Errorf("UI version %d: %d areas listed", ver, n)
		}
	}
}
