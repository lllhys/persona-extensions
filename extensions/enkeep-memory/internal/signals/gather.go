package signals

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
)

// Item kinds of the hub's session items (hubapi.Item.Kind).
const (
	kUser       = "user"
	kAgent      = "agent"
	kToolUse    = "tool_use"
	kToolResult = "tool_result"
	kNotice     = "notice"
	kFold       = "fold"
)

// session is one session with activity on the day, and its folded page (the whole page: the day's items and the
// words before them, for context).
type session struct {
	hubapi.Session
	Ref    string // "agents/<machine>/<session>"
	Source string // S1 for the assistant's own session, S3 for other agents
	Items  []hubapi.Item
	start  time.Time
	end    time.Time
}

func (s *session) inDay(it hubapi.Item) bool { return !it.At.Before(s.start) && it.At.Before(s.end) }

// contextSeqs: how far before the last day's end a folded page is read again, for the words around early hits.
const contextSeqs = 60

// gathered is a day's material: sessions with their folded pages, ask_user cards, to-dos.
type gathered struct {
	Day        string
	Start, End time.Time
	Sessions   []*session
	Cards      []hubapi.Interaction
	Todos      []hubapi.Todo
	Memories   []hubapi.MemoryFile
	FoldBytes  int
	Missing    []string
}

// gather reads the day from the hub: the sessions active that day, each as a folded page (fold_work=1: words, and
// every stretch of work as one line with counts), the cards, and the to-dos. A source the hub cannot give is noted
// in Missing and left out.
func gather(ctx context.Context, o Options, day string, sd *side) (*gathered, error) {
	if sd.Read == nil {
		sd.Read = map[string]Cursor{}
	}
	start, err := time.ParseInLocation("2006-01-02", day, o.loc())
	if err != nil {
		return nil, err
	}
	g := &gathered{Day: day, Start: start, End: start.AddDate(0, 0, 1)}
	if o.Hub == nil {
		g.Missing = append(g.Missing, "hub")
		return g, nil
	}
	missing := func(what string, err error) bool {
		if err == nil {
			return false
		}
		if ctx.Err() != nil {
			return true
		}
		g.Missing = append(g.Missing, what+": "+err.Error())
		if !errors.Is(err, hubapi.ErrNoScope) && !errors.Is(err, hubapi.ErrWaitingHub) {
			o.logf("signals: %s: %v", what, err)
		}
		return true
	}
	sessions, err := o.Hub.Sessions(ctx, g.Start)
	if !missing("sessions", err) {
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Started.Before(sessions[j].Started) })
		for _, s := range sessions {
			if s.Health || s.Updated.Before(g.Start) || !s.Started.Before(g.End) {
				continue
			}
			ref := "agents/" + s.Machine + "/" + s.ID
			q := hubapi.ItemsQuery{Fold: true}
			if c, ok := sd.Read[ref]; ok && c.Day < day {
				q.After = max(0, c.Seq-contextSeqs) // read on from the last day, with some words before for context
			}
			items, err := o.Hub.Items(ctx, s.Machine, s.ID, q)
			if missing("items "+s.ID, err) {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			raw, _ := json.Marshal(items)
			g.FoldBytes += len(raw)
			src := entry.SrcAgent
			if s.Assistant {
				src = entry.SrcAssistant
			}
			ss := &session{Session: s, Ref: ref, Source: src, Items: items, start: g.Start, end: g.End}
			sort.SliceStable(ss.Items, func(i, j int) bool { return ss.Items[i].Seq < ss.Items[j].Seq })
			g.Sessions = append(g.Sessions, ss)
			last := sd.Read[ref].Seq
			for _, it := range items {
				if it.At.Before(g.End) {
					last = max(last, it.Seq)
					if it.Fold != nil {
						last = max(last, it.Fold.To)
					}
				}
			}
			if c := sd.Read[ref]; day >= c.Day {
				sd.Read[ref] = Cursor{Seq: last, Day: day}
			}
		}
	}
	cards, err := o.Hub.Interactions(ctx, hubapi.InteractionsQuery{Since: g.Start, Before: g.End})
	if err != nil && !(errors.Is(err, hubapi.ErrWaitingHub) && len(cards) > 0) {
		missing("cards", err)
	}
	for _, c := range cards {
		if !c.Private && !c.At.Before(g.Start) && c.At.Before(g.End) {
			g.Cards = append(g.Cards, c)
		}
	}
	todos, err := o.Hub.Todos(ctx, g.Start)
	if !missing("todos", err) {
		for _, t := range todos {
			if !t.Updated.Before(g.Start) && t.Updated.Before(g.End) {
				g.Todos = append(g.Todos, t)
			}
		}
	}
	mems, err := o.Hub.Memories(ctx, false)
	if !missing("memory", err) {
		g.Memories = mems
	}
	return g, ctx.Err()
}

// msg is one message of the user's: a session's user text, or an answer on a card.
type msg struct {
	Source  string
	Ref     string
	Session string // the session's ref, for "in different sessions"
	Seq     int
	At      time.Time
	Text    string
	Prior   string // the AI's line before it, or the card's question
	Card    bool
}

// userMsgs lists the day's messages by the user. Texts the hub marks as not the user's (injected, relayed,
// Persona's own notices) come as notices and are not here.
func (g *gathered) userMsgs() []msg {
	var out []msg
	for _, s := range g.Sessions {
		prior := ""
		for _, it := range s.Items {
			switch it.Kind {
			case kAgent:
				prior = it.Text
			case kUser:
				if s.inDay(it) && strings.TrimSpace(it.Text) != "" {
					out = append(out, msg{Source: s.Source, Ref: s.Ref, Session: s.Ref, Seq: it.Seq, At: it.At, Text: it.Text, Prior: cut(prior, 300)})
				}
			}
		}
	}
	for _, c := range g.Cards {
		if c.AnsweredBy != "user" || len(c.Answers) == 0 {
			continue
		}
		text := strings.TrimSpace(strings.Join(c.Answers, "\n"))
		if text == "" {
			continue
		}
		sess := "asks/" + c.ID
		if c.Session != "" {
			sess = "agents/" + c.Machine + "/" + c.Session
		}
		out = append(out, msg{Source: entry.SrcAsk, Ref: "asks/" + g.Day, Session: sess, At: c.At, Text: text, Prior: cut(c.Question, 300), Card: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// asks counts the questions the AI put to the user on the day: cards, and AI lines with a question answered by the
// user's next line (the denominator of R2).
func (g *gathered) asks() int {
	n := len(g.Cards)
	for _, s := range g.Sessions {
		for i, it := range s.Items {
			if it.Kind != kAgent || !s.inDay(it) || !containsAny(it.Text, []string{"?", "？"}) {
				continue
			}
			for _, nx := range s.Items[i+1:] {
				if nx.Kind == kUser {
					n++
					break
				}
				if nx.Kind == kAgent {
					break
				}
			}
		}
	}
	return n
}

// foldsUnknown counts the day's folds without an error count (a hub without H3).
func (g *gathered) foldsUnknown() int {
	n := 0
	for _, s := range g.Sessions {
		for _, it := range s.Items {
			if it.Kind == kFold && it.Fold != nil && !it.Fold.Known && s.inDay(it) {
				n++
			}
		}
	}
	return n
}

func (g *gathered) session(ref string) *session {
	for _, s := range g.Sessions {
		if s.Ref == ref {
			return s
		}
	}
	return nil
}
