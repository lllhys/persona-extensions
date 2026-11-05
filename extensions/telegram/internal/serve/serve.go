// Package serve runs the Telegram connector as an extension program: the hub starts it and talks the process protocol
// (docs/reference.md#process-protocol-and-views) to it over stdin / stdout through Persona's official Go SDK
// (sdk/go/ext), the connector protocol's methods in the connector namespace (connector.status, …;
// docs/reference.md#process-protocol-and-views). One connector per program.
//
// The hub calls the methods below; the program calls back for its secret (connector.secrets.*: the hub binds them to
// this program's connector) and tells the hub what changed (connector.changed). Nothing here logs what crosses the
// pipe. A write (actions.do) comes only once the hub's gate let it through; the program never starts one itself.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/kb"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/proto"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Methods the hub calls.
const (
	MManifest  = "manifest"
	MStatus    = "status"
	MLoginStep = "login.step"
	MLogout    = "logout"
	MChanges   = "archive.changes"
	MCollect   = "inbox.collect"
	MReply     = "inbox.reply"
	MTools     = "tools.list"
	MCall      = "tools.call"
	MActions   = "actions.list"
	MDescribe  = "actions.describe"
	MDo        = "actions.do" // only once the hub's gate let it through
	MThread    = "thread.page"
	MMedia     = "thread.media"
	// The knowledge base's items (connector.Items) and an archived message's attachment (connector.ArchiveMedia).
	MItems        = "items.list"
	MDocTitle     = "items.doc_title"
	MArchiveMedia = "archive.media"
	// The live state for the assistant's snapshot (connector.Snapshot): SnapshotParams → connector.SnapshotData.
	MSnapshot = "snapshot.get"
)

// Methods the program calls on the hub, and its event.
const (
	MSecretsLoad   = "secrets.load"
	MSecretsSave   = "secrets.save"
	MSecretsDelete = "secrets.delete"
	// The knowledge base (connector.KnowledgeBase): PostsPutParams → PostsPutResult, PostsPendingParams →
	// PostsPendingResult, ArchiveChatsParams → ArchiveChatsResult.
	MPostsPut      = "posts.put"
	MPostsComments = "posts.comments"
	MPostsPending  = "posts.pending"
	MArchiveChats  = "archive.chats"
	EChanged       = "changed" // ChangedEvent
)

// The knowledge base's calls on the wire.
type (
	PostsPutParams struct {
		Posts []connector.PostPut `json:"posts"`
	}
	PostsPutResult struct {
		Statuses []string `json:"statuses"` // per post: connector.PostNew, PostChanged, PostSame, PostRefused
	}
	PostsCommentsParams struct {
		ID           string                  `json:"id"`
		CommentCount int                     `json:"comment_count"`
		Comments     []connector.PostComment `json:"comments"`
	}
	SnapshotParams struct {
		TZ string `json:"tz,omitempty"` // the user's time zone ("" the program's own)
	}
	PostsPendingParams struct {
		Limit int `json:"limit,omitempty"`
	}
	PostsPendingResult struct {
		Posts []connector.PendingPost `json:"posts"`
	}
	ArchiveChatsParams struct {
		Source string `json:"source,omitempty"` // "" is the connector's id
	}
	ArchiveChatsResult struct {
		Chats []connector.ArchivedChat `json:"chats"`
	}
)

// Namespace is the connector extension point's (docs/reference.md#process-protocol-and-views): under the plugin protocol every method above is
// Namespace + "." + method, with the connector's id as the contribution.
const Namespace = "connector"

// InPlugin is a method's name under the plugin protocol.
func InPlugin(method string) string { return Namespace + "." + method }

// Protocol is the version of the host's connector protocol: the program answers it in its
// connector's manifest (connector.manifest), and the hub refuses a program of another version.
const Protocol = 2

// Manifest is who the connector is and its connector.Manifest, on the wire.
type Manifest struct {
	Protocol     int      `json:"protocol"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	Hosts        []string `json:"hosts,omitempty"`
	AgentTools   []string `json:"agent_tools,omitempty"`
	Unlisted     []string `json:"unlisted,omitempty"`
	// ConfirmAlways: see connector.Manifest.
	ConfirmAlways []string `json:"confirm_always,omitempty"`
	PollSeconds   int      `json:"poll_seconds,omitempty"`
	Kinds         []string `json:"kinds,omitempty"`
	// KindDescriptions: connector.Manifest.KindDescriptions.
	KindDescriptions map[string]string `json:"kind_descriptions,omitempty"`
	// ArchiveSources: connector.Manifest.ArchiveSources.
	ArchiveSources []string `json:"archive_sources,omitempty"`
	// CLI: connector.Manifest.CLI.
	CLI string `json:"cli,omitempty"`
}

type (
	LoginParams struct {
		Step   string            `json:"step,omitempty"`
		Values map[string]string `json:"values,omitempty"`
	}
	ChangesParams struct {
		Cursor string `json:"cursor,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	ChangesResult struct {
		Batches []connector.ChatBatch `json:"batches,omitempty"`
		Next    string                `json:"next,omitempty"`
		More    bool                  `json:"more,omitempty"`
	}
	CollectResult struct {
		Things []connector.InboxThing `json:"things,omitempty"`
	}
	// ReplyParams is the reply as the connector gets it.
	ReplyParams = connector.InboxReply
	ToolsResult struct {
		Tools []proto.Tool `json:"tools,omitempty"`
	}
	CallParams struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args,omitempty"`
	}
	CallResult struct {
		Text string `json:"text,omitempty"`
	}
	// DescribeResult is what the card says about a write (connector.Actions.Describe).
	DescribeResult struct {
		Action  connector.Action `json:"action"`
		Summary string           `json:"summary"`
	}
	ThreadParams struct {
		Thread string `json:"thread"`
		Before string `json:"before,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	MediaParams struct {
		Ref string `json:"ref"`
	}
	// ItemsResult is the knowledge base's items (connector.Items: documents, meetings, events).
	ItemsResult struct {
		Items []kb.Item `json:"items"`
	}
	DocTitleParams struct {
		URL string `json:"url"`
	}
	DocTitleResult struct {
		Title string `json:"title"`
	}
	// DoParams is a write the gate let through: the very args the card was made from, and the hub's id for this one
	// run, the same if the hub ever sends it again (the program runs it at most once).
	DoParams struct {
		Name      string          `json:"name"`
		Args      json.RawMessage `json:"args,omitempty"`
		RequestID string          `json:"request_id"`
	}
	SecretData struct {
		Data []byte `json:"data,omitempty"` // nil: nothing stored
	}
	ChangedEvent struct {
		Contribution string `json:"contribution,omitempty"` // the connector's id
		Scope        string `json:"scope"`
	}
)

// Init is what a connector program is started with.
type Init struct {
	Config json.RawMessage
	// Declared are the secrets the extension's manifest declares and the user filled in (an API key), by name; the
	// connector's own state still goes to the hub's store (secrets).
	Declared map[string]string
	// DataDir is the extension's own directory for caches.
	DataDir string
	// Timezone is the user's time zone by name (initialize's hub.timezone; "" while the hub does not know it).
	Timezone string
}

// BuildWith makes the connector once the hub has sent what the program is started with; secrets is the hub's store
// for this connector.
type BuildWith func(init Init, secrets connector.Secrets) (connector.Connector, error)

// Restarts of a failing Watch inside the program (the hub's Registry restarts the hub side).
var (
	watchFirst = 5 * time.Second
	watchLast  = 5 * time.Minute
)

// Program is build's connector as an extension program; id, version and manifest are the extension's
// (persona-extension.json), answered to initialize and manifest.
func Program(id, version string, manifest json.RawMessage, build BuildWith) ext.Program {
	s := &server{build: build}
	return ext.Program{ID: id, Version: version, Manifest: manifest, Init: s.initialize, Namespaces: map[string]ext.Handler{Namespace: s.handle}}
}

// MainWith serves build's connector on in / out until the hub closes them (the program then exits) or ctx ends. What
// the connector logs goes to the standard logger (the program's stderr, which the hub keeps).
func MainWith(ctx context.Context, in io.Reader, out io.Writer, id, version string, manifest json.RawMessage, build BuildWith) error {
	return ext.Serve(ctx, in, out, Program(id, version, manifest, build))
}

type server struct {
	build BuildWith
	// ctx is the program's (the connector's Run and Watch end with it), hub the SDK's: both set by initialize, which
	// the SDK runs before any other call.
	ctx context.Context
	hub *ext.Hub

	mu sync.Mutex
	c  connector.Connector
	// cid is the connector's id, for the secrets' calls: what initialize said is active until the connector is built,
	// then its own. Atomic: the secrets read it while init holds mu (a connector may load its secret while it is built).
	cid atomic.Pointer[string]
	// once runs each actions.do once by its request id.
	once writes
}

// handle answers the connector namespace's calls (method without it: "status" for connector.status).
func (s *server) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	s.mu.Lock()
	c := s.c
	s.mu.Unlock()
	if c == nil {
		return nil, &ext.Error{Code: wire.ErrorCodeNotInitialized, Message: "not initialized"}
	}
	if want := strings.TrimSpace(ext.Contribution(params)); want != "" && want != c.ID() {
		return nil, &ext.Error{Code: wire.ErrorCodeNoContribution, Message: "no connector " + want}
	}
	switch method {
	case MManifest:
		return ManifestOf(c), nil
	case MStatus:
		return connector.StatusOf(ctx, c), nil
	case MLoginStep:
		l, ok := connector.As[connector.Login](c)
		if !ok {
			return nil, noCapability(c, "login")
		}
		var p LoginParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return l.LoginStep(ctx, p.Step, p.Values)
	case MLogout:
		l, ok := connector.As[connector.Login](c)
		if !ok {
			return nil, noCapability(c, "login")
		}
		return struct{}{}, l.Logout(ctx)
	case MChanges:
		a, ok := connector.As[connector.Archive](c)
		if !ok {
			return nil, noCapability(c, "archive")
		}
		var p ChangesParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		b, next, more, err := a.Changes(ctx, p.Cursor, p.Limit)
		return ChangesResult{Batches: b, Next: next, More: more}, err
	case MCollect:
		in, ok := connector.As[connector.Inbox](c)
		if !ok {
			return nil, noCapability(c, "inbox")
		}
		things, err := in.Collect(ctx)
		return CollectResult{Things: things}, err
	case MReply:
		in, ok := connector.As[connector.Inbox](c)
		if !ok {
			return nil, noCapability(c, "inbox")
		}
		var p ReplyParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return struct{}{}, in.Reply(ctx, p)
	case MTools:
		t, ok := connector.As[connector.ReadTools](c)
		if !ok {
			return ToolsResult{}, nil
		}
		return ToolsResult{Tools: t.Tools()}, nil
	case MCall:
		t, ok := connector.As[connector.ReadTools](c)
		if !ok {
			return nil, noCapability(c, "tools")
		}
		var p CallParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		text, err := t.Call(ctx, p.Name, p.Args)
		return CallResult{Text: text}, err
	case MActions:
		a, ok := connector.As[connector.Actions](c)
		if !ok {
			return ToolsResult{}, nil
		}
		return ToolsResult{Tools: a.Actions()}, nil
	case MDescribe:
		a, ok := connector.As[connector.Actions](c)
		if !ok {
			return nil, noCapability(c, "actions")
		}
		var p CallParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		act, summary, err := a.Describe(ctx, p.Name, p.Args)
		return DescribeResult{Action: act, Summary: summary}, err
	case MThread:
		th, ok := connector.As[connector.Threads](c)
		if !ok {
			return nil, noCapability(c, "threads")
		}
		var p ThreadParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return th.Thread(ctx, p.Thread, p.Before, min(max(p.Limit, 1), connector.ThreadMost))
	case MSnapshot:
		sn, ok := connector.As[connector.Snapshot](c)
		if !ok {
			return nil, noCapability(c, "snapshot")
		}
		var p SnapshotParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return sn.Snapshot(ctx, p.TZ)
	case MItems:
		is, ok := connector.As[connector.Items](c)
		if !ok {
			return nil, noCapability(c, "items")
		}
		items, err := is.ItemSource().Items(ctx)
		if items == nil {
			items = []kb.Item{}
		}
		return ItemsResult{Items: items}, err
	case MDocTitle:
		is, ok := connector.As[connector.Items](c)
		if !ok {
			return nil, noCapability(c, "items")
		}
		var p DocTitleParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return DocTitleResult{Title: is.ItemSource().DocTitle(ctx, p.URL)}, nil
	case MArchiveMedia:
		am, ok := c.(connector.ArchiveMedia)
		if _, archive := connector.As[connector.Archive](c); !ok || !archive {
			return nil, noCapability(c, "archive")
		}
		var p MediaParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		m, err := am.ArchiveMedia(ctx, p.Ref)
		if err == nil && len(m.Data) > connector.ArchiveMediaMost {
			return nil, fmt.Errorf("attachment %d bytes, more than %d", len(m.Data), connector.ArchiveMediaMost)
		}
		return m, err
	case MMedia:
		th, ok := connector.As[connector.Threads](c)
		if !ok {
			return nil, noCapability(c, "threads")
		}
		var p MediaParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		m, err := th.Media(ctx, p.Ref)
		if err == nil && len(m.Data) > connector.MediaMost {
			return nil, fmt.Errorf("media %d bytes, more than %d", len(m.Data), connector.MediaMost)
		}
		return m, err
	case MDo:
		a, ok := connector.As[connector.Actions](c)
		if !ok {
			return nil, noCapability(c, "actions")
		}
		var p DoParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.RequestID == "" {
			return nil, errors.New("actions.do without a request_id")
		}
		return s.once.do(p.RequestID, func() (CallResult, error) {
			text, err := a.Do(ctx, p.Name, p.Args)
			return CallResult{Text: text}, err
		})
	}
	return nil, ext.UnknownMethod(InPlugin(method))
}

// initialize builds the connector (the SDK's Init: once, before any other call) and contributes it.
func (s *server) initialize(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	s.ctx, s.hub = ctx, hub
	if ids := in.Active["connectors"]; len(ids) == 1 {
		s.cid.Store(&ids[0])
	}
	m, err := s.init(Init{Config: in.InitializeParams.Config, Declared: in.Secrets, DataDir: in.Info().DataDir, Timezone: in.Hub.Timezone})
	if err != nil {
		return nil, err
	}
	return map[string]any{"connectors": []wire.ConnectorItem{ItemOf(m)}}, nil
}

// ItemOf is a connector's manifest as a contribution to the connectors extension point.
func ItemOf(m Manifest) wire.ConnectorItem {
	return wire.ConnectorItem{ID: m.ID, Name: m.Name, Capabilities: m.Capabilities, Hosts: m.Hosts, AgentTools: m.AgentTools,
		Unlisted: m.Unlisted, ConfirmAlways: m.ConfirmAlways, PollSeconds: m.PollSeconds, Kinds: m.Kinds, KindDescriptions: m.KindDescriptions,
		ArchiveSources: m.ArchiveSources, CLI: m.CLI}
}

// init builds the connector the first time and starts its Run and Watch; a second init answers the same.
func (s *server) init(init Init) (Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		return ManifestOf(s.c), nil
	}
	c, err := s.build(init, &secrets{s: s})
	if err != nil {
		return Manifest{}, err
	}
	if err := connector.Check(c); err != nil {
		return Manifest{}, err
	}
	s.c = c
	id := c.ID()
	s.cid.Store(&id)
	if r, ok := c.(connector.Runner); ok {
		go r.Run(s.ctx)
	}
	if e, ok := c.(connector.Events); ok {
		go s.watch(e)
	}
	return ManifestOf(c), nil
}

// watch passes the connector's changes on to the hub, starting a Watch that fails again after a backoff.
func (s *server) watch(e connector.Events) {
	wait := watchFirst
	for s.ctx.Err() == nil {
		start := time.Now()
		err := e.Watch(s.ctx, func(scope connector.Scope) { s.notifyChanged(scope) })
		if s.ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			wait = watchFirst
		}
		log.Printf("watch stopped (%v), again in %s", err, wait)
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, watchLast)
	}
}

// doneMost is how many writes the program remembers by request id.
const doneMost = 256

// writes runs each write (actions.do) once by its request id: the same id again answers what the first run did,
// waiting for it if it is still running.
type writes struct {
	mu    sync.Mutex
	runs  map[string]*write
	order []string
}

type write struct {
	done chan struct{}
	res  CallResult
	err  error
}

func (w *writes) do(id string, run func() (CallResult, error)) (CallResult, error) {
	w.mu.Lock()
	if prev, ok := w.runs[id]; ok {
		w.mu.Unlock()
		<-prev.done
		return prev.res, prev.err
	}
	if w.runs == nil {
		w.runs = map[string]*write{}
	}
	cur := &write{done: make(chan struct{})}
	w.runs[id] = cur
	w.order = append(w.order, id)
	if len(w.order) > doneMost {
		delete(w.runs, w.order[0])
		w.order = w.order[1:]
	}
	w.mu.Unlock()
	cur.res, cur.err = run()
	close(cur.done)
	return cur.res, cur.err
}

// ManifestOf is c's manifest on the wire.
func ManifestOf(c connector.Connector) Manifest {
	cm := c.Manifest()
	m := Manifest{Protocol: Protocol, ID: c.ID(), Name: c.Name(), Capabilities: []string{}, Hosts: cm.Hosts, AgentTools: cm.AgentTools,
		Unlisted: cm.Unlisted, ConfirmAlways: cm.ConfirmAlways, PollSeconds: int(cm.Poll / time.Second), Kinds: cm.Kinds,
		KindDescriptions: cm.KindDescriptions, ArchiveSources: cm.ArchiveSources, CLI: cm.CLI}
	for _, cp := range cm.Capabilities {
		m.Capabilities = append(m.Capabilities, string(cp))
	}
	return m
}

// Of is the connector.Manifest a program sent (a proxy's: it answers for the program).
func (m Manifest) Of() connector.Manifest {
	out := connector.Manifest{Hosts: m.Hosts, AgentTools: m.AgentTools, Unlisted: m.Unlisted, ConfirmAlways: m.ConfirmAlways,
		Poll: time.Duration(m.PollSeconds) * time.Second, Kinds: m.Kinds, KindDescriptions: m.KindDescriptions, ArchiveSources: m.ArchiveSources,
		CLI: m.CLI, Proxy: true}
	for _, cp := range m.Capabilities {
		out.Capabilities = append(out.Capabilities, connector.Capability(cp))
	}
	return out
}

func noCapability(c connector.Connector, what string) error {
	return &ext.Error{Code: wire.ErrorCodeNoCapability, Message: fmt.Sprintf("%s has no %s", c.Name(), what)}
}

// notifyChanged tells the hub something changed (connector.changed).
func (s *server) notifyChanged(scope connector.Scope) {
	s.mu.Lock()
	c := s.c
	s.mu.Unlock()
	if c != nil {
		_ = s.hub.Notify(InPlugin(EChanged), ChangedEvent{Contribution: c.ID(), Scope: string(scope)})
	}
}

// secrets is the hub's store for this program's connector, over the pipe.
type secrets struct{ s *server }

// call calls the hub's connector.<method> for this connector (params with its id as the contribution).
func (x *secrets) call(ctx context.Context, method string, params, out any) error {
	id := ""
	if p := x.s.cid.Load(); p != nil {
		id = *p
	}
	p, err := targeted(params, id)
	if err != nil {
		return err
	}
	return x.s.hub.Call(ctx, InPlugin(method), p, out)
}

// targeted is params with "contribution" added (params must be an object, or nil).
func targeted(params any, contribution string) (json.RawMessage, error) {
	b, err := json.Marshal(params)
	if err != nil || contribution == "" {
		return b, err
	}
	m := map[string]json.RawMessage{}
	if string(b) != "null" {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("params of a namespaced call must be an object: %w", err)
		}
	}
	m["contribution"], _ = json.Marshal(contribution)
	return json.Marshal(m)
}

func (x *secrets) Load(ctx context.Context) ([]byte, error) {
	var out SecretData
	err := x.call(ctx, MSecretsLoad, struct{}{}, &out)
	return out.Data, err
}

func (x *secrets) Save(ctx context.Context, data []byte) error {
	return x.call(ctx, MSecretsSave, SecretData{Data: data}, nil)
}

func (x *secrets) Delete(ctx context.Context) error {
	return x.call(ctx, MSecretsDelete, struct{}{}, nil)
}
