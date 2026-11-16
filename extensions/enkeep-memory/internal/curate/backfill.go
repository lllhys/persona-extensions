package curate

import (
	"context"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// DefaultBackfillDays is how far back the first-run replay reads by default (4.1): enough history for a start,
// bounded so the first day does not take forever; backfill_days changes it.
const DefaultBackfillDays = 90

// StartBackfill plans the first-run history replay (4.1) once: from backfill_days before today through yesterday,
// oldest first. It does nothing when one was planned already or backfill_days is 0. Exits are not written to the
// assistant before State.Backfill.Finished is set.
func (c *Curator) StartBackfill(today string) {
	c.St.Lock()
	defer c.St.Unlock()
	if c.St.S.Backfill != nil || c.Cfg.BackfillDays <= 0 {
		return
	}
	c.PlanBackfill(addDays(today, -c.Cfg.BackfillDays), addDays(today, -1))
}

// PlanBackfill sets the replay's range (the caller holds the lock). A range already planned is kept: progress
// resumes from where it stopped.
func (c *Curator) PlanBackfill(from, to string) {
	if c.St.S.Backfill != nil {
		return
	}
	c.St.S.Backfill = &store.Backfill{From: from, To: to, Next: from, Started: c.now().UTC().Format(time.RFC3339)}
	if to < from {
		c.St.S.Backfill.Next, c.St.S.Backfill.Finished = "", c.St.S.Backfill.Started
	}
	_ = c.St.Save()
}

// BackfillStep runs the replay's next day: the daily pass, then the weekly pass when the day closes an ISO week (or
// is the last day). Progress is saved after every day, so a restart goes on from the next one. It says whether the
// replay is finished.
func (c *Curator) BackfillStep(ctx context.Context) (bool, error) {
	c.St.Lock()
	b := c.St.S.Backfill
	if b == nil || b.Finished != "" || b.Next == "" {
		c.St.Unlock()
		return true, nil
	}
	day, last := b.Next, b.To
	c.St.Unlock()
	if _, err := c.RunDaily(ctx, day, false); err != nil {
		return false, err
	}
	if weekday(day) == time.Sunday || day == last {
		if _, err := c.RunWeekly(ctx, ISOWeek(day), false); err != nil {
			return false, err
		}
	}
	c.St.Lock()
	defer c.St.Unlock()
	b = c.St.S.Backfill
	b.Next = addDays(day, 1)
	if b.Next > b.To {
		b.Next, b.Finished = "", c.now().UTC().Format(time.RFC3339)
		// The replay is done: the exits are written now (4.1), not only at the next scheduled pass, so what the
		// history and the first-run answers gave reaches the assistant the day it was installed.
		run := &store.Run{ID: "backfill-" + day, Kind: "backfill", Period: day, Counts: map[string]int{}}
		var m model.Model
		if c.Model != nil {
			m = &model.Metered{Model: c.Model, Usage: c.St, Day: day}
		}
		c.compileExits(ctx, m, run)
	}
	return b.Finished != "", c.St.Save()
}

// RunBackfill runs the replay to its end, or until ctx is done or a day fails (the next call resumes there).
func (c *Curator) RunBackfill(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := c.BackfillStep(ctx)
		if err != nil || done {
			return err
		}
	}
}

// BackfillDone says whether the first-run replay is over (or there is none): only then may exits be written.
func (c *Curator) BackfillDone() bool {
	b := c.St.S.Backfill
	return b == nil || b.Finished != ""
}

func weekday(day string) time.Weekday {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return -1
	}
	return t.Weekday()
}
