package main

import (
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

func TestRerunDays(t *testing.T) {
	c := curate.Defaults()
	if len(c.RerunDays()) != 0 {
		t.Error("unset: no days")
	}
	c.RerunRange = "2026-11-04..2026-11-10"
	if d := c.RerunDays(); len(d) != 7 || d[0] != "2026-11-04" || d[6] != "2026-11-10" {
		t.Errorf("days: %v", d)
	}
	for _, bad := range []string{"2026-11-10..2026-11-04", "x..y", "2026-11-04"} {
		c.RerunRange = bad
		if len(c.RerunDays()) != 0 {
			t.Errorf("%q is not a range", bad)
		}
	}
}

// rerun_range digests each day of the range again with the current daily prompt, once each: what an earlier prompt
// produced stays as it was (a rejected item stays rejected), and a second pass does nothing more.
func TestRerunRange(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.S.Items["i-old"] = &store.Item{ID: "i-old", Class: store.Fact, Claim: "an old item", Status: "rejected", Prompt: "daily/3"}
	st.S.Items["i-keep"] = &store.Item{ID: "i-keep", Class: store.Fact, Claim: "another old item", Status: "candidate", Prompt: "daily/3"}
	cfg := curate.Defaults()
	cfg.Mode, cfg.AdmitAllBelowTokens = "shadow", 0
	cfg.RerunRange = "2026-11-03..2026-11-04"
	cfg.BackfillDays = 0                                // the first-run replay is tested on its own (backfill_test.go)
	now := time.Date(2026, 11, 9, 4, 0, 0, 0, time.UTC) // after daily_at; yesterday (11-08) is not in the range
	a := newApp()
	a.c = &curate.Curator{Src: fx, Model: &stub.Model{}, St: st, Cfg: cfg, Loc: time.UTC, Now: func() time.Time { return now }}
	settle := func() {
		for i := 0; i < 600; i++ {
			a.mu.Lock()
			idle := a.running == ""
			a.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("a run never ended")
	}
	for i := 0; i < 8; i++ {
		a.tick(t.Context())
		settle()
	}
	for _, day := range []string{"2026-11-03", "2026-11-04"} {
		if !slices.Contains(st.S.Reruns, day+"@"+schema.DailyPrompt) {
			t.Errorf("%s not digested again: %v", day, st.S.Reruns)
		}
	}
	runs := 0
	for _, r := range st.S.Runs {
		if r.Kind == "daily" && (r.Period == "2026-11-03" || r.Period == "2026-11-04") {
			runs++
		}
	}
	if runs != 2 {
		t.Errorf("each day once, got %d daily runs of the range", runs)
	}
	if st.S.Items["i-old"].Status != "rejected" || st.S.Items["i-keep"].Status != "candidate" {
		t.Error("an item an earlier prompt produced changed")
	}
}

func TestRerunRangeSkipsUnfinishedDays(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := curate.Defaults()
	cfg.Mode, cfg.AdmitAllBelowTokens = "shadow", 0
	cfg.BackfillDays = 0
	cfg.RerunRange = "2026-11-04..2026-11-05"
	now := time.Date(2026, 11, 4, 4, 0, 0, 0, time.UTC) // after daily_at: today and tomorrow are not complete
	a := newApp()
	a.c = &curate.Curator{Src: fx, Model: &stub.Model{}, St: st, Cfg: cfg, Loc: time.UTC, Now: func() time.Time { return now }}
	settle := func() {
		for i := 0; i < 600; i++ {
			a.mu.Lock()
			idle := a.running == ""
			a.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("a run never ended")
	}
	dailyRuns := func(day string) int {
		n := 0
		for _, r := range st.S.Runs {
			if r.Kind == "daily" && r.Period == day {
				n++
			}
		}
		return n
	}
	for i := 0; i < 4; i++ {
		a.tick(t.Context())
		settle()
	}
	for _, day := range []string{"2026-11-04", "2026-11-05"} {
		if dailyRuns(day) != 0 {
			t.Errorf("%s ran before the day was complete: %+v", day, st.S.Runs)
		}
		if slices.Contains(st.S.Reruns, day+"@"+schema.DailyPrompt) {
			t.Errorf("%s has a premature rerun marker", day)
		}
	}

	now = now.AddDate(0, 0, 1)
	a.tick(t.Context())
	settle()
	if got := dailyRuns("2026-11-04"); got != 1 {
		t.Errorf("the previous day's formal daily runs = %d, want 1: %+v", got, st.S.Runs)
	}
	if got := dailyRuns("2026-11-05"); got != 0 {
		t.Errorf("today was rerun early: %+v", st.S.Runs)
	}
	dayEnd := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for _, r := range st.S.Runs {
		if r.Kind == "daily" && r.Period == "2026-11-04" {
			started, err := time.Parse(time.RFC3339, r.Started)
			if err != nil || started.Before(dayEnd) {
				t.Errorf("the formal daily is an early leftover: %+v", r)
			}
		}
	}
}

func TestDailyRunBeforeDayEndDoesNotCount(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.S.Runs = []*store.Run{{
		ID: "early", Kind: "daily", Period: "2026-11-04",
		Started: "2026-11-04T12:00:00Z", Finished: "2026-11-04T12:30:00Z",
	}}
	cfg := curate.Defaults()
	cfg.Mode, cfg.AdmitAllBelowTokens = "shadow", 0
	cfg.BackfillDays = 0
	now := time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)
	a := newApp()
	a.c = &curate.Curator{Src: fx, Model: &stub.Model{}, St: st, Cfg: cfg, Loc: time.UTC, Now: func() time.Time { return now }}
	settle := func() {
		for i := 0; i < 600; i++ {
			a.mu.Lock()
			idle := a.running == ""
			a.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("a run never ended")
	}
	dailyRuns := func() int {
		n := 0
		for _, r := range st.S.Runs {
			if r.Kind == "daily" && r.Period == "2026-11-04" {
				n++
			}
		}
		return n
	}
	a.tick(t.Context())
	settle()
	if dailyRuns() != 2 {
		t.Fatalf("a premature Finished Run suppressed the formal daily: %+v", st.S.Runs)
	}
	a.tick(t.Context())
	settle()
	if dailyRuns() != 2 {
		t.Errorf("the formal daily ran twice: %+v", st.S.Runs)
	}
}

func TestPrematureRerunMarkerDoesNotBlockRecovery(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.S.Runs = []*store.Run{{
		ID: "early", Kind: "daily", Period: "2026-11-04",
		Started: "2026-11-04T12:00:00Z", Finished: "2026-11-04T12:30:00Z",
	}, {
		ID: "early-synth", Kind: "synth", Period: "2026-11-04",
		Started: "2026-11-04T12:35:00Z", Finished: "2026-11-04T12:40:00Z",
	}, {
		ID: "old-synth", Kind: "synth", Period: "2026-11-04",
		Started: "2026-11-05T04:00:00Z", Finished: "2026-11-05T04:10:00Z",
	}}
	st.S.Reruns = []string{"2026-11-04@" + schema.DailyPrompt}
	cfg := curate.Defaults()
	cfg.Mode, cfg.AdmitAllBelowTokens = "shadow", 0
	cfg.BackfillDays = 0
	cfg.RerunRange = "2026-11-04..2026-11-04"
	now := time.Date(2026, 11, 6, 4, 0, 0, 0, time.UTC) // the premature run is older than yesterday
	a := newApp()
	a.c = &curate.Curator{Src: fx, Model: &stub.Model{}, St: st, Cfg: cfg, Loc: time.UTC, Now: func() time.Time { return now }}
	settle := func() {
		for i := 0; i < 600; i++ {
			a.mu.Lock()
			idle := a.running == ""
			a.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("a run never ended")
	}
	dailyRuns := func() int {
		n := 0
		for _, r := range st.S.Runs {
			if r.Kind == "daily" && r.Period == "2026-11-04" {
				n++
			}
		}
		return n
	}
	dayEnd := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	validSynthRuns := func() int {
		var latestDaily time.Time
		for _, r := range st.S.Runs {
			if r.Kind != "daily" || r.Period != "2026-11-04" {
				continue
			}
			started, startErr := time.Parse(time.RFC3339, r.Started)
			finished, finishErr := time.Parse(time.RFC3339, r.Finished)
			if startErr == nil && finishErr == nil && !started.Before(dayEnd) && !finished.Before(started) && finished.After(latestDaily) {
				latestDaily = finished
			}
		}
		if latestDaily.IsZero() {
			return 0
		}
		n := 0
		for _, r := range st.S.Runs {
			if r.Kind != "synth" || r.Period != "2026-11-04" {
				continue
			}
			started, err := time.Parse(time.RFC3339, r.Started)
			if err == nil && !started.Before(latestDaily) {
				n++
			}
		}
		return n
	}

	a.tick(t.Context()) // the formal yesterday run goes first
	settle()
	if dailyRuns() != 1 {
		t.Fatalf("unexpected early runs: %+v", st.S.Runs)
	}
	a.tick(t.Context()) // the stale marker must not suppress a valid rerun
	settle()
	if dailyRuns() != 2 {
		t.Errorf("the premature marker suppressed recovery: %+v", st.S.Runs)
	}
	if a.synthAfterDaily("2026-11-04") {
		t.Errorf("synth ran before a valid rerun completed")
	}
	a.tick(t.Context()) // weekly is scheduled before synth
	settle()
	a.tick(t.Context())
	settle()
	if !a.synthAfterDaily("2026-11-04") {
		t.Errorf("synth did not follow the valid rerun: %+v", st.S.Runs)
	}
	if dailyRuns() != 2 && a.synthAfterDaily("2026-11-04") {
		t.Errorf("synth used a marker without a valid daily run")
	}
	if dailyRuns() != 2 {
		t.Errorf("recovery repeated the old day: %+v", st.S.Runs)
	}
	if validSynthRuns() != 1 {
		t.Errorf("an early synth was accepted instead of a post-recovery synth: %+v", st.S.Runs)
	}
}

func TestRunCompletionRejectsUnknownDayStart(t *testing.T) {
	for _, kind := range []string{"daily", "synth"} {
		for _, started := range []string{"", "not-a-time"} {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			st.S.Runs = []*store.Run{{Kind: kind, Period: "2026-11-04", Started: started, Finished: "2026-11-04T12:30:00Z"}}
			a := newApp()
			a.c = &curate.Curator{St: st, Loc: time.UTC}
			if a.ran(kind, "2026-11-04") {
				t.Errorf("%s with Started %q counted as a complete-day run", kind, started)
			}
			st.S.Runs[0].Started = "2026-11-05T00:00:00Z"
			st.S.Runs[0].Finished = "2026-11-05T00:30:00Z"
			if !a.ran(kind, "2026-11-04") {
				t.Errorf("%s with a valid later Started was rejected", kind)
			}
		}
	}
}

func TestInvalidDailyFinishDoesNotSkipRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finished string
	}{
		{name: "malformed", finished: "not-a-time"},
		{name: "inverted", finished: "2026-11-04T12:30:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, err := kbsrc.LoadFixture("testdata/sample")
			if err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			st.S.Runs = []*store.Run{{
				ID: "invalid-finish", Kind: "daily", Period: "2026-11-04",
				Started: "2026-11-05T04:00:00Z", Finished: tc.finished,
			}}
			st.S.Reruns = []string{"2026-11-04@" + schema.DailyPrompt}
			cfg := curate.Defaults()
			cfg.Mode, cfg.AdmitAllBelowTokens = "shadow", 0
			cfg.BackfillDays = 0
			cfg.RerunRange = "2026-11-04..2026-11-04"
			now := time.Date(2026, 11, 6, 4, 0, 0, 0, time.UTC)
			a := newApp()
			a.c = &curate.Curator{Src: fx, Model: &stub.Model{}, St: st, Cfg: cfg, Loc: time.UTC, Now: func() time.Time { return now }}
			settle := func() {
				for i := 0; i < 600; i++ {
					a.mu.Lock()
					idle := a.running == ""
					a.mu.Unlock()
					if idle {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatal("a run never ended")
			}
			dailyRuns := func() int {
				n := 0
				for _, r := range st.S.Runs {
					if r.Kind == "daily" && r.Period == "2026-11-04" {
						n++
					}
				}
				return n
			}

			a.tick(t.Context()) // formal yesterday
			settle()
			a.tick(t.Context()) // an invalid Finished must not suppress the rerun
			settle()
			if dailyRuns() != 2 {
				t.Errorf("invalid daily Finished suppressed recovery: %+v", st.S.Runs)
			}
			a.tick(t.Context()) // weekly
			settle()
			a.tick(t.Context()) // synth follows the recovered daily
			settle()
			if dailyRuns() != 2 {
				t.Errorf("recovery repeated the old day: %+v", st.S.Runs)
			}
			if !a.synthAfterDaily("2026-11-04") {
				t.Errorf("synth ran without a valid daily or did not follow recovery: %+v", st.S.Runs)
			}
		})
	}
}

func TestSynthWithInvalidFinishDoesNotCount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finished string
	}{
		{name: "malformed", finished: "not-a-time"},
		{name: "inverted", finished: "2026-11-04T12:30:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			st.S.Runs = []*store.Run{{
				Kind: "daily", Period: "2026-11-04",
				Started: "2026-11-05T04:00:00Z", Finished: "2026-11-05T04:10:00Z",
			}, {
				Kind: "synth", Period: "2026-11-04",
				Started: "2026-11-05T05:00:00Z", Finished: tc.finished,
			}}
			a := newApp()
			a.c = &curate.Curator{St: st, Loc: time.UTC}
			if a.synthAfterDaily("2026-11-04") {
				t.Errorf("synth with invalid Finished was accepted")
			}
			if a.ran("synth", "2026-11-04") {
				t.Errorf("ran accepted synth with invalid Finished")
			}
			st.S.Runs[1].Finished = "2026-11-05T05:30:00Z"
			if !a.synthAfterDaily("2026-11-04") || !a.ran("synth", "2026-11-04") {
				t.Errorf("a valid synth was rejected")
			}
		})
	}
}
