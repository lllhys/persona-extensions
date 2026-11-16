package budget

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

var loc = time.FixedZone("UTC+8", 8*3600)

func nowAt(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

// stateWith builds a state from "date hour model tokens" lines.
func stateWith(lines ...string) *store.State {
	st := &store.State{Format: 1}
	for _, l := range lines {
		f := strings.Fields(l)
		t, _ := time.ParseInLocation("2006-01-02 15", f[0]+" "+f[1], loc)
		var n int64
		for _, c := range f[3] {
			n = n*10 + int64(c-'0')
		}
		b := store.Bucket{Minute: t.Unix(), Model: f[2], Input: n}
		placed := false
		for i := range st.Days {
			if st.Days[i].Date == f[0] {
				st.Days[i].Buckets = append(st.Days[i].Buckets, b)
				placed = true
			}
		}
		if !placed {
			st.Days = append(st.Days, store.Day{Date: f[0], Machine: "m-a", Buckets: []store.Bucket{b}})
		}
	}
	return st
}

func TestBudgetThresholdsOncePerDay(t *testing.T) {
	f := Open("")
	if err := f.Set(Budget{Span: Daily, Limit: 1000}); err != nil {
		t.Fatal(err)
	}
	now := nowAt("2026-11-13 12:00")

	st := stateWith("2026-11-13 09 alpha 700")
	got, err := f.Alerts(st, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("700 of 1000 alerted: %+v", got)
	}

	st = stateWith("2026-11-13 09 alpha 850")
	got, err = f.Alerts(st, now, loc)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Title, "八成") {
		t.Fatalf("850 of 1000: %+v %v", got, err)
	}
	got, err = f.Alerts(st, now, loc)
	if err != nil || len(got) != 0 {
		t.Fatalf("the 80%% alert fired again the same day: %+v", got)
	}

	st = stateWith("2026-11-13 09 alpha 1200")
	got, err = f.Alerts(st, now, loc)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Title, "已用完") {
		t.Fatalf("1200 of 1000: %+v %v", got, err)
	}
	got, _ = f.Alerts(st, now, loc)
	if len(got) != 0 {
		t.Fatalf("the 100%% alert fired again: %+v", got)
	}

	// The next day both can fire again, and the higher one wins.
	got, err = f.Alerts(stateWith("2026-11-14 09 alpha 5000"), nowAt("2026-11-14 12:00"), loc)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Title, "已用完") {
		t.Fatalf("next day: %+v %v", got, err)
	}
}

func TestModelBudgetCountsOnlyThatModel(t *testing.T) {
	f := Open("")
	if err := f.Set(Budget{Model: "beta", Span: Daily, Limit: 100}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Alerts(stateWith("2026-11-13 09 alpha 9000"), nowAt("2026-11-13 12:00"), loc)
	if err != nil || len(got) != 0 {
		t.Fatalf("alpha's usage alerted beta's budget: %+v %v", got, err)
	}
	got, err = f.Alerts(stateWith("2026-11-13 09 beta 100"), nowAt("2026-11-13 12:00"), loc)
	if err != nil || len(got) != 1 {
		t.Fatalf("beta at its limit: %+v %v", got, err)
	}
}

func TestWeeklyBudgetStartsMonday(t *testing.T) {
	f := Open("")
	if err := f.Set(Budget{Span: Weekly, Limit: 1000}); err != nil {
		t.Fatal(err)
	}
	// 2026-11-13 is a Friday. Monday the 9th counts; Sunday the 8th does not.
	st := stateWith("2026-11-08 09 alpha 900", "2026-11-09 09 alpha 600", "2026-11-13 09 alpha 300")
	ss, err := f.Statuses(st, nowAt("2026-11-13 12:00"), loc)
	if err != nil {
		t.Fatal(err)
	}
	if ss[0].Used != 900 {
		t.Fatalf("week so far %d, want 900", ss[0].Used)
	}
}

func TestSpikeOverFiveTimesTheHourMean(t *testing.T) {
	f := Open("")
	// The same hour on three earlier days averaged 1000; today it is 6000.
	st := stateWith(
		"2026-11-10 09 alpha 1000",
		"2026-11-11 09 alpha 1000",
		"2026-11-12 09 alpha 1000",
		"2026-11-13 09 alpha 6000",
	)
	got, err := f.Alerts(st, nowAt("2026-11-13 09:30"), loc)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Title, "异常") {
		t.Fatalf("6x the mean: %+v %v", got, err)
	}
	got, _ = f.Alerts(st, nowAt("2026-11-13 09:45"), loc)
	if len(got) != 0 {
		t.Fatalf("the spike fired twice: %+v", got)
	}

	// Four times the mean is not a spike, and a small hour is not one either.
	g := Open("")
	small := stateWith("2026-11-12 09 alpha 100", "2026-11-13 09 alpha 900")
	got, err = g.Alerts(small, nowAt("2026-11-13 09:30"), loc)
	if err != nil || len(got) != 0 {
		t.Fatalf("below 1000 tokens spiked: %+v %v", got, err)
	}
}

func TestSetRejectsBadBudgets(t *testing.T) {
	f := Open("")
	if err := f.Set(Budget{Span: "month", Limit: 10}); err == nil {
		t.Fatal("a month span was accepted")
	}
	if err := f.Set(Budget{Span: Daily, Limit: 0}); err == nil {
		t.Fatal("a zero limit was accepted")
	}
}

func TestDelete(t *testing.T) {
	f := Open("")
	_ = f.Set(Budget{Model: "alpha", Span: Daily, Limit: 10})
	_ = f.Set(Budget{Span: Weekly, Limit: 20})
	if err := f.Delete(Daily, "alpha"); err != nil {
		t.Fatal(err)
	}
	left, _ := f.List()
	if len(left) != 1 || left[0].Span != Weekly {
		t.Fatalf("left %+v, want only the weekly budget", left)
	}
}
