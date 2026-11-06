package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// runHarness drives connector.run() with the in-memory hub and a controllable
// fake bridge factory. Fully synthetic: no network, no process, no files.
type runHarness struct {
	t                 *testing.T
	c                 *connector
	h                 *memoryHub
	cancel            context.CancelFunc
	done              chan struct{}
	stopOnce          sync.Once
	mu                sync.Mutex
	starts, failFirst int
	attempts          []time.Time
	bridges           []*fakeBridge
}

var persistedSessionData = func() []byte {
	raw, _ := json.Marshal(stored{Session: syntheticSession})
	return raw
}()

func newRunHarness(t *testing.T, persisted []byte, failFirst int) *runHarness {
	t.Helper()
	c := newConnector()
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx, c.hub = ctx, &memoryHub{data: persisted}
	c.argv, c.chats, c.senders = []string{"synthetic-command"}, []string{"chat-test"}, []string{"sender-test"}
	r := &runHarness{t: t, c: c, h: c.hub.(*memoryHub), cancel: cancel, done: make(chan struct{}), failFirst: failFirst}
	c.factory = func(context.Context, []string) (bridge, error) {
		r.mu.Lock()
		r.starts++
		n := r.starts
		r.attempts = append(r.attempts, time.Now())
		fail := n <= r.failFirst
		r.mu.Unlock()
		if fail {
			return nil, errors.New("synthetic companion unavailable")
		}
		b := newFakeBridge()
		r.mu.Lock()
		r.bridges = append(r.bridges, b)
		r.mu.Unlock()
		return b, nil
	}
	t.Cleanup(r.stop)
	go func() { c.run(); close(r.done) }()
	return r
}

func (r *runHarness) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			r.t.Fatal("run loop did not stop after context cancellation")
		}
	})
}
func (r *runHarness) stats() (starts, bridges int, attempts []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, len(r.bridges), slices.Clone(r.attempts)
}
func (r *runHarness) setFailFirst(n int) { r.mu.Lock(); r.failFirst = n; r.mu.Unlock() }
func (r *runHarness) bridgeAttempt(i int) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts[i]
}
func (r *runHarness) bridge(i int) *fakeBridge {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bridges[i]
}

func (b *fakeBridge) setFail(method string)      { b.mu.Lock(); b.fail = method; b.mu.Unlock() }
func (b *fakeBridge) setFailCode(code string)    { b.mu.Lock(); b.failCode = code; b.mu.Unlock() }
func (b *fakeBridge) setCheckState(state string) { b.mu.Lock(); b.check = state; b.mu.Unlock() }
func (b *fakeBridge) openedCredentials() any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openParams == nil {
		return nil
	}
	return b.openParams.(map[string]any)["credentials"]
}

func connectorState(c *connector) string  { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func connectorAttached(c *connector) bool { c.mu.Lock(); defer c.mu.Unlock(); return c.b != nil }
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

// waitOpen waits for auto-open (state ok, one companion, first check issued).
func waitOpen(t *testing.T, r *runHarness) *fakeBridge {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool { return connectorState(r.c) == "ok" },
		"persisted session was not auto-opened")
	if s, n, _ := r.stats(); s != 1 || n != 1 {
		t.Fatalf("auto-open starts=%d bridges=%d, want 1/1", s, n)
	}
	b := r.bridge(0)
	if b.count("session.open") != 1 {
		t.Fatal("auto-open did not call session.open once", b.snapshot())
	}
	waitFor(t, time.Second, func() bool { return b.count("session.check") >= 1 },
		"run loop did not issue session.check")
	return b
}

func assertClosed(t *testing.T, b *fakeBridge) {
	t.Helper()
	select {
	case <-b.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("companion was not closed")
	}
}

// Auto-open uses the persisted credentials and runs session.check after open;
// shutdown closes the attached companion via run()'s deferred detach.
func TestRunAutoOpensPersistedSession(t *testing.T) {
	r := newRunHarness(t, persistedSessionData, 0)
	b := waitOpen(t, r)
	if got, ok := b.openedCredentials().(json.RawMessage); !ok || string(got) != string(syntheticSession) {
		t.Fatal("session.open did not receive the persisted credentials", b.openedCredentials())
	}
	calls := b.snapshot()
	if slices.Index(calls, "session.open") > slices.Index(calls, "session.check") {
		t.Fatal("session.check ran before session.open", calls)
	}
	r.stop()
	assertClosed(t, b)
}

// After a failed factory start the loop waits for its 2s timer (no wake sent).
func TestRunReconnectBackoffFloor(t *testing.T) {
	r := newRunHarness(t, persistedSessionData, 1)
	waitFor(t, 2*time.Second, func() bool { s, _, _ := r.stats(); return s >= 1 },
		"first reconnect attempt never ran")
	first := r.bridgeAttempt(0)
	waitFor(t, 5*time.Second, func() bool { s, _, _ := r.stats(); return s >= 2 },
		"timer-driven retry never ran")
	if got := r.bridgeAttempt(1).Sub(first); got < 1800*time.Millisecond {
		t.Fatalf("reconnect ignored the 2s backoff floor: gap=%v", got)
	}
	waitFor(t, 2*time.Second, func() bool { return connectorState(r.c) == "ok" },
		"connector did not recover after the companion came back")
}

// A wake signal re-enters the backoff loop immediately instead of waiting.
func TestRunWakeReentersBackoffImmediately(t *testing.T) {
	r := newRunHarness(t, persistedSessionData, 2)
	c := r.c
	waitFor(t, 2*time.Second, func() bool { s, _, _ := r.stats(); return s >= 1 },
		"first reconnect attempt never ran")
	first := r.bridgeAttempt(0)
	time.Sleep(200 * time.Millisecond)
	if s, _, _ := r.stats(); s != 1 {
		t.Fatal("loop retried before the backoff floor")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s, _, _ := r.stats(); s >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wake signals did not re-enter the reconnect loop")
		}
		c.signal()
		time.Sleep(2 * time.Millisecond)
	}
	if got := r.bridgeAttempt(2).Sub(first); got >= 2*time.Second {
		t.Fatalf("wake did not bypass the backoff timer: gap=%v", got)
	}
	waitFor(t, 2*time.Second, func() bool { return connectorState(c) == "ok" },
		"connector did not recover after the companion came back")
	if b := r.bridge(0); b.count("session.open") != 1 {
		t.Fatal("recovered companion did not open the session once", b.snapshot())
	}
}

// An explicit sign-out (RPC auth_expired or a "not_signed_in" result)
// expires and detaches without reconnecting.
func TestRunDetachesOnAuthExpiredCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(*fakeBridge)
	}{
		{"rpc auth_expired error", func(b *fakeBridge) { b.setFail("session.check") }},
		{"not_signed_in result state", func(b *fakeBridge) { b.setCheckState("not_signed_in") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunHarness(t, persistedSessionData, 0)
			b := waitOpen(t, r)
			tc.mark(b)
			r.c.signal()
			waitFor(t, 2*time.Second, func() bool { return connectorState(r.c) == "not_signed_in" },
				"failed session.check did not expire the session")
			if connectorAttached(r.c) {
				t.Fatal("companion stayed attached after a failed session.check")
			}
			assertClosed(t, b)
			time.Sleep(100 * time.Millisecond)
			if s, n, _ := r.stats(); s != 1 || n != 1 {
				t.Fatalf("loop reopened without a new sign-in: starts=%d bridges=%d", s, n)
			}
		})
	}
}

// Non-auth check failures set "error", detach, then auto-reconnect. The
// companion reports a probe it could not complete two ways: an "unavailable"
// RPC error frame, or a successful reply whose state is "error" (the shape
// bridge.py returns when validate() raises or times out). Both must behave
// the same; the "error" result must not be mistaken for "not_signed_in".
func TestRunReconnectsAfterTransientCheckError(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(*fakeBridge)
	}{
		{"rpc unavailable error", func(b *fakeBridge) {
			b.setFailCode("unavailable")
			b.setFail("session.check")
		}},
		{"error result state", func(b *fakeBridge) {
			b.setCheckState("error")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunHarness(t, persistedSessionData, 0)
			b0 := waitOpen(t, r)
			r.setFailFirst(100) // freeze the detached "error" state for observation
			tc.mark(b0)
			r.c.signal()
			waitFor(t, 2*time.Second, func() bool { return connectorState(r.c) == "error" },
				"transient session.check failure did not set the error state")
			if connectorAttached(r.c) {
				t.Fatal("companion stayed attached after a transient session.check failure")
			}
			assertClosed(t, b0)
			r.setFailFirst(0)
			r.c.signal()
			waitFor(t, 2*time.Second, func() bool { return connectorState(r.c) == "ok" },
				"loop did not reconnect after a transient failure")
			if s, n, _ := r.stats(); n < 2 {
				t.Fatalf("loop did not open a replacement companion: starts=%d bridges=%d", s, n)
			}
			if b1 := r.bridge(1); b1.count("session.open") != 1 {
				t.Fatal("replacement companion did not open the session once", b1.snapshot())
			}
		})
	}
}

// No persisted session: never starts a companion; still exits on cancel.
func TestRunIdlesAndShutsDownWithoutSession(t *testing.T) {
	r := newRunHarness(t, nil, 0)
	c, h := r.c, r.h
	waitFor(t, time.Second, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.targets) > 0
	}, "run loop never loaded the persisted secret")
	if connectorState(c) != "not_signed_in" {
		t.Fatal("connector without a session left the not_signed_in state")
	}
	if s, n, _ := r.stats(); s != 0 || n != 0 {
		t.Fatalf("companion started without a session: starts=%d bridges=%d", s, n)
	}
	r.stop()
	if s, n, _ := r.stats(); s != 0 || n != 0 {
		t.Fatalf("shutdown started a companion: starts=%d bridges=%d", s, n)
	}
}

// A failure that detaches the companion (a transient session.check result or
// a failed send) reconnects on the 2s backoff floor by itself. Nothing calls
// signal() after the failure: the loop must not sleep the minute-long probe
// interval it uses while a companion is attached.
func TestRunReconnectsOnBackoffAfterDetach(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*runHarness, *fakeBridge)
	}{
		{"error check state", func(r *runHarness, b *fakeBridge) {
			b.setCheckState("error")
			r.c.signal() // run the next probe now instead of in a minute
		}},
		{"failed send", func(r *runHarness, b *fakeBridge) {
			b.setFailCode("unavailable")
			b.setFail("session.send")
			if _, err := invoke(r.c, "actions.do", sendParams("backoff-test", "Synthetic text")); err == nil {
				t.Error("failed send reported success")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunHarness(t, persistedSessionData, 0)
			b0 := waitOpen(t, r)
			tc.fail(r, b0)
			assertClosed(t, b0)
			waitFor(t, 5*time.Second, func() bool { return connectorState(r.c) == "ok" && connectorAttached(r.c) },
				"loop did not reconnect within the backoff after a detach")
			if s, n, _ := r.stats(); n != 2 {
				t.Fatalf("starts=%d bridges=%d, want one replacement companion", s, n)
			}
		})
	}
}

// probeErrorBridge reads one allowlisted message during session.check and
// then answers state "error", as bridge.py does when auth.validate times out.
type probeErrorBridge struct{ *fakeBridge }

func (b *probeErrorBridge) Call(ctx context.Context, m string, p, out any) error {
	if m == "session.check" {
		b.events <- bridgeEvent{Event: "message", Message: message{ID: "during-probe", Chat: "chat-test",
			Sender: "sender-test", Text: "Synthetic message during the probe", At: time.Now()}}
		raw, _ := json.Marshal(map[string]string{"state": "error"})
		return json.Unmarshal(raw, out)
	}
	return b.fakeBridge.Call(ctx, m, p, out)
}

// The message watch takes while the failed probe holds c.op still reaches
// the inbox after the reconnect (#285).
func TestRunKeepsMessageReadDuringFailedProbe(t *testing.T) {
	c := newConnector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.ctx, c.hub = ctx, &memoryHub{data: persistedSessionData}
	c.argv, c.chats, c.senders = []string{"synthetic-command"}, []string{"chat-test"}, []string{"sender-test"}
	var mu sync.Mutex
	starts := 0
	c.factory = func(context.Context, []string) (bridge, error) {
		mu.Lock()
		defer mu.Unlock()
		if starts++; starts == 1 {
			return &probeErrorBridge{newFakeBridge()}, nil
		}
		return newFakeBridge(), nil
	}
	done := make(chan struct{})
	go func() { c.run(); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 3*time.Second, func() bool { return connectorState(c) == "error" && !connectorAttached(c) },
		"the failed probe did not detach the companion")
	c.signal()
	waitFor(t, 5*time.Second, func() bool { return connectorState(c) == "ok" && connectorAttached(c) },
		"connector did not reconnect after the transient probe failure")
	time.Sleep(200 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data.Seen) != 1 || len(c.data.Inbox) != 1 {
		t.Fatalf("message read during a failed probe was dropped: seen=%d inbox=%d", len(c.data.Seen), len(c.data.Inbox))
	}
}
