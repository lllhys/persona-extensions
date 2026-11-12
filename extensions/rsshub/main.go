// Command rsshub is a Persona extension (docs/extensions/rsshub.md) for RSSHub (https://docs.rsshub.app) routes and
// any RSS, Atom or JSON feed:
//
//   - ui (nav_page "feeds"): the subscriptions grouped by tag, their unread entries and text, a refresh and mark-read
//     on each, and a preview of any route before subscribing; the nav entry's badge is the unread count.
//   - connectors (id rsshub): the read tools rsshub_feeds, rsshub_entries and rsshub_preview, and the writes
//     rsshub_subscribe, rsshub_unsubscribe (both confirmed on the phone every time) and rsshub_mark_read.
//
// The hub's machine reads the instance named in the settings (the public https://rsshub.app by default, or a
// self-hosted one with its access key) and refreshes every subscription in the background with conditional requests.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

// about is the manifest's id and version.
var about = func() (m struct{ ID, Version string }) {
	_ = json.Unmarshal(manifest, &m)
	return m
}()

// The contributions' ids (persona-extension.json).
const (
	pageUI      = "feeds"
	connectorID = "rsshub"
)

const (
	defaultRefresh = 30
	refreshFloor   = 5
	refreshCeil    = 1440
	refreshWorkers = 4
	previewMost    = 30
	previewsKept   = 8 // previews the page keeps in memory
)

// config is the extension's settings (the manifest's form).
type config struct {
	InstanceURL    string `json:"instance_url,omitempty"`
	AccessAuth     string `json:"access_auth,omitempty"`
	RefreshMinutes int    `json:"refresh_minutes,omitempty"`
	PreferJSON     bool   `json:"prefer_json,omitempty"`
}

// notifier is what the program tells the hub: the nav badge and that the page changed.
type notifier interface {
	Badge(contribution, text, tone string) error
	Invalidate(contribution string) error
}

// app is the running extension.
type app struct {
	logf   func(string, ...any)
	client *http.Client
	now    func() time.Time
	getenv func(string) string

	mu         sync.Mutex
	inst       instance
	cfgErr     error
	every      time.Duration
	store      *store
	hub        notifier
	cancel     context.CancelFunc
	lastOK     time.Time // the instance last answered
	lastErr    error     // and last failed
	lastErrAt  time.Time
	refreshing bool

	previews sync.Map // source -> *preview, the page's last previews
}

func newApp(logf func(string, ...any)) *app {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	base, _ := parseInstance("")
	return &app{logf: logf, client: newClient(), now: time.Now, getenv: os.Getenv,
		inst: instance{base: base}, every: defaultRefresh * time.Minute, store: openStore("", logf)}
}

func main() {
	log.SetFlags(0)
	ext.Main(program(newApp(log.Printf)))
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init:       a.init,
		UI:         map[string]ext.Page{pageUI: {Render: a.render, Action: a.action}},
		Namespaces: map[string]ext.Handler{"connector": a.connector},
	}
}

func (a *app) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	var cfg config
	if err := in.Config(&cfg); err != nil {
		return nil, err
	}
	a.configure(cfg, in.Secrets["access_key"])
	st := openStore(in.Info().DataDir, a.logf)
	a.mu.Lock()
	a.store = st
	if hub != nil {
		a.hub = hub
	}
	if a.cancel != nil {
		a.cancel()
	}
	loop, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	every, cfgErr := a.every, a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		a.logf("rsshub: %v", cfgErr)
	} else {
		a.logf("rsshub: instance %s, refreshing every %s", a.instance().base.Host, every)
		go a.poll(loop, every)
	}
	a.badge()
	return nil, nil
}

// configure applies the settings; an invalid instance address is kept as the reason nothing is read.
func (a *app) configure(cfg config, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfgErr = nil
	base, err := parseInstance(cfg.InstanceURL)
	if err != nil {
		a.cfgErr = err
		base, _ = parseInstance("")
	}
	a.inst = instance{base: base, key: key, code: cfg.AccessAuth == "code", json: cfg.PreferJSON}
	m := cfg.RefreshMinutes
	if m == 0 {
		m = defaultRefresh
	}
	a.every = time.Duration(min(max(m, refreshFloor), refreshCeil)) * time.Minute
}

func (a *app) instance() instance {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inst
}

func (a *app) feeds() *store {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store
}

// poll refreshes every feed now and then every period, until ctx ends.
func (a *app) poll(ctx context.Context, every time.Duration) {
	t := time.NewTimer(5 * time.Second) // let the hub finish starting first
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.refreshAll(ctx)
		t.Reset(every)
	}
}

// refreshAll reads every feed, a few at a time; a second call while one runs does nothing.
func (a *app) refreshAll(ctx context.Context) (fresh, failed int) {
	a.mu.Lock()
	if a.refreshing || a.cfgErr != nil {
		a.mu.Unlock()
		return 0, 0
	}
	a.refreshing = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.refreshing = false
		a.mu.Unlock()
	}()
	feeds := a.feeds().list()
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan string)
	for range min(refreshWorkers, max(len(feeds), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				n, err := a.refresh(ctx, id)
				mu.Lock()
				fresh += n
				if err != nil {
					failed++
				}
				mu.Unlock()
			}
		}()
	}
	for _, f := range feeds {
		select {
		case work <- f.ID:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	a.badge()
	a.invalidate()
	return fresh, failed
}

// refresh reads one feed, conditionally, and keeps what it found. It answers the number of new entries.
func (a *app) refresh(ctx context.Context, id string) (int, error) {
	st := a.feeds()
	f, ok := st.get(id)
	if !ok {
		return 0, fmt.Errorf("没有订阅 %s", id)
	}
	inst := a.instance()
	src, err := parseSource(f.Source, inst.base)
	if err != nil {
		st.update(id, nil, nil, err, a.now())
		return 0, err
	}
	res, parsed, err := a.read(ctx, inst, src, f.ETag, f.LastModified)
	return st.update(id, res, parsed, err, a.now()), err
}

// read fetches and parses src; a 304 has no parsed feed.
func (a *app) read(ctx context.Context, inst instance, src source, etag, lastModified string) (*fetchResult, *parsedFeed, error) {
	u, err := inst.url(src)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	res, err := fetch(ctx, a.client, dialURL(u, a.getenv), etag, lastModified, about.Version)
	if src.Route != "" {
		a.instanceAnswered(err)
	}
	if err != nil {
		return nil, nil, errors.New(inst.redact(err.Error()))
	}
	if res.NotModified {
		return res, nil, nil
	}
	parsed, err := parseFeed(res.Body)
	if err != nil {
		return nil, nil, errors.New(inst.redact(err.Error()))
	}
	inst.scrub(parsed)
	return res, parsed, nil
}

// instanceAnswered remembers how the instance last answered (a 4xx/5xx is an answer: it is reachable).
func (a *app) instanceAnswered(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var he *httpError
	if err == nil || (errors.As(err, &he) && he.Status != http.StatusUnauthorized && he.Status != http.StatusForbidden) {
		a.lastOK = a.now()
		return
	}
	a.lastErr, a.lastErrAt = errors.New(a.inst.redact(err.Error())), a.now()
}

// preview is a route read without subscribing.
type preview struct {
	Source string
	Link   string
	At     time.Time
	Feed   *parsedFeed
	Err    string
}

// preview reads src without keeping anything.
func (a *app) preview(ctx context.Context, raw string) (*preview, error) {
	inst := a.instance()
	src, err := parseSource(raw, inst.base)
	if err != nil {
		return nil, err
	}
	p := &preview{Source: src.String(), Link: inst.link(src), At: a.now()}
	_, parsed, err := a.read(ctx, inst, src, "", "")
	if err != nil {
		p.Err = err.Error()
	} else {
		p.Feed = parsed
	}
	return p, nil
}

// keepPreview keeps p for the page to draw, with at most previewsKept previews in memory.
func (a *app) keepPreview(p *preview) {
	n := 0
	a.previews.Range(func(k, _ any) bool {
		if n++; n >= previewsKept {
			a.previews.Delete(k)
		}
		return true
	})
	a.previews.Store(p.Source, p)
}

// subscribe reads src once and, when it is a feed, subscribes to it with what it read.
func (a *app) subscribe(ctx context.Context, raw, title, tag string) (feed, bool, int, error) {
	inst := a.instance()
	src, err := parseSource(raw, inst.base)
	if err != nil {
		return feed{}, false, 0, err
	}
	st := a.feeds()
	if f, ok := st.bySource(src.String()); ok {
		return f, false, 0, nil
	}
	res, parsed, err := a.read(ctx, inst, src, "", "")
	if err != nil {
		return feed{}, false, 0, fmt.Errorf("没有订阅：%s 读不到（%v）", src, err)
	}
	f, added, err := st.add(src.String(), title, tag, a.now())
	if err != nil || !added {
		return f, false, 0, err
	}
	n := st.update(f.ID, res, parsed, nil, a.now())
	f, _ = st.get(f.ID)
	a.badge()
	a.invalidate()
	return f, true, n, nil
}

// unsubscribe drops the feed with id and its entries.
func (a *app) unsubscribe(id string) (feed, error) {
	f, ok := a.feeds().remove(id)
	if !ok {
		return feed{}, fmt.Errorf("没有订阅 %s（id 从 rsshub_feeds 拿）", id)
	}
	a.badge()
	a.invalidate()
	return f, nil
}

func (a *app) markRead(feedID string, ids []string, all, read bool) (int, error) {
	n, err := a.feeds().markRead(feedID, ids, all, read)
	if err == nil && n > 0 {
		a.badge()
		a.invalidate()
	}
	return n, err
}

// badge puts the unread count on the nav entry (none at 0).
func (a *app) badge() {
	a.mu.Lock()
	hub := a.hub
	a.mu.Unlock()
	if hub == nil {
		return
	}
	text := ""
	if n := a.feeds().unread(); n > 999 {
		text = "999+"
	} else if n > 0 {
		text = strconv.Itoa(n)
	}
	_ = hub.Badge(pageUI, text, "neutral")
}

func (a *app) invalidate() {
	a.mu.Lock()
	hub := a.hub
	a.mu.Unlock()
	if hub != nil {
		_ = hub.Invalidate(pageUI)
	}
}

// health is how the instance is doing, for the page and the status.
type health struct {
	Host   string
	OK     time.Time
	Err    error
	ErrAt  time.Time
	Every  time.Duration
	Config error
	HasKey bool
}

func (a *app) health() health {
	a.mu.Lock()
	defer a.mu.Unlock()
	return health{Host: a.inst.base.Host + a.inst.base.Path, OK: a.lastOK, Err: a.lastErr, ErrAt: a.lastErrAt,
		Every: a.every, Config: a.cfgErr, HasKey: a.inst.key != ""}
}

// failing reports whether the instance's last answer was a failure.
func (h health) failing() bool { return h.Err != nil && h.ErrAt.After(h.OK) }
