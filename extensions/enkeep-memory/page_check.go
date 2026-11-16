package main

// The check page (design 9.3, 9.5): U1–U6 at most ten entries of each facet (at least two abstract ones when there
// are), U7 the cards of the ten people dealt with most, line by line, U8 thirty attributed lines a source ("is this
// by X?"), and F1 thirty facts against the words they cite. "Wrong" withdraws an entry from the exits at once.

import (
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
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Check parts: U1…U6 map to the facets ①…⑥.
var uFacets = map[string]entry.Facet{"u1": entry.Interaction, "u2": entry.Tools, "u3": entry.Judgement, "u4": entry.Workflow,
	"u5": entry.World, "u6": entry.Persona}

const (
	checkPerFacet  = 10
	checkAbstract  = 2
	checkCards     = 10
	checkSpeakers  = curate.SpeakerSample // 30 a source
	checkFacts     = 30
	checkCardLines = 60
)

// checkable: an entry that may be put to the user: not deleted, merged, retired or invalidated (a wrong mark keeps it
// on the page, showing the mark).
func checkable(e *entry.Entry) bool {
	return !e.Deleted && e.MergedInto == "" && e.Status != entry.Retired && e.ValidTo == ""
}

// pickFacet is U1–U6's choice: the facet's first ten by count × kinds of source, at least two abstract ones among
// them when there are.
func pickFacet(st *store.State, f entry.Facet) []*entry.Entry {
	var abs, rest []*entry.Entry
	for _, e := range st.Entries {
		if e.Facet != f || !checkable(e) {
			continue
		}
		if e.Grain == entry.Abstract {
			abs = append(abs, e)
		} else {
			rest = append(rest, e)
		}
	}
	sortEntries(abs)
	sortEntries(rest)
	var out []*entry.Entry
	out = append(out, abs[:min(checkAbstract, len(abs))]...)
	all := append(abs[min(checkAbstract, len(abs)):], rest...)
	sortEntries(all)
	for _, e := range all {
		if len(out) >= checkPerFacet {
			break
		}
		out = append(out, e)
	}
	return out
}

// pickCards is U7's choice: the people dealt with most, one card per person (a merge counts once).
func pickCards(st *store.State) [][]string {
	var out [][]string
	seen := map[string]bool{}
	for _, c := range people.Ranked(st.Contacts) {
		if seen[c.Person] {
			continue
		}
		persons := []string{c.Person}
		if m := mergeOf(st, c.Person); m != nil {
			persons = m.Persons
		}
		for _, p := range persons {
			seen[p] = true
		}
		if len(cardLines(st, persons)) == 0 {
			continue
		}
		out = append(out, persons)
		if len(out) >= checkCards {
			break
		}
	}
	return out
}

// pickFacts is F1's sample: thirty facts (⑤ single facts and the facts on the cards) with a quote, drawn by a hash of
// the two-week period so the same ones stay put for two weeks.
func pickFacts(st *store.State, today string) []*entry.Entry {
	period := today
	if t, err := time.Parse("2006-01-02", today); err == nil {
		period = fmt.Sprint(t.Unix() / 86400 / 14)
	}
	var cand []*entry.Entry
	for _, e := range st.Entries {
		if !checkable(e) || len(e.Evidence) == 0 || e.Evidence[0].Quote == "" {
			continue
		}
		if (e.Facet == entry.World && e.Grain == entry.Action) || (e.Facet == entry.People && e.Part == entry.PartFact) {
			cand = append(cand, e)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return hashKey(period, cand[i].ID) < hashKey(period, cand[j].ID) })
	return cand[:min(checkFacts, len(cand))]
}

// speakerSources are the sources with attribution checks, and their checks: the latest first, at most thirty.
func speakerSources(st *store.State) (names []string, by map[string][]entry.SpeakerCheck) {
	by = map[string][]entry.SpeakerCheck{}
	for _, c := range st.SpeakerChecks {
		by[c.Source] = append(by[c.Source], c)
	}
	for k, cs := range by {
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].Day != cs[j].Day {
				return cs[i].Day > cs[j].Day
			}
			if cs[i].Ref != cs[j].Ref {
				return cs[i].Ref < cs[j].Ref
			}
			return cs[i].N < cs[j].N
		})
		by[k] = cs[:min(checkSpeakers, len(cs))]
		names = append(names, k)
	}
	sort.Strings(names)
	return names, by
}

func speakerKey(c entry.SpeakerCheck) string { return "s:" + hashKey(c.Ref, c.Day, fmt.Sprint(c.N)) }

// tally is "right n of m marked (p%)", or "not marked yet".
func tally(t tr, marks []string) string {
	right, marked := 0, 0
	for _, m := range marks {
		switch m {
		case "right":
			right++
			marked++
		case "wrong", "unsupported":
			marked++
		}
	}
	if marked == 0 {
		return t.f("%d 条，还没判", "%d to mark, none marked yet", len(marks))
	}
	return t.f("判了 %d / %d 条，「对」%d 条（%.0f%%）", "%d of %d marked, %d right (%.0f%%)", marked, len(marks), right, float64(right)*100/float64(marked))
}

func checkChoices(t tr) []wire.Choice {
	out := []wire.Choice{}
	for _, k := range []string{"u1", "u2", "u3", "u4", "u5", "u6"} {
		out = append(out, wire.Choice{Value: k, Title: strings.ToUpper(k) + " " + facetShort(uFacets[k])})
	}
	return append(out, wire.Choice{Value: "u7", Title: t.s("U7 画像卡", "U7 cards")}, wire.Choice{Value: "u8", Title: t.s("U8 谁说的", "U8 speakers")},
		wire.Choice{Value: "f1", Title: t.s("F1 事实", "F1 facts")})
}

func (a *app) checkPage(t tr, st *store.State, s page) ([]wire.Section, *wire.Filter) {
	part := s.Check
	if part == "" {
		part = "u1"
	}
	filter := &wire.Filter{Name: "check", Title: t.s("核对哪部分", "Part"), Choices: checkChoices(t), Value: part, Action: wire.Action{ID: "check"}}
	var out []wire.Section
	switch {
	case uFacets[part] != "":
		out = a.checkFacet(t, st, part)
	case part == "u7":
		out = a.checkCards(t, st, s)
	case part == "u8":
		out = a.checkSpeakers(t, st, s)
	case part == "f1":
		out = a.checkFacts(t, st)
	}
	out = append([]wire.Section{{Items: []wire.Component{secondary(t.s(
		"点「对 / 不对 / 说不清」。点「不对」的条目立刻撤出所有出口，记成反例。",
		"Mark right, wrong or not sure. \"Wrong\" takes the entry out of every exit at once and keeps it as a counter-example."))}}}, out...)
	return out, filter
}

// checkItems are one entry's row, its first quote and its control.
func checkItems(t tr, e *entry.Entry, f1 bool) []wire.Component {
	sub := entrySub(t, e, nil)
	items := []wire.Component{row(short(e.Shown(), 120), sub, entryBadge(t, e), ext.Open("open:"+e.ID, "", page{Tab: tabFacets, Facet: string(e.Facet), Open: "e:" + e.ID}, short(e.Shown(), 20)))}
	n := 1
	if f1 {
		n = 2
	}
	for i, x := range e.Evidence {
		if i >= n {
			break
		}
		who := x.Speaker
		if x.Who == entry.WhoSelf {
			who = t.s("你", "you")
		}
		items = append(items, secondary(fmt.Sprintf("「%s」 %s", short(x.Quote, 140), strings.Join(nonEmpty(t.sourceLabel(x.Source, ""), x.Day, who), " · "))))
	}
	return append(items, verdictSeg(t, "m:"+e.ID, lastMark(e.Verdicts), f1))
}

func (a *app) checkFacet(t tr, st *store.State, part string) []wire.Section {
	f := uFacets[part]
	es := pickFacet(st, f)
	title := strings.ToUpper(part) + " " + t.facetLabel(f)
	if len(es) == 0 {
		return []wire.Section{{Title: title, Items: []wire.Component{empty(t.s("还没攒出来", "Nothing collected yet"))}}}
	}
	var items []wire.Component
	var marks []string
	for _, e := range es {
		items = append(items, checkItems(t, e, false)...)
		marks = append(marks, lastMark(e.Verdicts))
	}
	return []wire.Section{{Title: title, Footer: tally(t, marks) + t.s("。「对」≥70% 算过。", ". Passes at 70% right."), Items: items}}
}

func (a *app) checkCards(t tr, st *store.State, s page) []wire.Section {
	cards := pickCards(st)
	if len(cards) == 0 {
		return []wire.Section{{Title: t.s("U7 画像卡", "U7 people cards"), Items: []wire.Component{empty(t.s("还没攒出来", "Nothing collected yet"))}}}
	}
	if s.Sub != "" {
		for _, persons := range cards {
			if persons[0] != s.Sub {
				continue
			}
			var names []string
			for _, p := range persons {
				names = append(names, personName(st, p, t))
			}
			var out []wire.Section
			n := 0
			for _, part := range []string{entry.PartFact, entry.PartUserEval, entry.PartInferred, entry.PartCommitment} {
				var items []wire.Component
				var marks []string
				for _, e := range cardLines(st, persons) {
					if e.Part != part || !checkable(e) {
						continue
					}
					if n >= checkCardLines {
						break
					}
					n++
					items = append(items, checkItems(t, e, false)...)
					marks = append(marks, lastMark(e.Verdicts))
				}
				if len(items) == 0 {
					continue
				}
				out = append(out, wire.Section{Title: t.partLabel(part), Footer: tally(t, marks), Items: items})
			}
			if len(out) == 0 {
				out = append(out, wire.Section{Items: []wire.Component{empty(t.s("这张卡上还没有内容", "Nothing on this card yet"))}})
			}
			out[0].Title = strings.Join(names, " + ") + " · " + out[0].Title
			out = append(out, wire.Section{Items: []wire.Component{secondary(t.s(
				"卡上有你不想看到的（敏感类别、转述没标来源）：把那一条点「不对」，再到画像卡上删掉。",
				"Anything on the card you do not want there (a sensitive category, something reported without saying by whom): mark it wrong, then delete it on the card."))}})
			return out
		}
	}
	var rows []wire.Component
	parts := map[string][]string{}
	for _, persons := range cards {
		var names []string
		for _, p := range persons {
			names = append(names, personName(st, p, t))
		}
		var marks []string
		for _, e := range cardLines(st, persons) {
			if checkable(e) {
				m := lastMark(e.Verdicts)
				marks = append(marks, m)
				parts[e.Part] = append(parts[e.Part], m)
			}
		}
		rows = append(rows, row(strings.Join(names, " + "), tally(t, marks), nil,
			ext.Open("open:u7", "", page{Tab: tabCheck, Check: "u7", Sub: persons[0]}, strings.Join(names, " + "))))
	}
	var sum []string
	for _, p := range []string{entry.PartFact, entry.PartUserEval, entry.PartInferred, entry.PartCommitment} {
		if len(parts[p]) > 0 {
			sum = append(sum, t.partLabel(p)+"："+tally(t, parts[p]))
		}
	}
	return []wire.Section{{Title: t.f("U7 来往最多的 %d 个人的画像卡", "U7 cards of the %d people you deal with most", len(cards)),
		Footer: strings.Join(sum, "\n") + t.s("\n推断「对」≥70%、事实「对」≥90% 算过。", "\nPasses at 70% right for inferred lines and 90% for facts."), Items: rows}}
}

func (a *app) checkSpeakers(t tr, st *store.State, s page) []wire.Section {
	names, by := speakerSources(st)
	if len(names) == 0 {
		return []wire.Section{{Title: t.s("U8 说话人归属", "U8 who said it"), Items: []wire.Component{empty(t.s(
			"还没攒出来：接了消息来源、整理过一天后，每个来源会抽 30 句", "Nothing collected yet: after a day with a message source, 30 lines a source are drawn"))}}}
	}
	src := s.Sub
	if !slices.Contains(names, src) {
		src = ""
	}
	if src == "" {
		var rows []wire.Component
		for _, n := range names {
			var marks []string
			for _, c := range by[n] {
				m := ""
				if c.Verdict != nil {
					m = c.Verdict.Mark
				}
				marks = append(marks, m)
			}
			sub := tally(t, marks)
			if !curate.LearnPeople(st, n) {
				sub += t.s(" · 已停学闲聊里的人物和关系", " · not learning people from chit-chat")
			}
			rows = append(rows, row(t.sourceLabel(entry.SrcChat, n), sub, nil, ext.Open("open:u8", "", page{Tab: tabCheck, Check: "u8", Sub: n}, n)))
		}
		return []wire.Section{{Title: t.s("U8 说话人归属：每个来源 30 句", "U8 who said it: 30 lines a source"),
			Footer: t.s("≥95% 算过；不过的来源先不从闲聊里学人物和关系（K16），有一句别人的话被算成你说的也不过。", "Passes at 95%; a source that fails stops teaching people and relations from chit-chat (K16), and so does one where someone else's line was put on you."), Items: rows}}
	}
	var items []wire.Component
	var marks []string
	pj := readPrejudge(a.c.St.Dir)
	for _, c := range by[src] {
		who := c.Name
		if c.Who == entry.WhoSelf {
			who = t.s("你", "you")
		}
		if who == "" {
			who = strings.TrimPrefix(c.Who, "person:")
		}
		items = append(items, row("「"+short(c.Quote, 140)+"」", t.f("这句话是不是 %s 说的？ · %s", "Is this by %s? · %s", who, c.Day), nil, nil))
		if p, ok := pj.Speakers[fmt.Sprintf("%s@%s#%d", c.Ref, c.Day, c.N)]; ok {
			items = append(items, caption(prejudgeLine(t, p)))
		}
		cur := ""
		if c.Verdict != nil {
			cur = c.Verdict.Mark
		}
		marks = append(marks, cur)
		items = append(items, verdictSeg(t, speakerKey(c), cur, false))
	}
	return []wire.Section{{Title: t.sourceLabel(entry.SrcChat, src), Footer: tally(t, marks), Items: items}}
}

func (a *app) checkFacts(t tr, st *store.State) []wire.Section {
	es := pickFacts(st, time.Now().In(a.c.Loc).Format("2006-01-02"))
	if len(es) == 0 {
		return []wire.Section{{Title: t.s("F1 事实核对", "F1 facts"), Items: []wire.Component{empty(t.s("还没攒出来", "Nothing collected yet"))}}}
	}
	var items []wire.Component
	var marks []string
	pj := readPrejudge(a.c.St.Dir)
	for _, e := range es {
		it := checkItems(t, e, true)
		if p, ok := pj.Facts[e.ID]; ok {
			// The model's call first (9.5), for the user to confirm or overrule: it never marks the entry itself.
			it = append(it[:len(it)-1], caption(prejudgeLine(t, p)), it[len(it)-1])
		}
		items = append(items, it...)
		marks = append(marks, lastMark(e.Verdicts))
	}
	return []wire.Section{{Title: t.f("F1 事实核对：%d 条，对照出处原文", "F1 facts: %d, against the words they cite", len(es)),
		Footer: tally(t, marks) + t.s("。≥90% 算过；「出处不支持」算错。", ". Passes at 90%; \"unsupported\" counts as wrong."), Items: items}}
}

// prejudge is the model's first call on the F1 facts and the U8 lines (accept -save writes it beside the state).
type prejudge struct {
	Facts    map[string]prejudgeCall `json:"facts"`
	Speakers map[string]prejudgeCall `json:"speakers"` // "<ref>@<day>#<n>"
}

type prejudgeCall struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

func readPrejudge(dir string) prejudge {
	var p prejudge
	if dir == "" {
		return p
	}
	if raw, err := os.ReadFile(filepath.Join(dir, prejudgeFile)); err == nil {
		_ = json.Unmarshal(raw, &p)
	}
	return p
}

func prejudgeLine(t tr, p prejudgeCall) string {
	v := map[string][2]string{"right": {"对", "right"}, "wrong": {"错", "wrong"}, "unsupported": {"出处不支持", "unsupported"},
		"yes": {"是", "yes"}, "no": {"不是", "no"}, "unsure": {"说不清", "unsure"}}[p.Verdict]
	if v[0] == "" {
		v = [2]string{p.Verdict, p.Verdict}
	}
	return t.f("模型先判：%s · %s（以你点的为准）", "The model's first call: %s · %s (yours decides)", t.s(v[0], v[1]), short(p.Reason, 100))
}
