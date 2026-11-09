package x

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

// The X API v2 as the connector reads it: GET requests to api.x.com signed with the user's own app (OAuth 1.0a user
// context), the x-rate-limit-* headers kept per endpoint, and X's errors as the connector's (errUnauthorized,
// errCredits, *rateLimited). Only the official API: nothing here scrapes x.com, uses cookies or private endpoints.

// DefaultBase is X's API.
const DefaultBase = "https://api.x.com"

// Fields asked for. tweet.fields / referenced_tweets are the v2 wire names.
const (
	tweetFields  = "created_at,author_id,conversation_id,lang,public_metrics,entities,referenced_tweets,note_tweet,in_reply_to_user_id"
	expansions   = "author_id,referenced_tweets.id,referenced_tweets.id.author_id"
	feedUsers    = "id,name,username,public_metrics,verified"
	followFields = "id,name,username,description,public_metrics,verified,protected,profile_image_url"
)

var (
	// errUnauthorized: X refused the keys (401): revoked, regenerated or mistyped.
	errUnauthorized = errors.New("x: unauthorized")
	// errCredits: X refused for the app's account (402 out of credits, 403 client not enrolled or not allowed).
	errCredits = errors.New("x: no credits or access")
	// errNotFound: the post or user does not exist or is not visible (404, or a 200 with only errors).
	errNotFound = errors.New("x: not found")
)

// rateLimited is a 429 (or an endpoint whose window is used up): Until is when X said it resets.
type rateLimited struct{ Until time.Time }

func (e *rateLimited) Error() string {
	return "x: rate limited until " + e.Until.UTC().Format(time.RFC3339)
}

// apiError is any other failure, its text without anything secret.
type apiError struct {
	Status int
	Detail string
}

func (e *apiError) Error() string { return fmt.Sprintf("x: HTTP %d: %s", e.Status, e.Detail) }

// client calls the API with keys.
type client struct {
	base string
	http *http.Client
	now  func() time.Time

	mu     sync.Mutex
	limits map[string]limit // by endpoint (the path with ids as ":id")
}

// limit is what X said last about an endpoint's window.
type limit struct {
	remaining int
	reset     time.Time
}

// endpoint is a path as its rate limit is kept: numeric ids as ":id".
func endpoint(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p != "" && strings.Trim(p, "0123456789") == "" {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

// get reads path with query into out (the whole JSON answer). Before it asks, an endpoint whose window X said is used
// up answers *rateLimited without a request.
func (c *client) get(ctx context.Context, k Keys, path string, q url.Values, out any) error {
	ep := endpoint(path)
	now := c.now()
	c.mu.Lock()
	if l, ok := c.limits[ep]; ok && l.remaining <= 0 && now.Before(l.reset) {
		c.mu.Unlock()
		return &rateLimited{Until: l.reset}
	}
	c.mu.Unlock()
	u, err := url.Parse(c.base + path)
	if err != nil {
		return err
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authorization(k, http.MethodGet, u, nonce(), now))
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return scrubbed(err, k)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return scrubbed(err, k)
	}
	c.note(ep, resp.Header)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errUnauthorized
	case resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusForbidden:
		return errCredits
	case resp.StatusCode == http.StatusTooManyRequests:
		until := c.resetOf(resp.Header)
		c.mu.Lock()
		c.limits[ep] = limit{remaining: 0, reset: until}
		c.mu.Unlock()
		return &rateLimited{Until: until}
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode/100 != 2:
		return &apiError{Status: resp.StatusCode, Detail: detail(body, k)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &apiError{Status: resp.StatusCode, Detail: "unreadable answer"}
	}
	return nil
}

// note keeps the endpoint's window as X's headers say it.
func (c *client) note(ep string, h http.Header) {
	rem, err := strconv.Atoi(h.Get("x-rate-limit-remaining"))
	if err != nil {
		return
	}
	c.mu.Lock()
	c.limits[ep] = limit{remaining: rem, reset: c.resetOf(h)}
	c.mu.Unlock()
}

// resetOf is x-rate-limit-reset (unix seconds), or 15 minutes on when X did not say.
func (c *client) resetOf(h http.Header) time.Time {
	if s, err := strconv.ParseInt(h.Get("x-rate-limit-reset"), 10, 64); err == nil && s > 0 {
		return time.Unix(s, 0)
	}
	return c.now().Add(15 * time.Minute)
}

// detail is X's error text for an unexpected status, short and without the keys.
func detail(body []byte, k Keys) string {
	var e struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &e)
	s := e.Detail
	if s == "" {
		s = e.Title
	}
	if s == "" && len(e.Errors) > 0 {
		s = e.Errors[0].Message
	}
	if s == "" {
		s = "unexpected answer"
	}
	return clip(scrub(s, k), 200)
}

// scrubbed is a transport error with the keys taken out (a URL in it carries none, but a proxy's could).
func scrubbed(err error, k Keys) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(scrub(err.Error(), k))
}

// scrub takes every key out of s.
func scrub(s string, k Keys) string {
	for _, v := range k.secrets() {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "xxxxx")
		}
	}
	return s
}

// The API's shapes, only the fields used.
type (
	apiUser struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Username      string `json:"username"`
		Description   string `json:"description"`
		Verified      bool   `json:"verified"`
		Protected     bool   `json:"protected"`
		ProfileImage  string `json:"profile_image_url"`
		PublicMetrics struct {
			Followers int `json:"followers_count"`
			Following int `json:"following_count"`
			Posts     int `json:"tweet_count"`
		} `json:"public_metrics"`
	}
	apiTweet struct {
		ID             string    `json:"id"`
		Text           string    `json:"text"`
		AuthorID       string    `json:"author_id"`
		ConversationID string    `json:"conversation_id"`
		CreatedAt      time.Time `json:"created_at"`
		Lang           string    `json:"lang"`
		InReplyToUser  string    `json:"in_reply_to_user_id"`
		NoteTweet      *struct {
			Text string `json:"text"`
		} `json:"note_tweet"`
		PublicMetrics struct {
			Likes   int `json:"like_count"`
			Reposts int `json:"retweet_count"`
			Replies int `json:"reply_count"`
			Quotes  int `json:"quote_count"`
		} `json:"public_metrics"`
		Entities struct {
			URLs []struct {
				URL         string `json:"url"`
				ExpandedURL string `json:"expanded_url"`
				DisplayURL  string `json:"display_url"`
			} `json:"urls"`
		} `json:"entities"`
		Referenced []struct {
			Type string `json:"type"` // retweeted | quoted | replied_to
			ID   string `json:"id"`
		} `json:"referenced_tweets"`
	}
	apiMeta struct {
		ResultCount int    `json:"result_count"`
		NewestID    string `json:"newest_id"`
		OldestID    string `json:"oldest_id"`
		NextToken   string `json:"next_token"`
	}
	apiErr struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	// tweetsPage is an answer with posts: data, the expansions, meta.
	tweetsPage struct {
		Data     []apiTweet `json:"data"`
		Includes struct {
			Users  []apiUser  `json:"users"`
			Tweets []apiTweet `json:"tweets"`
		} `json:"includes"`
		Meta   apiMeta  `json:"meta"`
		Errors []apiErr `json:"errors"`
	}
	usersPage struct {
		Data   []apiUser `json:"data"`
		Meta   apiMeta   `json:"meta"`
		Errors []apiErr  `json:"errors"`
	}
	oneUser struct {
		Data   *apiUser `json:"data"`
		Errors []apiErr `json:"errors"`
	}
	oneTweet struct {
		Data     *apiTweet `json:"data"`
		Includes struct {
			Users  []apiUser  `json:"users"`
			Tweets []apiTweet `json:"tweets"`
		} `json:"includes"`
		Errors []apiErr `json:"errors"`
	}
)

// resources is what an answer with posts costs: every post (data and includes.tweets) and every user it carries.
func (p tweetsPage) resources() int {
	return len(p.Data) + len(p.Includes.Tweets) + len(p.Includes.Users)
}

// postQuery is the fields every read of posts asks for.
func postQuery() url.Values {
	return url.Values{"tweet.fields": {tweetFields}, "expansions": {expansions}, "user.fields": {feedUsers}}
}

// text is a post's whole text: the long post's (note_tweet) when it has one.
func (t apiTweet) text() string {
	if t.NoteTweet != nil && t.NoteTweet.Text != "" {
		return t.NoteTweet.Text
	}
	return t.Text
}

// ref is the post t references as kind (retweeted, quoted, replied_to), or "".
func (t apiTweet) ref(kind string) string {
	for _, r := range t.Referenced {
		if r.Type == kind {
			return r.ID
		}
	}
	return ""
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
