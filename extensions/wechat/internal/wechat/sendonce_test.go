package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

// The hub's retry of a channel.send still running (it timed out waiting) waits for that send: one message, not two.
func TestSentRefsWaitForRunningSend(t *testing.T) {
	var r sentRefs
	var sends atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	send := func() (wire.ChannelSendResult, error) {
		if sends.Add(1) == 1 {
			close(started)
			<-release
		}
		return wire.ChannelSendResult{MessageID: "m1"}, nil
	}
	type result struct {
		res wire.ChannelSendResult
		err error
	}
	results := make(chan result, 2)
	go func() { res, err := r.do("ref1", send); results <- result{res, err} }()
	<-started
	go func() { res, err := r.do("ref1", send); results <- result{res, err} }()
	// Give the retry time to reach the running send: it must wait for it, not send again or answer early.
	time.Sleep(50 * time.Millisecond)
	if n := sends.Load(); n != 1 {
		t.Fatalf("the retry sent again while the first send ran: %d send(s)", n)
	}
	select {
	case res := <-results:
		t.Fatalf("answered %+v before the first send finished", res)
	default:
	}
	close(release)
	for range 2 {
		if got := <-results; got.err != nil || got.res.MessageID != "m1" {
			t.Fatalf("%+v %v", got.res, got.err)
		}
	}
	if n := sends.Load(); n != 1 {
		t.Fatalf("sent %d times", n)
	}
	if res, err := r.do("ref1", send); err != nil || res.MessageID != "m1" || sends.Load() != 1 {
		t.Fatalf("again later: %+v %v, %d send(s)", res, err, sends.Load())
	}
}

// A failed send is forgotten: the same ref may be sent again, and once it went it is not sent a third time; only the
// latest doneMost are remembered.
func TestSentRefsRetryAfterFailure(t *testing.T) {
	var r sentRefs
	var sends atomic.Int32
	send := func() (wire.ChannelSendResult, error) {
		if sends.Add(1) == 1 {
			return wire.ChannelSendResult{}, errors.New("upstream timed out")
		}
		return wire.ChannelSendResult{MessageID: "m2"}, nil
	}
	if _, err := r.do("ref2", send); err == nil {
		t.Fatal("the first send did not fail")
	}
	if len(r.sent) != 0 || len(r.order) != 0 {
		t.Fatalf("a failed send is remembered: %v %v", r.sent, r.order)
	}
	if res, err := r.do("ref2", send); err != nil || res.MessageID != "m2" {
		t.Fatalf("retry: %+v %v", res, err)
	}
	if res, err := r.do("ref2", send); err != nil || res.MessageID != "m2" || sends.Load() != 2 {
		t.Fatalf("again later: %+v %v, %d send(s)", res, err, sends.Load())
	}
	later := 0 // sends of the later refs
	sendLater := func() (wire.ChannelSendResult, error) { later++; return wire.ChannelSendResult{}, nil }
	for i := range doneMost {
		_, _ = r.do("later-"+strconv.Itoa(i), sendLater)
	}
	if len(r.sent) > doneMost || len(r.order) > doneMost {
		t.Fatalf("remembers %d sends", len(r.sent))
	}
	// The oldest (ref2) is forgotten and goes again; the next oldest and the newest are still remembered.
	_, _ = r.do("later-0", sendLater)
	if later != doneMost {
		t.Fatal("the second oldest ref was sent again: more than the oldest was forgotten")
	}
	_, _ = r.do("ref2", send)
	if sends.Load() != 3 {
		t.Fatalf("the oldest ref was not forgotten: %d send(s)", sends.Load())
	}
	_, _ = r.do("later-"+strconv.Itoa(doneMost-1), sendLater)
	if later != doneMost {
		t.Fatal("the newest ref was sent again")
	}
}

// A retry waiting on a send that then fails gets that failure, and the ref is free again as it wakes: the failed ref
// is forgotten before its waiters wake, so the retry's own retry right after sends.
func TestSentRefsWaiterOfAFailedSend(t *testing.T) {
	for wait := 50 * time.Millisecond; wait <= 2*time.Second; wait *= 2 {
		if waiterOfAFailedSend(t, wait) {
			return
		}
	}
	t.Fatal("the retry never reached the running send (or a waiter of a failed send sent the ref itself)")
}

// waiterOfAFailedSend gives the retry wait to reach the running send; false if it came only once that send was over
// (it then sent itself: nothing to see).
func waiterOfAFailedSend(t *testing.T, wait time.Duration) bool {
	t.Helper()
	var r sentRefs
	var sends atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	send := func() (wire.ChannelSendResult, error) {
		if sends.Add(1) == 1 {
			close(started)
			<-release
			return wire.ChannelSendResult{}, errors.New("upstream timed out")
		}
		return wire.ChannelSendResult{MessageID: "m3"}, nil
	}
	first := make(chan error, 1)
	go func() { _, err := r.do("ref3", send); first <- err }()
	<-started
	type woke struct {
		err   error
		sends int32 // sends made by the time it returned
	}
	waited := make(chan woke, 1)
	type result struct {
		res wire.ChannelSendResult
		err error
	}
	again := make(chan result, 1)
	go func() {
		_, err := r.do("ref3", send)
		waited <- woke{err, sends.Load()}
		res, err := r.do("ref3", send)
		again <- result{res, err}
	}()
	time.Sleep(wait)
	// The failed send forgets its ref under the lock: while the test holds it, no waiter may wake.
	r.mu.Lock()
	close(release)
	select {
	case <-waited:
		r.mu.Unlock()
		t.Fatal("the retry woke before the failed ref was forgotten")
	case <-time.After(100 * time.Millisecond):
	}
	r.mu.Unlock()
	if err := <-first; err == nil {
		t.Fatal("the first send did not fail")
	}
	if w := <-waited; w.err == nil {
		if w.sends == 1 {
			t.Fatal("the waiter of a failed send was told it went")
		}
		return false
	}
	if a := <-again; a.err != nil || a.res.MessageID != "m3" || sends.Load() != 2 {
		t.Fatalf("the retry right after waking: %+v %v, %d send(s)", a.res, a.err, sends.Load())
	}
	return true
}

// Through the program: the same ref sent twice, as a hub that timed out sends it again, goes to iLink once, since
// program.channel sends through sentRefs by the ref (the calls may or may not overlap: TestSentRefsWaitForRunningSend
// waits); the next answer, another ref with the same text, goes too.
func TestProgramSendsARefOnce(t *testing.T) {
	r := newRig(t, true)
	ctx := context.Background()
	r.ilink.say(owner, "在吗")
	eventually(t, "the message at the hub", func() bool { return len(r.hub.got()) == 1 })
	eventually(t, "serving", func() bool { return r.bot.Status(ctx).State == wire.ConnectorStateOk })
	r.ilink.mu.Lock()
	r.ilink.sent, r.ilink.clientIDs = nil, nil
	r.ilink.mu.Unlock()
	p := &program{bot: r.bot}
	params, _ := json.Marshal(wire.ChannelSendParams{Contribution: ID, Ref: "run-2:1", Text: "一次", Kind: wire.ChannelSendKindAnswer})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = p.channel(ctx, "send", params) }()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("%v %v", errs[0], errs[1])
	}
	r.ilink.mu.Lock()
	calls, n := len(r.ilink.clientIDs), len(r.ilink.sent)
	r.ilink.mu.Unlock()
	if calls != 1 || n != 1 { // iLink would keep a repeated client_id once anyway: the program must not even ask twice
		t.Fatalf("%d sendmessage calls, the owner got %d copies", calls, n)
	}
	params, _ = json.Marshal(wire.ChannelSendParams{Contribution: ID, Ref: "run-3:1", Text: "一次", Kind: wire.ChannelSendKindAnswer})
	if _, err := p.channel(ctx, "send", params); err != nil {
		t.Fatal(err)
	}
	r.ilink.mu.Lock()
	calls, n = len(r.ilink.clientIDs), len(r.ilink.sent)
	r.ilink.mu.Unlock()
	if calls != 2 || n != 2 {
		t.Fatalf("the next ref: %d sendmessage calls, the owner got %d messages", calls, n)
	}
}
