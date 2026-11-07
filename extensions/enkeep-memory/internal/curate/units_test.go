package curate

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // the America/New_York rules, wherever the test runs

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona/sdk/go/wire"
)

// recentSource is a kbsrc.Source with only documents: Recent filters by since/until as kb.recent does, Get
// returns non-empty text for any of them.
type recentSource struct {
	kbsrc.Source
	entries []wire.KBEntry
}

func (s recentSource) Recent(_ context.Context, since, until time.Time) ([]wire.KBEntry, error) {
	var out []wire.KBEntry
	for _, e := range s.entries {
		if !e.Updated.Before(since) && (until.IsZero() || e.Updated.Before(until)) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s recentSource) Get(_ context.Context, kind, id string) (wire.KBDetail, error) {
	return wire.KBDetail{ID: id, Summary: "synthetic document " + id, Meta: json.RawMessage(`{}`)}, nil
}

// TestUnitsLocalCalendarDay: a non-chat entry counts on day when it changed between that day's local midnight and
// the next one, including the 23- and 25-hour days when daylight saving time starts and ends.
func TestUnitsLocalCalendarDay(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		day  time.Time
	}{
		{"spring forward", time.Date(2026, time.March, 8, 0, 0, 0, 0, loc)},
		{"fall back", time.Date(2026, time.November, 1, 0, 0, 0, 0, loc)},
		{"ordinary day", time.Date(2026, time.November, 7, 0, 0, 0, 0, loc)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			y, m, d := tc.day.Date()
			at := func(dd, h, min int) time.Time { return time.Date(y, m, d+dd, h, min, 0, 0, loc) }
			docs := []struct {
				id   string
				when time.Time
				want bool
			}{
				{"before-start", at(-1, 23, 59), false},
				{"at-start", at(0, 0, 0), true},
				{"late-evening", at(0, 23, 30), true},
				{"next-midnight", at(1, 0, 0), false},
				{"next-day-0030", at(1, 0, 30), false},
			}
			src := recentSource{}
			var want []string
			for _, doc := range docs {
				src.entries = append(src.entries, wire.KBEntry{Kind: "docs", ID: doc.id, Name: doc.id, Updated: doc.when})
				if doc.want {
					want = append(want, "docs/"+doc.id)
				}
			}
			c := &Curator{Src: src, Cfg: Defaults(), Loc: loc}
			units, err := c.units(context.Background(), tc.day.Format("2006-01-02"))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, u := range units {
				got = append(got, u.Ref)
			}
			slices.Sort(want)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("units(%s) = %v, want %v", tc.day.Format("2006-01-02"), got, want)
			}
		})
	}
}
