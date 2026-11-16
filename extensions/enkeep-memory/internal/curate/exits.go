package curate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/readings"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/signals"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The end of the daily pass: the correction ledger and the friction digs of the day (internal/signals, P5), their
// readings, then the exits compiled from every entry (internal/export, P6). The weekly pass picks the ledger's spot
// check. Both run with the store's lock held and never on a dry run.

func (c *Curator) afterDaily(ctx context.Context, m model.Model, day string, run *store.Run, dry bool) {
	if dry {
		return
	}
	res, err := signals.Run(ctx, c.St.S, day, signals.Options{Hub: c.Hub, Model: m, Tier: tier(c.Cfg.DailyTier),
		Lang: c.language(), Loc: c.Loc, Now: c.now, Dir: c.St.Dir, Observe: c.Observe, Run: run.ID, Logf: c.logf, Workers: c.Workers})
	if err != nil {
		c.logf("signals %s: %v", day, err)
		run.Notes = append(run.Notes, "纠正台账和不顺信号没跑完："+err.Error())
	} else {
		run.Counts["signal_user_msgs"] = res.UserMsgs
		run.Counts["signal_corrections"] = res.Corrections
		run.Counts["signal_recurrences"] = res.Recurrences
		run.Counts["signal_new_entries"] = len(res.NewEntries)
		run.Counts["signal_digs"] = res.Digs
		run.Counts["signal_segment_bytes"] = res.SegmentBytes
		run.Counts["signal_fold_bytes"] = res.FoldBytes
		run.Counts["signal_mined"] = len(res.Mined)
		for sig, n := range res.Hits {
			run.Counts["signal_hit_"+sig] = n
		}
	}
	c.readings(ctx, day, run)
	c.compileExits(ctx, m, run)
}

// readings computes the period's readings (internal/readings) with the cards of the two weeks, and opens or closes
// K2 per fork type by the R4 reading (only on cards that said they are reversible).
func (c *Curator) readings(ctx context.Context, day string, run *store.Run) {
	in := readings.Input{St: c.St.S, Dir: c.St.Dir, Day: day, Loc: c.Loc}
	days := readings.Days(day)
	if c.Hub == nil || len(days) == 0 {
		in.CardsMissing = "no hub"
	} else {
		start, _ := time.ParseInLocation("2006-01-02", days[0], c.Loc)
		end, _ := time.ParseInLocation("2006-01-02", day, c.Loc)
		cards, err := c.Hub.Interactions(ctx, hubapi.InteractionsQuery{Since: start, Before: end.AddDate(0, 0, 1)})
		if err != nil && !(errors.Is(err, hubapi.ErrWaitingHub) && len(cards) > 0) {
			in.CardsMissing = err.Error()
		}
		in.Cards = cards
	}
	if _, err := readings.Compute(in); err != nil {
		c.logf("readings %s: %v", day, err)
		return
	}
	if in.CardsMissing == "" {
		set := readings.DaySet(day)
		for _, ch := range readings.AutoK2(c.St.S, readings.R4Of(c.St.S, in.Cards, c.Loc, set), readings.Overturned(c.St.S, set), day) {
			run.Notes = append(run.Notes, "默认判断开关自动调整："+ch)
		}
	}
}

// compileExits writes the exits from every entry (nothing to the assistant while the first-run replay goes on).
func (c *Curator) compileExits(ctx context.Context, m model.Model, run *store.Run) {
	rep, err := export.Compile(ctx, c.St.S, export.Options{Dir: c.St.Dir, Model: m, Tier: tier(c.Cfg.DailyTier), Lang: c.language(),
		Loc: c.Loc, Now: c.now, Hub: c.Hub, Run: run.ID, Event: func(ev entry.Event) { c.event(ev) }, Logf: c.logf})
	if err != nil {
		c.logf("exits %s: %v", run.Period, err)
		run.Notes = append(run.Notes, "出口没写完："+err.Error())
		return
	}
	if rep.Backfill {
		run.Notes = append(run.Notes, "首次回放还没完成：出口先不写给助手")
	}
	if rep.Deferred {
		run.Notes = append(run.Notes, "每轮说明块今天已经写过一次，新内容明天再写")
	}
	written := 0
	for _, f := range rep.Files {
		if f.Written {
			written++
		}
	}
	run.Counts["exit_files_written"] = written
	run.Counts["exit_rewritten"] = rep.Rewritten
	run.Counts["exit_rejected"] = len(rep.Rejected)
	run.Counts["exit_effective"] = len(rep.Effective)
	for _, w := range c.St.S.Exits.Warnings {
		run.Notes = append(run.Notes, "出口告警："+w)
	}
}

func (c *Curator) afterWeekly(week string, days []string, run *store.Run, dry bool) {
	if dry {
		return
	}
	n := len(signals.SampleWeek(c.St.S, week, days))
	run.Counts["correction_samples"] = n
	if n > 0 {
		run.Notes = append(run.Notes, fmt.Sprintf("本周抽了 %d 条纠正判定，在核对页上看对不对", n))
	}
}
