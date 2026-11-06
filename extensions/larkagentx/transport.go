package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// frameLimit is the maximum JSON frame size in bytes, matching LIMIT in
// bridge.py. frameScanLimit leaves room for a frame of exactly frameLimit
// bytes plus its trailing newline: bufio.Scanner fails with ErrTooLong once
// the token plus its separator cannot fit in the buffer.
const (
	frameLimit     = 1 << 20
	frameScanLimit = frameLimit + 64*1024
)

// backlogLimit caps events read but not yet handed to the consumer (the
// same bound as the Seen list). Beyond it the bridge fails closed rather
// than grow without bound behind a wedged consumer.
const backlogLimit = 4096

type bridgeEvent struct {
	Event   string  `json:"event"`
	State   string  `json:"state"`
	Message message `json:"message"`
}

type bridgeReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
	bridgeEvent
}

type bridge interface {
	Call(context.Context, string, any, any) error
	Events() <-chan bridgeEvent
	Done() <-chan struct{}
	Close()
	// Drain returns, in order, the events read but not handed to the
	// consumer. It is meaningful once Done has fired.
	Drain() []bridgeEvent
}

type processBridge struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	mu       sync.Mutex
	writeMu  sync.Mutex
	n        int
	pending  map[int]chan bridgeReply
	events   chan bridgeEvent
	done     chan struct{}
	shutdown chan struct{}
	stop     sync.Once
	queueMu  sync.Mutex
	queue    []bridgeEvent
	eof      bool
	more     chan struct{}
}

func startBridge(ctx context.Context, argv []string) (bridge, error) {
	if len(argv) == 0 {
		return nil, failure("not_configured")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, failure("unavailable")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, failure("unavailable")
	}
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		return nil, failure("unavailable")
	}
	b := &processBridge{cmd: cmd, in: in, pending: map[int]chan bridgeReply{}, events: make(chan bridgeEvent, 64), done: make(chan struct{}), shutdown: make(chan struct{}), more: make(chan struct{}, 1)}
	go b.read(out)
	return b, nil
}

func (b *processBridge) read(out io.Reader) {
	defer func() { b.Close(); _ = b.cmd.Wait(); close(b.done) }()
	delivered := make(chan struct{})
	go b.deliver(delivered)
	// On EOF or a bad frame, hand over what was already read before
	// closing, as the blocking delivery did.
	defer func() { b.push(nil); <-delivered }()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 4096), frameScanLimit)
	for sc.Scan() {
		var r bridgeReply
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			return
		}
		if r.Event != "" {
			// Events go through a FIFO so the scanner keeps reading: a
			// reply can follow them, and its caller holds c.op, which
			// watch needs before it takes the next event (#250).
			if !b.push(&r.bridgeEvent) {
				b.Close()
				return
			}
			continue
		}
		b.mu.Lock()
		ch := b.pending[r.ID]
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- r:
			default:
			}
		}
	}
}

// push queues ev for deliver, or marks the end of the stream when ev is
// nil. It reports false when the backlog is full.
func (b *processBridge) push(ev *bridgeEvent) bool {
	b.queueMu.Lock()
	if ev == nil {
		b.eof = true
	} else if len(b.queue) >= backlogLimit {
		b.queueMu.Unlock()
		return false
	} else {
		b.queue = append(b.queue, *ev)
	}
	b.queueMu.Unlock()
	select {
	case b.more <- struct{}{}:
	default:
	}
	return true
}

// deliver hands queued events to the consumer in order. Delivery blocks for
// as long as the consumer needs: the watch goroutine holds the event across
// a slow op (a 10s secret save or a 30s session check), so a full buffer
// must not tear down a healthy companion. Only Close() unblocks delivery,
// by closing shutdown; otherwise deliver returns once the stream has ended
// and the queue is empty.
func (b *processBridge) deliver(delivered chan<- struct{}) {
	defer close(delivered)
	for {
		b.queueMu.Lock()
		if len(b.queue) == 0 {
			eof := b.eof
			b.queueMu.Unlock()
			if eof {
				return
			}
			select {
			case <-b.more:
			case <-b.shutdown:
				return
			}
			continue
		}
		ev := b.queue[0]
		b.queue[0] = bridgeEvent{}
		b.queue = b.queue[1:]
		b.queueMu.Unlock()
		select {
		case b.events <- ev:
		case <-b.shutdown:
			// Keep the event for Drain.
			b.queueMu.Lock()
			b.queue = append([]bridgeEvent{ev}, b.queue...)
			b.queueMu.Unlock()
			return
		}
	}
}

func (b *processBridge) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b.mu.Lock()
	b.n++
	id := b.n
	ch := make(chan bridgeReply, 1)
	b.pending[id] = ch
	raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err == nil && len(raw) > frameLimit {
		err = errors.New("frame too large")
	}
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
	if err != nil {
		return failure("invalid_params")
	}
	written := make(chan error, 1)
	go func() {
		b.writeMu.Lock()
		_, writeErr := b.in.Write(append(raw, '\n'))
		b.writeMu.Unlock()
		written <- writeErr
	}()
	select {
	case err = <-written:
		if err != nil {
			b.Close()
			return failure("unavailable")
		}
	case <-ctx.Done():
		b.Close()
		return failure("unavailable")
	case <-b.done:
		return failure("unavailable")
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return failure(r.Error.Code)
		}
		if out != nil && json.Unmarshal(r.Result, out) != nil {
			return failure("unavailable")
		}
		return nil
	case <-b.done:
		return failure("unavailable")
	case <-ctx.Done():
		b.Close()
		return failure("unavailable")
	}
}

func (b *processBridge) Events() <-chan bridgeEvent { return b.events }
func (b *processBridge) Done() <-chan struct{}      { return b.done }
func (b *processBridge) Close() {
	b.stop.Do(func() {
		close(b.shutdown)
		_ = b.in.Close()
		go func() {
			select {
			case <-b.done:
			case <-time.After(5 * time.Second):
				_ = b.cmd.Process.Kill()
			}
		}()
	})
}

func (b *processBridge) Drain() []bridgeEvent {
	var out []bridgeEvent
	for len(b.events) > 0 {
		out = append(out, <-b.events)
	}
	b.queueMu.Lock()
	out = append(out, b.queue...)
	b.queue = nil
	b.queueMu.Unlock()
	return out
}
