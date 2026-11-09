package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// api is the extension's own small client of Discord's REST API (v10), GET only: D1 reads. It keeps to Discord's
// rate limits from the response headers (a route whose bucket ran out waits until it resets; a global limit stops
// every route), retries a short 429 once, and answers a longer one as an error instead of waiting it out, so a call
// never outlives the hub's call timeout. Its errors never carry the token or a response body.
type api struct {
	base, token, agent string
	hc                 *http.Client

	mu     sync.Mutex
	until  map[string]time.Time // per route: its bucket ran out until then
	global time.Time
}

const (
	retryMost = 5 * time.Second // a 429 asking to wait longer is answered, not waited out
	bodyMost  = 16 << 20
)

func newAPI(base, token, version string, hc *http.Client) *api {
	return &api{base: base, token: token, hc: hc, until: map[string]time.Time{},
		agent: "DiscordBot (https://github.com/nwverse/persona-extensions, " + version + ")"}
}

// apiError is an answer of Discord's that is not a success.
type apiError struct {
	Status     int
	Code       int    // Discord's JSON error code
	Message    string // Discord's words
	RetryAfter time.Duration
	Global     bool // a 429 for every route
}

// badAnswer is a success whose body could not be read (Discord changed something): it is about that request only.
type badAnswer struct {
	path string
	err  error
}

func (b *badAnswer) Error() string {
	return fmt.Sprintf("reading Discord's answer to %s: %v", b.path, b.err)
}

// local says whether err is about one request only (Discord answered: a refusal, a failure, a route's rate limit,
// an unreadable body), as against the network, the token or a global rate limit, which are about every request.
func local(err error) bool {
	var b *badAnswer
	if errors.As(err, &b) {
		return true
	}
	e, ok := asAPIError(err)
	return ok && e.Status != 401 && !e.Global
}

func (e *apiError) Error() string {
	if e.Status == http.StatusTooManyRequests {
		return fmt.Sprintf("Discord asks to wait %d seconds", seconds(e.RetryAfter))
	}
	s := fmt.Sprintf("Discord answered %d", e.Status)
	if e.Message != "" {
		s += ": " + clip(oneLine(e.Message), 120)
	}
	return s
}

// asAPIError is err as an answer of Discord's.
func asAPIError(err error) (*apiError, bool) {
	var e *apiError
	ok := errors.As(err, &e)
	return e, ok
}

// statusOf is the HTTP status of an error from Discord, 0 for any other error (the network, a timeout).
func statusOf(err error) int {
	if e, ok := asAPIError(err); ok {
		return e.Status
	}
	return 0
}

// seconds is d in whole seconds, at least 1.
func seconds(d time.Duration) int { return max(int((d+time.Second-1)/time.Second), 1) }

// get asks for path (with q) and decodes the answer into out; it answers the status of a success (message search
// answers 202 while Discord is still indexing).
func (a *api) get(ctx context.Context, path string, q url.Values, out any) (int, error) {
	for try := 0; ; try++ {
		if wait, global := a.wait(path); wait > 0 {
			if wait > retryMost || !sleep(ctx, wait) {
				return 0, &apiError{Status: http.StatusTooManyRequests, RetryAfter: wait, Global: global}
			}
		}
		u := a.base + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bot "+a.token)
		req.Header.Set("User-Agent", a.agent)
		resp, err := a.hc.Do(req)
		if err != nil {
			return 0, err // a *url.Error: the URL, never the header
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, bodyMost+1))
		resp.Body.Close()
		if err != nil {
			return 0, err
		}
		a.note(path, resp.Header)
		if resp.StatusCode == http.StatusTooManyRequests {
			e := rateLimited(resp.Header, body)
			if e.Global {
				a.mu.Lock()
				a.global = time.Now().Add(e.RetryAfter)
				a.mu.Unlock()
			}
			if try == 0 && e.RetryAfter <= retryMost && sleep(ctx, e.RetryAfter) {
				continue
			}
			return 0, e
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			e := &apiError{Status: resp.StatusCode}
			var b struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(body, &b) == nil {
				e.Code, e.Message = b.Code, b.Message
			}
			return 0, e
		}
		if len(body) > bodyMost {
			return 0, errors.New("Discord's answer is too large")
		}
		if out != nil && len(body) > 0 {
			if err := json.Unmarshal(body, out); err != nil {
				return 0, &badAnswer{path: path, err: err}
			}
		}
		return resp.StatusCode, nil
	}
}

// wait is how long a request to route must wait for its bucket or the global limit, and whether it is the global
// limit.
func (a *api) wait(route string) (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	until, global := a.global, true
	if t, ok := a.until[route]; ok {
		if t.After(until) {
			until, global = t, false
		}
		if !t.After(now) {
			delete(a.until, route)
		}
	}
	return until.Sub(now), global
}

// note keeps what Discord said about route's bucket: when it ran out, the time until it resets.
func (a *api) note(route string, h http.Header) {
	if h.Get("X-RateLimit-Remaining") != "0" {
		return
	}
	after, err := strconv.ParseFloat(h.Get("X-RateLimit-Reset-After"), 64)
	if err != nil || after <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if len(a.until) >= 1000 { // routes never asked again
		for r, at := range a.until {
			if !at.After(now) {
				delete(a.until, r)
			}
		}
	}
	a.until[route] = now.Add(time.Duration(after * float64(time.Second)))
}

// rateLimited is a 429 as an error.
func rateLimited(h http.Header, body []byte) *apiError {
	var b struct {
		RetryAfter float64 `json:"retry_after"`
		Global     bool    `json:"global"`
		Message    string  `json:"message"`
	}
	_ = json.Unmarshal(body, &b)
	if b.RetryAfter <= 0 {
		b.RetryAfter, _ = strconv.ParseFloat(h.Get("Retry-After"), 64)
	}
	return &apiError{Status: http.StatusTooManyRequests, Message: b.Message, RetryAfter: max(time.Duration(b.RetryAfter*float64(time.Second)), time.Second),
		Global: b.Global || h.Get("X-RateLimit-Global") == "true"}
}

// sleep waits d unless ctx ends first or its deadline comes before; it says whether it waited.
func sleep(ctx context.Context, d time.Duration) bool {
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < d {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// The calls D1 makes.

func (a *api) application(ctx context.Context) (application, error) {
	var app application
	_, err := a.get(ctx, "/applications/@me", nil, &app)
	return app, err
}

// guilds are the servers the bot is in, all of them (200 a page).
func (a *api) guilds(ctx context.Context) ([]guild, error) {
	var out []guild
	var after snowflake
	for {
		q := url.Values{"limit": {"200"}}
		if after != 0 {
			q.Set("after", after.String())
		}
		var page []guild
		if _, err := a.get(ctx, "/users/@me/guilds", q, &page); err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 200 {
			return out, nil
		}
		after = page[len(page)-1].ID
	}
}

// inAnyGuild says whether the bot is in any server, with one small request.
func (a *api) inAnyGuild(ctx context.Context) (bool, error) {
	var page []guild
	_, err := a.get(ctx, "/users/@me/guilds", url.Values{"limit": {"1"}}, &page)
	return len(page) > 0, err
}

func (a *api) channels(ctx context.Context, g snowflake) ([]channel, error) {
	var out []channel
	_, err := a.get(ctx, "/guilds/"+g.String()+"/channels", nil, &out)
	return out, err
}

func (a *api) activeThreads(ctx context.Context, g snowflake) ([]channel, error) {
	var out struct {
		Threads []channel `json:"threads"`
	}
	_, err := a.get(ctx, "/guilds/"+g.String()+"/threads/active", nil, &out)
	return out.Threads, err
}

// archivedThreads are a channel's public threads archived before before (zero: the newest), newest first.
func (a *api) archivedThreads(ctx context.Context, ch snowflake, before time.Time) ([]channel, bool, error) {
	q := url.Values{"limit": {"100"}}
	if !before.IsZero() {
		q.Set("before", before.UTC().Format(time.RFC3339Nano))
	}
	var out struct {
		Threads []channel `json:"threads"`
		HasMore bool      `json:"has_more"`
	}
	_, err := a.get(ctx, "/channels/"+ch.String()+"/threads/archived/public", q, &out)
	return out.Threads, out.HasMore, err
}

// page is one answer of GET /channels/{id}/messages: the messages that could be read, and what the answer held
// (how many, its newest and oldest ids), which paging goes by, so a message that can't be read never ends a read
// early or is read past unnoticed.
type page struct {
	msgs           []message
	n              int
	newest, oldest snowflake
}

// messagesBefore is the page of the newest messages before before (zero: the newest of all).
func (a *api) messagesBefore(ctx context.Context, ch, before snowflake, limit int) (page, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != 0 {
		q.Set("before", before.String())
	}
	return a.messages(ctx, ch, q)
}

// messagesAfter is the page of the oldest messages after after (0: from the first).
func (a *api) messagesAfter(ctx context.Context, ch, after snowflake, limit int) (page, error) {
	return a.messages(ctx, ch, url.Values{"limit": {strconv.Itoa(limit)}, "after": {max(after, 1).String()}})
}

// messages reads a page of messages one by one: a message that cannot be read is left out rather than holding up
// the channel, unless none can (then the page is a badAnswer and nothing moves).
func (a *api) messages(ctx context.Context, ch snowflake, q url.Values) (page, error) {
	path := "/channels/" + ch.String() + "/messages"
	var raw []json.RawMessage
	if _, err := a.get(ctx, path, q, &raw); err != nil {
		return page{}, err
	}
	p := page{n: len(raw), msgs: make([]message, 0, len(raw))}
	var bad error
	for _, r := range raw {
		var id struct {
			ID snowflake `json:"id"`
		}
		if json.Unmarshal(r, &id) == nil && id.ID != 0 {
			p.newest = max(p.newest, id.ID)
			if p.oldest == 0 || id.ID < p.oldest {
				p.oldest = id.ID
			}
		}
		var m message
		if err := json.Unmarshal(r, &m); err != nil || m.ID == 0 {
			bad = fmt.Errorf("a message that cannot be read: %v", err)
			continue
		}
		p.msgs = append(p.msgs, m)
	}
	if bad != nil && (len(p.msgs) == 0 || p.newest == 0) {
		return page{}, &badAnswer{path: path, err: bad}
	}
	return p, nil
}

func (a *api) roles(ctx context.Context, g snowflake) ([]role, error) {
	var out []role
	_, err := a.get(ctx, "/guilds/"+g.String()+"/roles", nil, &out)
	return out, err
}

// search is one page of a server's message search; indexing says Discord is still building the index (202).
func (a *api) search(ctx context.Context, g snowflake, q url.Values) (res searchResult, indexing bool, err error) {
	status, err := a.get(ctx, "/guilds/"+g.String()+"/messages/search", q, &res)
	return res, status == http.StatusAccepted || res.Code == 110000, err
}

// Discord's objects, as much of them as the extension reads. Unknown fields are ignored, missing ones are zero.

// snowflake is a Discord id: a number sent as a string, which also tells its time.
type snowflake uint64

const discordEpoch = 1420070400000 // ms

func (s *snowflake) UnmarshalJSON(b []byte) error {
	str := strings.Trim(string(b), `"`)
	if str == "null" || str == "" {
		*s = 0
		return nil
	}
	n, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		return fmt.Errorf("an id that is not a number")
	}
	*s = snowflake(n)
	return nil
}

func (s snowflake) String() string { return strconv.FormatUint(uint64(s), 10) }

// Time is when the id was made.
func (s snowflake) Time() time.Time { return time.UnixMilli(int64(s>>22) + discordEpoch) }

func parseSnowflake(s string) (snowflake, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return snowflake(n), err == nil && n > 0
}

type user struct {
	ID         snowflake `json:"id"`
	Username   string    `json:"username"`
	GlobalName string    `json:"global_name"`
	Bot        bool      `json:"bot"`
}

// name is how the user shows: the display name, else the username.
func (u user) name() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

type message struct {
	ID              snowflake `json:"id"`
	ChannelID       snowflake `json:"channel_id"`
	Type            int       `json:"type"`
	Content         string    `json:"content"`
	Author          user      `json:"author"`
	Timestamp       time.Time `json:"timestamp"`
	Flags           int       `json:"flags"`
	Mentions        []user    `json:"mentions"`
	MentionChannels []struct {
		ID   snowflake `json:"id"`
		Name string    `json:"name"`
	} `json:"mention_channels"`
	Attachments  []attachment `json:"attachments"`
	Embeds       []msgEmbed   `json:"embeds"`
	StickerItems []struct {
		Name string `json:"name"`
	} `json:"sticker_items"`
	Poll *struct {
		Question struct {
			Text string `json:"text"`
		} `json:"question"`
	} `json:"poll"`
	Components        []json.RawMessage `json:"components"`
	ReferencedMessage *message          `json:"referenced_message"`
	MessageSnapshots  []struct {
		Message message `json:"message"`
	} `json:"message_snapshots"`
}

type attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
}

type msgEmbed struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Fields      []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"fields"`
}

// Channel types (https://discord.com/developers/docs/resources/channel).
const (
	chText               = 0
	chAnnouncement       = 5
	chAnnouncementThread = 10
	chPublicThread       = 11
	chPrivateThread      = 12
	chForum              = 15
	chMedia              = 16
)

type channel struct {
	ID             snowflake   `json:"id"`
	Type           int         `json:"type"`
	GuildID        snowflake   `json:"guild_id"`
	Name           string      `json:"name"`
	ParentID       snowflake   `json:"parent_id"`
	LastMessageID  snowflake   `json:"last_message_id"`
	ThreadMetadata *threadMeta `json:"thread_metadata"`
}

type threadMeta struct {
	Archived         bool      `json:"archived"`
	ArchiveTimestamp time.Time `json:"archive_timestamp"`
}

func (c channel) thread() bool {
	return c.Type == chAnnouncementThread || c.Type == chPublicThread || c.Type == chPrivateThread
}

// readable says whether a channel of its type has messages of its own: text and announcement channels and threads
// (a forum's posts are threads; voice channels' chat is left out).
func (c channel) readable() bool { return c.Type == chText || c.Type == chAnnouncement || c.thread() }

// hasThreads says whether a channel of its type can have threads.
func (c channel) hasThreads() bool {
	return c.Type == chText || c.Type == chAnnouncement || c.Type == chForum || c.Type == chMedia
}

type guild struct {
	ID   snowflake `json:"id"`
	Name string    `json:"name"`
}

type role struct {
	ID   snowflake `json:"id"`
	Name string    `json:"name"`
}

// Application flags that say the Message Content intent is on: for apps in 100 servers or more (reviewed), and
// for the rest (the toggle on the Bot page).
const (
	flagMessageContent        = 1 << 18
	flagMessageContentLimited = 1 << 19
)

type application struct {
	ID    snowflake `json:"id"`
	Name  string    `json:"name"`
	Flags int64     `json:"flags"`
	Bot   *user     `json:"bot"`
	Owner *user     `json:"owner"`
	Team  *struct {
		OwnerUserID snowflake `json:"owner_user_id"`
	} `json:"team"`
}

// contentIntent says whether Discord shows the bot what messages say.
func (a application) contentIntent() bool {
	return a.Flags&(flagMessageContent|flagMessageContentLimited) != 0
}

type searchResult struct {
	Code         int         `json:"code"`
	TotalResults int         `json:"total_results"`
	Messages     [][]message `json:"messages"`
}
