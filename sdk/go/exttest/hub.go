package exttest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nwverse/persona/sdk/go/ext"
)

// hub is the hub's side of the protocol, enough to check a program: it calls, and answers the program's calls to
// the hub with an empty result.
type hub struct {
	run     Run
	wmu     sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	pending map[string]chan frame
	closed  chan struct{}
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
	return &hub{run: run, pending: map[string]chan frame{}, closed: make(chan struct{})}
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
			_ = h.write(frame{ID: f.ID, Result: json.RawMessage("{}")})
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
