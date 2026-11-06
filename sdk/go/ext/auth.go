package ext

import (
	"context"
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Auth is an extension's sign-in (manifest.auth): the hub calls auth.status, auth.step and auth.logout. Keep what
// signing in yields in the data directory (Env().Data), never in a log or an answer.
type Auth interface {
	// Status is the sign-in now, from local state only: the hub waits at most 10 seconds.
	Status(ctx context.Context) (wire.AuthStatus, error)
	// Step goes on with signing in: step "" starts over, values are the fields of the step before. A device code or
	// QR step is asked again (no values) every Interval seconds until another step or Done comes back.
	Step(ctx context.Context, step string, values map[string]string) (wire.AuthPrompt, error)
	// Logout forgets the credentials and answers the status after.
	Logout(ctx context.Context) (wire.AuthStatus, error)
}

// AuthHandler is a's namespace: put it at Program.Namespaces["auth"].
func AuthHandler(a Auth) Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "status":
			return a.Status(ctx)
		case "step":
			var p wire.AuthStepParams
			if len(params) > 0 {
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, &Error{Code: "invalid", Message: err.Error()}
				}
			}
			return a.Step(ctx, p.Step, p.Values)
		case "logout":
			return a.Logout(ctx)
		}
		return nil, UnknownMethod("auth." + method)
	}
}

// AuthChanged tells the hub the sign-in changed by itself (expired, signed out elsewhere): it asks Status again.
func (h *Hub) AuthChanged() error { return h.Notify("auth.changed", map[string]any{}) }
