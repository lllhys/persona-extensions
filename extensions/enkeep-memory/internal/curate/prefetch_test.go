package curate

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

// locked makes the stub safe to call from several goroutines (it counts calls in a map) and counts what it was asked.
type locked struct {
	mu    sync.Mutex
	m     *stub.Model
	steps map[string]int
}

func (l *locked) Complete(ctx context.Context, r model.Request) (model.Response, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.steps == nil {
		l.steps = map[string]int{}
	}
	l.steps[r.Step]++
	return l.m.Complete(ctx, r)
}

// outcome is what a run produced, in a comparable form.
func outcome(t *testing.T, c *Curator) string {
	t.Helper()
	s := c.St.S
	for _, d := range s.Digests {
		slices.Sort(d.Items) // ingest walks the three classes in map order: the order of a digest's items is not stable
	}
	b, err := json.Marshal(map[string]any{"items": s.Items, "digests": s.Digests, "entries": s.Entries,
		"rejections": s.Rejections, "funnel": s.Funnel, "proposals": len(s.Proposals), "graph": s.Graph})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWorkersSameResultAsSequential: with Workers=4 the daily pass makes its calls concurrently up front and still
// produces exactly the items, digests, memory and graph of the sequential pass; every call the model answered is
// metered once (usage calls = model calls), and the digests are not asked twice.
func TestWorkersSameResultAsSequential(t *testing.T) {
	ctx := context.Background()
	prefetchedCalls := 0
	run := func(workers int) (*Curator, *locked, int) {
		m := &locked{m: &stub.Model{}}
		c, _ := newCurator(t, m, "review")
		c.Workers = workers
		c.Logf = func(format string, args ...any) {
			if strings.HasPrefix(format, "prefetched ") {
				prefetchedCalls += args[0].(int)
			}
		}
		calls := 0
		for _, day := range []string{"2026-11-03", "2026-11-04"} {
			r, err := c.RunDaily(ctx, day, false)
			if err != nil {
				t.Fatal(err)
			}
			calls += r.Counts["model_calls"]
		}
		return c, m, calls
	}
	seq, seqM, seqCalls := run(1)
	if prefetchedCalls != 0 {
		t.Errorf("Workers=1 prefetched %d calls", prefetchedCalls)
	}
	par, parM, parCalls := run(4)
	if prefetchedCalls < parM.steps["daily"] {
		t.Errorf("Workers=4 prefetched %d calls, fewer than the %d digests", prefetchedCalls, parM.steps["daily"])
	}
	if len(seq.St.S.Items) == 0 || len(seq.St.S.Digests) == 0 {
		t.Fatalf("the sample produced nothing: %d items, %d digests", len(seq.St.S.Items), len(seq.St.S.Digests))
	}
	if a, b := outcome(t, seq), outcome(t, par); a != b {
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		t.Errorf("Workers=4 differs from Workers=1 at byte %d:\n…%s\n…%s", i, a[max(0, i-200):min(len(a), i+200)], b[max(0, i-200):min(len(b), i+200)])
	}
	if seqM.steps["daily"] != parM.steps["daily"] || seqM.steps["triage"] != parM.steps["triage"] {
		t.Errorf("calls: sequential %v, concurrent %v", seqM.steps, parM.steps)
	}
	total := func(m *locked) int {
		n := 0
		for _, v := range m.steps {
			n += v
		}
		return n
	}
	usage := func(c *Curator) int {
		n := 0
		for _, u := range c.St.S.Usage {
			n += u.Calls
		}
		return n
	}
	if seqCalls != total(seqM) || parCalls != total(parM) || usage(seq) != seqCalls || usage(par) != parCalls {
		t.Errorf("metering: sequential %d calls (model %d, usage %d), concurrent %d (model %d, usage %d)",
			seqCalls, total(seqM), usage(seq), parCalls, total(parM), usage(par))
	}
	// A rerun of an unchanged day asks nothing, concurrently or not.
	before := total(parM)
	if _, err := par.RunDaily(ctx, "2026-11-04", false); err != nil {
		t.Fatal(err)
	}
	if after := total(parM); after != before {
		t.Errorf("rerun made %d calls", after-before)
	}
}
