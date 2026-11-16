package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// review reads a state directory (a replay's or a copy of the extension's) and writes what the acceptance checks
// look at (design §9.3–9.5, §10.3): per facet the counts by grain and status and the top entries the user is shown
// (at most 10), the cards of the people dealt with most (at most 10), 30 attributed lines per source, a sample of
// facts with their sources for checking, and a sample of exported entries for the empty-talk check. It reads only;
// the samples are drawn with a fixed seed so a rerun gives the same lines.
//
//	enkeep-memory review -state DIR -out DIR [-seed N]
func review(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	stateDir := fs.String("state", "", "state directory (read only)")
	out := fs.String("out", "", "directory for review.json and review.md")
	seed := fs.Int64("seed", 20261112, "seed of the samples")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stateDir == "" || *out == "" {
		return fmt.Errorf("-state and -out are required")
	}
	raw, err := os.ReadFile(filepath.Join(*stateDir, "state.json"))
	if err != nil {
		return err
	}
	st := &store.State{}
	if err := json.Unmarshal(raw, st); err != nil {
		return err
	}
	r := buildReview(st, rand.New(rand.NewSource(*seed)))
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "review.json"), b, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "review.md"), []byte(r.markdown()), 0o600); err != nil {
		return err
	}
	fmt.Print(r.summary())
	return nil
}

type reviewFacet struct {
	Facet   entry.Facet    `json:"facet"`
	Total   int            `json:"total"`
	ByGrain map[string]int `json:"by_grain"`
	ByState map[string]int `json:"by_status"`
	Top     []reviewEntry  `json:"top"` // U1–U6: at most 10, abstract entries at least 2 when there are any
}

type reviewEntry struct {
	ID       string           `json:"id"`
	Grain    entry.Grain      `json:"grain"`
	Type     string           `json:"type,omitempty"`
	Text     string           `json:"text"`
	Status   entry.Status     `json:"status"`
	Count    int              `json:"count"`
	Kinds    []string         `json:"kinds,omitempty"`
	Domain   string           `json:"domain,omitempty"`
	Inferred bool             `json:"inferred,omitempty"`
	Part     string           `json:"part,omitempty"`
	Impact   entry.Impact     `json:"impact"`
	Evidence []entry.Evidence `json:"evidence,omitempty"`
}

type reviewCard struct {
	Person  string                   `json:"person"`
	Name    string                   `json:"name"`
	Score   int                      `json:"score"`
	Ties    map[string]int           `json:"ties"`
	Parts   map[string][]reviewEntry `json:"parts"`
	Reports int                      `json:"reported_only"`
}

type reviewReport struct {
	Entries  int                             `json:"entries"`
	Facets   []reviewFacet                   `json:"facets"`
	Cards    []reviewCard                    `json:"cards"`    // U7
	Speakers map[string][]entry.SpeakerCheck `json:"speakers"` // U8, by connector
	Facts    []reviewEntry                   `json:"facts"`    // F1
	Empty    []reviewEntry                   `json:"empty_talk_sample"`
	Abstract []reviewEntry                   `json:"abstract_sample"`
	Sources  []*entry.SourceStatus           `json:"sources"`
	Exits    *entry.Exits                    `json:"exits,omitempty"`
}

func toReview(e *entry.Entry, withEvidence bool) reviewEntry {
	r := reviewEntry{ID: e.ID, Grain: e.Grain, Type: e.Type, Text: e.Shown(), Status: e.Status, Count: e.Count, Kinds: e.Kinds,
		Domain: e.Domain, Inferred: e.Inferred, Part: e.Part, Impact: e.Impact}
	if withEvidence {
		r.Evidence = e.Evidence
		if len(r.Evidence) > 3 {
			r.Evidence = r.Evidence[:3]
		}
	}
	return r
}

func buildReview(st *store.State, rnd *rand.Rand) *reviewReport {
	r := &reviewReport{Entries: len(st.Entries), Speakers: map[string][]entry.SpeakerCheck{}, Exits: st.Exits}
	for _, f := range entry.Facets {
		all := curate.EntriesOf(st, f, nil)
		rf := reviewFacet{Facet: f, Total: len(all), ByGrain: map[string]int{}, ByState: map[string]int{}}
		for _, e := range all {
			rf.ByGrain[string(e.Grain)]++
			rf.ByState[string(e.Status)]++
		}
		// At most 10, abstract entries first (at least 2 when there are any), then by the shared order.
		var top []*entry.Entry
		for _, e := range all {
			if e.Grain == entry.Abstract && len(top) < 2 {
				top = append(top, e)
			}
		}
		for _, e := range all {
			if len(top) >= 10 {
				break
			}
			if !slices.Contains(top, e) && (f != entry.People && f != entry.Persona || e.Grain != entry.Action) {
				top = append(top, e)
			}
		}
		for _, e := range top {
			rf.Top = append(rf.Top, toReview(e, true))
		}
		r.Facets = append(r.Facets, rf)
	}
	// U7: the cards of the ten people dealt with most.
	var contacts []*entry.Contact
	for _, c := range st.Contacts {
		if c.Manual != "removed" {
			contacts = append(contacts, c)
		}
	}
	sort.Slice(contacts, func(i, j int) bool {
		if contacts[i].Score != contacts[j].Score {
			return contacts[i].Score > contacts[j].Score
		}
		return contacts[i].Person < contacts[j].Person
	})
	for _, c := range contacts[:min(10, len(contacts))] {
		card := reviewCard{Person: c.Person, Name: c.Name, Score: c.Score, Ties: c.Ties, Parts: map[string][]reviewEntry{}}
		for _, e := range curate.EntriesOf(st, entry.People, func(e *entry.Entry) bool { return e.Person == c.Person }) {
			card.Parts[e.Part] = append(card.Parts[e.Part], toReview(e, true))
			if e.ReportedOnly {
				card.Reports++
			}
		}
		r.Cards = append(r.Cards, card)
	}
	// U8: 30 attributed lines per connector (all when fewer), as the speaker package sampled them.
	for _, sc := range st.SpeakerChecks {
		if len(r.Speakers[sc.Source]) < 30 {
			r.Speakers[sc.Source] = append(r.Speakers[sc.Source], sc)
		}
	}
	// F1: 30 facts written back (world facts and the fact part of the cards), with their sources.
	var facts []*entry.Entry
	for _, e := range st.Entries {
		if e.Live() && (e.Facet == entry.World || e.Facet == entry.People && e.Part == entry.PartFact) {
			facts = append(facts, e)
		}
	}
	for _, e := range drawEntries(facts, 30, rnd) {
		r.Facts = append(r.Facts, toReview(e, true))
	}
	// 9.4: 30 entries of exits A and B (≥4 per facet when it has them), and 10 abstract ones.
	var pool []*entry.Entry
	for _, f := range []entry.Facet{entry.Interaction, entry.Tools, entry.Judgement, entry.Workflow, entry.World, entry.Persona} {
		cand := curate.EntriesOf(st, f, func(e *entry.Entry) bool { return e.Grain != entry.Action || f == entry.Tools })
		pool = append(pool, drawEntries(cand, 4, rnd)...)
	}
	var rest []*entry.Entry
	for _, e := range st.Entries {
		if e.Live() && e.Facet != entry.People && !slices.Contains(pool, e) && e.Grain != entry.Action {
			rest = append(rest, e)
		}
	}
	pool = append(pool, drawEntries(rest, max(0, 30-len(pool)), rnd)...)
	for _, e := range pool {
		r.Empty = append(r.Empty, toReview(e, false))
	}
	var abs []*entry.Entry
	for _, e := range st.Entries {
		if e.Live() && e.Grain == entry.Abstract {
			abs = append(abs, e)
		}
	}
	for _, e := range drawEntries(abs, 10, rnd) {
		r.Abstract = append(r.Abstract, toReview(e, true))
	}
	for _, k := range sortedIDs(st.Sources) {
		r.Sources = append(r.Sources, st.Sources[k])
	}
	return r
}

// sample draws up to n entries in a stable order (by id first, then shuffled with the seed).
func drawEntries(in []*entry.Entry, n int, rnd *rand.Rand) []*entry.Entry {
	cp := slices.Clone(in)
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	rnd.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	return cp[:min(n, len(cp))]
}

func (r *reviewReport) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "entries: %d\n", r.Entries)
	for _, f := range r.Facets {
		fmt.Fprintf(&b, "%-12s total %4d  grain %v  status %v\n", f.Facet, f.Total, f.ByGrain, f.ByState)
	}
	fmt.Fprintf(&b, "cards: %d, speaker sources: %d, facts sampled: %d, empty-talk sample: %d, abstract sample: %d\n",
		len(r.Cards), len(r.Speakers), len(r.Facts), len(r.Empty), len(r.Abstract))
	return b.String()
}

func (r *reviewReport) markdown() string {
	var b strings.Builder
	b.WriteString("# Review\n\n")
	b.WriteString(r.summary())
	for _, f := range r.Facets {
		fmt.Fprintf(&b, "\n## %s (%d)\n\n", f.Facet, f.Total)
		if len(f.Top) == 0 {
			b.WriteString("not built up yet\n")
		}
		for i, e := range f.Top {
			fmt.Fprintf(&b, "%d. [%s/%s ×%d %v] %s\n   impact: %s / %s / %s\n", i+1, e.Grain, e.Status, e.Count, e.Kinds, e.Text,
				e.Impact.Moment, e.Impact.Action, e.Impact.Object)
			for _, ev := range e.Evidence {
				fmt.Fprintf(&b, "   - %s %s %s#%d %s: %s\n", ev.Source, ev.Day, ev.Ref, ev.N, ev.Speaker, ev.Quote)
			}
		}
	}
	fmt.Fprintf(&b, "\n## Cards (%d)\n", len(r.Cards))
	for _, c := range r.Cards {
		fmt.Fprintf(&b, "\n### %s (%s, score %d, ties %v)\n", c.Name, c.Person, c.Score, c.Ties)
		for _, part := range []string{entry.PartFact, entry.PartUserEval, entry.PartInferred, entry.PartCommitment} {
			for _, e := range c.Parts[part] {
				fmt.Fprintf(&b, "- %s ×%d: %s\n", part, e.Count, e.Text)
			}
		}
	}
	return b.String()
}
