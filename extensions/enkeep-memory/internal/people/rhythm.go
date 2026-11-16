package people

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
)

// Rhythm is how the user and one person exchange in direct chats, measured from the timestamps of the resolved lines
// (no model): who opens a day's exchange, and how long each takes to answer the other. It feeds the card's
// "how they work with the user" part as counted facts, beside whatever the extraction inferred.
type Rhythm struct {
	Days      int    `json:"days"`                 // days with a direct chat both spoke in
	ByThem    int    `json:"by_them"`              // of those, days they spoke first
	ByUser    int    `json:"by_user"`              // days the user spoke first
	TheirGaps []int  `json:"their_gaps,omitempty"` // minutes they took to answer the user, newest last
	UserGaps  []int  `json:"user_gaps,omitempty"`  // minutes the user took to answer them
	First     string `json:"first,omitempty"`
	Last      string `json:"last,omitempty"`
}

// maxGaps bounds the answer times kept per side: the newest are what the card speaks of.
const maxGaps = 80

// ObserveRhythm counts one day's direct chats into the rhythm. Call it once per day (the caller keeps a cursor).
// Only direct chats count: in a group or a mail thread, the next line is not necessarily an answer to the last.
func ObserveRhythm(m map[string]*Rhythm, day string, us []Unit) {
	for _, u := range us {
		if u.Entity != "chats" || u.Kind != "dm" {
			continue
		}
		type said struct {
			self bool
			min  int
		}
		var seq []said
		other := ""
	lines:
		for _, l := range u.Lines {
			who := u.Who[l.N]
			mins, ok := minutes(l.Time)
			if !ok {
				continue
			}
			switch {
			case who == entry.WhoSelf:
				seq = append(seq, said{true, mins})
			case speaker.PersonID(who) != "":
				if other != "" && other != who {
					seq = nil // not a two-party chat after all
					break lines
				}
				other = who
				seq = append(seq, said{false, mins})
			}
		}
		if other == "" || len(seq) == 0 {
			continue
		}
		both := false
		for _, s := range seq {
			if s.self != seq[0].self {
				both = true
				break
			}
		}
		if !both {
			continue
		}
		r := m[other]
		if r == nil {
			r = &Rhythm{First: day}
			m[other] = r
		}
		r.Days++
		if seq[0].self {
			r.ByUser++
		} else {
			r.ByThem++
		}
		for i := 1; i < len(seq); i++ {
			if seq[i].self == seq[i-1].self || seq[i].min < seq[i-1].min {
				continue
			}
			gap := seq[i].min - seq[i-1].min
			if seq[i].self {
				r.UserGaps = keepLast(append(r.UserGaps, gap))
			} else {
				r.TheirGaps = keepLast(append(r.TheirGaps, gap))
			}
		}
		if day > r.Last {
			r.Last = day
		}
		if r.First == "" || day < r.First {
			r.First = day
		}
	}
}

func keepLast(xs []int) []int {
	if len(xs) > maxGaps {
		return append([]int(nil), xs[len(xs)-maxGaps:]...)
	}
	return xs
}

// minutes parses "HH:MM".
func minutes(t string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(t), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return hh*60 + mm, true
}

// Median of answer times, and how many there are.
func Median(xs []int) (int, int) {
	if len(xs) == 0 {
		return 0, 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[len(s)/2], len(s)
}

// MergeRhythm adds up the rhythm of several person entries (a merged card).
func MergeRhythm(rs ...*Rhythm) *Rhythm {
	var out *Rhythm
	for _, r := range rs {
		if r == nil {
			continue
		}
		if out == nil {
			out = &Rhythm{}
		}
		out.Days += r.Days
		out.ByThem += r.ByThem
		out.ByUser += r.ByUser
		out.TheirGaps = keepLast(append(out.TheirGaps, r.TheirGaps...))
		out.UserGaps = keepLast(append(out.UserGaps, r.UserGaps...))
		if out.First == "" || (r.First != "" && r.First < out.First) {
			out.First = r.First
		}
		if r.Last > out.Last {
			out.Last = r.Last
		}
	}
	return out
}
