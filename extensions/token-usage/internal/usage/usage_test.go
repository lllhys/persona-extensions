package usage

import (
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

var loc = time.FixedZone("UTC+8", 8*3600)

func at(date string, hour int, model, machine string, tokens int64) (string, store.Bucket) {
	t, _ := time.ParseInLocation("2006-01-02", date, loc)
	return machine, store.Bucket{Minute: t.Add(time.Duration(hour) * time.Hour).Unix(), Model: model, Input: tokens, Requests: 1}
}

func sample() *store.State {
	st := &store.State{Format: 1}
	add := func(date string, hour int, model, machine string, tokens int64) {
		m, b := at(date, hour, model, machine, tokens)
		for i := range st.Days {
			if st.Days[i].Date == date && st.Days[i].Machine == m {
				st.Days[i].Buckets = append(st.Days[i].Buckets, b)
				return
			}
		}
		st.Days = append(st.Days, store.Day{Date: date, Machine: m, Buckets: []store.Bucket{b}})
	}
	// Today: 1000 + 500 on m-a, 200 on m-b. Yesterday: 400. Eight days ago: 9000, outside a 7-day range.
	add("2026-11-13", 9, "alpha", "m-a", 1000)
	add("2026-11-13", 10, "beta", "m-a", 500)
	add("2026-11-13", 11, "alpha", "m-b", 200)
	add("2026-11-12", 9, "alpha", "m-a", 400)
	add("2026-11-05", 9, "alpha", "m-a", 9000)
	return st
}

func today() time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15", "2026-11-13 12", loc)
	return t
}

func TestSummarizeRangeAndComparison(t *testing.T) {
	r := Summarize(sample(), today(), 7, loc, Filter{})
	if r.Total.Total() != 2100 {
		t.Fatalf("7 days total %d, want 2100 (the day 8 days ago is out)", r.Total.Total())
	}
	if r.Before.Total() != 9000 {
		t.Fatalf("the 7 days before total %d, want 9000", r.Before.Total())
	}
	if len(r.Trend) != 7 || r.Trend[0].Date != "2026-11-07" || r.Trend[6].Total() != 1700 {
		t.Fatalf("trend starts %s and ends at %d, want 2026-11-07 and 1700", r.Trend[0].Date, r.Trend[6].Total())
	}
	if r.Models[0].Name != "alpha" || r.Models[0].Total() != 1600 {
		t.Fatalf("top model %s %d, want alpha 1600", r.Models[0].Name, r.Models[0].Total())
	}
	if r.Machines[0].Name != "m-a" || r.Machines[0].Total() != 1900 {
		t.Fatalf("top machine %s %d, want m-a 1900", r.Machines[0].Name, r.Machines[0].Total())
	}
}

func TestSummarizeFilter(t *testing.T) {
	r := Summarize(sample(), today(), 1, loc, Filter{Model: "alpha"})
	if r.Total.Total() != 1200 {
		t.Fatalf("today alpha %d, want 1200", r.Total.Total())
	}
	if len(r.Machines) != 2 {
		t.Fatalf("alpha runs on %d machines, want both", len(r.Machines))
	}
}

func TestNames(t *testing.T) {
	models, machines, _ := Names(sample())
	if len(models) != 2 || models[0] != "alpha" || len(machines) != 2 {
		t.Fatalf("models %v machines %v", models, machines)
	}
}
