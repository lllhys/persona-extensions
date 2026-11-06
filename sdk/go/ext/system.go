package ext

import (
	"context"
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Places outside the app (docs/extensions.md "System-level places"): widgets are ui contributions with placement widget (Page,
// drawn per family); shortcuts are the shortcuts extension point, answered here.

// Shortcut runs one shortcut: r.Values are its params' values (strings). Its result is a sentence Siri says or shows
// and, optionally, a view the hub holds to the large widget's subset.
type Shortcut func(ctx context.Context, r *ShortcutRequest) (*wire.ShortcutResult, error)

// ShortcutRequest is a shortcut.run, and the hub to call while running.
type ShortcutRequest struct {
	wire.ShortcutRunParams
	Hub *Hub
}

// Widget says whether r draws a widget (placement widget); Family is then which (small, medium, large,
// accessory_circular, accessory_rectangular, accessory_inline): draw less for the small ones, nothing to tap.
func (r *UIRequest) Widget() bool { return r.Placement == wire.PlacementWidget }

// Family is the widget family r draws ("" when it is not a widget).
func (r *UIRequest) Family() string {
	if !r.Widget() {
		return ""
	}
	return r.Size
}

func (s *server) shortcut(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if method != "run" {
		return nil, UnknownMethod("shortcut." + method)
	}
	r := &ShortcutRequest{Hub: s.hub}
	if err := json.Unmarshal(raw, &r.ShortcutRunParams); err != nil {
		return nil, &Error{Code: "invalid", Message: err.Error()}
	}
	run, ok := s.prog.Shortcuts[r.Contribution]
	if !ok {
		return nil, NoContribution(r.Contribution)
	}
	if r.Values == nil {
		r.Values = map[string]string{}
	}
	res, err := run(ctx, r)
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &wire.ShortcutResult{}
	}
	return res, nil
}
