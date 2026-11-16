package main

// The switches page (design §8): K1…K17, each with its value, who set it (the user, or automatically and why) and
// its default. K13 has one switch per sensitive category, which only the user turns on; K2 one per fork type; K16
// one per message source.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type switchInfo struct {
	key            string
	zh, en         string // what it is
	zhNote, enNote string // what moves it
}

var switchTable = []switchInfo{
	{entry.K1PerTurnBlock, "每轮说明块整块（出口 A）", "The per-turn block as a whole (exit A)",
		"纠正数比上线前两周升高 20% 以上，或你说「乱了」，整块关掉再查", "Off when corrections rise 20% over the two weeks before, or when you say it got worse"},
	{entry.K3Abstract, "抽象条目进每轮说明块", "Abstract entries in the per-turn block",
		"抽象条目的空话率 >10%，或你看到的「对」少于 70%，就关，留在这个页面上", "Off when over 10% of them are empty words or under 70% are marked right; they stay on this page"},
	{entry.K4Persona, "「关于你」进说明块和收件箱", "\"About you\" in the block and the inbox",
		"同一条被你纠正 2 次以上，那一条撤出", "A line you corrected twice is withdrawn"},
	{entry.K5ToolsSkill, "技能「机器与工具手册」", "Skill \"machines and tools\"", "", ""},
	{entry.K6WorkflowSkill, "技能「做事方式」和 Agent 项目背景", "Skill \"ways of working\" and agent project context", "", ""},
	{entry.K7WorldFacts, "关于别人的事实写回知识库", "Facts about others written back to the knowledge base",
		"每周抽 30 条群聊来源的新条目，噪声 >10% 就关群聊分支", "30 new lines from group chats are checked weekly; over 10% noise turns the group-chat branch off"},
	{entry.K7GroupByProject, "群聊里的事实按项目名放回", "Group-chat facts filed by project name", "", ""},
	{entry.K8AutoEffective, "新条目自动生效（周报告诉你，可撤销）", "New entries take effect at once (the weekly report says so; undoable)",
		"两周内被撤销的比例 >30%，改成「只进周报，你点了才生效」", "Over 30% undone in two weeks: only in the report until you accept"},
	{entry.K9FixedFields, "把要靠判断的要求改写成固定字段", "Judgement-based asks rewritten as fixed fields",
		"只有对照显示对你有效时才开", "On only when a comparison shows it helps you"},
	{entry.K10Knowledge, "插件自己的一般性知识输出", "The curator's own general knowledge output",
		"知识库主题核对里「找不到」超过一半就开", "On when over half of knowledge-base topic lookups find nothing"},
	{entry.K11Recordings, "录音作为来源", "Recordings as a source",
		"抽 20 条，噪声 >10%，或混进私人内容 1 条，就关", "Off when 20 sampled have over 10% noise or one private line"},
	{entry.K12Onboarding, "首次引导（只出现一次，可跳过）", "First-run questions (once, skippable)", "", ""},
	{entry.K14People, "画像卡整块", "People cards as a whole",
		"画像卡「对」<70%，或两周删卡比例 >30%，只留在这个页面上", "Kept on this page only when under 70% right or over 30% deleted in two weeks"},
	{entry.K15Inferred, "画像卡里的推断侧写", "Inferred profiles on the cards",
		"推断被改或删的比例 >30%，关掉，只留事实、你的原话和承诺", "Off when over 30% of inferred lines are edited or deleted"},
}

// whoSet says who set a switch and why.
func whoSet(t tr, set map[string]entry.SwitchSetting, key string) string {
	s, ok := set[key]
	if !ok || s.Value == "" {
		return t.s("默认", "default")
	}
	at := s.At
	if len(at) > 10 {
		at = at[:10]
	}
	switch s.By {
	case "user":
		return t.f("你设的（%s）", "set by you (%s)", at)
	case "auto":
		why := s.Why
		if why == "" {
			why = t.s("读数到了门槛", "a reading crossed its threshold")
		}
		return t.f("自动（%s）：%s", "automatic (%s): %s", at, why)
	}
	return s.By
}

func onOff(t tr, v string) string {
	switch v {
	case "on":
		return t.s("开", "on")
	case "off":
		return t.s("关", "off")
	case "suggest":
		return t.s("只在请示时附建议", "suggest when asking")
	case "act":
		return t.s("可撤回的直接按建议办", "act on reversible ones")
	case "signal":
		return t.s("只挖被信号触发的片段", "only segments a signal points at")
	case "wide":
		return t.s("放宽初筛", "looser screen")
	}
	return v
}

func switchRow(t tr, set map[string]entry.SwitchSetting, key, title, note string) []wire.Component {
	cur := entry.Switch(set, key)
	def := entry.Defaults[key]
	if def == "" {
		base, _, _ := strings.Cut(key, ".")
		def = entry.Defaults[base]
	}
	sub := t.f("当前：%s · 默认：%s", "now: %s · default: %s", onOff(t, cur), onOff(t, def))
	out := []wire.Component{{Type: wire.ComponentRow, Key: "k:" + key, Title: key + " " + title, Subtitle: sub,
		Control: &wire.RowControl{Kind: "switch", Name: "sw:" + key, On: cur == "on", Action: wire.Action{ID: "switch"}}}}
	for _, c := range []string{whoSet(t, set, key), note} {
		if c != "" {
			out = append(out, caption(c))
		}
	}
	return out
}

func caption(s string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s, Style: wire.TextStyleCaption}
}

// forkTypes are the fork types K2 has a switch for: the judgement type table's and those the entries use.
func forkTypes(st *store.State) []string {
	ts := typesOf(st, entry.Judgement)
	for k := range st.Switches {
		if base, typ, ok := strings.Cut(k, "."); ok && base == entry.K2Judgement {
			ts[typ] += 0
		}
	}
	var out []string
	for k := range ts {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if ts[out[i]] != ts[out[j]] {
			return ts[out[i]] > ts[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// latestReading is the latest reading of a code.
func latestReading(st *store.State, code string) (entry.Reading, bool) {
	var best entry.Reading
	ok := false
	for _, r := range st.Readings {
		if r.Code == code && (!ok || r.Period > best.Period) {
			best, ok = r, true
		}
	}
	return best, ok
}

func (a *app) switchesPage(t tr, st *store.State) []wire.Section {
	set := st.Switches
	var out []wire.Section
	var general []wire.Component
	for _, s := range switchTable {
		general = append(general, switchRow(t, set, s.key, t.s(s.zh, s.en), t.s(s.zhNote, s.enNote))...)
	}
	k17 := entry.Switch(set, entry.K17DigScope)
	general = append(general, wire.Component{Type: wire.ComponentRow, Title: "K17 " + t.s("工具片段的挖掘范围", "How tool segments are dug"),
		Subtitle: t.f("当前：%s · 默认：%s", "now: %s · default: %s", onOff(t, k17), onOff(t, "signal"))},
		caption(whoSet(t, set, entry.K17DigScope)), caption(t.s("R3 两周不降、且人工看发现漏挖，才放宽；不会改成全量扫", "Loosened only when R3 does not fall for two weeks and missed segments are found; never a full scan")),
		wire.Component{Type: wire.ComponentSegmented, Name: "k17", Value: k17, Action: &wire.Action{ID: "k17"},
			Choices: []wire.Choice{{Value: "signal", Title: t.s("只挖信号段", "Signals only")}, {Value: "wide", Title: t.s("放宽初筛", "Looser")}}})
	out = append(out, wire.Section{Title: t.s("开关", "Switches"), Footer: t.s("默认值是设计时拍定的，不是从某个人的数据反推的；读数到了门槛会自动调，并在这里写明凭什么。", "Defaults are set by design, not read back from anyone's data; a reading crossing its threshold moves a switch automatically, and the reason is written here."), Items: general})

	// K2 per fork type: the most used here, every one on its own page.
	types := forkTypes(st)
	k2 := k2Items(t, st, types[:min(10, len(types))])
	if len(types) > 10 {
		k2 = append(k2, row(t.f("全部 %d 个岔口类型", "All %d fork types", len(types)), "", nil, ext.Open("open:k2", "", page{Tab: tabSwitches, Open: "k2"}, "K2")))
	}
	if len(k2) == 0 {
		k2 = append(k2, secondary(t.s("还没有岔口类型：默认判断攒出来以后，每一类在这里有一个开关", "No fork types yet: once default judgements are collected, each type gets a switch here")))
	}
	out = append(out, wire.Section{Title: "K2 " + t.s("默认判断怎么用（按岔口类型）", "How default judgements are used (by fork type)"),
		Footer: t.s("「直接办」只限能撤回的事：删数据、对外发消息、花钱、动线上服务永远只给建议。某一类被推翻 2 次自动关回「只附建议」。", "\"Act\" covers reversible things only: deleting data, messaging others, spending money and touching live services always stay suggestions. Two overturns put a type back to suggest."),
		Items:  k2})

	// K13 per sensitive category: only the user turns one on.
	var k13 []wire.Component
	for _, c := range entry.Sensitives {
		k13 = append(k13, switchRow(t, set, entry.K13Sensitive+"."+c, t.sensitiveLabel(c), "")...)
	}
	out = append(out, wire.Section{Title: "K13 " + t.s("敏感类别（情感、财务、健康、家庭）", "Sensitive categories (emotions, money, health, family)"),
		Footer: t.s("默认全关，只由你打开。打开后只进你自己的理解（⑥、画像卡、知识库），不进任何出口；健康空间照旧不读。", "All off by default and only you turn one on. Turned on, a category reaches only your own understanding (⑥, the cards, the knowledge base), never an exit; the health space is never read."),
		Items:  k13})

	// K16 per message source.
	var k16 []wire.Component
	seen := map[string]bool{}
	var names []string
	for _, s := range st.Sources {
		if (s.Code == entry.SrcChat || s.Code == entry.SrcRecording) && !seen[s.Name] {
			seen[s.Name] = true
			names = append(names, s.Name)
		}
	}
	for k := range set {
		if base, src, ok := strings.Cut(k, "."); ok && base == entry.K16LearnPeople && !seen[src] {
			seen[src] = true
			names = append(names, src)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		k16 = append(k16, switchRow(t, set, entry.K16LearnPeople+"."+n, n, "")...)
	}
	if len(k16) == 0 {
		k16 = append(k16, secondary(t.s("还没有消息来源", "No message source yet")))
	}
	out = append(out, wire.Section{Title: "K16 " + t.s("从闲聊里学人物和关系（按来源）", "Learning people and relations from chit-chat (by source)"),
		Footer: t.s("该来源说话人归属准确率 <95%，或有 1 句别人的话被算成你说的，自动关；下一轮抽检达标自动开。你设过的以你为准。", "Off by itself when a source's attribution is under 95% or one line of someone else's was put on you; back on when a later check passes. Your own setting wins."),
		Items:  k16})
	return out
}

// setSwitch records the user's setting.
func setSwitch(st *store.State, key, value string, now time.Time) {
	if st.Switches == nil {
		st.Switches = map[string]entry.SwitchSetting{}
	}
	st.Switches[key] = entry.SwitchSetting{Value: value, By: "user", At: now.UTC().Format(time.RFC3339)}
}

// switchKeyOK says whether a key is one the page may set, with the value it may take.
func switchKeyOK(key, value string) error {
	base, _, _ := strings.Cut(key, ".")
	if _, ok := entry.Defaults[base]; !ok && key != entry.K7GroupByProject {
		return fmt.Errorf("unknown switch %q", key)
	}
	switch base {
	case entry.K2Judgement:
		if value != "suggest" && value != "act" && value != "off" {
			return fmt.Errorf("K2 takes suggest, act or off")
		}
	case entry.K17DigScope:
		if value != "signal" && value != "wide" {
			return fmt.Errorf("K17 takes signal or wide")
		}
	default:
		if value != "on" && value != "off" {
			return fmt.Errorf("%s takes on or off", key)
		}
	}
	return nil
}

// k2Items are the K2 controls of some fork types: value, who set it, the automatic opening's reading, the choice.
func k2Items(t tr, st *store.State, types []string) []wire.Component {
	set := st.Switches
	var k2 []wire.Component
	for _, typ := range types {
		key := entry.K2Judgement + "." + typ
		cur := entry.Switch(set, key)
		auto := t.s("自动打开的判定：量不出（还没有这一类的采纳读数）", "automatic opening: not measurable (no acceptance reading for this type yet)")
		if r, ok := latestReading(st, "R4."+typ); ok {
			if r.Measure && r.Den > 0 {
				auto = t.f("采纳 %d / %d 次（%.0f%%）；≥10 次、≥90%%、0 次推翻才自动打开", "taken %d of %d (%.0f%%); opens itself at ≥10, ≥90%% and none overturned", r.Num, r.Den, float64(r.Num)*100/float64(r.Den))
			} else {
				auto = t.f("自动打开的判定：量不出（机会 %d 次，不到 20）", "automatic opening: not measurable (%d chances, under 20)", r.Den)
			}
		}
		k2 = append(k2, wire.Component{Type: wire.ComponentRow, Title: typ,
			Subtitle: t.f("当前：%s · 默认：%s", "now: %s · default: %s", onOff(t, cur), onOff(t, "suggest"))},
			caption(whoSet(t, set, key)), caption(auto),
			wire.Component{Type: wire.ComponentSegmented, Name: "k2:" + typ, Value: cur, Action: &wire.Action{ID: "k2"},
				Choices: []wire.Choice{{Value: "suggest", Title: t.s("只附建议", "Suggest")}, {Value: "act", Title: t.s("可撤回的直接办", "Act")}, {Value: "off", Title: t.s("关", "Off")}}})
	}
	return k2
}

// k2PerPage: fork types on one K2 page (four components each, within the hub's 300 a page).
const k2PerPage = 60

// k2Page is every fork type's K2 control, a page of them at a time (from offset).
func (a *app) k2Page(t tr, st *store.State, s page) *wire.View {
	types := forkTypes(st)
	from := 0
	fmt.Sscan(s.Sub, &from)
	from = max(0, min(from, len(types)))
	to := min(len(types), from+k2PerPage)
	items := k2Items(t, st, types[from:to])
	if len(items) == 0 {
		items = append(items, empty(t.s("没有更多了", "No more")))
	}
	if to < len(types) {
		p := s
		p.Sub = fmt.Sprint(to)
		items = append(items, row(t.f("后面 %d 类", "%d more types", len(types)-to), "", nil, ext.Open("open:k2more", "", p, "K2")))
	}
	return &wire.View{Title: "K2 " + t.s("默认判断怎么用（按岔口类型）", "How default judgements are used (by fork type)"),
		Sections: []wire.Section{{Title: t.f("第 %d–%d 类，共 %d 类", "Types %d–%d of %d", from+1, to, len(types)), Items: items}}}
}
