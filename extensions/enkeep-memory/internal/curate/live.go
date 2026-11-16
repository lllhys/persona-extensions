package curate

import (
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Live is what a page reads while a run holds the store's lock (a daily pass holds it for its whole run, tens of
// minutes with a real model): the run's progress, and a snapshot of what the health page shows, taken whenever the
// state last settled. Its own lock is only held to copy a value in or out, so reading it never waits for a run.
// A nil Live (a trial, tests) keeps nothing.
type Live struct {
	mu   sync.Mutex
	prog *Progress
	snap *Snapshot
}

// Progress is the run in progress.
type Progress struct {
	Kind    string // daily | weekly
	Period  string
	Stage   string // 分拣 | 摘要 | 周级
	Done    int    // units of the stage done (the current one included)
	Total   int
	Unit    string // the unit being read
	Refused int    // batches the model declined so far
	Started time.Time
}

// Snapshot is the pages' data as of At.
type Snapshot struct {
	At         time.Time
	State      *store.State // a copy of the whole state, for the main page and the overview card; never changed
	FunnelDay  string
	FunnelToks map[string]int // decision → estimated tokens
	Daily      *RunBrief      // the last finished daily run
}

// RunBrief is a finished run as the health page shows it.
type RunBrief struct {
	Period   string
	Finished string
	Refusals []store.Refusal
	Skipped  int // lines not read because the model declined them
}

func (l *Live) begin(kind, period string, now time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.prog = &Progress{Kind: kind, Period: period, Started: now}
	l.mu.Unlock()
}

func (l *Live) end() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.prog = nil
	l.mu.Unlock()
}

// progress changes the progress of the run in progress, if any.
func (l *Live) progress(f func(p *Progress)) {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.prog != nil {
		f(l.prog)
	}
	l.mu.Unlock()
}

// Progress is a copy of the run in progress; ok is false when none is.
func (l *Live) Progress() (p Progress, ok bool) {
	if l == nil {
		return Progress{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prog == nil {
		return Progress{}, false
	}
	return *l.prog, true
}

// Snapshot is the last one taken, nil before the first. It is not changed afterwards: a new one replaces it.
func (l *Live) Snapshot() *Snapshot {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snap
}

// Publish takes a new snapshot of the state for the pages. Call it with the store's lock held: after a run, after
// a source is forgotten, after the user changed something from a page, and once at start.
func (c *Curator) Publish() {
	if c.Live == nil {
		return
	}
	s := &Snapshot{At: c.now()}
	st, err := c.St.S.Clone(c.now())
	if err != nil { // cannot happen to a state that state.json holds; keep the old snapshot rather than an empty one
		c.logf("snapshot: %v", err)
		return
	}
	s.State = st
	var days []string
	for d := range c.St.S.Funnel {
		days = append(days, d)
	}
	sort.Strings(days)
	if len(days) > 0 {
		s.FunnelDay = days[len(days)-1]
		s.FunnelToks = maps.Clone(c.St.S.Funnel[s.FunnelDay].Tokens)
	}
	for i := len(c.St.S.Runs) - 1; i >= 0; i-- {
		if r := c.St.S.Runs[i]; r.Kind == "daily" && !r.DryRun && r.Finished != "" {
			s.Daily = &RunBrief{Period: r.Period, Finished: r.Finished, Refusals: slices.Clone(r.Refusals),
				Skipped: r.Counts["daily_refused_lines"]}
			break
		}
	}
	c.Live.mu.Lock()
	c.Live.snap = s
	c.Live.mu.Unlock()
}
