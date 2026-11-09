package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const connectorID = "discord"

// invitePermissions are what the invite link asks for in a server: View Channels and Read Message History.
const invitePermissions = 1<<10 | 1<<16

// endpoints are where Discord is; tests point them at a fake one.
type endpoints struct {
	API string // the REST API, with its version
	Web string // links people open
}

var discordEndpoints = endpoints{API: "https://discord.com/api/v10", Web: "https://discord.com"}

// config is the extension's configuration (the manifest's form, defaults filled in by the hub).
type config struct {
	HistoryDays int `json:"history_days"`
}

func (c config) historyDays() int {
	if c.HistoryDays < 1 || c.HistoryDays > 365 {
		return 30
	}
	return c.HistoryDays
}

// signIn is what the connector keeps in the hub (connector.secrets.*, encrypted there): the bot's token and who it
// is. Nothing else holds the token: not a file, not a log, not a status or a tool's answer.
type signIn struct {
	V       int    `json:"v"`
	Token   string `json:"token"`
	AppID   string `json:"app_id"`
	BotID   string `json:"bot_id"`
	BotName string `json:"bot_name"`
	OwnerID string `json:"owner_id"`
}

// session is a signed-in bot.
type session struct {
	in    signIn
	bot   snowflake
	owner snowflake // the user: their messages are theirs ("mine")
	api   *api
	stop  context.CancelFunc

	dmu  sync.Mutex // one discovery at a time
	disc *discovery

	rmu       sync.Mutex
	roleCache map[snowflake]roleNames

	hmu  sync.Mutex
	held map[snowflake]hold // conversations left alone for now

	wake chan struct{} // asks the health check to look now

}

// hold is a conversation left alone until its next message or a time, whichever comes first: it was refused (403,
// 404), showed nothing, or its read failed.
type hold struct {
	last    snowflake // its last message then
	until   time.Time
	refused bool
}

// hold leaves v alone until its next message, or for d.
func (s *session) hold(v view, d time.Duration, refused bool) {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	now := time.Now()
	if len(s.held) >= 1000 { // channels deleted while held are never asked about again
		for id, h := range s.held {
			if !h.until.After(now) {
				delete(s.held, id)
			}
		}
	}
	s.held[v.ID] = hold{last: v.LastMessageID, until: now.Add(d), refused: refused}
}

// heldNow says whether v is left alone now.
func (s *session) heldNow(v view) bool {
	h, ok := s.holding(v)
	return ok && h.until.After(time.Now())
}

// refusedNow says whether v refused the bot, as far as it knows now.
func (s *session) refusedNow(v view) bool {
	h, ok := s.holding(v)
	return ok && h.refused
}

func (s *session) holding(v view) (hold, bool) {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	h, ok := s.held[v.ID]
	if ok && (v.LastMessageID != h.last || !h.until.After(time.Now())) {
		delete(s.held, v.ID)
		return hold{}, false
	}
	return h, ok
}

// trouble is what needs the user, or will not pass by itself: the status says it, and the hub then hides the tools
// and stops archiving. A failure of one call never is; a health check has to fail twice in a row unless Discord's
// answer is definite (the token refused, Message Content off, no server).
type trouble struct {
	state    string
	text     string
	guide    []string
	until    time.Time // zero: until a check finds it gone
	definite bool
}

// conn is the Discord connector.
type conn struct {
	ep   endpoints
	logf func(string, ...any)
	// Timings, which the tests shorten.
	healthEvery time.Duration // between health checks while all is well
	healthRetry time.Duration // between them while not
	holdFor     time.Duration // how long a conversation that refused or showed nothing is left alone, at most
	budget      time.Duration // how long one archive call reads
	bytesMost   int           // how much one archive call answers
	scanEvery   time.Duration // how often a channel's archived threads are listed (a thread archives after an hour of quiet at the earliest)
	loadRetry   time.Duration // the first wait before reading the kept sign-in again after a failure
	// ready is closed once init has run; startGate, when set, holds back reading the sign-in (tests).
	ready     chan struct{}
	startGate chan struct{}

	// set once by init
	ctx      context.Context
	hub      *ext.Hub
	cfg      config
	tx       *texts
	zone     *time.Location
	net      network
	proxyErr string

	mu       sync.Mutex
	gen      int    // counts sign-ins and logouts: a sign-in read at start that is older than one is dropped
	loaded   bool   // the sign-in was read from the hub
	loadErr  string // why it could not be
	sess     *session
	trouble  *trouble
	lastSync time.Time
}

func newConn(ep endpoints, logf func(string, ...any)) *conn {
	return &conn{ep: ep, logf: logf, ready: make(chan struct{}), tx: textsFor(""), zone: time.UTC,
		healthEvery: 10 * time.Minute, healthRetry: time.Minute, holdFor: 6 * time.Hour, budget: 25 * time.Second, bytesMost: 2 << 20,
		scanEvery: 50 * time.Minute, loadRetry: 5 * time.Second}
}

// program is the extension program.
func (c *conn) program(version string) ext.Program {
	m := ext.Methods{
		"status":          ext.Method(c.status),
		"login.step":      ext.Method(c.loginStep),
		"logout":          ext.Method(c.logout),
		"tools.list":      ext.Method(c.toolsList),
		"tools.call":      ext.Method(c.toolsCall),
		"archive.changes": ext.Method(c.changes),
	}
	return ext.Program{ID: connectorID, Version: version, Manifest: manifest, Init: c.init,
		Namespaces: map[string]ext.Handler{ext.ConnectorNamespace: m.For(connectorID)}}
}

// init reads what the hub sent and starts reading the sign-in.
func (c *conn) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	if err := in.Config(&c.cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.ctx, c.hub = ctx, hub
	c.tx = textsFor(in.Hub.Locale)
	if in.Hub.Timezone != "" {
		if z, err := time.LoadLocation(in.Hub.Timezone); err == nil {
			c.zone = z
		}
	}
	if raw := in.Secrets["proxy"]; strings.TrimSpace(raw) != "" {
		if p, err := parseProxy(raw); err != nil {
			c.proxyErr = fmt.Sprintf(c.tx.proxyBad, err)
			c.logf("discord: the proxy is not usable: %v", err)
		} else {
			c.net.proxy = p
		}
	}
	go c.start()
	close(c.ready)
	return nil, nil
}

// start reads the sign-in kept in the hub and, when there is one, starts its session. A failure to read it (the
// hub busy) is tried again, waiting longer each time; a sign-in or logout meanwhile decides instead.
func (c *conn) start() {
	if c.startGate != nil {
		<-c.startGate
	}
	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()
	wait := c.loadRetry
	for {
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		raw, err := c.hub.LoadSecret(ctx, connectorID)
		cancel()
		c.mu.Lock()
		if c.gen != gen || c.ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		c.loaded = true
		if err == nil {
			c.loadErr = ""
			var in signIn
			switch {
			case len(raw) == 0:
			case json.Unmarshal(raw, &in) != nil || in.Token == "":
				c.loadErr = c.tx.secretRejected
			default:
				c.startLocked(in)
			}
			c.mu.Unlock()
			return
		}
		c.loadErr = c.tx.secretUnreadable
		c.mu.Unlock()
		c.logf("discord: reading the sign-in: %v", err)
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 5*time.Minute)
	}
}

// startLocked starts a session for in, replacing the one there was; its first health check runs at once and says
// what is left to do. Callers hold c.mu.
func (c *conn) startLocked(in signIn) {
	c.stopLocked()
	bot, _ := parseSnowflake(in.BotID)
	owner, _ := parseSnowflake(in.OwnerID)
	ctx, cancel := context.WithCancel(c.ctx)
	s := &session{in: in, bot: bot, owner: owner, api: newAPI(c.ep.API, in.Token, version, c.net.httpClient()), stop: cancel,
		roleCache: map[snowflake]roleNames{}, held: map[snowflake]hold{}, wake: make(chan struct{}, 1)}
	c.sess, c.trouble = s, nil
	if c.proxyErr == "" {
		go c.health(ctx, s)
	}
}

// stopLocked ends the session there is. Callers hold c.mu.
func (c *conn) stopLocked() {
	if c.sess != nil {
		c.sess.stop()
		c.sess = nil
	}
}

// session is the signed-in bot, nil when there is none, the proxy setting is not usable or Discord refused the
// token: then nothing asks Discord until a new sign-in (refused requests are what get an IP banned).
func (c *conn) session() *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == nil || c.proxyErr != "" || c.trouble != nil && c.trouble.state == wire.ConnectorStateNotSignedIn {
		return nil
	}
	return c.sess
}

// setTrouble notes what is wrong for s (nil: nothing), unless s is no longer the session. Only a new sign-in clears
// a refused token.
func (c *conn) setTrouble(s *session, t *trouble) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != s || c.trouble != nil && c.trouble.state == wire.ConnectorStateNotSignedIn {
		return
	}
	c.trouble = t
}

// health checks the session now and then: the token, the Message Content intent, and that the bot is in a server.
// What the user fixed in Discord shows within a minute, and so does what broke: every healthEvery while all is
// well, every healthRetry while not. A failure that is not a definite answer (the network, Discord failing) counts
// only the second time in a row.
func (c *conn) health(ctx context.Context, s *session) {
	var wait time.Duration
	failed := 0
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-s.wake:
			t.Stop()
		}
		if c.refusedFor(s) {
			return // a call found the token refused: nothing asks Discord until a new sign-in
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		tr := c.check(cctx, s)
		cancel()
		if ctx.Err() != nil {
			return
		}
		switch {
		case tr == nil:
			failed, wait = 0, c.healthEvery
			c.setTrouble(s, nil)
		case tr.definite:
			failed, wait = 0, c.healthRetry
			c.setTrouble(s, tr)
			if tr.state == wire.ConnectorStateNotSignedIn {
				c.logf("discord: Discord refused the bot's token; waiting for a new sign-in")
				return
			}
		default:
			if failed++; failed >= 2 {
				c.setTrouble(s, tr)
			}
			wait = c.healthRetry
		}
	}
}

// checkNow asks the health check to look now (an archive call found Message Content off).
func (s *session) checkNow() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// refusedFor says whether Discord refused s's token.
func (c *conn) refusedFor(s *session) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess == s && c.trouble != nil && c.trouble.state == wire.ConnectorStateNotSignedIn
}

// check asks Discord whether the session is usable: nil when it is, else what is wrong.
func (c *conn) check(ctx context.Context, s *session) *trouble {
	app, err := s.api.application(ctx)
	if err != nil {
		return c.troubleOf(err)
	}
	if !app.contentIntent() {
		return c.intentOff()
	}
	inAny, err := s.api.inAnyGuild(ctx)
	if err != nil {
		return c.troubleOf(err)
	}
	if !inAny {
		return &trouble{state: wire.ConnectorStateNoPermission, text: c.tx.noServers, guide: []string{fmt.Sprintf(c.tx.invite, c.inviteURL(s))},
			definite: true}
	}
	return nil
}

// intentOff is the trouble of the Message Content intent turned off: Discord then shows the bot no message's
// content, so nothing is archived until it is back on.
func (c *conn) intentOff() *trouble {
	return &trouble{state: wire.ConnectorStateNoPermission, text: c.tx.intentOff, guide: c.tx.intentGuide, definite: true}
}

// troubleOf is what a failed request means for the status: a refused token, a long rate limit, or Discord out of
// reach.
func (c *conn) troubleOf(err error) *trouble {
	e, ok := asAPIError(err)
	switch {
	case !ok:
		return &trouble{state: wire.ConnectorStateError, text: fmt.Sprintf(c.tx.unreachable, clip(oneLine(err.Error()), 160)),
			guide: []string{c.tx.proxyGuide}}
	case e.Status == 401:
		return &trouble{state: wire.ConnectorStateNotSignedIn, text: c.tx.tokenRevoked, definite: true}
	case e.Status == 429:
		return &trouble{state: wire.ConnectorStateRateLimited, text: fmt.Sprintf(c.tx.rateLimited, seconds(e.RetryAfter)),
			until: time.Now().Add(e.RetryAfter)}
	}
	return &trouble{state: wire.ConnectorStateError, text: fmt.Sprintf(c.tx.unreachable, e.Error())}
}

// refused notes a 401 from any call: the token is no longer good, and nothing asks Discord until a new sign-in.
func (c *conn) refused(s *session, err error) {
	if statusOf(err) == 401 {
		c.setTrouble(s, c.troubleOf(err))
	}
}

// status answers from memory.
func (c *conn) status(_ context.Context, _ wire.ConnectorParams) (wire.ConnectorStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := wire.ConnectorStatus{LastSync: c.lastSync}
	switch {
	case c.proxyErr != "":
		st.State, st.Error = wire.ConnectorStateError, c.proxyErr
	case !c.loaded:
		st.State, st.Error = wire.ConnectorStateError, c.tx.starting
	case c.sess == nil && c.loadErr != "":
		st.State, st.Error = wire.ConnectorStateError, c.loadErr
	case c.sess == nil:
		st.State, st.Error, st.Guide = wire.ConnectorStateNotSignedIn, c.tx.notSignedIn, c.tx.signInGuide
	default:
		st.Account = fmt.Sprintf(c.tx.account, c.sess.in.BotName)
		if t := c.trouble; t != nil && (t.until.IsZero() || time.Now().Before(t.until)) {
			st.State, st.Error, st.Guide = t.state, t.text, t.guide
		} else {
			st.State = wire.ConnectorStateOk
		}
	}
	return st, nil
}

// inviteURL is the link that adds the bot to a server, asking only for View Channels and Read Message History.
func (c *conn) inviteURL(s *session) string {
	return fmt.Sprintf("%s/oauth2/authorize?client_id=%s&scope=bot&permissions=%d&integration_type=0", c.ep.Web, s.in.AppID, invitePermissions)
}

// loginStep is the sign-in: one step, the bot token. It never waits on the rest of the setup: with Message Content
// off or the bot in no server yet, it signs in and the status says what to do, until a check finds it done.
func (c *conn) loginStep(ctx context.Context, p wire.ConnectorLoginParams) (wire.ConnectorPrompt, error) {
	ask := func(why string) wire.ConnectorPrompt {
		return wire.ConnectorPrompt{Step: "token", Hint: c.tx.tokenHint, Error: why,
			Fields: []wire.ConnectorField{{Name: "token", Label: c.tx.tokenLabel, Secret: true, Placeholder: c.tx.tokenPlaceholder}}}
	}
	if p.Step != "token" {
		return ask(""), nil
	}
	if c.proxyErr != "" {
		return ask(c.proxyErr), nil
	}
	token := cleanToken(p.Values["token"])
	if token == "" {
		return ask(c.tx.tokenEmpty), nil
	}
	actx, cancel := context.WithTimeout(ctx, 45*time.Second)
	app, err := newAPI(c.ep.API, token, version, c.net.httpClient()).application(actx)
	cancel()
	if err != nil {
		if statusOf(err) == 401 {
			return ask(c.tx.tokenWrong), nil
		}
		t := c.troubleOf(err)
		return ask(strings.Join(append([]string{t.text}, t.guide...), "; ")), nil
	}
	in := signIn{V: 1, Token: token, AppID: app.ID.String(), BotID: app.ID.String(), BotName: app.Name}
	if app.Bot != nil {
		in.BotID, in.BotName = app.Bot.ID.String(), app.Bot.name()
	}
	switch {
	case app.Team != nil && app.Team.OwnerUserID != 0:
		in.OwnerID = app.Team.OwnerUserID.String() // a team's app: its owner is a pseudo-user
	case app.Owner != nil:
		in.OwnerID = app.Owner.ID.String()
	}
	raw, _ := json.Marshal(in)
	if err := c.hub.SaveSecret(ctx, connectorID, raw); err != nil {
		return wire.ConnectorPrompt{}, fmt.Errorf("keeping the sign-in in the hub: %w", err)
	}
	c.mu.Lock()
	c.gen++
	c.loaded, c.loadErr = true, ""
	c.startLocked(in)
	c.mu.Unlock()
	return wire.ConnectorPrompt{Done: true, Account: fmt.Sprintf(c.tx.account, in.BotName)}, nil
}

// cleanToken is the token as pasted, without spaces, quotes or a "Bot " in front.
func cleanToken(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	if len(s) > 4 && strings.EqualFold(s[:4], "bot ") {
		s = strings.TrimSpace(s[4:])
	}
	return s
}

// logout ends the session and forgets the token.
func (c *conn) logout(ctx context.Context, _ wire.ConnectorParams) (wire.Empty, error) {
	c.mu.Lock()
	c.gen++
	c.loaded = true
	c.stopLocked()
	c.trouble, c.loadErr = nil, ""
	c.mu.Unlock()
	if err := c.hub.DeleteSecret(ctx, connectorID); err != nil {
		return wire.Empty{}, fmt.Errorf("forgetting the sign-in in the hub: %w", err)
	}
	return wire.Empty{}, nil
}

// notSignedIn is the error of a call that needs the bot signed in.
func (c *conn) notSignedIn() error {
	return &ext.Error{Code: wire.ConnectorErrorCodeNotSignedIn, Message: "the Discord bot is not signed in, or Discord refused its token"}
}
