// Package telegram is the Telegram connector: the user's own account through MTProto (gotd/td), read-only
// (docs/extensions/telegram.md). It runs in jarvisd-telegram, not in jarvisd (connector/serve, connector/proc): gotd would
// double jarvisd's size.
//
// Everything it keeps — the API credentials the user entered and the session — is one sealed blob in the hub
// (connector.Secrets). The API hash, the session, phone numbers, codes and passwords never go into a status, a prompt,
// an error or a log.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
)

// stored is what the hub keeps for the connector, sealed.
type stored struct {
	APIID   int    `json:"api_id,omitempty"`
	APIHash string `json:"api_hash,omitempty"`
	Session []byte `json:"session,omitempty"` // gotd's
	Account string `json:"account,omitempty"` // who is signed in, for display
}

func (s stored) hasCredentials() bool { return s.APIID > 0 && s.APIHash != "" }

// creds are the API credentials of the app the user made at my.telegram.org.
type creds struct {
	id   int
	hash string
}

// client is a connected Telegram client: what the connector does through it (client.go has gotd's).
type client interface {
	// Check asks Telegram whether it knows the API credentials: connecting does not (2026-11-03, against Telegram
	// itself, made-up ones connect fine), asking for a QR sign-in token does (API_ID_INVALID). Nothing changes.
	Check(ctx context.Context) error
	// SendCode asks Telegram for a sign-in code; hash goes with the code to SignIn, via says where the code went.
	SendCode(ctx context.Context, phone string) (hash, via string, err error)
	// SignIn signs in with the code; errPasswordNeeded when the account has a 2FA password.
	SignIn(ctx context.Context, phone, code, hash string) (me string, err error)
	PasswordHint(ctx context.Context) (string, error)
	Password(ctx context.Context, password string) (me string, err error)
	// Me is who the session is signed in as; ok false when it is not signed in.
	Me(ctx context.Context) (me string, ok bool, err error)
	LogOut(ctx context.Context) error
	// Done is closed when the connection ended (Telegram dropped it, or its ctx ended).
	Done() <-chan struct{}
	// Dialogs is the account's conversations, newest first: the main list and the archive folder, up to limit each.
	Dialogs(ctx context.Context, limit int) ([]dialog, error)
	// History is a conversation's messages newer than minID, oldest first, at most max. With minID 0 (a first read) it
	// goes back to since and keeps the newest; otherwise it keeps the oldest, so a read cut short goes on from the
	// newest it gave.
	History(ctx context.Context, d dialog, minID int, since time.Time, max int) ([]message, error)
	// Older is a conversation's limit messages before the id before (0: the newest) and sent before until (zero: no
	// bound), oldest first: one request.
	Older(ctx context.Context, d dialog, before int, until time.Time, limit int) ([]message, error)
	// Search finds messages with query, in one conversation (in) or across the account, sent in [since, until) (zero:
	// no bound), newest first.
	Search(ctx context.Context, query string, in *dialog, since, until time.Time, limit int) ([]found, error)
	// Messages are a conversation's messages by id, in that order; one Telegram does not have is left out.
	Messages(ctx context.Context, d dialog, ids []int) ([]message, error)
	// Info is a conversation's profile (one request).
	Info(ctx context.Context, d dialog) (chatInfo, error)
	// Members are a group's members, at most limit, and how many it has (one request).
	Members(ctx context.Context, d dialog, limit int) ([]member, int, error)

	// Page is a conversation's limit messages before the id before (0: the newest), oldest first, as the app's page
	// draws them, and the peers they name (with the access hashes their pictures need): one request.
	Page(ctx context.Context, d dialog, before, limit int) ([]pageMessage, map[string]peer, error)
	// Locate reads message id of d again for where its picture is (thumb: the smaller one), when the page that
	// listed it is gone (a restart).
	Locate(ctx context.Context, d dialog, id int, thumb bool) (*fileLoc, error)
	// Fetch is a picture's bytes, at most most of them: a part (512 KB) a request.
	Fetch(ctx context.Context, f *fileLoc, most int) ([]byte, error)

	// The writes: only ever from an action the hub's gate let through (cli.go).

	// Send sends text, as a reply to the message replyTo unless it is 0, and says the sent message's id. random is
	// Telegram's random_id: the same message sent again with it is dropped.
	Send(ctx context.Context, d dialog, text string, replyTo int, random int64) (int, error)
	// Forward forwards from's messages ids to to; random has one random_id each.
	Forward(ctx context.Context, from dialog, ids []int, to dialog, random []int64) error
	// React puts the emoji on the message id (replacing the account's reaction there).
	React(ctx context.Context, d dialog, id int, emoji string) error
	// MarkRead marks the conversation read up to the message upTo (0: all of it).
	MarkRead(ctx context.Context, d dialog, upTo int) error
	// UnreadMentions is a group's unread messages that @ me or reply to me, oldest first; it marks nothing as read.
	UnreadMentions(ctx context.Context, d dialog, limit int) ([]message, error)
}

// found is a search's match and its conversation.
type found struct {
	Dialog  dialog
	Message message
}

var errPasswordNeeded = errors.New("2FA password needed")

// dialer connects with c, keeping the session in store, until ctx ends (Check says whether Telegram knows c). Every
// request goes through g. updated is called whenever Telegram tells of something new (a message, a read): the
// connector's realtime kick.
type dialer func(ctx context.Context, c creds, store sessionStore, g *gate, updated func()) (client, error)

// sessionStore is where gotd keeps its session (session.Storage's two methods).
type sessionStore interface {
	LoadSession(ctx context.Context) ([]byte, error)
	StoreSession(ctx context.Context, data []byte) error
}

// Conn is the Telegram connector.
type Conn struct {
	secrets connector.Secrets
	dial    dialer

	mu     sync.Mutex
	st     stored
	loaded bool
	cl     client             // nil while not connected
	stop   context.CancelFunc // ends cl
	status connector.Status
	// pending is the sign-in under way: the phone it asked a code for and the hash that goes with the code.
	pending struct{ phone, hash string }
	// wake makes Run connect again (the credentials changed, or the user signed out).
	wake chan struct{}
	// news is Telegram telling of something new, for Watch.
	news chan struct{}
	// gate is the one way to Telegram (gate.go), kept across connections: a flood wait outlasts a reconnect.
	gate *gate
	// zone is the user's, for the times the tools show; nil while the hub does not know it (never the machine's:
	// the hub runs in UTC).
	zone *time.Location
	// rules are the user's own triage rules for Telegram (the setting triage_rules), on every inbox thing; "" leaves
	// the hub's global rules.
	rules string
	box   inboxState
}

// New is the connector, keeping what it needs in secrets; zone is the user's (nil: not known yet).
func New(secrets connector.Secrets, zone *time.Location) *Conn {
	c := newConn(secrets, dialGotd)
	c.zone = zone
	return c
}

// WithRules gives the connector the user's own triage rules (pluginkit.TriageRules of its settings), before it runs.
func (c *Conn) WithRules(rules string) *Conn {
	c.rules = rules
	return c
}

func newConn(secrets connector.Secrets, dial dialer) *Conn {
	c := &Conn{secrets: secrets, dial: dial, wake: make(chan struct{}, 1), news: make(chan struct{}, 1), gate: newGate(),
		status: connector.Status{State: connector.StateNotSignedIn, Error: "还没填 Telegram 的 API 凭据"}}
	c.gate.flooded = func(d time.Duration) { log.Printf("Telegram asked to wait %d s: no request until then", seconds(d)) }
	return c
}

func (*Conn) ID() string   { return spec.ID }
func (*Conn) Name() string { return spec.Name }

// Manifest is spec's: the hub registers the program with the same before it runs.
func (*Conn) Manifest() connector.Manifest { return spec.Manifest() }

// Status answers from memory. A signed-in account is rate_limited while a flood wait is not over: when it came, what
// Telegram asked for, how long is left (the hub reads only an ok connector, so nothing is asked of it meanwhile).
func (c *Conn) Status(context.Context) connector.Status {
	c.mu.Lock()
	st := c.status
	c.mu.Unlock()
	if st.State != connector.StateOK {
		return st
	}
	at, asked, until := c.gate.flood()
	if left := until.Sub(c.gate.now()); left > 0 {
		st.State = connector.StateRateLimited
		st.Error = fmt.Sprintf("Telegram 限流：%s 起要求等 %d 秒，还要 %d 秒（%s 恢复）。这期间不发任何请求。",
			c.clock(at), seconds(asked), seconds(left), c.clock(until))
	}
	return st
}

func (c *Conn) setStatus(st connector.Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = st
}

// via says which API credentials it uses: the id, and only the end of the hash.
func (s stored) via() string {
	if !s.hasCredentials() {
		return ""
	}
	tail := s.APIHash
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return fmt.Sprintf("api_id %d · api_hash ••••%s", s.APIID, tail)
}

// load reads what the hub keeps, once.
func (c *Conn) load(ctx context.Context) (stored, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.st, nil
	}
	raw, err := c.secrets.Load(ctx)
	if err != nil {
		return stored{}, err
	}
	var st stored
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &st); err != nil {
			return stored{}, errors.New("the stored Telegram sign-in does not read")
		}
	}
	c.st, c.loaded = st, true
	return st, nil
}

// save keeps c.st in the hub, under c.mu.
func (c *Conn) saveLocked(ctx context.Context) error {
	if !c.st.hasCredentials() && len(c.st.Session) == 0 {
		return c.secrets.Delete(ctx)
	}
	raw, _ := json.Marshal(c.st)
	return c.secrets.Save(ctx, raw)
}

// kick makes Run connect again.
func (c *Conn) kick() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Retries of a connection that fails.
var (
	retryFirst = 10 * time.Second
	retryLast  = 10 * time.Minute
)

// Run keeps a client connected while there are credentials, until ctx ends.
func (c *Conn) Run(ctx context.Context) {
	wait := retryFirst
	for ctx.Err() == nil {
		st, err := c.load(ctx)
		if err != nil {
			c.setStatus(connector.Status{State: connector.StateError, Error: "读不到存着的 Telegram 登录：" + err.Error()})
			if !c.sleep(ctx, wait) {
				return
			}
			continue
		}
		if !st.hasCredentials() {
			c.setStatus(connector.Status{State: connector.StateNotSignedIn, Error: "还没填 Telegram 的 API 凭据"})
			select {
			case <-ctx.Done():
				return
			case <-c.wake:
			}
			continue
		}
		cctx, stop := context.WithCancel(ctx)
		cl, err := c.dial(cctx, creds{st.APIID, st.APIHash}, sessionOf{c}, c.gate, c.updated)
		if err != nil {
			stop()
			c.setStatus(connector.Status{State: connector.StateError, Via: st.via(), Error: "连不上 Telegram：" + explain(err)})
			log.Printf("connect: %s", explain(err))
			if !c.sleep(ctx, wait) {
				return
			}
			wait = min(wait*2, retryLast)
			continue
		}
		wait = retryFirst
		c.mu.Lock()
		c.cl, c.stop = cl, stop
		c.mu.Unlock()
		c.checkSignedIn(cctx)
		select {
		case <-ctx.Done():
		case <-cl.Done(): // Telegram dropped it: connect again
		case <-c.wake:
		}
		stop()
		c.mu.Lock()
		if c.cl == cl {
			c.cl, c.stop = nil, nil
		}
		c.mu.Unlock()
	}
}

func (c *Conn) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-c.wake:
		return true
	case <-time.After(d):
		return true
	}
}

// checkSignedIn sets the status from who the session is.
func (c *Conn) checkSignedIn(ctx context.Context) {
	c.mu.Lock()
	cl, st := c.cl, c.st
	c.mu.Unlock()
	if cl == nil {
		return
	}
	me, ok, err := cl.Me(ctx)
	switch {
	case err != nil:
		c.setStatus(connector.Status{State: connector.StateError, Via: st.via(), Error: "Telegram：" + explain(err)})
	case !ok:
		c.setStatus(connector.Status{State: connector.StateNotSignedIn, Via: st.via(), Error: "还没登录 Telegram"})
	default:
		c.mu.Lock()
		if c.st.Account != me {
			c.st.Account = me
			_ = c.saveLocked(ctx)
		}
		c.mu.Unlock()
		c.setStatus(connector.Status{State: connector.StateOK, Account: me, Via: st.via()})
		c.updated() // the inbox and the archive catch up now
	}
}

// sessionOf is gotd's session storage: the session inside what the hub keeps.
type sessionOf struct{ c *Conn }

var errNoSession = errors.New("no session") // replaced by gotd's session.ErrNotFound in client.go

func (s sessionOf) LoadSession(context.Context) ([]byte, error) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if len(s.c.st.Session) == 0 {
		return nil, errNoSession
	}
	return append([]byte(nil), s.c.st.Session...), nil
}

func (s sessionOf) StoreSession(ctx context.Context, data []byte) error {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	s.c.st.Session = append([]byte(nil), data...)
	return s.c.saveLocked(ctx)
}
