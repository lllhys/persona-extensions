package curate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
)

// A model declines a whole call for what is often one or two lines in it. The same input is declined again (the hub
// says so), so a refused batch is not resent as it is: it is split in two halves, in line order, and each half is
// tried on its own, down to minRefusedBatch lines or maxRefusalDepth splits. Only a batch that is still refused at
// that size is skipped. Every refused batch is kept on the run with the hub's reason and what became of it, for the
// report and the health page.
const (
	minRefusedBatch = 4
	maxRefusalDepth = 3 // a unit ends up in at most 8 batches: at most 15 calls for one unit
)

// inBatches calls call with d's lines, and with smaller batches of them when the model declines. part names the
// batch ("" for all of d's lines; "12-40" for lines 12 to 40) so that the batches' results do not overwrite each
// other. The error is call's other errors (the model unavailable, the context ended); a refusal is not one.
func (c *Curator) inBatches(step string, u *Unit, d triage.Decision, run *store.Run, call func(d triage.Decision, part string) error) error {
	return c.batch(step, u, d, "", 0, run, call)
}

func (c *Curator) batch(step string, u *Unit, d triage.Decision, part string, depth int, run *store.Run, call func(triage.Decision, string) error) error {
	err := call(d, part)
	if !errors.Is(err, model.ErrRefused) {
		return err
	}
	lines := shownLines(d)
	r := c.refusal(step, u, lines, err)
	run.Counts[step+"_refused"]++
	if len(lines) <= minRefusedBatch || depth >= maxRefusalDepth {
		r.Outcome = store.RefusalSkipped
		run.Refusals = append(run.Refusals, r)
		run.Counts[step+"_refused_skipped"]++
		run.Counts[step+"_refused_lines"] += len(lines)
		c.logf("%s %s %s lines %d-%d refused, skipped: %s", step, u.Ref, u.Day, r.From, r.To, r.Reason)
		return nil
	}
	r.Outcome = store.RefusalSplit
	run.Refusals = append(run.Refusals, r)
	half := len(lines) / 2
	for _, ns := range [][]int{lines[:half], lines[half:]} {
		pd := d
		pd.Shown = map[int]bool{}
		for _, n := range ns {
			pd.Shown[n] = true
		}
		if err := c.batch(step, u, pd, fmt.Sprintf("%d-%d", ns[0], ns[len(ns)-1]), depth+1, run, call); err != nil {
			return err
		}
	}
	return nil
}

func (c *Curator) refusal(step string, u *Unit, lines []int, err error) store.Refusal {
	r := store.Refusal{Step: step, Unit: u.Ref, Name: u.Meta.Name, Day: u.Day, Lines: len(lines), Reason: refusalReason(err)}
	if len(lines) > 0 {
		r.From, r.To = lines[0], lines[len(lines)-1]
	}
	return r
}

// shownLines are the line numbers of d's batch, in order.
func shownLines(d triage.Decision) []int {
	var out []int
	for n, ok := range d.Shown {
		if ok {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// refusalReason is the hub's message without the prefixes every one of them has.
func refusalReason(err error) string {
	msg := strings.TrimPrefix(err.Error(), model.ErrRefused.Error())
	msg = strings.TrimSpace(strings.TrimPrefix(msg, ":"))
	msg = strings.TrimSpace(strings.TrimPrefix(msg, "model.complete:"))
	if msg == "" {
		return "模型拒答"
	}
	return msg
}

// RefusalOutcome is the outcome in words, for the report and the health page.
func RefusalOutcome(o string) string {
	switch o {
	case store.RefusalSplit:
		return "拆小重试"
	case store.RefusalSkipped:
		return "跳过（这几行没读）"
	case store.RefusalWeekly:
		return "只进周级"
	}
	return o
}

var stepLabel = map[string]string{"triage": "分拣", "daily": "摘要", "memory": "记忆"}

// StepLabel is a step's name in words.
func StepLabel(s string) string {
	if l := stepLabel[s]; l != "" {
		return l
	}
	return s
}

func lineNumbers(ls []norm.Line) []int {
	out := make([]int, len(ls))
	for i, l := range ls {
		out[i] = l.N
	}
	return out
}
