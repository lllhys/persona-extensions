package x_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/x/internal/x"
	"github.com/nwverse/persona-extensions/extensions/x/internal/xtest"
	"github.com/nwverse/persona-extensions/pluginkit"
)

// Every account, id and key here is synthetic (xtest).

var base = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// id is the n-th synthetic post id (larger is newer).
func id(n int) string { return fmt.Sprint(1800000000000000000 + n) }

type env struct {
	c   *x.Connector
	f   *xtest.Fake
	sec *xtest.Secrets
	clk *clock
	dir string
	// notified counts connector.changed {scope: inbox}.
	mu       sync.Mutex
	notified int
}

func (e *env) notifications() int { e.mu.Lock(); defer e.mu.Unlock(); return e.notified }

var me = xtest.User{ID: "1000", Name: "Example User", Username: "example_user", Followers: 50}

func newEnv(t *testing.T, cfg x.Settings, rules string) *env {
	t.Helper()
	e := &env{f: xtest.New(t), sec: &xtest.Secrets{}, clk: &clock{t: base}, dir: t.TempDir()}
	e.f.Me = me
	e.f.AddUser(me)
	e.c = x.New(cfg, x.Options{Base: e.f.URL(), DataDir: e.dir, Locale: "en", Timezone: "UTC", Now: e.clk.Now}).WithRules(rules)
	e.c.UseSecrets(e.sec)
	e.c.OnInboxChange(func() { e.mu.Lock(); e.notified++; e.mu.Unlock() })
	return e
}

func keyValues(k x.Keys) map[string]string {
	return map[string]string{"api_key": k.APIKey, "api_secret": k.APISecret, "access_token": k.AccessToken, "access_token_secret": k.AccessTokenSecret}
}

// signIn signs e in with the fake's keys.
func (e *env) signIn(t *testing.T) {
	t.Helper()
	p, err := e.c.LoginStep(context.Background(), "keys", keyValues(xtest.Keys))
	if err != nil || !p.Done || p.Account != "@example_user" {
		t.Fatalf("sign-in: %+v %v", p, err)
	}
}

func (e *env) sync(t *testing.T) error {
	t.Helper()
	return e.c.Sync(context.Background())
}

func (e *env) tool(t *testing.T, name, args string) string {
	t.Helper()
	out, err := e.c.Call(context.Background(), name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s %s: %v", name, args, err)
	}
	return out
}

// requests are the fake's requests to path's endpoint (a substring of the path).
func requests(f *xtest.Fake, part string) []url.Values {
	var out []url.Values
	for _, r := range f.Requests() {
		path, query, _ := strings.Cut(r, "?")
		if strings.Contains(path, part) {
			q, _ := url.ParseQuery(query)
			out = append(out, q)
		}
	}
	return out
}

func intp(n int) *int { return &n }

func TestLogin(t *testing.T) {
	e := newEnv(t, x.Settings{}, "")
	ctx := context.Background()
	// Neither way set up (no grok here: TestMain): not configured, with how to set up either.
	if st := e.c.Status(ctx); st.State != connector.StateNotConfigured || len(st.Guide) != 2 {
		t.Fatalf("before: %+v", st)
	}
	p, err := e.c.LoginStep(ctx, "", nil)
	if err != nil || p.Step != "keys" || len(p.Fields) != 4 || p.Hint == "" || !p.Fields[1].Secret || !p.Fields[3].Secret || p.Fields[0].Secret {
		t.Fatalf("first step: %+v %v", p, err)
	}
	if p, _ := e.c.LoginStep(ctx, "keys", map[string]string{"api_key": "only-one"}); p.Error == "" || p.Done {
		t.Fatalf("missing values: %+v", p)
	}
	wrong := xtest.Keys
	wrong.AccessTokenSecret = "not-the-secret"
	if p, err := e.c.LoginStep(ctx, "keys", keyValues(wrong)); err != nil || p.Done || p.Step != "keys" || !strings.Contains(p.Error, "did not accept") {
		t.Fatalf("401: %+v %v", p, err)
	}
	if data, _ := e.sec.Stored(); len(data) != 0 {
		t.Fatal("wrong keys were kept")
	}
	e.f.Lock()
	e.f.Fail["/2/users/me"] = http.StatusPaymentRequired
	e.f.Unlock()
	if p, _ := e.c.LoginStep(ctx, "keys", keyValues(xtest.Keys)); p.Done || !strings.Contains(p.Error, "credits") {
		t.Fatalf("402: %+v", p)
	}
	e.f.Lock()
	delete(e.f.Fail, "/2/users/me")
	e.f.Unlock()
	e.signIn(t)
	data, _ := e.sec.Stored()
	var kept struct {
		x.Keys
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(data, &kept); err != nil || kept.Keys != xtest.Keys || kept.UserID != "1000" || kept.Username != "example_user" {
		t.Fatalf("kept %+v %v", kept, err)
	}
	if st := e.c.Status(ctx); st.State != connector.StateOK || st.Account != "@example_user" {
		t.Fatalf("after: %+v", st)
	}
	// A new start reads the sign-in back from the hub's secret.
	again := x.New(x.Settings{}, x.Options{Base: e.f.URL(), Now: e.clk.Now})
	again.UseSecrets(e.sec)
	runCtx, cancel := context.WithCancel(ctx)
	go again.Run(runCtx)
	deadline := time.Now().Add(5 * time.Second)
	for again.Status(ctx).State != connector.StateOK {
		if time.Now().After(deadline) {
			t.Fatalf("restart: %+v", again.Status(ctx))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
}

func TestFollowingPagination(t *testing.T) {
	e := newEnv(t, x.Settings{FeedSource: x.FeedOff}, "")
	e.f.Lock()
	for i := range 2300 {
		e.f.Following = append(e.f.Following, xtest.User{ID: fmt.Sprint(5000 + i), Name: fmt.Sprintf("Account %d", i), Username: fmt.Sprintf("acct_%d", i),
			Description: "Writes about example topics", Followers: i})
	}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	reqs := requests(e.f, "/following")
	if len(reqs) != 3 || reqs[0].Get("max_results") != "1000" || reqs[1].Get("pagination_token") != "1000" || reqs[2].Get("pagination_token") != "2000" ||
		!strings.Contains(reqs[0].Get("user.fields"), "public_metrics") {
		t.Fatalf("following requests %v", reqs)
	}
	if out := e.tool(t, "x_following", `{"query":"acct_2299"}`); !strings.Contains(out, "1 of 2300 followed accounts") || !strings.Contains(out, "@acct_2299") {
		t.Fatalf("x_following: %s", out)
	}
	// Once a week: a day later it is not read again, a week later it is.
	e.f.ForgetRequests()
	e.clk.Add(24 * time.Hour)
	_ = e.sync(t)
	if n := len(requests(e.f, "/following")); n != 0 {
		t.Fatalf("read again after a day: %d", n)
	}
	e.clk.Add(6 * 24 * time.Hour)
	_ = e.sync(t)
	if n := len(requests(e.f, "/following")); n != 3 {
		t.Fatalf("next week: %d", n)
	}
}

// home fills the fake's home timeline with n posts by three authors, newest first, the newest at base - 1 minute.
func home(f *xtest.Fake, first, n int, at time.Time) []xtest.Tweet {
	authors := []xtest.User{{ID: "2001", Name: "Author One", Username: "author_one", Followers: 1000},
		{ID: "2002", Name: "Author Two", Username: "author_two", Followers: 10},
		{ID: "2003", Name: "Author Three", Username: "author_three", Followers: 100000}}
	for _, a := range authors {
		f.AddUser(a)
	}
	var ts []xtest.Tweet
	for i := range n {
		k := first + n - 1 - i
		ts = append(ts, f.AddTweet(xtest.Tweet{ID: id(k), Text: fmt.Sprintf("Post number %d", k), AuthorID: authors[k%3].ID,
			CreatedAt: at.Add(-time.Duration(i) * time.Minute), Likes: k}))
	}
	return ts
}

func TestHomeTimelineResumesSinceID(t *testing.T) {
	e := newEnv(t, x.Settings{DailyPostBudget: intp(5000)}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 30, base.Add(-time.Minute))
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	reqs := requests(e.f, "/timelines/reverse_chronological")
	if len(reqs) != 1 || reqs[0].Get("since_id") != "" || reqs[0].Get("start_time") == "" || reqs[0].Get("max_results") != "100" ||
		!strings.Contains(reqs[0].Get("tweet.fields"), "note_tweet") || !strings.Contains(reqs[0].Get("expansions"), "referenced_tweets.id.author_id") {
		t.Fatalf("first read %v", reqs)
	}
	used, _ := e.c.Budget()
	if used != 33 { // 30 posts, 3 authors
		t.Fatalf("budget used %d", used)
	}
	e.f.Lock()
	newer := home(e.f, 31, 2, base.Add(14*time.Minute))
	e.f.Home = append(newer, e.f.Home...)
	e.f.Unlock()
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	reqs = requests(e.f, "/timelines/reverse_chronological")
	if len(reqs) != 1 || reqs[0].Get("since_id") != id(30) {
		t.Fatalf("second read %v", reqs)
	}
	if out := e.tool(t, "x_feed", `{"limit":50}`); !strings.Contains(out, "32 kept posts") || !strings.Contains(out, "Post number 32") {
		t.Fatalf("x_feed: %s", out)
	}
	// What was read is kept across a restart.
	again := x.New(x.Settings{}, x.Options{Base: e.f.URL(), DataDir: e.dir, Now: e.clk.Now})
	again.UseSecrets(e.sec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go again.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := again.Call(context.Background(), "x_feed", json.RawMessage(`{"limit":50}`))
		if err == nil && strings.Contains(out, "32 kept posts") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a restart: %s %v", out, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Every post of the feed referencing two others by two other authors: the worst case per post. The day's count
// never passes the budget, and the next UTC day starts over.
func TestBudgetNeverExceeded(t *testing.T) {
	const budget = 100
	e := newEnv(t, x.Settings{DailyPostBudget: intp(budget)}, "")
	e.f.Lock()
	for i := range 200 {
		a := e.f.AddUser(xtest.User{ID: fmt.Sprint(3000 + 3*i), Name: "A", Username: fmt.Sprintf("a_%d", i)})
		b := e.f.AddUser(xtest.User{ID: fmt.Sprint(3001 + 3*i), Name: "B", Username: fmt.Sprintf("b_%d", i)})
		c := e.f.AddUser(xtest.User{ID: fmt.Sprint(3002 + 3*i), Name: "C", Username: fmt.Sprintf("c_%d", i)})
		q := e.f.AddTweet(xtest.Tweet{ID: id(100000 + 2*i), Text: "quoted", AuthorID: b.ID, CreatedAt: base.Add(-2 * time.Hour)})
		r := e.f.AddTweet(xtest.Tweet{ID: id(100001 + 2*i), Text: "replied to", AuthorID: c.ID, CreatedAt: base.Add(-2 * time.Hour)})
		e.f.Home = append(e.f.Home, xtest.Tweet{ID: id(200000 - i), Text: "quotes and replies", AuthorID: a.ID, CreatedAt: base.Add(-time.Duration(i) * time.Minute),
			Refs: []xtest.Ref{{Type: "quoted", ID: q.ID}, {Type: "replied_to", ID: r.ID}}, InReplyToUser: c.ID})
	}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	used, b := e.c.Budget()
	if b != budget || used > budget || used < budget-2*6 {
		t.Fatalf("used %d of %d", used, b)
	}
	for _, q := range requests(e.f, "/timelines/reverse_chronological") {
		if q.Get("max_results") == "" || q.Get("max_results") == "0" {
			t.Fatalf("request %v", q)
		}
	}
	// Spent: the same day reads no more of the feed.
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	_ = e.sync(t)
	if n := len(requests(e.f, "/timelines/reverse_chronological")); n != 0 {
		t.Fatalf("read %d more times with the budget spent", n)
	}
	if used2, _ := e.c.Budget(); used2 != used {
		t.Fatalf("used %d then %d", used, used2)
	}
	// x_post is refused without a request.
	if _, err := e.c.Call(context.Background(), "x_post", json.RawMessage(`{"id":"`+id(200000)+`"}`)); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("x_post with the budget spent: %v", err)
	}
	if n := len(requests(e.f, "/2/tweets/")); n != 0 {
		t.Fatalf("x_post asked X %d times", n)
	}
	// The next UTC day.
	e.clk.Add(12 * time.Hour)
	_ = e.sync(t)
	if n := len(requests(e.f, "/timelines/reverse_chronological")); n == 0 {
		t.Fatal("the next day read nothing")
	}
	if used3, _ := e.c.Budget(); used3 > budget {
		t.Fatalf("next day used %d", used3)
	}
}

func TestBudgetZeroTurnsTheFeedOff(t *testing.T) {
	e := newEnv(t, x.Settings{DailyPostBudget: intp(0)}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 5, base.Add(-time.Minute))
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if n := len(requests(e.f, "/timelines/")); n != 0 {
		t.Fatalf("home read %d times", n)
	}
	if n := len(requests(e.f, "/mentions")); n != 1 {
		t.Fatalf("mentions read %d times", n)
	}
}

func TestListsStopAtKnownID(t *testing.T) {
	e := newEnv(t, x.Settings{FeedSource: x.FeedLists, ListIDs: []string{"77", "not-a-list"}, DailyPostBudget: intp(5000)}, "")
	e.f.Lock()
	e.f.Lists["77"] = home(e.f, 1, 250, base.Add(-time.Minute))
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if reqs := requests(e.f, "/lists/"); len(reqs) != 3 {
		t.Fatalf("first read: %d requests %v", len(reqs), e.f.Requests())
	}
	for _, r := range e.f.Requests() {
		if strings.Contains(r, "not-a-list") || strings.Contains(r, "/timelines/") {
			t.Fatalf("asked %s", r)
		}
	}
	e.f.Lock()
	e.f.Lists["77"] = append(home(e.f, 251, 5, base.Add(10*time.Minute)), e.f.Lists["77"]...)
	e.f.Unlock()
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if reqs := requests(e.f, "/lists/77/tweets"); len(reqs) != 1 {
		t.Fatalf("second read: %d requests", len(reqs))
	}
	if out := e.tool(t, "x_feed", `{"query":"post number 255"}`); !strings.Contains(out, "1 kept posts") {
		t.Fatalf("x_feed: %s", out)
	}
}

func TestMentionsInbox(t *testing.T) {
	const rules = "Mentions from strangers are FYI"
	e := newEnv(t, x.Settings{FeedSource: x.FeedOff}, rules)
	friend := e.f.AddUser(xtest.User{ID: "4001", Name: "A Friend", Username: "a_friend", Followers: 20})
	other := e.f.AddUser(xtest.User{ID: "4002", Name: "Someone", Username: "someone_else", Followers: 5})
	e.f.Lock()
	e.f.Mentions = []xtest.Tweet{
		{ID: id(12), Text: "@example_user what do you think?", AuthorID: other.ID, ConversationID: id(10), CreatedAt: base.Add(-time.Hour)},
		{ID: id(11), Text: "@example_user replying to your post", AuthorID: friend.ID, ConversationID: id(9), InReplyToUser: me.ID,
			Refs: []xtest.Ref{{Type: "replied_to", ID: id(9)}}, CreatedAt: base.Add(-2 * time.Hour)},
		{ID: id(5), Text: "@example_user an old one", AuthorID: other.ID, CreatedAt: base.Add(-9 * 24 * time.Hour)},
	}
	// The user answered in the friend's conversation afterwards.
	e.f.Own = []xtest.Tweet{{ID: id(13), Text: "thanks!", AuthorID: me.ID, ConversationID: id(9), CreatedAt: base.Add(-30 * time.Minute)}}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if n := e.notifications(); n != 1 {
		t.Fatalf("notified %d times", n)
	}
	things, err := e.c.Collect(context.Background())
	if err != nil || len(things) != 2 {
		t.Fatalf("things %+v %v", things, err)
	}
	if err := pluginkit.CheckThings(things, rules, true); err != nil {
		t.Fatal(err)
	}
	a, b := things[0], things[1]
	if a.Kind != "mention" || a.Thread != id(10) || a.Sender != "@someone_else" || a.URL != "https://x.com/someone_else/status/"+id(12) ||
		a.Tier != connector.TierAction || a.Extra == nil || a.Extra.RuleTier != "action" || a.Done != "" || a.Last != id(12) || a.Rules != rules ||
		!strings.Contains(a.Context, "what do you think?") || len(a.Tags) != 1 || a.Tags[0].Value != other.ID {
		t.Fatalf("first thing %+v", a)
	}
	if b.Thread != id(9) || b.Done != connector.DoneReplied {
		t.Fatalf("answered thing %+v", b)
	}
	reqs := requests(e.f, "/mentions")
	if len(reqs) != 1 || reqs[0].Get("max_results") != "100" || reqs[0].Get("start_time") == "" {
		t.Fatalf("mentions requests %v", reqs)
	}
	if own := requests(e.f, "/users/1000/tweets"); len(own) != 1 {
		t.Fatalf("own posts read %d times", len(own))
	}
	// Nothing new: no notification, since_id resumes.
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	_ = e.sync(t)
	if n := e.notifications(); n != 1 {
		t.Fatalf("notified %d times", n)
	}
	if reqs := requests(e.f, "/mentions"); len(reqs) != 1 || reqs[0].Get("since_id") != id(12) {
		t.Fatalf("mentions requests %v", reqs)
	}
	if err := e.c.Reply(context.Background(), connector.InboxReply{Thread: id(10), Text: "hi"}); err == nil {
		t.Fatal("a reply went out")
	}
}

func TestRateLimited(t *testing.T) {
	e := newEnv(t, x.Settings{}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 5, base.Add(-time.Minute))
	e.f.Fail["home"] = http.StatusTooManyRequests
	e.f.Reset = base.Add(10 * time.Minute)
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err == nil {
		t.Fatal("no error")
	}
	st := e.c.Status(context.Background())
	if st.State != connector.StateRateLimited || !strings.Contains(st.Error, "12:10") {
		t.Fatalf("status %+v", st)
	}
	if n := len(requests(e.f, "/mentions")); n != 1 {
		t.Fatalf("the other endpoints were not read: %d", n)
	}
	// Until the reset the endpoint is not asked again; the others are.
	e.f.ForgetRequests()
	e.clk.Add(5 * time.Minute)
	_ = e.sync(t)
	if n := len(requests(e.f, "/timelines/")); n != 0 {
		t.Fatalf("asked during the window %d times", n)
	}
	if n := len(requests(e.f, "/mentions")); n != 1 {
		t.Fatalf("mentions %d", n)
	}
	e.f.Lock()
	delete(e.f.Fail, "home")
	e.f.Unlock()
	e.clk.Add(6 * time.Minute)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if st := e.c.Status(context.Background()); st.State != connector.StateOK {
		t.Fatalf("after the reset %+v", st)
	}
	if n := len(requests(e.f, "/timelines/")); n != 1 {
		t.Fatalf("after the reset %d", n)
	}
}

func TestCreditsAndRevokedKeys(t *testing.T) {
	for code, want := range map[int]string{http.StatusPaymentRequired: "credits", http.StatusForbidden: "credits", http.StatusUnauthorized: "refused the keys"} {
		e := newEnv(t, x.Settings{}, "")
		e.signIn(t)
		e.f.Lock()
		e.f.Fail["home"] = code
		e.f.Unlock()
		if err := e.sync(t); err == nil {
			t.Fatalf("%d: no error", code)
		}
		st := e.c.Status(context.Background())
		if st.State != connector.StateError || !strings.Contains(st.Error, want) || len(st.Guide) == 0 || st.Account != "@example_user" {
			t.Fatalf("%d: %+v", code, st)
		}
		if n := len(requests(e.f, "/mentions")); n != 0 {
			t.Fatalf("%d: went on to mentions", code)
		}
	}
}

func TestRetentionPrunes(t *testing.T) {
	e := newEnv(t, x.Settings{RetentionDays: intp(1), DailyPostBudget: intp(5000)}, "")
	e.f.AddUser(xtest.User{ID: "2001", Name: "Author One", Username: "author_one"})
	e.f.Lock()
	e.f.Home = []xtest.Tweet{
		{ID: id(2), Text: "recent post", AuthorID: "2001", CreatedAt: base.Add(-2 * time.Hour)},
		{ID: id(1), Text: "older post", AuthorID: "2001", CreatedAt: base.Add(-20 * time.Hour)},
	}
	e.f.Mentions = []xtest.Tweet{{ID: id(3), Text: "@example_user hello", AuthorID: "2001", CreatedAt: base.Add(-24 * time.Hour)}}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if out := e.tool(t, "x_feed", `{"hours":48}`); !strings.Contains(out, "2 kept posts") {
		t.Fatalf("before: %s", out)
	}
	e.clk.Add(10 * time.Hour) // the older post is now 30 hours old
	_ = e.sync(t)
	out := e.tool(t, "x_feed", `{"hours":168}`)
	if !strings.Contains(out, "1 kept posts") || strings.Contains(out, "older post") {
		t.Fatalf("after: %s", out)
	}
	raw, _ := os.ReadFile(e.dir + "/posts.json")
	if strings.Contains(string(raw), "older post") || !strings.Contains(string(raw), "recent post") {
		t.Fatalf("posts.json: %s", raw)
	}
	if things, _ := e.c.Collect(context.Background()); len(things) != 1 {
		t.Fatalf("mention dropped early: %d", len(things))
	}
	e.clk.Add(7 * 24 * time.Hour) // the mention is over 8 days old
	_ = e.sync(t)
	if things, _ := e.c.Collect(context.Background()); len(things) != 0 {
		t.Fatalf("mention kept: %+v", things)
	}
}

func TestLogoutWipesTheDataDir(t *testing.T) {
	e := newEnv(t, x.Settings{}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 5, base.Add(-time.Minute))
	e.f.Mentions = []xtest.Tweet{{ID: id(50), Text: "@example_user hi", AuthorID: "2001", CreatedAt: base.Add(-time.Hour)}}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) < 4 {
		t.Fatalf("data dir has %d files", len(entries))
	}
	_ = os.WriteFile(e.dir+"/leftover.json", []byte("{}"), 0o600)
	if err := e.c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
	if data, deleted := e.sec.Stored(); !deleted || len(data) != 0 {
		t.Fatal("the secret was not deleted")
	}
	if st := e.c.Status(context.Background()); st.State != connector.StateNotConfigured {
		t.Fatalf("status %+v", st)
	}
	if things, _ := e.c.Collect(context.Background()); len(things) != 0 {
		t.Fatalf("things %+v", things)
	}
	if _, err := e.c.Call(context.Background(), "x_feed", nil); err == nil {
		t.Fatal("x_feed signed out")
	}
	if e.notifications() < 2 {
		t.Fatal("the inbox was not told")
	}
}

func TestTools(t *testing.T) {
	e := newEnv(t, x.Settings{DailyPostBudget: intp(5000)}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 9, base.Add(-time.Minute))
	e.f.Following = []xtest.User{{ID: "2001", Name: "Author One", Username: "author_one", Description: "Writes about databases", Followers: 1000}}
	orig := xtest.Tweet{ID: id(900), Text: "The original post", AuthorID: "2002", CreatedAt: base.Add(-3 * time.Hour), Likes: 4}
	e.f.Tweets[orig.ID] = orig
	e.f.Tweets[id(901)] = xtest.Tweet{ID: id(901), Text: "Quoting it", Note: "Quoting it, and the long text of a long post", AuthorID: "2001",
		CreatedAt: base.Add(-time.Hour), Refs: []xtest.Ref{{Type: "quoted", ID: orig.ID}}}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	tools := e.c.Tools()
	if len(tools) != 5 {
		t.Fatalf("tools %v", tools)
	}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.Name, "x_") || !json.Valid(tl.InputSchema) || !strings.Contains(tl.Description, "third-party") {
			t.Fatalf("tool %+v", tl)
		}
	}
	out := e.tool(t, "x_feed", `{"author":"@AUTHOR_ONE"}`)
	if !strings.HasPrefix(out, "[Post text") || !strings.Contains(out, "3 kept posts") || strings.Contains(out, "@author_two") ||
		!strings.Contains(out, "https://x.com/author_one/status/") {
		t.Fatalf("x_feed author: %s", out)
	}
	if out := e.tool(t, "x_feed", `{"query":"NUMBER 7","limit":1}`); !strings.Contains(out, "1 kept posts") || !strings.Contains(out, "Post number 7") {
		t.Fatalf("x_feed query: %s", out)
	}
	if out := e.tool(t, "x_following", `{"query":"databases"}`); !strings.Contains(out, "Author One @author_one · 1000 followers · 3 posts") ||
		!strings.Contains(out, "Bio: Writes about databases") {
		t.Fatalf("x_following: %s", out)
	}
	if out := e.tool(t, "x_digest", `{"window":"3d"}`); !strings.Contains(out, "X digest, last 3d: 9 posts seen") || !strings.Contains(out, "## Top posts") ||
		!strings.Contains(out, "## Most active followed accounts") || !strings.Contains(out, "@author_one: 3 posts") {
		t.Fatalf("x_digest: %s", out)
	}
	before, _ := e.c.Budget()
	out = e.tool(t, "x_post", `{"id":"`+id(901)+`"}`)
	if !strings.Contains(out, "and the long text of a long post") || !strings.Contains(out, "Quotes:\nAuthor Two @author_two") ||
		!strings.Contains(out, "The original post") {
		t.Fatalf("x_post: %s", out)
	}
	if after, _ := e.c.Budget(); after != before+4 { // two posts, two users
		t.Fatalf("budget %d → %d", before, after)
	}
	if _, err := e.c.Call(context.Background(), "x_post", json.RawMessage(`{"id":"12a"}`)); err == nil {
		t.Fatal("x_post took a bad id")
	}
	if _, err := e.c.Call(context.Background(), "x_post", json.RawMessage(`{"id":"`+id(999)+`"}`)); err == nil {
		t.Fatal("x_post found a missing post")
	}
}

// No key shows anywhere: statuses in every state, prompts, tools, inbox things, errors.
func TestNoKeyInOutput(t *testing.T) {
	e := newEnv(t, x.Settings{Proxy: ""}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 5, base.Add(-time.Minute))
	e.f.Mentions = []xtest.Tweet{{ID: id(50), Text: "@example_user hi", AuthorID: "2001", CreatedAt: base.Add(-time.Hour)}}
	e.f.Unlock()
	var outputs []string
	add := func(v any) {
		b, _ := json.Marshal(v)
		outputs = append(outputs, string(b))
	}
	ctx := context.Background()
	p, _ := e.c.LoginStep(ctx, "", nil)
	add(p)
	wrong := xtest.Keys
	wrong.APISecret = "wrong"
	p, _ = e.c.LoginStep(ctx, "keys", keyValues(wrong))
	add(p)
	e.signIn(t)
	add(e.sync(t))
	add(e.c.Status(ctx))
	things, _ := e.c.Collect(ctx)
	add(things)
	for _, call := range [][2]string{{"x_digest", `{}`}, {"x_feed", `{}`}, {"x_following", `{}`}, {"x_post", `{"id":"` + id(1) + `"}`}, {"x_post", `{"id":"1"}`}} {
		out, err := e.c.Call(ctx, call[0], json.RawMessage(call[1]))
		add(out)
		if err != nil {
			add(err.Error())
		}
	}
	for _, code := range []int{429, 402, 401, 500} {
		e.f.Lock()
		e.f.Fail["home"], e.f.Reset = code, e.clk.Now().Add(time.Minute)
		e.f.Unlock()
		e.clk.Add(2 * time.Minute)
		if err := e.sync(t); err != nil {
			add(err.Error())
		}
		add(e.c.Status(ctx))
	}
	for _, out := range outputs {
		for _, k := range []string{xtest.Keys.APIKey, xtest.Keys.APISecret, xtest.Keys.AccessToken, xtest.Keys.AccessTokenSecret} {
			if strings.Contains(out, k) {
				t.Fatalf("a key in %s", out)
			}
		}
	}
	// Nor in the data directory: only the hub's secret has them.
	entries, _ := os.ReadDir(e.dir)
	for _, en := range entries {
		raw, _ := os.ReadFile(e.dir + "/" + en.Name())
		if strings.Contains(string(raw), xtest.Keys.AccessTokenSecret) || strings.Contains(string(raw), xtest.Keys.APISecret) {
			t.Fatalf("a key in %s", en.Name())
		}
	}
}

// A home read that fails on its second page keeps since_id where it was: the next sync reads what lies between the
// old cursor and the oldest post already read (until_id), then what is newer, and loses nothing.
func TestHomeReadResumesAfterAFailedPage(t *testing.T) {
	e := newEnv(t, x.Settings{DailyPostBudget: intp(5000)}, "")
	e.f.Lock()
	e.f.Home = home(e.f, 1, 250, base.Add(-time.Minute))
	e.f.Fail["home"] = http.StatusServiceUnavailable
	e.f.FailAfter["home"] = 1
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err == nil {
		t.Fatal("no error")
	}
	if n := len(requests(e.f, "/timelines/reverse_chronological")); n != 2 {
		t.Fatalf("first sync asked %d times", n)
	}
	e.f.Lock()
	delete(e.f.Fail, "home")
	newer := home(e.f, 251, 2, base.Add(14*time.Minute))
	e.f.Home = append(newer, e.f.Home...)
	e.f.Unlock()
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	// Page 1 was id(250)..id(151): the gap (100 then 50), then what came since id(250).
	reqs := requests(e.f, "/timelines/reverse_chronological")
	if len(reqs) != 3 || reqs[0].Get("until_id") != id(151) || reqs[0].Get("since_id") != "" || reqs[0].Get("start_time") == "" ||
		reqs[1].Get("until_id") != id(151) || reqs[1].Get("pagination_token") == "" ||
		reqs[2].Get("since_id") != id(250) || reqs[2].Get("until_id") != "" {
		t.Fatalf("second sync %v", reqs)
	}
	if d := e.c.Digest("3d"); d.Seen != 252 {
		t.Fatalf("kept %d posts", d.Seen)
	}
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if reqs := requests(e.f, "/timelines/reverse_chronological"); len(reqs) != 1 || reqs[0].Get("since_id") != id(252) || reqs[0].Get("until_id") != "" {
		t.Fatalf("third sync %v", reqs)
	}
}

// More mentions than a sync reads: the backlog comes in on the next syncs instead of being skipped.
func TestMentionsBacklogIsReadLater(t *testing.T) {
	e := newEnv(t, x.Settings{FeedSource: x.FeedOff}, "")
	other := e.f.AddUser(xtest.User{ID: "4002", Name: "Someone", Username: "someone_else", Followers: 5})
	e.f.Lock()
	for i := range 350 {
		e.f.Mentions = append(e.f.Mentions, xtest.Tweet{ID: id(350 - i), Text: "@example_user hello", AuthorID: other.ID, ConversationID: id(350 - i),
			CreatedAt: base.Add(-time.Duration(i+1) * time.Minute)})
	}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if things, _ := e.c.Collect(context.Background()); len(things) != 300 {
		t.Fatalf("first sync: %d mentions", len(things))
	}
	e.f.Lock()
	e.f.Mentions = append([]xtest.Tweet{{ID: id(351), Text: "@example_user new", AuthorID: other.ID, ConversationID: id(351), CreatedAt: base.Add(10 * time.Minute)}}, e.f.Mentions...)
	e.f.Unlock()
	e.f.ForgetRequests()
	e.clk.Add(15 * time.Minute)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	reqs := requests(e.f, "/mentions")
	if len(reqs) != 2 || reqs[0].Get("until_id") != id(51) || reqs[1].Get("since_id") != id(350) || reqs[1].Get("until_id") != "" {
		t.Fatalf("second sync %v", reqs)
	}
	if things, _ := e.c.Collect(context.Background()); len(things) != 351 {
		t.Fatalf("second sync: %d mentions", len(things))
	}
}

// Two x_post calls at once with room for one: the second is refused while the first's worst case is reserved, and
// what the first spent survives a restart before the next sync.
func TestBudgetReservedAndKept(t *testing.T) {
	cfg := x.Settings{FeedSource: x.FeedOff, DailyPostBudget: intp(6)}
	e := newEnv(t, cfg, "")
	e.f.Lock()
	a := e.f.AddUser(xtest.User{ID: "5001", Name: "A", Username: "a_author"})
	b := e.f.AddUser(xtest.User{ID: "5002", Name: "B", Username: "b_author"})
	c := e.f.AddUser(xtest.User{ID: "5003", Name: "C", Username: "c_author"})
	q := e.f.AddTweet(xtest.Tweet{ID: id(1), Text: "quoted", AuthorID: b.ID, CreatedAt: base.Add(-time.Hour)})
	r := e.f.AddTweet(xtest.Tweet{ID: id(2), Text: "replied to", AuthorID: c.ID, CreatedAt: base.Add(-time.Hour)})
	p := e.f.AddTweet(xtest.Tweet{ID: id(3), Text: "quotes and replies", AuthorID: a.ID, CreatedAt: base.Add(-time.Minute),
		Refs: []xtest.Ref{{Type: "quoted", ID: q.ID}, {Type: "replied_to", ID: r.ID}}, InReplyToUser: c.ID})
	e.f.Unlock()
	e.signIn(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.f.Lock()
	e.f.Before = func(path string) {
		if strings.HasPrefix(path, "/2/tweets/") {
			once.Do(func() { close(started); <-release })
		}
	}
	e.f.Unlock()
	args := json.RawMessage(`{"id":"` + p.ID + `"}`)
	first := make(chan error, 1)
	go func() { _, err := e.c.Call(context.Background(), "x_post", args); first <- err }()
	<-started
	second := make(chan error, 1)
	go func() { _, err := e.c.Call(context.Background(), "x_post", args); second <- err }()
	select {
	case err := <-second:
		if err == nil || !strings.Contains(err.Error(), "budget") {
			t.Fatalf("second x_post: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second x_post did not answer")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if used, _ := e.c.Budget(); used != 6 {
		t.Fatalf("used %d", used)
	}
	if n := len(requests(e.f, "/2/tweets/")); n != 1 {
		t.Fatalf("asked X %d times", n)
	}
	// A restart before the next sync remembers the spend.
	again := x.New(cfg, x.Options{Base: e.f.URL(), DataDir: e.dir, Now: e.clk.Now})
	again.UseSecrets(e.sec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go again.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if used, _ := again.Budget(); used == 6 {
			break
		}
		if time.Now().After(deadline) {
			used, _ := again.Budget()
			t.Fatalf("after a restart used %d", used)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
