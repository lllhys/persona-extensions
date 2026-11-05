package telegram

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
)

// fakeClock is a gate's time: a wait moves it on; a request takes 200 ms of it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(max(d, 0))
}

func clocked() (*gate, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g := newGate()
	g.now = clk.now
	g.sleep = func(ctx context.Context, d time.Duration) error { clk.add(d); return ctx.Err() }
	return g, clk
}

// The gate, on its own clock: one request at a time, 0.5–1 s from the end of one to the start of the next however
// many ask at once; after a flood wait, nothing until Telegram's time and 0.5–1 s more, and no retry.
func TestGatePaces(t *testing.T) {
	g, clk := clocked()
	type span struct{ start, end time.Time }
	var mu sync.Mutex
	var spans []span
	inFlight, most := 0, 0
	request := func(context.Context) error {
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		start := clk.now()
		mu.Unlock()
		clk.add(200 * time.Millisecond)
		mu.Lock()
		inFlight--
		spans = append(spans, span{start, clk.now()})
		mu.Unlock()
		return nil
	}
	var wg sync.WaitGroup
	for range 20 { // the archive, the inbox and the tools all at once
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.do(context.Background(), request); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most != 1 || len(spans) != 20 {
		t.Fatalf("%d in flight at once, %d sent", most, len(spans))
	}
	for i := 1; i < len(spans); i++ {
		if gap := spans[i].start.Sub(spans[i-1].end); gap < paceMin || gap > paceMax {
			t.Fatalf("gap %d: %v", i, gap)
		}
	}

	// A flood wait: the request that met it is not tried again, and nothing goes until it is over.
	sent := 0
	flood := func(context.Context) error { sent++; return tgerr.New(420, "FLOOD_WAIT_30") }
	if err := g.do(context.Background(), flood); err == nil {
		t.Fatal("the flood wait was not returned")
	}
	at := clk.now()
	_, asked, until := g.flood()
	if asked != 30*time.Second || until.Sub(at) < 30*time.Second+paceMin || until.Sub(at) > 30*time.Second+paceMax {
		t.Fatalf("asked %v, until %v after", asked, until.Sub(at))
	}
	clk.add(29 * time.Second)
	err := g.do(context.Background(), flood)
	if held, ok := err.(heldError); !ok || sent != 1 || seconds(held.left) < 1 || seconds(held.left) > 2 {
		t.Fatalf("during the wait: %v, %d sent", err, sent)
	}
	clk.add(2 * time.Second)
	if err := g.do(context.Background(), func(context.Context) error { sent++; return nil }); err != nil || sent != 2 {
		t.Fatalf("after the wait: %v, %d sent", err, sent)
	}
}

// Through the connector, on the real clock and the real pace: the archive, the inbox and two tool calls at once never
// have more than one request in flight, and every request starts at least 0.5 s after the one before ended.
func TestOneRequestAtATime(t *testing.T) {
	if testing.Short() {
		t.Skip("takes a few seconds")
	}
	c, f, ctx := archiveAccount(t)
	f.mu.Lock()
	f.dialogs[1].Unread, f.dialogs[1].TopFrom = 1, "示例用户"
	f.latency = 20 * time.Millisecond
	f.mu.Unlock()
	paceMin, paceMax = 500*time.Millisecond, time.Second // what the connector runs with (archiveAccount made it quick)
	before := len(f.requests())
	var wg sync.WaitGroup
	for _, run := range []func(){
		func() { _, _, _, _ = c.Changes(ctx, "", 20) },
		func() { c.refreshInbox(ctx) },
		func() { _, _ = c.Call(ctx, "telegram_chats", json.RawMessage(`{}`)) },
		func() { _, _ = c.Call(ctx, "telegram_search", json.RawMessage(`{"query":"示例"}`)) },
	} {
		wg.Add(1)
		go func() { defer wg.Done(); run() }()
	}
	wg.Wait()
	sent := f.requests()[before:]
	f.mu.Lock()
	most := f.most
	f.mu.Unlock()
	if most != 1 || len(sent) < 6 {
		t.Fatalf("%d in flight at once; %d sent: %+v", most, len(sent), sent)
	}
	for i := 1; i < len(sent); i++ {
		if gap := sent[i].start.Sub(sent[i-1].end); gap < paceMin {
			t.Fatalf("%s started %v after %s", sent[i].method, gap, sent[i-1].method)
		}
	}
}
