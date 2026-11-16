package curate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// The weekly consolidation of the upper layers: the abstract gate (5.3), the impact review (5.0) and the clean-up
// of relations (5.4).

// Limits of the weekly calls.
const (
	abstractClasses   = 80 // classes shown to one abstract call, most seen first
	abstractMaxTokens = 6000
	reviewBatch       = 15
	reviewMaxTokens   = 4000
	minAbstractClass  = 3 // member classes an abstract entry rests on
	minSituations     = 2 // kinds of situation it governs
	minAbstractDays   = 2 // active days its members span
)

// Gate failures of a proposed abstract entry: why it stays at the class grain.
const (
	gateNoDefault  = "no_default"       // no capable stranger's default, or the same as the text
	gateGeneric    = "holds_for_anyone" // the fixed question: "replace the user with any capable person: does it still hold?"
	gateMembers    = "members"          // fewer than three live member classes of the facet
	gateSituations = "situations"       // fewer than two kinds of situation among them, or named
	gateDays       = "active_days"      // members on fewer than two active days
	gateQuotes     = "quotes"           // a member without a quoted line
)

type (
	absClass struct {
		ID        string `json:"id"`
		Type      string `json:"type,omitempty"`
		Text      string `json:"text"`
		Situation string `json:"situation,omitempty"`
		Choice    string `json:"choice,omitempty"`
		Count     int    `json:"count"`
		Days      int    `json:"active_days"`
	}
	absExisting struct {
		ID      string   `json:"id"`
		Text    string   `json:"text"`
		Default string   `json:"default"`
		Members []string `json:"members"`
	}
	absInput struct {
		Facet     string        `json:"facet"`
		Meaning   string        `json:"facet_meaning"`
		Lang      string        `json:"language"`
		Classes   []absClass    `json:"classes"`
		Abstracts []absExisting `json:"abstracts"`
	}
	absSituation struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
	}
	absProposal struct {
		Extends    string         `json:"extends,omitempty"`
		Text       string         `json:"text"`
		Default    string         `json:"default"`
		Situations []absSituation `json:"situations"`
		Exception  string         `json:"exception,omitempty"`
		Reason     string         `json:"reason"`
	}
	absOutput struct {
		Abstracts []absProposal `json:"abstracts"`
	}
	reviewItem struct {
		ID      string `json:"id"`
		Facet   string `json:"facet"`
		Text    string `json:"text"`
		Default string `json:"default,omitempty"`
	}
	reviewInput struct {
		Lang     string       `json:"language"`
		Question string       `json:"question"`
		Items    []reviewItem `json:"items"`
	}
	reviewAnswer struct {
		ID     string `json:"id"`
		Anyone *bool  `json:"holds_for_anyone,omitempty"`
		Moment string `json:"moment,omitempty"`
		Action string `json:"action,omitempty"`
		Object string `json:"object,omitempty"`
		Reason string `json:"reason"`
	}
	reviewOutput struct {
		Answers []reviewAnswer `json:"answers"`
	}
)

// AbstractGate checks a proposed abstract entry against the three conditions of 5.3, which never relax with little
// data: a capable stranger's default that differs from the user's way (and the fixed question answered "no"), at
// least three member classes from at least two kinds of situation and two active days, each with quoted lines, and at
// least two named situations each linked to a member. It returns "" when all hold, else the first that fails.
func AbstractGate(s *store.State, a *entry.Entry, situations map[string][]string, holdsForAnyone bool) string {
	if strings.TrimSpace(a.Default) == "" || norm.Q(a.Default) == norm.Q(a.Text) {
		return gateNoDefault
	}
	if holdsForAnyone {
		return gateGeneric
	}
	var members []*entry.Entry
	for _, id := range a.Members {
		if m := s.Entries[id]; m != nil && m.Live() && m.Facet == a.Facet && m.Grain == entry.Class {
			members = append(members, m)
		}
	}
	if len(members) < minAbstractClass {
		return gateMembers
	}
	kinds := map[string]bool{}
	var days []string
	for _, m := range members {
		k := norm.Q(m.Type)
		if k == "" {
			k = norm.Q(m.Situation)
		}
		kinds[k] = true
		t := tally(s, m)
		for _, d := range t.Days {
			if !slices.Contains(days, d) {
				days = append(days, d)
			}
		}
		if !hasQuote(s, m) {
			return gateQuotes
		}
	}
	named := 0
	for name, ids := range situations {
		if strings.TrimSpace(name) != "" && slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(a.Members, id) }) {
			named++
		}
	}
	if len(kinds) < minSituations || named < minSituations || len(a.Situations) < minSituations {
		return gateSituations
	}
	if len(days) < minAbstractDays {
		return gateDays
	}
	return ""
}

// hasQuote: a class rests on at least one quoted line (its own or a member's).
func hasQuote(s *store.State, e *entry.Entry) bool {
	if slices.ContainsFunc(e.Evidence, func(x entry.Evidence) bool { return x.Quote != "" }) {
		return true
	}
	for _, id := range e.Members {
		if m := s.Entries[id]; m != nil && slices.ContainsFunc(m.Evidence, func(x entry.Evidence) bool { return x.Quote != "" }) {
			return true
		}
	}
	return false
}

// liftAbstracts asks, facet by facet, for the tendencies behind the classes, puts every proposal to the fixed
// question, and keeps what passes the gate as an abstract entry; what fails stays at the class grain, counted by
// reason in the run.
func (c *Curator) liftAbstracts(ctx context.Context, m model.Model, run *store.Run) error {
	s := c.St.S
	for _, f := range entry.Facets {
		if f == entry.People {
			// A tendency over several people's classes would be about nobody: each person's overall way of dealing with
			// the user is lifted per person by the cards (curate/cards.go).
			continue
		}
		var classes []*entry.Entry
		for _, e := range EntriesOf(s, f, func(e *entry.Entry) bool { return e.Grain == entry.Class && e.Status != entry.Shifting }) {
			if len(classes) < abstractClasses {
				classes = append(classes, e)
			}
		}
		if len(classes) < minAbstractClass {
			continue
		}
		in := absInput{Facet: string(f), Meaning: facetMeaning[f], Lang: c.language()}
		local, short := map[string]string{}, map[string]string{}
		for i, e := range classes {
			id := fmt.Sprintf("c%d", i+1)
			local[id], short[e.ID] = e.ID, id
			in.Classes = append(in.Classes, absClass{ID: id, Type: e.Type, Text: e.Shown(), Situation: e.Situation, Choice: e.Choice,
				Count: e.Count, Days: len(e.ActiveDays)})
		}
		for i, e := range EntriesOf(s, f, func(e *entry.Entry) bool { return e.Grain == entry.Abstract }) {
			id := fmt.Sprintf("x%d", i+1)
			local[id] = e.ID
			x := absExisting{ID: id, Text: e.Shown(), Default: e.Default}
			for _, mid := range e.Members {
				if sid, ok := short[mid]; ok {
					x.Members = append(x.Members, sid)
				}
			}
			in.Abstracts = append(in.Abstracts, x)
		}
		body, _ := json.Marshal(in)
		resp, err := m.Complete(ctx, model.Request{Step: "abstract", Tier: model.Medium, System: abstractPrompt, User: string(body),
			MaxTokens: abstractMaxTokens, Schema: abstractSchema})
		if errors.Is(err, model.ErrRefused) {
			run.Counts["abstract_refused"]++
			continue
		}
		if err != nil {
			return err
		}
		run.Counts["abstract_calls"]++
		var out absOutput
		if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &out); err != nil {
			run.Notes = append(run.Notes, fmt.Sprintf("抽象（%s）：答案读不了", f))
			continue
		}
		var props []*entry.Entry
		sits := map[*entry.Entry]map[string][]string{}
		extends := map[*entry.Entry]*entry.Entry{}
		for _, p := range out.Abstracts {
			a := &entry.Entry{Facet: f, Grain: entry.Abstract, Text: strings.TrimSpace(p.Text), Default: strings.TrimSpace(p.Default),
				Exception: strings.TrimSpace(p.Exception), Domain: entry.Work, Impact: entry.Impact{Moment: "every_turn"}, OriginVersion: ConsolidatePrompt}
			sm := map[string][]string{}
			for _, st := range p.Situations {
				name := strings.TrimSpace(st.Name)
				for _, sid := range st.Members {
					if id, ok := local[sid]; ok && strings.HasPrefix(sid, "c") {
						sm[name] = append(sm[name], id)
						if !slices.Contains(a.Members, id) {
							a.Members = append(a.Members, id)
						}
					}
				}
				if name != "" && len(sm[name]) > 0 {
					a.Situations = append(a.Situations, name)
				}
			}
			if x := s.Entries[local[p.Extends]]; p.Extends != "" && x != nil && x.Grain == entry.Abstract {
				extends[a] = x
			}
			props = append(props, a)
			sits[a] = sm
		}
		if len(props) == 0 {
			continue
		}
		anyone, err := c.askAnyone(ctx, m, props, run)
		if err != nil {
			return err
		}
		for i, a := range props {
			run.Counts["abstract_proposed"]++
			if g := AbstractGate(s, a, sits[a], anyone[i]); g != "" {
				run.Counts["abstract_blocked"]++
				run.Counts["abstract_blocked:"+g]++
				run.Notes = append(run.Notes, fmt.Sprintf("抽象门槛拦下（%s，%s）：%s", f, g, clip(a.Text, 60)))
				continue
			}
			if tombstoned(s, f, a.Text, "") {
				run.Counts["abstract_tombstoned"]++
				continue
			}
			c.keepAbstract(a, extends[a], run)
		}
	}
	return nil
}

// keepAbstract records a proposal that passed the gate: a new abstract entry, or more members for the one it extends.
func (c *Curator) keepAbstract(a, into *entry.Entry, run *store.Run) {
	s := c.St.S
	if into != nil && !userTouched(into) {
		for _, id := range a.Members {
			if !slices.Contains(into.Members, id) {
				into.Members = append(into.Members, id)
			}
		}
		for _, sit := range a.Situations {
			if !slices.Contains(into.Situations, sit) {
				into.Situations = append(into.Situations, sit)
			}
		}
		recount(s, into)
		into.Updated = c.stamp()
		c.commit(entry.EvFile, into, "", "abstract extended", run.ID)
		run.Counts["abstract_extended"]++
		a = into
	} else {
		var scopes []entry.Scope
		for _, id := range a.Members {
			if m := s.Entries[id]; m != nil {
				scopes = unionScopes(scopes, m.Scopes)
			}
		}
		a.Scopes = scopes
		a = c.create(a, run.ID)
		run.Counts["abstract_new"]++
	}
	for _, id := range a.Members {
		if m := s.Entries[id]; m != nil && m.Parent == "" {
			m.Parent = a.ID
			c.commit(entry.EvFile, m, a.ID, "member of abstract", run.ID)
		}
	}
}

// askAnyone puts the fixed question of the abstract gate to each proposal: replace the user with any capable person,
// does it still hold? An unanswered proposal counts as holding for anyone (not lifted).
func (c *Curator) askAnyone(ctx context.Context, m model.Model, props []*entry.Entry, run *store.Run) ([]bool, error) {
	out := make([]bool, len(props))
	for i := range out {
		out[i] = true
	}
	for start := 0; start < len(props); start += reviewBatch {
		end := min(start+reviewBatch, len(props))
		in := reviewInput{Lang: c.language(), Question: "anyone"}
		for i := start; i < end; i++ {
			in.Items = append(in.Items, reviewItem{ID: fmt.Sprintf("q%d", i+1), Facet: string(props[i].Facet), Text: props[i].Text, Default: props[i].Default})
		}
		ans, err := c.review(ctx, m, in, run)
		if err != nil {
			return nil, err
		}
		for i := start; i < end; i++ {
			if a, ok := ans[fmt.Sprintf("q%d", i+1)]; ok && a.Anyone != nil {
				out[i] = *a.Anyone
			}
		}
	}
	return out, nil
}

func (c *Curator) review(ctx context.Context, m model.Model, in reviewInput, run *store.Run) (map[string]reviewAnswer, error) {
	body, _ := json.Marshal(in)
	resp, err := m.Complete(ctx, model.Request{Step: "review", Tier: model.Medium, System: reviewPrompt, User: string(body),
		MaxTokens: reviewMaxTokens, Schema: reviewSchema})
	if errors.Is(err, model.ErrRefused) {
		run.Counts["review_refused"]++
		return map[string]reviewAnswer{}, nil
	}
	if err != nil {
		return nil, err
	}
	run.Counts["review_calls"]++
	var out reviewOutput
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &out); err != nil {
		return map[string]reviewAnswer{}, nil
	}
	got := map[string]reviewAnswer{}
	for _, a := range out.Answers {
		if _, dup := got[a.ID]; !dup {
			got[a.ID] = a
		}
	}
	return got, nil
}

// exitFacets are the facets whose class and abstract entries go into exits A and B (§6): ⑤ and ⑤′ go to the
// knowledge base and the inbox instead.
var exitFacets = []entry.Facet{entry.Interaction, entry.Tools, entry.Judgement, entry.Workflow, entry.Persona}

// inExitAB says whether an entry is (or would be) in exit A or B: written there already, or live, stable, of a
// class or abstract grain in a facet those exits carry.
func inExitAB(e *entry.Entry) bool {
	if e.Grain == entry.Action || !e.Live() || !slices.Contains(exitFacets, e.Facet) || e.Sensitive != "" {
		return false
	}
	return e.Export != nil || e.Status == entry.Stable
}

// reviewImpact puts the fixed question of 5.0 to the class and abstract entries in exits A and B: "delete this
// entry: at which moment in the next month would the assistant act differently?" An entry without an answer goes
// back to candidate, with the reason, until what it says changes. The user's own entries and hand-written memory are
// not reviewed. An entry is asked again only when its text changes.
func (c *Curator) reviewImpact(ctx context.Context, m model.Model, run *store.Run) error {
	s := c.St.S
	var todo []*entry.Entry
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		b := basis(e)
		if !inExitAB(e) || userTouched(e) || e.Origin != entry.Behavior ||
			slices.Contains(e.Flags, flagImpactOK+b) || slices.Contains(e.Flags, flagImpactNone+b) {
			continue
		}
		todo = append(todo, e)
	}
	for start := 0; start < len(todo); start += reviewBatch {
		end := min(start+reviewBatch, len(todo))
		in := reviewInput{Lang: c.language(), Question: "impact"}
		for i := start; i < end; i++ {
			in.Items = append(in.Items, reviewItem{ID: fmt.Sprintf("r%d", i-start+1), Facet: string(todo[i].Facet), Text: todo[i].Shown(), Default: todo[i].Default})
		}
		ans, err := c.review(ctx, m, in, run)
		if err != nil {
			return err
		}
		for i := start; i < end; i++ {
			e := todo[i]
			a, ok := ans[fmt.Sprintf("r%d", i-start+1)]
			if !ok {
				continue
			}
			b := basis(e)
			run.Counts["impact_reviewed"]++
			if a.Moment == "" || a.Moment == "none" || strings.TrimSpace(a.Action) == "" {
				e.Flags = dropPrefixed(e.Flags, flagImpactNone)
				e.Flags = appendFlag(e.Flags, flagImpactNone+b)
				e.Updated = c.stamp()
				c.setStatus(e, entry.Candidate, "impact review: "+clip(a.Reason, 100), run.ID)
				run.Counts["impact_none"]++
				continue
			}
			e.Flags = dropPrefixed(e.Flags, flagImpactOK)
			e.Flags = appendFlag(e.Flags, flagImpactOK+b)
			if e.Impact.Moment == "" || e.Impact.Action == "" {
				e.Impact = entry.Impact{Moment: a.Moment, Action: strings.TrimSpace(a.Action), Object: strings.TrimSpace(a.Object)}
			}
			e.Updated = c.stamp()
			c.commit(entry.EvStatus, e, "", "impact review: "+a.Moment, run.ID)
		}
	}
	return nil
}

func dropPrefixed(fs []string, p string) []string {
	return slices.DeleteFunc(fs, func(f string) bool { _, ok := cutPrefix(f, p); return ok })
}

// address are forms of address and titles: a line that is only one of these with an agreement or a greeting says
// nothing about a relation ("好的师父", "thanks boss").
var address = regexp.MustCompile(`(?i)师父|师傅|老师|老板|领导|大佬|大哥|大姐|哥哥|姐姐|哥|姐|兄弟|总|亲|宝|同学|boss|sir|madam|bro|dude|mate|teacher|master|guys?`)

// greeting are words that open or close a line and carry nothing.
var greeting = regexp.MustCompile(`(?i)你好|您好|早上好|晚上好|早|晚安|辛苦了|辛苦|麻烦了|谢谢你|谢谢您|谢啦|拜拜|hi|hello|hey|thank you|morning|bye`)

// CourtesyOnly says whether a quote is only a form of address with agreement, thanks or a greeting: no relation rests
// on it (a polite "好的师父" is not mentoring).
func CourtesyOnly(quote string, names ...string) bool {
	q := norm.Q(quote)
	for _, n := range names {
		if n = norm.Q(n); n != "" {
			q = strings.ReplaceAll(q, n, "")
		}
	}
	q = address.ReplaceAllString(q, "")
	q = greeting.ReplaceAllString(q, "")
	return voice.Agreement(q)
}

// relationNames are the two ends of a relation as written in its text ("A predicate B").
func relationNames(e *entry.Entry) (string, string) {
	if e.Predicate == "" {
		return "", ""
	}
	a, b, ok := strings.Cut(e.Text, " "+e.Predicate+" ")
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(a), strings.TrimSpace(b)
}

// RelationProblem says why a ⑤ relation is not one, or "": an end that is a program (a notification bot as "owner"
// of something), or nothing but courtesy behind it (a polite form of address taken for mentoring). An end that does
// not resolve to a person or project of the knowledge base is not a problem of the relation: it stays (it is still
// understanding of the user's world), marked by unresolvedEnd so it is not written back to the knowledge base.
func RelationProblem(e *entry.Entry) string {
	if e.Facet != entry.World || e.Predicate == "" {
		return ""
	}
	sn, tn := relationNames(e)
	for _, n := range []string{sn, tn} {
		if n != "" && voice.Automated(n) {
			return "bot"
		}
	}
	for _, ev := range e.Evidence {
		if ev.Role == voice.RoleBot && ev.Who != "" && (ev.Who == e.Subject || ev.Who == e.Target) {
			return "bot"
		}
	}
	if len(e.Evidence) > 0 && !slices.ContainsFunc(e.Evidence, func(ev entry.Evidence) bool { return !CourtesyOnly(ev.Quote, sn, tn, ev.Speaker) }) {
		return "courtesy"
	}
	return ""
}

// unresolvedEnd says whether an end of a relation is not a person or project of the knowledge base (a name as
// written, a link, a session): the relation is kept and shown, not written back (5.4).
func unresolvedEnd(e *entry.Entry) bool {
	entity := func(x string) bool { return strings.HasPrefix(x, "person:") || strings.HasPrefix(x, "project:") }
	return e.Facet == entry.World && e.Predicate != "" && (!entity(e.Subject) || !entity(e.Target))
}

// flagUnresolved marks a relation whose end is not a knowledge-base entity.
const flagUnresolved = "unresolved_end"

// cleanRelations retires the relations that are not ones (RelationProblem): kept with the reason, never deleted; a
// relation with an unresolved end is marked, not retired.
func (c *Curator) cleanRelations(run *store.Run) {
	s := c.St.S
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		if !e.Live() || e.Pinned || e.User != nil {
			continue
		}
		if unresolvedEnd(e) && !slices.Contains(e.Flags, flagUnresolved) {
			e.Flags = appendFlag(e.Flags, flagUnresolved)
			c.commit(entry.EvObserve, e, "", flagUnresolved, run.ID)
			run.Counts["relation_unresolved"]++
		}
		if why := RelationProblem(e); why != "" {
			e.Flags = appendFlag(e.Flags, flagRelationOff+why)
			c.setStatus(e, entry.Retired, "not a relation: "+why, run.ID)
			run.Counts["relation_retired:"+why]++
		}
	}
}
