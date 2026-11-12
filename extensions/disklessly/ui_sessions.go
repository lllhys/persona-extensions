package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The cold-session backup page: machine filter, cloud status, search over the local cold index
// (deep full-text search on demand), results with their location and restore. It is drawn in the
// main page's 会话与台账 tab and as its own nav page, Agent 对话.

const (
	sessionsUI      = "disklessly-sessions"
	allMachines     = "all"
	coldShown       = 50
	coldSearchHint  = "搜索会话：标题、首条消息、项目、关键词"
	coldOffloadText = "冷会话上云（读回校验后删本地）"
	coldRefreshText = "刷新云端索引与状态"
	coldDeepText    = "全文搜索"
	// coldQueryRunes keeps a query well under the 2000-character Search.value limit.
	coldQueryRunes = 200
)

// clipQuery trims a search query and cuts it to coldQueryRunes runes.
func clipQuery(q string) string {
	q = strings.TrimSpace(q)
	if r := []rune(q); len(r) > coldQueryRunes {
		q = strings.TrimSpace(string(r[:coldQueryRunes]))
	}
	return q
}

// sessPage is the Agent 对话 page state: the machine filter and the query only (16 KB limit).
type sessPage struct {
	Machine string `json:"m,omitempty"`
	Query   string `json:"q,omitempty"`
}

// deepResult carries a deep search's hits into one render; they are never stored in page state.
type deepResult struct {
	hits []ColdHit
	errs []string
}

func locationTone(loc string) string {
	switch loc {
	case LocCloud:
		return wire.TonePositive
	case LocMissing:
		return wire.ToneDanger
	case LocLocal:
		return wire.ToneNeutral
	}
	return wire.ToneCaution
}

// coldSections draws the cold backup status, actions and results for machine m (nil: all machines).
func (d draw) coldSections(m *Machine, query string, deep *deepResult) []wire.Section {
	query = clipQuery(query)
	machineArg := ""
	if m != nil {
		machineArg = m.ID
	}
	var machines []*Machine
	for _, mm := range d.st.Machines {
		if (m == nil && mm.Enabled) || (m != nil && mm.ID == m.ID) {
			machines = append(machines, mm)
		}
	}

	counts := map[string]int{}
	var total, indexed int
	var capped bool
	var lastCheck time.Time
	var backends []string
	var problems []string
	for _, mm := range machines {
		cat := d.st.cold(mm.ID)
		if cat == nil {
			continue
		}
		total += cat.Total
		indexed += len(cat.Entries)
		capped = capped || cat.Capped
		if cat.Backend.CheckedAt.After(lastCheck) {
			lastCheck = cat.Backend.CheckedAt
		}
		for _, e := range cat.Entries {
			counts[cat.location(e, mm, d.now)]++
		}
		name := cmp.Or(mm.Name, mm.ID)
		switch b := cat.Backend; {
		case b.Reachable == nil:
			backends = append(backends, name+" 未知")
		case *b.Reachable:
			backends = append(backends, fmt.Sprintf("%s 可达 %d ms", name, b.LatencyMs))
		default:
			backends = append(backends, name+" 不可达")
			problems = append(problems, fmt.Sprintf("%s：备份后端不可达（%s），云端位置显示为未知。", name, cmpOr(b.Error, "backend unreachable")))
		}
		if cat.Error != "" {
			problems = append(problems, name+"："+cat.Error)
		}
		if len(cat.Missing) > 0 {
			problems = append(problems, fmt.Sprintf("%s：备份后端缺少 %d 个归档分段，相关会话无法恢复。", name, len(cat.Missing)))
		}
		if len(cat.Unchecked) > 0 || cat.TimedOut {
			problems = append(problems, fmt.Sprintf("%s：%d 个归档分段未能确认，相关会话显示为未知。", name, len(cat.Unchecked)))
		}
	}

	backendStat := wire.Stat{Label: "备份后端", Value: "未检查", Detail: "刷新云端索引后显示", Tone: wire.ToneCaution}
	if len(backends) > 0 {
		backendStat = wire.Stat{Label: "备份后端", Value: fmt.Sprintf("%d 台机器", len(backends)), Detail: clip(strings.Join(backends, "；"), 80)}
		if counts[LocUnknown] == 0 && !strings.Contains(backendStat.Detail, "不可达") && !strings.Contains(backendStat.Detail, "未知") {
			backendStat.Tone = wire.TonePositive
		} else {
			backendStat.Tone = wire.ToneCaution
		}
	}
	checked := "从未"
	if !lastCheck.IsZero() {
		checked = d.when(lastCheck)
	}
	indexDetail := "最近检查 " + checked
	if capped {
		indexDetail = fmt.Sprintf("仅保留最新 %d 个（共 %d 个）· %s", indexed, total, indexDetail)
	}
	status := wire.Section{
		Title:  "云端会话备份",
		Footer: "位置只按最近 24 小时内的检查判断：后端不可达、未检查或机器离线时显示「未知」，从不当作已丢失。",
		Items: []wire.Component{{
			Type: wire.ComponentStatGrid,
			Stats: []wire.Stat{
				{Label: "已索引会话", Value: fmt.Sprintf("%d 个", indexed), Detail: indexDetail},
				{Label: LocCloud, Value: fmt.Sprintf("%d 个", counts[LocCloud]), Tone: toneIf(counts[LocCloud] > 0, wire.TonePositive)},
				{Label: "未知 / 云端缺失", Value: fmt.Sprintf("%d / %d", counts[LocUnknown], counts[LocMissing]),
					Tone: cmp.Or(toneIf(counts[LocMissing] > 0, wire.ToneDanger), toneIf(counts[LocUnknown] > 0, wire.ToneCaution))},
				backendStat,
			},
		}},
	}
	for _, t := range problems[:min(len(problems), 4)] {
		status.Items = append(status.Items, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: clip(t, 200)})
	}
	if m != nil {
		status.Items = append(status.Items, wire.Component{
			Type:   wire.ComponentButton,
			Title:  coldOffloadText,
			Action: &wire.Action{ID: "cold-offload", Values: map[string]string{"m": m.ID}, Confirm: sessionOffloadConfirm(m)},
		})
	}
	status.Items = append(status.Items, wire.Component{
		Type: wire.ComponentButton, Title: coldRefreshText,
		Action: &wire.Action{ID: "cold-refresh", Values: map[string]string{"m": machineArg}},
	})
	if strings.TrimSpace(query) != "" {
		status.Items = append(status.Items, wire.Component{
			Type: wire.ComponentButton, Title: coldDeepText,
			Action: &wire.Action{ID: "cold-deep", Values: map[string]string{"m": machineArg, "q": query}},
		})
	}

	var hits []ColdHit
	title := "最近归档的会话"
	if deep != nil {
		hits = deep.hits
		title = fmt.Sprintf("全文搜索「%s」（%d 个）", clip(query, 24), len(hits))
	} else {
		hits = SearchCold(d.st, query, machineArg, 0, d.now)
		if strings.TrimSpace(query) != "" {
			title = fmt.Sprintf("搜索「%s」（%d 个）", clip(query, 24), len(hits))
		}
	}
	results := wire.Section{Title: title}
	if deep != nil {
		for _, e := range deep.errs[:min(len(deep.errs), 4)] {
			results.Items = append(results.Items, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: clip("全文搜索未完成："+e, 200)})
		}
	}
	shown := hits[:min(len(hits), coldShown)]
	for _, h := range shown {
		results.Items = append(results.Items, d.coldRow(h))
	}
	var notes []string
	if len(hits) > len(shown) {
		notes = append(notes, fmt.Sprintf("显示最新 %d 个，共 %d 个；输入更多关键词缩小范围。", len(shown), len(hits)))
	}
	if capped {
		notes = append(notes, fmt.Sprintf("本地索引每台机器只保留最新 %d 个会话，更早的请用「全文搜索」。", maxColdEntries))
	}
	results.Footer = strings.Join(notes, " ")
	if len(shown) == 0 {
		detail := "扫描机器或点击「" + coldRefreshText + "」读取 disklessly 的会话索引。"
		if strings.TrimSpace(query) != "" {
			detail = "本地索引没有匹配的会话；可试试「" + coldDeepText + "」在机器上搜索完整摘要。"
		}
		results.Items = append(results.Items, wire.Component{Type: wire.ComponentEmpty, Icon: "chat", Title: "没有会话", Detail: detail})
	}
	return []wire.Section{status, results}
}

func (d draw) coldRow(h ColdHit) wire.Component {
	sub := []string{h.MachineName, cmp.Or(toolLabel(h.Tool), "Agent")}
	if p := cmp.Or(h.Project, lastPathElem(h.Cwd)); p != "" {
		sub = append(sub, p)
	}
	if t := entryTime(h.ColdEntry); !t.IsZero() {
		sub = append(sub, t.In(d.loc).Format(time.DateOnly))
	}
	if !h.Local {
		sub = append(sub, "来自其他设备")
	}
	row := wire.Component{
		Type:     wire.ComponentRow,
		Key:      "cold-" + h.MachineID + "-" + h.ID,
		Icon:     "chat",
		Title:    clip(cmp.Or(h.Title, h.FirstPrompt, h.SessionID, h.ID), 120),
		Subtitle: strings.Join(sub, " · "),
		Detail:   clip(cmp.Or(h.Preview, h.FirstPrompt), 240),
		Badge:    &wire.Badge{Text: h.Location, Tone: locationTone(h.Location)},
	}
	// Another device's row is not restored here: its original path belongs to that device.
	if h.Location != LocLocal && h.Local {
		confirm := fmt.Sprintf("从云端恢复到原路径 %s？", cmp.Or(h.Path, "（原路径）"))
		if hint := resumeHint(h.ColdEntry); hint != "" {
			confirm += "恢复后可运行 " + hint
		}
		row.Actions = []wire.RowAction{{Title: "恢复", Swipe: true, Action: wire.Action{
			ID:      "cold-restore",
			Values:  map[string]string{"m": h.MachineID, "id": h.ID},
			Confirm: confirm,
		}}}
	}
	return row
}

func lastPathElem(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// coldAction runs a cold-session action that changes state. machine "" or "all" means every machine.
func (a *app) coldAction(ctx context.Context, action, machine, id string) (string, error) {
	runner := a.run()
	var toast string
	err := a.db().update(func(st *State, now time.Time) error {
		pick := func() (*Machine, error) {
			m := st.machine(cmp.Or(machine, "local"))
			if m == nil || machine == allMachines {
				return nil, fmt.Errorf("找不到机器 %q", machine)
			}
			return m, nil
		}
		switch action {
		case "cold-refresh":
			var ms []*Machine
			if machine == "" || machine == allMachines {
				for _, m := range st.Machines {
					if m.Enabled {
						ms = append(ms, m)
					}
				}
			} else if m, err := pick(); err != nil {
				return err
			} else {
				ms = []*Machine{m}
			}
			var ok, n int
			var errs []string
			for _, m := range ms {
				cat, err := RefreshColdIndex(ctx, runner, st, m, now)
				if err != nil {
					errs = append(errs, cmp.Or(m.Name, m.ID)+"："+err.Error())
					continue
				}
				ok++
				n += len(cat.Entries)
			}
			toast = fmt.Sprintf("已刷新 %d 台机器的云端索引（%d 个会话）", ok, n)
			if len(errs) > 0 {
				toast += "；" + clip(strings.Join(errs, "；"), 120)
			}
		case "cold-offload", "archive-sessions":
			m, err := pick()
			if err != nil {
				return err
			}
			_, rec, err := OffloadSessions(ctx, runner, st, m, false, nil, now)
			if err != nil {
				return err
			}
			toast = "冷会话已上云：" + rec.Summary
		case "cold-restore":
			m, err := pick()
			if err != nil {
				return err
			}
			e, hint, err := RestoreCold(ctx, runner, st, m, id, now)
			if err != nil {
				return err
			}
			toast = "已恢复到 " + e.Path
			if hint != "" {
				toast += "，可运行 " + hint
			}
		default:
			return fmt.Errorf("unknown action %q", action)
		}
		return nil
	})
	return toast, err
}

// coldDeep runs a deep search; it reads the state only.
func (a *app) coldDeep(ctx context.Context, machine, query string) (*deepResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("请输入要全文搜索的内容")
	}
	if machine == allMachines {
		machine = ""
	}
	runner := a.run()
	var res deepResult
	err := a.db().read(func(st *State) {
		res.hits, res.errs = DeepSearchCold(ctx, runner, st, query, machine, coldShown, a.now())
	})
	return &res, err
}

// ---- Agent 对话 nav page ----

func (a *app) renderSessions(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	var p sessPage
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	return a.sessionsView(r, p, nil)
}

func (a *app) sessionsView(r *ext.UIRequest, p sessPage, deep *deepResult) (*wire.View, error) {
	var v *wire.View
	err := a.db().read(func(st *State) { v = a.drawer(st, r).sessionsPage(p, deep) })
	return v, err
}

func (d draw) sessionsPage(p sessPage, deep *deepResult) *wire.View {
	var m *Machine
	if p.Machine != "" && p.Machine != allMachines {
		if m = d.st.machine(p.Machine); m == nil || !m.Enabled {
			m, p.Machine = nil, ""
		}
	}
	choices := []wire.Choice{{Value: allMachines, Title: "全部机器"}}
	for _, mm := range d.st.Machines {
		if mm.Enabled {
			choices = append(choices, wire.Choice{Value: mm.ID, Title: clip(cmp.Or(mm.Name, mm.Host), 28)})
		}
	}
	value := allMachines
	if m != nil {
		value = m.ID
	}
	return &wire.View{
		Title:    "Agent 对话",
		State:    ext.EncodeState(p),
		Search:   &wire.Search{Placeholder: coldSearchHint, Value: clipQuery(p.Query), Action: wire.Action{ID: "search"}},
		Filter:   &wire.Filter{Name: "machine", Title: "机器", Choices: choices, Value: value, Action: wire.Action{ID: "machine"}},
		Sections: d.coldSections(m, p.Query, deep),
	}
}

func (a *app) actionSessions(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var p sessPage
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	res := &wire.ActionResult{}
	var deep *deepResult
	switch r.Action {
	case "machine":
		p.Machine = r.Values["machine"]
	case "search":
		p.Query = clipQuery(r.Values["q"])
	case "cold-deep":
		p.Query = clipQuery(cmp.Or(strings.TrimSpace(r.Values["q"]), p.Query))
		var err error
		if deep, err = a.coldDeep(ctx, cmp.Or(r.Values["m"], p.Machine), p.Query); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: err.Error()}
		}
	default:
		toast, err := a.coldAction(ctx, r.Action, cmp.Or(r.Values["m"], p.Machine), r.Values["id"])
		if err != nil {
			return nil, &ext.Error{Code: "invalid", Message: err.Error()}
		}
		res.Toast = toast
	}
	var err error
	res.View, err = a.sessionsView(r, p, deep)
	return res, err
}
