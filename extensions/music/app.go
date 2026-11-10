package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// toolBudget is how long one tool call or action may read.
const toolBudget = 25 * time.Second

// source is one music service: a connector contribution and the endpoints configured under it (a Mac, a phone, a
// cloud account). The extension contributes one connector per service; each is its own status, tool namespace and
// page tab, so adding Spotify is a manifest entry and a builder here, not a rewrite of the program.
type source struct {
	id        string // the connector id: applemusic, spotify…
	name      string // its display name: Apple Music
	short     string // at most 12 characters, for the page's in-page switch when it fits
	badge     string // at most 8 characters, for a row's source badge in All sources
	guide     []string
	providers map[string]provider
	order     []string
}

func newSource(id, name, short, badge string, guide ...string) *source {
	return &source{id: id, name: name, short: short, badge: badge, guide: guide, providers: map[string]provider{}}
}

var (
	sourceIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	endpointIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)
)

func (s *source) add(p provider) error {
	id := p.ID()
	if !endpointIDPattern.MatchString(id) {
		return fmt.Errorf("source %s: endpoint id %q must match %s", s.id, id, endpointIDPattern)
	}
	if _, ok := s.providers[id]; ok {
		return fmt.Errorf("source %s: two endpoints named %q", s.id, id)
	}
	s.providers[id] = p
	s.order = append(s.order, id)
	return nil
}

func (s *source) list() []provider {
	out := make([]provider, 0, len(s.order))
	for _, id := range s.order {
		if p, ok := s.providers[id]; ok {
			out = append(out, p)
		}
	}
	return out
}

// app is the running extension: one source per enabled connector, plus the write receipts.
type app struct {
	logf        func(string, ...any)
	cfg         Config
	dataDir     string
	sources     map[string]*source
	sourceOrder []string
	receipts    *writeStore
	hub         *ext.Hub
	// enabled is the connector id -> on map the hub's initialize sent. nil means the hub sent no active map (an
	// older hub): every contribution runs, as before per-connector switches existed.
	enabled map[string]bool
}

func newApp() *app {
	return &app{
		logf:     log.Printf,
		sources:  map[string]*source{},
		receipts: newWriteStore(""),
	}
}

func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	var cfg Config
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	a.cfg = cfg
	a.hub = hub
	a.dataDir = in.Info().DataDir
	if dir := a.dataDir; dir != "" {
		store, err := openWriteStore(dir)
		if err != nil {
			return nil, err
		}
		a.receipts = store
	}
	a.enabled = activeConnectors(in)
	return nil, a.addAppleMusicSource()
}

// activeConnectors reads the hub's initialize. An older hub sends no active map at all; a newer one sends every
// running point and omits connectors when the user switched the connector off.
func activeConnectors(in *ext.Init) map[string]bool {
	if in == nil || in.Active == nil {
		return nil
	}
	out := map[string]bool{}
	for _, id := range in.Active["connectors"] {
		out[id] = true
	}
	return out
}

// appleMusicGuide is what the page says when the Apple Music connector is on but has no endpoint yet.
var appleMusicGuide = []string{"Open Settings › Extensions › Music.", "Choose a Mac, turn on the iPhone, or both."}

// addAppleMusicSource builds Apple Music's endpoints from the configuration. The source exists even when neither
// endpoint is configured, so its connect card and its not_configured status have somewhere to live.
func (a *app) addAppleMusicSource() error {
	src := newSource(appleMusicID, appleMusicName, appleMusicName, "Apple", appleMusicGuide...)
	if machine := a.cfg.machine(); machine != "" {
		var node nodeCaller
		if a.hub != nil {
			node = hubNodeCaller{hub: a.hub}
		}
		if err := src.add(newMacProvider(machine, node)); err != nil {
			return err
		}
	}
	if a.cfg.IPhone {
		if err := src.add(newIPhoneProvider(openSnapshotStore(a.dataDir))); err != nil {
			return err
		}
	}
	// A second service is added here: its connector id is in the manifest, and its builder registers a source with
	// the endpoints its own configuration describes (see ADDING_A_SOURCE.md).
	a.addSource(src)
	return nil
}

func (a *app) addSource(s *source) {
	if s == nil || !sourceIDPattern.MatchString(s.id) {
		return
	}
	if _, ok := a.sources[s.id]; ok {
		a.logf("music: two sources with the id %q", s.id)
		return
	}
	a.sources[s.id] = s
	a.sourceOrder = append(a.sourceOrder, s.id)
}

func (a *app) sourceActive(id string) bool {
	return a.enabled == nil || a.enabled[id]
}

// activeSources are the sources whose connector is on, in manifest order.
func (a *app) activeSources() []*source {
	out := make([]*source, 0, len(a.sourceOrder))
	for _, id := range a.sourceOrder {
		if s, ok := a.sources[id]; ok && a.sourceActive(id) {
			out = append(out, s)
		}
	}
	return out
}

func (a *app) endpointProvider(sourceID, endpointID string) provider {
	if s, ok := a.sources[sourceID]; ok {
		return s.providers[endpointID]
	}
	return nil
}

// contributionSource maps a connector call's contribution to its source. An older hub that sends none gets the only
// active source.
func (a *app) contributionSource(id string) (*source, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		active := a.activeSources()
		if len(active) == 1 {
			return active[0], nil
		}
		if len(a.sourceOrder) == 1 {
			return a.sources[a.sourceOrder[0]], nil
		}
		return nil, errors.New("this extension needs the connector id of the call")
	}
	s, ok := a.sources[id]
	if !ok || !a.sourceActive(id) {
		return nil, ext.NoContribution(id)
	}
	return s, nil
}

// connectorStatus is one connector's state: ok while any endpoint is configured. A configured endpoint that is
// offline or has no snapshot still has its page and its tools; a failed read says what happened.
func (a *app) connectorStatus(_ context.Context, src *source) providerStatus {
	if len(src.order) == 0 {
		return providerStatus{State: "not_configured", Account: src.name, Guide: src.guide}
	}
	names := make([]string, 0, len(src.order))
	for _, p := range src.list() {
		names = append(names, p.Name())
	}
	return providerStatus{State: "ok", Account: src.name + " · " + strings.Join(names, " + ")}
}

// splitTool turns an assistant-facing name (<connector>_<endpoint>_<tool>) into its endpoint and the endpoint-local
// tool name. It is the one place the connector's prefix and its endpoints meet.
func (a *app) splitTool(src *source, name string) (provider, string, error) {
	rest, ok := strings.CutPrefix(name, src.id+"_")
	if !ok {
		return nil, "", fmt.Errorf("unknown tool %q", name)
	}
	endpoint, local, ok := strings.Cut(rest, "_")
	if !ok || endpoint == "" || local == "" {
		return nil, "", fmt.Errorf("unknown tool %q", name)
	}
	p, ok := src.providers[endpoint]
	if !ok {
		return nil, "", fmt.Errorf("unknown tool %q", name)
	}
	return p, endpoint + "_" + local, nil
}

func (a *app) connector(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var in struct {
		Contribution string          `json:"contribution"`
		Name         string          `json:"name"`
		Args         json.RawMessage `json:"args"`
		RequestID    string          `json:"request_id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid connector parameters"}
		}
	}
	src, err := a.contributionSource(in.Contribution)
	if err != nil {
		return nil, err
	}
	switch method {
	case "status":
		return a.connectorStatus(ctx, src), nil
	case "tools.list":
		return map[string]any{"tools": a.connectorTools(src)}, nil
	case "tools.call":
		p, local, err := a.splitTool(src, in.Name)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, toolBudget)
		defer cancel()
		text, err := a.callTool(ctx, p, local, in.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	case "actions.list":
		return map[string]any{"tools": a.connectorActions(src)}, nil
	case "actions.describe":
		p, local, err := a.splitTool(src, in.Name)
		if err != nil {
			return nil, err
		}
		return describeAction(p, local, in.Args)
	case "actions.do":
		if len(in.RequestID) == 0 || len(in.RequestID) > 128 || strings.TrimSpace(in.RequestID) == "" {
			return nil, errors.New("actions.do needs a nonempty request_id of at most 128 characters")
		}
		p, local, err := a.splitTool(src, in.Name)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, toolBudget)
		defer cancel()
		return a.doAction(ctx, src, p, in.RequestID, local, in.Args)
	}
	return nil, ext.UnknownMethod("connector." + method)
}

func (a *app) connectorTools(src *source) []tool {
	out := make([]tool, 0)
	for _, p := range src.list() {
		for _, t := range toolsFor(src, p) {
			t.Name = src.id + "_" + t.Name
			out = append(out, t)
		}
	}
	return out
}

func (a *app) connectorActions(src *source) []tool {
	out := make([]tool, 0)
	for _, p := range src.list() {
		for _, t := range actionToolsFor(src, p) {
			t.Name = src.id + "_" + t.Name
			out = append(out, t)
		}
	}
	return out
}

// image answers ui.image for an endpoint's picture: the page's ref is "<source>:<endpoint>:<kind>:<id>". The hub
// caches the bytes per ref and width.
func (a *app) image(ctx context.Context, r *ext.ImageRequest) (*wire.UIImage, error) {
	parts := strings.SplitN(strings.TrimSpace(r.Ref), ":", 4)
	if len(parts) != 4 {
		return nil, ext.Unsupported("unknown image ref")
	}
	src, ok := a.sources[parts[0]]
	if !ok {
		return nil, ext.Unsupported("unknown image ref")
	}
	p, ok := src.providers[parts[1]]
	if !ok {
		return nil, ext.Unsupported("unknown image ref")
	}
	ap, ok := p.(artworkProvider)
	if !ok {
		return nil, ext.Unsupported("this endpoint has no pictures")
	}
	kind, id := parts[2], parts[3]
	if (kind != "track" && kind != "playlist") || id == "" || len(id) > 200 {
		return nil, ext.Unsupported("unknown image ref")
	}
	data, err := ap.Artwork(ctx, kind, id, r.Width)
	if err != nil {
		return nil, err
	}
	return ext.Image("", data, 86400), nil
}

func oneLine(s string) string { return strings.TrimSpace(strings.Join(strings.Fields(s), " ")) }

// clip cuts s to at most n characters, marking the cut inside the budget.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func clamp(v, def, lo, hi int) int {
	if v <= 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// invalidate tells the apps that both the nav page and the older plugin page changed.
func (a *app) invalidate() {
	if a.hub == nil {
		return
	}
	_ = a.hub.Invalidate(navUI)
	_ = a.hub.Invalidate(pageUI)
}
