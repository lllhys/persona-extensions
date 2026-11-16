package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
)

// peopleTop is how many cards the replay reads out: the design's U7 shows the user at most ten.
const peopleTop = 10

// peopleReading is one card's readout, numbers only: what it rests on, what it says per part, how much of it is
// second-hand. Cards are referred to by their rank, never by name.
type peopleReading struct {
	Rank     int            `json:"rank"`
	Persons  int            `json:"persons"`
	Score    int            `json:"score"`
	Ties     map[string]int `json:"ties"`
	Lines    map[string]int `json:"lines"`    // folded lines per part
	Evidence map[string]int `json:"evidence"` // occurrences per part
	Few      map[string]int `json:"few"`      // lines with little evidence per part
	Reported struct {
		Lines        int `json:"evidence_lines"`
		Of           int `json:"of"`
		ReportedOnly int `json:"reported_only_lines"`
	} `json:"reported"`
	Actions    int      `json:"actions_before"` // action entries about them
	Folded     int      `json:"lines_after"`    // lines they were folded into (all parts but the promises)
	Pending    int      `json:"pending"`
	Advice     bool     `json:"advice"`
	Rhythm     bool     `json:"rhythm"`
	Dims       []string `json:"dims"` // the dimensions of the inferred and user-evaluation lines
	Proposals  []string `json:"proposals"`
	PageCounts map[string]int
}

// peopleReplay runs the card pass on a replay state, records the person-page writes the fact proposals make (the dry
// source keeps them), and writes the readout: people-readout.json (numbers) and people-cards.json (the cards and the
// proposed pages: the user's data, kept on this machine with the rest of the replay).
func peopleReplay(ctx context.Context, c *curate.Curator, dry *kbsrc.DryRun, from, to, out string) (*store.Run, error) {
	r, err := c.RunPeople(ctx, from, to, false)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "people: %d contacts, %d people with cards made, %d model calls, counts %v\n",
		len(c.St.S.Contacts), r.Counts["cards_people"], r.Counts["model_calls"], r.Counts)
	c.St.Lock()
	list := c.PersonCards(to)
	if len(list) > peopleTop {
		list = list[:peopleTop]
	}
	var top []string
	for _, s := range list {
		top = append(top, s.Persons...)
	}
	var facts []string
	for _, p := range c.St.S.Proposals {
		if p.Kind == "person_fact" && p.Status == "pending" && slices.ContainsFunc(top, func(x string) bool { return "people/"+people.PersonID(x) == p.Target }) {
			facts = append(facts, p.ID)
		}
	}
	c.St.Unlock()
	writesBefore := len(dry.Writes)
	for _, id := range facts {
		if _, err := c.Decide(ctx, id, "accept", ""); err != nil {
			fmt.Fprintf(os.Stderr, "people: accept %s: %v\n", id, err)
		}
	}
	c.St.Lock()
	defer c.St.Unlock()
	type cardDump struct {
		Rank      int                   `json:"rank"`
		Card      *people.Card          `json:"card"`
		Proposals []*store.Proposal     `json:"proposals"`
		Writes    []wire.KBUpdateParams `json:"writes"`
	}
	var readings []peopleReading
	var dumps []cardDump
	for i, s := range list {
		card := c.PersonCard(s.Person, to)
		rd := peopleReading{Rank: i + 1, Persons: len(card.Persons), Score: s.Score, Lines: map[string]int{}, Evidence: map[string]int{},
			Few: map[string]int{}, Actions: card.Actions, Pending: card.Pending, Advice: card.Advice != "",
			Rhythm: card.Rhythm != nil && card.Rhythm.Days > 0}
		if card.Contact != nil {
			rd.Ties = card.Contact.Ties
		}
		for _, sec := range people.Sections {
			for _, l := range card.Parts[sec] {
				if l.Pending {
					continue
				}
				rd.Lines[sec]++
				rd.Evidence[sec] += l.Evidence
				if l.Few {
					rd.Few[sec]++
				}
				rd.Reported.Lines += l.Reported
				rd.Reported.Of += l.Lines
				if l.ReportedOnly {
					rd.Reported.ReportedOnly++
				}
				if sec != people.SecCommitments {
					rd.Folded++
				}
				if (sec == people.SecInferred || sec == people.SecUserEval || l.Inferred) && !slices.Contains(rd.Dims, l.Dim) {
					rd.Dims = append(rd.Dims, l.Dim)
				}
			}
		}
		d := cardDump{Rank: i + 1, Card: card}
		for _, p := range c.St.S.Proposals {
			if (p.Kind == "person_fact" || p.Kind == "person_card") && slices.ContainsFunc(card.Persons, func(x string) bool { return "people/"+people.PersonID(x) == p.Target }) {
				d.Proposals = append(d.Proposals, p)
				rd.Proposals = append(rd.Proposals, p.Kind+":"+p.Status)
			}
		}
		for _, w := range dry.Writes[writesBefore:] {
			if slices.ContainsFunc(card.Persons, func(x string) bool { return people.PersonID(x) == w.ID }) {
				d.Writes = append(d.Writes, w)
			}
		}
		rd.PageCounts = people.RenderPage(card, people.Options{Inferred: true, Today: to, Lang: c.Cfg.Language}).Counts
		readings = append(readings, rd)
		dumps = append(dumps, d)
	}
	write := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(out, name), b, 0o600)
	}
	if err := write("people-readout.json", map[string]any{"contacts": len(c.St.S.Contacts), "listed": len(c.PersonCards(to)),
		"run": r.Counts, "cards": readings}); err != nil {
		return nil, err
	}
	if err := write("people-cards.json", dumps); err != nil {
		return nil, err
	}
	return r, nil
}
