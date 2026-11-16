package curate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// sameName is the character bigram Jaccard at which two short texts (a rule's, a name) read as one (rules.go).
const sameName = 0.6

// RunSynth consolidates the entries as of day: what is left of the old judgement layer is migrated, every action
// entry not filed yet is recalled, judged and filed (consolidate.go), the counts are taken again and the statuses
// settled. The daily pass does the same for its own day; this catches up after a rerun or a pass without a model.
func (c *Curator) RunSynth(ctx context.Context, day string, dry bool) (*store.Run, error) {
	c.St.Lock()
	defer c.St.Unlock()
	run := &store.Run{ID: fmt.Sprintf("synth-%s-%d", day, c.now().Unix()), Kind: "synth", Period: day,
		Started: c.now().UTC().Format(time.RFC3339), DryRun: dry, Counts: map[string]int{}}
	var m model.Model
	var metered *model.Metered
	if c.Model != nil {
		metered = &model.Metered{Model: c.Model, Usage: c.St, Day: day}
		m = metered
	}
	if rep, err := c.Migrate(); err != nil {
		run.Notes = append(run.Notes, "迁移旧判断层："+err.Error())
	} else {
		run.Counts["migrated_new"] = rep.New
	}
	if m == nil {
		run.Notes = append(run.Notes, "模型不可用：只重新计数，没有归档新的条目")
	}
	if err := c.Consolidate(ctx, m, day, run); err != nil {
		return nil, err
	}
	if metered != nil {
		run.Counts["model_calls"] = metered.Calls
	}
	for _, u := range c.St.S.Usage {
		if u.Day == day && (u.Step == "consolidate" || u.Step == "abstract" || u.Step == "review") {
			run.Tokens += u.In + u.Out
		}
	}
	run.Finished = c.now().UTC().Format(time.RFC3339)
	run.Report = c.synthReport(day, run)
	c.St.S.Runs = append(c.St.S.Runs, run)
	if dry {
		return run, nil
	}
	err := c.St.Save()
	c.Publish()
	return run, err
}

// weeklyEntries is the weekly consolidation of the entries: catch up the filing, lift what passes the abstract gate,
// put the exits' entries to the impact question, clean the relations, and settle the statuses as of the week's end.
func (c *Curator) weeklyEntries(ctx context.Context, m model.Model, today string, run *store.Run) {
	if rep, err := c.Migrate(); err != nil {
		run.Notes = append(run.Notes, "迁移旧判断层："+err.Error())
	} else {
		run.Counts["migrated_new"] = rep.New
	}
	if err := c.Consolidate(ctx, m, today, run); err != nil {
		run.Notes = append(run.Notes, "整理没做完："+err.Error())
		return
	}
	if m == nil {
		run.Notes = append(run.Notes, "模型不可用：本周没有升抽象层，也没有做影响复核")
		return
	}
	if err := c.mergeClasses(ctx, m, run); err != nil {
		run.Notes = append(run.Notes, "类的合并没做完："+err.Error())
	}
	c.settleAll(today, run)
	if err := c.liftAbstracts(ctx, m, run); err != nil {
		run.Notes = append(run.Notes, "抽象层没做完："+err.Error())
	}
	c.settleAll(today, run)
	if err := c.reviewImpact(ctx, m, run); err != nil {
		run.Notes = append(run.Notes, "影响复核没做完："+err.Error())
	}
	c.settleAll(today, run)
}

// Recount takes the entries' counts again and settles their statuses as of today (after the user changed something).
func (c *Curator) Recount() { c.recount() }

func (c *Curator) recount() {
	c.settleAll(c.today(), &store.Run{Counts: map[string]int{}})
}

func (c *Curator) synthReport(day string, run *store.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 条目整理 %s\n\n", day)
	fmt.Fprintf(&b, "待归档 %d 条：归到已有 %d、新的一类 %d、反例 %d、失效 %d、单独一条 %d、没答到的留到下次 %d。模型调用 %d 次。\n",
		run.Counts["consolidate_pending"], run.Counts["consolidate_filed"], run.Counts["consolidate_new_class"], run.Counts["consolidate_counter"],
		run.Counts["consolidate_replaced"], run.Counts["consolidate_standalone"], run.Counts["consolidate_unanswered"], run.Counts["model_calls"])
	c.entriesReport(&b, run)
	for _, n := range run.Notes {
		fmt.Fprintf(&b, "\n- %s", n)
	}
	b.WriteString("\n")
	return b.String()
}
