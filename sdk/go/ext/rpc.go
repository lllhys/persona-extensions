package ext

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Error is an error on the wire: a message for people (never a secret or a message's body) and, for errors the
// other side acts on, a code (wire.ErrorCode*, or an extension point's own: not_found, invalid, conflict…).
type Error struct {
	Message string          `json:"message"`
	Code    string          `json:"code,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Coded is an error of another package that carries a code on the wire like Error does (the hub's own rpc errors,
// for the hub's built-in extensions).
type Coded interface {
	error
	WireError() (code string, data json.RawMessage)
}

// ErrClosed is what a call returns once the hub has gone.
var ErrClosed = errors.New("ext: connection closed")

type frame struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Event  string          `json:"event,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// peer is one side of the JSON-lines protocol: requests both ways, answers, one-way events.
type peer struct {
	w       io.Writer
	wmu     sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	pending map[string]chan frame
	closed  chan struct{}
	once    sync.Once

	// onRead, if set, sees each request in the order requests are read, before it is handled (each in its own
	// goroutine), and gives the context it is handled with.
	onRead    func(ctx context.Context, method string) context.Context
	onRequest func(ctx context.Context, method string, params json.RawMessage) (any, error)
	onEvent   func(event string, params json.RawMessage)
}

func newPeer(w io.Writer) *peer {
	return &peer{w: w, pending: map[string]chan frame{}, closed: make(chan struct{})}
}

func (p *peer) serve(ctx context.Context, r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		switch {
		case f.Method != "":
			rctx := ctx
			if p.onRead != nil {
				rctx = p.onRead(ctx, f.Method)
			}
			go p.handle(rctx, f)
		case f.Event != "":
			if p.onEvent != nil {
				p.onEvent(f.Event, f.Params)
			}
		case f.ID != "":
			p.mu.Lock()
			ch := p.pending[f.ID]
			delete(p.pending, f.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	p.once.Do(func() { close(p.closed) })
	return err
}

func (p *peer) handle(ctx context.Context, f frame) {
	res, err := p.onRequest(ctx, f.Method, f.Params)
	out := frame{ID: f.ID}
	if err != nil {
		out.Error = wireError(err)
	} else if b, merr := json.Marshal(res); merr != nil {
		out.Error = &Error{Message: merr.Error()}
	} else {
		out.Result = b
	}
	_ = p.write(out)
}

func wireError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var c Coded
	if errors.As(err, &c) {
		code, data := c.WireError()
		return &Error{Message: c.Error(), Code: code, Data: data}
	}
	return &Error{Message: err.Error()}
}

func (p *peer) write(f frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p.wmu.Lock()
	defer p.wmu.Unlock()
	select {
	case <-p.closed:
		return ErrClosed
	default:
	}
	_, err = p.w.Write(b)
	return err
}

func (p *peer) call(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := fmt.Sprintf("p%d", p.next.Add(1))
	ch := make(chan frame, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := p.write(frame{ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case f := <-ch:
		if f.Error != nil {
			return f.Error
		}
		if out != nil && len(f.Result) > 0 {
			return json.Unmarshal(f.Result, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.closed:
		return ErrClosed
	}
}

func (p *peer) notify(event string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.write(frame{Event: event, Params: raw})
}
