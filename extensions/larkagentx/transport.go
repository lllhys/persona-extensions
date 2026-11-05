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
}

type processBridge struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	n       int
	pending map[int]chan bridgeReply
	events  chan bridgeEvent
	done    chan struct{}
	stop    sync.Once
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
	b := &processBridge{cmd: cmd, in: in, pending: map[int]chan bridgeReply{}, events: make(chan bridgeEvent, 64), done: make(chan struct{})}
	go b.read(out)
	return b, nil
}

func (b *processBridge) read(out io.Reader) {
	defer func() { b.Close(); _ = b.cmd.Wait(); close(b.done) }()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 4096), frameScanLimit)
	for sc.Scan() {
		var r bridgeReply
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			return
		}
		if r.Event != "" {
			select {
			case b.events <- r.bridgeEvent:
			case <-time.After(5 * time.Second):
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
