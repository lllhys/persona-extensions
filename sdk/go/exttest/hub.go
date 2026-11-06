package exttest

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// hub is the hub's side of the protocol, enough to check a program: it calls, and answers the program's calls to
// the hub: a connector's secrets from memory, a channel's messages kept, what answers says, and the rest with an
// empty result. What the
// program sends that the schemas describe (wire.Calls) is held to them; what does not hold is in problems.
type hub struct {
	run     Run
	wmu     sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	pending map[string]chan frame
	closed  chan struct{}

	answers  map[string]func(params json.RawMessage) (any, error)
	secrets  map[string][]byte // by contribution
	events   []Event
	received []wire.ChannelReceiveParams
	problems []string
}

// Event is one the program sent the hub.
type Event struct {
	Name   string
	Params json.RawMessage
}

type frame struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Event  string          `json:"event,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ext.Error      `json:"error,omitempty"`
}

func newHub(run Run) *hub {
	return &hub{run: run, pending: map[string]chan frame{}, closed: make(chan struct{}),
		answers: map[string]func(json.RawMessage) (any, error){}, secrets: map[string][]byte{}}
}

func (h *hub) serve(ctx context.Context) {
	defer close(h.closed)
	sc := bufio.NewScanner(h.run.FromProgram)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		switch {
		case f.Method != "":
			go h.answer(f)
		case f.Event != "":
			h.mu.Lock()
			h.events = append(h.events, Event{f.Event, append(json.RawMessage(nil), f.Params...)})
			h.mu.Unlock()
			if _, ok := wire.Calls[f.Event]; ok {
				h.holds(f.Event, wire.ValidateParams(f.Event, f.Params))
			}
		case f.ID != "":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
}

// answer answers one of the program's calls.
func (h *hub) answer(f frame) {
	if _, ok := wire.Calls[f.Method]; ok {
		h.holds(f.Method, wire.ValidateParams(f.Method, f.Params))
	}
	res, err := h.result(f.Method, f.Params)
	out := frame{ID: f.ID}
	if err != nil {
		e, ok := err.(*ext.Error)
		if !ok {
			e = &ext.Error{Message: err.Error()}
		}
		out.Error = e
	} else {
		b, merr := json.Marshal(res)
		if merr != nil {
			b = json.RawMessage("{}")
		}
		out.Result = b
	}
	_ = h.write(out)
}

func (h *hub) result(method string, params json.RawMessage) (any, error) {
	h.mu.Lock()
	f := h.answers[method]
	h.mu.Unlock()
	if f != nil {
		return f(params)
	}
	c := ext.Contribution(params)
	switch method {
	case ext.ChannelNamespace + ".receive":
		var p wire.ChannelReceiveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.received = append(h.received, p)
		h.mu.Unlock()
	case ext.ConnectorNamespace + ".secrets.load":
		h.mu.Lock()
		defer h.mu.Unlock()
		if data, ok := h.secrets[c]; ok {
			return wire.ConnectorSecret{Data: base64.StdEncoding.EncodeToString(data)}, nil
		}
		return wire.ConnectorSecret{}, nil
	case ext.ConnectorNamespace + ".secrets.save":
		var p wire.ConnectorSecretSaveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.secrets[c] = data
		h.mu.Unlock()
	case ext.ConnectorNamespace + ".secrets.delete":
		h.mu.Lock()
		delete(h.secrets, c)
		h.mu.Unlock()
	}
	return struct{}{}, nil
}

// holds notes a message that does not hold to its schema.
func (h *hub) holds(what string, err error) {
	if err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.problems = append(h.problems, fmt.Sprintf("%s: does not hold to schema/extension: %v", what, err))
}

func (h *hub) takeProblems() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.problems
	h.problems = nil
	return p
}

func (h *hub) write(f frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	_, err = h.run.ToProgram.Write(append(b, '\n'))
	return err
}

// notify sends the program an event (event.kb.changed…).
func (h *hub) notify(event string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return h.write(frame{Event: event, Params: raw})
}

func (h *hub) call(ctx context.Context, method string, params, out any) error {
	id := fmt.Sprintf("h%d", h.next.Add(1))
	ch := make(chan frame, 1)
	h.mu.Lock()
	h.pending[id] = ch
	h.mu.Unlock()
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := h.write(frame{ID: id, Method: method, Params: raw}); err != nil {
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
		return fmt.Errorf("%s: no answer: %w", method, ctx.Err())
	case <-h.closed:
		return fmt.Errorf("%s: the program closed its stdout", method)
	}
}
