package memory

import (
	"sort"
)

// Conflict is two entries that say opposite things about the same matter, in the same setting and with the same
// subject (6.18 §6). Entries that differ in setting or subject both stand and are never paired.
type Conflict struct {
	A string `json:"a"`
	B string `json:"b"`
}

// Book is every entry and the contradictions between them.
type Book struct {
	Entries   map[string]*Entry `json:"entries"`
	Conflicts []Conflict        `json:"conflicts,omitempty"`
}

// NewBook is an empty book.
func NewBook() *Book { return &Book{Entries: map[string]*Entry{}} }

// Init fills in what an old state file lacks.
func (b *Book) Init() {
	if b.Entries == nil {
		b.Entries = map[string]*Entry{}
	}
}

// Put adds an entry or, when one with the same id is there, adds the new evidence and events to it.
func (b *Book) Put(e *Entry) *Entry {
	cur := b.Entries[e.ID]
	if cur == nil {
		b.Entries[e.ID] = e
		return e
	}
	for _, x := range e.Evidence {
		if !hasEvidence(cur.Evidence, x) {
			cur.Evidence = append(cur.Evidence, x)
		}
	}
	for _, x := range e.Events {
		cur.AddEvent(x)
	}
	return cur
}

func hasEvidence(ev []Evidence, x Evidence) bool {
	for _, y := range ev {
		if y.Anchor == x.Anchor && y.Day == x.Day && y.Quote == x.Quote {
			return true
		}
	}
	return false
}

// AddEvent records an event once.
func (e *Entry) AddEvent(x EntryEvent) {
	for _, y := range e.Events {
		if y.Day == x.Day && y.Kind == x.Kind && y.Anchor == x.Anchor && y.With == x.With {
			return
		}
	}
	e.Events = append(e.Events, x)
}

func (e *Entry) dropEvent(kind EventKind, with string) {
	out := e.Events[:0]
	for _, y := range e.Events {
		if !(y.Kind == kind && y.With == with) {
			out = append(out, y)
		}
	}
	e.Events = out
}

// Contradict records a contradiction, once.
func (b *Book) Contradict(a, c string) {
	if a == c || b.Entries[a] == nil || b.Entries[c] == nil {
		return
	}
	for _, x := range b.Conflicts {
		if (x.A == a && x.B == c) || (x.A == c && x.B == a) {
			return
		}
	}
	b.Conflicts = append(b.Conflicts, Conflict{A: a, B: c})
}

// Tick settles every entry as of day: the contradictions first (6.18 §6, compared once a day), then each entry's
// status. It returns the entries whose state or layer changed since the last tick.
func (b *Book) Tick(day string, p Params) []Change {
	before := map[string]Status{}
	for id, e := range b.Entries {
		before[id] = e.Status
	}
	for _, c := range b.Conflicts {
		b.resolve(c, day, p)
	}
	var out []Change
	for _, id := range b.ids() {
		e := b.Entries[id]
		e.Status = Evaluate(e, day, p)
		was := before[id]
		if was.State != e.Status.State || was.Layer != e.Status.Layer || was.Lost != e.Status.Lost {
			out = append(out, Change{ID: id, From: was, To: e.Status})
		}
	}
	return out
}

// Change is an entry whose state or layer moved.
type Change struct {
	ID   string `json:"id"`
	From Status `json:"from"`
	To   Status `json:"to"`
}

func (b *Book) ids() []string {
	ids := make([]string, 0, len(b.Entries))
	for id := range b.Entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// resolve compares the two sides of a contradiction: the newer one wins once its score is above the older one's
// (for facts, which do not decay, a tie goes to the newer one too). Until then the newer one is held at candidate.
func (b *Book) resolve(c Conflict, day string, p Params) {
	x, y := b.Entries[c.A], b.Entries[c.B]
	if x == nil || y == nil {
		return
	}
	sx, sy := Evaluate(x, day, p), Evaluate(y, day, p)
	if sx.FirstSeen == "" || sy.FirstSeen == "" {
		return // one side has nothing yet
	}
	older, newer, so, sn := x, y, sx, sy
	if sy.FirstSeen < sx.FirstSeen || (sy.FirstSeen == sx.FirstSeen && y.ID < x.ID) {
		older, newer, so, sn = y, x, sy, sx
	}
	if so.Lost || sn.Lost || so.State == Obsolete || sn.State == Obsolete {
		return // decided already, or one side is gone
	}
	fact := so.Layer == Fact && !so.Descriptive && sn.Layer == Fact && !sn.Descriptive
	if sn.Score > so.Score || (fact && sn.Score == so.Score) {
		older.AddEvent(EntryEvent{Day: day, Kind: EvLost, With: newer.ID})
		if held(newer, older.ID) {
			newer.AddEvent(EntryEvent{Day: day, Kind: EvReleased, With: older.ID})
		}
		return
	}
	if !held(newer, older.ID) {
		newer.AddEvent(EntryEvent{Day: day, Kind: EvHeld, With: older.ID})
	}
}

// held: the last hold or release against `with` is a hold.
func held(e *Entry, with string) bool {
	h := false
	for _, x := range e.Events {
		if x.With != with {
			continue
		}
		switch x.Kind {
		case EvHeld:
			h = true
		case EvReleased:
			h = false
		}
	}
	return h
}

// Confirm is the user saying yes to an entry ("对，以后就这样", or the confirm button).
func (b *Book) Confirm(id, day, anchor string) bool {
	e := b.Entries[id]
	if e == nil {
		return false
	}
	e.AddEvent(EntryEvent{Day: day, Kind: EvConfirm, Anchor: anchor})
	return true
}

// Reject is the user saying no ("不对", "别这样记"): one vote voids it.
func (b *Book) Reject(id, day, anchor string) bool {
	e := b.Entries[id]
	if e == nil {
		return false
	}
	e.AddEvent(EntryEvent{Day: day, Kind: EvReject, Anchor: anchor})
	return true
}

// FollowUp records that a suggestion was carried out. Only a stated follow-through or one visible in a work
// product counts; words of agreement are recorded as such and change nothing. It never goes to the weekly report
// for the user to confirm: the user does not want to referee.
func (b *Book) FollowUp(id, day string, kind FollowKind, anchor, quote string) bool {
	e := b.Entries[id]
	if e == nil {
		return false
	}
	e.AddEvent(EntryEvent{Day: day, Kind: EvFollowUp, Follow: kind, Anchor: anchor, Quote: quote})
	return kind == FollowStated || kind == FollowArtifact
}

// Supersede marks an entry replaced by a new version.
func (b *Book) Supersede(id, day, by string) {
	if e := b.Entries[id]; e != nil {
		e.AddEvent(EntryEvent{Day: day, Kind: EvSuperseded, With: by})
	}
}

// Awaiting are the entries with a suggestion waiting for a follow-through, for the daily extraction to look for.
func (b *Book) Awaiting() []*Entry {
	var out []*Entry
	for _, id := range b.ids() {
		if e := b.Entries[id]; e.Status.Awaiting {
			out = append(out, e)
		}
	}
	return out
}
