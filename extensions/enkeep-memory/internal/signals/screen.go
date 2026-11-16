package signals

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// hit is one position the cheap screen flagged.
type hit struct {
	Signal string
	Ref    string   // the session's ref; "todos/<id>" or "asks/<day>" when there is no session to read
	sess   *session // nil: nothing to dig
	Seq    int
	What   string // what matched: a word, a count (never the content)
}

// signalOrder: when hits of one session are too many, these come first.
var signalOrder = []string{entry.SigCorrection, entry.SigExplicit, entry.SigAskInfra, entry.SigRepeat, entry.SigRetry,
	entry.SigStuck, entry.SigBlocked, entry.SigPraise, entry.SigRework}

const (
	hitsPerSession = 8
	hitGap         = 2 // hits closer than this many seqs are one place
)

// screen finds the day's friction without a model (3.1): the ledger's corrections; the user's approval and standing
// instructions; the AI's questions about machines, ports, accounts and tools; the agent saying it is stuck; notices of
// blocked actions; folds with errors (a hub with H3), or with errors touching a known ② object; permission cards;
// reopened to-dos and interrupted sessions. K17 "wide" loosens the fold and question conditions; it never reads more
// than the folded pages.
func screen(g *gathered, st *store.State, words map[string][]string, corrections []entry.Correction) []hit {
	wide := entry.Switch(st.Switches, entry.K17DigScope) == "wide"
	objects := knownObjects(st)
	var out []hit
	for _, c := range corrections {
		if s := g.session(c.Ref); s != nil {
			out = append(out, hit{Signal: entry.SigCorrection, Ref: s.Ref, sess: s, Seq: c.Seq, What: "correction " + c.ID})
		}
	}
	for _, s := range g.Sessions {
		for _, it := range s.Items {
			if !s.inDay(it) {
				continue
			}
			add := func(sig, what string) {
				out = append(out, hit{Signal: sig, Ref: s.Ref, sess: s, Seq: it.Seq, What: what})
			}
			switch it.Kind {
			case kUser:
				if w := match(entry.SigExplicit, it.Text, words); w != "" {
					add(entry.SigExplicit, w)
				} else if w := match(entry.SigPraise, it.Text, words); w != "" {
					add(entry.SigPraise, w)
				}
			case kAgent:
				if w := match(entry.SigAskInfra, it.Text, words); w != "" && (wide || containsAny(it.Text, questionMarks)) {
					add(entry.SigAskInfra, w)
				} else if w := match(entry.SigStuck, it.Text, words); w != "" {
					add(entry.SigStuck, w)
				}
			case kNotice:
				if w := match(entry.SigBlocked, it.Text, words); w != "" {
					add(entry.SigBlocked, w)
				}
			case kFold:
				f := it.Fold
				if f == nil || !f.Known || f.Errors == 0 {
					continue // unknown is not "no errors"; it is counted (foldsUnknown), not guessed
				}
				if obj := touches(f, objects); obj != "" {
					add(entry.SigRepeat, fmt.Sprintf("%d errors, %s", f.Errors, obj))
				} else if f.Calls >= 2 || wide {
					add(entry.SigRetry, fmt.Sprintf("%d errors in %d calls", f.Errors, f.Calls))
				}
			}
		}
		if (s.Ended == "interrupted" || s.Ended == "timeout") && len(s.Items) > 0 && s.Updated.Before(s.end) {
			last := s.Items[len(s.Items)-1]
			out = append(out, hit{Signal: entry.SigRework, Ref: s.Ref, sess: s, Seq: last.Seq, What: "session " + s.Ended})
		}
	}
	for _, c := range g.Cards {
		s := g.cardSession(c)
		h := hit{Ref: "asks/" + g.Day, sess: s, Seq: g.seqAt(s, c)}
		if s != nil {
			h.Ref = s.Ref
		}
		switch {
		case strings.Contains(strings.ToLower(c.Kind), "permission"):
			h.Signal, h.What = entry.SigBlocked, "permission card"
		case match(entry.SigAskInfra, c.Question, words) != "":
			h.Signal, h.What = entry.SigAskInfra, "card: "+match(entry.SigAskInfra, c.Question, words)
		default:
			continue
		}
		out = append(out, h)
	}
	for _, t := range g.Todos {
		if t.Reopened > 0 {
			out = append(out, hit{Signal: entry.SigRework, Ref: "todos/" + t.ID, What: fmt.Sprintf("reopened %d", t.Reopened)})
		}
	}
	return thin(out)
}

// thin keeps one hit per place and at most hitsPerSession per session, by signalOrder.
func thin(hits []hit) []hit {
	rank := map[string]int{}
	for i, s := range signalOrder {
		rank[s] = i
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Ref != hits[j].Ref {
			return hits[i].Ref < hits[j].Ref
		}
		return rank[hits[i].Signal] < rank[hits[j].Signal]
	})
	var out []hit
	per := map[string]int{}
	for _, h := range hits {
		if h.sess != nil {
			dup := false
			for _, k := range out {
				if k.Ref == h.Ref && abs(k.Seq-h.Seq) <= hitGap {
					dup = true
					break
				}
			}
			if dup || per[h.Ref] >= hitsPerSession {
				continue
			}
			per[h.Ref]++
		}
		out = append(out, h)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// knownObjects are the machines and tools ② entries are about (lower case, ≥3 characters).
func knownObjects(st *store.State) []string {
	var out []string
	for _, e := range st.Entries {
		if e.Facet != entry.Tools || !e.Live() {
			continue
		}
		names := []string{e.Object}
		for _, sc := range e.Scopes {
			if sc.Kind == entry.ScopeObject {
				names = append(names, sc.Name)
			}
		}
		for _, n := range names {
			if n = strings.ToLower(strings.TrimSpace(n)); len(n) >= 3 {
				out = append(out, n)
			}
		}
	}
	return uniq(out)
}

// touches says which known object a fold's tool names or files name.
func touches(f *hubapi.Fold, objects []string) string {
	for _, o := range objects {
		for _, x := range append(append([]string(nil), f.Tools...), f.Files...) {
			if strings.Contains(strings.ToLower(x), o) {
				return o
			}
		}
	}
	return ""
}

// cardSession is the session a card was asked in, when it is among the day's.
func (g *gathered) cardSession(c hubapi.Interaction) *session {
	if c.Session == "" {
		return nil
	}
	for _, s := range g.Sessions {
		if s.ID == c.Session && (c.Machine == "" || s.Machine == c.Machine) {
			return s
		}
	}
	return nil
}

// seqAt is the seq of the last item of s at or before the card's time.
func (g *gathered) seqAt(s *session, c hubapi.Interaction) int {
	if s == nil {
		return 0
	}
	seq := 0
	for _, it := range s.Items {
		if it.At.After(c.At) {
			break
		}
		seq = it.Seq
	}
	return seq
}
