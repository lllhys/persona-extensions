package ext

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Pages pushed for apps before UI version 3 (docs/extensions.md "Declarative UI" "Compatibility"): they do not know push and pop and
// call ui.action with the action's id instead. Here an action that pushes gets an id of its own when the view goes to
// such an app, and that ui.action draws the pushed page in place of this one; a result's push draws the page, a pop
// the page as it is first drawn. The program writes push and pop once, for every app.

// pushVersion is the UI version that brought push and pop.
const pushVersion = 3

const pushPrefix = "ext.push."

// pushes remembers the pushes behind the ids given to older apps (a few hundred, the newest).
type pushes struct {
	mu    sync.Mutex
	next  int
	byID  map[string]wire.Push
	order []string
}

func (p *pushes) put(push wire.Push) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID == nil {
		p.byID = map[string]wire.Push{}
	}
	p.next++
	id := pushPrefix + strconv.Itoa(p.next)
	p.byID[id] = push
	p.order = append(p.order, id)
	if len(p.order) > 512 {
		delete(p.byID, p.order[0])
		p.order = p.order[1:]
	}
	return id
}

func (p *pushes) get(id string) (wire.Push, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	push, ok := p.byID[id]
	return push, ok
}

// older: the app draws a UI version before push and pop.
func older(r *UIRequest) bool { return r.UIVersion > 0 && r.UIVersion < pushVersion }

// forOlder gives each action that pushes an id of its own, for an older app (which calls ui.action with it).
func (s *server) forOlder(v *wire.View, contribution string) {
	if v == nil {
		return
	}
	fix := func(a *wire.Action) {
		if a == nil || a.Push == nil {
			return
		}
		push := *a.Push
		if push.UI == "" {
			push.UI = contribution
		}
		a.ID = s.pushes.put(push)
	}
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			c := &v.Sections[i].Items[j]
			fix(c.Action)
			if c.Button != nil {
				fix(&c.Button.Action)
			}
		}
	}
}

// render draws the page of contribution with state for r's app.
func (s *server) render(ctx context.Context, r *UIRequest, contribution, state string) (*wire.View, error) {
	page, ok := s.prog.UI[contribution]
	if !ok || page.Render == nil {
		return nil, NoContribution(contribution)
	}
	q := *r
	q.Contribution, q.State, q.Action, q.Values = contribution, state, "", nil
	v, err := page.Render(ctx, &q)
	if err == nil && older(r) {
		s.forOlder(v, contribution)
	}
	return v, err
}

// olderAction answers an older app's tap on an action that pushes: the pushed page, in place of this one.
func (s *server) olderAction(ctx context.Context, r *UIRequest) (*wire.ActionResult, bool, error) {
	if !strings.HasPrefix(r.Action, pushPrefix) {
		return nil, false, nil
	}
	push, ok := s.pushes.get(r.Action)
	if !ok { // the program restarted since: draw this page again
		v, err := s.render(ctx, r, r.Contribution, r.State)
		return &wire.ActionResult{View: v}, true, err
	}
	v, err := s.render(ctx, r, push.UI, push.State)
	return &wire.ActionResult{View: v}, true, err
}

// olderResult turns a result's push or pop into a view an older app can draw.
func (s *server) olderResult(ctx context.Context, r *UIRequest, res *wire.ActionResult) (*wire.ActionResult, error) {
	switch {
	case res == nil:
		return nil, nil
	case res.Push != nil:
		ui := res.Push.UI
		if ui == "" {
			ui = r.Contribution
		}
		v, err := s.render(ctx, r, ui, res.Push.State)
		return &wire.ActionResult{View: v, Toast: res.Toast}, err
	case res.Pop:
		v, err := s.render(ctx, r, r.Contribution, "")
		return &wire.ActionResult{View: v, Toast: res.Toast}, err
	}
	s.forOlder(res.View, r.Contribution)
	return res, nil
}

// Open is an action that opens a page on top of this one: ui.render of ui ("" this one) with state (EncodeState of
// the program's own), title shown until it is drawn. id names it for ui.action in apps before UI version 3 (the
// SDK answers those itself).
func Open(id, ui string, state any, title string) *wire.Action {
	return &wire.Action{ID: id, Push: &wire.Push{UI: ui, State: EncodeState(state), Title: title}}
}
