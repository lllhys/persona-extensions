package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// This file is the page's presentation: it turns the home, the page's state and the asking app into a wire.View.
// The data (states, areas, history, the logbook) comes from the helpers in page.go and the connector.

// renderTabs is 家 / 动态 / 自动化, with how many alarms there are on 家.
func renderTabs(r *ext.UIRequest, st pageState, v *wire.View, alarms int) {
	cur := st.Tab
	if cur == "" {
		cur = "home"
	}
	switch {
	case r.UIVersion >= 4:
		t := &wire.Tabs{Name: "tab", Value: cur, Action: wire.Action{ID: "tab"}}
		for _, c := range tabs {
			tab := wire.Tab{Value: c.Value, Title: c.Title}
			if c.Value == "home" && alarms > 0 {
				tab.Badge = strconv.Itoa(min(alarms, 99))
			}
			t.Items = append(t.Items, tab)
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

// alarmCount is how many alarms the overview draws.
func alarmCount(hm *home) int {
	n := 0
	for _, s := range hm.States {
		if stateTone(s) == "danger" && !hm.Hidden[s.EntityID] {
			n++
		}
	}
	return n
}

// homePage is 家's overview: the home's figures, its alarms, then every area, the busiest first. An area says what
// is on in it, its badge how many, and its value the temperature (or humidity) a room is.
func (a *app) homePage(r *ext.UIRequest, hm *home, reg *Registry, v *wire.View) {
	var alarms []wire.Component
	for _, s := range hm.States {
		if stateTone(s) == "danger" && !hm.Hidden[s.EntityID] {
			alarms = append(alarms, entityRow(hm, s, true))
		}
	}

	type room struct {
		ar  Area
		sum areaSummary
	}
	rooms := make([]room, 0, len(hm.Areas))
	for i := range hm.Areas {
		rooms = append(rooms, room{ar: hm.Areas[i], sum: roomSummary(hm, reg, hm.Areas[i])})
	}
	if len(alarms) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: fmt.Sprintf("报警 · %d", len(alarms)), Items: alarms})
	}
	if hm.Note != "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: hm.Note, Style: "secondary"}}})
	}
	sort.SliceStable(rooms, func(i, j int) bool {
		if rooms[i].sum.on != rooms[j].sum.on {
			return rooms[i].sum.on > rooms[j].sum.on
		}
		return rooms[i].ar.Name < rooms[j].ar.Name
	})
	title := fmt.Sprintf("区域 · %d", len(rooms))
	if hm.ByType {
		title = fmt.Sprintf("类型 · %d", len(rooms))
	}
	sec := wire.Section{Title: title}
	for _, x := range rooms {
		row := wire.Component{Type: "row", Key: "area:" + x.ar.ID, Title: x.ar.Name,
			Subtitle: clip(onNames(hm, x.sum), 120), Value: areaEnv(x.sum), Action: &wire.Action{ID: "area:" + x.ar.ID}}
		if x.sum.on > 0 {
			row.Badge = &wire.Badge{Text: fmt.Sprintf("%d 开着", x.sum.on), Tone: "positive"}
		}
		sec.Items = append(sec.Items, row)
	}
	if len(rooms) == 0 {
		sec.Items = append(sec.Items, emptyItem(r, "还没有区域", "用上面的搜索找设备"))
	}
	v.Sections = append(v.Sections, sec, footer())
}

// onNames says what is on in an area, by name; a climate with its mode.
func onNames(hm *home, sum areaSummary) string {
	var names []string
	for _, s := range sum.notable {
		if stateTone(s) != "positive" {
			continue
		}
		n := shortName(hm, s)
		if s.Domain() == "climate" {
			n += "（" + stateText(s) + "）"
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "都关着"
	}
	if len(names) > 2 {
		names = names[:2]
	}
	return strings.Join(names, "、")
}

// areaEnv is an area's right-hand value: its air, when it has sensors for it.
func areaEnv(sum areaSummary) string {
	var parts []string
	if len(sum.temp) > 0 {
		parts = append(parts, tidy(avg(sum.temp))+"°")
	}
	if len(sum.humid) > 0 {
		parts = append(parts, tidy(avg(sum.humid))+"%")
	}
	return strings.Join(parts, " · ")
}

// roomPage is one area: a header with its figures, then its devices (a section per kind), sensors or everything.
func (a *app) roomPage(r *ext.UIRequest, hm *home, reg *Registry, st pageState, v *wire.View) {
	ar := hm.areaByID(st.Area)
	if ar == nil {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: errNoEntity.Error(), Style: "secondary"}}})
		return
	}
	v.Title = ar.Name
	sum := roomSummary(hm, reg, *ar)
	if r.UIVersion >= 2 {
		// the room's air first: that is what one opens a room for; the devices' state follows it
		var stats []wire.Stat
		if len(sum.temp) > 0 {
			stats = append(stats, wire.Stat{Label: "温度", Value: tidy(avg(sum.temp)) + "°"})
		}
		if len(sum.humid) > 0 {
			stats = append(stats, wire.Stat{Label: "湿度", Value: tidy(avg(sum.humid)) + "%"})
		}
		if len(stats) == 0 {
			stats = append(stats, wire.Stat{Label: "设备", Value: strconv.Itoa(sum.devices)})
		}
		stats = append(stats, wire.Stat{Label: "开着", Value: strconv.Itoa(sum.on), Tone: toneWhen(sum.on > 0, "positive")})
		if sum.offline > 0 {
			stats = append(stats, wire.Stat{Label: "离线", Value: strconv.Itoa(sum.offline), Tone: "caution"})
		}
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "stat_grid", Stats: stats}}})
	} else {
		v.Sections = append(v.Sections, crumbs(r, []wire.Crumb{{Title: "家", Action: &wire.Action{ID: "home"}}, {Title: ar.Name}}, "home"))
		line := roomLine(sum)
		if env := areaEnv(sum); env != "" {
			line += " · " + env
		}
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: line, Style: "secondary"}}})
	}
	kindControl(r, st, v)
	if st.Kind == "devices" {
		if r.UIVersion >= 2 && a.hasQuick(hm, reg, ar) {
			pick := wire.Component{Type: "segmented", Name: "pick", Value: "toggle", Action: &wire.Action{ID: "pick"},
				Choices: []wire.Choice{{Value: "toggle", Title: "点按开关"}, {Value: "open", Title: "点按查看"}}}
			if st.Pick {
				pick.Value = "open"
			}
			v.Sections = append(v.Sections, wire.Section{Title: "点按设备", Items: []wire.Component{pick}})
		}
		a.roomDevices(r, hm, reg, ar, st, v)
	} else {
		a.roomEntities(r, hm, reg, ar, st, v)
	}
	v.Sections = append(v.Sections, footer())
}

// roomSummary is an area's figures as its page draws them: a device's main entities (its settings folded away),
// what is on and offline, and the room's temperature and humidity.
func roomSummary(hm *home, reg *Registry, ar Area) areaSummary {
	sum := summarizeArea(hm, ar) // its air
	sum.devices, sum.on, sum.offline, sum.notable = 0, 0, 0, nil
	for _, dr := range deviceRows(hm, reg, &ar) {
		sum.devices++
		if dr.s.Unavailable() {
			sum.offline++
			continue
		}
		if stateTone(dr.s) == "positive" {
			sum.on++
			if len(sum.notable) < 12 {
				sum.notable = append(sum.notable, dr.s)
			}
		}
	}
	return sum
}

// roomLine is a room's one-line summary under its name.
func roomLine(sum areaSummary) string { return fmt.Sprintf("%d 个设备", sum.devices) }

// kindControl switches an area between its devices, its sensors and everything.
func kindControl(r *ext.UIRequest, st pageState, v *wire.View) {
	if r.UIVersion >= 2 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "segmented", Name: "kind", Value: st.Kind,
			Choices: kinds, Action: &wire.Action{ID: "kind"}}}})
		return
	}
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

// hasQuick says whether the room has a device whose row can turn it on or off: only then does the tap mode matter.
func (a *app) hasQuick(hm *home, reg *Registry, ar *Area) bool {
	for _, dr := range deviceRows(hm, reg, ar) {
		s := dr.s
		if !slices.Contains(quickDomains, s.Domain()) || (s.State != "on" && s.State != "off") || a.canWrite(s.Domain(), false) != nil {
			continue
		}
		return true
	}
	return false
}

// roomDevices is an area's devices, a section per kind (灯, 开关, 空调…): a light's or a switch's row turns it on or
// off (st.Pick: opens it), the rest open.
func (a *app) roomDevices(r *ext.UIRequest, hm *home, reg *Registry, ar *Area, st pageState, v *wire.View) {
	var shown []devRow
	offline := 0
	for _, dr := range deviceRows(hm, reg, ar) {
		if dr.s.Unavailable() && !st.Offline {
			offline++
			continue
		}
		shown = append(shown, dr)
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
		for _, dr := range shown[i:j] {
			if n == rowsMost {
				sec.Footer = fmt.Sprintf("只列前 %d 个，用搜索找别的", rowsMost)
				break
			}
			row, q := a.deviceRow(hm, dr, st.Pick)
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
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{emptyItem(r, "这里没有设备", "看看「传感器」「全部」，或者搜索")}})
	}
	more := offlineRow(offline, st.Offline)
	if r.UIVersion < 2 { // before the segmented control, the mode is a row
		switch {
		case st.Pick:
			more = append(more, wire.Component{Type: "row", Title: "改回点一下就开关", Subtitle: "现在点设备是打开它", Action: &wire.Action{ID: "pick"}})
		case quick:
			more = append(more, wire.Component{Type: "row", Title: "调亮度、看设备的设置", Subtitle: "点这里，再点设备就是打开它", Action: &wire.Action{ID: "pick"}})
		}
	}
	if len(more) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: more})
	}
}

// roomEntities is an area's sensors or everything, one row each.
func (a *app) roomEntities(r *ext.UIRequest, hm *home, reg *Registry, ar *Area, st pageState, v *wire.View) {
	var shown []State
	offline, diag := 0, 0
	for _, e := range ar.Entities {
		s, ok := hm.ByID[e]
		if !ok || !inKind(st.Kind, s) {
			continue
		}
		if st.Kind == "sensors" && reg != nil && reg.Entities[e].EntityCategory == "diagnostic" {
			diag++
			continue
		}
		if s.Unavailable() && !st.Offline {
			offline++
			continue
		}
		shown = append(shown, s)
	}
	sort.SliceStable(shown, func(i, j int) bool {
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
		sec.Items = append(sec.Items, emptyItem(r, "这里没有"+kindTitle(st.Kind), "用上面的搜索找别的"))
	}
	if diag > 0 && sec.Footer == "" {
		sec.Footer = fmt.Sprintf("另有 %d 个诊断项（信号、IP 之类）在「全部」里", diag)
	}
	sec.Items = append(sec.Items, offlineRow(offline, st.Offline)...)
	v.Sections = append(v.Sections, sec)
}

// searchPage finds any entity by name, id, area or kind.
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
		sec.Items = append(sec.Items, emptyItem(r, "没找到对得上的", "换个名字、区域或类型试试"))
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

// entityPage is one entity: a header, its controls (and the progress bars of what it can dim or move), the rest of
// its device, its last 24 hours and its attributes.
func (a *app) entityPage(ctx context.Context, r *ext.UIRequest, hm *home, reg *Registry, st *pageState, v *wire.View) {
	v.RefreshAfter = refreshEntity
	s, ok := hm.ByID[st.Entity]
	if !ok || !hm.visible(st.Entity) {
		v.Sections = []wire.Section{crumbs(r, entityPath(hm, st, st.Entity), "back"),
			{Items: []wire.Component{{Type: "text", Text: errNoEntity.Error(), Style: "secondary"}}}}
		return
	}
	v.Title = clip(s.Name(), 60)
	if r.UIVersion >= 4 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{entityHeader(hm, s, a.loc())}})
	} else {
		v.Sections = append(v.Sections, crumbs(r, entityPath(hm, st, clip(shortName(hm, s), 40)), "back"))
		facts := []string{domainName(s.Domain())}
		if n := hm.areaName(s.EntityID); n != "" {
			facts = append(facts, n)
		}
		facts = append(facts, "变于 "+s.LastChanged.In(a.loc()).Format("01-02 15:04"))
		head := wire.Section{}
		head.Items = append(head.Items,
			wire.Component{Type: "metric", Label: "状态", Value: orDash(stateText(s)), Detail: detail(s), Tone: stateTone(s)},
			wire.Component{Type: "text", Text: strings.Join(facts, " · ") + "\n" + s.EntityID, Style: "caption"})
		v.Sections = append(v.Sections, head)
	}

	if ctl := a.controlSection(r, s); len(ctl.Items) > 0 {
		if p := progressItems(s); len(p) > 0 {
			ctl.Items = append(p, ctl.Items...)
		}
		v.Sections = append(v.Sections, ctl)
	} else if p := progressItems(s); len(p) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: "状态", Items: p})
	}

	if sec, ok := deviceSection(hm, reg, s, st.Sub); ok {
		v.Sections = append(v.Sections, sec)
	}
	if hist := a.historySection(ctx, s); len(hist.Items) > 0 {
		v.Sections = append(v.Sections, hist)
	}

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

// entityHeader is an entity's head: its name, what it is, its state and where it belongs.
func entityHeader(hm *home, s State, loc *time.Location) wire.Component {
	h := wire.Component{Type: "header", Title: clip(s.Name(), 80), Subtitle: detail(s)}
	if t := stateText(s); t != "" {
		h.Badges = append(h.Badges, wire.Badge{Text: clip(t, 12), Tone: stateTone(s)})
	}
	h.Facts = append(h.Facts, wire.Fact{Label: "类型", Value: domainName(s.Domain())})
	if n := hm.areaName(s.EntityID); n != "" {
		h.Facts = append(h.Facts, wire.Fact{Label: "区域", Value: n})
	}
	h.Facts = append(h.Facts, wire.Fact{Label: "变更", Value: s.LastChanged.In(loc).Format("01-02 15:04")})
	return h
}

// progressItems are the entity's dials: a light's brightness, a cover's position, a player's volume.
func progressItems(s State) []wire.Component {
	var out []wire.Component
	add := func(key, label string, ratio float64, detail string) {
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		out = append(out, wire.Component{Type: "progress", Key: "progress-" + key, Label: label, Ratio: &ratio, Detail: detail})
	}
	switch s.Domain() {
	case "light":
		if b, ok := s.num("brightness"); ok && b > 0 {
			add("brightness", "亮度", b/255, fmt.Sprintf("%.0f%%", b/255*100))
		}
	case "fan":
		if p, ok := s.num("percentage"); ok && p > 0 {
			add("percentage", "风速", p/100, fmt.Sprintf("%.0f%%", p))
		}
	case "cover", "valve":
		if p, ok := s.num("current_position"); ok {
			add("position", "开度", p/100, fmt.Sprintf("%.0f%%", p))
		}
	case "media_player":
		if v, ok := s.num("volume_level"); ok && v > 0 {
			add("volume", "音量", v, fmt.Sprintf("%.0f%%", v*100))
		}
	}
	return out
}

// logPage is 动态: the logbook of the last logHours, a section a day, newest first; an entity that changed more
// than noisyMost times is one row.
func (a *app) logPage(ctx context.Context, r *ext.UIRequest, hm *home, reg *Registry, st pageState, v *wire.View) {
	v.Title = "动态"
	if r.UIVersion >= 2 {
		which := "devices"
		if st.LogAll {
			which = "all"
		}
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "segmented", Name: "log", Value: which,
			Choices: []wire.Choice{{Value: "devices", Title: "设备"}, {Value: "all", Title: "全部"}},
			Action:  &wire.Action{ID: "log"}}}})
	} else {
		title := "看全部变化"
		if st.LogAll {
			title = "只看设备"
		}
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "row", Title: title, Action: &wire.Action{ID: "log"}}}})
	}
	h, _, _ := a.client()
	to := time.Now()
	entries, err := h.Logbook(ctx, to.Add(-logHours*time.Hour), to, "")
	if err != nil {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: "text", Text: "读不到日志：" + err.Error(), Style: "secondary"}}}, footer())
		return
	}
	if !st.LogAll {
		entries = onlyDeviceLog(hm, entries)
	}
	kept, count := keepLog(hm, withoutDiagnostics(hm, reg, entries), nil)
	loc := a.loc()
	now := time.Now().In(loc)

	type day struct {
		key, title string
		items      []wire.Component
	}
	var days []*day
	shown := map[string]bool{}
	rows := 0
	for i := len(kept) - 1; i >= 0; i-- {
		e := kept[i]
		if rows == logRows {
			break
		}
		at := e.When.In(loc)
		what, where, by := logParts(hm, e)
		sub := []string{}
		if n := count[e.EntityID]; e.EntityID != "" && n > logPageNoisy {
			if shown[e.EntityID] {
				continue
			}
			shown[e.EntityID] = true
			what = fmt.Sprintf("%s 变化了 %d 次", logName(hm, e), n)
			by = "最后是" + orDash(entryStateText(hm, e))
		}
		for _, p := range []string{where, by} {
			if p != "" {
				sub = append(sub, p)
			}
		}
		row := wire.Component{Type: "row", Title: clip(what, 80), Subtitle: clip(strings.Join(sub, " · "), 120),
			Value: at.Format("15:04")}
		if _, ok := hm.ByID[e.EntityID]; ok {
			row.Action = &wire.Action{ID: "open:" + e.EntityID}
		}
		key := at.Format("2006-01-02")
		var d *day
		for _, x := range days {
			if x.key == key {
				d = x
			}
		}
		if d == nil {
			d = &day{key: key, title: dayTitle(at, now)}
			days = append(days, d)
		}
		d.items = append(d.items, row)
		rows++
	}
	if rows == 0 {
		what := fmt.Sprintf("最近 %d 小时家里没有变化", logHours)
		if !st.LogAll {
			what = fmt.Sprintf("最近 %d 小时设备没有变化", logHours)
		}
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{emptyItem(r, what, "切到「全部」看看传感器和人")}}, footer())
		return
	}
	footerText := ""
	if rows == logRows {
		footerText = fmt.Sprintf("只列最近 %d 条；更早的、某个区域的，问助手", logRows)
	}
	for i, d := range days {
		sec := wire.Section{Title: fmt.Sprintf("%s · %d", d.title, len(d.items)), Items: d.items}
		if i == len(days)-1 {
			sec.Footer = footerText
		}
		v.Sections = append(v.Sections, sec)
	}
	v.Sections = append(v.Sections, footer())
}

// onlyDeviceLog keeps the changes a person makes or sees at home: lights, switches, covers, locks, climates,
// players, automations and scenes. Sensors, people and trackers are for the 全部 choice.
func onlyDeviceLog(hm *home, entries []LogEntry) []LogEntry {
	out := make([]LogEntry, 0, len(entries))
	for _, e := range entries {
		if e.EntityID == "" { // the home itself starting, a service answering
			out = append(out, e)
			continue
		}
		s, ok := hm.ByID[e.EntityID]
		if !ok {
			continue
		}
		d := s.Domain()
		if slices.Contains(deviceDomains, d) || d == "automation" || d == "button" || d == "input_button" {
			out = append(out, e)
		}
	}
	return out
}

// withoutDiagnostics leaves out what is not something happening in the home: a signal strength, an uptime, a clock
// sensor or a template helper changing every minute. The registry's diagnostic category is the first filter; the
// rest are the names Home Assistant's own helpers carry.
func withoutDiagnostics(hm *home, reg *Registry, entries []LogEntry) []LogEntry {
	out := make([]LogEntry, 0, len(entries))
	for _, e := range entries {
		if noisyLog(hm, reg, e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func noisyLog(hm *home, reg *Registry, e LogEntry) bool {
	if reg != nil {
		if re, ok := reg.Entities[e.EntityID]; ok && re.EntityCategory == "diagnostic" {
			return true
		}
	}
	s, ok := hm.ByID[e.EntityID]
	name := strings.TrimSpace(e.Name)
	if ok {
		name = strings.TrimSpace(s.Name())
	} else if name == "" {
		return false
	}
	switch name {
	case "时间", "日期", "日期和时间", "日期时间":
		return true
	}
	if strings.Contains(name, "自定义属性") || strings.Contains(name, "调试") {
		return true
	}
	if s.Domain() == "person" || s.Domain() == "device_tracker" {
		if _, err := time.Parse(time.RFC3339Nano, e.State); err == nil {
			return true
		}
	}
	if s.Domain() != "sensor" {
		return false
	}
	if strings.Contains(name, "时间") || strings.Contains(name, "日期") {
		return true
	}
	switch s.attr("device_class") {
	case "timestamp", "duration":
		return true
	}
	id := strings.ToLower(e.EntityID)
	for _, x := range []string{"sensor.time", "sensor.date", "date_time", "uptime", "_runtime", "_time"} {
		if strings.Contains(id, x) {
			return true
		}
	}
	return false
}

// dayTitle is a log day's section title: 今天, 昨天, else its date and weekday.
func dayTitle(day time.Time, now time.Time) string {
	y, m, d := day.Date()
	if ny, nm, nd := now.Date(); y == ny && m == nm && d == nd {
		return "今天"
	}
	if yy, ym, yd := now.AddDate(0, 0, -1).Date(); y == yy && m == ym && d == yd {
		return "昨天"
	}
	week := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return day.Format("01-02") + " " + week[day.Weekday()]
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
			"在 Home Assistant 里建，或者让助手帮你写一个")}})
	}
	v.Sections = append(v.Sections, footer())
}

// autoRow is an automation, script or scene: its state, when it last ran, and the tap its kind allows.
func (a *app) autoRow(s State, loc *time.Location) wire.Component {
	row := wire.Component{Type: "row", Key: s.EntityID, Title: clip(s.Name(), 80), Action: &wire.Action{ID: "open:" + s.EntityID}}
	last := s.attr("last_triggered")
	if s.Domain() == "scene" {
		last = s.State
	}
	if at, err := time.Parse(time.RFC3339Nano, last); err == nil {
		row.Value = at.In(loc).Format("01-02 15:04")
	}
	switch s.Domain() {
	case "script":
		row.Subtitle = "点一下运行"
	case "scene":
		row.Subtitle = "点一下执行"
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

// toneWhen is a stat's tone only when it says something (a zero does not need a color).
func toneWhen(on bool, tone string) string {
	if on {
		return tone
	}
	return ""
}
