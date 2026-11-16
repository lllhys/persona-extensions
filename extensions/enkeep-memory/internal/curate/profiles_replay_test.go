package curate

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// TestProfileReplayReadout reads a copy of a replay's state (ENKEEP_PROFILE_STATE: its directory; skipped without
// it) and reports, for the first ENKEEP_PROFILE_CARDS cards (default 10) of the intersection list, the profile each
// would be written as: items per part, bytes, items cut to fit and items left out for having no source. Counts
// only: no name and no text is printed. Nothing is written.
func TestProfileReplayReadout(t *testing.T) {
	dir := os.Getenv("ENKEEP_PROFILE_STATE")
	if dir == "" {
		t.Skip("ENKEEP_PROFILE_STATE not set")
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	today := os.Getenv("ENKEEP_PROFILE_DAY")
	if today == "" {
		today = "2026-11-11"
	}
	day, _ := time.Parse("2006-01-02", today)
	c := &Curator{St: st, Cfg: Defaults(), Loc: time.UTC, Now: func() time.Time { return day.Add(12 * time.Hour) }}
	inferred := entry.On(st.S.Switches, entry.K15Inferred)
	n := 10
	if v := os.Getenv("ENKEEP_PROFILE_CARDS"); v != "" {
		if _, err := fmt.Sscan(v, &n); err != nil {
			t.Fatal(err)
		}
	}
	shown := 0
	for i, sm := range c.PersonCards(today) {
		if shown >= n {
			break
		}
		card := c.PersonCard(sm.Person, today)
		if cardAsOf(card) == "" {
			continue
		}
		shown++
		p, fs := c.cardProfile(card, inferred)
		folded := map[string]int{}
		for sec, ls := range card.Parts {
			for _, l := range ls {
				if !l.Pending {
					folded[sec]++
				}
			}
		}
		t.Logf("card #%d lines on the card (folded): facts %d, collaboration %d, user_eval %d, inferred %d, commitments %d; pending %d, hidden %d",
			shown, folded["facts"], folded["collaboration"], folded["user_eval"], folded["inferred"], folded["commitments"], card.Pending, card.Hidden)
		t.Logf("card #%d (rank %d, %d person entries): facts %d, views %d, inferences %d, commitments %d; %d bytes; cut %d, shortened %d, no source %d; hub rules: %v",
			shown, i+1, len(card.Persons), len(p.Facts), len(p.Views), len(p.Inferences), len(p.Commitments), kbsrc.ProfileBytes(&p),
			fs.Dropped, fs.Shortened, fs.NoSource, kbsrc.CheckProfile(&p) == nil)
	}
}
