package curate

import (
	"math"
	"slices"
	"sort"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Thresholds that adapt to the user's amount of data (design 4.4). Every one counts chances (times the kind of
// situation came up) and active days (days with any source data for this user), never calendar time, so a user who
// works with Persona one day a week is not held back by the calendar and a heavy user does not take chance for habit.
// The quality gates (the abstract gate, 5.3) and the gates that widen what is decided for the user do not adapt.
const (
	recentActiveDays  = 20   // the window counter-examples are looked for in ("the last 4 weeks" before)
	shiftingShare     = 0.25 // of the chances in that window that went against an entry: it is shifting
	dormantActiveDays = 40   // quiet this many active days: dormant ("8 weeks" before)
	stableMinDays     = 2    // a stable entry rests on at least this many active days
)

// StableK is how many consistent occurrences an entry needs to be stable, given n chances: 2 below 5 chances, 3 for
// 5 to 20, and 15% of the chances (at least 3) above 20.
func StableK(n int) int {
	switch {
	case n < 5:
		return 2
	case n <= 20:
		return 3
	}
	return max(3, int(math.Ceil(float64(n)*0.15)))
}

// occurrence is one (unit, day) an entry was seen in: what counts once (5.0 "one per entry, session and day").
type occurrence struct{ ref, day string }

// Tally is what an entry's counts rest on: the occurrences of its own evidence (or its members'), and those of the
// actions that went against it.
type Tally struct {
	For     []occurrence
	Against []occurrence
	Days    []string // active days with any occurrence, for or against
	Kinds   []string
	Refs    []string
}

// recentDays are the last n active days up to today (all days with data when the state has no active days yet).
func recentDays(s *store.State, today string, n int) []string {
	days := s.ActiveDays
	i := sort.SearchStrings(days, today)
	if i < len(days) && days[i] == today {
		i++
	}
	return days[max(0, i-n):i]
}

// quietDays counts the active days after last, up to today.
func quietDays(s *store.State, last, today string) int {
	if last == "" {
		return 0
	}
	n := 0
	for _, d := range s.ActiveDays {
		if d > last && d <= today {
			n++
		}
	}
	return n
}

// Decision of the thresholds for one entry, with the reason the page and the event log show.
type Decision struct {
	Status entry.Status
	Why    string
}

// Settle decides an entry's status as of today from its tally (4.4, 5.0):
//   - stable: n chances, at least StableK(n) consistent, no counter-example in the last 20 active days, on ≥2
//     active days; self-reported, first-run and hand-written entries start stable;
//   - shifting: counter-examples are ≥25% of the chances in the last 20 active days (shown, not exported);
//   - dormant: quiet for 40 active days; an entry the user confirmed or pinned is not put to sleep but asked about
//     once (the "ask" result, for the weekly report);
//   - candidate otherwise.
//
// Retired entries and user-pinned ones keep what they have. ask says the weekly report should ask the user about it.
func Settle(s *store.State, e *entry.Entry, t Tally, today string) (d Decision, ask bool) {
	if e.Status == entry.Retired || !e.Live() && e.Status != "" {
		return Decision{e.Status, "not live"}, false
	}
	recent := recentDays(s, today, recentActiveDays)
	inRecent := func(o occurrence) bool { return len(recent) == 0 || o.day >= recent[0] && o.day <= today }
	var forRecent, againstRecent int
	for _, o := range t.For {
		if inRecent(o) {
			forRecent++
		}
	}
	for _, o := range t.Against {
		if inRecent(o) {
			againstRecent++
		}
	}
	if againstRecent > 0 && float64(againstRecent) >= shiftingShare*float64(forRecent+againstRecent) && !e.Pinned {
		return Decision{entry.Shifting, "recent counter-examples"}, false
	}
	confirmed := e.Pinned || userConfirmed(e)
	if quietDays(s, e.Last, today) >= dormantActiveDays {
		if confirmed || e.Origin != entry.Behavior {
			return Decision{e.Status, "quiet, confirmed: ask once"}, !slices.Contains(e.Flags, flagAskedDormant)
		}
		return Decision{entry.Dormant, "quiet for 40 active days"}, false
	}
	if e.Origin != entry.Behavior && e.Origin != "" {
		return Decision{entry.Stable, "the user's own words or hand-written memory"}, false
	}
	if e.Pinned {
		return Decision{entry.Stable, "pinned"}, false
	}
	if e.Grain == entry.Action && oneShot(e) {
		return Decision{entry.Stable, "proved once by a tool result or a quoted fact"}, false
	}
	if e.Grain == entry.Action && entry.SelfFact(e) {
		return Decision{entry.Stable, "a fact the user stated about themself"}, false
	}
	if e.Grain == entry.Action && entry.PersonFact(e) {
		return Decision{entry.Stable, "a fact or a promise on the person's or the user's own line"}, false
	}
	if e.Grain == entry.Action && slices.Contains(e.Flags, "durable") && slices.ContainsFunc(e.Evidence, func(x entry.Evidence) bool { return x.Who == entry.WhoSelf }) {
		return Decision{entry.Stable, "the user said it holds from now on"}, false
	}
	n := len(t.For) + len(t.Against)
	if len(t.For) >= StableK(n) && againstRecent == 0 && len(t.Days) >= stableMinDays {
		return Decision{entry.Stable, "consistent often enough"}, false
	}
	return Decision{entry.Candidate, "not yet enough"}, false
}

// oneShot: an action entry that one sighting proves: a pitfall and its fix shown by the tool results (② from a dug
// segment, 5.5), or a quoted fact about the world (⑤, kept with its single-source mark, 4.4).
func oneShot(e *entry.Entry) bool {
	switch e.Facet {
	case entry.Tools:
		return slices.Contains(e.Kinds, entry.SrcTools) && e.Pitfall != "" && e.Fix != ""
	case entry.World:
		return slices.ContainsFunc(e.Evidence, func(x entry.Evidence) bool { return x.Quote != "" })
	}
	return false
}

func userConfirmed(e *entry.Entry) bool {
	for i := len(e.Verdicts) - 1; i >= 0; i-- {
		if e.Verdicts[i].By == "user" {
			return e.Verdicts[i].Mark == "right"
		}
	}
	return false
}

// flagAskedDormant: the weekly report has asked the user once whether a quiet, confirmed entry still holds.
const flagAskedDormant = "asked_dormant"
