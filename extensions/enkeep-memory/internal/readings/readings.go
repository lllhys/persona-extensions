// Package readings computes the readings of design §9.2 over the two weeks ending on a day, one or more per facet,
// none standing in for another:
//
//	①  R1, R1.recur, R2         (internal/signals: the correction ledger)
//	②  R3, R3.repeat            (internal/signals: the friction screen and digs)
//	③  R4, R4.<fork type>, R4.overturn
//	④  R5                       corrections of how a kind of task was done, per 100 sessions
//	⑤  R6, R6.corr, F1, F1.model
//	⑤′ R8                       commitment reminders not marked wrong
//	⑥  R7                       corrections of wording, tone or the user's picture, per 100 user messages
//	U1…U8                       the user's marks on the check page
//
// A reading with fewer than signals.MinChances chances is not measurable ("量不出"): neither passed nor failed.
//
// R4 follows the cards as the hub keeps them since hub #5876: of the questions that carried a recommendation, the
// share the user answered themself (answered_by absent, which the hub reads as the user; an expired card answered
// late counts) by choosing the recommended option. Answers by the assistant for the user and recommendations taken
// at the deadline (answered_by default) are counted apart and are never adoptions. Per fork type, R4.<type> maps a
// card to the judgement class its answer was filed under; a card no class holds counts as "未归类".
//
// AutoK2 is the automatic opening of K2.<type> (design 4.4, three): only on cards that said they are reversible
// (Reversible true; unknown is never read as reversible), only when the reading is measurable, ≥10 adoptions,
// ≥90% and none overturned; a type the user set is never touched, and an automatic opening closes again on an
// overturn.
package readings

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/signals"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Unclassified is the fork type of a card no judgement class holds.
const Unclassified = "未归类"

// K2 automatic opening thresholds (design 4.4): never relaxed for little data.
const (
	K2MinAdopted = 10
	K2MinRate    = 0.9
)

// Input is what one computation reads.
type Input struct {
	St  *store.State
	Dir string // the state directory (signals.json)
	Day string // the last day of the period
	Loc *time.Location
	// Cards are the period's cards (S2); CardsMissing says why there are none to read ("" when they were read).
	Cards        []hubapi.Interaction
	CardsMissing string
	// F1Model is the model's pre-judgement of the F1 sample (entry id → right | wrong | unsupported), optional.
	F1Model map[string]string
}

// Compute works out every reading for the period and stores them in State.Readings (replacing the same code and
// period). It returns them in a stable order.
func Compute(in Input) ([]entry.Reading, error) {
	st := in.St
	out, err := signals.Readings(st, in.Dir, in.Day)
	if err != nil {
		return nil, err
	}
	days := Days(in.Day)
	inPeriod := map[string]bool{}
	for _, d := range days {
		inPeriod[d] = true
	}
	stats, err := signals.Stats(in.Dir)
	if err != nil {
		return nil, err
	}
	var msgs, sessions int
	for d, s := range stats {
		if inPeriod[d] {
			msgs += s.UserMsgs
			sessions += s.Sessions
		}
	}
	// ③ R4 from the cards.
	r4 := R4Of(st, in.Cards, in.Loc, inPeriod)
	// Corrections by the facet of the entry they were filed against.
	byFacet := map[entry.Facet]int{}
	// fork type → adoptions of a recommendation that a correction of that type's judgement entries came after: what
	// was done as recommended, overturned. A correction with no adoption before it is not an overturn.
	corrected := map[string][]time.Time{}
	for _, c := range st.Corrections {
		if !inPeriod[c.Day] || c.Entry == "" {
			continue
		}
		e := st.Entries[c.Entry]
		if e == nil {
			continue
		}
		byFacet[e.Facet]++
		if e.Facet == entry.Judgement {
			if at, err := time.Parse(time.RFC3339, c.At); err == nil {
				corrected[TypeOf(st, e)] = append(corrected[TypeOf(st, e)], at)
			}
		}
	}
	overturn := r4.Overturned(corrected)
	note := fmt.Sprintf("有建议的卡片 %d 张：用户亲答 %d（采纳 %d，含过期后补答 %d）；助手代答 %d、到期默认 %d（都不算采纳）；未答 %d",
		r4.WithRecommend, r4.User.Den, r4.User.Num, r4.Late, r4.Assistant, r4.Default, r4.Unanswered)
	if in.CardsMissing != "" {
		note = "卡片没读到：" + in.CardsMissing
	}
	out = append(out, reading("R4", in.Day, r4.User.Num, r4.User.Den, 1, note))
	totalOver := 0
	for _, n := range overturn {
		totalOver += n
	}
	out = append(out, reading("R4.overturn", in.Day, totalOver, r4.User.Num, 1, "按建议办后被推翻的采纳次数（之后对同类默认判断有纠正），分母是采纳次数"))
	for _, typ := range sortedKeys(r4.ByType) {
		c := r4.ByType[typ]
		rev := r4.Reversible[typ]
		out = append(out, reading("R4."+typ, in.Day, c.Num, c.Den, 1,
			fmt.Sprintf("可撤回的卡片 %d 张（采纳 %d）；推翻 %d", rev.Den, rev.Num, overturn[typ])))
	}
	// ④ R5: corrections of how things were done, per 100 sessions.
	out = append(out, reading("R5", in.Day, byFacet[entry.Workflow], sessions, 100, "对处理方式条目的纠正，分母是会话数（助手和 Agent）"))
	// ⑤ R6: kb_* hits need the hub's record of what kb_* returned, which no source gives the plugin yet.
	out = append(out, entry.Reading{Code: "R6", Period: in.Day, Note: signals.NotMeasurable + "：插件还读不到助手和 Agent 调 kb_* 的返回记录"})
	out = append(out, reading("R6.corr", in.Day, byFacet[entry.World]+byFacet[entry.People], msgs, 100, "对事实和画像条目的纠正，每百条用户消息"))
	// ⑤ F1: the user's marks on facts, and the model's pre-judgement of the sample.
	fr, fd := marks(st, func(e *entry.Entry) bool { return isFact(e) })
	out = append(out, reading("F1", in.Day, fr, fd, 1, "用户在核对页上点过的事实"))
	if in.F1Model != nil {
		right, wrong, unsup := 0, 0, 0
		for _, v := range in.F1Model {
			switch v {
			case "right":
				right++
			case "wrong":
				wrong++
			default:
				unsup++
			}
		}
		out = append(out, reading("F1.model", in.Day, right, len(in.F1Model), 1,
			fmt.Sprintf("模型先判：对 %d / 错 %d / 出处不支持 %d，等用户复看", right, wrong, unsup)))
	}
	// ⑤′ R8: commitment reminders due in the period, not marked wrong.
	var due, kept int
	for _, e := range st.Entries {
		if e.Commitment == nil || e.Deleted || e.Commitment.Due == "" || e.Commitment.Due > in.Day || e.Commitment.Due < days[0] {
			continue
		}
		due++
		if n := len(e.Verdicts); n == 0 || e.Verdicts[n-1].Mark != "wrong" {
			kept++
		}
	}
	out = append(out, reading("R8", in.Day, kept, due, 1, "到期的双向承诺，没被点「不对」的"))
	// ⑥ R7.
	out = append(out, reading("R7", in.Day, byFacet[entry.Persona], msgs, 100, "对「关于用户」条目的纠正，每百条用户消息"))
	// U1…U7: the user's marks per facet; U8: the speaker lines.
	for i, f := range []entry.Facet{entry.Interaction, entry.Tools, entry.Judgement, entry.Workflow, entry.World, entry.Persona, entry.People} {
		r, d := marks(st, func(e *entry.Entry) bool { return e.Facet == f })
		out = append(out, reading(fmt.Sprintf("U%d", i+1), in.Day, r, d, 1, ""))
	}
	sr, sd := 0, 0
	for _, sc := range st.SpeakerChecks {
		if sc.Verdict != nil && sc.Verdict.By != "model" && sc.Verdict.Mark != "unsure" {
			sd++
			if sc.Verdict.Mark == "right" {
				sr++
			}
		}
	}
	out = append(out, reading("U8", in.Day, sr, sd, 1, ""))
	for _, r := range out {
		st.Readings = put(st.Readings, r)
	}
	return out, nil
}

// Ratio is a count out of a number of chances.
type Ratio struct{ Num, Den int }

// R4Result is what the cards of a period say about adoption.
type R4Result struct {
	WithRecommend int
	User          Ratio // adoptions out of the user's own answers
	Late          int   // of the user's answers, given after the card expired
	Assistant     int   // answered by the assistant for the user
	Default       int   // the recommendation taken at the deadline
	Unanswered    int
	ByType        map[string]Ratio // fork type → adoptions out of the user's answers
	Reversible    map[string]Ratio // the same, cards marked reversible only (what K2 may act on)
	// AdoptedAt is, per fork type, when the user took a recommendation, in order: an adoption is overturned when a
	// correction of that type's judgement entries follows it, before the next adoption of the type (R4.overturn).
	AdoptedAt map[string][]time.Time
}

// Overturned counts, per fork type, the adoptions a correction (its time) came after, each adoption once.
func (r R4Result) Overturned(corrections map[string][]time.Time) map[string]int {
	out := map[string]int{}
	for typ, ads := range r.AdoptedAt {
		ads = slices.Clone(ads)
		slices.SortFunc(ads, func(a, b time.Time) int { return a.Compare(b) })
		for i, a := range ads {
			for _, c := range corrections[typ] {
				if !c.Before(a) && (i+1 == len(ads) || c.Before(ads[i+1])) {
					out[typ]++
					break
				}
			}
		}
	}
	return out
}

// AnsweredByUser says whether a card's answer is the user's own choice (hub #5876): answered_by absent or "user",
// with an answer; an expired card answered late counts.
func AnsweredByUser(c hubapi.Interaction) bool {
	if len(c.Answers) == 0 {
		return false
	}
	return c.AnsweredBy == "" || c.AnsweredBy == "user"
}

// Adopted says whether the answer is the recommended option.
func Adopted(c hubapi.Interaction) bool {
	rec := norm.Q(c.Recommend)
	if rec == "" {
		return false
	}
	for _, a := range c.Answers {
		q := norm.Q(a)
		if q == "" {
			continue
		}
		if q == rec || len([]rune(rec)) >= 2 && strings.HasPrefix(q, rec) || len([]rune(q)) >= 2 && strings.HasPrefix(rec, q) {
			return true
		}
	}
	return false
}

// R4Of counts the cards raised on the given days (all when days is nil).
func R4Of(st *store.State, cards []hubapi.Interaction, loc *time.Location, days map[string]bool) R4Result {
	if loc == nil {
		loc = time.UTC
	}
	r := R4Result{ByType: map[string]Ratio{}, Reversible: map[string]Ratio{}, AdoptedAt: map[string][]time.Time{}}
	idx := newCardIndex(st)
	for _, c := range cards {
		day := c.At.In(loc).Format("2006-01-02")
		if days != nil && !days[day] || c.Private || strings.TrimSpace(c.Recommend) == "" {
			continue
		}
		r.WithRecommend++
		switch {
		case len(c.Answers) == 0:
			r.Unanswered++
			continue
		case c.AnsweredBy == "assistant":
			r.Assistant++
			continue
		case c.AnsweredBy == "default":
			r.Default++
			continue
		case !AnsweredByUser(c):
			r.Unanswered++
			continue
		}
		if c.Late {
			r.Late++
		}
		typ := idx.typeOf(c, day)
		ok := Adopted(c)
		add := func(m map[string]Ratio) {
			x := m[typ]
			x.Den++
			if ok {
				x.Num++
			}
			m[typ] = x
		}
		r.User.Den++
		if ok {
			r.User.Num++
			r.AdoptedAt[typ] = append(r.AdoptedAt[typ], c.At)
		}
		add(r.ByType)
		if c.Reversible != nil && *c.Reversible {
			add(r.Reversible)
		}
	}
	return r
}

// TypeOf is the fork type of a judgement entry: its own type for a class, the type of the class it was filed under
// for an action, Unclassified otherwise.
func TypeOf(st *store.State, e *entry.Entry) string {
	if e.Grain == entry.Class && e.Type != "" {
		return e.Type
	}
	if p := st.Entries[e.Parent]; p != nil && p.Type != "" {
		return p.Type
	}
	for _, x := range st.Entries {
		if x.Facet == entry.Judgement && x.Grain == entry.Class && x.Type != "" {
			for _, m := range x.Members {
				if m == e.ID {
					return x.Type
				}
			}
		}
	}
	return Unclassified
}

// cardIndex finds the judgement entry a card was extracted into: an action with evidence from the same day, in the
// card's unit (asks/<day>, or the agent session it was raised in), quoting the card's answer or question.
type cardIndex struct {
	st  *store.State
	evs []cardEv
}

type cardEv struct {
	e     *entry.Entry
	ref   string
	day   string
	quote string
}

func newCardIndex(st *store.State) *cardIndex {
	ix := &cardIndex{st: st}
	for _, e := range st.Entries {
		if e.Facet != entry.Judgement || e.Grain != entry.Action || e.Deleted {
			continue
		}
		for _, ev := range e.Evidence {
			ix.evs = append(ix.evs, cardEv{e: e, ref: ev.Ref, day: ev.Day, quote: norm.Q(ev.Quote)})
		}
	}
	sort.Slice(ix.evs, func(i, j int) bool { return ix.evs[i].e.ID < ix.evs[j].e.ID })
	return ix
}

func (ix *cardIndex) typeOf(c hubapi.Interaction, day string) string {
	var keys []string
	for _, a := range c.Answers {
		if q := norm.Q(a); len([]rune(q)) >= 2 {
			keys = append(keys, q)
		}
	}
	if q := []rune(norm.Q(c.Question)); len(q) >= 6 {
		keys = append(keys, string(q[:min(len(q), 24)]))
	}
	for _, ev := range ix.evs {
		if ev.day != day || !(ev.ref == "asks/"+day || c.Session != "" && strings.HasSuffix(ev.ref, "/"+c.Session)) {
			continue
		}
		for _, k := range keys {
			if strings.Contains(ev.quote, k) {
				return TypeOf(ix.st, ev.e)
			}
		}
	}
	return Unclassified
}

// AutoK2 opens K2.<type> ("act": reversible things are done as the user would choose) for the fork types whose
// readings in rs pass the thresholds on reversible cards, and closes an automatic opening again after an overturn.
// A switch the user set is left alone. It returns what it changed, as "K2.<type>=<value>".
func AutoK2(st *store.State, r R4Result, overturned map[string]int, day string) []string {
	var changed []string
	for _, typ := range sortedKeys(r.ByType) {
		if typ == Unclassified {
			continue
		}
		key := entry.K2Judgement + "." + typ
		cur, set := st.Switches[key]
		if set && cur.By == "user" {
			continue
		}
		rev := r.Reversible[typ]
		measurable := r.ByType[typ].Den >= signals.MinChances
		pass := measurable && rev.Num >= K2MinAdopted && float64(rev.Num) >= K2MinRate*float64(rev.Den) && overturned[typ] == 0
		switch {
		case pass && (!set || cur.Value != "act"):
			st.Switches[key] = entry.SwitchSetting{Value: "act", By: "auto", At: day,
				Why: fmt.Sprintf("R4.%s：可撤回的卡片采纳 %d/%d，0 次推翻", typ, rev.Num, rev.Den)}
			changed = append(changed, key+"=act")
		case !pass && set && cur.By == "auto" && cur.Value == "act" && overturned[typ] > 0:
			st.Switches[key] = entry.SwitchSetting{Value: "suggest", By: "auto", At: day,
				Why: fmt.Sprintf("R4.%s：按建议办后被推翻 %d 次，改回只附建议", typ, overturned[typ])}
			changed = append(changed, key+"=suggest")
		}
	}
	return changed
}

// Overturned counts, per fork type, the corrections of judgement entries on the given days.
func Overturned(st *store.State, days map[string]bool) map[string]int {
	out := map[string]int{}
	for _, c := range st.Corrections {
		if !days[c.Day] || c.Entry == "" {
			continue
		}
		if e := st.Entries[c.Entry]; e != nil && e.Facet == entry.Judgement {
			out[TypeOf(st, e)]++
		}
	}
	return out
}

// Days are the period's days, oldest first.
func Days(day string) []string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return nil
	}
	var out []string
	for i := signals.Period - 1; i >= 0; i-- {
		out = append(out, t.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

// DaySet is Days as a set.
func DaySet(day string) map[string]bool {
	m := map[string]bool{}
	for _, d := range Days(day) {
		m[d] = true
	}
	return m
}

func isFact(e *entry.Entry) bool {
	return e.Facet == entry.World || e.Facet == entry.People && e.Part == entry.PartFact
}

// marks counts the user's last right/wrong marks on the entries kept (unsure is not a chance).
func marks(st *store.State, keep func(*entry.Entry) bool) (right, den int) {
	for _, e := range st.Entries {
		if e.Deleted || !keep(e) {
			continue
		}
		for i := len(e.Verdicts) - 1; i >= 0; i-- {
			v := e.Verdicts[i]
			if v.By == "model" {
				continue
			}
			if v.Mark != "unsure" {
				den++
				if v.Mark == "right" {
					right++
				}
			}
			break
		}
	}
	return right, den
}

func reading(code, day string, num, den int, scale float64, note string) entry.Reading {
	r := entry.Reading{Code: code, Period: day, Num: num, Den: den, Measure: den >= signals.MinChances, Note: note}
	if den > 0 {
		r.Value = float64(num) * scale / float64(den)
	}
	if !r.Measure {
		r.Note = strings.TrimSpace(signals.NotMeasurable + " " + note)
	}
	return r
}

func put(rs []entry.Reading, r entry.Reading) []entry.Reading {
	for i := range rs {
		if rs[i].Code == r.Code && rs[i].Period == r.Period {
			rs[i] = r
			return rs
		}
	}
	return append(rs, r)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
