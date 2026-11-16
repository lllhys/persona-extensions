package curate

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/convsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	srcs "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// People cards (design 5.7, ⑤′; P9). The daily pass keeps every observation about a person as an action entry; the
// generic consolidation leaves them to the card pass (PeopleCards, the CardFiler). Once a week the card pass takes
// the people on the intersection list in order, most dealings first, and folds each one's new actions into the
// card's class lines, part by part (facts, working together, the user's own evaluation, inferred), with a tip on how
// to communicate with them for the inbox exit, and an overall way they and the user get along when the abstract gate
// lets it through. The user's own lines are never reworded, deleted lines are never written again, merged person
// entries share one card (kept in the plugin only, undoable). What is ready goes to the person page in the knowledge
// base through the proposal flow.

//go:embed cards.md
var cardsPrompt string

//go:embed cards.schema.json
var cardsSchema []byte

// CardsPrompt is the version of the card prompt, recorded with what it made.
const CardsPrompt = "cards/1"

// Limits of the card pass.
const (
	cardActions     = 40 // a person's actions per call: the best evidenced and newest first; the rest wait
	cardCallsPerRun = 60 // people per weekly pass: in the list's order; the rest the next week
	cardMaxTokens   = 6000
	rhythmCursor    = "people/rhythm"
)

func init() {
	ProposalKinds["person_fact"] = struct {
		Risk  string
		Title string
		KB    bool
	}{"medium", "记进人物页（事实）", true}
	ProposalKinds["person_card"] = struct {
		Risk  string
		Title string
		KB    bool
	}{"medium", "人物画像写进人物页", true}
}

// PeopleCards is the card filer of the consolidation: it keeps every card line for the card pass, which folds a
// person's lines together rather than one at a time.
type PeopleCards struct{}

// CardClass holds the action for the card pass.
func (PeopleCards) CardClass(*store.State, *entry.Entry) (string, *entry.Entry, bool) {
	return "", nil, true
}

type (
	cardPerson struct {
		Ties  map[string]int `json:"ties,omitempty"`
		First string         `json:"first,omitempty"`
		Last  string         `json:"last,omitempty"`
	}
	cardLineIn struct {
		ID         string `json:"id"`
		Part       string `json:"part"`
		Dim        string `json:"dim"`
		Text       string `json:"text"`
		Evidence   int    `json:"evidence"`
		UserEdited bool   `json:"user_edited,omitempty"`
	}
	cardItem struct {
		ID       string   `json:"id"`
		Part     string   `json:"part"`
		Dim      string   `json:"dim,omitempty"`
		Text     string   `json:"text"`
		Day      string   `json:"day"`
		By       string   `json:"by"`
		Reported bool     `json:"reported,omitempty"`
		Quotes   []string `json:"quotes,omitempty"`
	}
	cardInput struct {
		Language        string              `json:"language"`
		Person          cardPerson          `json:"person"`
		InferredAllowed bool                `json:"inferred_allowed"`
		Dims            map[string][]string `json:"dims"`
		Lines           []cardLineIn        `json:"lines"`
		Deleted         []string            `json:"deleted"`
		Items           []cardItem          `json:"items"`
	}
	cardLineOut struct {
		Line          string   `json:"line"`
		Key           string   `json:"key"`
		Part          string   `json:"part"`
		Dim           string   `json:"dim"`
		Text          string   `json:"text"`
		Items         []string `json:"items"`
		ConflictsWith string   `json:"conflicts_with"`
		Ends          string   `json:"ends"`
	}
	cardSituation struct {
		Name  string   `json:"name"`
		Lines []string `json:"lines"`
	}
	cardOverall struct {
		Text       string          `json:"text"`
		Default    string          `json:"default"`
		Lines      []string        `json:"lines"`
		Situations []cardSituation `json:"situations"`
	}
	cardOutput struct {
		Lines   []cardLineOut `json:"lines"`
		Skip    []string      `json:"skip"`
		Advice  string        `json:"advice"`
		Overall *cardOverall  `json:"overall"`
	}
)

// cardItems are a group's actions waiting for the card pass: live, not filed, not left alone, not promises (those are
// the ledger), shown under the switches; the best evidenced and newest first.
func cardItems(s *store.State, persons []string) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range s.Entries {
		if e.Facet != entry.People || e.Grain != entry.Action || !slices.Contains(persons, e.Person) || !e.Live() || e.Parent != "" ||
			e.Commitment != nil || e.Part == entry.PartCommitment || e.Part == people.PartAdvice || !people.Shows(e, s.Switches) {
			continue
		}
		if slices.ContainsFunc(e.Flags, func(f string) bool { _, counter := cutPrefix(f, flagCounterOf); return f == flagStandalone || counter }) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Last != out[j].Last {
			return out[i].Last > out[j].Last
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// cardLines are a group's live card lines (class and abstract, not the tip, not promises).
func cardLines(s *store.State, persons []string) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range s.Entries {
		if e.Facet == entry.People && e.Grain != entry.Action && slices.Contains(persons, e.Person) && e.Live() &&
			e.Part != people.PartAdvice && e.Commitment == nil && people.Shows(e, s.Switches) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// cardDeleted are the texts the user deleted from a group's card: never written again.
func cardDeleted(s *store.State, persons []string) []string {
	var out []string
	for _, t := range s.Tombstones {
		if t.Facet != entry.People {
			continue
		}
		for _, p := range persons {
			if t.Key == tombKey(p, t.Text) && !slices.Contains(out, t.Text) {
				out = append(out, t.Text)
			}
		}
	}
	sort.Strings(out)
	return out
}

// cardTombstoned: the user deleted this judgement (or one worded nearly the same) from the card before.
func cardTombstoned(s *store.State, persons []string, text string) bool {
	for _, d := range cardDeleted(s, persons) {
		if norm.Q(d) == norm.Q(text) || norm.Jaccard(d, text) >= 0.6 {
			return true
		}
	}
	return false
}

// RunCards is the card pass: the people on the intersection list in order, each with new actions, folded into their
// card. Without a model nothing is folded: the actions stay on the card as they are, marked not folded yet. Call it
// with the store's lock held.
func (c *Curator) RunCards(ctx context.Context, m model.Model, today string, run *store.Run) {
	s := c.St.S
	if m == nil {
		return
	}
	calls := 0
	seen := map[string]bool{}
	for _, ct := range people.Ranked(s.Contacts) {
		key := people.Key(s.PersonMerges, ct.Person)
		if seen[key] {
			continue
		}
		seen[key] = true
		persons, _ := people.Group(s.PersonMerges, ct.Person)
		items := cardItems(s, persons)
		if len(items) == 0 {
			continue
		}
		if calls >= cardCallsPerRun {
			run.Counts["cards_waiting"]++
			continue
		}
		if err := ctx.Err(); err != nil {
			return
		}
		calls++
		if len(items) > cardActions {
			run.Counts["cards_actions_waiting"] += len(items) - cardActions
			items = items[:cardActions]
		}
		err := c.cardCall(ctx, m, key, persons, items, today, run)
		switch {
		case errors.Is(err, model.ErrUnavailable):
			run.Notes = append(run.Notes, "画像卡：模型不可用，留到下次整理")
			return
		case err != nil:
			run.Notes = append(run.Notes, fmt.Sprintf("画像卡（%s）：%v", key, err))
			run.Counts["cards_errors"]++
		default:
			run.Counts["cards_people"]++
		}
	}
}

// cardCall folds one group's actions into its card with one call (halves on a refusal).
func (c *Curator) cardCall(ctx context.Context, m model.Model, key string, persons []string, items []*entry.Entry, today string, run *store.Run) error {
	s := c.St.S
	inferred := entry.On(s.Switches, entry.K15Inferred)
	in := cardInput{Language: c.language(), InferredAllowed: inferred, Lines: []cardLineIn{}, Items: []cardItem{},
		Dims:    map[string][]string{"fact": people.FactDims, "collaboration": people.CollabDims, "inferred": people.InferredDims},
		Deleted: cardDeleted(s, persons)}
	if in.Deleted == nil {
		in.Deleted = []string{}
	}
	card := people.BuildCard(c.cardInput(today), key)
	if ct := card.Contact; ct != nil {
		in.Person = cardPerson{Ties: ct.Ties, First: ct.First, Last: ct.Last}
	}
	lineIDs := map[string]string{}
	for i, e := range cardLines(s, persons) {
		id := fmt.Sprintf("L%d", i+1)
		lineIDs[id] = e.ID
		part := e.Part
		if part == "" {
			part = entry.PartFact
		}
		in.Lines = append(in.Lines, cardLineIn{ID: id, Part: part, Dim: e.Type, Text: e.Shown(), Evidence: e.Count, UserEdited: e.User != nil})
	}
	itemIDs := map[string]string{}
	for i, a := range items {
		id := fmt.Sprintf("a%d", i+1)
		itemIDs[id] = a.ID
		by := "other"
		for _, ev := range a.Evidence {
			switch {
			case slices.Contains(persons, ev.Who):
				by = "them"
			case ev.Who == entry.WhoSelf && by == "other":
				by = "user"
			}
		}
		part := a.Part
		if part == "" {
			part = entry.PartFact
		}
		in.Items = append(in.Items, cardItem{ID: id, Part: part, Dim: a.Type, Text: a.Text, Day: a.First, By: by,
			Reported: a.ReportedOnly, Quotes: quotesOf(a, 2)})
	}
	body, _ := json.Marshal(in)
	resp, err := m.Complete(ctx, model.Request{Step: "cards", Tier: model.Small, System: cardsPrompt, User: string(body),
		MaxTokens: cardMaxTokens, Schema: cardsSchema})
	if errors.Is(err, model.ErrRefused) {
		run.Counts["cards_refused"]++
		if len(items) == 1 {
			c.standalone(items[0], "refused", run)
			return nil
		}
		half := len(items) / 2
		if err := c.cardCall(ctx, m, key, persons, items[:half], today, run); err != nil {
			return err
		}
		return c.cardCall(ctx, m, key, persons, items[half:], today, run)
	}
	if err != nil {
		return err
	}
	run.Counts["cards_calls"]++
	var out cardOutput
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &out); err != nil {
		return fmt.Errorf("card answer: %w", err)
	}
	c.applyCard(key, persons, out, lineIDs, itemIDs, today, run)
	return nil
}

// cardPart is the part a line may have given its items: never stronger than what it rests on. A fact rests only on
// facts; the user's evaluation needs one of the user's own evaluations among its items; anything else is inferred.
func cardPart(want string, members []*entry.Entry) string {
	has := func(p string) bool {
		return slices.ContainsFunc(members, func(m *entry.Entry) bool { return m.Part == p })
	}
	all := func(p string) bool {
		return !slices.ContainsFunc(members, func(m *entry.Entry) bool { return m.Part != p && !(p == entry.PartFact && m.Part == "") })
	}
	switch want {
	case entry.PartUserEval:
		if has(entry.PartUserEval) {
			return entry.PartUserEval
		}
	case entry.PartFact:
		if all(entry.PartFact) {
			return entry.PartFact
		}
	}
	return entry.PartInferred
}

// applyCard is the hard check and the filing of one card answer: every item handled at most once, lines only those
// shown, parts no stronger than their items, the user's lines kept as written, deleted lines not made again.
func (c *Curator) applyCard(key string, persons []string, out cardOutput, lineIDs, itemIDs map[string]string, today string, run *store.Run) {
	s := c.St.S
	inferred := entry.On(s.Switches, entry.K15Inferred)
	used := map[string]bool{}
	keys := map[string]string{}
	for _, ol := range out.Lines {
		var members []*entry.Entry
		for _, it := range ol.Items {
			if id := itemIDs[it]; id != "" && !used[it] && s.Entries[id] != nil {
				used[it] = true
				members = append(members, s.Entries[id])
			}
		}
		text := strings.TrimSpace(ol.Text)
		var line *entry.Entry
		if id := lineIDs[ol.Line]; id != "" {
			line = s.Entries[id]
		}
		part := cardPart(ol.Part, members)
		if line != nil && len(members) > 0 && cardPart(line.Part, members) != line.Part && line.Part != entry.PartInferred {
			line = nil // items of a weaker kind than the line: a line of their own
		}
		if line == nil && len(members) == 0 {
			continue
		}
		if line == nil && part == entry.PartInferred && !inferred {
			for _, m := range members {
				used[keyOf(itemIDs, m.ID)] = false
			}
			continue
		}
		if line == nil {
			if text == "" {
				continue
			}
			if cardTombstoned(s, persons, text) {
				for _, m := range members {
					c.standalone(m, "card: deleted by the user", run)
				}
				run.Counts["cards_tombstoned"]++
				continue
			}
			dim := strings.TrimSpace(ol.Dim)
			line = c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: key, Part: part, Type: dim, Text: text,
				Inferred: part == entry.PartInferred, Origin: entry.Behavior, OriginVersion: CardsPrompt,
				Impact: entry.Impact{Moment: "kb_lookup", Action: "answer about this person from their card", Object: key},
				Flags:  []string{people.FlagCard, flagNoAutoMerge}}, run.ID)
			run.Counts["cards_lines_new"]++
		} else if text != "" && text != line.Text && !userTouched(line) && !line.Deleted && !cardTombstoned(s, persons, text) {
			line.Text, line.Updated = text, c.stamp()
			if d := strings.TrimSpace(ol.Dim); d != "" && line.Type == "" {
				line.Type = d
			}
			c.commit(entry.EvFile, line, "", "card reworded", run.ID)
			run.Counts["cards_lines_reworded"]++
		}
		if ol.Key != "" {
			keys[ol.Key] = line.ID
		}
		for _, m := range members {
			if c.File(m.ID, line.ID, run.ID) == nil {
				run.Counts["cards_filed"]++
			}
		}
		if line.Deleted {
			continue // filed into a line the user deleted: dropped with it
		}
		c.cardDerive(line)
		if other := s.Entries[lineIDs[ol.ConflictsWith]]; other != nil && other.ID != line.ID {
			line.Flags = appendFlag(line.Flags, people.FlagConflict+other.ID)
			other.Flags = appendFlag(other.Flags, people.FlagConflict+line.ID)
			c.commit(entry.EvFile, other, line.ID, "card conflict", run.ID)
			run.Counts["cards_conflicts"]++
		}
		if other := s.Entries[lineIDs[ol.Ends]]; other != nil && other.ID != line.ID && line.Part == entry.PartFact && other.Part == entry.PartFact {
			if c.Invalidate(other.ID, line.ID, today, run.ID) == nil {
				run.Counts["cards_facts_ended"]++
			}
		}
	}
	for _, it := range out.Skip {
		if id := itemIDs[it]; id != "" && !used[it] && s.Entries[id] != nil {
			used[it] = true
			c.standalone(s.Entries[id], "card: too thin", run)
			run.Counts["cards_skipped"]++
		}
	}
	if inferred {
		c.cardAdvice(key, persons, strings.TrimSpace(out.Advice), run)
		if out.Overall != nil {
			c.cardOverall(key, persons, *out.Overall, lineIDs, keys, run)
		}
	}
}

func keyOf(m map[string]string, v string) string {
	for k, x := range m {
		if x == v {
			return k
		}
	}
	return ""
}

// cardDerive sets what a line takes from its members: resting only on others' words, a sensitive category, the
// domain, and when it holds from.
func (c *Curator) cardDerive(line *entry.Entry) {
	s := c.St.S
	var ms []*entry.Entry
	for _, id := range line.Members {
		if m := s.Entries[id]; m != nil && !m.Deleted {
			ms = append(ms, m)
		}
	}
	if len(ms) == 0 {
		return
	}
	line.ReportedOnly = !slices.ContainsFunc(ms, func(m *entry.Entry) bool { return !m.ReportedOnly })
	line.Domain = entry.Life
	for _, m := range ms {
		if m.Sensitive != "" && line.Sensitive == "" {
			line.Sensitive = m.Sensitive
		}
		if m.Domain != entry.Life {
			line.Domain = entry.Work
		}
		for _, sc := range m.Scopes {
			line.Scopes = unionScopes(line.Scopes, []entry.Scope{sc})
		}
	}
	if line.ValidFrom == "" {
		line.ValidFrom = line.First
	}
}

// neverForCard are the card's evaluation and inferred lines and the user's words they rest on: a tip that repeats
// one of them is not a tip.
func neverForCard(s *store.State, persons []string) []string {
	var out []string
	for _, e := range s.Entries {
		if e.Facet != entry.People || !slices.Contains(persons, e.Person) || (e.Part != entry.PartUserEval && e.Part != entry.PartInferred) {
			continue
		}
		out = append(out, e.Text, e.Shown())
		if e.Part == entry.PartUserEval {
			for _, ev := range e.Evidence {
				if ev.Who == entry.WhoSelf {
					out = append(out, ev.Quote)
				}
			}
		}
		for _, id := range e.Members {
			if m := s.Entries[id]; m != nil && e.Part == entry.PartUserEval {
				for _, ev := range m.Evidence {
					if ev.Who == entry.WhoSelf {
						out = append(out, ev.Quote)
					}
				}
			}
		}
	}
	return out
}

// QuotesCard says whether a text repeats a card's evaluation or inferred line, or the user's words about the person:
// the same check the inbox exit makes on drafts (never_quote), made before a tip is kept.
func QuotesCard(text string, never []string) bool {
	t := norm.Q(text)
	if t == "" {
		return false
	}
	for _, n := range never {
		q := norm.Q(n)
		if len([]rune(q)) < 6 {
			continue
		}
		if strings.Contains(t, q) || strings.Contains(q, t) {
			return true
		}
	}
	return false
}

// cardAdvice keeps a card's tip: one per card, reworded as the cards change, never the user's own wording replaced,
// never one that repeats an evaluation or inferred line.
func (c *Curator) cardAdvice(key string, persons []string, text string, run *store.Run) {
	s := c.St.S
	if text == "" {
		return
	}
	if QuotesCard(text, neverForCard(s, persons)) {
		run.Counts["cards_advice_quotes"]++
		return
	}
	if cardTombstoned(s, persons, text) {
		return
	}
	var lines []string
	for _, e := range cardLines(s, persons) {
		if e.Sensitive == "" {
			lines = append(lines, e.ID)
		}
	}
	for _, e := range s.Entries {
		if e.Facet == entry.People && e.Part == people.PartAdvice && e.Person == key && !e.Deleted && e.MergedInto == "" && e.Status != entry.Retired {
			if userTouched(e) || e.Text == text {
				return
			}
			e.Text, e.Members, e.Updated = text, lines, c.stamp()
			recount(s, e)
			c.commit(entry.EvFile, e, "", "card advice", run.ID)
			run.Counts["cards_advice_updated"]++
			return
		}
	}
	c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: key, Part: people.PartAdvice, Type: "communication", Text: text,
		Inferred: true, Members: lines, Origin: entry.Behavior, OriginVersion: CardsPrompt, Domain: entry.Work,
		Impact: entry.Impact{Moment: "reply_person", Action: "adjust tone and focus when triaging or drafting a reply to this person", Object: key},
		Flags:  []string{people.FlagCard, flagNoAutoMerge}}, run.ID)
	run.Counts["cards_advice_new"]++
}

// cardOverall keeps the overall way the person and the user get along when it passes the abstract gate (5.3).
func (c *Curator) cardOverall(key string, persons []string, o cardOverall, lineIDs, keys map[string]string, run *store.Run) {
	s := c.St.S
	ref := func(x string) string {
		if id := lineIDs[x]; id != "" {
			return id
		}
		return keys[x]
	}
	a := &entry.Entry{Facet: entry.People, Grain: entry.Abstract, Person: key, Part: entry.PartInferred, Type: "overall",
		Text: strings.TrimSpace(o.Text), Default: strings.TrimSpace(o.Default), Inferred: true, Origin: entry.Behavior,
		OriginVersion: CardsPrompt, Impact: entry.Impact{Moment: "kb_lookup", Action: "answer about this person from their card", Object: key},
		Flags: []string{people.FlagCard, flagNoAutoMerge}}
	for _, l := range o.Lines {
		if id := ref(l); id != "" && !slices.Contains(a.Members, id) {
			a.Members = append(a.Members, id)
		}
	}
	sit := map[string][]string{}
	for _, x := range o.Situations {
		name := strings.TrimSpace(x.Name)
		if name == "" {
			continue
		}
		for _, l := range x.Lines {
			if id := ref(l); id != "" {
				sit[name] = append(sit[name], id)
			}
		}
		a.Situations = append(a.Situations, name)
	}
	if a.Text == "" || cardTombstoned(s, persons, a.Text) {
		return
	}
	if why := AbstractGate(s, a, sit, false); why != "" {
		run.Counts["cards_overall_blocked_"+why]++
		return
	}
	for _, e := range s.Entries {
		if e.Facet == entry.People && e.Grain == entry.Abstract && e.Person == key && e.Live() && e.Type == "overall" {
			if userTouched(e) {
				return
			}
			e.Text, e.Default, e.Members, e.Situations, e.Updated = a.Text, a.Default, a.Members, a.Situations, c.stamp()
			recount(s, e)
			c.commit(entry.EvFile, e, "", "card overall", run.ID)
			run.Counts["cards_overall_updated"]++
			return
		}
	}
	c.create(a, run.ID)
	run.Counts["cards_overall_new"]++
}

// ---- what the page and the exits read --------------------------------------------------------------------------

// cardInput is what assembling a card reads from the state; the commitment ledger is CommitmentsDue's.
func (c *Curator) cardInput(today string) people.Input {
	s := c.St.S
	due, overdue := CommitmentsDue(s, today)
	in := people.Input{Entries: s.Entries, Contacts: s.Contacts, Merges: s.PersonMerges, Switches: s.Switches, Rhythm: s.Rhythm,
		Names: func(p string) string { return c.personName(speaker.PersonID(p)) }, Due: map[string]bool{}, Overdue: map[string]bool{}}
	for _, e := range due {
		in.Due[e.ID] = true
	}
	for _, e := range overdue {
		in.Overdue[e.ID] = true
	}
	return in
}

// PersonCards lists the cards in the intersection list's order, merged groups once. Call it with the store's lock
// held.
func (c *Curator) PersonCards(today string) []people.Summary { return people.List(c.cardInput(today)) }

// PersonCard is one person's card (the merged group's when they are in one): the five parts, the counts and the
// tip for the inbox, kept apart from the evaluation and inferred lines. Call it with the store's lock held.
func (c *Curator) PersonCard(person, today string) *people.Card {
	return people.BuildCard(c.cardInput(today), person)
}

// PersonTip is the tip on how to communicate with a person, for the inbox exit (E): no evaluation, no quote, empty
// with K14 or K15 off. Call it with the store's lock held.
func (c *Curator) PersonTip(person string) string {
	s := c.St.S
	if !entry.On(s.Switches, entry.K14People) || !entry.On(s.Switches, entry.K15Inferred) {
		return ""
	}
	return people.BuildCard(c.cardInput(c.today()), person).Advice
}

// SetContact is the user adding a person to the intersection list or taking one off (people.Added, people.Removed,
// "" back to the counts). Call it with the store's lock held.
func (c *Curator) SetContact(person, name, choice string) error {
	if speaker.PersonID(person) == "" {
		return fmt.Errorf("not a person: %s", person)
	}
	if choice != people.Added && choice != people.Removed && choice != "" {
		return fmt.Errorf("unknown choice %q", choice)
	}
	people.Set(c.St.S.Contacts, person, name, choice, c.today())
	data, _ := json.Marshal(map[string]string{"person": person, "choice": choice})
	c.event(entry.Event{At: c.stamp(), Kind: entry.EvUser, Entry: person, Detail: "contact", Data: data})
	return nil
}

// MergePeople is the user saying several person entries are one person: kept in the plugin only (the hub's people
// are not touched), the card then covers all of them. Undo with SplitPeople. Call it with the store's lock held.
func (c *Curator) MergePeople(persons []string, name string) (string, error) {
	s := c.St.S
	var ps []string
	for _, p := range persons {
		if speaker.PersonID(p) == "" {
			return "", fmt.Errorf("not a person: %s", p)
		}
		if !slices.Contains(ps, p) {
			ps = append(ps, p)
		}
	}
	if len(ps) < 2 {
		return "", errors.New("a merge takes two person entries or more")
	}
	for _, m := range s.PersonMerges {
		for _, p := range ps {
			if slices.Contains(m.Persons, p) {
				return "", fmt.Errorf("%s is merged already (%s): split that first", p, m.ID)
			}
		}
	}
	id := "M-" + hash(append([]string{c.stamp()}, ps...)...)[:8]
	s.PersonMerges = append(s.PersonMerges, entry.PersonMerge{ID: id, Persons: ps, Name: strings.TrimSpace(name), At: c.stamp()})
	data, _ := json.Marshal(ps)
	c.event(entry.Event{At: c.stamp(), Kind: entry.EvUser, Entry: id, Detail: "person_merge", Data: data})
	return id, nil
}

// SplitPeople undoes a merge: each person entry has its own card again. Card lines keep what was their own; actions of
// the other entries filed into them go back to wait for their own card, and lines merged away come back. Call it with
// the store's lock held.
func (c *Curator) SplitPeople(id string) error {
	s := c.St.S
	i := slices.IndexFunc(s.PersonMerges, func(m entry.PersonMerge) bool { return m.ID == id })
	if i < 0 {
		return fmt.Errorf("no merge %s", id)
	}
	m := s.PersonMerges[i]
	s.PersonMerges = slices.Delete(s.PersonMerges, i, i+1)
	for _, eid := range sortedKeys(s.Entries) {
		e := s.Entries[eid]
		if e.Facet != entry.People || e.Grain == entry.Action || !slices.Contains(m.Persons, e.Person) {
			continue
		}
		moved := 0
		for _, mid := range slices.Clone(e.Members) {
			if a := s.Entries[mid]; a != nil && a.Grain == entry.Action && a.Person != e.Person && a.Parent == e.ID {
				c.detach(a, "")
				moved++
			}
		}
		if moved > 0 && len(e.Members) == 0 && !userTouched(e) && e.Part != people.PartAdvice {
			c.setStatus(e, entry.Retired, "person split", "")
		}
	}
	for _, eid := range sortedKeys(s.Entries) {
		e := s.Entries[eid]
		if e.Facet == entry.People && e.MergedInto != "" && slices.Contains(m.Persons, e.Person) {
			if into := s.Entries[e.MergedInto]; into != nil && into.Person != e.Person {
				_ = c.Unmerge(e.ID, "")
			}
		}
	}
	data, _ := json.Marshal(m.Persons)
	c.event(entry.Event{At: c.stamp(), Kind: entry.EvUser, Entry: id, Detail: "person_split", Data: data})
	return nil
}

// ---- rhythm and catching up ------------------------------------------------------------------------------------

// observeRhythm counts the direct chats' rhythm of the days since it last did, up to today (the first time, the
// contactsWarmup days before), reading the days' units again: no model.
func (c *Curator) observeRhythm(ctx context.Context, today string) {
	s := c.St.S
	cur := s.Cursors[rhythmCursor]
	start := addDays(today, -contactsWarmup)
	if cur != "" {
		start = addDays(cur, 1)
	}
	for d := start; d <= today; d = addDays(d, 1) {
		us, err := c.units(ctx, d)
		if err != nil {
			c.logf("rhythm %s: %v", d, err)
			continue
		}
		people.ObserveRhythm(s.Rhythm, d, c.contactUnits(us))
		s.Cursors[rhythmCursor] = d
	}
}

// PeopleCatchUp counts the intersection list and the direct chats' rhythm for days already curated without them (a
// state from before the list, a replay): no model. Call it with the store's lock held.
func (c *Curator) PeopleCatchUp(ctx context.Context, from, to string) error {
	for d := from; d <= to; d = addDays(d, 1) {
		us, err := c.units(ctx, d)
		if err != nil {
			return err
		}
		c.observeContacts(ctx, d, us)
	}
	c.observeRhythm(ctx, to)
	return nil
}

// weeklyPeople is the people part of the weekly pass: the rhythm brought up to date, the card pass, and the person
// pages proposed.
func (c *Curator) weeklyPeople(ctx context.Context, m model.Model, days []string, run *store.Run) {
	today := days[len(days)-1]
	c.observeRhythm(ctx, today)
	c.RunCards(ctx, m, today, run)
	c.proposePeople(ctx, today, run)
	run.Notes = append(run.Notes, c.peopleNotes(today, run)...)
}

// peopleNotes are the weekly report's lines on people: the promises either way due soon or possibly overdue (never
// closed by the curator), what the card pass did, and how the cards are used.
func (c *Curator) peopleNotes(today string, run *store.Run) []string {
	s := c.St.S
	var out []string
	due, overdue := CommitmentsDue(s, today)
	line := func(e *entry.Entry, mark string) string {
		cm := e.Commitment
		who, head := cm.To, "用户答应"
		if cm.To == entry.WhoSelf {
			who, head = cm.From, "对方答应用户"
		}
		name := c.personName(speaker.PersonID(who))
		if name == "" {
			name = who
		}
		return fmt.Sprintf("承诺（%s）：%s%s：%s（到期 %s）", mark, name, head, clip(cm.What, 60), cm.Due)
	}
	for _, e := range due {
		if e.Sensitive == "" {
			out = append(out, line(e, "快到期"))
		}
	}
	for _, e := range overdue {
		if e.Sensitive == "" {
			out = append(out, line(e, "可能逾期，没有完成的证据，不自动关"))
		}
	}
	if n := run.Counts["cards_people"]; n > 0 || run.Counts["proposal_person_card"] > 0 {
		wait := "等 hub 放行后才写"
		if c.ProfilesReleased() {
			wait = "按提议流程写"
		}
		out = append(out, fmt.Sprintf("画像卡：本周整理 %d 人，新行 %d、改写 %d、冲突并排 %d；人物页提议：事实 %d 条，整张卡 %d 份（分四部分写成人物画像，%s；没有出处没写 %d 条，超出上限没写 %d 条）",
			n, run.Counts["cards_lines_new"], run.Counts["cards_lines_reworded"], run.Counts["cards_conflicts"],
			run.Counts["proposal_person_fact"], run.Counts["proposal_person_card"], wait, run.Counts["profile_no_source"], run.Counts["profile_dropped"]))
		if entry.On(s.Switches, entry.K14People) && entry.On(s.Switches, entry.K15Inferred) {
			out = append(out, "画像卡的用法：起草回复时只用每人一条「沟通上要注意什么」来调语气和重点，评价和推断的原句不会写进草稿；但语气本身可能让对方有所察觉，不想这样用可以在开关页关掉 K15 或 K14")
		}
	}
	for _, w := range c.ProfileWarnings() {
		out = append(out, "人物画像没送到："+w)
	}
	return out
}

// ---- the person page (exit C) ------------------------------------------------------------------------------------

// proposePeople proposes writing the cards to their person pages: each fact line pinned on its own with where it
// comes from (the hub takes that on any build), and the whole card as this extension's person profile (facts, the
// user's own words, inferences and commitments kept apart, every item with where it comes from; profiles.go),
// blocked until the hub is released for it (ProfileMinHubBuild). K14 off writes nothing; K15 off leaves the
// inferences out; a sensitive category only when the user turned it on (K13, on the card already).
//
// A card of several person entries the user merged (kept in the plugin only) is one person to the user: every
// person entry of the merge gets the merged card's profile, so whichever entry the assistant looks up has all of
// it.
func (c *Curator) proposePeople(ctx context.Context, today string, run *store.Run) {
	s := c.St.S
	c.ReleaseProfiles()
	if !entry.On(s.Switches, entry.K14People) {
		return
	}
	var k *known
	seen := map[string]bool{}
	pins := 0
	in := c.cardInput(today)
	inferred := entry.On(s.Switches, entry.K15Inferred)
	for _, ct := range people.Ranked(s.Contacts) {
		key := people.Key(s.PersonMerges, ct.Person)
		if seen[key] {
			continue
		}
		seen[key] = true
		card := people.BuildCard(in, key)
		asOf := cardAsOf(card)
		if asOf == "" && !slices.ContainsFunc(card.Persons, func(p string) bool { return c.profileWritten("people/" + speaker.PersonID(p)) }) {
			continue // nothing folded yet, and nothing written to take down
		}
		page := people.RenderPage(card, people.Options{Inferred: inferred, Today: asOf, Lang: c.language()})
		prof, st := c.cardProfile(card, inferred)
		run.Counts["profile_no_source"] += st.NoSource
		run.Counts["profile_dropped"] += st.Dropped
		if k == nil {
			k = c.knownEntities(ctx)
		}
		var ids []string
		for _, sec := range people.Sections {
			for _, l := range card.Parts[sec] {
				if !l.Pending {
					ids = append(ids, l.ID)
				}
			}
		}
		for _, p := range card.Persons {
			pid := speaker.PersonID(p)
			if k.people[pid] == "" {
				continue
			}
			target := "people/" + pid
			for _, f := range page.Facts {
				if pins >= PinsPerWeek {
					break
				}
				e := s.Entries[f.Entry]
				var ev []store.Evidence
				for _, id := range append([]string{f.Entry}, e.Members...) {
					if x := s.Entries[id]; x != nil {
						for _, q := range x.Evidence {
							if len(ev) < 8 {
								ev = append(ev, store.Evidence{Chat: q.Ref, Day: q.Day, N: q.N, Speaker: q.Speaker, Role: q.Role, Quote: q.Quote})
							}
						}
					}
				}
				pr := c.propose("person_fact", "人物页事实："+e.Shown(), fmt.Sprintf("写进「%s」的已确认事实，带出处", target), target,
					Change{Update: &wire.KBUpdateParams{Kind: "people", ID: pid}, PinLine: f.Line}, []string{f.Entry}, ev, "")
				if pr.Created == c.today() && pr.Status == "pending" {
					pins++
					run.Counts["proposal_person_fact"]++
				}
			}
			if prof.Empty() && !c.profileWritten(target) {
				continue // nothing to write and nothing written before to take down
			}
			if c.profileSame(target, &prof) {
				continue // the person page has this one already
			}
			detail := fmt.Sprintf("写进「%s」的人物画像（只替换本插件自己那份）：事实 %d、用户原话 %d、推断 %d、承诺 %d；每条带出处，推断带依据；%d 字节",
				target, len(prof.Facts), len(prof.Views), len(prof.Inferences), len(prof.Commitments), kbsrc.ProfileBytes(&prof))
			if st.Dropped > 0 || st.NoSource > 0 {
				detail += fmt.Sprintf("；超出 hub 上限没写 %d 条，没有出处没写 %d 条", st.Dropped, st.NoSource)
			}
			if prof.Empty() {
				detail = fmt.Sprintf("「%s」的卡上已没有能写的内容：从人物页撤下本插件的画像", target)
			}
			needs := ""
			if !c.ProfilesReleased() {
				needs = ProfileNeeds
			}
			pp := prof
			ch := Change{Update: &wire.KBUpdateParams{Kind: "people", ID: pid}, Profile: &pp}
			pr := c.propose("person_card", "人物画像写进人物页", detail, target, ch, ids, nil, needs)
			if pr.Created == c.today() {
				run.Counts["proposal_person_card"]++
				for _, old := range s.Proposals {
					if old != pr && old.Kind == "person_card" && old.Target == target && (old.Status == "pending" || old.Status == "blocked" || old.Status == "later") {
						old.Status, old.Decided = "expired", c.today() // a newer card replaces it
					}
				}
			}
		}
	}
}

// profileWritten says whether a person page has a profile of this extension now (a write not undone, and not a
// removal).
func (c *Curator) profileWritten(target string) bool {
	return c.lastProfile(target) != nil
}

// profileSame says whether the person page has this very profile from this extension now.
func (c *Curator) profileSame(target string, p *kbsrc.PersonProfile) bool {
	last := c.lastProfile(target)
	if last == nil {
		return p.Empty()
	}
	a, _ := json.Marshal(last)
	b, _ := json.Marshal(p)
	return string(a) == string(b)
}

// lastProfile is the profile the last person_card write not undone left on a person page (nil: none).
func (c *Curator) lastProfile(target string) *kbsrc.PersonProfile {
	s := c.St.S
	for i := len(s.Applied) - 1; i >= 0; i-- {
		a := s.Applied[i]
		if a.Kind != "person_card" || a.Entity != target {
			continue
		}
		if a.Undone != "" {
			return nil
		}
		var p kbsrc.PersonProfile
		if json.Unmarshal(a.After, &p) != nil || p.Empty() {
			return nil
		}
		return &p
	}
	return nil
}

// ProfileStats is what building a card's profile left out.
type ProfileStats = kbsrc.FitStats

// cardProfile is a card as the hub's person profile:
//
//   - facts: the fact lines and the non-inferred lines of working together, in the user's version when they edited
//     one; a fact only others said, or one another line disagrees with, says so in its text;
//   - views: the user's own words about the person, quoted as said (a line the user wrote or edited is their words
//     as they are), from where they said them;
//   - inferences (K15 on): the inferred lines and the inferred lines of working together, each with its basis: how
//     many occurrences, whether that is little, whether only others said it, whether another line disagrees;
//   - commitments: the promises either way, owes by direction, due and done; a promise not between the user and
//     them is a fact about them, labelled so.
//
// Every item names where it comes from (profileSource); one with no source the hub can follow is left out. Lines not
// folded yet are left out. Over the hub's bounds the inferences with least evidence go first, then commitments
// (done first), then facts, the user's words last (kbsrc.FitProfile).
func (c *Curator) cardProfile(card *people.Card, inferred bool) (kbsrc.PersonProfile, ProfileStats) {
	s := c.St.S
	zh := !strings.HasPrefix(c.language(), "en") && !strings.HasPrefix(c.language(), "ja")
	say := func(z, e string) string {
		if zh {
			return z
		}
		return e
	}
	const (
		tierInferred = iota * 1_000_000
		tierDone
		tierOpen
		tierFact
		tierView
	)
	var cands []kbsrc.ProfileCandidate
	var noSource int
	add := func(cd kbsrc.ProfileCandidate, sources []string) {
		if len(sources) == 0 {
			noSource++
			return
		}
		if cd.Part == kbsrc.PartCommitments {
			cd.Commitment.Sources = sources
		} else {
			cd.Item.Sources = sources
		}
		cands = append(cands, cd)
	}
	fact := func(l people.Line) {
		text := l.Text
		var marks []string
		if l.ReportedOnly {
			marks = append(marks, say("只来自别人转述", "only from what others said"))
		}
		if len(l.Conflicts) > 0 {
			marks = append(marks, say("与另一条说法冲突", "another line disagrees"))
		}
		if len(marks) > 0 {
			text += say("（"+strings.Join(marks, "；")+"）", " ("+strings.Join(marks, "; ")+")")
		}
		keep := tierFact + l.Evidence
		if l.ReportedOnly {
			keep = tierFact
		}
		add(kbsrc.ProfileCandidate{Part: kbsrc.PartFacts, Item: kbsrc.ProfileItem{Text: text}, Keep: keep}, lineSources(s, l.ID))
	}
	guess := func(l people.Line) {
		var b []string
		if l.Few {
			b = append(b, fmt.Sprintf(say("证据少（%d 条）", "little evidence (%d)"), l.Evidence))
		} else {
			b = append(b, fmt.Sprintf(say("证据 %d 条", "%d occurrences"), l.Evidence))
		}
		if l.Lines > 0 {
			b = append(b, fmt.Sprintf(say("原话 %d 行，其中别人转述 %d 行", "%d lines said, %d of them by others"), l.Lines, l.Reported))
		}
		if l.ReportedOnly {
			b = append(b, say("只来自转述", "only from what others said"))
		}
		if len(l.Conflicts) > 0 {
			b = append(b, say("与另一条说法冲突", "another line disagrees"))
		}
		if l.Dim != "" {
			b = append(b, say("方面：", "aspect: ")+l.Dim)
		}
		if l.UserEdited {
			b = append(b, say("用户改过措辞", "worded by the user"))
		}
		keep := tierInferred + l.Evidence*10
		if !l.ReportedOnly {
			keep += 5
		}
		add(kbsrc.ProfileCandidate{Part: kbsrc.PartInferences, Item: kbsrc.ProfileItem{Text: l.Text, Basis: strings.Join(b, say("；", "; "))}, Keep: keep},
			lineSources(s, l.ID))
	}
	for _, l := range card.Parts[people.SecFacts] {
		if !l.Pending {
			fact(l)
		}
	}
	for _, l := range card.Parts[people.SecCollab] {
		switch {
		case l.Pending:
		case !l.Inferred:
			fact(l)
		case inferred:
			guess(l)
		}
	}
	for _, l := range card.Parts[people.SecUserEval] {
		if l.Pending {
			continue
		}
		if l.UserEdited {
			// The user wrote or edited this line: it is their own word as it stands.
			add(kbsrc.ProfileCandidate{Part: kbsrc.PartViews, Item: kbsrc.ProfileItem{Text: l.Text}, Keep: tierView + l.Evidence + 1}, []string{kbsrc.SourceUser})
			continue
		}
		views := 0
		seen := map[string]bool{}
		for _, ev := range lineEvidence(s, l.ID) {
			if views >= 3 {
				break
			}
			q := strings.TrimSpace(ev.Quote)
			if !userSaid(ev) || q == "" || seen[norm.Q(q)] {
				continue
			}
			seen[norm.Q(q)] = true
			views++
			var src []string
			if x := profileSource(ev); x != "" {
				src = []string{x}
			}
			add(kbsrc.ProfileCandidate{Part: kbsrc.PartViews, Item: kbsrc.ProfileItem{Text: q}, Keep: tierView + l.Evidence}, src)
		}
		if views == 0 {
			noSource++ // an evaluation with none of the user's words behind it: not written as theirs
		}
	}
	if inferred {
		for _, l := range card.Parts[people.SecInferred] {
			if !l.Pending {
				guess(l)
			}
		}
	}
	for _, l := range card.Parts[people.SecCommitments] {
		cm := l.Commitment
		if cm == nil {
			continue
		}
		owes := ""
		switch l.Direction {
		case "user_owes":
			owes = kbsrc.OwesUser
		case "they_owe":
			owes = kbsrc.OwesThem
		}
		if owes == "" {
			// Not between the user and them (a promise to someone else, or a side not resolved): the hub's commitments
			// are the user's and theirs only, so it is a fact about them, labelled.
			mark := say("（对方和别人之间的承诺，不是和用户）", " (a promise between them and someone else, not the user)")
			if cm.From == "" || cm.To == "" {
				mark = say("（承诺，没认出另一方是谁）", " (a promise; the other side is not known)")
			}
			add(kbsrc.ProfileCandidate{Part: kbsrc.PartFacts, Item: kbsrc.ProfileItem{Text: cm.What + mark}, Keep: tierFact}, lineSources(s, l.ID))
			continue
		}
		keep := tierOpen + l.Evidence
		if cm.Done != "" {
			keep = tierDone + l.Evidence
		}
		add(kbsrc.ProfileCandidate{Part: kbsrc.PartCommitments, Keep: keep,
			Commitment: kbsrc.ProfileCommitment{Text: cm.What, Owes: owes, Due: cm.Due, Done: cm.Done != ""}}, lineSources(s, l.ID))
	}
	prof, st := kbsrc.FitProfile(cands)
	st.NoSource += noSource
	return prof, st
}

// userSaid says whether an evidence line is the user's own word.
func userSaid(ev entry.Evidence) bool {
	return ev.Who == entry.WhoSelf || ev.Role == voice.RoleUser
}

// lineEvidence is what a card line rests on: its own evidence and its members', a level at a time, the newest day
// first.
func lineEvidence(s *store.State, id string) []entry.Evidence {
	var out []entry.Evidence
	seen := map[string]bool{}
	level := []string{id}
	for len(level) > 0 {
		var next []string
		for _, x := range level {
			e := s.Entries[x]
			if e == nil || seen[x] || (x != id && e.Deleted) {
				continue
			}
			seen[x] = true
			out = append(out, e.Evidence...)
			next = append(next, e.Members...)
		}
		level = next
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Day > out[j].Day })
	return out
}

// lineSources are the sources of a card line: where its evidence was said, the newest first, distinct, at most
// kbsrc.ProfileSourcesMost.
func lineSources(s *store.State, id string) []string {
	var out []string
	for _, ev := range lineEvidence(s, id) {
		x := profileSource(ev)
		if x == "" || slices.Contains(out, x) {
			continue
		}
		out = append(out, x)
		if len(out) == kbsrc.ProfileSourcesMost {
			break
		}
	}
	return out
}

// profileSource is where an evidence line was said, as the hub takes a profile item's source ("" when none):
//
//   - a knowledge-base chat's day: chats/<id>/messages/<day>.md;
//   - a recording, a document, a meeting, an event of the knowledge base: <kind>/<id>;
//   - a URL as it is;
//   - the user's own words in the assistant's conversations, agent sessions, cards, to-dos and "about me": "user"
//     (none of those is a knowledge-base path); anything else said there is no source.
func profileSource(ev entry.Evidence) string {
	ref := strings.TrimSpace(ev.Ref)
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	kind, id, ok := strings.Cut(ref, "/")
	if !ok || id == "" {
		return ""
	}
	switch kind {
	case "chats":
		if strings.HasPrefix(id, convsrc.Prefix) {
			break // the assistant's conversations: not in the knowledge base
		}
		if _, err := time.Parse("2006-01-02", ev.Day); err != nil {
			return ""
		}
		return "chats/" + id + "/messages/" + ev.Day + ".md"
	case "recordings", "docs", "meetings", "events":
		return kind + "/" + id
	}
	if userSaid(ev) && (kind == "chats" || srcs.DayKind(kind)) && kind != srcs.KindMemory {
		return kbsrc.SourceUser
	}
	return ""
}

// cardAsOf is the last day a card's folded lines rest on ("" when nothing is folded yet): the page says "as of" it,
// so the same card proposed again is the same proposal.
func cardAsOf(card *people.Card) string {
	last := ""
	for _, sec := range people.Sections {
		for _, l := range card.Parts[sec] {
			if !l.Pending && l.Last > last {
				last = l.Last
			}
		}
	}
	return last
}

// RunPeople runs the people part of the weekly pass on its own, as of a day: the intersection list and the rhythm
// caught up from the days given (no model), the card pass, and the person pages proposed. For a state curated before
// the cards (a replay); the scheduled passes do the same within the daily and weekly runs.
func (c *Curator) RunPeople(ctx context.Context, from, to string, dry bool) (*store.Run, error) {
	c.St.Lock()
	defer c.St.Unlock()
	run := &store.Run{ID: fmt.Sprintf("people-%s-%d", to, c.now().Unix()), Kind: "people", Period: to,
		Started: c.stamp(), DryRun: dry, Counts: map[string]int{}}
	if err := c.PeopleCatchUp(ctx, from, to); err != nil {
		return nil, err
	}
	var m model.Model
	var metered *model.Metered
	if c.Model != nil {
		metered = &model.Metered{Model: c.Model, Usage: c.St, Day: to}
		m = metered
	}
	c.RunCards(ctx, m, to, run)
	c.proposePeople(ctx, to, run)
	if metered != nil {
		run.Counts["model_calls"] = metered.Calls
	}
	run.Finished = c.stamp()
	c.St.S.Runs = append(c.St.S.Runs, run)
	if dry {
		return run, nil
	}
	return run, c.St.Save()
}
