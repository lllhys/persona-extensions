package serve

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// A request id stays pinned while its write runs, even after a full result cache of newer writes. Once it finishes,
// its result joins the same bounded cache as every other completed write.
func TestWritesPinInflightAndBoundResults(t *testing.T) {
	var w writes
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	runA := func() (CallResult, error) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-release
		}
		return CallResult{Text: "call-" + strconv.FormatInt(int64(n), 10)}, nil
	}
	type result struct {
		res CallResult
		err error
	}
	first := make(chan result, 1)
	go func() {
		res, err := w.do("a", runA)
		first <- result{res, err}
	}()
	<-started

	for i := range doneMost {
		id := "other-" + strconv.Itoa(i)
		res, err := w.do(id, func() (CallResult, error) { return CallResult{Text: id}, nil })
		if err != nil || res.Text != id {
			t.Fatalf("write %s: %+v %v", id, res, err)
		}
	}

	retry := make(chan result, 1)
	go func() {
		res, err := w.do("a", runA)
		retry <- result{res, err}
	}()
	// Give the retry time to reach the running write: it must wait rather than run the callback again.
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("the retry ran the callback while the first write ran: %d calls", n)
	}
	select {
	case got := <-retry:
		t.Fatalf("the retry answered before the first write finished: %+v", got)
	default:
	}

	close(release)
	for _, wait := range []struct {
		name string
		ch   <-chan result
	}{{"first", first}, {"retry", retry}} {
		got := <-wait.ch
		if got.err != nil || got.res.Text != "call-1" {
			t.Fatalf("%s result: %+v %v", wait.name, got.res, got.err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("callback ran %d times", n)
	}

	// The completed result is cached immediately after the running write is released.
	res, err := w.do("a", runA)
	if err != nil || res.Text != "call-1" || calls.Load() != 1 {
		t.Fatalf("completed retry: %+v %v, %d calls", res, err, calls.Load())
	}
	// A full cache of still newer completed writes evicts A again: completed results remain bounded.
	for i := range doneMost {
		id := "later-" + strconv.Itoa(i)
		res, err := w.do(id, func() (CallResult, error) { return CallResult{Text: id}, nil })
		if err != nil || res.Text != id {
			t.Fatalf("later write %s: %+v %v", id, res, err)
		}
	}
	res, err = w.do("a", runA)
	if err != nil || res.Text != "call-2" || calls.Load() != 2 {
		t.Fatalf("retry after cache eviction: %+v %v, %d calls", res, err, calls.Load())
	}
}
