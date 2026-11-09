// Package xtest is a fake X API v2 for tests: the endpoints the connector reads, with OAuth 1.0a signatures
// checked, pagination, since_id / until_id, the expansions it asks for, rate-limit headers and failures on demand. Every
// account, id and key in tests is synthetic.
package xtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/x/internal/x"
)

// Keys are the synthetic keys the fake accepts.
var Keys = x.Keys{APIKey: "fake-api-key-0001", APISecret: "fake-api-secret-0002", AccessToken: "1000-fake-access-token-0003",
	AccessTokenSecret: "fake-access-secret-0004"}

// User is an account.
type User struct {
	ID, Name, Username, Description string
	Followers                       int
}

// Ref is a post a post references: Type retweeted, quoted or replied_to.
type Ref struct{ Type, ID string }

// Tweet is a post.
type Tweet struct {
	ID, Text, AuthorID, ConversationID, InReplyToUser string
	Note                                              string // a long post's whole text (note_tweet)
	CreatedAt                                         time.Time
	Likes, Reposts, Replies, Quotes                   int
	URLs                                              []string
	Refs                                              []Ref
}

// Fake is the API. Set its fields before the connector reads; they are read under its lock.
type Fake struct {
	Me        User
	Users     map[string]User    // every account, by id
	Following []User             // whom Me follows
	Home      []Tweet            // Me's home timeline, newest first
	Lists     map[string][]Tweet // by list id, newest first
	Mentions  []Tweet            // newest first
	Own       []Tweet            // Me's own posts, newest first
	Tweets    map[string]Tweet   // every post by id (what includes and /2/tweets/:id read)
	// Fail answers a status instead, by endpoint ("/2/users/me", "home", "mentions", "following", "lists", "own",
	// "tweet"); a 429 carries x-rate-limit-reset Reset.
	Fail  map[string]int
	Reset time.Time
	// FailAfter answers this many requests of an endpoint normally before Fail applies to it.
	FailAfter map[string]int
	// Before, when set, is called with each request's path before the fake answers it (outside its lock).
	Before func(path string)

	mu       sync.Mutex
	requests []string
	srv      *httptest.Server
}

// New starts the fake for t.
func New(t testing.TB) *Fake {
	f := &Fake{Users: map[string]User{}, Lists: map[string][]Tweet{}, Tweets: map[string]Tweet{}, Fail: map[string]int{},
		FailAfter: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the fake's base URL.
func (f *Fake) URL() string { return f.srv.URL }

// Lock / Unlock guard the fields while a test changes them during a run.
func (f *Fake) Lock()   { f.mu.Lock() }
func (f *Fake) Unlock() { f.mu.Unlock() }

// Requests are the requests so far (path?query), and Reset forgets them.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *Fake) ForgetRequests() {
	f.mu.Lock()
	f.requests = nil
	f.mu.Unlock()
}

// AddUser adds an account to Users (under Lock while the connector may be reading).
func (f *Fake) AddUser(u User) User {
	f.Users[u.ID] = u
	return u
}

// AddTweet adds a post to Tweets (under Lock while the connector may be reading).
func (f *Fake) AddTweet(t Tweet) Tweet {
	f.Tweets[t.ID] = t
	return t
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	before := f.Before
	f.mu.Unlock()
	if before != nil {
		before(r.URL.Path)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.RequestURI())
	if r.Method != http.MethodGet || !x.Verify(Keys, r.Method, "http://"+r.Host+r.URL.RequestURI(), r.Header.Get("Authorization")) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"title": "Unauthorized", "detail": "Unauthorized"})
		return
	}
	q := r.URL.Query()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	endpoint := ""
	switch {
	case r.URL.Path == "/2/users/me":
		endpoint = "/2/users/me"
	case len(parts) == 4 && parts[1] == "users" && parts[3] == "following":
		endpoint = "following"
	case len(parts) == 5 && parts[1] == "users" && parts[3] == "timelines":
		endpoint = "home"
	case len(parts) == 4 && parts[1] == "users" && parts[3] == "mentions":
		endpoint = "mentions"
	case len(parts) == 4 && parts[1] == "users" && parts[3] == "tweets":
		endpoint = "own"
	case len(parts) == 4 && parts[1] == "lists" && parts[3] == "tweets":
		endpoint = "lists"
	case len(parts) == 3 && parts[1] == "tweets":
		endpoint = "tweet"
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"title": "Not Found"})
		return
	}
	if code := f.Fail[endpoint]; code != 0 && f.FailAfter[endpoint] > 0 {
		f.FailAfter[endpoint]--
	} else if code != 0 {
		if code == http.StatusTooManyRequests {
			w.Header().Set("x-rate-limit-remaining", "0")
			w.Header().Set("x-rate-limit-reset", strconv.FormatInt(f.Reset.Unix(), 10))
		}
		writeJSON(w, code, map[string]any{"title": http.StatusText(code), "detail": http.StatusText(code)})
		return
	}
	if endpoint != "/2/users/me" && endpoint != "lists" && endpoint != "tweet" && parts[2] != f.Me.ID {
		writeJSON(w, http.StatusForbidden, map[string]any{"title": "Forbidden"})
		return
	}
	w.Header().Set("x-rate-limit-remaining", "100")
	w.Header().Set("x-rate-limit-reset", strconv.FormatInt(time.Now().Add(15*time.Minute).Unix(), 10))
	switch endpoint {
	case "/2/users/me":
		writeJSON(w, http.StatusOK, map[string]any{"data": userJSON(f.Me)})
	case "following":
		page, next := paginate(f.Following, q, 1000)
		data := make([]any, len(page))
		for i, u := range page {
			data[i] = userJSON(u)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": meta(len(page), "", next)})
	case "home":
		f.tweets(w, filter(f.Home, q), q, 100)
	case "mentions":
		f.tweets(w, filter(f.Mentions, q), q, 100)
	case "own":
		f.tweets(w, filter(f.Own, q), q, 100)
	case "lists":
		f.tweets(w, f.Lists[parts[2]], q, 100)
	case "tweet":
		t, ok := f.Tweets[parts[2]]
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"errors": []any{map[string]any{"title": "Not Found Error"}}})
			return
		}
		users, tweets := f.includes([]Tweet{t})
		writeJSON(w, http.StatusOK, map[string]any{"data": tweetJSON(t), "includes": map[string]any{"users": users, "tweets": tweets}})
	}
}

// tweets answers a page of ts with its includes.
func (f *Fake) tweets(w http.ResponseWriter, ts []Tweet, q map[string][]string, most int) {
	page, next := paginate(ts, q, most)
	data := make([]any, len(page))
	for i, t := range page {
		data[i] = tweetJSON(t)
	}
	users, tweets := f.includes(page)
	m := meta(len(page), "", next)
	if len(page) > 0 {
		m["newest_id"], m["oldest_id"] = page[0].ID, page[len(page)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "includes": map[string]any{"users": users, "tweets": tweets}, "meta": m})
}

// includes are the authors of ts, the posts they reference and those posts' authors, each once.
func (f *Fake) includes(ts []Tweet) (users, tweets []any) {
	seenU, seenT := map[string]bool{}, map[string]bool{}
	addUser := func(id string) {
		if u, ok := f.Users[id]; ok && !seenU[id] {
			seenU[id] = true
			users = append(users, userJSON(u))
		}
	}
	for _, t := range ts {
		addUser(t.AuthorID)
	}
	for _, t := range ts {
		for _, r := range t.Refs {
			if rt, ok := f.Tweets[r.ID]; ok && !seenT[r.ID] {
				seenT[r.ID] = true
				tweets = append(tweets, tweetJSON(rt))
				addUser(rt.AuthorID)
			}
		}
	}
	return users, tweets
}

// filter is ts after since_id and start_time, before until_id.
func filter(ts []Tweet, q map[string][]string) []Tweet {
	since, until, start := first(q, "since_id"), first(q, "until_id"), first(q, "start_time")
	var from time.Time
	if start != "" {
		from, _ = time.Parse(time.RFC3339, start)
	}
	var out []Tweet
	for _, t := range ts {
		if since != "" && !Newer(t.ID, since) || until != "" && !Newer(until, t.ID) {
			continue
		}
		if !from.IsZero() && t.CreatedAt.Before(from) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Newer says whether id a is newer (a larger number) than b.
func Newer(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

func paginate[T any](all []T, q map[string][]string, most int) ([]T, string) {
	n, err := strconv.Atoi(first(q, "max_results"))
	if err != nil || n <= 0 || n > most {
		n = most
	}
	off, _ := strconv.Atoi(first(q, "pagination_token"))
	if off > len(all) {
		off = len(all)
	}
	end := min(off+n, len(all))
	next := ""
	if end < len(all) {
		next = strconv.Itoa(end)
	}
	return all[off:end], next
}

func first(q map[string][]string, k string) string {
	if v := q[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func meta(n int, newest, next string) map[string]any {
	m := map[string]any{"result_count": n}
	if newest != "" {
		m["newest_id"] = newest
	}
	if next != "" {
		m["next_token"] = next
	}
	return m
}

func userJSON(u User) map[string]any {
	return map[string]any{"id": u.ID, "name": u.Name, "username": u.Username, "description": u.Description,
		"public_metrics": map[string]any{"followers_count": u.Followers, "following_count": 10, "tweet_count": 100}}
}

func tweetJSON(t Tweet) map[string]any {
	conv := t.ConversationID
	if conv == "" {
		conv = t.ID
	}
	m := map[string]any{"id": t.ID, "text": t.Text, "author_id": t.AuthorID, "conversation_id": conv, "created_at": t.CreatedAt.UTC().Format(time.RFC3339),
		"lang": "en", "public_metrics": map[string]any{"like_count": t.Likes, "retweet_count": t.Reposts, "reply_count": t.Replies, "quote_count": t.Quotes}}
	if t.InReplyToUser != "" {
		m["in_reply_to_user_id"] = t.InReplyToUser
	}
	if t.Note != "" {
		m["note_tweet"] = map[string]any{"text": t.Note}
	}
	if len(t.URLs) > 0 {
		var urls []any
		for i, u := range t.URLs {
			urls = append(urls, map[string]any{"url": "https://t.co/fake" + strconv.Itoa(i), "expanded_url": u, "display_url": strings.TrimPrefix(u, "https://")})
		}
		m["entities"] = map[string]any{"urls": urls}
	}
	if len(t.Refs) > 0 {
		var refs []any
		for _, r := range t.Refs {
			refs = append(refs, map[string]any{"type": r.Type, "id": r.ID})
		}
		m["referenced_tweets"] = refs
	}
	return m
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Secrets is the hub's sealed store, in memory.
type Secrets struct {
	mu      sync.Mutex
	data    []byte
	deleted bool
}

func (s *Secrets) Load(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.data...), nil
}

func (s *Secrets) Save(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data, s.deleted = append([]byte(nil), data...), false
	return nil
}

func (s *Secrets) Delete(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data, s.deleted = nil, true
	return nil
}

// Stored is what is kept, and whether it was deleted.
func (s *Secrets) Stored() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.data...), s.deleted
}
