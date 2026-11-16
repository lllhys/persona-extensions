package signals

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Readings of facets ① and ② (design 9.2), over the two weeks ending on a day:
//
//	R1        corrections per 100 user messages (den: user messages)
//	R1.recur  recurrences: corrections after their entry took effect (den: corrections filed against an entry);
//	          the note lists them by entry
//	R2        asks answered "I said that already" (den: questions the AI put to the user)
//	R3        the AI asking the user about a machine, entry point, account or tool (den: sessions and cards)
//	R3.repeat the same tool pitfall again in another session: repeat signals the model dug (den: sessions)
//
// A reading with fewer than MinChances chances is not measurable: Measure false, Note "量不出".
const (
	MinChances = 20
	Period     = 14
)

// NotMeasurable is the note of a reading with too few chances.
const NotMeasurable = "量不出"

// Readings computes the readings for the period ending on day and stores them in State.Readings (replacing the same
// code and period).
func Readings(st *store.State, dir, day string) ([]entry.Reading, error) {
	sd, err := loadSide(dir)
	if err != nil {
		return nil, err
	}
	days := periodDays(day, Period)
	in := map[string]bool{}
	for _, d := range days {
		in[d] = true
	}
	var msgs, asks, sessions, askInfra, repeats int
	for d, s := range sd.Days {
		if in[d] {
			msgs += s.UserMsgs
			asks += s.Asks
			sessions += s.Sessions + s.Cards
			askInfra += s.AskInfra
		}
	}
	// A known object in a stretch with errors is only a screen hit (names turn up in paths); a repeat is one the
	// model judged worth digging.
	for _, d := range st.Digs {
		if in[d.Day] && d.Signal == entry.SigRepeat && d.Decision == DecDig {
			repeats++
		}
	}
	var corr, filed, recur, said int
	perEntry := map[string]int{}
	for _, c := range st.Corrections {
		if !in[c.Day] {
			continue
		}
		corr++
		if c.Entry != "" {
			filed++
		}
		if c.Recurrence {
			recur++
			perEntry[c.Entry]++
		}
		if containsAny(c.Quote, alreadySaid) && (c.Source == entry.SrcAsk || containsAny(c.Prior, []string{"?", "？"})) {
			said++
		}
	}
	var notes []string
	for id, n := range perEntry {
		notes = append(notes, fmt.Sprintf("%s×%d", id, n))
	}
	sort.Strings(notes)
	out := []entry.Reading{
		reading("R1", day, corr, msgs, 100, ""),
		reading("R1.recur", day, recur, filed, 1, strings.Join(notes, " ")),
		reading("R2", day, said, asks, 100, ""),
		reading("R3", day, askInfra, sessions, 100, ""),
		reading("R3.repeat", day, repeats, sessions, 1, ""),
	}
	for _, r := range out {
		st.Readings = putReading(st.Readings, r)
	}
	return out, nil
}

func reading(code, day string, num, den int, scale float64, note string) entry.Reading {
	r := entry.Reading{Code: code, Period: day, Num: num, Den: den, Measure: den >= MinChances, Note: note}
	if den > 0 {
		r.Value = float64(num) * scale / float64(den)
	}
	if !r.Measure {
		r.Note = strings.TrimSpace(NotMeasurable + " " + note)
	}
	return r
}

func putReading(rs []entry.Reading, r entry.Reading) []entry.Reading {
	for i := range rs {
		if rs[i].Code == r.Code && rs[i].Period == r.Period {
			rs[i] = r
			return rs
		}
	}
	return append(rs, r)
}

func periodDays(day string, n int) []string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return nil
	}
	var out []string
	for i := n - 1; i >= 0; i-- {
		out = append(out, t.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

// Volume is what the tool-segment reading cost over some days: the page's "read this week, segments dug" (3.1).
type Volume struct {
	Days         int            `json:"days"`
	FoldBytes    int            `json:"fold_bytes"`    // folded pages read
	SegmentBytes int            `json:"segment_bytes"` // unfolded stretches read for digs
	Segments     int            `json:"segments"`      // stretches read
	Hits         int            `json:"hits"`          // signals screened
	BySignal     map[string]int `json:"by_signal"`     // signal → stretches read
	Decisions    map[string]int `json:"decisions"`     // dig | skip | waiting_hub
	Entries      int            `json:"entries"`       // entries the digs made or saw again
	FoldsUnknown int            `json:"folds_unknown"`
}

// VolumeOf sums the digs and the folded-page reading of the given days.
func VolumeOf(st *store.State, dir string, days []string) (Volume, error) {
	sd, err := loadSide(dir)
	if err != nil {
		return Volume{}, err
	}
	v := Volume{Days: len(days), BySignal: map[string]int{}, Decisions: map[string]int{}}
	in := map[string]bool{}
	for _, d := range days {
		in[d] = true
		if s := sd.Days[d]; s != nil {
			v.FoldBytes += s.FoldBytes
			v.FoldsUnknown += s.FoldsUnknown
		}
	}
	for _, d := range st.Digs {
		if !in[d.Day] {
			continue
		}
		v.Hits++
		v.Decisions[d.Decision]++
		if d.Bytes > 0 {
			v.Segments++
			v.SegmentBytes += d.Bytes
			v.BySignal[d.Signal]++
		}
		v.Entries += len(d.Entries)
	}
	return v, nil
}
