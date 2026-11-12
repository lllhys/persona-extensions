package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// 家's overview (homePage), top to bottom, each part only when the home has something for it:
//   - the home at a glance: how many devices are on and of which kinds, the rooms' temperature and humidity, the
//     weather outside, how many things need a look.
//   - 需要注意: alarms (a leak, smoke, a jammed lock, a sounding siren), doors, windows and garage doors left open,
//     locks not locked, batteries running low (once a device), and the devices offline (one row that unfolds when
//     there are several).
//   - 设备: the devices one uses (lights, switches, fans, covers, climates, players…), those on first. UI version 6:
//     a tile each with its switch, a tap opening it; before, a row each that a tap turns on or off (点按查看: opens).
//   - 调节 (UI version 6): a slider or a stepper for what is on (a light's brightness, a fan's speed, a climate's
//     target temperature) and for every curtain or blind that can be set to a position (not a garage door, a gate or
//     a door: those open on their own page).
//   - 区域: the areas with something on, something to look at or their air first (tiles; rows before UI version 6),
//     the quiet ones after them in a carousel (one row that unfolds, before UI version 6), each in Home Assistant's
//     order. Devices in no area count and are listed after the others.
//
// The switches and dials write through setControl (page.go), with a row tap's checks; locks and alarm panels have
// none here: they ask first, on their own page.

const (
	quickMost  = 24 // device tiles (or rows) before 全部设备; more when more are on
	tilesMost  = 60 // a grid takes 60 tiles
	quietMost  = 20 // a carousel takes 20
	dialsMost  = 12
	alertsMost = 12
	offMost    = 50 // offline devices listed when unfolded
	quietFold  = 3  // before UI version 6, more quiet areas than this are one row
	lowBattery = 20 // percent
	// deviceColumns: a device's tile says little, so a wide screen takes more of them in a row (a phone two)
	deviceColumns = 6
)

// overviewDomains are the devices the overview offers: deviceDomains less what asks first or is run rather than
// used (locks, alarm panels, sirens, remotes, scenes and scripts).
var overviewDomains = []string{"light", "switch", "fan", "cover", "climate", "humidifier", "input_boolean", "media_player",
	"vacuum", "water_heater", "valve", "lawn_mower"}

// powerDomains are those a switch turns on and off (turn_on / turn_off).
var powerDomains = []string{"light", "switch", "fan", "input_boolean", "humidifier", "climate", "media_player"}

// areaFacts is an area as the overview tells it.
type areaFacts struct {
	ar          *Area
	devices     int     // its devices: a device's main entities (deviceRows), not scenes, scripts and remotes
	on          []State // those on
	offline     int
	temp, humid []float64 // its air (airOf)
	alerts      []alert   // what in it needs a look
}

// lively: something on in it, something to look at, or its air to show.
func (af *areaFacts) lively() bool {
	return len(af.on) > 0 || len(af.alerts) > 0 || len(af.temp) > 0 || len(af.humid) > 0
}

// alert is something that needs a look: an alarm (danger) or a door, window or lock left open, a battery running low
// (caution).
type alert struct {
	s     State
	what  string // the badge: 有水、开着、没锁、电量 12%
	tone  string
	group string // what the summary counts it as: 报警、门窗、门锁、电量
}

var alertGroups = []string{"报警", "门窗", "门锁", "电量"}

// homeFacts is one read of the home as the overview draws it.
type homeFacts struct {
	areas   []*areaFacts // the visible areas, in Home Assistant's order
	devices []State      // their devices, in the areas' order, offline ones too (a home without areas: all of them)
	on      []State      // those on
	alerts  []alert      // danger first, then caution
	offline []State      // the devices offline, in the areas' order
	weather *State       // the first weather entity that has a temperature
}

// counted says s is a device the figures count: not a scene or a script (they are run) nor a remote (on whenever it
// is reachable).
func counted(s State) bool {
	d := s.Domain()
	return d != "scene" && d != "script" && d != "remote"
}

// offline: Home Assistant cannot reach the device. A state it does not know (unknown: a blind that only assumes its
// state, a player just started) is not offline: the device answers.
func offline(s State) bool { return s.State == "unavailable" }

func gatherHome(hm *home, reg *Registry) *homeFacts {
	f := &homeFacts{}
	devices := func(ar *Area, af *areaFacts) {
		for _, dr := range deviceRows(hm, reg, ar) {
			s := dr.s
			if !counted(s) {
				continue
			}
			f.devices = append(f.devices, s)
			switch {
			case offline(s):
				f.offline = append(f.offline, s)
			case stateTone(s) == "positive":
				f.on = append(f.on, s)
			}
			if af == nil {
				continue
			}
			af.devices++
			switch {
			case offline(s):
				af.offline++
			case stateTone(s) == "positive":
				af.on = append(af.on, s)
			}
		}
	}
	of := map[*Area]*areaFacts{}
	for i := range hm.Areas {
		ar := &hm.Areas[i]
		af := &areaFacts{ar: ar}
		af.temp, af.humid = airOf(hm, reg, ar)
		devices(ar, af)
		f.areas = append(f.areas, af)
		of[ar] = af
	}
	// the devices in no area count too, after the areas' (an entity Home Assistant keeps for a device it no longer
	// has, restored and unavailable, is not one)
	loose := &Area{}
	for _, s := range hm.States {
		if hm.AreaOf[s.EntityID] == nil && hm.visible(s.EntityID) && !(offline(s) && s.attr("restored") == "true") {
			loose.Entities = append(loose.Entities, s.EntityID)
		}
	}
	devices(loose, nil)
	var danger, caution []alert
	// a device that reports a low battery twice (a percentage and a binary sensor) is one alert, the percentage's
	battery, percent := map[string]bool{}, map[string]bool{}
	if reg != nil {
		for _, s := range hm.States {
			if al, ok := alertOf(s); ok && al.group == "电量" && s.Domain() == "sensor" {
				percent[reg.Entities[s.EntityID].DeviceID] = true
			}
		}
	}
	for _, s := range hm.States {
		if !hm.visible(s.EntityID) || regHidden(reg, s.EntityID) {
			continue
		}
		if f.weather == nil && s.Domain() == "weather" && !s.Unavailable() {
			if _, ok := s.num("temperature"); ok {
				w := s
				f.weather = &w
			}
		}
		al, ok := alertOf(s)
		if !ok {
			continue
		}
		if al.group == "电量" && reg != nil {
			if dev := reg.Entities[s.EntityID].DeviceID; dev != "" {
				if battery[dev] || s.Domain() != "sensor" && percent[dev] {
					continue
				}
				battery[dev] = true
			}
		}
		if al.tone == "danger" {
			danger = append(danger, al)
		} else {
			caution = append(caution, al)
		}
		if af := of[hm.AreaOf[s.EntityID]]; af != nil {
			af.alerts = append(af.alerts, al)
		}
	}
	f.alerts = append(danger, caution...)
	return f
}

// regHidden says the registry hides e (hidden_by): Home Assistant's own dashboards leave it out too.
func regHidden(reg *Registry, e string) bool {
	if reg == nil {
		return false
	}
	re, ok := reg.Entities[e]
	return ok && re.HiddenBy != nil && *re.HiddenBy != ""
}

// alertOf says whether s needs a look, and how.
func alertOf(s State) (alert, bool) {
	if s.Unavailable() {
		return alert{}, false
	}
	if stateTone(s) == "danger" {
		return alert{s: s, what: clip(orDash(stateText(s)), 8), tone: "danger", group: "报警"}, true
	}
	dc := s.attr("device_class")
	switch s.Domain() {
	case "siren":
		if s.State == "on" {
			return alert{s: s, what: "响着", tone: "danger", group: "报警"}, true
		}
	case "lock":
		switch s.State {
		case "unlocked", "unlocking", "open", "opening":
			return alert{s: s, what: stateText(s), tone: "caution", group: "门锁"}, true
		}
	case "binary_sensor":
		switch {
		case s.State != "on":
		case dc == "door" || dc == "window" || dc == "garage_door" || dc == "opening":
			return alert{s: s, what: "开着", tone: "caution", group: "门窗"}, true
		case dc == "battery":
			return alert{s: s, what: "电量低", tone: "caution", group: "电量"}, true
		}
	case "cover":
		if (dc == "garage" || dc == "door" || dc == "gate") && (s.State == "open" || s.State == "opening") {
			return alert{s: s, what: stateText(s), tone: "caution", group: "门窗"}, true
		}
	case "sensor":
		if dc == "battery" && s.attr("unit_of_measurement") == "%" {
			if v, err := strconv.ParseFloat(s.State, 64); err == nil && v >= 0 && v <= lowBattery {
				return alert{s: s, what: "电量 " + tidy(math.Round(v)) + "%", tone: "caution", group: "电量"}, true
			}
		}
	}
	return alert{}, false
}

// airOf is an area's air: its temperature and humidity sensors (not a device's diagnostic or setting, nor what a room
// cannot be, like a fridge's), else what its climates read.
func airOf(hm *home, reg *Registry, ar *Area) (temp, humid []float64) {
	var ctemp, chumid []float64
	for _, e := range ar.Entities {
		s, ok := hm.ByID[e]
		if !ok || s.Unavailable() {
			continue
		}
		switch s.Domain() {
		case "sensor":
			if reg != nil {
				if re, ok := reg.Entities[e]; ok && (re.EntityCategory != "" || regHidden(reg, e)) {
					continue
				}
			}
			v, err := strconv.ParseFloat(s.State, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			switch s.attr("device_class") {
			case "temperature":
				if c, ok := roomTemp(v, s.attr("unit_of_measurement")); ok {
					temp = append(temp, c)
				}
			case "humidity":
				if v >= 0 && v <= 100 {
					humid = append(humid, v)
				}
			}
		case "climate":
			if t, ok := s.num("current_temperature"); ok {
				if c, ok := roomTemp(t, tempUnit(hm)); ok {
					ctemp = append(ctemp, c)
				}
			}
			if h, ok := s.num("current_humidity"); ok && h >= 0 && h <= 100 {
				chumid = append(chumid, h)
			}
		}
	}
	if len(temp) == 0 {
		temp = ctemp
	}
	if len(humid) == 0 {
		humid = chumid
	}
	return temp, humid
}

// tempUnit is the home's temperature unit, as its temperature sensors write it (they follow Home Assistant's unit
// system unless one is set otherwise): °F when most do, else °C. A climate's numbers are in it.
func tempUnit(hm *home) string {
	c, f := 0, 0
	for _, s := range hm.States {
		if s.Domain() != "sensor" || s.attr("device_class") != "temperature" {
			continue
		}
		switch s.attr("unit_of_measurement") {
		case "°F":
			f++
		case "°C":
			c++
		}
	}
	if f > c {
		return "°F"
	}
	return "°C"
}

// roomTemp is a temperature in °C when a room can be that warm (10–40 °C).
func roomTemp(v float64, unit string) (float64, bool) {
	c, ok := celsius(v, unit)
	return c, ok && c >= 10 && c <= 40
}

func tempText(c float64) string { return tidy(math.Round(c*10)/10) + "°" }

func humidText(h float64) string { return tidy(math.Round(h)) + "%" }

// airText is an area's air as its tile or row writes it: 24.1° · 40%, 24.1°, 湿度 40%.
func airText(temp, humid []float64) string {
	switch {
	case len(temp) > 0 && len(humid) > 0:
		return tempText(avg(temp)) + " · " + humidText(avg(humid))
	case len(temp) > 0:
		return tempText(avg(temp))
	case len(humid) > 0:
		return "湿度 " + humidText(avg(humid))
	}
	return ""
}

// kindNames are the short names the overview counts devices by.
var kindNames = map[string]string{"light": "灯", "switch": "开关", "input_boolean": "开关", "fan": "风扇", "climate": "空调",
	"cover": "窗帘", "media_player": "播放器", "humidifier": "加湿器", "vacuum": "扫地机", "water_heater": "热水器",
	"valve": "阀门", "lawn_mower": "割草机", "lock": "门锁", "alarm_control_panel": "警戒", "siren": "警报器", "remote": "遥控"}

// kindOf is what the overview counts s as: 灯, 开关, 窗帘 (a garage door, a door or a window by its device class)…
func kindOf(s State) string {
	if s.Domain() == "cover" {
		switch s.attr("device_class") {
		case "garage":
			return "车库门"
		case "door", "gate":
			return "门"
		case "window":
			return "窗"
		}
	}
	if k := kindNames[s.Domain()]; k != "" {
		return k
	}
	return domainName(s.Domain())
}

// kindCounts says how many of each kind there are, in deviceDomains' order: 2 灯 · 1 开关 (sep between).
func kindCounts(ss []State, sep string) string {
	n := map[string]int{}
	var order []string
	for _, d := range deviceDomains {
		for _, s := range ss {
			if s.Domain() != d {
				continue
			}
			k := kindOf(s)
			if n[k] == 0 {
				order = append(order, k)
			}
			n[k]++
		}
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%d %s", n[k], k))
	}
	return strings.Join(parts, sep)
}

// homeStats are the overview's figures: 开着, 温度, 湿度, 室外, 需注意.
func homeStats(hm *home, f *homeFacts) []wire.Stat {
	var stats []wire.Stat
	if len(f.devices) > 0 {
		st := wire.Stat{Label: "开着", Value: strconv.Itoa(len(f.on)), Detail: "都关着"}
		if len(f.on) > 0 {
			st.Detail, st.Tone = kindCounts(f.on, " · "), "positive"
		}
		stats = append(stats, st)
	}
	if st, ok := airStat(hm, f, "温度", func(af *areaFacts) []float64 { return af.temp }, tempText); ok {
		stats = append(stats, st)
	}
	if st, ok := airStat(hm, f, "湿度", func(af *areaFacts) []float64 { return af.humid }, humidText); ok {
		stats = append(stats, st)
	}
	if w := f.weather; w != nil {
		t, _ := w.num("temperature")
		unit := w.attr("temperature_unit")
		if unit == "" {
			unit = tempUnit(hm)
		}
		if c, ok := celsius(t, unit); ok { // °C, as the rooms' are written
			t = c
		}
		st := wire.Stat{Label: "室外", Value: tidy(math.Round(t*10)/10) + "°", Detail: weatherNames[w.State]}
		if st.Detail == "" {
			st.Detail = clip(w.State, 12)
		}
		if h, ok := w.num("humidity"); ok {
			st.Detail = strings.TrimPrefix(st.Detail+" · 湿度 "+humidText(h), " · ")
		}
		stats = append(stats, st)
	}
	if n := len(f.alerts) + len(f.offline); n > 0 {
		count := map[string]int{}
		tone := "caution"
		for _, al := range f.alerts {
			count[al.group]++
			if al.tone == "danger" {
				tone = "danger"
			}
		}
		var parts []string
		for _, g := range alertGroups {
			if count[g] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", count[g], g))
			}
		}
		if len(f.offline) > 0 {
			parts = append(parts, fmt.Sprintf("%d 离线", len(f.offline)))
		}
		stats = append(stats, wire.Stat{Label: "需注意", Value: strconv.Itoa(n), Detail: strings.Join(parts, " · "), Tone: tone})
	}
	return stats
}

// airStat is the home's temperature or humidity: one area's (named, unless the areas are the device types), or the
// areas' average with their range.
func airStat(hm *home, f *homeFacts, label string, of func(*areaFacts) []float64, text func(float64) string) (wire.Stat, bool) {
	var names []string
	var vals []float64
	for _, af := range f.areas {
		if xs := of(af); len(xs) > 0 {
			names, vals = append(names, af.ar.Name), append(vals, avg(xs))
		}
	}
	switch len(vals) {
	case 0:
		return wire.Stat{}, false
	case 1:
		st := wire.Stat{Label: label, Value: text(vals[0])}
		if !hm.ByType {
			st.Detail = clip(names[0], 20)
		}
		return st, true
	}
	lo, hi := slices.Min(vals), slices.Max(vals)
	detail := fmt.Sprintf("%d 个区域", len(vals))
	if a, b := text(lo), text(hi); a != b {
		detail += " " + strings.TrimSuffix(strings.TrimSuffix(a, "°"), "%") + "–" + b
	}
	return wire.Stat{Label: label, Value: text(avg(vals)), Detail: detail}, true
}

var weatherNames = map[string]string{"clear-night": "晴", "cloudy": "阴", "exceptional": "极端天气", "fog": "雾", "hail": "冰雹",
	"lightning": "雷电", "lightning-rainy": "雷阵雨", "partlycloudy": "多云", "pouring": "暴雨", "rainy": "雨", "snowy": "雪",
	"snowy-rainy": "雨夹雪", "sunny": "晴", "windy": "有风", "windy-variant": "多云有风"}

// statItems are the figures as the app draws them: a stat grid (UI version 2), metrics before.
func statItems(r *ext.UIRequest, stats []wire.Stat) []wire.Component {
	if r.UIVersion >= 2 {
		return []wire.Component{{Type: "stat_grid", Key: "summary", Stats: stats}}
	}
	var out []wire.Component
	for _, st := range stats {
		out = append(out, wire.Component{Type: "metric", Label: st.Label, Value: st.Value, Detail: st.Detail, Tone: st.Tone})
	}
	return out
}

// homePage is 家's overview.
func (a *app) homePage(r *ext.UIRequest, hm *home, reg *Registry, st pageState, v *wire.View) {
	f := gatherHome(hm, reg)
	if stats := homeStats(hm, f); len(stats) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: statItems(r, stats)})
	}
	if sec := alertSection(r, hm, f, st); len(sec.Items) > 0 {
		v.Sections = append(v.Sections, sec)
	}
	if r.UIVersion >= 6 {
		a.deviceTiles(hm, f, st, v)
		a.dialSection(hm, f, v)
	} else {
		a.deviceList(r, hm, f, st, v)
	}
	if hm.Note != "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: hm.Note, Style: "secondary"}}})
	}
	if r.UIVersion >= 6 {
		areaTiles(r, hm, f, st, v)
	} else {
		areaList(r, hm, f, st, v)
	}
	v.Sections = append(v.Sections, footer())
}

// alertSection is 需要注意: the alarms, then what is left open or running low, then the devices offline (st.Lost
// unfolds them).
func alertSection(r *ext.UIRequest, hm *home, f *homeFacts, st pageState) wire.Section {
	sec := wire.Section{Title: fmt.Sprintf("需要注意 · %d", len(f.alerts)+len(f.offline))}
	for i, al := range f.alerts {
		if i == alertsMost {
			sec.Footer = fmt.Sprintf("只列前 %d 条，其余的在各区域里", alertsMost)
			break
		}
		color := "orange"
		if al.tone == "danger" {
			color = "red"
		}
		sec.Items = append(sec.Items, lookRow(r, hm, "a:", al.s, &wire.Badge{Text: al.what, Tone: al.tone}, color))
	}
	off := f.offline
	switch {
	case len(off) == 0:
	case len(off) == 1 || st.Lost:
		for i, s := range off {
			if i == offMost {
				sec.Footer = fmt.Sprintf("只列前 %d 个离线的，用搜索找别的", offMost)
				break
			}
			sec.Items = append(sec.Items, lookRow(r, hm, "o:", s, &wire.Badge{Text: "离线", Tone: "caution"}, "gray"))
		}
		if len(off) > 1 {
			sec.Items = append(sec.Items, wire.Component{Type: "row", Key: "offline", Title: "收起离线的设备", Action: &wire.Action{ID: "lost"}})
		}
	default:
		var names []string
		for _, s := range off[:min(4, len(off))] {
			names = append(names, shortName(hm, s))
		}
		row := wire.Component{Type: "row", Key: "offline", Title: fmt.Sprintf("%d 个设备离线", len(off)),
			Subtitle: clip(strings.Join(names, "、"), 80), Badge: &wire.Badge{Text: "离线", Tone: "caution"}, Action: &wire.Action{ID: "lost"}}
		if r.UIVersion >= 6 {
			row.Icon, row.IconColor = "wifi", "gray"
		}
		sec.Items = append(sec.Items, row)
	}
	return sec
}

// lookRow is an entity that needs a look: its name, where it is, the badge; a tap opens it.
func lookRow(r *ext.UIRequest, hm *home, prefix string, s State, badge *wire.Badge, color string) wire.Component {
	where := hm.areaName(s.EntityID)
	if where == "" || hm.ByType {
		where = domainName(s.Domain())
	}
	row := wire.Component{Type: "row", Key: ref(prefix, s.EntityID), Title: clip(shortName(hm, s), 80), Subtitle: where,
		Badge: badge, Action: openAction(s.EntityID)}
	if r.UIVersion >= 6 {
		row.Icon, row.IconColor = entityIcon(s), color
	}
	return row
}

// quickList is the devices for 设备: those of overviewDomains not offline, those on first, then in the areas' order (the
// ones in no area last); all of them (at most tilesMost) when all, else those on and the first few. total is how many
// there are.
func quickList(f *homeFacts, all bool) (shown []State, total int) {
	var on, rest []State
	for _, s := range f.devices {
		if !slices.Contains(overviewDomains, s.Domain()) || offline(s) {
			continue
		}
		if stateTone(s) == "positive" {
			on = append(on, s)
		} else {
			rest = append(rest, s)
		}
	}
	list := append(on, rest...)
	n := max(quickMost, len(on))
	if all {
		n = tilesMost
	}
	return list[:min(n, tilesMost, len(list))], len(list)
}

// moreItems are 全部设备 (or 收起) under 设备.
func moreItems(shown, total int, all bool) ([]wire.Component, string) {
	switch {
	case shown < total && !all:
		return []wire.Component{{Type: "row", Key: "more", Title: fmt.Sprintf("全部设备 · %d", total),
			Subtitle: "现在列的是开着的和前几个", Action: &wire.Action{ID: "more"}}}, ""
	case all && total > quickMost:
		footer := ""
		if shown < total {
			footer = fmt.Sprintf("只列前 %d 个，其余的在各区域里", shown)
		}
		return []wire.Component{{Type: "row", Key: "more", Title: "只看开着的和前几个", Action: &wire.Action{ID: "more"}}}, footer
	}
	return nil, ""
}

// deviceTiles is 设备 on UI version 6: a tile each.
func (a *app) deviceTiles(hm *home, f *homeFacts, st pageState, v *wire.View) {
	shown, total := quickList(f, st.All)
	if total == 0 {
		return
	}
	tiles := make([]wire.Tile, 0, len(shown))
	for _, s := range shown {
		tiles = append(tiles, a.deviceTile(hm, s, true))
	}
	sec := wire.Section{Title: fmt.Sprintf("设备 · %d", total), Items: []wire.Component{{Type: "grid", Key: "devices", Tiles: tiles, MaxColumns: deviceColumns}}}
	more, footer := moreItems(len(shown), total, st.All)
	sec.Items, sec.Footer = append(sec.Items, more...), footer
	v.Sections = append(v.Sections, sec)
}

// deviceList is 设备 before UI version 6: a row each, with its area (deviceRow: a light's or a switch's row turns it on
// or off, unless 点按查看).
func (a *app) deviceList(r *ext.UIRequest, hm *home, f *homeFacts, st pageState, v *wire.View) {
	shown, total := quickList(f, st.All)
	if total == 0 {
		return
	}
	sec := wire.Section{Title: fmt.Sprintf("设备 · %d", total)}
	var rows []wire.Component
	quick := false
	for _, s := range shown {
		row, q := a.deviceRow(hm, devRow{s: s}, st.Pick)
		quick = quick || q
		row.Key = ref("h:", s.EntityID)
		if n := hm.areaName(s.EntityID); n != "" && !hm.ByType {
			row.Subtitle = strings.TrimSuffix(n+" · "+row.Subtitle, " · ")
		}
		rows = append(rows, row)
	}
	if quick && r.UIVersion >= 2 {
		pick := wire.Component{Type: "segmented", Name: "pick", Value: "toggle", Action: &wire.Action{ID: "pick"},
			Choices: []wire.Choice{{Value: "toggle", Title: "点按开关"}, {Value: "open", Title: "点按查看"}}}
		if st.Pick {
			pick.Value = "open"
		}
		sec.Items = append(sec.Items, pick)
	}
	sec.Items = append(sec.Items, rows...)
	if quick && r.UIVersion < 2 {
		title := "点设备是打开它"
		if st.Pick {
			title = "点设备是开关它"
		}
		sec.Items = append(sec.Items, wire.Component{Type: "row", Title: title, Action: &wire.Action{ID: "pick"}})
	}
	more, footer := moreItems(len(shown), total, st.All)
	sec.Items, sec.Footer = append(sec.Items, more...), footer
	v.Sections = append(v.Sections, sec)
}

// dialSection is 调节 (UI version 6): the dials of what is on and of the covers.
func (a *app) dialSection(hm *home, f *homeFacts, v *wire.View) {
	list, _ := quickList(f, true)
	var rows []wire.Component
	n := 0
	for _, s := range list {
		row, ok := a.dialRow(hm, s, true)
		if !ok {
			continue
		}
		if n++; n <= dialsMost {
			rows = append(rows, row)
		}
	}
	if n == 0 {
		return
	}
	sec := wire.Section{Title: fmt.Sprintf("调节 · %d", n), Items: rows}
	if n > dialsMost {
		sec.Footer = fmt.Sprintf("只列前 %d 个，其余的点开设备调", dialsMost)
	}
	v.Sections = append(v.Sections, sec)
}

// deviceTile is a device as a tile: its icon in its kind's color, lit when on; its name, where it is and what the
// switch does not say (a climate's mode, a cover's state); the figure that matters (brightness, speed, target
// temperature, position); its switch when it has one and may be written. A tap opens it.
func (a *app) deviceTile(hm *home, s State, withArea bool) wire.Tile {
	t := wire.Tile{Key: ref("t:", s.EntityID), Title: clip(shortName(hm, s), 40), Icon: entityIcon(s), Color: entityColor(s),
		Active: stateTone(s) == "positive", Action: openAction(s.EntityID)}
	var sub []string
	if n := hm.areaName(s.EntityID); withArea && n != "" && !hm.ByType {
		sub = append(sub, n)
	}
	state, figure := tileWords(s)
	if offline(s) {
		t.Badge = &wire.Badge{Text: "离线", Tone: "caution"}
		state, figure = "", ""
	} else if t.Control = a.powerControl(s); t.Control == nil && state == "" {
		state = stateText(s) // no switch to show it
	}
	if state != "" {
		sub = append(sub, state)
	}
	t.Subtitle, t.Value = clip(strings.Join(sub, " · "), 80), clip(figure, 40)
	return t
}

// tileWords are what a device's tile says besides its name: the state its switch does not show, and its figure.
func tileWords(s State) (state, figure string) {
	switch s.Domain() {
	case "light":
		if b, ok := s.num("brightness"); ok && s.State == "on" {
			figure = fmt.Sprintf("亮度 %d%%", int(math.Round(b/255*100)))
		}
	case "fan":
		if p, ok := s.num("percentage"); ok && s.State == "on" && p > 0 {
			figure = "风速 " + tidy(math.Round(p)) + "%"
		}
	case "humidifier":
		if h, ok := s.num("humidity"); ok && s.State == "on" {
			figure = "湿度 " + humidText(h)
		}
	case "climate":
		state = stateText(s)
		if t, ok := s.num("temperature"); ok && s.State != "off" {
			figure = "设定 " + tidy(t) + "°"
		} else if c, ok := s.num("current_temperature"); ok {
			figure = "室温 " + tidy(c) + "°"
		}
	case "cover", "valve":
		state = stateText(s)
		if p, ok := s.num("current_position"); ok {
			figure = "开度 " + tidy(math.Round(p)) + "%"
		}
	case "media_player":
		state = stateText(s)
		if t := s.attr("media_title"); t != "" && s.State == "playing" {
			figure = clip(t, 20)
		}
	case "vacuum", "lawn_mower":
		state = stateText(s)
		if b, ok := s.num("battery_level"); ok {
			figure = "电量 " + tidy(math.Round(b)) + "%"
		}
	case "water_heater":
		state = stateText(s)
		if t, ok := s.num("temperature"); ok {
			figure = "设定 " + tidy(t) + "°"
		}
	case "switch", "input_boolean":
	default:
		state = stateText(s)
	}
	return state, figure
}

// features are an entity's supported_features bits.
func features(s State) int64 {
	f, _ := s.num("supported_features")
	return int64(f)
}

// The bits of supported_features the overview reads.
const (
	fanSetSpeed       = 1   // FanEntityFeature.SET_SPEED
	coverSetPosition  = 4   // CoverEntityFeature.SET_POSITION
	climateTurnOff    = 128 // ClimateEntityFeature.TURN_OFF
	climateTurnOn     = 256 // ClimateEntityFeature.TURN_ON
	playerTurnOn      = 128 // MediaPlayerEntityFeature.TURN_ON
	playerTurnOff     = 256 // MediaPlayerEntityFeature.TURN_OFF
	targetTempDefault = 1.0
)

// controlOf is a control's name and action: ctl:<kind> with the entity in its values, which setControl reads.
func controlOf(kind, eid string) (string, wire.Action) {
	return ref(kind+":", eid), wire.Action{ID: "ctl:" + kind, Values: map[string]string{"entity": eid}}
}

// powerControl is s's switch (turn_on / turn_off), or nil: a light, switch, fan, helper or humidifier that is on or
// off, a climate or a player that can be turned on and off, of a domain that may be written.
func (a *app) powerControl(s State) *wire.RowControl {
	d := s.Domain()
	var on bool
	switch d {
	case "light", "switch", "fan", "input_boolean", "humidifier":
		if s.State != "on" && s.State != "off" {
			return nil
		}
		on = s.State == "on"
	case "climate":
		if !switchable(s) {
			return nil
		}
		on = s.State != "off"
	case "media_player":
		if !switchable(s) {
			return nil
		}
		on = s.State != "off" && s.State != "standby"
	default:
		return nil
	}
	if s.Unavailable() || a.canWrite(d, false) != nil {
		return nil
	}
	name, act := controlOf("power", s.EntityID)
	return &wire.RowControl{Kind: "switch", Name: name, On: on, Action: act}
}

// dialRow is s's dial as a row (UI version 6), or false: a light's brightness and a fan's speed (sliders) when on, a
// climate's target temperature (a stepper) when on, a cover's position (a slider) when it can be set. A tap opens it.
func (a *app) dialRow(hm *home, s State, withArea bool) (wire.Component, bool) {
	d := s.Domain()
	if s.Unavailable() || !slices.Contains([]string{"light", "fan", "climate", "cover"}, d) || a.canWrite(d, false) != nil {
		return wire.Component{}, false
	}
	var c *wire.RowControl
	var what string
	switch d {
	case "light":
		b, ok := s.num("brightness")
		if s.State != "on" || !ok || !dimmable(s) {
			return wire.Component{}, false
		}
		c, what = slider("bri", s.EntityID, math.Round(b/255*100), 1, 100, 1), "亮度"
	case "fan":
		p, ok := s.num("percentage")
		if s.State != "on" || !ok || features(s)&fanSetSpeed == 0 {
			return wire.Component{}, false
		}
		step := 1.0
		if ps, ok := s.num("percentage_step"); ok && ps >= 1 && ps <= 100 {
			step = ps
		}
		c, what = slider("pct", s.EntityID, p, 0, 100, step), "风速"
	case "climate":
		t, ok := s.num("temperature")
		if s.State == "off" || !ok {
			return wire.Component{}, false
		}
		lo, hi := math.Min(s.attrNum("min_temp", 7), t), math.Max(s.attrNum("max_temp", 35), t)
		step := s.attrNum("target_temp_step", targetTempDefault)
		if lo >= hi || step <= 0 {
			return wire.Component{}, false
		}
		name, act := controlOf("temp", s.EntityID)
		c = &wire.RowControl{Kind: "stepper", Name: name, Value: &t, Min: &lo, Max: &hi, Step: &step, Unit: tempUnit(hm), Action: act}
		what = stateText(s)
		if cur, ok := s.num("current_temperature"); ok {
			what += " · 室温 " + tidy(cur) + "°"
		}
	case "cover":
		p, ok := s.num("current_position")
		if !ok || !positionable(s) {
			return wire.Component{}, false
		}
		c, what = slider("pos", s.EntityID, p, 0, 100, 1), "开度"
	}
	var sub []string
	if n := hm.areaName(s.EntityID); withArea && n != "" && !hm.ByType {
		sub = append(sub, n)
	}
	sub = append(sub, what)
	return wire.Component{Type: "row", Key: ref("d:", s.EntityID), Title: clip(shortName(hm, s), 80), Subtitle: strings.Join(sub, " · "),
		Icon: entityIcon(s), IconColor: entityColor(s), Control: c, Action: openAction(s.EntityID)}, true
}

// dimmable: a light whose brightness can be set (a color mode other than on / off).
func dimmable(s State) bool {
	modes := s.list("supported_color_modes")
	return s.Domain() == "light" && !(len(modes) == 1 && modes[0] == "onoff")
}

// positionable: a cover that can be set to a position, and not one that lets people in (a garage door, a gate, a
// door): a slider moves at a touch, those open on their own page.
func positionable(s State) bool {
	switch s.attr("device_class") {
	case "garage", "gate", "door":
		return false
	}
	return s.Domain() == "cover" && features(s)&coverSetPosition != 0
}

// switchable: a climate or a player that declares it can be turned on and off (others only change mode or play).
func switchable(s State) bool {
	switch s.Domain() {
	case "climate":
		return features(s)&(climateTurnOn|climateTurnOff) == climateTurnOn|climateTurnOff
	case "media_player":
		return features(s)&(playerTurnOn|playerTurnOff) == playerTurnOn|playerTurnOff
	}
	return true
}

// slider is a percentage's slider on eid, its value held to lo…hi.
func slider(kind, eid string, v, lo, hi, step float64) *wire.RowControl {
	v = math.Max(lo, math.Min(hi, v))
	name, act := controlOf(kind, eid)
	return &wire.RowControl{Kind: "slider", Name: name, Value: &v, Min: &lo, Max: &hi, Step: &step, Unit: "%", Action: act}
}

// splitAreas is the areas with something on, something to look at or their air (an alarm first, then the busiest),
// and the quiet ones, each otherwise in Home Assistant's order (the user's own, which its area settings can change).
func splitAreas(f *homeFacts) (lively, quiet []*areaFacts) {
	for _, af := range f.areas {
		if af.lively() {
			lively = append(lively, af)
		} else {
			quiet = append(quiet, af)
		}
	}
	danger := func(af *areaFacts) bool {
		return slices.ContainsFunc(af.alerts, func(al alert) bool { return al.tone == "danger" })
	}
	sort.SliceStable(lively, func(i, j int) bool {
		if di, dj := danger(lively[i]), danger(lively[j]); di != dj {
			return di
		}
		return len(lively[i].on) > len(lively[j].on)
	})
	return lively, quiet
}

func areasTitle(hm *home, n int) string {
	if hm.ByType {
		return fmt.Sprintf("类型 · %d", n)
	}
	return fmt.Sprintf("区域 · %d", n)
}

// areaStatus is an area's line: what kinds are on (2 灯、1 开关开着; a type's, how many: 2 开着), else how many
// devices it has.
func areaStatus(hm *home, af *areaFacts) string {
	switch {
	case len(af.on) > 0 && hm.ByType:
		return fmt.Sprintf("%d 开着", len(af.on))
	case len(af.on) > 0:
		return kindCounts(af.on, "、") + "开着"
	case af.devices > 0:
		return fmt.Sprintf("%d 个设备", af.devices)
	}
	return ""
}

// areaBadge is what in an area needs a look: an alarm, else what is open or low, else what is offline.
func areaBadge(af *areaFacts) *wire.Badge {
	for _, al := range af.alerts {
		if al.tone == "danger" {
			return &wire.Badge{Text: al.what, Tone: "danger"}
		}
	}
	switch {
	case len(af.alerts) == 1:
		return &wire.Badge{Text: af.alerts[0].what, Tone: "caution"}
	case len(af.alerts) > 1:
		return &wire.Badge{Text: fmt.Sprintf("%d 项", len(af.alerts)), Tone: "caution"}
	case af.offline > 0:
		return &wire.Badge{Text: fmt.Sprintf("%d 离线", af.offline), Tone: "caution"}
	}
	return nil
}

// areaTile is an area as a tile: its icon, its name, what is on, its air; lit when something is on, its badge what
// needs a look. A tap opens the area.
func areaTile(hm *home, af *areaFacts) wire.Tile {
	icon, color := areaLook(hm, af.ar)
	return wire.Tile{Key: ref("area:", af.ar.ID), Title: clip(af.ar.Name, 40), Subtitle: clip(areaStatus(hm, af), 80),
		Value: clip(airText(af.temp, af.humid), 40), Icon: icon, Color: color, Active: len(af.on) > 0, Badge: areaBadge(af),
		Action: areaAction(af.ar.ID)}
}

// areaTiles is 区域 on UI version 6: the lively areas as tiles, the quiet ones after them in a carousel, or, when
// there are more than a carousel takes, one row that unfolds them (st.Rooms) into tiles. With none lively, all are
// tiles: they are the way into the areas.
func areaTiles(r *ext.UIRequest, hm *home, f *homeFacts, st pageState, v *wire.View) {
	sec := wire.Section{Title: areasTitle(hm, len(f.areas))}
	if len(f.areas) == 0 {
		sec.Items = append(sec.Items, emptyItem(r, "还没有区域", "在 Home Assistant 里给设备分好区域，这里就按区域列出"))
		v.Sections = append(v.Sections, sec)
		return
	}
	lively, quiet := splitAreas(f)
	tiles := func(list []*areaFacts) []wire.Tile {
		if len(list) > tilesMost {
			list, sec.Footer = list[:tilesMost], fmt.Sprintf("只列前 %d 个，用上面的区域筛选或搜索找别的", tilesMost)
		}
		out := make([]wire.Tile, 0, len(list))
		for _, af := range list {
			out = append(out, areaTile(hm, af))
		}
		return out
	}
	switch {
	case len(lively) == 0 || len(quiet) == 0:
		sec.Items = append(sec.Items, wire.Component{Type: "grid", Key: "areas", Tiles: tiles(append(lively, quiet...))})
	case len(quiet) <= quietMost:
		sec.Items = append(sec.Items, wire.Component{Type: "grid", Key: "areas", Tiles: tiles(lively)},
			wire.Component{Type: "carousel", Key: "quiet", Title: quietTitle(hm, len(quiet)), Width: "narrow", Tiles: tiles(quiet)})
	case st.Rooms:
		sec.Items = append(sec.Items, wire.Component{Type: "grid", Key: "areas", Tiles: tiles(lively)},
			wire.Component{Type: "grid", Key: "quiet", Tiles: tiles(quiet)},
			wire.Component{Type: "row", Key: "rooms", Title: "收起没开东西的区域", Action: &wire.Action{ID: "rooms"}})
	default:
		sec.Items = append(sec.Items, wire.Component{Type: "grid", Key: "areas", Tiles: tiles(lively)}, quietRow(hm, quiet))
	}
	v.Sections = append(v.Sections, sec)
}

func quietTitle(hm *home, n int) string {
	if hm.ByType {
		return fmt.Sprintf("其他类型 · %d", n)
	}
	return fmt.Sprintf("其他区域 · %d", n)
}

// quietRow is the quiet areas folded into one row: how many, their names; a tap unfolds them (rooms).
func quietRow(hm *home, quiet []*areaFacts) wire.Component {
	var names []string
	for _, af := range quiet {
		names = append(names, af.ar.Name)
	}
	return wire.Component{Type: "row", Key: "rooms", Title: quietTitle(hm, len(quiet)),
		Subtitle: clip("都关着："+strings.Join(names, "、"), 80), Action: &wire.Action{ID: "rooms"}}
}

// areaList is 区域 before UI version 6: a row per lively area, the quiet ones one row that unfolds (st.Rooms) when
// there are more than a few.
func areaList(r *ext.UIRequest, hm *home, f *homeFacts, st pageState, v *wire.View) {
	sec := wire.Section{Title: areasTitle(hm, len(f.areas))}
	if len(f.areas) == 0 {
		sec.Items = append(sec.Items, emptyItem(r, "还没有区域", "在 Home Assistant 里给设备分好区域，这里就按区域列出"))
		v.Sections = append(v.Sections, sec)
		return
	}
	lively, quiet := splitAreas(f)
	rows := 0
	add := func(list []*areaFacts) {
		for _, af := range list {
			if rows == tilesMost {
				sec.Footer = fmt.Sprintf("只列前 %d 个，用上面的区域筛选或搜索找别的", tilesMost)
				return
			}
			sec.Items = append(sec.Items, areaRow(hm, af))
			rows++
		}
	}
	add(lively)
	fold := len(lively) > 0 && len(quiet) > quietFold
	if fold && !st.Rooms {
		sec.Items = append(sec.Items, quietRow(hm, quiet))
	} else {
		add(quiet)
		if fold {
			sec.Items = append(sec.Items, wire.Component{Type: "row", Key: "rooms", Title: "收起没开东西的区域", Action: &wire.Action{ID: "rooms"}})
		}
	}
	v.Sections = append(v.Sections, sec)
}

// areaRow is an area as a row: what is on, its air on the right, its badge what needs a look or how many are on.
func areaRow(hm *home, af *areaFacts) wire.Component {
	row := wire.Component{Type: "row", Key: ref("area:", af.ar.ID), Title: clip(af.ar.Name, 80), Subtitle: clip(areaStatus(hm, af), 120),
		Value: airText(af.temp, af.humid), Badge: areaBadge(af), Action: areaAction(af.ar.ID)}
	if row.Badge == nil && len(af.on) > 0 && !hm.ByType { // a type's line already says how many
		row.Badge = &wire.Badge{Text: fmt.Sprintf("%d 开着", len(af.on)), Tone: "positive"}
	}
	return row
}

// entityIcon is the icon of what s is (UI version 6's set).
func entityIcon(s State) string {
	dc := s.attr("device_class")
	switch s.Domain() {
	case "light":
		return "light"
	case "switch", "input_boolean":
		if dc == "outlet" {
			return "plug"
		}
		return "power"
	case "fan":
		return "fan"
	case "climate", "water_heater":
		return "thermometer"
	case "humidifier", "valve":
		return "droplet"
	case "cover":
		switch dc {
		case "garage":
			return "car"
		case "door", "gate":
			return "door"
		}
		return "window"
	case "media_player":
		if dc == "tv" {
			return "tv"
		}
		return "speaker"
	case "vacuum":
		return "robot"
	case "lawn_mower":
		return "leaf"
	case "lock":
		switch s.State {
		case "unlocked", "unlocking", "open", "opening":
			return "unlock"
		}
		return "lock"
	case "alarm_control_panel":
		return "shield"
	case "siren":
		return "bell"
	case "scene":
		return "star"
	case "script":
		return "play"
	case "remote":
		return "tv"
	case "binary_sensor":
		switch dc {
		case "door", "opening":
			return "door"
		case "window":
			return "window"
		case "garage_door":
			return "car"
		case "moisture":
			return "droplet"
		case "battery":
			return "battery"
		case "motion", "occupancy", "presence":
			return "person"
		case "connectivity":
			return "wifi"
		}
		return "warning"
	case "sensor":
		switch dc {
		case "temperature":
			return "thermometer"
		case "humidity", "moisture":
			return "droplet"
		case "battery":
			return "battery"
		case "power", "energy", "current", "voltage":
			return "bolt"
		}
		return "gauge"
	}
	return "home"
}

// entityColor is the color of s's kind (a climate's by its mode).
func entityColor(s State) string {
	switch s.Domain() {
	case "light":
		return "amber"
	case "switch", "input_boolean", "valve":
		return "blue"
	case "fan", "humidifier":
		return "teal"
	case "climate":
		if s.State == "heat" {
			return "orange"
		}
		return "blue"
	case "water_heater":
		return "orange"
	case "cover":
		return "indigo"
	case "media_player", "remote":
		return "purple"
	case "vacuum", "lawn_mower":
		return "green"
	}
	return "gray"
}

// areaLooks are an area's icon and color by what its name says it is; the word nearest the end of the name wins
// (主卧卫生间 is a bathroom, 客厅阳台 a balcony).
var areaLooks = []struct {
	words       []string
	icon, color string
}{
	{[]string{"客厅", "起居", "大厅", "living", "lounge"}, "sofa", "orange"},
	{[]string{"卧", "睡房", "bedroom"}, "moon", "indigo"},
	{[]string{"儿童", "宝宝", "婴儿", "kid", "child", "nursery"}, "star", "pink"},
	{[]string{"厨", "餐", "kitchen", "dining"}, "home", "amber"},
	{[]string{"卫生间", "浴", "洗手间", "厕", "盥洗", "洗衣", "bath", "toilet", "washroom", "shower", "laundry"}, "droplet", "teal"},
	{[]string{"书房", "办公", "工作室", "学习", "study", "office"}, "doc", "blue"},
	{[]string{"阳台", "花园", "庭院", "院子", "露台", "天台", "balcony", "garden", "yard", "patio", "terrace"}, "leaf", "green"},
	{[]string{"车库", "车位", "garage", "carport"}, "car", "gray"},
	{[]string{"玄关", "门厅", "入户", "走廊", "过道", "门口", "楼梯", "entrance", "entry", "hallway", "corridor", "foyer", "stair"}, "door", "purple"},
	{[]string{"影音", "影院", "娱乐", "theater", "cinema"}, "tv", "purple"},
	{[]string{"储物", "储藏", "杂物", "衣帽", "storage", "closet", "pantry"}, "folder", "gray"},
	{[]string{"机房", "弱电", "network", "server"}, "wifi", "blue"},
}

// areaLook is an area's icon and color: by its name, or a type's (the areas are the device types) by the domain.
func areaLook(hm *home, ar *Area) (icon, color string) {
	if hm.ByType {
		probe := State{EntityID: strings.TrimPrefix(ar.ID, "@") + ".x"}
		return entityIcon(probe), entityColor(probe)
	}
	name := strings.ToLower(ar.Name)
	icon, color, at := "home", "blue", -1
	for _, l := range areaLooks {
		for _, w := range l.words {
			if i := strings.LastIndex(name, w); i >= 0 && i+len(w) > at {
				icon, color, at = l.icon, l.color, i+len(w)
			}
		}
	}
	return icon, color
}

// idMost is the most characters an action id, a key or a control's name has (the view schema's).
const idMost = 128

// ref is prefix and s when they fit idMost, else prefix and a digest of s: an entity id may be longer than an action
// id, a key or a name can be.
func ref(prefix, s string) string {
	if len(prefix)+len(s) <= idMost {
		return prefix + s
	}
	sum := sha256.Sum256([]byte(s))
	return prefix + "#" + hex.EncodeToString(sum[:12])
}

// openAction opens eid: open:<entity> when that fits an action id, else open with the entity in the action's values
// (UI version 4).
func openAction(eid string) *wire.Action {
	if id := "open:" + eid; len(id) <= idMost {
		return &wire.Action{ID: id}
	}
	return &wire.Action{ID: "open", Values: map[string]string{"entity": eid}}
}

// areaAction opens an area: area:<id>, or area with the id in the action's values (an id may be as long as a name).
func areaAction(id string) *wire.Action {
	if a := "area:" + id; len(a) <= idMost {
		return &wire.Action{ID: a}
	}
	return &wire.Action{ID: "area", Values: map[string]string{"area": id}}
}

// rowDo is a row's own call (rowCall): do:<entity>|<service>, or do with both in the action's values.
func rowDo(eid, svc string) *wire.Action {
	if id := "do:" + eid + "|" + svc; len(id) <= idMost {
		return &wire.Action{ID: id}
	}
	return &wire.Action{ID: "do", Values: map[string]string{"entity": eid, "service": svc}}
}
