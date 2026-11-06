package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page (ui contribution "home", a nav_page), three tabs:
//   - 家: the areas, each with what is on in it by name; an area's devices, one row per device (its main entity; its
//     settings, diagnostics and accessory switches folded into the device), a light or a switch turned on and off by
//     tapping its row; a search across every entity; an entity opened with its controls, the rest of its device and
//     recent history, its attributes folded.
//   - 动态: the logbook of the last two hours, newest first.
//   - 自动化: the automations, scripts and scenes, run and switched from their rows (each asks first).
//
// A tap here is the user's own: it calls the service at once (locks, alarms, automations, scripts and scenes ask
// first), unlike the assistant's actions, which the hub's gate confirms. Statistics, energy, watches and cameras
// stay with the assistant.

type pageState struct {
	Tab     string `json:"t,omitempty"` // "" (家) | log | auto
	Area    string `json:"a,omitempty"` // area id
	Q       string `json:"q,omitempty"`
	Entity  string `json:"e,omitempty"`
	Kind    string `json:"k,omitempty"`  // devices (default) | sensors | all
	Offline bool   `json:"o,omitempty"`  // show unavailable entities too
	Pick    bool   `json:"p,omitempty"`  // a device's row opens it instead of turning it on or off
	Sub     bool   `json:"s,omitempty"`  // the rest of the open entity's device, unfolded
	Attrs   bool   `json:"at,omitempty"` // the open entity's attributes, unfolded
}

const (
	refreshList   = 30
	refreshEntity = 15
	rowsMost      = 250 // a view takes 300 items
	searchMost    = 100
	logHours      = 2
	logRows       = 100
)

var (
	kinds = []wire.Choice{{Value: "devices", Title: "设备"}, {Value: "sensors", Title: "传感器"}, {Value: "all", Title: "全部"}}
	tabs  = []wire.Choice{{Value: "home", Title: "家"}, {Value: "log", Title: "动态"}, {Value: "auto", Title: "自动化"}}
)

// quickDomains are turned on and off by tapping their row.
var quickDomains = []string{"light", "switch", "fan", "input_boolean", "humidifier"}

// rowServices are what a row (not an open entity's buttons) may call, by domain: do:<entity>|<service>.
var rowServices = map[string][]string{
	"light": {"turn_on", "turn_off"}, "switch": {"turn_on", "turn_off"}, "fan": {"turn_on", "turn_off"},
	"input_boolean": {"turn_on", "turn_off"}, "humidifier": {"turn_on", "turn_off"},
	"script": {"turn_on", "turn_off"}, "scene": {"turn_on"},
}

func (st *pageState) clean() {
	if !slices.ContainsFunc(kinds, func(c wire.Choice) bool { return c.Value == st.Kind }) {
		st.Kind = "devices"
	}
	if st.Tab != "log" && st.Tab != "auto" {
		st.Tab = ""
	}
	st.Q = clip(strings.TrimSpace(st.Q), 40)
	if !entityRE.MatchString(st.Entity) {
		st.Entity = ""
	}
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	var st pageState
	_ = r.DecodeState(&st)
	st.clean()
	return a.page(ctx, r, st, statesFor), nil
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var st pageState
	_ = r.DecodeState(&st)
	st.clean()
	fresh, toast := statesFor, ""
	kind, arg, _ := strings.Cut(r.Action, ":")
	switch kind {
	case "tab":
		if arg == "" {
			arg = r.Values["tab"]
		}
		st = pageState{Tab: arg, Kind: st.Kind}
		st.clean()
	case "area":
		st = pageState{Area: arg, Kind: st.Kind}
	case "filter": // the area filter next to the search box
		st.Area, st.Entity = r.Values["area"], ""
	case "search":
		q, ok := r.Values["q"]
		if !ok {
			q = r.Values["query"] // an app before UI version 3: a field and a button
		}
		st.Q, st.Entity = clip(strings.TrimSpace(q), 40), ""
	case "clear":
		st.Q = ""
	case "kind":
		if arg == "" {
			arg = r.Values["kind"]
		}
		st.Kind = arg
		st.clean()
	case "offline":
		st.Offline = !st.Offline
	case "pick":
		st.Pick = !st.Pick
	case "sub":
		st.Sub = !st.Sub
	case "attrs":
		st.Attrs = !st.Attrs
	case "open":
		if entityRE.MatchString(arg) {
			st.Entity, st.Sub, st.Attrs = arg, false, false
		}
	case "back":
		if st.Entity != "" {
			st.Entity, st.Sub, st.Attrs = "", false, false
		} else {
			st.Area = ""
		}
	case "home":
		st = pageState{Kind: st.Kind}
	case "refresh":
		fresh, toast = 0, "已刷新"
		if h, _, _ := a.client(); h != nil {
			h.tryAgain()
		}
	case "do": // a row's own call: a light or a switch on or off, a script or a scene run
		eid, svc, _ := strings.Cut(arg, "|")
		msg, err := a.rowCall(ctx, eid, svc)
		if err != nil {
			toast = err.Error()
		} else {
			toast = msg
		}
		fresh = 0
	case "svc", "opt", "num", "hvac":
		msg, err := a.tap(ctx, st.Entity, kind, arg, r.Values)
		if err != nil {
			toast = err.Error()
		} else {
			toast = msg
		}
		fresh = 0
	}
	return &wire.ActionResult{View: a.page(ctx, r, st, fresh), Toast: toast}, nil
}

// rowCall runs a row's call on eid: only rowServices, only an entity the page shows.
func (a *app) rowCall(ctx context.Context, eid, svc string) (string, error) {
	d, _, _ := strings.Cut(eid, ".")
	if !entityRE.MatchString(eid) || !slices.Contains(rowServices[d], svc) {
		return "", fmt.Errorf("不认识的操作")
	}
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	if _, ok := hm.ByID[eid]; !ok || !hm.visible(eid) {
		return "", errNoEntity
	}
	return a.tap(ctx, eid, "svc", svc, nil)
}

// tap runs a control of an entity: svc (a button: service|data), opt (a select's option), num (a number's value),
// hvac (a climate's mode).
func (a *app) tap(ctx context.Context, eid, kind, arg string, values map[string]string) (string, error) {
	if eid == "" {
		return "", fmt.Errorf("先打开一个设备")
	}
	d, _, _ := strings.Cut(eid, ".")
	if err := a.canWrite(d, slices.Contains(secureDomains, d)); err != nil {
		return "", err
	}
	var service string
	data := map[string]any{}
	switch kind {
	case "svc":
		svc, raw, _ := strings.Cut(arg, "|")
		if !serviceRE.MatchString(svc) {
			return "", fmt.Errorf("不认识的操作")
		}
		service = svc
		if raw != "" && json.Unmarshal([]byte(raw), &data) != nil {
			return "", fmt.Errorf("不认识的操作")
		}
	case "opt":
		service, data["option"] = "select_option", values["option"]
		if values["option"] == "" {
			return "", fmt.Errorf("先选一个")
		}
	case "num":
		v, err := strconv.ParseFloat(strings.TrimSpace(values["value"]), 64)
		if err != nil {
			return "", fmt.Errorf("填一个数")
		}
		service, data["value"] = "set_value", v
	case "hvac":
		mode := arg
		if mode == "" {
			mode = values["hvac"]
		}
		if !serviceRE.MatchString(mode) {
			return "", fmt.Errorf("先选一个模式")
		}
		service, data["hvac_mode"] = "set_hvac_mode", mode
	}
	for _, k := range []string{"entity_id", "device_id", "area_id"} {
		delete(data, k)
	}
	text, err := a.run(ctx, call{Domain: d, Service: service, Entities: []string{eid}, Data: data})
	if err != nil {
		return "", err
	}
	_, now, _ := strings.Cut(text, "现在：")
	return "好了：" + clip(now, 80), nil
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState, fresh time.Duration) *wire.View {
	v := &wire.View{Title: "家", RefreshAfter: refreshList}
	hm, err := a.home(ctx, fresh)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{
			{Type: "text", Text: err.Error(), Style: "secondary"},
			{Type: "button", Title: "重试", Action: &wire.Action{ID: "refresh"}}}}}
		v.State = ext.EncodeState(st)
		return v
	}
	if st.Area != "" && hm.areaByID(st.Area) == nil {
		st.Area = ""
	}
	reg := a.pageRegistry(ctx)
	if st.Entity == "" {
		tabBar(r, st, v)
	}
	if r.UIVersion >= 3 && st.Entity == "" && st.Tab == "" {
		v.Search = &wire.Search{Placeholder: "搜设备、传感器：客厅灯、空调、温度", Value: st.Q, Action: wire.Action{ID: "search"}}
		if len(hm.Areas) >= 1 && len(hm.Areas) <= 19 {
			f := &wire.Filter{Name: "area", Title: "区域", Value: st.Area, Action: wire.Action{ID: "filter"},
				Choices: []wire.Choice{{Value: "", Title: "全部区域"}}}
			if hm.ByType {
				f.Title, f.Choices[0].Title = "类型", "全部类型"
			}
			for _, ar := range hm.Areas {
				f.Choices = append(f.Choices, wire.Choice{Value: ar.ID, Title: ar.Name})
			}
			v.Filter = f
		}
	}
	switch {
	case st.Entity != "":
		a.entityPage(ctx, r, hm, reg, &st, v)
	case st.Tab == "log":
		a.logPage(ctx, r, hm, v)
	case st.Tab == "auto":
		a.autoPage(r, hm, v)
	case st.Q != "":
		a.searchPage(r, hm, st, v)
	case st.Area == "":
		overviewPage(hm, reg, v)
	default:
		a.areaPage(r, hm, reg, st, v)
	}
	v.State = ext.EncodeState(st)
	return v
}

// tabBar is 家 / 动态 / 自动化: the view's tabs (UI version 4), a segmented or rows on top before.
func tabBar(r *ext.UIRequest, st pageState, v *wire.View) {
	cur := st.Tab
	if cur == "" {
		cur = "home"
	}
	switch {
	case r.UIVersion >= 4:
		t := &wire.Tabs{Name: "tab", Value: cur, Action: wire.Action{ID: "tab"}}
		for _, c := range tabs {
			t.Items = append(t.Items, wire.Tab{Value: c.Value, Title: c.Title})
		}
		v.Tabs = t
	case r.UIVersion >= 2:
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "segmented", Name: "tab", Value: cur,
			Choices: tabs, Action: &wire.Action{ID: "tab"}}}})
	default:
		var rows []wire.Component
		for _, c := range tabs {
			row := wire.Component{Type: "row", Title: c.Title, Action: &wire.Action{ID: "tab:" + c.Value}}
			if c.Value == cur {
				row.Badge = &wire.Badge{Text: "正在看", Tone: "positive"}
			}
			rows = append(rows, row)
		}
		v.Sections = append(v.Sections, wire.Section{Items: rows})
	}
}

// crumbs is the way back: a breadcrumb (UI version 4), a 返回 button on top before.
func crumbs(r *ext.UIRequest, path []wire.Crumb, back string) wire.Section {
	if r.UIVersion >= 4 {
		return wire.Section{Items: []wire.Component{{Type: "breadcrumb", Crumbs: path}}}
	}
	return wire.Section{Items: []wire.Component{{Type: "button", Title: "返回", Action: &wire.Action{ID: back}}}}
}

func (hm *home) areaByID(id string) *Area {
	for i := range hm.Areas {
		if hm.Areas[i].ID == id {
			return &hm.Areas[i]
		}
	}
	return nil
}

// visible: the entity is not only in a hidden area.
func (hm *home) visible(e string) bool { return !hm.Hidden[e] || hm.AreaOf[e] != nil }

// shortName is an entity's name without its area's in front ("客厅吸顶灯" in 客厅: "吸顶灯"), when two characters or
// more are left.
func shortName(hm *home, s State) string {
	n := s.Name()
	if area := hm.areaName(s.EntityID); area != "" && !hm.ByType && strings.HasPrefix(n, area) {
		if rest := strings.TrimSpace(strings.TrimPrefix(n, area)); utf8.RuneCountInString(rest) >= 2 {
			return rest
		}
	}
	return n
}

// devRow is a row of an area's devices: a device's main entity, and how many of the device's entities in the area are
// folded into it (its settings, diagnostics, accessory switches, sensors), on its first row.
type devRow struct {
	s      State
	folded int
}

// deviceRows are an area's devices: its entities gathered by device (the entity registry's device_id; without the
// registry each entity is its own), each device's main entities (deviceDomains, not a setting by reg.skip) a row.
// A device without one (a sensor, a gateway) has no row.
func deviceRows(hm *home, reg *Registry, ar *Area) []devRow {
	var order []string
	groups := map[string][]State{}
	for _, e := range ar.Entities {
		s, ok := hm.ByID[e]
		if !ok {
			continue
		}
		key := "e:" + e
		if reg != nil {
			if re, ok := reg.Entities[e]; ok && re.DeviceID != "" {
				key = re.DeviceID
			}
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], s)
	}
	var rows []devRow
	for _, k := range order {
		var mains []State
		folded := 0
		for _, s := range groups[k] {
			if slices.Contains(deviceDomains, s.Domain()) && reg.skip(s.EntityID, hm.ByID) == "" {
				mains = append(mains, s)
			} else {
				folded++
			}
		}
		for i, s := range mains {
			r := devRow{s: s}
			if i == 0 {
				r.folded = folded
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// areaOn names what is on in an area: its devices (not their settings), a climate with its mode.
func areaOn(hm *home, reg *Registry, ar *Area) []string {
	var names []string
	for _, r := range deviceRows(hm, reg, ar) {
		d := r.s.Domain()
		if d == "scene" || d == "script" || r.s.Unavailable() || stateTone(r.s) != "positive" {
			continue
		}
		n := shortName(hm, r.s)
		if d == "climate" {
			n += "（" + stateText(r.s) + "）"
		}
		names = append(names, n)
	}
	return names
}

// onLine is an area's row on the overview: what is on by name, then its temperature and humidity.
func onLine(on []string, sum areaSummary) string {
	var parts []string
	switch {
	case len(on) == 0:
		parts = append(parts, "都关着")
	case len(on) <= 3:
		parts = append(parts, strings.Join(on, "、")+" 开着")
	default:
		parts = append(parts, fmt.Sprintf("%s 等 %d 个开着", strings.Join(on[:3], "、"), len(on)))
	}
	if len(sum.temp) > 0 {
		parts = append(parts, tidy(avg(sum.temp))+"°")
	}
	if len(sum.humid) > 0 {
		parts = append(parts, "湿度 "+tidy(avg(sum.humid))+"%")
	}
	return strings.Join(parts, " · ")
}

func overviewPage(hm *home, reg *Registry, v *wire.View) {
	var alarms []wire.Component
	for _, s := range hm.States {
		if stateTone(s) == "danger" && !hm.Hidden[s.EntityID] {
			alarms = append(alarms, entityRow(hm, s, true))
		}
	}
	if len(alarms) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: "报警", Items: alarms})
	}
	if hm.Note != "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: hm.Note, Style: "secondary"}}})
	}
	sec := wire.Section{Title: fmt.Sprintf("区域 · %d", len(hm.Areas))}
	if hm.ByType {
		sec.Title = fmt.Sprintf("类型 · %d", len(hm.Areas))
	}
	for i := range hm.Areas {
		ar := &hm.Areas[i]
		on := areaOn(hm, reg, ar)
		row := wire.Component{Type: "row", Key: "area:" + ar.ID, Title: ar.Name, Subtitle: clip(onLine(on, summarizeArea(hm, *ar)), 120),
			Action: &wire.Action{ID: "area:" + ar.ID}}
		if len(on) > 0 {
			row.Badge = &wire.Badge{Text: fmt.Sprintf("%d 开着", len(on)), Tone: "positive"}
		}
		sec.Items = append(sec.Items, row)
	}
	if len(hm.Areas) == 0 {
		sec.Items = append(sec.Items, wire.Component{Type: "text", Text: "Home Assistant 里还没有区域：用上面的搜索找设备", Style: "secondary"})
	}
	v.Sections = append(v.Sections, sec, footer())
}

func footer() wire.Section {
	return wire.Section{Items: []wire.Component{
		{Type: "text", Text: "Home Assistant · 读于 " + time.Now().Format("15:04:05"), Style: "caption"},
		{Type: "button", Title: "刷新", Action: &wire.Action{ID: "refresh"}}}}
}

func inKind(kind string, s State) bool {
	switch kind {
	case "devices":
		return slices.Contains(deviceDomains, s.Domain())
	case "sensors":
		return slices.Contains(sensorDomains, s.Domain())
	}
	return true
}

func (a *app) areaPage(r *ext.UIRequest, hm *home, reg *Registry, st pageState, v *wire.View) {
	ar := hm.areaByID(st.Area)
	v.Title = ar.Name
	v.Sections = append(v.Sections, crumbs(r, []wire.Crumb{{Title: "家", Action: &wire.Action{ID: "home"}}, {Title: ar.Name}}, "home"))
	if r.UIVersion >= 2 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "segmented", Name: "kind", Value: st.Kind,
			Choices: kinds, Action: &wire.Action{ID: "kind"}}}})
	} else {
		var rows []wire.Component
		for _, k := range kinds {
			c := wire.Component{Type: "row", Title: k.Title, Action: &wire.Action{ID: "kind:" + k.Value}}
			if k.Value == st.Kind {
				c.Badge = &wire.Badge{Text: "正在看", Tone: "positive"}
			}
			rows = append(rows, c)
		}
		v.Sections = append(v.Sections, wire.Section{Items: rows})
	}
	if st.Kind == "devices" {
		a.devicesList(hm, reg, ar, st, v)
		v.Sections = append(v.Sections, footer())
		return
	}
	var shown []State
	offline, diag := 0, 0
	for _, e := range ar.Entities {
		s, ok := hm.ByID[e]
		if !ok || !inKind(st.Kind, s) {
			continue
		}
		if st.Kind == "sensors" && reg != nil && reg.Entities[e].EntityCategory == "diagnostic" { // signal, IP, uptime
			diag++
			continue
		}
		if s.Unavailable() && !st.Offline {
			offline++
			continue
		}
		shown = append(shown, s)
	}
	sort.SliceStable(shown, func(i, j int) bool { // by domain in deviceDomains' order, then name
		di, dj := domainRank(shown[i].Domain()), domainRank(shown[j].Domain())
		if di != dj {
			return di < dj
		}
		return shown[i].Name() < shown[j].Name()
	})
	sec := wire.Section{Title: fmt.Sprintf("%s · %d", kindTitle(st.Kind), len(shown))}
	for i, s := range shown {
		if i == rowsMost {
			sec.Footer = fmt.Sprintf("只列前 %d 个，用搜索找别的", rowsMost)
			break
		}
		sec.Items = append(sec.Items, entityRow(hm, s, false))
	}
	if len(shown) == 0 {
		sec.Items = append(sec.Items, wire.Component{Type: "text", Text: "这里没有" + kindTitle(st.Kind), Style: "secondary"})
	}
	if diag > 0 && sec.Footer == "" {
		sec.Footer = fmt.Sprintf("另有 %d 个诊断项（信号、IP 之类）在「全部」里", diag)
	}
	sec.Items = append(sec.Items, offlineRow(offline, st.Offline)...)
	v.Sections = append(v.Sections, sec, footer())
}

func offlineRow(offline int, showing bool) []wire.Component {
	switch {
	case offline > 0:
		return []wire.Component{{Type: "row", Title: fmt.Sprintf("还有 %d 个离线的", offline), Subtitle: "点一下也列出来", Action: &wire.Action{ID: "offline"}}}
	case showing:
		return []wire.Component{{Type: "row", Title: "不列离线的", Action: &wire.Action{ID: "offline"}}}
	}
	return nil
}

// devicesList is an area's devices, a section per kind (灯, 开关, 空调…): a light's or a switch's row turns it on or
// off (st.Pick: opens it), the rest open.
func (a *app) devicesList(hm *home, reg *Registry, ar *Area, st pageState, v *wire.View) {
	var shown []devRow
	offline := 0
	for _, r := range deviceRows(hm, reg, ar) {
		if r.s.Unavailable() && !st.Offline {
			offline++
			continue
		}
		shown = append(shown, r)
	}
	sort.SliceStable(shown, func(i, j int) bool {
		di, dj := domainRank(shown[i].s.Domain()), domainRank(shown[j].s.Domain())
		if di != dj {
			return di < dj
		}
		return shortName(hm, shown[i].s) < shortName(hm, shown[j].s)
	})
	quick, n := false, 0
	for i := 0; i < len(shown); {
		d := shown[i].s.Domain()
		j := i
		for j < len(shown) && shown[j].s.Domain() == d {
			j++
		}
		sec := wire.Section{Title: fmt.Sprintf("%s · %d", domainName(d), j-i)}
		for _, r := range shown[i:j] {
			if n == rowsMost {
				sec.Footer = fmt.Sprintf("只列前 %d 个，用搜索找别的", rowsMost)
				break
			}
			row, q := a.deviceRow(hm, r, st.Pick)
			quick = quick || q
			sec.Items = append(sec.Items, row)
			n++
		}
		if len(sec.Items) > 0 {
			v.Sections = append(v.Sections, sec)
		}
		i = j
	}
	if len(shown) == 0 && offline == 0 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: "这里没有设备：看看「传感器」「全部」", Style: "secondary"}}})
	}
	more := offlineRow(offline, st.Offline)
	switch {
	case st.Pick:
		more = append(more, wire.Component{Type: "row", Title: "改回点一下就开关", Subtitle: "现在点设备是打开它", Action: &wire.Action{ID: "pick"}})
	case quick:
		more = append(more, wire.Component{Type: "row", Title: "调亮度、看设备的设置", Subtitle: "点这里，再点设备就是打开它", Action: &wire.Action{ID: "pick"}})
	}
	if len(more) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: more})
	}
}

// deviceRow is a device's row: its name, what matters (亮度, 室温 and 设定, 开度) and how many settings it holds, its
// state. A light, switch, fan… that can be written turns on or off when tapped (true), unless open (st.Pick).
func (a *app) deviceRow(hm *home, r devRow, open bool) (wire.Component, bool) {
	s := r.s
	var sub []string
	if d := detail(s); d != "" {
		sub = append(sub, d)
	}
	if r.folded > 0 {
		sub = append(sub, fmt.Sprintf("另有 %d 项", r.folded))
	}
	row := wire.Component{Type: "row", Key: s.EntityID, Title: clip(shortName(hm, s), 80), Subtitle: strings.Join(sub, " · "),
		Action: &wire.Action{ID: "open:" + s.EntityID}}
	if text := stateText(s); text != "" {
		row.Badge = &wire.Badge{Text: clip(text, 12), Tone: stateTone(s)}
	}
	d := s.Domain()
	if !slices.Contains(quickDomains, d) || (s.State != "on" && s.State != "off") || a.canWrite(d, false) != nil {
		return row, false
	}
	if !open {
		svc := "turn_on"
		if s.State == "on" {
			svc = "turn_off"
		}
		row.Action.ID = "do:" + s.EntityID + "|" + svc
	}
	return row, true
}

func kindTitle(k string) string {
	for _, c := range kinds {
		if c.Value == k {
			return c.Title
		}
	}
	return k
}

func domainRank(d string) int {
	if i := slices.Index(deviceDomains, d); i >= 0 {
		return i
	}
	if i := slices.Index(sensorDomains, d); i >= 0 {
		return len(deviceDomains) + i
	}
	return 1000
}

// entityRow is an entity in a list: its name, what it is and the detail, its state (a badge for devices, the value
// for the rest).
func entityRow(hm *home, s State, withArea bool) wire.Component {
	sub := []string{domainName(s.Domain())}
	if d := detail(s); d != "" {
		sub = append(sub, d)
	}
	if withArea {
		if n := hm.areaName(s.EntityID); n != "" {
			sub = append(sub, n)
		}
	}
	row := wire.Component{Type: "row", Key: s.EntityID, Title: clip(s.Name(), 80), Subtitle: strings.Join(sub, " · "),
		Action: &wire.Action{ID: "open:" + s.EntityID}}
	text := stateText(s)
	switch {
	case text == "":
	case slices.Contains(deviceDomains, s.Domain()) || s.Domain() == "binary_sensor" || s.Unavailable():
		row.Badge = &wire.Badge{Text: clip(text, 12), Tone: stateTone(s)}
	default:
		row.Value = clip(text, 24)
	}
	return row
}

func (a *app) searchPage(r *ext.UIRequest, hm *home, st pageState, v *wire.View) {
	found := hm.match(st.Q, hm.areaByID(st.Area), "", st.Offline)
	sec := wire.Section{Title: fmt.Sprintf("搜索「%s」· %d", st.Q, len(found))}
	for i, s := range found {
		if i == searchMost {
			sec.Footer = fmt.Sprintf("只列前 %d 个，写得具体些", searchMost)
			break
		}
		sec.Items = append(sec.Items, entityRow(hm, s, true))
	}
	if len(found) == 0 {
		sec.Items = append(sec.Items, wire.Component{Type: "text", Text: "没找到对得上的", Style: "secondary"})
	}
	if !st.Offline {
		sec.Items = append(sec.Items, wire.Component{Type: "row", Title: "也搜离线的", Action: &wire.Action{ID: "offline"}})
	}
	sec.Items = append(sec.Items, wire.Component{Type: "button", Title: "返回", Action: &wire.Action{ID: "clear"}})
	if r.UIVersion < 3 {
		v.Sections = append(v.Sections, searchField(st.Q))
	}
	v.Sections = append(v.Sections, sec, footer())
}

func searchField(q string) wire.Section {
	return wire.Section{Items: []wire.Component{
		{Type: "field", Name: "query", Label: "搜索", Kind: "text", Value: q, Placeholder: "客厅灯、空调、温度"},
		{Type: "button", Title: "搜索", Action: &wire.Action{ID: "search", Submit: true}}}}
}

// entityPath is the open entity's way back: where it was opened from (动态, 自动化, a search) or its area.
func entityPath(hm *home, st *pageState, name string) []wire.Crumb {
	back := &wire.Action{ID: "back"}
	switch {
	case st.Tab == "log":
		return []wire.Crumb{{Title: "动态", Action: back}, {Title: name}}
	case st.Tab == "auto":
		return []wire.Crumb{{Title: "自动化", Action: back}, {Title: name}}
	case st.Q != "":
		return []wire.Crumb{{Title: "家", Action: &wire.Action{ID: "home"}}, {Title: "搜索「" + st.Q + "」", Action: back}, {Title: name}}
	}
	path := []wire.Crumb{{Title: "家", Action: &wire.Action{ID: "home"}}}
	if ar := hm.AreaOf[st.Entity]; ar != nil {
		act := &wire.Action{ID: "area:" + ar.ID}
		if st.Area == ar.ID {
			act = back // keeps the area's tab, offline rows and pick
		}
		path = append(path, wire.Crumb{Title: ar.Name, Action: act})
	}
	return append(path, wire.Crumb{Title: name})
}

func (a *app) entityPage(ctx context.Context, r *ext.UIRequest, hm *home, reg *Registry, st *pageState, v *wire.View) {
	v.RefreshAfter = refreshEntity
	s, ok := hm.ByID[st.Entity]
	if !ok || !hm.visible(st.Entity) {
		v.Sections = []wire.Section{crumbs(r, entityPath(hm, st, st.Entity), "back"),
			{Items: []wire.Component{{Type: "text", Text: errNoEntity.Error(), Style: "secondary"}}}}
		return
	}
	v.Title = clip(s.Name(), 60)
	v.Sections = append(v.Sections, crumbs(r, entityPath(hm, st, clip(shortName(hm, s), 40)), "back"))

	// the state
	head := wire.Section{}
	facts := []string{domainName(s.Domain())}
	if n := hm.areaName(s.EntityID); n != "" {
		facts = append(facts, n)
	}
	facts = append(facts, "变于 "+s.LastChanged.Local().Format("01-02 15:04"))
	head.Items = append(head.Items,
		wire.Component{Type: "metric", Label: "状态", Value: orDash(stateText(s)), Detail: detail(s), Tone: stateTone(s)},
		wire.Component{Type: "text", Text: strings.Join(facts, " · ") + "\n" + s.EntityID, Style: "caption"})
	v.Sections = append(v.Sections, head)

	// the controls
	if ctl := a.controlSection(r, s); len(ctl.Items) > 0 {
		v.Sections = append(v.Sections, ctl)
	}

	// the rest of its device, folded
	if sec, ok := deviceSection(hm, reg, s, st.Sub); ok {
		v.Sections = append(v.Sections, sec)
	}

	// the history
	if hist := a.historySection(ctx, s); len(hist.Items) > 0 {
		v.Sections = append(v.Sections, hist)
	}

	// the attributes, folded
	var keys []string
	for k := range s.Attributes {
		if !slices.Contains(noisyAttrs, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return
	}
	if !st.Attrs {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "row", Title: fmt.Sprintf("属性 · %d", len(keys)),
			Subtitle: clip(strings.Join(keys, "、"), 60), Action: &wire.Action{ID: "attrs"}}}})
		return
	}
	attrs := wire.Section{Title: fmt.Sprintf("属性 · %d", len(keys))}
	if r.UIVersion >= 3 {
		t := wire.Component{Type: "table", Columns: []wire.TableColumn{{Title: "名称"}, {Title: "值"}}}
		for _, k := range keys {
			if len(t.Rows) == 100 {
				break
			}
			t.Rows = append(t.Rows, []string{k, clip(attrText(s.Attributes[k]), 300)})
		}
		attrs.Items = append(attrs.Items, t)
	} else {
		var lines []string
		for _, k := range keys {
			lines = append(lines, k+"："+clip(attrText(s.Attributes[k]), 120))
		}
		attrs.Items = append(attrs.Items, wire.Component{Type: "text", Text: clip(strings.Join(lines, "\n"), 1900), Style: "secondary"})
	}
	attrs.Items = append(attrs.Items, wire.Component{Type: "row", Title: "收起属性", Action: &wire.Action{ID: "attrs"}})
	v.Sections = append(v.Sections, attrs)
}

// deviceSection is the rest of s's device (its settings, diagnostics, sensors): one row saying how many, or (open)
// a row each. false: s has no device, or nothing else on it.
func deviceSection(hm *home, reg *Registry, s State, open bool) (wire.Section, bool) {
	if reg == nil {
		return wire.Section{}, false
	}
	re, ok := reg.Entities[s.EntityID]
	if !ok || re.DeviceID == "" {
		return wire.Section{}, false
	}
	var sibs []State
	for _, e := range reg.ByDevice[re.DeviceID] {
		if x, ok := hm.ByID[e]; ok && e != s.EntityID && hm.visible(e) {
			sibs = append(sibs, x)
		}
	}
	if len(sibs) == 0 {
		return wire.Section{}, false
	}
	sort.SliceStable(sibs, func(i, j int) bool { return domainRank(sibs[i].Domain()) < domainRank(sibs[j].Domain()) })
	dev := reg.Devices[re.DeviceID].title()
	if dev == "" {
		dev = "这台设备"
	} else {
		dev = "「" + clip(dev, 30) + "」"
	}
	if !open {
		var names []string
		for _, x := range sibs[:min(3, len(sibs))] {
			names = append(names, shortName(hm, x))
		}
		return wire.Section{Items: []wire.Component{{Type: "row", Title: fmt.Sprintf("%s的设置和其他项 · %d", dev, len(sibs)),
			Subtitle: clip(strings.Join(names, "、"), 80), Action: &wire.Action{ID: "sub"}}}}, true
	}
	sec := wire.Section{Title: fmt.Sprintf("%s的设置和其他项 · %d", dev, len(sibs))}
	for i, x := range sibs {
		if i == 100 {
			break
		}
		row := entityRow(hm, x, false)
		switch reg.Entities[x.EntityID].EntityCategory {
		case "config":
			row.Subtitle = "设置 · " + row.Subtitle
		case "diagnostic":
			row.Subtitle = "诊断 · " + row.Subtitle
		}
		sec.Items = append(sec.Items, row)
	}
	sec.Items = append(sec.Items, wire.Component{Type: "row", Title: "收起", Action: &wire.Action{ID: "sub"}})
	return sec, true
}

// logPage is 动态: the logbook of the last logHours, newest first; an entity that changed more than noisyMost times
// is one row.
func (a *app) logPage(ctx context.Context, r *ext.UIRequest, hm *home, v *wire.View) {
	v.Title = "动态"
	h, _, _ := a.client()
	to := time.Now()
	entries, err := h.Logbook(ctx, to.Add(-logHours*time.Hour), to, "")
	if err != nil {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: "读不到日志：" + err.Error(), Style: "secondary"}}}, footer())
		return
	}
	kept, count := keepLog(hm, entries, nil)
	loc := a.loc()
	sec := wire.Section{Title: fmt.Sprintf("最近 %d 小时", logHours)}
	shown := map[string]bool{}
	for i := len(kept) - 1; i >= 0; i-- {
		e := kept[i]
		if len(sec.Items) == logRows {
			sec.Footer = fmt.Sprintf("只列最近 %d 条；更早的、某个区域的，问助手", logRows)
			break
		}
		what, where, by := logParts(hm, e)
		sub := []string{e.When.In(loc).Format("15:04")}
		if n := count[e.EntityID]; e.EntityID != "" && n > noisyMost {
			if shown[e.EntityID] {
				continue
			}
			shown[e.EntityID] = true
			what = fmt.Sprintf("%s 变化了 %d 次", logName(hm, e), n)
			by = "最后是" + orDash(entryState(hm, e))
		}
		for _, p := range []string{where, by} {
			if p != "" {
				sub = append(sub, p)
			}
		}
		row := wire.Component{Type: "row", Title: clip(what, 80), Subtitle: clip(strings.Join(sub, " · "), 120)}
		if _, ok := hm.ByID[e.EntityID]; ok {
			row.Action = &wire.Action{ID: "open:" + e.EntityID}
		}
		sec.Items = append(sec.Items, row)
	}
	if len(sec.Items) == 0 {
		sec.Items = append(sec.Items, emptyItem(r, fmt.Sprintf("最近 %d 小时家里没有变化", logHours), ""))
	}
	v.Sections = append(v.Sections, sec, footer())
}

// autoPage is 自动化: the automations (opened: 启用 / 停用, 触发一次), scripts and scenes (run from the row), each
// asking first.
func (a *app) autoPage(r *ext.UIRequest, hm *home, v *wire.View) {
	v.Title = "自动化"
	loc := a.loc()
	n := 0
	for _, d := range []string{"automation", "script", "scene"} {
		var items []wire.Component
		for _, s := range hm.States {
			if s.Domain() != d || !hm.visible(s.EntityID) {
				continue
			}
			if n == rowsMost {
				break
			}
			items = append(items, a.autoRow(s, loc))
			n++
		}
		if len(items) == 0 {
			continue
		}
		sec := wire.Section{Title: fmt.Sprintf("%s · %d", domainName(d), len(items)), Items: items}
		switch d {
		case "automation":
			sec.Footer = "点开可以停用、启用或手动触发一次"
		case "script", "scene":
			sec.Footer = "点一下就运行，会先问一句"
		}
		v.Sections = append(v.Sections, sec)
	}
	if n == 0 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{emptyItem(r, "还没有自动化、脚本和场景",
			"在 Home Assistant 里建，或者让助手帮你写一个自动化")}})
	}
	v.Sections = append(v.Sections, footer())
}

func (a *app) autoRow(s State, loc *time.Location) wire.Component {
	row := wire.Component{Type: "row", Key: s.EntityID, Title: clip(s.Name(), 80), Action: &wire.Action{ID: "open:" + s.EntityID}}
	last := s.attr("last_triggered")
	if s.Domain() == "scene" {
		last = s.State // a scene's state is when it was last run
	}
	if at, err := time.Parse(time.RFC3339Nano, last); err == nil {
		row.Subtitle = "上次 " + at.In(loc).Format("01-02 15:04")
	}
	switch {
	case s.Unavailable():
		row.Badge = &wire.Badge{Text: "离线", Tone: "caution"}
		return row
	case s.Domain() == "automation" && s.State == "on":
		row.Badge = &wire.Badge{Text: "开着", Tone: "positive"}
	case s.Domain() == "automation":
		row.Badge = &wire.Badge{Text: "停用了", Tone: "neutral"}
	case s.Domain() == "script" && s.State == "on":
		row.Badge = &wire.Badge{Text: "运行中", Tone: "positive"}
	}
	if s.Domain() == "automation" || a.canWrite(s.Domain(), false) != nil {
		return row
	}
	switch {
	case s.Domain() == "scene":
		row.Action = &wire.Action{ID: "do:" + s.EntityID + "|turn_on", Confirm: "执行场景「" + s.Name() + "」？"}
	case s.State == "on":
		row.Action = &wire.Action{ID: "do:" + s.EntityID + "|turn_off", Confirm: "停下脚本「" + s.Name() + "」？"}
	default:
		row.Action = &wire.Action{ID: "do:" + s.EntityID + "|turn_on", Confirm: "运行脚本「" + s.Name() + "」？"}
	}
	return row
}

// emptyItem is an empty state (UI version 3), a text before.
func emptyItem(r *ext.UIRequest, title, detail string) wire.Component {
	if r.UIVersion >= 3 {
		return wire.Component{Type: "empty", Title: title, Detail: detail}
	}
	return wire.Component{Type: "text", Text: strings.TrimSpace(title + "\n" + detail), Style: "secondary"}
}

func attrText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return tidy(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// controlSection is the entity's controls, or why there are none.
func (a *app) controlSection(r *ext.UIRequest, s State) wire.Section {
	sec := wire.Section{Title: "操作"}
	d := s.Domain()
	if err := a.canWrite(d, slices.Contains(secureDomains, d)); err != nil {
		if len(controls(s)) > 0 || options(s) != nil {
			sec.Items = append(sec.Items, wire.Component{Type: "text", Text: err.Error() + "：在扩展设置里加上才能操作", Style: "secondary"})
		}
		return sec
	}
	if s.Unavailable() {
		sec.Items = append(sec.Items, wire.Component{Type: "text", Text: "设备离线，现在操作不了", Style: "secondary"})
		return sec
	}
	secure := slices.Contains(secureDomains, d)
	for _, c := range controls(s) {
		id := "svc:" + c.Service
		if len(c.Data) > 0 {
			b, _ := json.Marshal(c.Data)
			id += "|" + string(b)
		}
		act := &wire.Action{ID: id}
		if secure || c.Role == "destructive" || d == "automation" || d == "script" || d == "scene" {
			act.Confirm = c.Title + "「" + s.Name() + "」？"
		}
		sec.Items = append(sec.Items, wire.Component{Type: "button", Title: c.Title, Role: c.Role, Action: act})
	}
	if modes := hvacModes(s); len(modes) > 0 {
		var choices []wire.Choice
		for _, m := range modes {
			choices = append(choices, wire.Choice{Value: m, Title: orDash(stateNames[m])})
		}
		if r.UIVersion >= 2 && len(choices) >= 2 && len(choices) <= 5 {
			sec.Items = append(sec.Items, wire.Component{Type: "segmented", Name: "hvac", Value: s.State, Choices: choices, Action: &wire.Action{ID: "hvac"}})
		} else if len(choices) >= 2 {
			sec.Items = append(sec.Items, wire.Component{Type: "field", Name: "hvac", Label: "模式", Kind: "choice", Value: s.State, Choices: choices},
				wire.Component{Type: "button", Title: "设成这个模式", Action: &wire.Action{ID: "hvac", Submit: true}})
		}
	}
	if opts := options(s); len(opts) > 0 {
		var choices []wire.Choice
		for _, o := range opts {
			choices = append(choices, wire.Choice{Value: o})
		}
		sec.Items = append(sec.Items, wire.Component{Type: "field", Name: "option", Label: "选项", Kind: "choice", Value: s.State, Choices: choices},
			wire.Component{Type: "button", Title: "设成这个", Action: &wire.Action{ID: "opt", Submit: true}})
	}
	if lo, hi, step, ok := numberRange(s); ok {
		sec.Items = append(sec.Items, wire.Component{Type: "field", Name: "value", Label: fmt.Sprintf("数值（%s–%s，步长 %s）", tidy(lo), tidy(hi), tidy(step)),
			Kind: "number", Value: s.State}, wire.Component{Type: "button", Title: "设成这个数", Action: &wire.Action{ID: "num", Submit: true}})
	}
	return sec
}

// historySection is the last 24 hours: a line for a number, the last changes otherwise.
func (a *app) historySection(ctx context.Context, s State) wire.Section {
	sec := wire.Section{Title: "最近 24 小时"}
	h, _, _ := a.client()
	changes, err := h.History(ctx, s.EntityID, time.Now().Add(-24*time.Hour))
	if err != nil {
		sec.Items = append(sec.Items, wire.Component{Type: "text", Text: "读不到历史：" + err.Error(), Style: "secondary"})
		return sec
	}
	if len(changes) == 0 {
		return sec
	}
	if pts := series(changes, 96); len(pts) >= 3 {
		unit := s.attr("unit_of_measurement")
		for i := range pts {
			pts[i].Display = tidy(pts[i].Value) + unit
		}
		sec.Items = append(sec.Items, wire.Component{Type: "chart", Kind: "line", Unit: unit, Series: []wire.ChartSeries{{Name: s.Name(), Points: pts}}})
		return sec
	}
	t := wire.Component{Type: "table", Columns: []wire.TableColumn{{Title: "时间"}, {Title: "状态"}}}
	shown := changes
	if len(shown) > 12 {
		shown = shown[len(shown)-12:]
	}
	for i := len(shown) - 1; i >= 0; i-- {
		c := shown[i]
		t.Rows = append(t.Rows, []string{c.At.Local().Format("01-02 15:04"), orDash(stateText(State{EntityID: s.EntityID, State: c.State, Attributes: s.Attributes}))})
	}
	sec.Items = append(sec.Items, t)
	return sec
}

// series turns numeric changes into at most n points: the value each change set, averaged into n even buckets over
// the span when there are more.
func series(cs []Change, n int) []wire.ChartPoint {
	type pt struct {
		at time.Time
		v  float64
	}
	var ps []pt
	for _, c := range cs {
		if f, err := strconv.ParseFloat(c.State, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			ps = append(ps, pt{c.At, f})
		}
	}
	if len(ps) < len(cs)/2 || len(ps) == 0 {
		return nil
	}
	label := func(t time.Time) string { return t.Local().Format("15:04") }
	if len(ps) <= n {
		out := make([]wire.ChartPoint, len(ps))
		for i, p := range ps {
			out[i] = wire.ChartPoint{Label: label(p.at), Value: p.v}
		}
		return out
	}
	start, end := ps[0].at, ps[len(ps)-1].at
	span := end.Sub(start) / time.Duration(n)
	if span <= 0 {
		return nil
	}
	var out []wire.ChartPoint
	j := 0
	last := ps[0].v
	for b := range n {
		lo, hi := start.Add(span*time.Duration(b)), start.Add(span*time.Duration(b+1))
		sum, cnt := 0.0, 0
		for j < len(ps) && (ps[j].at.Before(hi) || b == n-1) {
			if !ps[j].at.Before(lo) {
				sum, cnt = sum+ps[j].v, cnt+1
			}
			j++
		}
		if cnt > 0 {
			last = sum / float64(cnt)
		}
		out = append(out, wire.ChartPoint{Label: label(lo), Value: math.Round(last*100) / 100})
	}
	return out
}
