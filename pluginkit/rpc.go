// Package pluginkit is a Persona extension's side of the process protocol (Persona's docs/extensions.md「进程扩展协议」)
// as this repository's extensions use it. The protocol itself is the official SDK's (sdk/go/ext, a copy kept by
// Persona's scripts/gen/sync-sdk.sh): this package keeps the shape the extensions were written against (handlers by
// full method name, InitParams) and adds the helpers they share (inbox, quota, ui).
package pluginkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/nwverse/persona/sdk/go/ext"
)

// Protocol is the process protocol's version.
const Protocol = ext.Protocol

// Error codes.
const (
	CodeNotInitialized = "not_initialized"
	CodeUnknownMethod  = "unknown_method"
	CodeWrongProtocol  = "wrong_protocol"
	CodeNoContribution = "no_contribution"
	CodeUnsupported    = "unsupported"
)

// Error is an error with a code the hub acts on.
type Error struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// WireError is its code, for the SDK to send as it is.
func (e *Error) WireError() (string, json.RawMessage) { return e.Code, nil }

// Errorf is an error with a code.
func Errorf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// InitParams is what initialize carries (the parts an extension uses).
type InitParams struct {
	Protocol int `json:"protocol"`
	Hub      struct {
		Build           int64          `json:"build"`
		ExtensionPoints map[string]int `json:"extension_points"`
		// Role is the hub's place in its group: "center" or "member" ("" from a hub before roles: a center).
		Role string `json:"role"`
		// Capabilities are what the hub does that an extension may go by (HubInboxGroup); none from a hub before them.
		Capabilities []string `json:"capabilities"`
	} `json:"hub"`
	// Plugin is the extension's own, under whichever key the hub sent it (extension; plugin before Persona's #1844).
	Plugin struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Dir     string `json:"dir"`
		DataDir string `json:"data_dir"`
	} `json:"plugin"`
	Config  json.RawMessage     `json:"config"`
	Secrets map[string]string   `json:"secrets"`
	Granted json.RawMessage     `json:"granted"`
	Active  map[string][]string `json:"active"`
}

// Hub capabilities (InitParams.Hub.Capabilities).
const (
	// HubInboxGroup: the hub relates a connector's batches of new messages to inbox events (Persona's docs/inbox.md
	// 「事件合成」): a connector that declares the capability "group" hands batches and writes the events' meta.
	HubInboxGroup = "inbox_group"
)

// HubCan says whether the hub said it has capability c.
func (p InitParams) HubCan(c string) bool {
	for _, have := range p.Hub.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}

// Handler answers one method ("quota.query").
type Handler func(ctx context.Context, hub *Hub, params json.RawMessage) (any, error)

// Plugin is an extension program.
type Plugin struct {
	ID, Version string
	// Manifest is its persona-extension.json, answered to manifest (needed when it runs elsewhere).
	Manifest json.RawMessage
	// Contributes is what initialize says it contributes (a subset of the manifest's).
	Contributes map[string]any
	// Init runs once with initialize's parameters; hub is for the extension's own calls and events from then on.
	Init     func(p InitParams, hub *Hub) error
	Handlers map[string]Handler
}

// Hub is the hub, for the extension's calls and events: the SDK's, or (in tests) lines written to w.
type Hub struct {
	ext *ext.Hub

	w  io.Writer
	mu sync.Mutex
}

// Notify sends the hub a one-way event ({"event", "params"}): connector.changed, api.broadcast.
func (h *Hub) Notify(event string, params any) error {
	if h.ext != nil {
		return h.ext.Notify(event, params)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	b, err := json.Marshal(struct {
		Event  string          `json:"event"`
		Params json.RawMessage `json:"params"`
	}{event, raw})
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.w.Write(append(b, '\n'))
	return err
}

// Call calls a hub method and decodes its result into out. A coded error of the hub's is an *Error.
func (h *Hub) Call(ctx context.Context, method string, params, out any) error {
	if h.ext == nil {
		return Errorf(CodeUnknownMethod, "no hub to call %s", method)
	}
	err := h.ext.Call(ctx, method, params, out)
	if e, ok := err.(*ext.Error); ok {
		return &Error{Message: e.Message, Code: e.Code}
	}
	return err
}

// Run serves p on stdin / stdout until the hub closes stdin.
func Run(p Plugin) error { return Serve(context.Background(), os.Stdin, os.Stdout, p) }

// Serve serves p on in / out.
func Serve(ctx context.Context, in io.Reader, out io.Writer, p Plugin) error {
	var hub *Hub
	prog := ext.Program{ID: p.ID, Version: p.Version, Manifest: p.Manifest, Namespaces: map[string]ext.Handler{}}
	prog.Init = func(_ context.Context, in *ext.Init, h *ext.Hub) (map[string]any, error) {
		hub = &Hub{ext: h}
		var ip InitParams
		if err := json.Unmarshal(in.Raw, &ip); err != nil {
			return nil, err
		}
		if ip.Plugin.ID == "" { // a hub that sends it as extension only
			info := in.Info()
			ip.Plugin.ID, ip.Plugin.Version, ip.Plugin.Dir, ip.Plugin.DataDir = info.ID, info.Version, info.Dir, info.DataDir
		}
		if p.Init != nil {
			if err := p.Init(ip, hub); err != nil {
				return nil, err
			}
		}
		return p.Contributes, nil
	}
	// The handlers are by full method name; the SDK hands calls over by namespace.
	for method := range p.Handlers {
		ns, _, ok := strings.Cut(method, ".")
		if !ok || prog.Namespaces[ns] != nil {
			continue
		}
		prog.Namespaces[ns] = func(ctx context.Context, rest string, params json.RawMessage) (any, error) {
			h := p.Handlers[ns+"."+rest]
			if h == nil {
				return nil, Errorf(CodeUnknownMethod, "unknown method %s.%s", ns, rest)
			}
			return h(ctx, hub, params)
		}
	}
	err := ext.Serve(ctx, in, out, prog)
	if err == io.EOF {
		return nil
	}
	return err
}

// Contribution is the contribution a call names.
func Contribution(params json.RawMessage) string { return strings.TrimSpace(ext.Contribution(params)) }
