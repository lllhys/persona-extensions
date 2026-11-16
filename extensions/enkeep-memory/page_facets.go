package main

// The six facets and the people cards (design 1, 5.0, 5.7): every entry with its text, status, counts, kinds of
// source, evidence, counter-examples, whether it is inferred and which exits it is in; pin, edit, delete (restorable).
// What the user said about themself (the first-run answers, "about me") is shown apart from what was learned from
// behaviour, and when the two disagree both are shown and the user picks one.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const rowsPerSection = 40

func facetChoice(t tr, f entry.Facet) string {
	switch f {
	case entry.Interaction:
		return t.s("① 和 AI 交互", "① With the AI")
	case entry.Tools:
		return t.s("② AI 和工具", "② Tools")
	case entry.Judgement:
		return t.s("③ 默认判断", "③ Judgements")
	case entry.Workflow:
		return t.s("④ 处理方式", "④ Ways of work")
	case entry.World:
		return t.s("⑤ 人和事", "⑤ World")
	case entry.Persona:
		return t.s("⑥ 关于你", "⑥ About you")
	}
	return t.facetLabel(f)
}

// entrySub is an entry's one-line facts in a list.
func entrySub(t tr, e *entry.Entry, exits []string) string {
	parts := []string{t.grainLabel(e.Grain)}
	if e.Type != "" {
		parts = append(parts, e.Type)
	}
	parts = append(parts, t.f("%d 次", "%d×", e.Count))
	if len(e.Kinds) > 0 {
		parts = append(parts, t.f("%d 种来源", "%d kinds of source", len(e.Kinds)))
	}
	if len(e.Counters) > 0 {
		parts = append(parts, t.f("反例 %d", "%d against", len(e.Counters)))
	}
	if e.Inferred {
		parts = append(parts, t.s("推断", "inferred"))
	}
	if e.ReportedOnly {
		parts = append(parts, t.s("只来自转述", "reported only"))
	}
	if e.SingleSource {
		parts = append(parts, t.s("单一来源", "single source"))
	}
	if e.Sensitive != "" {
		parts = append(parts, t.f("敏感：%s（不进出口）", "sensitive: %s (never sent out)", t.sensitiveLabel(e.Sensitive)))
	}
	if len(exits) > 0 {
		parts = append(parts, t.s("在出口 ", "in exit ")+strings.Join(exits, ","))
	}
	if e.User != nil {
		parts = append(parts, t.s("你改过", "edited by you"))
	}
	if e.Last != "" {
		parts = append(parts, t.s("最近 ", "last ")+e.Last)
	}
	return strings.Join(parts, " · ")
}

func entryBadge(t tr, e *entry.Entry) *wire.Badge {
	if e.Deleted {
		return &wire.Badge{Text: t.s("已删", "deleted")}
	}
	if m := lastMark(e.Verdicts); m == "wrong" || m == "unsupported" {
		return &wire.Badge{Text: t.s("判了不对", "marked wrong"), Tone: wire.ToneDanger}
	}
	if e.Pinned {
		return &wire.Badge{Text: t.s("钉住", "pinned"), Tone: wire.TonePositive}
	}
	tone := wire.ToneNeutral
	switch e.Status {
	case entry.Stable:
		tone = wire.TonePositive
	case entry.Shifting:
		tone = wire.ToneCaution
	}
	return &wire.Badge{Text: t.statusLabel(e.Status), Tone: tone}
}

func entryRow(t tr, e *entry.Entry, exits []string, back page) wire.Component {
	p := back
	p.Open = "e:" + e.ID
	return row(short(e.Shown(), 90), entrySub(t, e, exits), entryBadge(t, e), ext.Open("open:"+e.ID, "", p, short(e.Shown(), 20)))
}

// selfReported: what the user wrote about themself, kept apart from what was learned from behaviour.
func selfReported(e *entry.Entry) bool {
	return e.Origin == entry.FromOnboarding || e.Origin == entry.SelfReport
}

// conflicts pairs each self-reported entry with the behaviour entries that went against it (its counters, or an
// entry that invalidated it). Both stay; the user picks one.
func conflicts(st *store.State, f entry.Facet) [][2]*entry.Entry {
	var out [][2]*entry.Entry
	for _, id := range sortedKeys(st.Entries) {
		e := st.Entries[id]
		if e.Facet != f || !selfReported(e) || e.Deleted {
			continue
		}
		var others []string
		others = append(others, e.Counters...)
		for _, x := range st.Entries {
			if x.Facet == f && !selfReported(x) && slices.Contains(x.Counters, e.ID) {
				others = append(others, x.ID)
			}
		}
		for _, o := range others {
			if x := st.Entries[o]; x != nil && x.Live() && !selfReported(x) {
				out = append(out, [2]*entry.Entry{e, x})
			}
		}
	}
	return out
}

func (a *app) facetPage(t tr, st *store.State, s page) ([]wire.Section, *wire.Filter) {
	f := entry.Facet(s.Facet)
	if !slices.Contains(facetsShown, f) {
		f = entry.Interaction
	}
	var choices []wire.Choice
	for _, x := range facetsShown {
		choices = append(choices, wire.Choice{Value: string(x), Title: facetChoice(t, x)})
	}
	filter := &wire.Filter{Name: "facet", Title: t.s("方面", "Facet"), Choices: choices, Value: string(f), Action: wire.Action{ID: "facet"}}
	exits := exitIndex(a.exitsDir())
	back := page{Tab: tabFacets, Facet: string(f)}
	var out []wire.Section
	head := []wire.Component{text(t.facetLabel(f))}
	if m := missingFor(t, st, f); m != "" {
		head = append(head, secondary(m))
	}
	if f == entry.Judgement || f == entry.Workflow {
		label := t.s("岔口类型", "Fork types")
		if f == entry.Workflow {
			label = t.s("事情类型", "Kinds of task")
		}
		p := back
		p.Open = "types:" + string(f)
		head = append(head, row(t.s("改名、合并", "Rename, merge ")+label, t.f("%d 种", "%d types", len(typesOf(st, f))), nil, ext.Open("open:types", "", p, label)))
	}
	out = append(out, wire.Section{Items: head})

	// Self-report and behaviour disagree: show both, the user picks one.
	if cs := conflicts(st, f); len(cs) > 0 {
		var items []wire.Component
		for i, c := range cs {
			if i >= 10 {
				break
			}
			items = append(items, toned(t.s("你自己说的：", "You said: ")+short(c[0].Shown(), 80), wire.ToneNeutral),
				toned(t.s("从你的做法里看到的：", "Seen in what you do: ")+short(c[1].Shown(), 80), wire.ToneCaution),
				button(t.s("以我说的为准", "Keep what I said"), "pick:"+c[0].ID+":"+c[1].ID, ""),
				button(t.s("以做法为准", "Keep what I do"), "pick:"+c[1].ID+":"+c[0].ID, ""))
		}
		out = append(out, wire.Section{Title: t.s("你说的和做的不一致：选一条", "What you said and what you do differ: pick one"),
			Footer: t.s("两条都留着；选中的钉住，另一条记成不对、撤出出口（可以再改）。不会自动覆盖。", "Both are kept; the one picked is pinned, the other is marked wrong and withdrawn from the exits (you can change it again). Nothing is overwritten by itself."), Items: items})
	}

	var self, hand, abs []*entry.Entry
	byType := map[string][]*entry.Entry{}
	byGrain := map[entry.Grain][]*entry.Entry{}
	var deleted []*entry.Entry
	for _, e := range st.Entries {
		if e.Facet != f {
			continue
		}
		if e.Deleted {
			deleted = append(deleted, e)
			continue
		}
		if e.MergedInto != "" || e.Status == entry.Retired {
			continue
		}
		switch {
		case selfReported(e):
			self = append(self, e)
		case e.Origin == entry.Handwritten:
			hand = append(hand, e)
		case e.Grain == entry.Abstract:
			abs = append(abs, e)
		case f == entry.Judgement || f == entry.Workflow:
			byType[e.Type] = append(byType[e.Type], e)
		default:
			byGrain[e.Grain] = append(byGrain[e.Grain], e)
		}
	}
	section := func(title, foot string, es []*entry.Entry) {
		if len(es) == 0 {
			return
		}
		sortEntries(es)
		var items []wire.Component
		for i, e := range es {
			if i >= rowsPerSection {
				items = append(items, secondary(t.f("还有 %d 条（按计数 × 来源种数排在后面）", "%d more (lower by count × kinds of source)", len(es)-i)))
				break
			}
			items = append(items, entryRow(t, e, exits[e.ID], back))
		}
		out = append(out, wire.Section{Title: fmt.Sprintf("%s（%d）", title, len(es)), Footer: foot, Items: items})
	}
	section(t.s("你自己说的", "What you said yourself"), t.s("首次引导的回答和「关于我」：你说了算，直接生效，和从做法里攒出来的分开放。", "Your first-run answers and \"About me\": your word goes, in effect at once, kept apart from what is learned from what you do."), self)
	section(t.s("助手的手写记忆", "The assistant's hand-written memory"), t.s("当成你认可过的；插件从不改它的正文。", "Taken as approved by you; the curator never rewrites it."), hand)
	section(t.s("抽象", "Abstract"), t.s("过了抽象门槛：和一般人做法不同、管得住至少两类情境。", "Passed the abstract gate: differs from what most people do and governs at least two kinds of situation."), abs)
	if f == entry.Judgement || f == entry.Workflow {
		kind := t.s("岔口类型：", "Fork type: ")
		if f == entry.Workflow {
			kind = t.s("事情类型：", "Kind of task: ")
		}
		var types []string
		for k := range byType {
			types = append(types, k)
		}
		sort.Slice(types, func(i, j int) bool {
			if len(byType[types[i]]) != len(byType[types[j]]) {
				return len(byType[types[i]]) > len(byType[types[j]])
			}
			return types[i] < types[j]
		})
		for _, k := range types {
			name := k
			if name == "" {
				name = t.s("（未分类）", "(no type)")
			}
			es := byType[k]
			sort.SliceStable(es, func(i, j int) bool { return grainRank(es[i].Grain) < grainRank(es[j].Grain) })
			section(kind+name, "", es)
		}
	} else {
		for _, g := range []entry.Grain{entry.Class, entry.Action} {
			foot := ""
			if g == entry.Action && f == entry.Persona {
				foot = t.s("单次表现只作上两层的证据，不单独送到助手那里。", "Single moments are evidence for the two layers above and are not sent to the assistant on their own.")
			}
			section(t.grainLabel(g), foot, byGrain[g])
		}
	}
	if len(self)+len(hand)+len(abs)+len(byType)+len(byGrain[entry.Class])+len(byGrain[entry.Action]) == 0 {
		out = append(out, wire.Section{Items: []wire.Component{empty(t.s("这一面还没攒出来", "Nothing collected for this facet yet"))}})
	}
	if len(deleted) > 0 {
		section(t.s("删掉的（可以恢复）", "Deleted (can be restored)"), t.s("删掉的留着记号，同样的内容再抽出来会直接丢掉；恢复后记号也撤掉。", "A deleted entry leaves a mark so the same content is dropped when found again; restoring removes the mark."), deleted)
	}
	return out, filter
}

func grainRank(g entry.Grain) int {
	return map[entry.Grain]int{entry.Abstract: 0, entry.Class: 1, entry.Action: 2}[g]
}

// sortEntries orders as the exits and EntriesOf do: count × kinds of source, then the latest, then id.
func sortEntries(es []*entry.Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		wi, wj := es[i].Count*max(1, len(es[i].Kinds)), es[j].Count*max(1, len(es[j].Kinds))
		if wi != wj {
			return wi > wj
		}
		if es[i].Last != es[j].Last {
			return es[i].Last > es[j].Last
		}
		return es[i].ID < es[j].ID
	})
}

// typesOf lists a facet's types with how many live entries each has: the type table's, and any an entry uses.
func typesOf(st *store.State, f entry.Facet) map[string]int {
	out := map[string]int{}
	for _, d := range st.Types[f] {
		if d.Renamed == "" {
			out[d.Name] += 0
		}
	}
	for _, e := range st.Entries {
		if e.Facet == f && e.Live() && e.Type != "" {
			out[e.Type]++
		}
	}
	return out
}

// typesPage: rename a fork type (③) or kind of task (④), or merge several into one (5.2): the open type table is the
// user's to tidy.
func (a *app) typesPage(t tr, st *store.State, f entry.Facet) *wire.View {
	ts := typesOf(st, f)
	names := sortedKeys(ts)
	// Most used first; a choice field takes at most 50 (the hub's check), the rest are listed only.
	sort.SliceStable(names, func(i, j int) bool { return ts[names[i]] > ts[names[j]] })
	var rows []wire.Component
	var choices []wire.Choice
	for i, n := range names {
		if i < 120 {
			rows = append(rows, row(n, t.f("%d 条", "%d entries", ts[n]), nil, nil))
		}
		if i < 50 {
			choices = append(choices, wire.Choice{Value: n, Title: n})
		}
	}
	if len(names) > 120 {
		rows = append(rows, secondary(t.f("还有 %d 种", "%d more", len(names)-120)))
	}
	title := t.s("岔口类型", "Fork types")
	if f == entry.Workflow {
		title = t.s("事情类型", "Kinds of task")
	}
	if len(names) == 0 {
		return &wire.View{Title: title, Sections: []wire.Section{{Items: []wire.Component{empty(t.s("还没有类型", "No types yet"))}}}}
	}
	edit := []wire.Component{
		{Type: wire.ComponentField, Name: "type_from", Label: t.s("改哪一个", "Which one"), Kind: wire.FieldKindChoice, Choices: choices, Value: names[0]},
		{Type: wire.ComponentField, Name: "type_to", Label: t.s("改成", "New name"), Kind: wire.FieldKindText},
		{Type: wire.ComponentButton, Title: t.s("改名", "Rename"), Action: &wire.Action{ID: "type:rename:" + string(f), Submit: true}},
	}
	merge := []wire.Component{
		{Type: wire.ComponentField, Name: "type_merge", Label: t.s("把这些", "These"), Kind: wire.FieldKindMultichoice, Choices: choices},
		{Type: wire.ComponentField, Name: "type_into", Label: t.s("合进", "into"), Kind: wire.FieldKindChoice, Choices: choices, Value: names[0]},
		{Type: wire.ComponentButton, Title: t.s("合并", "Merge"), Action: &wire.Action{ID: "type:merge:" + string(f), Submit: true}},
	}
	return &wire.View{Title: title, Sections: []wire.Section{{Items: rows}, {Title: t.s("改名", "Rename"), Items: edit},
		{Title: t.s("合并", "Merge"), Footer: t.s("类型从你自己的数据里长出来；改名、合并后条目跟着走，以后同类的也归到新名下。", "Types grow from your own data; renamed or merged, their entries follow and later ones go under the new name."), Items: merge}}}
}

// entryPage is one entry: its text (editable), facts, structure, evidence, counter-examples, members, marks and exits.
func (a *app) entryPage(t tr, st *store.State, id string) *wire.View {
	e := st.Entries[id]
	if e == nil {
		return &wire.View{Title: t.s("不见了", "Gone"), Sections: []wire.Section{{Items: []wire.Component{empty(t.s("这一条不见了", "This entry is gone"))}}}}
	}
	exits := exitIndex(a.exitsDir())[e.ID]
	info := []wire.Component{
		{Type: wire.ComponentField, Name: "text", Label: t.facetLabel(e.Facet), Kind: wire.FieldKindMultiline, Value: e.Shown()},
	}
	if e.User != nil {
		note := t.s("这是你改过的版本，整理不会覆盖它。", "This is your own wording; curation never overwrites it.")
		if e.Updated > e.User.At {
			note += t.s("你改过以后又有了新依据：原来的整理结果是「", " New evidence came after your edit: the curated text is \"") + short(e.Text, 80) + t.s("」。", "\".")
		}
		info = append(info, secondary(note))
	}
	facts := []string{t.grainLabel(e.Grain), t.statusLabel(e.Status)}
	if e.Type != "" {
		facts = append(facts, e.Type)
	}
	facts = append(facts, t.f("%d 次", "seen %d times", e.Count))
	if e.Chances > 0 {
		facts = append(facts, t.f("同类情境出现 %d 次", "the situation came up %d times", e.Chances))
	}
	if len(e.ActiveDays) > 0 {
		facts = append(facts, t.f("%d 个活跃天", "%d active days", len(e.ActiveDays)))
	}
	var kinds []string
	for _, k := range e.Kinds {
		kinds = append(kinds, t.sourceLabel(k, ""))
	}
	if len(kinds) > 0 {
		facts = append(facts, t.s("来源：", "sources: ")+strings.Join(kinds, "、"))
	}
	if e.First != "" {
		facts = append(facts, e.First+" → "+e.Last)
	}
	switch e.Origin {
	case entry.FromOnboarding:
		facts = append(facts, t.s("你在首次引导里说的", "from your first-run answers"))
	case entry.SelfReport:
		facts = append(facts, t.s("你在「关于我」里写的", "from your \"About me\""))
	case entry.Handwritten:
		facts = append(facts, t.s("助手的手写记忆", "the assistant's hand-written memory"))
	}
	info = append(info, secondary(strings.Join(facts, " · ")))
	var flags []string
	if e.Inferred {
		flags = append(flags, t.s("推断：不是你明说的", "inferred: not said outright"))
	}
	if e.ReportedOnly {
		flags = append(flags, t.s("只来自别人的转述", "rests only on what others said"))
	}
	if e.SingleSource {
		flags = append(flags, t.s("目前只有一个来源", "one source so far"))
	}
	if e.Sensitive != "" {
		flags = append(flags, t.f("敏感类别「%s」：只进你自己的理解，不进任何出口", "sensitive (%s): kept for your own understanding only, never in an exit", t.sensitiveLabel(e.Sensitive)))
	}
	if e.Domain == entry.Life {
		flags = append(flags, t.s("生活类", "personal life"))
	}
	if len(flags) > 0 {
		info = append(info, toned(strings.Join(flags, " · "), wire.ToneCaution))
	}
	if len(exits) > 0 {
		var xs []string
		for _, x := range exits {
			xs = append(xs, t.exitLabel(x))
		}
		info = append(info, secondary(t.s("在出口：", "In exits: ")+strings.Join(xs, "、")))
	} else {
		info = append(info, secondary(t.s("不在任何出口里（只在这个页面上）", "In no exit (only on this page)")))
	}
	if e.Export != nil && e.Export.Text != "" {
		info = append(info, secondary(t.s("写给助手的说法：", "As written for the assistant: ")+e.Export.Text))
	}
	if e.Impact.Action != "" {
		info = append(info, secondary(t.f("会让助手在「%s」时：%s", "Changes what the assistant does at %s: %s", e.Impact.Moment, e.Impact.Action)))
	}
	var detail []string
	add := func(zh, en, v string) {
		if v != "" {
			detail = append(detail, t.s(zh, en)+v)
		}
	}
	add("情境：", "Situation: ", e.Situation)
	add("选了：", "Chose: ", e.Choice)
	add("没选：", "Not chosen: ", e.Rejected)
	add("理由：", "Why: ", e.Why)
	add("例外：", "Except: ", e.Exception)
	add("对象：", "Object: ", e.Object)
	add("坑：", "Pitfall: ", e.Pitfall)
	add("绕法：", "Way round: ", e.Fix)
	add("一般人会：", "Most people would: ", e.Default)
	if len(e.Situations) > 0 {
		add("管的情境：", "Situations: ", strings.Join(e.Situations, "、"))
	}
	if cm := e.Commitment; cm != nil {
		add("承诺：", "Promise: ", fmt.Sprintf("%s → %s：%s", personName(st, cm.From, t), personName(st, cm.To, t), cm.What))
		add("到期：", "Due: ", cm.Due)
		add("完成：", "Done: ", cm.Done)
		if slices.Contains(e.Flags, curate.PossiblyOverdue) {
			detail = append(detail, t.s("可能逾期（插件不自动关）", "possibly overdue (never closed by the curator)"))
		}
	}
	if e.Person != "" {
		add("关于：", "About: ", personName(st, e.Person, t))
	}
	for _, sc := range e.Scopes {
		add("范围：", "Scope: ", sc.Kind+" "+sc.Name)
	}
	v := &wire.View{Title: t.facetLabel(e.Facet), Sections: []wire.Section{{Items: info}}}
	if len(detail) > 0 {
		var items []wire.Component
		for _, d := range detail {
			items = append(items, secondary(d))
		}
		v.Sections = append(v.Sections, wire.Section{Items: items})
	}
	acts := []wire.Component{{Type: wire.ComponentButton, Title: t.s("按上面改过的保存", "Save the edited text"), Action: &wire.Action{ID: "act:edit:" + e.ID, Submit: true}}}
	if e.Pinned {
		acts = append(acts, button(t.s("取消钉住", "Unpin"), "act:unpin:"+e.ID, ""))
	} else {
		acts = append(acts, button(t.s("钉住", "Pin"), "act:pin:"+e.ID, ""))
	}
	if e.Deleted {
		acts = append(acts, button(t.s("恢复", "Restore"), "act:restore:"+e.ID, ""))
	} else {
		acts = append(acts, button(t.s("删掉", "Delete"), "act:delete:"+e.ID, t.s("删掉后同样的内容不再生成；可以恢复。确定？", "Deleted, the same content is not made again; it can be restored. Sure?")))
	}
	v.Sections = append(v.Sections, wire.Section{Items: acts},
		wire.Section{Title: t.s("对不对", "Is it right?"), Footer: t.s("点「不对」立刻撤出出口，记成反例。", "\"Wrong\" withdraws it from every exit at once and keeps it as a counter-example."),
			Items: []wire.Component{verdictSeg(t, "m:"+e.ID, lastMark(e.Verdicts), false)}})
	v.Sections = append(v.Sections, wire.Section{Title: t.s("出处", "Evidence"), Items: evidenceRows(t, entryEvidence(st, e))})
	if len(e.Counters) > 0 {
		var items []wire.Component
		for _, id := range e.Counters {
			if x := st.Entries[id]; x != nil {
				items = append(items, entryRow(t, x, nil, page{Tab: tabFacets, Facet: string(x.Facet)}))
			}
		}
		v.Sections = append(v.Sections, wire.Section{Title: t.s("反例", "Counter-examples"), Items: items})
	}
	if len(e.Members) > 0 {
		var items []wire.Component
		for i, id := range e.Members {
			if i >= 30 {
				items = append(items, secondary(t.f("还有 %d 条", "%d more", len(e.Members)-i)))
				break
			}
			if x := st.Entries[id]; x != nil {
				items = append(items, entryRow(t, x, nil, page{Tab: tabFacets, Facet: string(x.Facet)}))
			}
		}
		v.Sections = append(v.Sections, wire.Section{Title: t.s("依据的下一层条目", "Resting on"), Items: items})
	}
	return v
}

func evidenceRows(t tr, ev []entry.Evidence) []wire.Component {
	var out []wire.Component
	for i, x := range ev {
		if i >= 12 {
			out = append(out, secondary(t.f("还有 %d 处", "%d more", len(ev)-i)))
			break
		}
		who := x.Speaker
		if x.Who == entry.WhoSelf {
			who = t.s("你", "you")
		} else if who == "" {
			who = t.s("认不出说话人", "speaker unknown")
		}
		sub := strings.Join(nonEmpty(t.sourceLabel(x.Source, ""), x.Day, who), " · ")
		if x.Reported {
			sub += t.s(" · 转述（别人说起的）", " · reported (someone else speaking of them)")
		}
		out = append(out, row("「"+short(x.Quote, 120)+"」", sub, nil, nil))
	}
	if len(out) == 0 {
		out = append(out, secondary(t.s("没有原话", "No quote")))
	}
	return out
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" && x != " " {
			out = append(out, x)
		}
	}
	return out
}

// personName is a person's display name: the list's, else the card's, else the id.
func personName(st *store.State, who string, t tr) string {
	if who == entry.WhoSelf {
		return t.s("你", "you")
	}
	if c := st.Contacts[who]; c != nil && c.Name != "" {
		return c.Name
	}
	return strings.TrimPrefix(who, "person:")
}

// ---- people (5.7) ---------------------------------------------------------------------------------------------------

// mergeOf is the merge a person is in, if any.
func mergeOf(st *store.State, who string) *entry.PersonMerge {
	for i := range st.PersonMerges {
		if slices.Contains(st.PersonMerges[i].Persons, who) {
			return &st.PersonMerges[i]
		}
	}
	return nil
}

// cardLines are the people entries of the persons of one card (a merge counts as one).
func cardLines(st *store.State, persons []string) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range st.Entries {
		if e.Facet == entry.People && slices.Contains(persons, e.Person) && e.MergedInto == "" && e.Status != entry.Retired {
			out = append(out, e)
		}
	}
	sortEntries(out)
	return out
}

func evidenceCount(es []*entry.Entry) int {
	n := 0
	for _, e := range es {
		if e.Live() {
			n += len(e.Evidence)
		}
	}
	return n
}

// fewEvidence: a card or line resting on fewer than this many quotes says so.
const fewEvidence = 3

func (a *app) peopleTab(t tr, st *store.State) []wire.Section {
	var out []wire.Section
	head := []wire.Component{secondary(t.s(
		"画像卡只存在你自己的知识库和这里。起草回复时只用来调语气和重点，评价和推断不会写进发出去的内容；助手读到人物页时，标了「推断」「评价」的只供参考、不对外转述。",
		"Cards live only in your own knowledge base and here. When a reply is drafted they only tune tone and emphasis; evaluations and inferences never go into what is sent, and the assistant treats lines marked inferred or evaluation as reference only, not to be passed on."))}
	head = append(head, a.profileNotice(t, st)...)
	if !entry.On(st.Switches, entry.K14People) {
		head = append(head, toned(t.s("画像卡整块已关（K14）：只留在这里，不写进知识库和收件箱。", "People cards are off as a whole (K14): kept here only, not written to the knowledge base or the inbox."), wire.ToneCaution))
	}
	if !entry.On(st.Switches, entry.K15Inferred) {
		head = append(head, toned(t.s("推断侧写已关（K15）：卡上只留事实、你说过的评价和承诺。", "Inferred profiles are off (K15): cards keep facts, what you said and commitments only."), wire.ToneCaution))
	}
	out = append(out, wire.Section{Items: head})

	ranked := people.Ranked(st.Contacts)
	seen := map[string]bool{}
	var rows []wire.Component
	var mergeChoices []wire.Choice
	for _, c := range ranked {
		if seen[c.Person] {
			continue
		}
		persons := []string{c.Person}
		title := c.Name
		if title == "" {
			title = strings.TrimPrefix(c.Person, "person:")
		}
		if m := mergeOf(st, c.Person); m != nil {
			persons = m.Persons
			var names []string
			for _, p := range m.Persons {
				names = append(names, personName(st, p, t))
				seen[p] = true
			}
			title = strings.Join(names, " + ")
			if m.Name != "" {
				title = m.Name + "（" + title + "）"
			}
		}
		seen[c.Person] = true
		if len(mergeChoices) < 40 && mergeOf(st, c.Person) == nil {
			mergeChoices = append(mergeChoices, wire.Choice{Value: c.Person, Title: title})
		}
		if len(rows) >= 160 {
			continue
		}
		lines := cardLines(st, persons)
		var ties []string
		for _, k := range people.Ties {
			if n := c.Ties[k]; n > 0 {
				ties = append(ties, t.tieLabel(k)+fmt.Sprintf(" %d", n))
			}
		}
		parts := map[string]int{}
		for _, e := range lines {
			if e.Live() {
				parts[e.Part]++
			}
		}
		sub := strings.Join(ties, " · ")
		sub2 := t.f("事实 %d · 你的评价 %d · 推断 %d · 承诺 %d", "%d facts · %d of your words · %d inferred · %d commitments",
			parts[entry.PartFact], parts[entry.PartUserEval], parts[entry.PartInferred], parts[entry.PartCommitment])
		if c.Manual == people.Added {
			sub2 += t.s(" · 你加的", " · added by you")
		}
		var badge *wire.Badge
		if n := evidenceCount(lines); n < fewEvidence {
			badge = &wire.Badge{Text: t.f("证据少（%d 条）", "little evidence (%d)", n), Tone: wire.ToneCaution}
		}
		rows = append(rows, row(title, sub, badge, ext.Open("open:card", "", page{Tab: tabPeople, Open: "card:" + c.Person}, title)), caption(sub2))
	}
	if len(rows) == 0 {
		rows = append(rows, empty(t.s("交集名单还是空的：接一个消息来源后，和你有来往的人会列在这里", "The list is empty: once a message source is connected, the people you deal with are listed here")))
	}
	out = append(out, wire.Section{Title: t.f("和你有来往的人（%d）", "People you deal with (%d)", len(ranked)),
		Footer: t.s("按来往多少排：私聊、群里来回、邮件、同会、承诺。同一个人在不同来源是不同的人物条目，可以在下面合成一个人（只记在插件里，不改知识库，可以拆开）。", "Ordered by dealings: direct chats, group exchanges, mail, meetings, commitments. One person in several sources is several person entries; merge them below (kept in the curator only, never written to the knowledge base, undoable)."),
		Items:  rows})

	// Merge cards, split merges.
	var merge []wire.Component
	if len(mergeChoices) >= 2 {
		merge = append(merge,
			wire.Component{Type: wire.ComponentField, Name: "merge_people", Label: t.s("这几张卡是同一个人", "These cards are one person"), Kind: wire.FieldKindMultichoice, Choices: mergeChoices},
			wire.Component{Type: wire.ComponentField, Name: "merge_name", Label: t.s("叫什么（可不填）", "Name (optional)"), Kind: wire.FieldKindText},
			wire.Component{Type: wire.ComponentButton, Title: t.s("合成一个人", "Merge into one person"), Action: &wire.Action{ID: "people:merge", Submit: true}})
	}
	for _, m := range st.PersonMerges {
		var names []string
		for _, p := range m.Persons {
			names = append(names, personName(st, p, t))
		}
		merge = append(merge, row(strings.Join(names, " + "), m.At, nil, nil), button(t.s("拆开", "Split"), "people:split:"+m.ID, ""))
	}
	// Add someone not on the list: a person the cards know of, or one the user removed.
	var addChoices []wire.Choice
	known := map[string]bool{}
	for _, e := range st.Entries {
		if e.Facet == entry.People && e.Person != "" && !people.On(st.Contacts, e.Person) && !known[e.Person] {
			known[e.Person] = true
		}
	}
	for who, c := range st.Contacts {
		if c.Manual == people.Removed {
			known[who] = true
		}
	}
	for _, who := range sortedKeys(known) {
		if len(addChoices) >= 40 {
			break
		}
		addChoices = append(addChoices, wire.Choice{Value: who, Title: personName(st, who, t)})
	}
	if len(addChoices) > 0 {
		merge = append(merge,
			wire.Component{Type: wire.ComponentField, Name: "add_person", Label: t.s("加到名单", "Add to the list"), Kind: wire.FieldKindChoice, Choices: addChoices, Value: addChoices[0].Value},
			wire.Component{Type: wire.ComponentButton, Title: t.s("加上", "Add"), Action: &wire.Action{ID: "people:add", Submit: true}})
	}
	if len(merge) > 0 {
		out = append(out, wire.Section{Title: t.s("合并、拆开、加人", "Merge, split, add"), Items: merge})
	}
	return out
}

// cardPage is one person's card (5.7): facts, the user's own words about them, the inferred profile (labelled, with
// evidence; little evidence and reported-only marked), and commitments either way. Lines resting only on what others
// said are folded until opened.
func (a *app) cardPage(t tr, st *store.State, s page, who string) *wire.View {
	persons := []string{who}
	m := mergeOf(st, who)
	if m != nil {
		persons = m.Persons
	}
	var names []string
	for _, p := range persons {
		names = append(names, personName(st, p, t))
	}
	title := strings.Join(names, " + ")
	v := &wire.View{Title: title}
	var head []wire.Component
	if c := st.Contacts[who]; c != nil {
		var ties []string
		for _, k := range people.Ties {
			if n := c.Ties[k]; n > 0 {
				ties = append(ties, t.tieLabel(k)+fmt.Sprintf(" %d", n))
			}
		}
		if len(ties) > 0 {
			head = append(head, secondary(strings.Join(ties, " · ")+" · "+c.First+" → "+c.Last))
		}
	}
	if people.On(st.Contacts, who) {
		head = append(head, button(t.s("移出名单", "Remove from the list"), "people:remove:"+who, t.s("移出后不再给他整理画像；卡上已有的留着。确定？", "Removed, no card is curated for them any more; what is there stays. Sure?")))
	} else {
		head = append(head, button(t.s("加到名单", "Add to the list"), "people:addone:"+who, ""))
	}
	if m != nil {
		head = append(head, button(t.s("拆开这几张卡", "Split these cards"), "people:split:"+m.ID, ""))
	}
	for _, p := range persons {
		head = append(head, profileDeliveryLine(t, st, p, len(persons) > 1))
	}
	v.Sections = append(v.Sections, wire.Section{Items: head})
	all := cardLines(st, persons)
	exits := exitIndex(a.exitsDir())
	back := page{Tab: tabPeople, Open: "card:" + who}
	var reported []*entry.Entry
	for _, part := range []string{entry.PartFact, entry.PartUserEval, entry.PartInferred, entry.PartCommitment} {
		var items []wire.Component
		if part == entry.PartInferred && !entry.On(st.Switches, entry.K15Inferred) {
			items = append(items, secondary(t.s("推断侧写已关（K15）。", "Inferred profiles are off (K15).")))
		}
		for _, e := range all {
			if e.Part != part || e.Deleted {
				continue
			}
			if e.ReportedOnly && s.Show != "reported" {
				reported = append(reported, e)
				continue
			}
			r := entryRow(t, e, exits[e.ID], back)
			if n := len(e.Evidence); part == entry.PartInferred && n < fewEvidence {
				r.Subtitle += t.f(" · 证据少（%d 条）", " · little evidence (%d)", n)
			}
			if part == entry.PartUserEval {
				r.Subtitle += t.s(" · 只在你自己的理解里", " · for your own understanding only")
			}
			items = append(items, r)
		}
		if len(items) == 0 {
			items = append(items, secondary(t.s("还没有", "None yet")))
		}
		foot := ""
		switch part {
		case entry.PartInferred:
			foot = t.s("一律标「推断」、带证据；只来自转述的单独标出。", "Always marked inferred, with evidence; what rests only on others' words is marked so.")
		case entry.PartCommitment:
			foot = t.s("到期前进周报；过期没完成证据的标「可能逾期」，不自动关。", "Due ones go into the weekly report; past due with no sign of being done they are marked possibly overdue, never closed by themselves.")
		}
		v.Sections = append(v.Sections, wire.Section{Title: t.partLabel(part), Footer: foot, Items: items})
	}
	if len(reported) > 0 {
		p := s
		p.Show = "reported"
		v.Sections = append(v.Sections, wire.Section{Title: t.s("只来自转述（默认折叠）", "Reported by others only (folded)"),
			Items: []wire.Component{row(t.f("别人说起他的 %d 条：点开看，每条标着是谁说的", "%d lines others said about them: open to see, each with who said it", len(reported)), "", nil, ext.Open("open:reported", "", p, title))}})
	}
	return v
}

// entryEvidence is what an entry rests on as the page shows it: its own quotes, or for a class or abstract entry (which
// carry none of their own) the quotes of the entries under it, a level at a time.
func entryEvidence(st *store.State, e *entry.Entry) []entry.Evidence {
	if len(e.Evidence) > 0 {
		return e.Evidence
	}
	var out []entry.Evidence
	seen := map[string]bool{e.ID: true}
	level := e.Members
	for len(level) > 0 && len(out) < 12 {
		var next []string
		for _, id := range level {
			m := st.Entries[id]
			if m == nil || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, m.Evidence...)
			next = append(next, m.Members...)
		}
		level = next
	}
	return out
}

// profileNotice is what the people tab says of the cards reaching the knowledge base, as the hub's probe and the
// privacy release stand.
func (a *app) profileNotice(t tr, st *store.State) []wire.Component {
	released := a.c != nil && a.c.ProfilesReleased()
	failed, written := 0, 0
	targets := map[string]bool{}
	for _, p := range st.Proposals {
		if p.Kind != "person_card" || targets[p.Target] {
			continue
		}
		targets[p.Target] = true
		switch d := curate.ProfileDeliveryIn(st, strings.TrimPrefix(p.Target, "people/")); d.Status {
		case "readback_failed":
			failed++
		case "written":
			written++
		}
	}
	// Two things gate what reaches people pages: the hub taking profiles at all (probed), and the hub's privacy filter
	// that gives the inbox and the Lark agent only facts and commitments (curate.ProfileViewsWritten). Without the
	// filter only facts and commitments are written; evaluations and inferences stay here, never in the pinned facts.
	held := toned(t.s("你的评价和推断的侧写目前只在插件页能看：hub 还没有让收件箱和飞书 agent 只读到事实和承诺的隐私过滤，在那之前不写进人物页，助手查人物页时也拿不到。",
		"Your evaluations and the inferred profile are on the plugin page only: until the hub has the privacy filter that gives the inbox and the Lark agent only facts and commitments, they are not written to people pages, and the assistant does not get them when it looks a person up."), wire.ToneCaution)
	views := curate.ProfileViewsWritten()
	switch {
	case !released:
		return []wire.Component{toned(t.s("目前写进知识库人物页的只有卡上的事实（等 hub 支持分区块的人物画像）；你的评价和推断的侧写只在插件页能看，助手查人物页时还拿不到。",
			"For now only a card's facts are written to the knowledge base's people pages (waiting for the hub to take sectioned people profiles); your evaluations and the inferred profile can be seen only on the plugin page, and the assistant does not get them when it looks a person up."), wire.ToneCaution)}
	case failed > 0:
		return []wire.Component{toned(t.f("这个 hub 收了 %d 份人物画像却回读不到，当作还不支持，第二天再试；评价和侧写仍只在插件页能看。",
			"This hub took %d people profiles and does not show them when read back, so it is taken as not supporting them; tried again the next day. Evaluations and the inferred profile stay on the plugin page.", failed), wire.ToneCaution)}
	case !views:
		return []wire.Component{secondary(t.s("hub 已支持人物画像：卡上的事实和承诺按提议流程写进人物页，每条带出处。",
			"The hub takes people profiles: a card's facts and commitments are written to people pages through the proposals, each with where it comes from.")), held}
	case written == 0:
		return []wire.Component{secondary(t.s("hub 已支持人物画像：卡会按提议流程分四部分（事实、你的原话、推断、承诺）写进人物页，写进去并回读到之后这句说明就不再显示。",
			"The hub takes people profiles now: cards are written to people pages in four parts (facts, your words, inferences, commitments) through the proposals; this note goes once one is written and read back."))}
	}
	return []wire.Component{secondary(t.s("画像卡分四部分（事实、你的原话、推断、承诺）写进人物页，每条带出处；推断标「仅供参考」。",
		"Cards are written to people pages in four parts (facts, your words, inferences, commitments), each line with where it comes from; inferences are marked as reference only."))}
}

// profileDeliveryLine says whether a person entry's card reached its person page.
func profileDeliveryLine(t tr, st *store.State, person string, named bool) wire.Component {
	pid := people.PersonID(person)
	d := curate.ProfileDeliveryIn(st, pid)
	var text string
	tone := wire.ToneCaution
	switch d.Status {
	case "written":
		text, tone = t.f("已写进人物页并回读确认（%s，%s）", "Written to the person page and read back (%s, %s)", d.Day, d.Applied), ""
	case "blocked":
		text = t.s("还没写进人物页：等 hub 支持人物画像，评价和侧写目前只在插件页能看", "Not on the person page yet: waiting for the hub to take people profiles; evaluations and the inferred profile are on the plugin page only")
	case "readback_failed":
		text = t.s("写过但 hub 回读不到，当作还不支持，等 hub 升级再试", "Written, but the hub does not show it when read back: taken as not supported, tried again after a hub upgrade")
	case "shadow":
		text = t.s("影子期：只提议，不写进人物页", "Shadow period: proposed only, not written")
	case "pending":
		text = t.s("等你在提议里确认后写进人物页", "Waiting for you to accept the proposal")
	case "undone":
		text = t.f("写过，已撤销（%s）", "Written, then undone (%s)", d.Day)
	case "rejected":
		text = t.s("你没让写进人物页", "You chose not to write it")
	default:
		text, tone = t.s("还没有可写进人物页的画像", "No profile to write yet"), ""
	}
	if named {
		text = personName(st, person, t) + "：" + text
	}
	if tone == "" {
		return secondary(text)
	}
	return toned(text, tone)
}
