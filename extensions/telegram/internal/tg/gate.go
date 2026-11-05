package telegram

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"
)

// The connector's one way to Telegram (gotd's middleware, client.go: every request of the account goes through it,
// the sign-in's too): one request at a time, 0.5–1 s between the end of one and the start of the next (as the
// user's own Telethon skill does), and none at all until a flood wait Telegram asked for is over, plus 0.5–1 s.
// It never retries: a flood wait ends the round that met it (the archive's, the inbox's) or answers the tool call,
// and what comes after it is refused without a request (heldError) until the wait is over.

var (
	paceMin = 500 * time.Millisecond
	paceMax = time.Second
)

type gate struct {
	slot chan struct{} // the one request in flight

	mu        sync.Mutex
	last      time.Time     // when the previous request ended
	until     time.Time     // no request before this: the latest flood wait and its jitter
	floodAt   time.Time     // when the latest flood wait came
	floodWait time.Duration // how long Telegram asked for

	// Replaced by tests: the clock, a wait, the gap before a request, what a flood wait is told to.
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	pace    func() time.Duration
	flooded func(time.Duration)
}

func newGate() *gate {
	return &gate{slot: make(chan struct{}, 1), now: time.Now, sleep: sleepCtx, pace: jitter, flooded: func(time.Duration) {}}
}

// jitter is a gap between paceMin and paceMax.
func jitter() time.Duration { return paceMin + rand.N(paceMax-paceMin) }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type inGateKey struct{}

// inGate says whether ctx is a request's that holds the gate.
func inGate(ctx context.Context) bool { return ctx.Value(inGateKey{}) != nil }

// heldError is a request the gate did not send: a flood wait is not over.
type heldError struct{ left time.Duration }

func (e heldError) Error() string {
	return fmt.Sprintf("Telegram 限流中，%d 秒后再试", seconds(e.left))
}

// seconds is d in whole seconds, rounded up: a wait is never said shorter than it is.
func seconds(d time.Duration) int { return int((d + time.Second - 1) / time.Second) }

// do sends one request (call) when the gate lets it: after the one in flight, the gap, and any flood wait.
func (g *gate) do(ctx context.Context, call func(context.Context) error) error {
	entered := g.now()
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g.slot }()
	if left := g.left(); left > 0 {
		return heldError{left}
	}
	g.mu.Lock()
	wait := g.last.Add(g.pace()).Sub(g.now())
	g.mu.Unlock()
	if err := g.sleep(ctx, wait); err != nil {
		return err
	}
	started := g.now()
	err := call(context.WithValue(ctx, inGateKey{}, true))
	g.mu.Lock()
	now := g.now()
	g.last = now
	d, flood := tgerr.AsFloodWait(err)
	if flood {
		g.floodAt, g.floodWait, g.until = now, d, now.Add(d+g.pace())
	}
	g.mu.Unlock()
	traceOf(ctx).update(func(t *fetchTrace) {
		t.requests++
		t.queued += started.Sub(entered)
		t.call += now.Sub(started)
	})
	if flood {
		g.flooded(d)
	}
	return err
}

// left is how long until requests go again (0 or less: they go).
func (g *gate) left() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.until.Sub(g.now())
}

// flood is the latest flood wait: when it came, how long Telegram asked for, when requests go again; zero ones when
// there has been none.
func (g *gate) flood() (at time.Time, asked time.Duration, until time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.floodAt, g.floodWait, g.until
}
