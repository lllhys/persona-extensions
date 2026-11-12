package main

import (
	"context"
	"encoding/json"
	"errors"
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
//   - 家: the overview (home.go): the home's figures, what needs a look, the devices with their switches and dials,
//     and the areas, those with something going on first. An area opens on its figures (temperature, humidity, what
//     is on and offline), its devices (one tile or row each; a device's settings, diagnostics and accessory switches
//     are folded into it), sensors or everything. A light or a switch turns on and off from its tile's switch (UI
//     version 6) or its row; a search finds any entity; an entity opens on its header, its progress bars and
//     controls, the rest of its device, its last 24 hours and its attributes.
//   - 动态: the logbook of the last two hours, a section per day, devices by default (全部 adds sensors and people),
//     clock and diagnostic sensors left out.
//   - 自动化: the automations, scripts and scenes, when each last ran, run and switched from their rows (each asks
//     first).
//
// A tap here is the user's own: it calls the service at once (locks, alarms, automations, scripts and scenes ask
// first), unlike the assistant's actions, which the hub's gate confirms. Statistics, energy, watches and cameras
// stay with the assistant. The presentation is in views.go; this file holds the page's state, actions and data.

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
	LogAll  bool   `json:"la,omitempty"` // 动态 shows every change, not only devices'
	All     bool   `json:"qa,omitempty"` // 家 lists every device, not only those on and the first few
	Rooms   bool   `json:"r,omitempty"`  // 家 lists the quiet areas one by one
	Lost    bool   `json:"lo,omitempty"` // 家 lists the devices offline one by one
}

const (
	refreshList   = 30
	refreshEntity = 15
	rowsMost      = 250 // a view takes 300 items
	searchMost    = 100
	logHours      = 2
	logRows       = 100
	logPageNoisy  = 3 // the page folds an entity after this many entries; the tools keep 8
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
		if arg == "" {
			arg = r.Values["area"] // an area id too long for the action's id (areaAction)
		}
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
	case "more":
		st.All = !st.All
	case "rooms":
		st.Rooms = !st.Rooms
	case "lost":
		st.Lost = !st.Lost
	case "pick":
		if v, ok := r.Values["pick"]; ok {
			st.Pick = v == "open"
		} else {
			st.Pick = !st.Pick
		}
	case "log":
		which := r.Values["log"]
		if arg != "" {
			which = arg
		}
		st.LogAll = which == "all"
	case "sub":
		st.Sub = !st.Sub
	case "attrs":
		st.Attrs = !st.Attrs
	case "open":
		if arg == "" {
			arg = r.Values["entity"] // an entity id too long for the action's id (openAction)
		}
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
		if arg == "" { // an entity id too long for the action's id (rowDo)
			eid, svc = r.Values["entity"], r.Values["service"]
		}
		msg, err := a.rowCall(ctx, eid, svc)
		if err != nil {
			toast = err.Error()
		} else {
			toast = msg
		}
		fresh = 0
	case "ctl": // a switch, a slider or a stepper of the overview or an area (controlOf): the entity and its value
		eid := r.Values["entity"]
		name, _ := controlOf(arg, eid)
		msg, err := a.setControl(ctx, eid, arg, r.Values[name])
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

// setControl runs a control's change on eid: power (a switch: turn_on or turn_off), bri (a light's brightness in
// percent), pct (a fan's speed), temp (a climate's target temperature), pos (a cover's position). Only on an entity the
// page shows, that the control is drawn for (its domain and what it declares it can do), of a domain the settings
// allow, with a value in its range; never a lock or an alarm panel (canWrite: those go through their own page, which
// asks first), nor a garage door, a gate or a door's position.
func (a *app) setControl(ctx context.Context, eid, kind, value string) (string, error) {
	if !entityRE.MatchString(eid) {
		return "", errUnknownOp
	}
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	s, ok := hm.ByID[eid]
	if !ok || !hm.visible(eid) {
		return "", errNoEntity
	}
	d := s.Domain()
	number := func(lo, hi float64) (float64, error) {
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < lo || v > hi {
			return 0, fmt.Errorf("要在 %s 到 %s 之间", tidy(lo), tidy(hi))
		}
		return v, nil
	}
	c := call{Domain: d, Entities: []string{eid}, Data: map[string]any{}}
	switch {
	case kind == "power" && slices.Contains(powerDomains, d) && switchable(s):
		switch value {
		case "true":
			c.Service = "turn_on"
		case "false":
			c.Service = "turn_off"
		default:
			return "", errUnknownOp
		}
	case kind == "bri" && dimmable(s):
		v, err := number(1, 100)
		if err != nil {
			return "", err
		}
		c.Service, c.Data["brightness_pct"] = "turn_on", int(math.Round(v))
	case kind == "pct" && d == "fan" && features(s)&fanSetSpeed != 0:
		v, err := number(0, 100)
		if err != nil {
			return "", err
		}
		c.Service, c.Data["percentage"] = "set_percentage", int(math.Round(v))
	case kind == "temp" && d == "climate":
		t, ok := s.num("temperature") // a single target (not a range, not off): what the stepper is drawn for
		if !ok {
			return "", errUnknownOp
		}
		v, err := number(math.Min(s.attrNum("min_temp", 7), t), math.Max(s.attrNum("max_temp", 35), t))
		if err != nil {
			return "", err
		}
		c.Service, c.Data["temperature"] = "set_temperature", v
	case kind == "pos" && positionable(s):
		v, err := number(0, 100)
		if err != nil {
			return "", err
		}
		c.Service, c.Data["position"] = "set_cover_position", int(math.Round(v))
	default:
		return "", errUnknownOp
	}
	if err := a.canWrite(d, false); err != nil {
		return "", err
	}
	text, err := a.run(ctx, c)
	if err != nil {
		return "", err
	}
	_, now, _ := strings.Cut(text, "现在：")
	return "好了：" + clip(now, 80), nil
}

var errUnknownOp = errors.New("不认识的操作")

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
	// the open entity comes back in the page's state: only one the page would show (not in a hidden area)
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	if _, ok := hm.ByID[eid]; !ok || !hm.visible(eid) {
		return "", errNoEntity
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
		renderTabs(r, st, v, alarmCount(hm, reg))
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
		a.logPage(ctx, r, hm, reg, st, v)
	case st.Tab == "auto":
		a.autoPage(r, hm, v)
	case st.Q != "":
		a.searchPage(r, hm, st, v)
	case st.Area == "":
		a.homePage(r, hm, reg, st, v)
	default:
		a.roomPage(r, hm, reg, st, v)
	}
	v.State = ext.EncodeState(st)
	return v
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

func offlineRow(offline int, showing bool) []wire.Component {
	switch {
	case offline > 0:
		return []wire.Component{{Type: "row", Title: fmt.Sprintf("还有 %d 个离线的", offline), Subtitle: "点一下也列出来", Action: &wire.Action{ID: "offline"}}}
	case showing:
		return []wire.Component{{Type: "row", Title: "不列离线的", Action: &wire.Action{ID: "offline"}}}
	}
	return nil
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
	row := wire.Component{Type: "row", Key: ref("", s.EntityID), Title: clip(shortName(hm, s), 80), Subtitle: strings.Join(sub, " · "),
		Action: openAction(s.EntityID)}
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
		row.Action = rowDo(s.EntityID, svc)
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
	row := wire.Component{Type: "row", Key: ref("", s.EntityID), Title: clip(s.Name(), 80), Subtitle: strings.Join(sub, " · "),
		Action: openAction(s.EntityID)}
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
		act := areaAction(ar.ID)
		if st.Area == ar.ID {
			act = back // keeps the area's tab, offline rows and pick
		}
		path = append(path, wire.Crumb{Title: ar.Name, Action: act})
	}
	return append(path, wire.Crumb{Title: name})
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
func (a *app) historySection(ctx context.Context, s State, loc *time.Location) wire.Section {
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
	if pts := series(changes, 96, loc); len(pts) >= 3 {
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
		t.Rows = append(t.Rows, []string{c.At.In(loc).Format("01-02 15:04"), orDash(stateText(State{EntityID: s.EntityID, State: c.State, Attributes: s.Attributes}))})
	}
	sec.Items = append(sec.Items, t)
	return sec
}

// series turns numeric changes into at most n points: the value each change set, averaged into n even buckets over
// the span when there are more.
func series(cs []Change, n int, loc *time.Location) []wire.ChartPoint {
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
	label := func(t time.Time) string { return t.In(loc).Format("15:04") }
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
