package main

// The v4 pages (design §4.2, §4.3, §5.7, §6.1 G, §8, §9.3, §9.5; work package P7): the start card, the sources card,
// the exits' state, the first-run questions, the six facets and the people cards, the switches and the check page.
// Everything is read from the last snapshot (a.read) and written only through the curator's entry API (UserAct,
// Observe) or the fields the page owns (State.Onboarding, user switches, verdicts).

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	exits "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/signals"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// v4 tabs. The older tabs (rules, judgement, memory, fact, knowledge, graph) still draw when asked for; they are
// reached from "older".
const (
	tabStart    = "start"
	tabFacets   = "facets"
	tabPeople   = "people"
	tabCheck    = "check"
	tabSwitches = "switches"
	tabReview   = "review"
	tabReports  = "reports"
	tabOlder    = "older"
)

var legacyTabs = []string{"rules", "judgement", "memory", "fact", "knowledge", "graph"}

func v4Tabs(t tr) []wire.Tab {
	return []wire.Tab{{Value: tabStart, Title: t.s("起步", "Start")}, {Value: tabFacets, Title: t.s("六个方面", "Six facets")},
		{Value: tabPeople, Title: t.s("画像卡", "People")}, {Value: tabCheck, Title: t.s("核对", "Check")},
		{Value: tabSwitches, Title: t.s("开关", "Switches")}, {Value: tabReview, Title: t.s("待确认", "To confirm")},
		{Value: tabReports, Title: t.s("报告", "Reports")}, {Value: tabOlder, Title: t.s("旧版", "Older")}}
}

// facetsShown are the facets of the facets tab; ⑤′ has a tab of its own.
var facetsShown = []entry.Facet{entry.Interaction, entry.Tools, entry.Judgement, entry.Workflow, entry.World, entry.Persona}

// ---- small helpers ------------------------------------------------------------------------------------------------

func row(title, sub string, badge *wire.Badge, act *wire.Action) wire.Component {
	return wire.Component{Type: wire.ComponentRow, Title: title, Subtitle: sub, Badge: badge, Action: act}
}

func toned(s, tone string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s, Tone: tone}
}

func empty(title string) wire.Component {
	return wire.Component{Type: wire.ComponentEmpty, Title: title}
}

// stableish: the entry counts as "已有" on the start card: stable, or pinned by the user.
func stableish(e *entry.Entry) bool { return e.Status == entry.Stable || e.Pinned }

// lastMark is the user's latest mark on an entry ("" none).
func lastMark(vs []entry.Verdict) string {
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].By == "user" || vs[i].By == "" {
			if vs[i].Mark == "wrong" && vs[i].Note == "unsupported" {
				return "unsupported"
			}
			return vs[i].Mark
		}
	}
	return ""
}

// verdictSeg is one entry's right / wrong / not sure control on the check page (a segmented choice acts at once).
// F1 adds "not in the source".
func verdictSeg(t tr, name, cur string, f1 bool) wire.Component {
	if cur == "" {
		cur = "none"
	}
	choices := []wire.Choice{{Value: "none", Title: t.s("未判", "Unmarked")}, {Value: "right", Title: t.markLabel("right")},
		{Value: "wrong", Title: t.markLabel("wrong")}}
	if f1 {
		choices = append(choices, wire.Choice{Value: "unsupported", Title: t.s("出处不支持", "Unsupported")})
	}
	choices = append(choices, wire.Choice{Value: "unsure", Title: t.markLabel("unsure")})
	return wire.Component{Type: wire.ComponentSegmented, Name: name, Value: cur, Choices: choices, Action: &wire.Action{ID: "mark"}}
}

// hashKey is a short stable key for field names (entry ids are safe already; refs are not).
func hashKey(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:6])
}

// ---- exits: which entry is in which exit ---------------------------------------------------------------------------

// exitIndex reads exits.json (internal/export's manifest, in the data directory): entry id → the exits it is in.
func exitIndex(dir string) map[string][]string {
	out := map[string][]string{}
	raw, err := os.ReadFile(filepath.Join(dir, exits.ManifestPath))
	if err != nil {
		return out
	}
	var m struct {
		Files map[string]struct {
			Exit    string   `json:"exit"`
			Entries []string `json:"entries"`
		} `json:"files"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for _, f := range m.Files {
		for _, id := range f.Entries {
			if !slices.Contains(out[id], f.Exit) {
				out[id] = append(out[id], f.Exit)
			}
		}
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

// exitHashes reads each exit's hash from exits.json: what the hub's readback is compared with.
func exitHashes(dir string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(dir, exits.ManifestPath))
	if err != nil {
		return out
	}
	var m struct {
		Exits map[string]struct {
			Hash string `json:"hash"`
		} `json:"exits"`
	}
	if json.Unmarshal(raw, &m) == nil {
		for x, s := range m.Exits {
			out[x] = s.Hash
		}
	}
	return out
}

// ---- sources ------------------------------------------------------------------------------------------------------

// sourceRows are the sources card's rows in display order.
func sourceRows(st *store.State) []*entry.SourceStatus {
	var out []*entry.SourceStatus
	for _, s := range st.Sources {
		out = append(out, s)
	}
	order := map[string]int{entry.SrcAssistant: 1, entry.SrcAsk: 2, entry.SrcAgent: 3, entry.SrcTools: 4, entry.SrcTodo: 5,
		entry.SrcMemory: 6, entry.SrcAbout: 7, entry.SrcChat: 8, entry.SrcKB: 9, entry.SrcRecording: 11}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Code] != order[out[j].Code] {
			return order[out[i].Code] < order[out[j].Code]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// have says whether any source of a code is there.
func have(st *store.State, code string) bool {
	for _, s := range st.Sources {
		if s.Code == code && (s.Available || s.Units > 0) {
			return true
		}
	}
	return false
}

func kb(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// sourcesSection is the sources card (§1.7, 4.2): is each source there, how much was read, why not, the speaker
// attribution checks and whether chit-chat may teach people (K16); this week's reading of tool segments (3.1); and
// the hint when the knowledge base does not know the user's accounts.
func (a *app) sourcesSection(ctx context.Context, t tr, st *store.State) []wire.Section {
	var rows []wire.Component
	for _, s := range sourceRows(st) {
		var badge *wire.Badge
		switch {
		case s.Available && s.Waiting != "":
			badge = &wire.Badge{Text: t.f("有 · 部分等 hub（%s）", "partly · waiting for the hub (%s)", s.Waiting), Tone: wire.ToneCaution}
		case s.Available || s.Units > 0:
			badge = &wire.Badge{Text: t.s("有", "on"), Tone: wire.TonePositive}
		default:
			tone := wire.ToneNeutral
			if s.Missing == "error" {
				tone = wire.ToneDanger
			} else if s.Missing == "waiting_hub" || s.Missing == "offline" {
				tone = wire.ToneCaution
			}
			badge = &wire.Badge{Text: t.missingLabel(s), Tone: tone}
		}
		sub := t.f("读了 %d 段、%d 行、%s", "read %d units, %d lines, %s", s.Units, s.Lines, kb(s.Bytes))
		if s.LastRead != "" && len(s.LastRead) >= 10 {
			sub += t.f(" · 最近 %s", " · last %s", s.LastRead[:10])
		}
		if s.Code == entry.SrcChat || s.Code == entry.SrcRecording {
			if s.Sampled > 0 {
				// Before the user marked any, "0 right" would read as all wrong: say they are waiting.
				marked := 0
				for _, c := range st.SpeakerChecks {
					if c.Source == s.Name && c.Verdict != nil && c.Verdict.By != "model" {
						marked++
					}
				}
				acc := t.s("，等你核对", ", waiting for your check")
				if marked > 0 {
					acc = t.f("，核对了 %d 句、对 %d 句", ", %d checked, %d right", marked, s.Right)
				}
				sub += t.f(" · 说话人抽检 %d 句%s", " · speaker check: %d lines%s", s.Sampled, acc)
			}
			if s.Unresolved > 0 {
				sub += t.f(" · 认不出说话人 %.0f%%", " · speaker unknown %.0f%%", s.Unresolved*100)
			}
			if !curate.LearnPeople(st, s.Name) {
				why := s.Note
				if why == "" {
					why = t.s("开关 K16 关着", "switch K16 is off")
				}
				sub += t.s(" · 已停学闲聊里的人物和关系：", " · not learning people from chit-chat: ") + why
			}
		} else if s.Note != "" && s.Missing != "" {
			sub += " · " + s.Note
		}
		rows = append(rows, row(t.sourceLabel(s.Code, s.Name), sub, badge, nil))
	}
	if len(rows) == 0 {
		rows = append(rows, empty(t.s("还没读过任何来源：第一次整理后这里会列出每个来源有没有、读了多少", "No source read yet: after the first run every source is listed here")))
	}
	foot := t.s("没有的来源不会报错：插件只用已有的来源，并在起步卡片上写明每个方面因此少了什么。", "A missing source is not an error: the curator uses what is there and the start card says what each facet misses for it.")
	out := []wire.Section{{Title: t.s("来源", "Sources"), Footer: foot, Items: rows}}

	// This week's tool-segment reading: folded pages and the segments dug for friction signals.
	days := lastDays(time.Now().In(a.c.Loc), 7)
	if v, err := signals.VolumeOf(st, a.c.St.Dir, days); err == nil {
		items := []wire.Component{{Type: wire.ComponentStatGrid, Stats: []wire.Stat{
			{Label: t.s("本周读的折叠页", "Folded pages read"), Value: kb(v.FoldBytes)},
			{Label: t.s("挖了几段", "Segments dug"), Value: fmt.Sprint(v.Segments), Detail: kb(v.SegmentBytes)},
			{Label: t.s("不顺信号", "Friction signals"), Value: fmt.Sprint(v.Hits)},
			{Label: t.s("由此记下的条目", "Entries from them"), Value: fmt.Sprint(v.Entries)},
		}}}
		if v.FoldsUnknown > 0 {
			items = append(items, secondary(t.f("%d 页折叠摘要没有报错数和工具名（等 hub H3）：不当作「没报错」。", "%d folded pages came without error counts and tool names (waiting for hub H3): not read as \"no errors\".", v.FoldsUnknown)))
		}
		out = append(out, wire.Section{Title: t.s("本周读了多少、挖了几段", "Read this week, segments dug"),
			Footer: t.s("工具调用不全量读：只读折叠摘要，被不顺信号触发的那一段才展开。", "Tool calls are never read in bulk: only folded pages, and a segment is opened only when a friction signal points at it."), Items: items})
	}
	if hint := a.ownerHint(ctx, t); hint != "" {
		out[0].Items = append(out[0].Items, toned(hint, wire.ToneCaution))
	}
	return out
}

// ownerHint: kb.owner without accounts in the sources means the user's own lines in group chats and mail cannot be
// told apart (5.0): ③④⑥ are then not learned from them.
func (a *app) ownerHint(ctx context.Context, t tr) string {
	if a.c.Src == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	o, err := a.c.Src.Owner(ctx)
	if err != nil || len(o.Handles) > 0 {
		return ""
	}
	return t.s("补上你在各来源的账号后，能从群聊里学到更多（助手设置 › 关于我 › 账号）。现在群聊和邮件里认不出哪句是你说的，③④⑥ 暂不从那里学。",
		"Add your accounts in each source and more can be learned from group chats (assistant settings › About me › accounts). For now your own lines in group chats and mail cannot be told apart, so ③④⑥ are not learned from them.")
}

func lastDays(now time.Time, n int) []string {
	var out []string
	for i := n - 1; i >= 0; i-- {
		out = append(out, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

// ---- the start card (4.2) -----------------------------------------------------------------------------------------

// facetCounts: live entries "已有" (stable or pinned) and "正在攒" (candidate or shifting), by facet.
func facetCounts(st *store.State) (have, collecting map[entry.Facet]int) {
	have, collecting = map[entry.Facet]int{}, map[entry.Facet]int{}
	for _, e := range st.Entries {
		if !e.Live() {
			continue
		}
		if stableish(e) {
			have[e.Facet]++
		} else {
			collecting[e.Facet]++
		}
	}
	return
}

// forkSeen is one fork type (③) or kind of task (④) seen so far: how often, and how often the choice agreed.
type forkSeen struct {
	Type          string
	Seen, Agree   int
	Disagree, Ids int
}

// forksSeen sums, by type, what the facet has seen: a class's actions and counter-examples, or (with no class yet)
// the actions themselves, the most common choice counting as agreeing.
func forksSeen(st *store.State, f entry.Facet) []forkSeen {
	by := map[string]*forkSeen{}
	choices := map[string]map[string]int{}
	get := func(typ string) *forkSeen {
		if typ == "" {
			typ = "—"
		}
		if by[typ] == nil {
			by[typ] = &forkSeen{Type: typ}
		}
		return by[typ]
	}
	for _, e := range st.Entries {
		if e.Facet != f || !e.Live() {
			continue
		}
		switch e.Grain {
		case entry.Class:
			x := get(e.Type)
			agree := max(len(e.Members), e.Count)
			x.Agree += agree
			x.Disagree += len(e.Counters)
			x.Seen += max(e.Chances, agree+len(e.Counters))
			x.Ids++
		case entry.Action:
			if e.Parent != "" {
				continue // counted with its class
			}
			typ := e.Type
			if typ == "" {
				typ = "—"
			}
			if choices[typ] == nil {
				choices[typ] = map[string]int{}
			}
			key := norm.Q(e.Choice)
			if key == "" {
				key = norm.Q(e.Text)
			}
			choices[typ][key] += max(1, e.Count)
		}
	}
	for typ, cs := range choices {
		x := get(typ)
		top, total := 0, 0
		for _, n := range cs {
			total += n
			top = max(top, n)
		}
		x.Seen += total
		x.Agree += top
		x.Disagree += total - top
	}
	var out []forkSeen
	for _, x := range by {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seen != out[j].Seen {
			return out[i].Seen > out[j].Seen
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// missingFor says, per facet, which source it lacks and what the user could connect (§1.7): a sentence, never a
// push. "" when the facet has what it needs.
func missingFor(t tr, st *store.State, f entry.Facet) string {
	s1, s7, s8 := have(st, entry.SrcAssistant), have(st, entry.SrcAbout), have(st, entry.SrcChat)
	s3 := have(st, entry.SrcAgent)
	var parts []string
	if !s1 {
		parts = append(parts, t.s("还没读到 Persona 助手对话（每个方面的起点）", "no Persona assistant chats read yet (where every facet starts)"))
	}
	switch f {
	case entry.Interaction, entry.Tools:
		if !s3 {
			parts = append(parts, t.s("没有 Agent 会话：只从助手对话里学", "no agent sessions: learning from the assistant chats only"))
		}
	case entry.Judgement:
		if !s3 && !s8 {
			parts = append(parts, t.s("目前只从和助手的对话里学；接一个消息来源（飞书、邮件、微信…）能看到你在别处怎么拍板", "learning only from your chats with the assistant; a message source (Lark, mail, WeChat…) would show how you decide elsewhere"))
		}
	case entry.Workflow:
		if !s8 {
			parts = append(parts, t.s("没接消息来源：看不到你自己在别处怎么处理事，只能从你怎么指挥 AI 学；可以接飞书、邮件等", "no message source: only how you direct the AI is seen, not how you handle things elsewhere; Lark, mail and the like can be connected"))
		}
	case entry.World:
		if !s8 {
			parts = append(parts, t.s("没接消息来源：只剩助手对话里提到的人和事；接一个消息来源后这一面会多很多", "no message source: only people and things named in the assistant chats; a message source adds much more"))
		}
	case entry.People:
		if !s8 {
			parts = append(parts, t.s("没接消息来源：只有你在助手对话里提到的人，卡上没有推断侧写；接一个消息来源后交集名单和画像卡才能攒起来", "no message source: only people you named to the assistant, with no inferred profile; a message source fills the list and the cards"))
		}
	case entry.Persona:
		if !s7 {
			parts = append(parts, t.s("「关于我」是空的或读不到：在助手设置里写几句，这一面能更快起步", "\"About me\" is empty or unread: a few lines there in the assistant's settings start this facet sooner"))
		}
	}
	return strings.Join(parts, t.s("；", "; "))
}

func (a *app) startPage(ctx context.Context, t tr, st *store.State) []wire.Section {
	var out []wire.Section
	// The first-run replay (4.1): nothing goes to the assistant before it is done.
	if b := st.Backfill; b != nil && b.Finished == "" {
		total, done := daysBetween(b.From, b.To), daysBetween(b.From, b.Next)-1
		if b.Next == "" {
			done = total
		}
		ratio := 0.0
		if total > 0 {
			ratio = float64(max(0, done)) / float64(total)
		}
		out = append(out, wire.Section{Title: t.s("正在回放你的历史", "Replaying your history"),
			Footer: t.s("回放完成前不往助手那里写任何东西；读到的都在下面列着。", "Nothing goes to the assistant until the replay is done; what was read is listed below."),
			Items:  []wire.Component{{Type: wire.ComponentProgress, Label: t.f("%s 到 %s，已读到 %s（%d / %d 天）", "%s to %s, now at %s (%d / %d days)", b.From, b.To, b.Next, max(0, done), total), Ratio: &ratio}}})
	}
	// The first-run questions (4.3, K12): once, skippable.
	if entry.On(st.Switches, entry.K12Onboarding) {
		ob := st.Onboarding
		switch {
		case ob == nil || (ob.At == "" && !ob.Skipped):
			out = append(out, wire.Section{Title: t.s("三个问题（都可以跳过）", "Three questions (all skippable)"),
				Footer: t.s("只问你自己知道、一时从记录里看不出的事；答案以后随时能改。", "Only what you know and your records cannot show yet; change the answers any time."),
				Items: []wire.Component{
					{Type: wire.ComponentButton, Title: t.s("回答", "Answer"), Action: ext.Open("open:onboarding", "", page{Tab: tabStart, Open: "onboarding"}, t.s("三个问题", "Three questions"))},
					button(t.s("跳过", "Skip"), "ob:skip", ""),
				}})
		default:
			sub := t.s("跳过了", "skipped")
			if ob.At != "" {
				sub = t.f("用途：%s · 要求 %d 条 · 在推进 %d 件", "use: %s · %d standing asks · %d things in progress", useLabel(t, ob.Use), len(ob.Musts), len(ob.Focus))
			}
			out = append(out, wire.Section{Items: []wire.Component{row(t.s("首次引导的回答", "Your first-run answers"), sub, nil,
				ext.Open("open:onboarding", "", page{Tab: tabStart, Open: "onboarding"}, t.s("三个问题", "Three questions")))}})
		}
	}
	// The start card: each facet in one row.
	have, collecting := facetCounts(st)
	var rows []wire.Component
	for _, f := range entry.Facets {
		sub := t.f("已有 %d 条 · 正在攒 %d 条", "%d in use · %d collecting", have[f], collecting[f])
		if f == entry.People {
			sub = t.f("名单 %d 人 · 卡上已有 %d 条 · 正在攒 %d 条", "%d people on the list · %d card lines in use · %d collecting", len(people.Ranked(st.Contacts)), have[f], collecting[f])
		}
		var notes []string
		if (f == entry.Judgement || f == entry.Workflow) && have[f] == 0 {
			var seen []string
			for i, x := range forksSeen(st, f) {
				if i >= 3 {
					break
				}
				seen = append(seen, t.f("「%s」看到过 %d 次，选择一致 %d、不一致 %d", "\"%s\" seen %d times, same choice %d, different %d", x.Type, x.Seen, x.Agree, x.Disagree))
			}
			if len(seen) > 0 {
				notes = append(notes, seen...)
			} else {
				notes = append(notes, t.s("还没看到过同类的岔口：要同类的事重复出现才看得出", "no repeated fork seen yet: it takes the same kind of choice coming up again"))
			}
		}
		if n, ok := closestToStable(st, f); ok {
			notes = append(notes, t.f("离成条目最近的一条还差 %d 次（门槛按你自己的数据量自动调整）",
				"the nearest one needs %d more (the bar adapts to how much data you have)", n))
		}
		if m := missingFor(t, st, f); m != "" {
			notes = append(notes, m)
		}
		tab := tabFacets
		if f == entry.People {
			tab = tabPeople
		}
		rows = append(rows, row(t.facetLabel(f), sub, nil, ext.Open("open:facet:"+string(f), "", page{Tab: tab, Facet: string(f)}, t.facetLabel(f))))
		for _, n := range notes {
			rows = append(rows, wire.Component{Type: wire.ComponentText, Text: n, Style: wire.TextStyleCaption})
		}
	}
	out = append(out, wire.Section{Title: t.s("起步", "Getting started"),
		Footer: t.s("哪一面还没攒够，这一面在助手那里就空着，不用泛泛的默认画像去填。", "A facet without enough yet stays empty for the assistant; it is never filled with generic defaults."), Items: rows})
	out = append(out, a.sourcesSection(ctx, t, st)...)
	out = append(out, a.exitsSection(ctx, t, st))
	return out
}

func daysBetween(from, to string) int {
	a, err1 := time.Parse("2006-01-02", from)
	b, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(b.Sub(a).Hours()/24) + 1
}

func useLabel(t tr, use string) string {
	switch use {
	case entry.Work:
		return t.s("工作", "work")
	case entry.Life:
		return t.s("个人生活", "personal life")
	}
	return t.s("都有", "both")
}

// ---- exits (6.1, 6.2, 6.4) ----------------------------------------------------------------------------------------

// exitsSection: each exit, whether the hub took it (Exits.Adopted), waits for a hub change, or has a warning; and when
// the per-turn block takes effect for the runtime the assistant uses.
func (a *app) exitsSection(ctx context.Context, t tr, st *store.State) wire.Section {
	ex := st.Exits
	if ex == nil {
		ex = &entry.Exits{}
	}
	bytes, entries, written := map[string]int{}, map[string]int{}, map[string]string{}
	exitOfPath := func(p string) string {
		switch {
		case p == exits.InstructionsPath:
			return "A"
		case strings.HasPrefix(p, exits.SkillsDir):
			return "B"
		case strings.HasPrefix(p, exits.ProjectsDir):
			return "D"
		case strings.HasPrefix(p, "inbox/"):
			return "E"
		}
		return ""
	}
	{
		for p, f := range ex.Files {
			x := exitOfPath(p)
			bytes[x] += f.Bytes
			entries[x] += f.Entries
			if f.Written > written[x] {
				written[x] = f.Written
			}
		}
	}
	hashes := exitHashes(a.exitsDir())
	var rows []wire.Component
	for _, x := range []string{"A", "B", "C", "D", "E"} {
		var badge *wire.Badge
		sub := ""
		if x == "C" {
			sub = t.s("经「待确认」里的提议写回知识库，每处都能撤销", "written back to the knowledge base as proposals under \"To confirm\", each undoable")
			badge = &wire.Badge{Text: t.s("不用等 hub", "no hub change needed"), Tone: wire.TonePositive}
			rows = append(rows, row(t.exitLabel(x), sub, badge, nil))
			continue
		}
		if bytes[x] > 0 {
			sub = t.f("写了 %s、%d 条", "%s, %d entries", kb(bytes[x]), entries[x])
			if len(written[x]) >= 16 {
				sub += t.f(" · 写于 %s", " · written %s", strings.Replace(written[x][:16], "T", " ", 1))
			}
		} else {
			sub = t.s("还没有内容（空着就不写占位话）", "nothing in it yet (left empty, never filled with placeholders)")
		}
		switch ad, took := ex.Adopted[x]; {
		case ex.Waiting[x] != "":
			badge = &wire.Badge{Text: t.f("等 hub（%s）", "waiting for the hub (%s)", ex.Waiting[x]), Tone: wire.ToneCaution}
		case took && ad.Reason != "":
			badge = &wire.Badge{Text: t.s("没被采纳", "not taken"), Tone: wire.ToneDanger}
			sub += " · " + ad.Reason
		case took && hashes[x] != "" && ad.Hash != hashes[x]:
			badge = &wire.Badge{Text: t.s("hub 还在用旧版本", "the hub has an older version"), Tone: wire.ToneCaution}
			if len(ad.Earliest) >= 16 {
				sub += t.f(" · 最早 %s 采纳新版本", " · the new one is taken at the earliest %s", strings.Replace(ad.Earliest[:16], "T", " ", 1))
			}
		case took:
			badge = &wire.Badge{Text: t.s("已送到", "delivered"), Tone: wire.TonePositive}
			if len(ad.At) >= 16 {
				sub += t.f(" · hub 采纳于 %s", " · taken by the hub %s", strings.Replace(ad.At[:16], "T", " ", 1))
			}
			if ad.Truncated {
				sub += t.s(" · 被 hub 截断", " · cut by the hub")
			}
		default:
			badge = &wire.Badge{Text: t.s("还没采纳", "pending"), Tone: wire.ToneNeutral}
			if took && ad.Earliest != "" && len(ad.Earliest) >= 16 {
				sub += t.f(" · 最早 %s 采纳", " · taken at the earliest %s", strings.Replace(ad.Earliest[:16], "T", " ", 1))
			}
		}
		rows = append(rows, row(t.exitLabel(x), sub, badge, nil))
	}
	if len(ex.Waiting) > 0 {
		rows = append(rows, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleCaption, Text: t.s("标「等 hub」的出口：文件已经写好，hub 上线对应改动后助手才读得到；不算出错。", "Exits marked waiting for the hub: the files are written, and the assistant reads them once the hub has that change; not an error.")})
	}
	for _, w := range ex.Warnings {
		rows = append(rows, toned(t.s("告警：", "Warning: ")+w, wire.ToneDanger))
	}
	rows = append(rows, toned(a.runtimeLine(ctx, t, st), wire.ToneNeutral))
	return wire.Section{Title: t.s("送到助手那里了没有", "What reached the assistant"), Items: rows,
		Footer: t.s("出口 F（请示时附「按以往会选」）是每轮说明块里的一行说明，跟着 A 走。界面和周报（G）不用等 hub。", "Exit F (\"you would usually pick…\" on questions) is a line in the per-turn block and goes with A. The page and the weekly report (G) need no hub change.")}
}

// runtimeLine is "your assistant runs on X; the per-turn block takes effect at Y" (6.2), honest about what is not
// known and about the hub change it still waits for.
func (a *app) runtimeLine(ctx context.Context, t tr, st *store.State) string {
	rt := a.assistantRuntime(ctx)
	waiting := st.Exits != nil && st.Exits.Waiting["A"] != ""
	var when string
	switch {
	case strings.Contains(strings.ToLower(rt), "claude"):
		when = t.s("说明块变了以后，助手的下一条消息就用上", "a changed block is used from the assistant's next message")
	case strings.Contains(strings.ToLower(rt), "codex"):
		when = t.s("说明块变了以后，要新开会话或重启助手才用上（Codex 会话中途不重读说明）", "a changed block is used once a new session starts or the assistant restarts (Codex does not reread its instructions mid-session)")
	case rt != "":
		when = t.s("要看这个运行时读不读 CLAUDE.md / AGENTS.md 和技能目录", "depends on whether this runtime reads CLAUDE.md / AGENTS.md and the skills folder")
	}
	var s string
	if rt == "" {
		s = t.s("还不知道你的助手用哪种运行时。Claude：说明块下一条消息生效；Codex 等：新开会话或重启后生效。", "Not known yet which runtime your assistant uses. Claude: the block takes effect from the next message; Codex and others: from a new session or a restart.")
	} else {
		s = t.f("你的助手现在用 %s 运行；%s。", "Your assistant runs on %s; %s.", rt, when)
	}
	if waiting {
		s += t.s("现在 hub 还不能采纳说明块（等 H1），所以助手暂时读不到。", " The hub cannot take the block yet (waiting for H1), so the assistant does not read it for now.")
	}
	return s
}

// assistantRuntime is the runtime of the assistant's own session (hub sessions, Assistant true), cached for ten
// minutes; "" when it cannot be told (no agents.read, no hub API).
func (a *app) assistantRuntime(ctx context.Context) string {
	a.mu.Lock()
	if time.Since(a.rtAt) < 10*time.Minute {
		rt := a.rt
		a.mu.Unlock()
		return rt
	}
	h := a.hubAPI
	a.mu.Unlock()
	rt := ""
	if h != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if ss, err := h.Sessions(ctx, time.Now().AddDate(0, 0, -14)); err == nil {
			var at time.Time
			for _, s := range ss {
				if s.Assistant && s.Agent != "" && s.Updated.After(at) {
					rt, at = s.Agent, s.Updated
				}
			}
		}
	}
	a.mu.Lock()
	a.rt, a.rtAt = rt, time.Now()
	a.mu.Unlock()
	return rt
}

// ---- the first-run questions (4.3) --------------------------------------------------------------------------------

func (a *app) onboardingPage(t tr, st *store.State) *wire.View {
	ob := st.Onboarding
	if ob == nil {
		ob = &entry.Onboarding{}
	}
	use := ob.Use
	if use == "" {
		use = "both"
	}
	items := []wire.Component{
		secondary(t.s("都可以跳过、可以只答一部分，以后在这里随时改。不问你的默认判断和做事方式：那两面要从你实际怎么做里看。", "Skip any of them, answer only some, change them here any time. Nothing is asked about how you decide or work: those two are learned from what you actually do.")),
		{Type: wire.ComponentField, Name: "use", Label: t.s("1. 你主要用 Persona 做什么？", "1. What do you mostly use Persona for?"), Kind: wire.FieldKindChoice, Value: use,
			Choices: []wire.Choice{{Value: entry.Work, Title: t.s("工作", "Work")}, {Value: entry.Life, Title: t.s("个人生活", "Personal life")}, {Value: "both", Title: t.s("都有", "Both")}}},
		secondary(t.s("选「工作」时，生活类内容只在这个页面上显示，不送到助手那里。", "With \"Work\", personal-life content stays on this page and is not sent to the assistant.")),
		{Type: wire.ComponentField, Name: "musts", Label: t.s("2. 有没有 AI 每次都该照做、或者绝不能做的事？（一行一条）", "2. Anything the AI should always do, or never do? (one per line)"), Kind: wire.FieldKindMultiline,
			Value: strings.Join(ob.Musts, "\n"), Placeholder: t.s("例如：先给结论再给理由", "e.g. give the answer first, then the reasons")},
		{Type: wire.ComponentField, Name: "focus", Label: t.s("3. 你现在最在推进的一到三件事是什么？（一行一件）", "3. The one to three things you are pushing forward most now? (one per line)"), Kind: wire.FieldKindMultiline,
			Value: strings.Join(ob.Focus, "\n"), Placeholder: t.s("例如：project-x 上线", "e.g. launching project-x")},
	}
	acts := []wire.Component{
		{Type: wire.ComponentButton, Title: t.s("保存", "Save"), Action: &wire.Action{ID: "ob:save", Submit: true}},
		button(t.s("全部跳过", "Skip all"), "ob:skip", ""),
	}
	foot := t.s("第 2 题每条成为「和 AI 的交互」里一条你自述的要求，直接生效；第 3 题每件在知识库里找对应的项目，找不到就记在「关于你」的在做的事里。", "Each line of question 2 becomes one of your own asks under \"Working with the AI\", in effect at once; each item of question 3 is matched to a project in your knowledge base, or else kept under \"About you\" as something you are working on.")
	return &wire.View{Title: t.s("三个问题", "Three questions"), Sections: []wire.Section{{Items: items, Footer: foot}, {Items: acts}}}
}

// stripListPrefix removes one list marker ("1. ", "2、", "3) ", "- ", "• ") and nothing else: a line that starts
// with a year or another number is kept as it is.
func stripListPrefix(s string) string {
	for _, p := range []string{"- ", "• ", "* ", "· "} {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(strings.TrimPrefix(s, p))
		}
	}
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		rest := s[i+1:]
		for _, p := range []string{". ", "、", ") ", "）"} {
			if strings.HasPrefix(rest, p) {
				if out := strings.TrimSpace(strings.TrimPrefix(rest, p)); out != "" {
					return out
				}
			}
		}
		break
	}
	return s
}

// lines splits a multi-line answer: trimmed, empty lines dropped, duplicates once.
func lines(s string, most int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = stripListPrefix(strings.TrimSpace(l))
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
		if most > 0 && len(out) >= most {
			break
		}
	}
	return out
}

// onboardingEntry is the entry an answer makes: ① a standing ask, or ⑥ something in progress (with its project when
// the knowledge base has one).
func onboardingEntry(lang string, f entry.Facet, text, project, day string) *entry.Entry {
	e := &entry.Entry{Facet: f, Grain: entry.Action, Origin: entry.FromOnboarding, Text: text,
		Evidence: []entry.Evidence{{Ref: "onboarding/" + string(f), Day: day, Who: entry.WhoSelf, Role: "user", Quote: text}}}
	switch f {
	case entry.Interaction:
		e.Type = "standing_ask"
		e.Impact = entry.Impact{Moment: "every_turn", Action: text}
	case entry.Persona:
		e.Type = "current_focus"
		e.Impact = entry.Impact{Moment: "every_turn", Action: text}
		if project != "" {
			e.Scopes = []entry.Scope{{Kind: entry.ScopeProject, Name: project}}
			e.Object = project
		}
	}
	return e
}

// focusText is a ⑥ "working on" line in the target language; the user's words stay as written.
func focusText(lang, what string) string {
	if strings.HasPrefix(strings.ToLower(lang), "zh") || lang == "" {
		return "正在推进：" + what
	}
	return "Working on: " + what
}

// saveOnboarding writes the answers (State.Onboarding) and their entries: each standing ask an ① entry, each focus
// a ⑥ entry. An answer taken out later is deleted (it can be restored); one given again is restored. Call it with
// the store's lock held.
func (a *app) saveOnboarding(ctx context.Context, use string, musts, focus []string) error {
	c := a.c
	s := c.St.S
	now := time.Now()
	day := now.In(c.Loc).Format("2006-01-02")
	lang := c.Cfg.Language
	if lang == "" {
		lang = c.Lang
	}
	old := s.Onboarding
	if old == nil {
		old = &entry.Onboarding{}
	}
	if use == "both" {
		use = ""
	}
	projects := a.projects(ctx)
	keep := map[string]bool{}
	put := func(e *entry.Entry) error {
		id := curate.EntryID(e.Facet, e.Grain, e.Text, e.Person)
		keep[id] = true
		if x := s.Entries[id]; x != nil && x.Deleted {
			return c.UserAct(id, curate.ActRestore, "")
		}
		c.Observe(e, "onboarding")
		return nil
	}
	for _, m := range musts {
		if err := put(onboardingEntry(lang, entry.Interaction, m, "", day)); err != nil {
			return err
		}
	}
	for _, f := range focus {
		if err := put(onboardingEntry(lang, entry.Persona, focusText(lang, f), matchProject(projects, f), day)); err != nil {
			return err
		}
	}
	// Answers taken out: their entries go (restorable), unless the user pinned or edited them since.
	for _, e := range s.Entries {
		if e.Origin == entry.FromOnboarding && !keep[e.ID] && !e.Deleted && !e.Pinned && e.User == nil {
			if err := c.UserAct(e.ID, curate.ActDelete, "onboarding answer removed"); err != nil {
				return err
			}
		}
	}
	shown := old.Shown
	if shown == "" {
		shown = now.UTC().Format(time.RFC3339)
	}
	s.Onboarding = &entry.Onboarding{Shown: shown, Use: use, Musts: musts, Focus: focus, At: now.UTC().Format(time.RFC3339)}
	return nil
}

// projects are the knowledge base's projects (id → name), for matching what the user is working on.
func (a *app) projects(ctx context.Context) map[string]string {
	out := map[string]string{}
	if a.c.Src == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	es, err := a.c.Src.List(ctx, "projects")
	if err != nil {
		return out
	}
	for _, e := range es {
		out["project:"+e.ID] = e.Name
	}
	return out
}

// matchProject finds the project an answer names: the project's name in the answer, or the answer in the name.
func matchProject(projects map[string]string, what string) string {
	w := norm.Q(what)
	best, bestLen := "", 0
	for id, name := range projects {
		n := norm.Q(name)
		if len([]rune(n)) < 2 {
			continue
		}
		if (strings.Contains(w, n) || strings.Contains(n, w)) && len(n) > bestLen {
			best, bestLen = id, len(n)
		}
	}
	return best
}

// closestToStable is how many more consistent sightings the candidate of a facet nearest to stable needs (design
// 4.2 "还差几次"): its bar is curate.StableK of its chances, and it must rest on at least two active days.
func closestToStable(st *store.State, f entry.Facet) (int, bool) {
	best, found := 0, false
	for _, e := range st.Entries {
		if e.Facet != f || !e.Live() || e.Status != entry.Candidate || e.Origin != entry.Behavior {
			continue
		}
		if f != entry.World && f != entry.Tools && e.Grain == entry.Action && e.Parent != "" {
			continue // counted with its class
		}
		chances := e.Chances
		if chances < e.Count+len(e.Counters) {
			chances = e.Count + len(e.Counters)
		}
		need := curate.StableK(chances) - e.Count
		if days := 2 - len(e.ActiveDays); days > need {
			need = days
		}
		if need < 1 {
			need = 1 // it waits for the next pass to settle it
		}
		if !found || need < best {
			best, found = need, true
		}
	}
	return best, found
}
