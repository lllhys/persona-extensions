package x_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/x/internal/x"
	"github.com/nwverse/persona-extensions/extensions/x/internal/xtest"
)

// fakeGrok answers each run: by default a post by the first handle of the prompt (or about the topic).
type fakeGrok struct {
	mu      sync.Mutex
	prompts []string
	err     error
	reply   func(prompt string) string
}

func (f *fakeGrok) Run(_ context.Context, path, prompt string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, prompt)
	if path != "/fake/grok" {
		return "", fmt.Errorf("ran %s", path)
	}
	if f.err != nil {
		return "", f.err
	}
	if f.reply != nil {
		return f.reply(prompt), nil
	}
	return defaultReply(prompt, len(f.prompts)), nil
}

func (f *fakeGrok) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
}

var promptHandle = regexp.MustCompile(`(?m)^- @([A-Za-z0-9_]+)$`)

// defaultReply is Grok's answer to prompt: one post by each of the first two handles, or one about the topics.
func defaultReply(prompt string, n int) string {
	var posts []string
	for i, m := range promptHandle.FindAllStringSubmatch(prompt, 2) {
		posts = append(posts, fmt.Sprintf(`{"url":"https://x.com/%s/status/%d","author":"%s","name":"Name %s","text":"Post %d by %s","at":"%s","why":"news"}`,
			m[1], 1000*n+i, m[1], m[1], i, m[1], base.Add(-time.Hour).Format(time.RFC3339)))
	}
	if len(posts) == 0 {
		posts = append(posts, fmt.Sprintf(`{"url":"https://x.com/topic_author/status/%d","author":"topic_author","text":"About the topic","at":"%s"}`,
			1000*n, base.Add(-2*time.Hour).Format(time.RFC3339)))
	}
	first := strings.SplitN(strings.TrimPrefix(posts[0], `{"url":"`), `"`, 2)[0]
	return fmt.Sprintf("```json\n{\"summary\":\"Run %d summary\",\"topics\":[{\"title\":\"Topic %d\",\"summary\":\"About it\",\"post_urls\":[%q]}],\"posts\":[%s]}\n```",
		n, n, first, strings.Join(posts, ","))
}

type grokEnv struct {
	c   *x.Connector
	g   *fakeGrok
	clk *clock
	dir string
	sec *xtest.Secrets
}

// newGrok is a connector whose grok is the fake (found at /fake/grok unless found is false), its data in dir ("" a
// new one).
func newGrok(t *testing.T, cfg x.Settings, g *fakeGrok, clk *clock, dir string, found bool) *grokEnv {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	if g == nil {
		g = &fakeGrok{}
	}
	if clk == nil {
		clk = &clock{t: base}
	}
	find := func(string) string {
		if found {
			return "/fake/grok"
		}
		return ""
	}
	e := &grokEnv{g: g, clk: clk, dir: dir, sec: &xtest.Secrets{}}
	e.c = x.New(cfg, x.Options{DataDir: dir, Locale: "en", Timezone: "UTC", Now: clk.Now, Grok: g, FindGrok: find})
	e.c.UseSecrets(e.sec)
	e.c.Load(context.Background())
	return e
}

func handles(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("handle_%03d", i)
	}
	return out
}

func TestGrokSourceSelection(t *testing.T) {
	ctx := context.Background()
	// auto, no X API sign-in, grok on the hub machine: Grok.
	e := newGrok(t, x.Settings{}, nil, nil, "", true)
	if e.c.Via() != x.SourceGrok {
		t.Fatalf("via %q", e.c.Via())
	}
	if st := e.c.Status(ctx); st.State != connector.StateOK || st.Account != "Grok" || st.Via != "grok on the hub machine" {
		t.Fatalf("status %+v", st)
	}
	// auto, neither: none, not configured, with how to set up both.
	e = newGrok(t, x.Settings{}, nil, nil, "", false)
	if st := e.c.Status(ctx); e.c.Via() != "" || st.State != connector.StateNotConfigured || len(st.Guide) != 2 ||
		!strings.Contains(st.Guide[0], "https://x.ai/cli/install.sh") || !strings.Contains(st.Guide[0], "grok login") {
		t.Fatalf("via %q status %+v", e.c.Via(), st)
	}
	// grok chosen but missing: Grok, not configured.
	e = newGrok(t, x.Settings{Source: x.SourceGrok}, nil, nil, "", false)
	if st := e.c.Status(ctx); e.c.Via() != x.SourceGrok || st.State != connector.StateNotConfigured || st.Error != "grok is not installed on the hub machine." {
		t.Fatalf("via %q status %+v", e.c.Via(), st)
	}
	// x_api only: grok is never looked for, the X API's sign-in is asked for.
	looked := false
	c := x.New(x.Settings{Source: x.SourceXAPI}, x.Options{Locale: "en", Grok: &fakeGrok{}, FindGrok: func(string) string { looked = true; return "/fake/grok" }})
	c.Load(ctx)
	if st := c.Status(ctx); looked || c.Via() != x.SourceXAPI || st.State != connector.StateNotSignedIn {
		t.Fatalf("x_api: looked %v via %q status %+v", looked, c.Via(), st)
	}
	// auto, signed in to the X API: the X API, even with grok there.
	s := newEnv(t, x.Settings{}, "")
	s.signIn(t)
	if s.c.Via() != x.SourceXAPI {
		t.Fatalf("signed in: via %q", s.c.Via())
	}
}

// Handles go 20 a run, at most 5 batches a refresh (the next one goes on), then the topics; every run counts against
// the day's cap, which holds across a restart and rolls over at 00:00 UTC.
func TestGrokBatchesAndDailyCap(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: base}
	dir := t.TempDir()
	cfg := x.Settings{Handles: append(handles(120), "@handle_000", "not a handle!", "Handle_001"), Topics: []string{" Go releases ", "Go releases", ""},
		GrokSearchesPerDay: intp(8)}
	e := newGrok(t, cfg, nil, clk, dir, true)
	n, err := e.c.RefreshGrok(ctx, false)
	if err != nil || n != 6 {
		t.Fatalf("first refresh: %d %v", n, err)
	}
	runs := e.g.runs()
	if len(runs) != 6 {
		t.Fatalf("%d runs", len(runs))
	}
	for i := range 5 {
		hs := promptHandle.FindAllStringSubmatch(runs[i], -1)
		if len(hs) != 20 || hs[0][1] != fmt.Sprintf("handle_%03d", 20*i) {
			t.Fatalf("batch %d: %d handles, first %v", i, len(hs), hs[0])
		}
		if !strings.Contains(runs[i], "Use your X search only") || !strings.Contains(runs[i], "Write \"summary\"") || !strings.Contains(runs[i], "English") {
			t.Fatalf("prompt %d: %s", i, runs[i])
		}
	}
	if !strings.Contains(runs[5], "- Go releases\n") || strings.Count(runs[5], "- Go releases") != 1 || len(promptHandle.FindAllString(runs[5], -1)) != 0 {
		t.Fatalf("topics run: %s", runs[5])
	}
	if used, most := e.c.GrokRuns(); used != 6 || most != 8 {
		t.Fatalf("runs %d of %d", used, most)
	}
	// Forced again: the sixth batch (handles 100–119), then the cap stops the topics.
	if n, err := e.c.RefreshGrok(ctx, true); err != nil || n != 2 {
		t.Fatalf("second refresh: %d %v", n, err)
	}
	runs = e.g.runs()
	if hs := promptHandle.FindAllStringSubmatch(runs[6], -1); len(hs) != 20 || hs[0][1] != "handle_100" {
		t.Fatalf("rotation: %v", hs)
	}
	if hs := promptHandle.FindAllStringSubmatch(runs[7], -1); hs[0][1] != "handle_000" {
		t.Fatalf("rotation wraps: %v", hs[0])
	}
	if _, err := e.c.RefreshGrok(ctx, true); err == nil || len(e.g.runs()) != 8 {
		t.Fatalf("over the cap: %v, %d runs", err, len(e.g.runs()))
	}
	// A restart remembers the day's runs.
	r := newGrok(t, cfg, nil, clk, dir, true)
	if used, _ := r.c.GrokRuns(); used != 8 {
		t.Fatalf("after a restart: %d runs", used)
	}
	if _, err := r.c.Call(ctx, "x_search", json.RawMessage(`{"query":"go"}`)); err == nil || len(r.g.runs()) != 0 {
		t.Fatalf("x_search over the cap: %v", err)
	}
	// The next UTC day starts over.
	clk.Add(13 * time.Hour)
	if used, _ := r.c.GrokRuns(); used != 0 {
		t.Fatalf("next day: %d runs", used)
	}
	if n, err := r.c.RefreshGrok(ctx, true); err != nil || n == 0 {
		t.Fatalf("next day: %d %v", n, err)
	}
	// A cap of 0 turns Grok's runs off.
	z := newGrok(t, x.Settings{Handles: handles(3), GrokSearchesPerDay: intp(0)}, nil, nil, "", true)
	if _, err := z.c.RefreshGrok(ctx, true); err == nil || len(z.g.runs()) != 0 {
		t.Fatalf("cap 0: %v", err)
	}
}

func TestGrokRefreshCadence(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: base}
	g := &fakeGrok{}
	e := newGrok(t, x.Settings{Handles: handles(3), GrokEveryHours: intp(6)}, g, clk, "", true)
	if n, _ := e.c.RefreshGrok(ctx, false); n != 1 {
		t.Fatalf("first: %d", n)
	}
	clk.Add(5 * time.Hour)
	if n, _ := e.c.RefreshGrok(ctx, false); n != 0 || len(g.runs()) != 1 {
		t.Fatalf("not due yet: %d", n)
	}
	clk.Add(time.Hour)
	if n, _ := e.c.RefreshGrok(ctx, false); n != 1 || len(g.runs()) != 2 {
		t.Fatalf("due: %d", n)
	}
	// A failed refresh waits grokRetry (30 minutes), not the whole interval.
	clk.Add(6 * time.Hour)
	g.mu.Lock()
	g.err = &x.GrokError{Kind: x.GrokFailed, Msg: "connection reset"}
	g.mu.Unlock()
	if n, _ := e.c.RefreshGrok(ctx, false); n != 0 || len(g.runs()) != 3 {
		t.Fatalf("failed: %d", n)
	}
	if st := e.c.Status(ctx); st.State != connector.StateError || !strings.Contains(st.Error, "connection reset") {
		t.Fatalf("status %+v", st)
	}
	g.mu.Lock()
	g.err = nil
	g.mu.Unlock()
	clk.Add(20 * time.Minute)
	if n, _ := e.c.RefreshGrok(ctx, false); n != 0 || len(g.runs()) != 3 {
		t.Fatal("retried too soon")
	}
	clk.Add(10 * time.Minute)
	if n, _ := e.c.RefreshGrok(ctx, false); n != 1 {
		t.Fatal("not retried")
	}
	if st := e.c.Status(ctx); st.State != connector.StateOK {
		t.Fatalf("recovered: %+v", st)
	}
	// Nothing to watch: no run.
	empty := newGrok(t, x.Settings{}, nil, nil, "", true)
	if n, err := empty.c.RefreshGrok(ctx, true); n != 0 || err != nil || len(empty.g.runs()) != 0 {
		t.Fatalf("nothing to watch: %d %v", n, err)
	}
}

func TestGrokAuthAndRateStatus(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		kind, state string
	}{{x.GrokAuth, connector.StateNotSignedIn}, {x.GrokRate, connector.StateRateLimited}} {
		g := &fakeGrok{err: &x.GrokError{Kind: tc.kind, Msg: "nope"}}
		e := newGrok(t, x.Settings{Handles: handles(45)}, g, nil, "", true)
		if n, _ := e.c.RefreshGrok(ctx, true); n != 0 || len(g.runs()) != 1 {
			t.Fatalf("%s: %d runs (stops at the first)", tc.kind, len(g.runs()))
		}
		st := e.c.Status(ctx)
		if st.State != tc.state || st.Error == "" {
			t.Fatalf("%s: %+v", tc.kind, st)
		}
		if tc.kind == x.GrokAuth && (len(st.Guide) != 1 || !strings.Contains(st.Guide[0], "grok login") || !strings.Contains(st.Guide[0], "7 days")) {
			t.Fatalf("auth guide %+v", st.Guide)
		}
		if _, err := e.c.Call(ctx, "x_search", json.RawMessage(`{"query":"go"}`)); err == nil {
			t.Fatalf("%s: x_search succeeded", tc.kind)
		}
	}
}

// Grok's digest: kept across a restart and an X sign-out, merged, pruned by retention, and removed when the source
// is set to x_api.
func TestGrokDigestKept(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: base}
	dir := t.TempDir()
	cfg := x.Settings{Handles: handles(2)}
	e := newGrok(t, cfg, nil, clk, dir, true)
	if n, err := e.c.RefreshGrok(ctx, true); n != 1 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	d := e.c.GrokDigest()
	if len(d.Summaries) != 1 || len(d.Topics) != 1 || len(d.Topics[0].Posts) != 1 || len(d.Posts) != 1 || d.Handles != 2 {
		t.Fatalf("digest %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "grok_digest.json")); err != nil {
		t.Fatal(err)
	}
	// Signing out of X keeps it.
	if err := e.c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "grok_digest.json")); err != nil {
		t.Fatalf("signing out of X removed Grok's digest: %v", err)
	}
	// A restart has it; a second refresh adds to the posts.
	r := newGrok(t, cfg, nil, clk, dir, true)
	if d := r.c.GrokDigest(); len(d.Topics) != 1 || len(d.Posts) != 1 {
		t.Fatalf("after a restart %+v", d)
	}
	clk.Add(7 * time.Hour)
	if n, _ := r.c.RefreshGrok(ctx, false); n != 1 {
		t.Fatal("not refreshed")
	}
	out, err := r.c.Call(ctx, "x_feed", json.RawMessage(`{"hours":168}`))
	if err != nil || !strings.Contains(out, "2 posts Grok found") {
		t.Fatalf("x_feed %q %v", out, err)
	}
	// Retention (3 days) drops them.
	clk.Add(4 * 24 * time.Hour)
	q := newGrok(t, cfg, nil, clk, dir, true)
	if out, _ := q.c.Call(ctx, "x_feed", json.RawMessage(`{"hours":168}`)); !strings.Contains(out, "0 posts Grok found") {
		t.Fatalf("not pruned: %s", out)
	}
	// x_api only: the file goes.
	newGrok(t, x.Settings{Source: x.SourceXAPI}, nil, clk, dir, true)
	if _, err := os.Stat(filepath.Join(dir, "grok_digest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("x_api kept Grok's digest: %v", err)
	}
}

func TestGrokTools(t *testing.T) {
	ctx := context.Background()
	g := &fakeGrok{}
	e := newGrok(t, x.Settings{Handles: []string{"example_author", "other_author"}}, g, nil, "", true)
	if _, err := e.c.RefreshGrok(ctx, true); err != nil {
		t.Fatal(err)
	}
	out, err := e.c.Call(ctx, "x_digest", nil)
	if err != nil || !strings.HasPrefix(out, "[Post text") || !strings.Contains(out, "found by Grok") || !strings.Contains(out, "Run 1 summary") ||
		!strings.Contains(out, "https://x.com/example_author/status/1000") || !strings.Contains(out, "Grok runs today (UTC): 1 of 30") {
		t.Fatalf("x_digest %q %v", out, err)
	}
	out, err = e.c.Call(ctx, "x_following", json.RawMessage(`{"query":"other"}`))
	if err != nil || !strings.Contains(out, "1 of the 2 accounts") || !strings.Contains(out, "@other_author · https://x.com/other_author") {
		t.Fatalf("x_following %q %v", out, err)
	}
	if _, err := e.c.Call(ctx, "x_post", json.RawMessage(`{"id":"1000"}`)); err == nil || !strings.Contains(err.Error(), "X API") {
		t.Fatalf("x_post in Grok mode: %v", err)
	}
	// x_search: its arguments are checked before anything runs.
	before := len(g.runs())
	for _, args := range []string{`{}`, `{"query":"  "}`, `{"query":"` + strings.Repeat("q", 301) + `"}`,
		`{"query":"q","handles":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 21), ",") + `]}`, `{"query":"q","handles":["bad handle"]}`,
		`{"query":"q","from":"2026-13-01"}`, `{"query":"q","from":"2026-11-06","to":"2026-11-01"}`, `{"query":5}`} {
		if _, err := e.c.Call(ctx, "x_search", json.RawMessage(args)); err == nil {
			t.Fatalf("x_search took %s", args)
		}
	}
	if len(g.runs()) != before {
		t.Fatal("a refused x_search ran grok")
	}
	g.mu.Lock()
	g.reply = func(string) string {
		return `Sure. {"summary":"People discuss Go 1.27.","posts":[{"url":"https://twitter.com/gopher_news/status/42?s=20","author":"someone_else","text":"Go 1.27 is out"},` +
			`{"url":"https://x.com/made_up/status/not-a-number","text":"invented"}]}`
	}
	g.mu.Unlock()
	out, err = e.c.Call(ctx, "x_search", json.RawMessage(`{"query":"Go 1.27 release","handles":["@gopher_news","Gopher_News"],"from":"2026-11-01","to":"2026-11-06"}`))
	if err != nil || !strings.HasPrefix(out, "[Post text") || !strings.Contains(out, "People discuss Go 1.27.") || !strings.Contains(out, "## Posts (1)") ||
		!strings.Contains(out, "@gopher_news") || !strings.Contains(out, "https://x.com/gopher_news/status/42\n") || strings.Contains(out, "invented") ||
		!strings.Contains(out, "Grok runs today (UTC): 2 of 30") {
		t.Fatalf("x_search %q %v", out, err)
	}
	prompt := g.runs()[len(g.runs())-1]
	if !strings.Contains(prompt, "The search: Go 1.27 release") || strings.Count(prompt, "- @gopher_news") != 1 || strings.Contains(prompt, "Gopher_News") ||
		!strings.Contains(prompt, "from 2026-11-01 to 2026-11-06") {
		t.Fatalf("search prompt: %s", prompt)
	}
	// No grok, or x_api only: refused without running anything.
	none := newGrok(t, x.Settings{Source: x.SourceGrok}, nil, nil, "", false)
	if _, err := none.c.Call(ctx, "x_search", json.RawMessage(`{"query":"go"}`)); err == nil || !strings.Contains(err.Error(), "grok login") {
		t.Fatalf("no grok: %v", err)
	}
	api := newGrok(t, x.Settings{Source: x.SourceXAPI}, nil, nil, "", true)
	if _, err := api.c.Call(ctx, "x_search", json.RawMessage(`{"query":"go"}`)); err == nil || len(api.g.runs()) != 0 {
		t.Fatalf("x_api: %v", err)
	}
	if _, err := api.c.Call(ctx, "x_digest", nil); err == nil {
		t.Fatal("x_digest with nothing set up")
	}
}

// Without grok_handles, the accounts the user follows (as the X API read them), the most active first.
func TestGrokUsesFollowing(t *testing.T) {
	e := newEnv(t, x.Settings{Source: x.SourceGrok}, "")
	e.f.Lock()
	e.f.Following = []xtest.User{{ID: "2001", Name: "Quiet", Username: "quiet_one", Followers: 10},
		{ID: "2002", Name: "Big", Username: "big_one", Followers: 9000}, {ID: "2003", Name: "Locked", Username: "locked_one", Followers: 99999}}
	e.f.Unlock()
	e.signIn(t)
	if err := e.sync(t); err != nil {
		t.Fatal(err)
	}
	if reqs := requests(e.f, "reverse_chronological"); len(reqs) != 0 {
		t.Fatal("Grok mode read the paid timeline")
	}
	if e.c.Via() != x.SourceGrok {
		t.Fatalf("via %q", e.c.Via())
	}
	out, err := e.c.Call(context.Background(), "x_following", nil)
	if err != nil || !strings.Contains(out, "accounts the user follows") {
		t.Fatalf("%q %v", out, err)
	}
	if i, j := strings.Index(out, "@locked_one"), strings.Index(out, "@big_one"); i < 0 || j < 0 || i > j {
		t.Fatalf("order: %s", out)
	}
}

// Run refreshes the Grok digest in its own loop, and returns when its context ends.
func TestGrokRunLoop(t *testing.T) {
	g := &fakeGrok{}
	clk := &clock{t: base}
	c := x.New(x.Settings{Handles: handles(2)}, x.Options{DataDir: t.TempDir(), Locale: "zh-Hans", Now: clk.Now, Grok: g, FindGrok: func(string) string { return "/fake/grok" }})
	c.UseSecrets(&xtest.Secrets{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.runs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the loop never ran grok")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(g.runs()[0], "Simplified Chinese") {
		t.Fatalf("prompt language: %s", g.runs()[0])
	}
	for c.GrokDigest().RefreshedAt.IsZero() { // the first refresh is kept
		if time.Now().After(deadline) {
			t.Fatal("the first refresh was not kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.RefreshGrokSoon() { // right after a refresh: the button is ignored
		t.Fatal("refresh asked for right after one")
	}
	clk.Add(31 * time.Minute)
	if !c.RefreshGrokSoon() {
		t.Fatal("refresh refused 31 minutes after the last")
	}
	for len(g.runs()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("refresh asked for, not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

// What grok_digest.json keeps is read before anything changes it, and whatever the X sign-in does around load: an
// x_search before Run's load, or a sign-out before or during it, keeps the day's count and the kept digest.
func TestGrokStateReadFirst(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: base}
	dir := t.TempDir()
	cfg := x.Settings{Handles: handles(2), GrokSearchesPerDay: intp(3)}
	e := newGrok(t, cfg, nil, clk, dir, true)
	if n, err := e.c.RefreshGrok(ctx, true); n != 1 || err != nil { // 1 run, 1 post kept
		t.Fatalf("%d %v", n, err)
	}
	fresh := func() (*x.Connector, *fakeGrok) {
		g := &fakeGrok{}
		c := x.New(cfg, x.Options{DataDir: dir, Locale: "en", Timezone: "UTC", Now: clk.Now, Grok: g, FindGrok: func(string) string { return "/fake/grok" }})
		c.UseSecrets(&xtest.Secrets{})
		return c, g
	}
	// x_search before load: it counts on top of the kept run, and the kept post stays.
	c, g := fresh()
	if _, err := c.Call(ctx, "x_search", json.RawMessage(`{"query":"go"}`)); err != nil || len(g.runs()) != 1 {
		t.Fatalf("x_search before load: %v", err)
	}
	c.Load(ctx)
	if used, _ := c.GrokRuns(); used != 2 {
		t.Fatalf("runs after x_search before load: %d, want 2", used)
	}
	if d := c.GrokDigest(); len(d.Posts) != 1 || len(d.Summaries) != 1 {
		t.Fatalf("digest lost: %+v", d)
	}
	// A sign-out before load (load then keeps nothing of the sign-in's): the Grok state is still read.
	c, _ = fresh()
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	c.Load(ctx)
	if used, _ := c.GrokRuns(); used != 2 {
		t.Fatalf("runs after a sign-out before load: %d, want 2", used)
	}
	if d := c.GrokDigest(); len(d.Posts) != 1 {
		t.Fatalf("digest after a sign-out before load: %+v", d)
	}
	// The cap holds across all of it: one run left today.
	if _, err := c.RefreshGrok(ctx, true); err != nil {
		t.Fatal(err)
	}
	if used, _ := c.GrokRuns(); used != 3 {
		t.Fatalf("runs: %d", used)
	}
	if _, err := c.Call(ctx, "x_search", json.RawMessage(`{"query":"go"}`)); err == nil {
		t.Fatal("x_search over the cap")
	}
}

// A refresh whose batches failed starts the next one with the first that failed, not past it; one where only the
// first failed moves on.
func TestGrokBatchRotationAfterFailures(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: base}
	g := &fakeGrok{err: &x.GrokError{Kind: x.GrokFailed, Msg: "connection reset"}}
	e := newGrok(t, x.Settings{Handles: handles(200), GrokSearchesPerDay: intp(100)}, g, clk, "", true) // 10 batches, 5 a refresh
	first := func(prompt string) string {
		if hs := promptHandle.FindAllStringSubmatch(prompt, 1); len(hs) > 0 {
			return hs[0][1]
		}
		return ""
	}
	if n, _ := e.c.RefreshGrok(ctx, true); n != 0 || len(g.runs()) != 5 {
		t.Fatalf("failed refresh: %d, %d runs", n, len(g.runs()))
	}
	g.mu.Lock()
	g.err = nil
	g.mu.Unlock()
	if n, _ := e.c.RefreshGrok(ctx, true); n != 5 {
		t.Fatalf("retry: %d", n)
	}
	if h := first(g.runs()[5]); h != "handle_000" {
		t.Fatalf("the retry started at %s, not the batch that failed", h)
	}
	// Batches 0–4 done: the next starts at 5. There the first fails and the rest do not: the one after moves on to 0.
	g.mu.Lock()
	g.reply = func(p string) string {
		if first(p) == "handle_100" {
			return "not json"
		}
		return defaultReply(p, 1)
	}
	g.mu.Unlock()
	if n, _ := e.c.RefreshGrok(ctx, true); n != 4 {
		t.Fatalf("partly failed: %d", n)
	}
	if h := first(g.runs()[10]); h != "handle_100" {
		t.Fatalf("started at %s", h)
	}
	if _, err := e.c.RefreshGrok(ctx, true); err != nil {
		t.Fatal(err)
	}
	if h := first(g.runs()[15]); h != "handle_000" {
		t.Fatalf("after a first batch that failed, started at %s, want the turn to move on", h)
	}
	// A batch past the first that fails: the next refresh starts with it.
	g.mu.Lock()
	g.reply = func(p string) string {
		if first(p) == "handle_060" {
			return "not json"
		}
		return defaultReply(p, 1)
	}
	g.mu.Unlock()
	if _, err := e.c.RefreshGrok(ctx, true); err != nil { // batches 0–4 (runs 15–19) were fine: 5–9 now
		t.Fatal(err)
	}
	if _, err := e.c.RefreshGrok(ctx, true); err != nil { // batches 0–4: batch 3 (handle_060) fails
		t.Fatal(err)
	}
	if _, err := e.c.RefreshGrok(ctx, true); err != nil {
		t.Fatal(err)
	}
	if h := first(g.runs()[30]); h != "handle_060" {
		t.Fatalf("after batch 3 failed, started at %s", h)
	}
}
