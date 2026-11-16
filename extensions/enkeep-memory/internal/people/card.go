package people

// Cards (design 5.7, ⑤′): one card per person entry, or per group of entries the user merged. A card is a portrait,
// not a pile of single facts: the card pass (curate/cards.go) folds the person's action entries into a few class
// lines per part, and this file assembles what the page, the exits and the knowledge base read:
//
//  1. facts: role, what they are responsible for, their relation to the user, the projects they are in;
//  2. how they work with the user: what they deal with each other about, who reaches out, how fast each answers
//     (counted from the direct chats, Rhythm), where they tend to disagree;
//  3. what the user said about them, in the user's words, with where it was said;
//  4. inferred profile and judgements (way of working, way of speaking, tendencies, reliability…): always labelled
//     inferred, with the evidence count; "few" when it rests on little, "reported only" when only others said it;
//  5. promises either way: what they promised the user, what the user promised them, due or possibly overdue.
//
// When there is little to go on the card is mostly facts and has few inferred lines: that is the right result.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
)

// PartAdvice is a card's one tip on how to communicate with the person: the only card text meant for an outward
// exit (E). It carries no evaluation and no quote; it is model-made, so it is marked Inferred and follows K15.
const PartAdvice = "advice"

// Flags of card lines.
const (
	FlagCard     = "card"           // made by the card pass (not merged by the generic weekly merge)
	FlagConflict = "card_conflict:" // shown side by side with that line: newer evidence disagrees
)

// FewEvidence: a line resting on fewer occurrences than this says "few (N)".
const FewEvidence = 3

// Sections of a card, in display order.
const (
	SecFacts       = "facts"
	SecCollab      = "collaboration"
	SecUserEval    = "user_eval"
	SecInferred    = "inferred"
	SecCommitments = "commitments"
)

// Sections in display order.
var Sections = []string{SecFacts, SecCollab, SecUserEval, SecInferred, SecCommitments}

// collabDims are the dimensions of the "how they work with the user" part. Other dimensions are open: facts go to
// the facts part, inferred lines to the inferred part.
var collabDims = []string{"collaboration", "initiation", "responsiveness", "disagreement", "dealings", "communication"}

// FactDims and InferredDims are the generic dimensions the card pass offers the model (it may name others): none is
// tied to work; work dimensions appear only when there is work content.
var (
	FactDims     = []string{"role", "responsibility", "relation", "projects", "background", "interests"}
	CollabDims   = collabDims
	InferredDims = []string{"style", "tendency", "reliability", "working_style", "overall"}
)

// SectionOf is the part of the card an entry goes in.
func SectionOf(e *entry.Entry) string {
	switch {
	case e.Commitment != nil || e.Part == entry.PartCommitment:
		return SecCommitments
	case e.Part == entry.PartUserEval:
		return SecUserEval
	case slices.Contains(collabDims, strings.ToLower(strings.TrimSpace(e.Type))):
		return SecCollab
	case e.Part == entry.PartInferred || e.Inferred:
		return SecInferred
	}
	return SecFacts
}

// Quote is one original line a card line rests on, with where it was said.
type Quote struct {
	Text     string `json:"text"`
	Ref      string `json:"ref"`
	Day      string `json:"day"`
	N        int    `json:"n,omitempty"`
	Who      string `json:"who,omitempty"` // self | person:<id> | ""
	Reported bool   `json:"reported,omitempty"`
}

// Line is one line of a card.
type Line struct {
	ID           string            `json:"id"`
	Section      string            `json:"section"`
	Part         string            `json:"part"`
	Dim          string            `json:"dim,omitempty"`
	Grain        string            `json:"grain"`
	Text         string            `json:"text"`
	Evidence     int               `json:"evidence"`      // occurrences (one per unit and day)
	Reported     int               `json:"reported"`      // evidence lines said by others about them
	Lines        int               `json:"lines"`         // evidence lines in all
	Few          bool              `json:"few,omitempty"` // fewer than FewEvidence occurrences
	ReportedOnly bool              `json:"reported_only,omitempty"`
	Inferred     bool              `json:"inferred,omitempty"`
	UserEdited   bool              `json:"user_edited,omitempty"`
	Pending      bool              `json:"pending,omitempty"` // an action not folded into a line yet
	Conflicts    []string          `json:"conflicts,omitempty"`
	Sensitive    string            `json:"sensitive,omitempty"`
	First        string            `json:"first,omitempty"`
	Last         string            `json:"last,omitempty"`
	ValidFrom    string            `json:"valid_from,omitempty"`
	ValidTo      string            `json:"valid_to,omitempty"`
	Quotes       []Quote           `json:"quotes,omitempty"`
	Commitment   *entry.Commitment `json:"commitment,omitempty"`
	Direction    string            `json:"direction,omitempty"` // they_owe | user_owes
	DueSoon      bool              `json:"due_soon,omitempty"`
	Overdue      bool              `json:"possibly_overdue,omitempty"`
}

// Card is one person's card (or one merged group's).
type Card struct {
	Person   string            `json:"person"`            // the card's key: the first person entry of a merge
	Persons  []string          `json:"persons"`           // every person entry it covers
	Merge    string            `json:"merge,omitempty"`   // the plugin-side merge id, when several
	Name     string            `json:"name"`              // display name
	Contact  *entry.Contact    `json:"contact,omitempty"` // dealings, summed over the persons
	Rhythm   *Rhythm           `json:"rhythm,omitempty"`
	Parts    map[string][]Line `json:"parts"`
	Advice   string            `json:"advice,omitempty"` // how to communicate with them: tips only, no evaluation
	AdviceID string            `json:"advice_id,omitempty"`
	Actions  int               `json:"actions"` // action entries about them (the evidence before folding)
	Pending  int               `json:"pending"` // actions not folded yet
	Hidden   int               `json:"hidden"`  // lines left out by a switch (K13 category off, K15 off)
}

// Count is the number of lines in a part.
func (c *Card) Count(sec string) int { return len(c.Parts[sec]) }

// Input is what assembling a card reads.
type Input struct {
	Entries  map[string]*entry.Entry
	Contacts map[string]*entry.Contact
	Merges   []entry.PersonMerge
	Switches map[string]entry.SwitchSetting
	Rhythm   map[string]*Rhythm
	Names    func(person string) string
	// Due and Overdue are the open commitments due soon and possibly overdue (curate.CommitmentsDue): the ledger is
	// kept there, the card only shows it.
	Due, Overdue map[string]bool
}

// Group is the person entries a person's card covers: the merge it is in, else itself. The first is the card key.
func Group(merges []entry.PersonMerge, person string) (persons []string, merge string) {
	for _, m := range merges {
		if slices.Contains(m.Persons, person) {
			return slices.Clone(m.Persons), m.ID
		}
	}
	return []string{person}, ""
}

// Key is the card key of a person: the first person entry of its merge.
func Key(merges []entry.PersonMerge, person string) string {
	g, _ := Group(merges, person)
	return g[0]
}

// Of says whether an entry is about one of the persons (its card line, or a promise either way with them).
func Of(e *entry.Entry, persons []string) bool {
	if e.Facet == entry.People && slices.Contains(persons, e.Person) {
		return true
	}
	if cm := e.Commitment; cm != nil {
		return (cm.From == entry.WhoSelf && slices.Contains(persons, cm.To)) || (cm.To == entry.WhoSelf && slices.Contains(persons, cm.From))
	}
	return false
}

// Shows says whether an entry may be on the card at all under the switches: a sensitive category only when the
// user turned it on (K13), inferred lines and the tip only with K15.
func Shows(e *entry.Entry, sw map[string]entry.SwitchSetting) bool {
	if e.Sensitive != "" && !entry.On(sw, entry.K13Sensitive+"."+e.Sensitive) {
		return false
	}
	if (e.Part == entry.PartInferred || e.Inferred || e.Part == PartAdvice) && !entry.On(sw, entry.K15Inferred) {
		return false
	}
	return true
}

// Standalone is the consolidation's flag of an action looked at and left alone (a one-off, or dropped).
const Standalone = "standalone"

// BuildCard assembles the card of a person (and of everyone merged with them).
func BuildCard(in Input, person string) *Card {
	persons, merge := Group(in.Merges, person)
	c := &Card{Person: persons[0], Persons: persons, Merge: merge, Parts: map[string][]Line{}}
	for _, m := range in.Merges {
		if m.ID == merge && m.Name != "" {
			c.Name = m.Name
		}
	}
	var ct *entry.Contact
	var rs []*Rhythm
	for _, p := range persons {
		if x := in.Contacts[p]; x != nil {
			if ct == nil {
				ct = &entry.Contact{Person: c.Person, Name: x.Name, Source: x.Source, Ties: map[string]int{}, First: x.First, Last: x.Last}
			}
			for k, v := range x.Ties {
				ct.Ties[k] += v
			}
			if x.First != "" && (ct.First == "" || x.First < ct.First) {
				ct.First = x.First
			}
			if x.Last > ct.Last {
				ct.Last = x.Last
			}
			if c.Name == "" && x.Name != "" {
				c.Name = x.Name
			}
		}
		rs = append(rs, in.Rhythm[p])
	}
	if ct != nil {
		ct.Score = Score(ct)
	}
	c.Contact, c.Rhythm = ct, MergeRhythm(rs...)
	if c.Name == "" && in.Names != nil {
		c.Name = in.Names(c.Person)
	}
	ids := make([]string, 0, len(in.Entries))
	for id := range in.Entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := in.Entries[id]
		if !Of(e, persons) {
			continue
		}
		if e.Facet == entry.People && e.Grain == entry.Action && e.Commitment == nil {
			c.Actions++
		}
		if !e.Live() {
			continue
		}
		if !Shows(e, in.Switches) {
			c.Hidden++
			continue
		}
		if e.Part == PartAdvice {
			if e.Person == c.Person || c.Advice == "" {
				c.Advice, c.AdviceID = e.Shown(), e.ID
			}
			continue
		}
		folded := e.Grain != entry.Action || e.Commitment != nil
		if !folded && (e.Parent != "" || slices.Contains(e.Flags, Standalone)) {
			continue // in a line already, or left alone
		}
		if !folded {
			c.Pending++
		}
		l := lineOf(in, e)
		l.Pending = !folded
		c.Parts[l.Section] = append(c.Parts[l.Section], l)
	}
	for _, sec := range Sections {
		ls := c.Parts[sec]
		sort.SliceStable(ls, func(i, j int) bool {
			if sec == SecCommitments {
				di, dj := ls[i].Commitment.Due, ls[j].Commitment.Due
				if di != dj {
					return di != "" && (dj == "" || di < dj)
				}
			}
			if ls[i].Pending != ls[j].Pending {
				return !ls[i].Pending
			}
			if ls[i].Evidence != ls[j].Evidence {
				return ls[i].Evidence > ls[j].Evidence
			}
			return ls[i].ID < ls[j].ID
		})
	}
	return c
}

// lineOf turns an entry into a card line: its counts from its own evidence and its members'.
func lineOf(in Input, e *entry.Entry) Line {
	l := Line{ID: e.ID, Section: SectionOf(e), Part: e.Part, Dim: e.Type, Grain: string(e.Grain), Text: e.Shown(),
		Evidence: max(e.Count, 1), Inferred: e.Inferred || e.Part == entry.PartInferred, UserEdited: e.User != nil,
		Sensitive: e.Sensitive, First: e.First, Last: e.Last, ValidFrom: e.ValidFrom, ValidTo: e.ValidTo, Commitment: e.Commitment}
	if l.Part == "" {
		l.Part = entry.PartFact
	}
	evs := slices.Clone(e.Evidence)
	allReported := e.Grain == entry.Action && e.ReportedOnly
	if e.Grain != entry.Action {
		allReported = len(e.Members) > 0
		for _, id := range e.Members {
			m := in.Entries[id]
			if m == nil || m.Deleted {
				continue
			}
			evs = append(evs, m.Evidence...)
			if !m.ReportedOnly {
				allReported = false
			}
		}
	}
	for _, ev := range evs {
		l.Lines++
		if ev.Reported {
			l.Reported++
		}
	}
	l.ReportedOnly = allReported || (l.Lines > 0 && l.Reported == l.Lines)
	l.Few = l.Evidence < FewEvidence
	for _, f := range e.Flags {
		if id, ok := strings.CutPrefix(f, FlagConflict); ok {
			if x := in.Entries[id]; x != nil && x.Live() {
				l.Conflicts = append(l.Conflicts, id)
			}
		}
	}
	// The user's words are shown as said; other lines show a few of the lines they rest on.
	limit := 3
	for i := len(evs) - 1; i >= 0 && len(l.Quotes) < limit; i-- {
		ev := evs[i]
		if strings.TrimSpace(ev.Quote) == "" {
			continue
		}
		if l.Part == entry.PartUserEval && ev.Who != entry.WhoSelf {
			continue
		}
		l.Quotes = append(l.Quotes, Quote{Text: ev.Quote, Ref: ev.Ref, Day: ev.Day, N: ev.N, Who: ev.Who, Reported: ev.Reported})
	}
	if cm := e.Commitment; cm != nil {
		switch {
		case cm.To == entry.WhoSelf:
			l.Direction = "they_owe"
		case cm.From == entry.WhoSelf:
			l.Direction = "user_owes"
		}
		l.DueSoon, l.Overdue = in.Due[e.ID], in.Overdue[e.ID]
	}
	return l
}

// Summary is one row of the card list.
type Summary struct {
	Person   string         `json:"person"`
	Persons  []string       `json:"persons"`
	Merge    string         `json:"merge,omitempty"`
	Name     string         `json:"name"`
	Score    int            `json:"score"`
	Manual   string         `json:"manual,omitempty"`
	Counts   map[string]int `json:"counts"` // lines per part
	Actions  int            `json:"actions"`
	Pending  int            `json:"pending"`
	Reported int            `json:"reported"` // lines resting only on what others said
	Advice   bool           `json:"advice"`
}

// List is the cards in the order of the intersection list (merged groups once, at their best rank).
func List(in Input) []Summary {
	var out []Summary
	seen := map[string]bool{}
	for _, ct := range Ranked(in.Contacts) {
		key := Key(in.Merges, ct.Person)
		if seen[key] {
			continue
		}
		seen[key] = true
		c := BuildCard(in, ct.Person)
		s := Summary{Person: c.Person, Persons: c.Persons, Merge: c.Merge, Name: c.Name, Score: ct.Score, Manual: ct.Manual,
			Counts: map[string]int{}, Actions: c.Actions, Pending: c.Pending, Advice: c.Advice != ""}
		if c.Contact != nil {
			s.Score = c.Contact.Score
		}
		for _, sec := range Sections {
			s.Counts[sec] = c.Count(sec)
			for _, l := range c.Parts[sec] {
				if l.ReportedOnly {
					s.Reported++
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// PersonID is the knowledge-base id of a "person:<id>" reference.
func PersonID(ref string) string { return speaker.PersonID(ref) }

// ---- the knowledge base's person page (exit C) ----------------------------------------------------------------

// Words of the lines written into the knowledge base, in the output language: the knowledge base is read by the
// assistant and the user in their language.
type Words struct {
	Heading, Facts, Collab, UserEval, Inferred, Commitments           string
	Fact, Said, Guess, Stat                                           string
	Evidence, Few, ReportedOnly, Conflict, NotForward                 string
	TheyOwe, UserOwes, Due, Overdue, DueSoon, Open, Close, Sep, Quote string
	StartedBy, Answers, Minutes, Log                                  string
}

// WordsFor gives the words of a language (BCP 47).
func WordsFor(lang string) Words {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return Words{Heading: "画像（插件整理，截至 %s）", Facts: "事实", Collab: "和用户的合作", UserEval: "用户原话评价", Inferred: "推断（仅供参考，不对外转述）",
			Commitments: "双向承诺", Fact: "事实：", Said: "用户原话：", Guess: "推断：", Stat: "统计：",
			Evidence: "证据 %d 条", Few: "证据少（%d 条）", ReportedOnly: "只来自转述", Conflict: "与另一条说法冲突，并排列出", NotForward: "",
			TheyOwe: "对方答应用户：", UserOwes: "用户答应对方：", Due: "到期 %s", Overdue: "可能逾期", DueSoon: "快到期",
			Open: "（", Close: "）", Sep: "；", Quote: "「%s」",
			StartedBy: "私聊 %d 天，对方先开口 %d 天，用户先开口 %d 天", Answers: "%s回复中位 %d 分钟（%d 次）", Minutes: "分钟",
			Log: "画像更新：事实 %d、合作 %d、用户评价 %d、推断 %d、承诺 %d"}
	case strings.HasPrefix(lang, "ja"):
		return Words{Heading: "人物像（プラグイン整理、%s 時点）", Facts: "事実", Collab: "ユーザーとの協働", UserEval: "ユーザー自身の評価", Inferred: "推定（参考のみ、転送しない）",
			Commitments: "約束", Fact: "事実：", Said: "ユーザーの言葉：", Guess: "推定：", Stat: "集計：",
			Evidence: "根拠 %d 件", Few: "根拠少（%d 件）", ReportedOnly: "伝聞のみ", Conflict: "別の記述と矛盾、並記",
			TheyOwe: "相手の約束：", UserOwes: "ユーザーの約束：", Due: "期限 %s", Overdue: "期限切れの可能性", DueSoon: "期限間近",
			Open: "（", Close: "）", Sep: "、", Quote: "「%s」",
			StartedBy: "個別チャット %d 日、相手から %d 日、ユーザーから %d 日", Answers: "%sの返信 中央値 %d 分（%d 回）", Minutes: "分",
			Log: "人物像を更新：事実 %d、協働 %d、評価 %d、推定 %d、約束 %d"}
	}
	return Words{Heading: "Profile (curated by the plugin, as of %s)", Facts: "Facts", Collab: "Working with the user", UserEval: "The user's own words",
		Inferred: "Inferred (for reference only, not to be passed on)", Commitments: "Promises", Fact: "Fact: ", Said: "The user said: ",
		Guess: "Inferred: ", Stat: "Counted: ", Evidence: "%d pieces of evidence", Few: "little evidence (%d)", ReportedOnly: "only from what others said",
		Conflict: "conflicts with another line, shown side by side", TheyOwe: "They promised the user: ", UserOwes: "The user promised them: ",
		Due: "due %s", Overdue: "possibly overdue", DueSoon: "due soon", Open: " (", Close: ")", Sep: "; ", Quote: "\"%s\"",
		StartedBy: "%d days of direct chat; they started %d, the user %d", Answers: "%s answers in a median %d minutes (%d times)", Minutes: "minutes",
		Log: "profile updated: facts %d, working together %d, the user's words %d, inferred %d, promises %d"}
}

// Page is a card as written to a person page: the pinned fact lines (each a line of its own, with provenance) and
// the summary block with the three kinds of content labelled apart, plus one log line.
type Page struct {
	Facts   []PageFact `json:"facts"`
	Summary string     `json:"summary"`
	Log     string     `json:"log"`
	Counts  map[string]int
}

// PageFact is one fact line and the entry it comes from.
type PageFact struct {
	Entry string `json:"entry"`
	Line  string `json:"line"`
}

// Options of the page: K15 off leaves inferred lines out; the date is the card's "as of".
type Options struct {
	Inferred bool
	Today    string
	Lang     string
}

// RenderPage writes a card for the knowledge base. Lines resting only on others' words are marked; facts that rest
// only on others' words are not pinned (they stay in the summary, marked).
func RenderPage(c *Card, o Options) Page {
	w := WordsFor(o.Lang)
	p := Page{Counts: map[string]int{}}
	var b strings.Builder
	fmt.Fprintf(&b, "## "+w.Heading+"\n", o.Today)
	marks := func(l Line) string {
		var ms []string
		if l.Few {
			ms = append(ms, fmt.Sprintf(w.Few, l.Evidence))
		} else {
			ms = append(ms, fmt.Sprintf(w.Evidence, l.Evidence))
		}
		if l.ReportedOnly {
			ms = append(ms, w.ReportedOnly)
		}
		if len(l.Conflicts) > 0 {
			ms = append(ms, w.Conflict)
		}
		if q := firstSource(l); q != "" {
			ms = append(ms, q)
		}
		return w.Open + strings.Join(ms, w.Sep) + w.Close
	}
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		b.WriteString("### " + title + "\n")
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
	}
	var facts, collab, evals, guesses, promises []string
	for _, l := range c.Parts[SecFacts] {
		if l.Pending {
			continue
		}
		line := w.Fact + l.Text + marks(l)
		facts = append(facts, line)
		if !l.ReportedOnly && len(l.Conflicts) == 0 && l.Sensitive == "" {
			p.Facts = append(p.Facts, PageFact{Entry: l.ID, Line: "- " + line})
		}
	}
	if r := c.Rhythm; r != nil && r.Days > 0 {
		collab = append(collab, w.Stat+fmt.Sprintf(w.StartedBy, r.Days, r.ByThem, r.ByUser))
		them, user := theyUser(o.Lang)
		if m, n := Median(r.TheirGaps); n > 0 {
			collab = append(collab, w.Stat+fmt.Sprintf(w.Answers, them, m, n))
		}
		if m, n := Median(r.UserGaps); n > 0 {
			collab = append(collab, w.Stat+fmt.Sprintf(w.Answers, user, m, n))
		}
	}
	for _, l := range c.Parts[SecCollab] {
		if l.Pending || (l.Inferred && !o.Inferred) {
			continue
		}
		label := w.Fact
		if l.Inferred {
			label = w.Guess
		}
		collab = append(collab, label+l.Text+marks(l))
	}
	for _, l := range c.Parts[SecUserEval] {
		if l.Pending {
			continue
		}
		var qs []string
		for _, q := range l.Quotes {
			qs = append(qs, fmt.Sprintf(w.Quote, q.Text)+w.Open+source(q)+w.Close)
		}
		if len(qs) == 0 {
			continue // the user's evaluation is shown in their words or not at all
		}
		evals = append(evals, w.Said+strings.Join(qs, w.Sep))
	}
	if o.Inferred {
		for _, l := range c.Parts[SecInferred] {
			if l.Pending {
				continue
			}
			guesses = append(guesses, w.Guess+l.Text+marks(l))
		}
	}
	for _, l := range c.Parts[SecCommitments] {
		if l.Commitment == nil || l.Commitment.Done != "" {
			continue
		}
		head := w.TheyOwe
		if l.Direction == "user_owes" {
			head = w.UserOwes
		}
		var ms []string
		if l.Commitment.Due != "" {
			ms = append(ms, fmt.Sprintf(w.Due, l.Commitment.Due))
		}
		if l.Overdue {
			ms = append(ms, w.Overdue)
		} else if l.DueSoon {
			ms = append(ms, w.DueSoon)
		}
		if q := firstSource(l); q != "" {
			ms = append(ms, q)
		}
		promises = append(promises, head+l.Commitment.What+w.Open+strings.Join(ms, w.Sep)+w.Close)
	}
	section(w.Facts, facts)
	section(w.Collab, collab)
	section(w.UserEval, evals)
	section(w.Inferred, guesses)
	section(w.Commitments, promises)
	p.Summary = strings.TrimRight(b.String(), "\n") + "\n"
	p.Counts = map[string]int{SecFacts: len(facts), SecCollab: len(collab), SecUserEval: len(evals), SecInferred: len(guesses), SecCommitments: len(promises)}
	p.Log = fmt.Sprintf(w.Log, len(facts), len(collab), len(evals), len(guesses), len(promises))
	return p
}

func theyUser(lang string) (string, string) {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return "对方", "用户"
	case strings.HasPrefix(lang, "ja"):
		return "相手", "ユーザー"
	}
	return "They", "The user"
}

// source is where a quote was said: "<ref> <day> #<n>".
func source(q Quote) string {
	s := q.Ref + " " + q.Day
	if q.N > 0 {
		s += fmt.Sprintf(" #%d", q.N)
	}
	return s
}

func firstSource(l Line) string {
	for _, q := range l.Quotes {
		if q.Ref != "" {
			return source(q)
		}
	}
	return ""
}
