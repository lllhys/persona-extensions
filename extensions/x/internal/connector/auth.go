package connector

import "context"

// AuthSource is a connector whose sign-in can come from one of the user's computers (a CLI's login read there) or
// from a sign-in of its own (the device flow). The hub keeps the user's choice and hands it over at start and when it
// changes (connector.auth.set); the connector does not keep it.
type AuthSource interface {
	Auth() AuthState
	SetAuth(ctx context.Context, choice AuthChoice) (AuthState, error)
}

// AuthChoice is where the sign-in comes from: Source "machine" (Machine's login; MachineName for messages) or
// "device".
type AuthChoice struct {
	Source      string `json:"source"`
	Machine     string `json:"machine,omitempty"`
	MachineName string `json:"machine_name,omitempty"`
}

// AuthState is the choice in use and what its token may do (Scopes, once the service said: ScopesKnown).
type AuthState struct {
	Source      string   `json:"source"`
	Machine     string   `json:"machine,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	ScopesKnown bool     `json:"scopes_known"`
}
