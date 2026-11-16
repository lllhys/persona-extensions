package curate

import (
	"context"
	"errors"
	"fmt"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/convsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona/sdk/go/wire"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

// flaky is the stub model, or the hub saying the model is unavailable while down is set.
type flaky struct {
	stub.Model
	down bool
}

func (f *flaky) Complete(ctx context.Context, r model.Request) (model.Response, error) {
	if f.down {
		return model.Response{}, fmt.Errorf("%w: model.complete: 并发已满，5 分钟后重试", model.ErrUnavailable)
	}
	return f.Model.Complete(ctx, r)
}

// A day the model is unavailable to is not done: the replay stays on it, the run is not recorded as finished, the
// pause says why and when the next try is, with a growing wait; once the model is back the replay goes on from that
// day and the pause is gone.
func TestModelDownKeepsTheDay(t *testing.T) {
	m := &flaky{down: true}
	c, _ := newCurator(t, m, "shadow")
	c.Cfg.BackfillDays = 3
	c.StartBackfill("2026-11-05") // 11-02 … 11-04
	ctx := context.Background()

	// 11-02 has nothing to read: it needs no model and is done.
	if done, err := c.BackfillStep(ctx); done || err != nil {
		t.Fatalf("empty day: done %v err %v", done, err)
	}
	done, err := c.BackfillStep(ctx)
	if done || !errors.Is(err, ErrModelDown) {
		t.Fatalf("step with the model down: done %v err %v, want ErrModelDown", done, err)
	}
	if c.St.S.Backfill.Next != "2026-11-03" {
		t.Fatalf("the replay moved on: next %s", c.St.S.Backfill.Next)
	}
	if got := strings.Join(dailyRuns(c.St.S), ","); got != "2026-11-02" {
		t.Fatalf("a day without the model counted as run: %v", got)
	}
	p := c.St.S.ModelPause
	if p == nil || p.Fails != 1 || p.Day != "2026-11-03" || !strings.Contains(p.Reason, "并发已满") {
		t.Fatalf("pause: %+v", p)
	}
	first, wait := c.ModelWait(c.now())
	if !wait {
		t.Fatal("no wait after a failure")
	}
	// Saved: a restart keeps the day and the pause.
	st, err := store.Open(c.St.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.S.Backfill.Next != "2026-11-03" || st.S.ModelPause == nil {
		t.Fatalf("saved: backfill %+v pause %+v", st.S.Backfill, st.S.ModelPause)
	}

	// A second failure waits longer.
	if _, err := c.BackfillStep(ctx); !errors.Is(err, ErrModelDown) {
		t.Fatalf("second step: %v", err)
	}
	second, _ := c.ModelWait(c.now())
	if c.St.S.ModelPause.Fails != 2 || !second.After(first) {
		t.Fatalf("backoff: fails %d, next %v after %v", c.St.S.ModelPause.Fails, second, first)
	}

	// The model is back: the same day runs, the replay goes on, the pause is cleared.
	m.down = false
	if err := c.RunBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dailyRuns(c.St.S), ","); got != "2026-11-02,2026-11-03,2026-11-04" {
		t.Errorf("daily runs after recovery: %s", got)
	}
	if c.St.S.ModelPause != nil || !c.BackfillDone() {
		t.Errorf("after recovery: pause %+v, done %v", c.St.S.ModelPause, c.BackfillDone())
	}
}

func TestPauseBackoff(t *testing.T) {
	for fails, want := range map[int]time.Duration{1: 10 * time.Minute, 2: 20 * time.Minute, 3: 40 * time.Minute, 4: 80 * time.Minute, 5: 2 * time.Hour, 9: 2 * time.Hour} {
		if got := pauseBackoff(fails); got != want {
			t.Errorf("pauseBackoff(%d) = %v, want %v", fails, got, want)
		}
	}
}

// awaySrc is the sample source whose day reads find the assistant's computer away.
type awaySrc struct{ kbsrc.Source }

func (a awaySrc) Recent(ctx context.Context, since, until time.Time) ([]wire.KBEntry, error) {
	return nil, fmt.Errorf("kb.recent: %w: the assistant's computer is not connected", convsrc.ErrAway)
}

// The assistant's computer away: the day is not done either, the pause says what was away, and it is tried again
// after the wait rather than only at the next start.
func TestAssistantAwayKeepsTheDay(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Src = awaySrc{c.Src}
	_, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if !errors.Is(err, ErrModelDown) {
		t.Fatalf("err %v, want ErrModelDown", err)
	}
	if got := strings.Join(dailyRuns(c.St.S), ","); got != "" {
		t.Fatalf("a day not read counted as run: %s", got)
	}
	p := c.St.S.ModelPause
	if p == nil || p.What != "assistant" || p.Day != "2026-11-03" || !strings.Contains(p.Reason, "not connected") {
		t.Fatalf("pause: %+v", p)
	}
	if _, wait := c.ModelWait(c.now()); !wait {
		t.Fatal("no wait before the next try")
	}
}
