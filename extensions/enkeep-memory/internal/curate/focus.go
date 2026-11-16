package curate

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
)

// The first-run question 3 (4.3): "what are you pushing most right now?" Besides the ⑥ entries the answers make,
// the history replay takes the units about those things first, so what the user cares about now is curated before
// the rest of each day.

// FocusTerms are the words a unit is matched on: each answer as written, its longer words, and the names of the
// knowledge base's projects the answer names (or that name it).
func FocusTerms(focus []string, projects []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		q := norm.Q(s)
		if len([]rune(q)) < 3 || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, q)
	}
	for _, f := range focus {
		add(f)
		for _, w := range strings.FieldsFunc(f, func(r rune) bool { return unicode.IsSpace(r) || r == '，' || r == '、' || r == ',' || r == '；' }) {
			// A word on its own needs some length to say anything: ≥4 letters in Latin script ("the" would match
			// everything), ≥3 characters otherwise ("推进" alone is not a topic).
			if ascii(w) && len([]rune(norm.Q(w))) < 4 {
				continue
			}
			add(w)
		}
		qf := norm.Q(f)
		for _, p := range projects {
			qp := norm.Q(p)
			if len([]rune(qp)) >= 2 && (strings.Contains(qf, qp) || strings.Contains(qp, qf)) {
				add(p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func ascii(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// FocusRelated says whether a unit is about one of the terms: its name or any of its lines mentions one.
func FocusRelated(u *Unit, terms []string) bool {
	if u == nil || len(terms) == 0 {
		return false
	}
	texts := []string{u.Meta.Name}
	for _, l := range u.Lines {
		texts = append(texts, l.Body)
	}
	for _, t := range texts {
		q := norm.Q(t)
		for _, term := range terms {
			if strings.Contains(q, term) {
				return true
			}
		}
	}
	return false
}

// focusFirst puts the admitted units about the user's focus first while the first-run replay runs (keeping the
// triage order within each part). Outside the replay, and without answers, it leaves the order as it is.
func (c *Curator) focusFirst(ctx context.Context, ds []triage.Decision, byRef map[string]*Unit) int {
	b := c.St.S.Backfill
	ob := c.St.S.Onboarding
	if b == nil || b.Finished != "" || ob == nil || len(ob.Focus) == 0 {
		return 0
	}
	var projects []string
	if c.Src != nil {
		if es, err := c.Src.List(ctx, "projects"); err == nil {
			for _, e := range es {
				projects = append(projects, e.Name)
			}
		}
	}
	terms := FocusTerms(ob.Focus, projects)
	hit := map[string]bool{}
	n := 0
	for _, d := range ds {
		if FocusRelated(byRef[d.Chat], terms) {
			hit[d.Chat] = true
			n++
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return hit[ds[i].Chat] && !hit[ds[j].Chat] })
	return n
}
