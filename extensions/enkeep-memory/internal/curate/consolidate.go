package curate

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Consolidation (design 5.0, 5.2): every new action entry is recalled against the existing entries of its facet and
// scope, the model judges whether it is one more occurrence of one of them, a counter-example, a newer state that
// replaces one, the start of a new class, or a one-off, and the program checks the answer, files it, counts and
// settles the statuses. The model judges meaning, never shared characters, so the assistant's hand-written memory in
// another language is matched all the same.

//go:embed consolidate.md
var consolidatePrompt string

//go:embed consolidate.schema.json
var consolidateSchema []byte

//go:embed abstract.md
var abstractPrompt string

//go:embed abstract.schema.json
var abstractSchema []byte

//go:embed review.md
var reviewPrompt string

//go:embed review.schema.json
var reviewSchema []byte

// ConsolidatePrompt is the version of the consolidation prompts, recorded with what they made.
const ConsolidatePrompt = "consolidate/2"

// Limits of the consolidation calls: inputs cut to a conservative window (5.0), no long context assumed.
const (
	recallTop              = 20    // entries of other types each new one is compared with, by likeness
	recallAll              = 60    // a facet with at most this many entries shows all of them; beyond, all of the same type
	consolidateBatchItems  = 12    // new entries per call
	consolidateBatchTokens = 10000 // the input of one call
	consolidateMaxTokens   = 6000
	sameClass              = 0.85 // bigram Jaccard of two class texts that the hard check takes for one
)

// facetMeaning tells the model what a facet is about (instructions are in English; texts are in the target language).
var facetMeaning = map[entry.Facet]string{
	entry.Interaction: "how the user wants the AI to work with them: asking, reporting, taking on tasks, when not to interrupt, tone and form",
	entry.Tools:       "how the AI works with machines, tools, runtimes, models and gateways: how to reach them, what fails and how it was got around",
	entry.Judgement:   "the user's default choices at a fork: what they choose, what they do not, and why",
	entry.Workflow:    "how the user handles a kind of task: their steps and ways, and why",
	entry.World:       "facts about the world around the user: who is responsible for what, what is blocked where, what a word means, relations",
	entry.People:      "one person the user deals with: facts, what the user said about them, how they work, commitments",
	entry.Persona:     "the user as a person: role, responsibilities, situation, current work, goals, what they value, preferences",
}

// CardFiler lets the people package (P9) place a card line (⑤′ action) by its own card dimensions: the class it
// belongs to, or a fresh class to start. ok false leaves it to the model's judgement like every other facet.
type CardFiler interface {
	CardClass(s *store.State, a *entry.Entry) (classID string, fresh *entry.Entry, ok bool)
}

// pending are the action entries not looked at yet, seen by day, by facet: not filed, not a counter-example, not a
// one-off already, live. World relations are classes already and are not pending.
func pending(s *store.State, day string) map[entry.Facet][]*entry.Entry {
	out := map[entry.Facet][]*entry.Entry{}
	for _, e := range s.Entries {
		if e.Grain != entry.Action || !e.Live() || e.Parent != "" || e.First > day || e.Origin == entry.Handwritten {
			continue
		}
		if slices.ContainsFunc(e.Flags, func(f string) bool {
			_, counter := cutPrefix(f, flagCounterOf)
			return f == flagStandalone || counter
		}) {
			continue
		}
		out[e.Facet] = append(out[e.Facet], e)
	}
	for f := range out {
		sort.Slice(out[f], func(i, j int) bool { // by type, so that a batch shares its candidates
			if ti, tj := norm.Q(out[f][i].Type), norm.Q(out[f][j].Type); ti != tj {
				return ti < tj
			}
			if out[f][i].First != out[f][j].First {
				return out[f][i].First < out[f][j].First
			}
			return out[f][i].ID < out[f][j].ID
		})
	}
	return out
}

// overlaps says whether two entries hold in a common place: a global scope overlaps every scope.
func overlaps(a, b []entry.Scope) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if x.Kind == entry.ScopeGlobal || y.Kind == entry.ScopeGlobal || x.Kind == y.Kind && norm.Q(x.Name) == norm.Q(y.Name) {
				return true
			}
		}
	}
	return false
}

// candidatePool is what an action may be filed under: entries of its facet and scope (for a person's card line, of
// the same person; for a fact, the facts about the same subject), the user's own and the hand-written ones included,
// and deleted ones (filing into one drops the action: the same content again).
func candidatePool(s *store.State, a *entry.Entry) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range s.Entries {
		if e.ID == a.ID || e.Facet != a.Facet || e.MergedInto != "" || e.ValidTo != "" || e.Status == entry.Retired {
			continue
		}
		own := e.Origin != entry.Behavior && e.Origin != ""
		switch a.Facet {
		case entry.World:
			// facts about the same subject seen before it, and the subject's relations (a fact may end one)
			if e.First > a.First || (a.Subject != "" && e.Subject != a.Subject) || e.Grain == entry.Abstract ||
				(e.Grain == entry.Class && (e.Predicate == "" || a.Subject == "")) {
				continue
			}
		case entry.People:
			if e.Person != a.Person || (e.Grain != entry.Class && !own) {
				continue
			}
		default:
			if e.Grain != entry.Class && !own {
				continue
			}
		}
		if a.Facet != entry.World && a.Facet != entry.People && !overlaps(e.Scopes, a.Scopes) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// recall picks the existing entries an action is compared with. A facet with few entries shows all of them; beyond
// that, every entry of the action's type (or of the type of the class most like it), and the top recallTop of the
// others by type, object, likeness and count. The model judges sameness of situation: the score only decides what it
// is shown, and an entry worded quite differently is shown all the same when it is of the type.
func recall(s *store.State, a *entry.Entry, top int) []*entry.Entry {
	pool := candidatePool(s, a)
	if len(pool) > recallAll {
		score := func(e *entry.Entry) float64 {
			v := 3 * norm.Jaccard(a.Text+a.Situation+a.Choice, e.Text+e.Situation+e.Choice)
			if a.Object != "" && norm.Q(a.Object) == norm.Q(e.Object) {
				v += 2
			}
			if e.Origin == entry.Handwritten || e.Origin == entry.SelfReport {
				v += 1 // few, and often in another language: shown first among equals
			}
			return v + 0.5*math.Log1p(float64(e.Count))
		}
		sort.SliceStable(pool, func(i, j int) bool {
			si, sj := score(pool[i]), score(pool[j])
			if si != sj {
				return si > sj
			}
			return pool[i].ID < pool[j].ID
		})
		types := map[string]bool{}
		if t := norm.Q(resolveType(s, a.Facet, a.Type)); t != "" {
			types[t] = true
		}
		if t := norm.Q(pool[0].Type); t != "" {
			types[t] = true // the type of the most alike
		}
		var same, rest []*entry.Entry
		for _, e := range pool {
			if types[norm.Q(resolveType(s, e.Facet, e.Type))] && len(same) < recallAll-top {
				same = append(same, e)
			} else if len(rest) < top {
				rest = append(rest, e)
			}
		}
		pool = append(same, rest...)
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].ID < pool[j].ID })
	return pool
}

// The input and answer of a consolidation call.
type (
	consItem struct {
		ID         string   `json:"id"`
		Type       string   `json:"type,omitempty"`
		Text       string   `json:"text"`
		Situation  string   `json:"situation,omitempty"`
		Choice     string   `json:"choice,omitempty"`
		Rejected   string   `json:"rejected,omitempty"`
		Why        string   `json:"why,omitempty"`
		Object     string   `json:"object,omitempty"`
		Pitfall    string   `json:"pitfall,omitempty"`
		Fix        string   `json:"fix,omitempty"`
		Part       string   `json:"part,omitempty"`
		Day        string   `json:"day"`
		Quotes     []string `json:"quotes,omitempty"`
		Candidates []string `json:"candidates"`
	}
	consEntry struct {
		ID          string `json:"id"`
		Grain       string `json:"grain"`
		Type        string `json:"type,omitempty"`
		Text        string `json:"text"`
		Situation   string `json:"situation,omitempty"`
		Choice      string `json:"choice,omitempty"`
		Object      string `json:"object,omitempty"`
		Count       int    `json:"count"`
		Handwritten bool   `json:"handwritten,omitempty"`
		Deleted     bool   `json:"deleted,omitempty"`
	}
	consInput struct {
		Facet   string      `json:"facet"`
		Meaning string      `json:"facet_meaning"`
		Lang    string      `json:"language"`
		Rule    string      `json:"rule"`
		Types   []string    `json:"types"`
		Items   []consItem  `json:"items"`
		Entries []consEntry `json:"entries"`
	}
	consNew struct {
		Key       string `json:"key"`
		Type      string `json:"type"`
		Text      string `json:"text"`
		Situation string `json:"situation,omitempty"`
		Choice    string `json:"choice,omitempty"`
		Rejected  string `json:"rejected,omitempty"`
		Why       string `json:"why,omitempty"`
		Exception string `json:"exception,omitempty"`
		Object    string `json:"object,omitempty"`
	}
	consDecision struct {
		Item   string   `json:"item"`
		Action string   `json:"action"`
		Entry  string   `json:"entry,omitempty"`
		Reason string   `json:"reason"`
		New    *consNew `json:"new,omitempty"`
	}
	consOutput struct {
		Decisions []consDecision `json:"decisions"`
	}
)

func quotesOf(e *entry.Entry, n int) []string {
	var out []string
	for _, ev := range e.Evidence {
		if q := strings.TrimSpace(ev.Quote); q != "" && len(out) < n {
			out = append(out, clip(q, 160))
		}
	}
	return out
}

// Consolidate files every pending action seen by day (design 5.0 steps 1–4) and settles the statuses. Without a
// model nothing is judged: the actions wait for the next run with one. Call it with the store's lock held.
func (c *Curator) Consolidate(ctx context.Context, m model.Model, day string, run *store.Run) error {
	s := c.St.S
	c.noteActiveDays(day)
	c.seedTypes()
	for _, f := range entry.Facets {
		as := pending(s, day)[f]
		if len(as) == 0 {
			continue
		}
		run.Counts["consolidate_pending"] += len(as)
		if f == entry.People && c.Cards != nil {
			as = c.fileCards(as, run)
		}
		if m == nil {
			continue
		}
		for len(as) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			batch := c.consBatch(as)
			as = as[len(batch):]
			if err := c.judgeBatch(ctx, m, f, batch, day, run); err != nil {
				if errors.Is(err, model.ErrUnavailable) {
					run.Notes = append(run.Notes, "整理：模型不可用，新条目留到下次归档")
					return nil
				}
				run.Notes = append(run.Notes, fmt.Sprintf("整理（%s）：%v", f, err))
				run.Counts["consolidate_errors"]++
			}
		}
	}
	c.cleanRelations(run)
	c.learnTypes(run)
	c.settleAll(day, run)
	return nil
}

// fileCards lets the card filer place what it can; the rest is returned for the model.
func (c *Curator) fileCards(as []*entry.Entry, run *store.Run) []*entry.Entry {
	var rest []*entry.Entry
	for _, a := range as {
		id, fresh, ok := c.Cards.CardClass(c.St.S, a)
		switch {
		case !ok:
			rest = append(rest, a)
			continue
		case id == "" && fresh != nil:
			fresh.Facet, fresh.Grain, fresh.Person = entry.People, entry.Class, a.Person
			id = c.create(fresh, run.ID).ID
		}
		if id != "" && c.File(a.ID, id, run.ID) == nil {
			run.Counts["consolidate_card_filed"]++
		}
	}
	return rest
}

// consBatch takes the next actions that fit one call: the items and the union of their candidates within the budget.
func (c *Curator) consBatch(as []*entry.Entry) []*entry.Entry {
	size := 0
	shown := map[string]bool{}
	for i, a := range as {
		t := norm.Tokens(a.Text+a.Situation+a.Choice+a.Why) + 120
		var fresh []string
		for _, e := range recall(c.St.S, a, recallTop) {
			if !shown[e.ID] {
				fresh = append(fresh, e.ID)
				t += norm.Tokens(e.Shown()+e.Situation+e.Choice) + 30
			}
		}
		if i > 0 && (i >= consolidateBatchItems || size+t > consolidateBatchTokens) {
			return as[:i]
		}
		for _, id := range fresh {
			shown[id] = true
		}
		size += t
	}
	return as
}

// judgeBatch asks the model about a batch of one facet's actions and applies the checked answer. A batch the model
// declines is split; a single action it declines is left standing alone.
func (c *Curator) judgeBatch(ctx context.Context, m model.Model, f entry.Facet, batch []*entry.Entry, day string, run *store.Run) error {
	s := c.St.S
	in := consInput{Facet: string(f), Meaning: facetMeaning[f], Lang: c.language(), Types: c.typeNames(f), Rule: mergeRule[f], Entries: []consEntry{}}
	local := map[string]string{} // short id → entry id
	short := map[string]string{} // entry id → short id
	allowed := map[string][]string{}
	for i, a := range batch {
		it := consItem{ID: fmt.Sprintf("a%d", i+1), Type: a.Type, Text: a.Text, Situation: a.Situation, Choice: a.Choice, Rejected: a.Rejected,
			Why: a.Why, Object: a.Object, Pitfall: a.Pitfall, Fix: a.Fix, Part: a.Part, Day: a.First, Quotes: quotesOf(a, 2), Candidates: []string{}}
		for _, e := range recall(s, a, recallTop) {
			id, ok := short[e.ID]
			if !ok {
				id = fmt.Sprintf("e%d", len(short)+1)
				short[e.ID], local[id] = id, e.ID
				in.Entries = append(in.Entries, consEntry{ID: id, Grain: string(e.Grain), Type: e.Type, Text: e.Shown(), Situation: e.Situation,
					Choice: e.Choice, Object: e.Object, Count: e.Count, Handwritten: e.Origin == entry.Handwritten, Deleted: e.Deleted})
			}
			it.Candidates = append(it.Candidates, id)
		}
		local[it.ID] = a.ID
		allowed[it.ID] = it.Candidates
		in.Items = append(in.Items, it)
	}
	body, _ := json.Marshal(in)
	resp, err := m.Complete(ctx, model.Request{Step: "consolidate", Tier: model.Small, System: consolidatePrompt, User: string(body),
		MaxTokens: consolidateMaxTokens, Schema: consolidateSchema})
	if errors.Is(err, model.ErrRefused) {
		run.Counts["consolidate_refused"]++
		if len(batch) == 1 {
			c.standalone(batch[0], "refused", run)
			return nil
		}
		half := len(batch) / 2
		if err := c.judgeBatch(ctx, m, f, batch[:half], day, run); err != nil {
			return err
		}
		return c.judgeBatch(ctx, m, f, batch[half:], day, run)
	}
	if err != nil {
		return err
	}
	run.Counts["consolidate_calls"]++
	var out consOutput
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &out); err != nil {
		return fmt.Errorf("consolidation answer: %w", err)
	}
	c.applyDecisions(f, batch, out.Decisions, local, allowed, run)
	return nil
}

// extractJSON cuts the outermost JSON object out of an answer that may carry text around it.
func extractJSON(t string) string {
	i, j := strings.Index(t, "{"), strings.LastIndex(t, "}")
	if i < 0 || j < i {
		return t
	}
	return t[i : j+1]
}

// applyDecisions is the hard check and the filing (5.0 steps 2–3): every action is handled exactly once (the first
// answer for it counts, an action with none waits for the next run), an entry named must be one it was shown, a new
// class whose name or text is an existing class's is filed there instead, and a new class the user deleted before is
// not made again.
func (c *Curator) applyDecisions(f entry.Facet, batch []*entry.Entry, ds []consDecision, local map[string]string, allowed map[string][]string,
	run *store.Run) {
	s := c.St.S
	done := map[string]bool{}
	byKey := map[string]string{} // new class key → class id made in this batch
	for _, d := range ds {
		aid := local[d.Item]
		a := s.Entries[aid]
		if a == nil || done[d.Item] {
			run.Counts["consolidate_answer_dropped"]++
			continue
		}
		done[d.Item] = true
		target := ""
		if d.Entry != "" {
			if !slices.Contains(allowed[d.Item], d.Entry) {
				if d.Action == "file" || d.Action == "counter" || d.Action == "replaces" {
					run.Counts["consolidate_bad_target"]++
					d.Action = "none" // named something it was not shown: not trusted
				}
			} else {
				target = local[d.Entry]
			}
		}
		why := clip(strings.TrimSpace(d.Reason), 120)
		switch d.Action {
		case "file":
			c.fileInto(a, s.Entries[target], why, run)
		case "counter":
			t := s.Entries[target]
			if t == nil {
				c.standalone(a, "no target", run)
				break
			}
			if t.Grain == entry.Action { // facts that disagree: both kept, the knowledge base's conflict flow decides
				a.Flags = appendFlag(a.Flags, "conflicts_with:"+t.ID)
				c.standalone(a, "conflict", run)
				run.Counts["consolidate_conflict"]++
				break
			}
			if err := c.Counter(a.ID, t.ID, run.ID); err == nil {
				run.Counts["consolidate_counter"]++
			}
		case "replaces":
			t := s.Entries[target]
			if t == nil {
				c.standalone(a, "no target", run)
				break
			}
			if err := c.Invalidate(t.ID, a.ID, a.First, run.ID); err != nil {
				run.Counts["consolidate_protected"]++
				c.standalone(a, "protected", run)
				break
			}
			run.Counts["consolidate_replaced"]++
			if f == entry.World || t.Grain == entry.Action {
				c.standalone(a, "replaces", run)
				break
			}
			nc := &consNew{Type: t.Type, Text: a.Text, Situation: a.Situation, Choice: a.Choice, Rejected: a.Rejected, Why: a.Why,
				Exception: a.Exception, Object: a.Object}
			c.newClass(a, nc, byKey, run)
		case "new":
			if f == entry.World || d.New == nil || strings.TrimSpace(d.New.Text) == "" {
				c.standalone(a, "fact", run)
				break
			}
			c.newClass(a, d.New, byKey, run)
		default:
			c.standalone(a, "one-off", run)
		}
	}
	for i := range batch {
		if !done[fmt.Sprintf("a%d", i+1)] {
			run.Counts["consolidate_unanswered"]++
		}
	}
}

// fileInto files an action under the target: into a deleted entry it is dropped (the user threw the same content
// away), into a fact it is merged (the same fact once more).
func (c *Curator) fileInto(a, t *entry.Entry, why string, run *store.Run) {
	switch {
	case t == nil:
		c.standalone(a, "no target", run)
	case t.Deleted:
		c.dropTombstoned(a, run)
	case t.Grain == entry.Action:
		if err := c.Merge(a.ID, t.ID, "same fact: "+why, run.ID); err == nil {
			run.Counts["consolidate_same_fact"]++
		}
	default:
		if err := c.File(a.ID, t.ID, run.ID); err == nil {
			run.Counts["consolidate_filed"]++
			if t.Origin == entry.Handwritten {
				run.Counts["consolidate_handwritten_counted"]++
			}
		}
	}
}

// newClass makes the class an action starts, after the hard check: a class of this batch with the same key, a
// deleted class's text, or an existing class with the same text or the same type and situation takes it instead.
func (c *Curator) newClass(a *entry.Entry, n *consNew, byKey map[string]string, run *store.Run) {
	s := c.St.S
	text := strings.TrimSpace(n.Text)
	if id := byKey[n.Key]; n.Key != "" && id != "" {
		c.fileInto(a, s.Entries[id], "same new class", run)
		return
	}
	person := ""
	if a.Facet == entry.People {
		person = a.Person
	}
	if tombstoned(s, a.Facet, text, person) {
		c.dropTombstoned(a, run)
		return
	}
	if same := c.sameClass(a, text, n); same != nil {
		same.Flags = appendFlag(same.Flags, flagAutoMerged)
		c.fileInto(a, same, "same class", run)
		run.Counts["consolidate_hard_merged"]++
		if n.Key != "" {
			byKey[n.Key] = same.ID
		}
		return
	}
	typ := strings.TrimSpace(n.Type)
	if typ == "" {
		typ = a.Type
	}
	k := c.create(&entry.Entry{Facet: a.Facet, Grain: entry.Class, Type: typ, Text: text, Situation: strings.TrimSpace(n.Situation),
		Choice: strings.TrimSpace(n.Choice), Rejected: strings.TrimSpace(n.Rejected), Why: strings.TrimSpace(n.Why),
		Exception: strings.TrimSpace(n.Exception), Object: firstNonEmpty(strings.TrimSpace(n.Object), a.Object), Pitfall: a.Pitfall, Fix: a.Fix,
		Person: person, Part: a.Part, Inferred: a.Inferred, ReportedOnly: a.ReportedOnly, Domain: a.Domain, Sensitive: a.Sensitive,
		Scopes: slices.Clone(a.Scopes), Impact: a.Impact, OriginVersion: ConsolidatePrompt}, run.ID)
	c.noteType(a.Facet, typ)
	if n.Key != "" {
		byKey[n.Key] = k.ID
	}
	run.Counts["consolidate_new_class"]++
	c.fileInto(a, k, "new class", run)
}

// sameClass is the hard check's match: a live class of the facet (and person) with the same text, the same type and
// situation, or a text nearly the same, unless the user undid such a merge.
func (c *Curator) sameClass(a *entry.Entry, text string, n *consNew) *entry.Entry {
	var best *entry.Entry
	top := sameClass
	for _, id := range sortedKeys(c.St.S.Entries) {
		e := c.St.S.Entries[id]
		if e.Facet != a.Facet || e.Grain != entry.Class || !e.Live() || slices.Contains(e.Flags, flagNoAutoMerge) ||
			(a.Facet == entry.People && e.Person != a.Person) || !overlaps(e.Scopes, a.Scopes) {
			continue
		}
		if norm.Q(e.Text) == norm.Q(text) || n.Situation != "" && norm.Q(e.Type) == norm.Q(n.Type) && norm.Q(e.Situation) == norm.Q(n.Situation) &&
			norm.Q(e.Choice) == norm.Q(n.Choice) {
			return e
		}
		if j := norm.Jaccard(e.Text, text); j >= top {
			best, top = e, j
		}
	}
	return best
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// standalone marks an action looked at and left alone: a one-off, a fact of its own.
func (c *Curator) standalone(a *entry.Entry, why string, run *store.Run) {
	a.Flags = appendFlag(a.Flags, flagStandalone)
	a.Updated = c.stamp()
	c.commit(entry.EvFile, a, "", "standalone: "+why, run.ID)
	run.Counts["consolidate_standalone"]++
}

// dropTombstoned: the action says what the user deleted before: it is deleted too.
func (c *Curator) dropTombstoned(a *entry.Entry, run *store.Run) {
	a.Deleted = true
	a.Flags = appendFlag(a.Flags, "tombstoned")
	a.Updated = c.stamp()
	c.commit(entry.EvFile, a, "", "tombstoned", run.ID)
	run.Counts["consolidate_tombstoned"]++
}

// noteActiveDays adds the day, and every day an entry was seen on, to the user's active days (days with any source
// data, 4.4): the thresholds count these, not the calendar.
func (c *Curator) noteActiveDays(day string) {
	s := c.St.S
	add := func(d string) {
		if d == "" || len(d) != 10 {
			return
		}
		i := sort.SearchStrings(s.ActiveDays, d)
		if i < len(s.ActiveDays) && s.ActiveDays[i] == d {
			return
		}
		s.ActiveDays = slices.Insert(s.ActiveDays, i, d)
	}
	add(day)
	for _, e := range s.Entries {
		if e.Grain == entry.Action && e.First <= day {
			for _, o := range occurrences(e) {
				if o.day <= day {
					add(o.day)
				}
			}
		}
	}
}

// MarkActive records that day had source data (the daily pass, whenever it reads any unit).
func (c *Curator) MarkActive(day string) {
	s := c.St.S
	if !slices.Contains(s.ActiveDays, day) {
		s.ActiveDays = append(s.ActiveDays, day)
		sort.Strings(s.ActiveDays)
	}
}

// basis is the hash of what an entry says: a review holds while it is unchanged.
func basis(e *entry.Entry) string {
	return hash(e.Shown(), e.Situation, e.Choice, e.Default)[:10]
}

// settleAll recounts the classes and abstract entries and settles every live entry's status as of today (4.4). A
// class the weekly review found no impact for stays a candidate while it says the same.
func (c *Curator) settleAll(today string, run *store.Run) {
	s := c.St.S
	ids := sortedKeys(s.Entries)
	for _, g := range []entry.Grain{entry.Action, entry.Class, entry.Abstract} {
		for _, id := range ids {
			e := s.Entries[id]
			if e.Grain != g || !e.Live() || e.Status == entry.Retired {
				continue
			}
			if g != entry.Action {
				recount(s, e)
			}
			d, ask := Settle(s, e, tally(s, e), today)
			if g == entry.Abstract && d.Status == entry.Stable && !abstractStable(s, e) {
				d = Decision{entry.Candidate, "fewer than two stable member classes"}
			}
			if d.Status == entry.Stable && slices.Contains(e.Flags, flagImpactNone+basis(e)) && !userTouched(e) {
				d = Decision{entry.Candidate, "no impact found by the weekly review"}
			}
			if ask {
				e.Flags = appendFlag(e.Flags, flagAskedDormant)
				run.Counts["ask_dormant"]++
			}
			if d.Status != e.Status {
				run.Counts["status_"+string(d.Status)]++
				c.setStatus(e, d.Status, d.Why, run.ID)
			}
		}
	}
}

// abstractStable: an abstract entry is stable when at least two of its member classes are.
func abstractStable(s *store.State, e *entry.Entry) bool {
	n := 0
	for _, id := range e.Members {
		if m := s.Entries[id]; m != nil && m.Live() && m.Status == entry.Stable {
			n++
		}
	}
	return n >= 2 || e.Origin != entry.Behavior
}
