// Package x is the X connector: the user's own X account read through the official X API v2 (api.x.com) with their
// own developer app's keys (OAuth 1.0a user context). It keeps who the user follows, a few days of their home
// timeline or lists within a daily post budget, and their mentions; the inbox gets the mentions, the assistant the
// x_* read tools, the page an organized digest (digest.go). It never writes to X, never scrapes x.com and never
// uses cookies or private endpoints.
//
// The keys live only in the hub's sealed connector secret and this process's memory: never in a log, a status, an
// error, a tool result or the page.
package x

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
)

// ID is the connector's (persona-extension.json).
const ID = "x"

// Settings is the extension's configuration (persona-extension.json's form).
type Settings struct {
	TriageRules     string   `json:"triage_rules,omitempty"`
	FeedSource      string   `json:"feed_source,omitempty"` // home | lists | off
	ListIDs         []string `json:"list_ids,omitempty"`
	DailyPostBudget *int     `json:"daily_post_budget,omitempty"`
	RetentionDays   *int     `json:"retention_days,omitempty"`
	Proxy           string   `json:"proxy,omitempty"`
	// The Grok source (grok.go, grokdigest.go).
	Source             string   `json:"source,omitempty"` // auto | x_api | grok
	GrokPath           string   `json:"grok_path,omitempty"`
	Handles            []string `json:"handles,omitempty"`
	Topics             []string `json:"topics,omitempty"`
	GrokEveryHours     *int     `json:"grok_every_hours,omitempty"`
	GrokSearchesPerDay *int     `json:"grok_searches_per_day,omitempty"`
}

// Feed sources.
const (
	FeedHome  = "home"
	FeedLists = "lists"
	FeedOff   = "off"
)

// Defaults and bounds of the settings (the manifest's).
const (
	defaultBudget    = 200
	maxBudget        = 5000
	defaultRetention = 3
	maxRetention     = 7
	// mentionDays is how long mentions are kept and how far back they are read.
	mentionDays = 8
)

// budget is the day's post budget, within its bounds.
func (s Settings) budget() int {
	if s.DailyPostBudget == nil {
		return defaultBudget
	}
	return min(max(*s.DailyPostBudget, 0), maxBudget)
}

// retention is how long posts are kept.
func (s Settings) retention() time.Duration {
	d := defaultRetention
	if s.RetentionDays != nil {
		d = min(max(*s.RetentionDays, 1), maxRetention)
	}
	return time.Duration(d) * 24 * time.Hour
}

// source is the feed's source; a budget of 0 turns the feed off.
func (s Settings) source() string {
	if s.budget() == 0 {
		return FeedOff
	}
	switch s.FeedSource {
	case FeedLists, FeedOff:
		return s.FeedSource
	}
	return FeedHome
}

// lists are the list ids, only the numeric ones.
func (s Settings) lists() []string {
	var out []string
	for _, id := range s.ListIDs {
		if validID(id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// validID: X's ids are decimal numbers.
func validID(id string) bool {
	if id == "" || len(id) > 20 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Options are what the connector is made with besides its settings.
type Options struct {
	// Base is the API's base URL (DefaultBase when empty; a fake server in tests).
	Base string
	// DataDir is the extension's data directory ("" keeps everything in memory).
	DataDir string
	// Locale is the user's language for what people read; Timezone the user's, for times.
	Locale, Timezone string
	// Now is the clock (time.Now when nil).
	Now func() time.Time
	// Grok runs grok (ExecGrok when nil); FindGrok finds it for grok_path (LookupGrok when nil).
	Grok     GrokRunner
	FindGrok func(configured string) string
}

// Connector is X.
type Connector struct {
	cfg   Settings
	tx    text
	loc   *time.Location
	api   *client
	store store
	now   func() time.Time
	rules string

	// disk serializes writing the files against wiping them (signing out, or in as another account), so a save
	// that took its snapshot before the sign-out cannot put the files back after the wipe. Taken before mu.
	disk sync.Mutex

	mu      sync.Mutex
	secrets connector.Secrets
	notify  func()
	loaded  bool // the secret and the files were read
	keys    Keys
	me      Account // id, username, name of the signed-in user
	// status is the last sync's outcome (Status adds the account); rateUntil: when a 429's window ends.
	status    connector.Status
	rateUntil time.Time
	following []Account
	posts     map[string]Post
	mentions  map[string]Post
	state     syncState
	// reserved is the budget held by requests in flight (their worst case), given back when they answer: what is
	// left for another request is the budget less what was spent and what is reserved.
	reserved int
	syncing  bool
	// gen counts sign-ins and sign-outs: a sync applies what it read only to the sign-in it started with.
	gen  int
	poke chan struct{}

	// The Grok source (grokdigest.go): its runner and how it is found, where grok is ("" none, as last looked up),
	// whether it was looked up, what is kept (grok_digest.json) and whether it was read, a refresh running, and the
	// refresh loop's poke.
	locale     string
	grokRun    GrokRunner
	findGrok   func(string) string
	grokPath   string
	grokLooked bool
	grok       grokState
	grokLoaded bool // grok_digest.json was read (loadGrok)
	refreshing bool
	grokPoke   chan bool
}

// New is the connector for the settings.
func New(cfg Settings, o Options) *Connector {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	base := o.Base
	if base == "" {
		base = DefaultBase
	}
	loc, err := time.LoadLocation(o.Timezone)
	if err != nil || o.Timezone == "" {
		loc = time.Local
	}
	run, find := o.Grok, o.FindGrok
	if run == nil {
		run = ExecGrok{}
	}
	if find == nil {
		find = func(p string) string { return LookupGrok(p) }
	}
	return &Connector{
		locale: o.Locale, grokRun: run, findGrok: find, grokPoke: make(chan bool, 1),
		cfg: cfg, tx: texts(o.Locale), loc: loc, now: now, store: store{dir: o.DataDir},
		api:   &client{base: base, http: httpClient(cfg), now: now, limits: map[string]limit{}},
		posts: map[string]Post{}, mentions: map[string]Post{}, poke: make(chan struct{}, 1),
		status: connector.Status{State: connector.StateNotSignedIn},
	}
}

// refuse is the transport of a connector whose proxy setting cannot be used: every request fails, none goes direct.
type refuse struct{ err error }

func (r refuse) RoundTrip(*http.Request) (*http.Response, error) { return nil, r.err }

func httpClient(cfg Settings) *http.Client {
	const timeout = 30 * time.Second
	cl, err := connector.Egress{Proxy: cfg.Proxy}.HTTPClient(timeout)
	if err != nil {
		return &http.Client{Timeout: timeout, Transport: refuse{fmt.Errorf("proxy setting: %w", err)}}
	}
	return cl
}

// WithRules sets the user's triage rules, carried on every inbox thing.
func (c *Connector) WithRules(rules string) *Connector { c.rules = rules; return c }

// UseSecrets gives the connector the hub's sealed store for its keys.
func (c *Connector) UseSecrets(s connector.Secrets) {
	c.mu.Lock()
	c.secrets = s
	c.mu.Unlock()
}

// OnInboxChange is called when new mentions came in (the program tells the hub: connector.changed {scope: inbox}).
func (c *Connector) OnInboxChange(f func()) {
	c.mu.Lock()
	c.notify = f
	c.mu.Unlock()
}

func (c *Connector) ID() string   { return ID }
func (c *Connector) Name() string { return "X" }

// KindDescriptions are the inbox kinds as the models read them.
var KindDescriptions = map[string]string{"mention": "X 上 @ 了用户或回复了用户的帖子"}

func (c *Connector) Manifest() connector.Manifest {
	return connector.Manifest{
		Capabilities:     []connector.Capability{connector.CapStatus, connector.CapTools, connector.CapInbox, connector.CapLogin, connector.CapFallback},
		Hosts:            []string{"x.com", "twitter.com"},
		Kinds:            []string{"mention"},
		KindDescriptions: KindDescriptions,
	}
}

// Status answers from memory.
func (c *Connector) Status(context.Context) connector.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Connector) statusLocked() connector.Status {
	if !c.keys.complete() {
		if c.cfg.dataSource() != SourceXAPI {
			return c.grokStatusLocked()
		}
		return connector.Status{State: connector.StateNotSignedIn, Error: c.tx.notSignedIn, Guide: []string{c.tx.notSignedInGuide}}
	}
	st := c.status
	if st.State == connector.StateRateLimited && !c.now().Before(c.rateUntil) {
		st = connector.Status{State: connector.StateOK}
	}
	if st.State == "" || st.State == connector.StateNotSignedIn {
		st.State = connector.StateOK
	}
	st.Account = "@" + c.me.Username
	st.LastSync = c.lastSyncLocked()
	return st
}

func (c *Connector) lastSyncLocked() time.Time {
	t := c.state.MentionsAt
	if c.state.FeedAt.After(t) {
		t = c.state.FeedAt
	}
	return t
}

// signedIn says whether keys are there.
func (c *Connector) signedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.keys.complete()
}

// Intervals of the loop.
var (
	syncEvery      = 15 * time.Minute
	followingEvery = 7 * 24 * time.Hour
	// retryFirst / retryLast bound the backoff after a failed sync; waitMost bounds any wait (a 429's reset).
	retryFirst = time.Minute
	retryLast  = 15 * time.Minute
	waitMost   = 15 * time.Minute
	// syncBudget bounds one sync.
	syncBudget = 3 * time.Minute
)

// Run reads the keys and what was kept, then syncs every syncEvery until ctx ends (sooner when poked: signed in,
// refresh asked for).
func (c *Connector) Run(ctx context.Context) {
	c.load(ctx)
	done := make(chan struct{})
	go func() { defer close(done); c.runGrok(ctx) }()
	defer func() { <-done }()
	retry := retryFirst
	for ctx.Err() == nil {
		wait := syncEvery
		if c.signedIn() {
			sctx, cancel := context.WithTimeout(ctx, syncBudget)
			err := c.Sync(sctx)
			cancel()
			var rl *rateLimited
			switch {
			case ctx.Err() != nil:
				return
			case errors.As(err, &rl):
				wait = min(max(time.Until(rl.Until), time.Minute), waitMost)
			case errors.Is(err, errUnauthorized) || errors.Is(err, errCredits):
				wait = syncEvery // nothing to retry soon: the user has to act
			case err != nil:
				wait, retry = retry, min(retry*2, retryLast)
			default:
				retry = retryFirst
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-c.poke:
		case <-t.C:
		}
		t.Stop()
	}
}

// Poke asks the loop to sync now.
func (c *Connector) Poke() {
	select {
	case c.poke <- struct{}{}:
	default:
	}
}

// signIn is what the secret keeps: the keys and who they sign in as.
type signIn struct {
	Keys
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
}

// Load reads the sign-in and what was kept, and looks for grok (Run starts with it; tests call it).
func (c *Connector) Load(ctx context.Context) { c.load(ctx); c.lookGrok() }

// load reads the secret and the files once. A sign-in or sign-out while it reads wins: what it read is dropped.
func (c *Connector) load(ctx context.Context) {
	c.mu.Lock()
	if c.loaded {
		c.mu.Unlock()
		return
	}
	s, gen := c.secrets, c.gen
	c.mu.Unlock()
	var in signIn
	if s != nil {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		raw, err := s.Load(lctx)
		cancel()
		if err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &in)
		}
	}
	var (
		following []Account
		posts     []Post
		mentions  []Post
		st        syncState
	)
	c.disk.Lock()
	for name, v := range map[string]any{fileFollowing: &following, filePosts: &posts, fileMentions: &mentions, fileState: &st} {
		if err := c.store.read(name, v); err != nil {
			log.Printf("%s unreadable, starting it over: %v", name, err)
		}
	}
	c.disk.Unlock()
	c.loadGrok() // not the sign-in's: read whatever the sign-in did meanwhile
	c.lookGrok()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded || c.gen != gen {
		return // signed in or out meanwhile: that is what holds now
	}
	c.loaded = true
	if in.complete() && in.UserID != "" && !c.keys.complete() {
		c.keys, c.me = in.Keys, Account{ID: in.UserID, Username: in.Username, Name: in.Name}
	}
	c.following, c.state = following, st
	for _, p := range posts {
		c.posts[p.ID] = p
	}
	for _, p := range mentions {
		c.mentions[p.ID] = p
	}
}

// save writes the files, if the sign-in is still gen. The snapshot and the writes happen under c.disk, so a wipe
// waits for them, and a save that comes after the sign-out finds gen changed and writes nothing.
func (c *Connector) save(gen int) error {
	c.disk.Lock()
	defer c.disk.Unlock()
	c.mu.Lock()
	if gen != c.gen {
		c.mu.Unlock()
		return nil
	}
	following, st := slices.Clone(c.following), c.state
	st.Replied = maps.Clone(c.state.Replied)
	posts, mentions := sortedPosts(c.posts), sortedPosts(c.mentions)
	c.mu.Unlock()
	if beforeWrite != nil {
		beforeWrite()
	}
	return errors.Join(c.store.write(fileFollowing, following), c.store.write(filePosts, posts),
		c.store.write(fileMentions, mentions), c.store.write(fileState, st))
}

// beforeWrite, in tests, runs between save's snapshot and its writes.
var beforeWrite func()

// sortedPosts are m's posts newest first.
func sortedPosts(m map[string]Post) []Post {
	out := slices.Collect(maps.Values(m))
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// clock is t in the user's time zone, for people.
func (c *Connector) clock(t time.Time) string { return t.In(c.loc).Format("15:04") }
