// Package usage turns the stored minute buckets into the numbers the page, the card, the reminders and the assistant
// tools show: totals over a range of days, the change against the range before, a daily trend, and rankings by model,
// machine and launcher. A day is a calendar day in the hub's time zone.
package usage

import (
	"cmp"
	"slices"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

// Tokens are the four kinds and the replies, summed.
type Tokens struct {
	Input      int64 `json:"in"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
	Output     int64 `json:"out"`
	Requests   int   `json:"req"`
}

// Total is every token, cache reads included.
func (t Tokens) Total() int64 { return t.Input + t.CacheWrite + t.CacheRead + t.Output }

// add counts one bucket.
func (t *Tokens) add(b store.Bucket) {
	t.Input += b.Input
	t.CacheWrite += b.CacheWrite
	t.CacheRead += b.CacheRead
	t.Output += b.Output
	t.Requests += b.Requests
}

// Share is one name's sums: a model, a machine or a launcher.
type Share struct {
	Name string `json:"name"`
	Tokens
}

// DayTotal is one day's sums, for the trend.
type DayTotal struct {
	Date string `json:"date"`
	Tokens
}

// Filter keeps the buckets a view shows. An empty field keeps all of that kind.
type Filter struct{ Machine, Model, Launcher string }

// Keeps reports whether a bucket passes the filter.
func (f Filter) Keeps(machine string, b store.Bucket) bool { return f.keeps(machine, b) }

func (f Filter) keeps(machine string, b store.Bucket) bool {
	return (f.Machine == "" || f.Machine == machine) &&
		(f.Model == "" || f.Model == b.Model) &&
		(f.Launcher == "" || f.Launcher == b.Launcher)
}

// Range is one span of days ending today, with the same span before it for the comparison.
type Range struct {
	Days                int        // how many days, today included
	Total               Tokens     // the span's sums
	Before              Tokens     // the span before it, for the change
	Trend               []DayTotal // one entry per day of the span, oldest first, days with nothing included
	Models              []Share    // largest first
	Machines, Launchers []Share
}

// Summarize sums st over the days days ending on today (today included), and over the days days before that.
// loc decides which day a minute belongs to. f keeps a subset; the rankings of a filtered kind still list every name,
// so a menu can offer them.
func Summarize(st *store.State, today time.Time, days int, loc *time.Location, f Filter) Range {
	if days < 1 {
		days = 1
	}
	end := dateOf(today, loc)
	start := end.AddDate(0, 0, -(days - 1))
	before := start.AddDate(0, 0, -days)
	r := Range{Days: days, Trend: make([]DayTotal, days)}
	for i := range r.Trend {
		r.Trend[i].Date = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	models, machines, launchers := map[string]*Tokens{}, map[string]*Tokens{}, map[string]*Tokens{}
	for _, d := range st.Days {
		day, err := time.Parse("2006-01-02", d.Date)
		if err != nil || day.Before(before) || day.After(end) {
			continue
		}
		i := int(day.Sub(start).Hours() / 24) // negative for the span before
		for _, b := range d.Buckets {
			if !f.keeps(d.Machine, b) {
				continue
			}
			if i >= 0 {
				r.Total.add(b)
				r.Trend[i].add(b)
				add(models, b.Model, b)
				add(machines, d.Machine, b)
				add(launchers, b.Launcher, b)
			} else {
				r.Before.add(b)
			}
		}
	}
	r.Models, r.Machines, r.Launchers = ranked(models), ranked(machines), ranked(launchers)
	return r
}

// add counts b under name.
func add(m map[string]*Tokens, name string, b store.Bucket) {
	t := m[name]
	if t == nil {
		t = &Tokens{}
		m[name] = t
	}
	t.add(b)
}

// ranked lists the shares largest first, then by name.
func ranked(m map[string]*Tokens) []Share {
	out := make([]Share, 0, len(m))
	for name, t := range m {
		out = append(out, Share{Name: name, Tokens: *t})
	}
	slices.SortFunc(out, func(a, b Share) int {
		return cmp.Or(cmp.Compare(b.Total(), a.Total()), cmp.Compare(a.Name, b.Name))
	})
	return out
}

// dateOf is the calendar day of t in loc, at midnight UTC so day arithmetic needs no zone.
func dateOf(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Hour is one model's sums in one clock hour: the hour of day (0-23) and the day it fell on.
type Hour struct {
	Day  string // YYYY-MM-DD
	Hour int
	Tokens
}

// ModelHours sums one model's buckets per clock hour in loc, for the spike reminder.
func ModelHours(st *store.State, model string, loc *time.Location) []Hour {
	type hk struct {
		day  string
		hour int
	}
	m := map[hk]*Tokens{}
	var order []hk
	for _, d := range st.Days {
		for _, b := range d.Buckets {
			if b.Model != model {
				continue
			}
			at := time.Unix(b.Minute, 0).In(loc)
			k := hk{at.Format("2006-01-02"), at.Hour()}
			if m[k] == nil {
				m[k] = &Tokens{}
				order = append(order, k)
			}
			m[k].add(b)
		}
	}
	out := make([]Hour, len(order))
	for i, k := range order {
		out[i] = Hour{Day: k.day, Hour: k.hour, Tokens: *m[k]}
	}
	return out
}

// Names lists the distinct models, machines and launchers in st, each sorted, for the filter menus.
func Names(st *store.State) (models, machines, launchers []string) {
	return distinct(st, func(d store.Day, b store.Bucket) string { return b.Model }),
		distinct(st, func(d store.Day, b store.Bucket) string { return d.Machine }),
		distinct(st, func(d store.Day, b store.Bucket) string { return b.Launcher })
}

func distinct(st *store.State, of func(store.Day, store.Bucket) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range st.Days {
		for _, b := range d.Buckets {
			n := of(d, b)
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return out
}
