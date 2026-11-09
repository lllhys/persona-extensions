package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// --- synthetic feeds ---

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel>
  <title><![CDATA[Example issues]]></title>
  <link>https://example.com/issues</link>
  <atom:link href="https://rsshub.example/github/issue/example/repo" rel="self" type="application/rss+xml"/>
  <description>Synthetic</description>
  <item>
    <title><![CDATA[Second issue &amp; more]]></title>
    <link>https://example.com/issues/2</link>
    <guid isPermaLink="false">issue-2</guid>
    <pubDate>Tue, 03 Nov 2026 10:00:00 GMT</pubDate>
    <dc:creator>alice</dc:creator>
    <description><![CDATA[<p>Short <b>summary</b> two.</p>]]></description>
    <content:encoded><![CDATA[<p>Full text of the second issue.</p><script>alert(1)</script><p>Second paragraph.</p>]]></content:encoded>
    <category>bug</category>
  </item>
  <item>
    <title>First issue</title>
    <link>https://example.com/issues/1</link>
    <guid>issue-1</guid>
    <pubDate>Mon, 2 Nov 2026 09:00:00 +0800</pubDate>
    <author>bob@example.com (bob)</author>
    <description>&lt;p&gt;Escaped &lt;i&gt;html&lt;/i&gt; &amp;amp; text&lt;/p&gt;</description>
  </item>
</channel>
</rss>`

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title type="text">Example Atom</title>
  <link rel="self" href="https://example.org/atom.xml"/>
  <link rel="alternate" href="https://example.org/"/>
  <entry>
    <id>urn:example:1</id>
    <title type="html">A &lt;em&gt;first&lt;/em&gt; post</title>
    <link rel="alternate" href="https://example.org/1"/>
    <published>2026-11-01T08:00:00Z</published>
    <updated>2026-11-02T08:00:00Z</updated>
    <author><name>Carol</name></author>
    <summary>Plain summary</summary>
    <content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>XHTML <strong>body</strong></p><p>more</p></div></content>
    <category term="news"/>
  </entry>
  <entry>
    <id>urn:example:2</id>
    <title>Second</title>
    <link href="https://example.org/2"/>
    <updated>2026-11-03T08:00:00+02:00</updated>
    <content type="html">&lt;p&gt;HTML &amp;amp; body&lt;/p&gt;</content>
  </entry>
</feed>`

const jsonSample = `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "Example JSON",
  "home_page_url": "https://example.net/",
  "items": [
    {"id": 42, "url": "https://example.net/42", "title": "Numbered", "content_html": "<p>Hello <a href=\"x\">world</a></p>",
     "date_published": "2026-11-04T01:02:03Z", "authors": [{"name": "Dana"}], "tags": ["go"]},
    {"id": "b", "external_url": "https://example.net/b", "content_text": "Only text, no title",
     "date_modified": "2026-11-03T01:02:03+00:00", "author": {"name": "Eve"}}
  ]
}`

const rdfSample = `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel rdf:about="https://example.com/"><title>RDF feed</title><link>https://example.com/</link></channel>
  <item rdf:about="https://example.com/a"><title>RDF item</title><link>https://example.com/a</link><dc:date>2026-11-05T10:00:00Z</dc:date><description>rdf text</description></item>
</rdf:RDF>`

func TestParseRSS(t *testing.T) {
	f, err := parseFeed([]byte("\xef\xbb\xbf" + rssSample))
	if err != nil {
		t.Fatal(err)
	}
	if f.Format != "rss" || f.Title != "Example issues" || f.Link != "https://example.com/issues" || len(f.Entries) != 2 {
		t.Fatalf("feed %+v", f)
	}
	e := f.Entries[0]
	if e.Key != "issue-2" || e.Title != "Second issue & more" || e.Author != "alice" || e.Link != "https://example.com/issues/2" {
		t.Fatalf("entry %+v", e)
	}
	if !e.Published.Equal(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("published %v", e.Published)
	}
	if e.Summary != "Short summary two." {
		t.Fatalf("summary %q", e.Summary)
	}
	if e.Content != "Full text of the second issue.\nSecond paragraph." || strings.Contains(e.Content, "alert") {
		t.Fatalf("content %q", e.Content)
	}
	if !slices.Equal(e.Tags, []string{"bug"}) {
		t.Fatalf("tags %v", e.Tags)
	}
	e = f.Entries[1]
	if e.Summary != "Escaped html & text" || e.Author != "bob@example.com (bob)" {
		t.Fatalf("entry %+v", e)
	}
	if !e.Published.Equal(time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("published %v", e.Published)
	}
}

func TestParseAtom(t *testing.T) {
	f, err := parseFeed([]byte(atomSample))
	if err != nil {
		t.Fatal(err)
	}
	if f.Format != "atom" || f.Title != "Example Atom" || f.Link != "https://example.org/" || len(f.Entries) != 2 {
		t.Fatalf("feed %+v", f)
	}
	e := f.Entries[0]
	if e.Key != "urn:example:1" || e.Title != "A first post" || e.Link != "https://example.org/1" || e.Author != "Carol" {
		t.Fatalf("entry %+v", e)
	}
	if e.Content != "XHTML body\nmore" || e.Summary != "Plain summary" {
		t.Fatalf("content %q summary %q", e.Content, e.Summary)
	}
	if !e.Published.Equal(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)) || !slices.Equal(e.Tags, []string{"news"}) {
		t.Fatalf("entry %+v", e)
	}
	e = f.Entries[1]
	if e.Content != "HTML & body" || e.Summary != "HTML & body" || !e.Published.Equal(time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("entry %+v", e)
	}
}

func TestParseJSONFeed(t *testing.T) {
	f, err := parseFeed([]byte(jsonSample))
	if err != nil {
		t.Fatal(err)
	}
	if f.Format != "json" || f.Title != "Example JSON" || f.Link != "https://example.net/" || len(f.Entries) != 2 {
		t.Fatalf("feed %+v", f)
	}
	e := f.Entries[0]
	if e.Key != "42" || e.Title != "Numbered" || e.Content != "Hello world" || e.Author != "Dana" || e.Summary != "Hello world" {
		t.Fatalf("entry %+v", e)
	}
	e = f.Entries[1]
	if e.Key != "b" || e.Link != "https://example.net/b" || e.Title != "Only text, no title" || e.Author != "Eve" {
		t.Fatalf("entry %+v", e)
	}
	if _, err := parseFeed([]byte(`{"title": "not a feed"}`)); err == nil {
		t.Fatal("a JSON object without a JSON Feed version was taken")
	}
}

func TestParseRDFAndRefusals(t *testing.T) {
	f, err := parseFeed([]byte(rdfSample))
	if err != nil {
		t.Fatal(err)
	}
	if f.Format != "rdf" || f.Title != "RDF feed" || len(f.Entries) != 1 || f.Entries[0].Key != "https://example.com/a" ||
		f.Entries[0].Published.IsZero() {
		t.Fatalf("feed %+v", f)
	}
	for _, body := range []string{"", "<html><body>no</body></html>", "plain text", "{"} {
		if _, err := parseFeed([]byte(body)); err == nil {
			t.Errorf("%q read as a feed", body)
		}
	}
}

func TestEntryKeyFallback(t *testing.T) {
	f, err := parseFeed([]byte(`<rss><channel><title>t</title><item><title>a</title><link>https://e.example/a</link></item><item><title>b</title></item></channel></rss>`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Entries[0].Key != "https://e.example/a" || !strings.HasPrefix(f.Entries[1].Key, "h:") {
		t.Fatalf("keys %q %q", f.Entries[0].Key, f.Entries[1].Key)
	}
}

func TestHTMLText(t *testing.T) {
	for in, want := range map[string]string{
		"plain  text\n\n\n\nnext":                      "plain text\nnext",
		"<p>a</p><p>b<br/>c</p>":                       "a\nb\nc",
		"x <style>p{}</style><!-- c -->y":              "x y",
		`<img src="i.png" alt="pic &amp; more"> after`: "[pic & more] after",
		"<table><tr><td>1</td><td>2</td></tr></table>": "1 2",
		"&lt;tag&gt; &#20013;&#25991;":                 "<tag> 中文",
		"a b":                                          "a b",
	} {
		if got := htmlText(in); got != want {
			t.Errorf("htmlText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := clip("一二三四五", 3); got != "一二…" {
		t.Errorf("clip %q", got)
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for _, s := range []string{"Tue, 03 Nov 2026 10:00:00 GMT", "Tue, 3 Nov 2026 18:00:00 +0800", "2026-11-03T10:00:00Z", "2026-11-03T18:00:00+08:00", "2026-11-03 10:00:00"} {
		if got := parseTime(s); !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v", s, got)
		}
	}
	if !parseTime("yesterday").IsZero() {
		t.Error("an unreadable date was read")
	}
}

// RFC 822 zone names and GMT with an offset keep their offset, whatever the computer's own zone is (EDT and PDT are
// never both the local zone, so one of them was read as UTC on any computer).
func TestParseTimeZoneNames(t *testing.T) {
	for s, want := range map[string]time.Time{
		"Tue, 10 Jun 2003 04:00:00 EDT":      time.Date(2003, 6, 10, 8, 0, 0, 0, time.UTC),
		"Tue, 10 Jun 2003 04:00:00 PDT":      time.Date(2003, 6, 10, 11, 0, 0, 0, time.UTC),
		"Tue, 3 Nov 2026 05:00:00 EST":       time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		"03 Nov 26 02:00 PST":                time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		"Tue, 03 Nov 2026 18:00:00 GMT+8":    time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		"Tue, 03 Nov 2026 18:00:00 GMT+0800": time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		"Tue, 03 Nov 2026 18:30:00 UTC+8:30": time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		"Tue, 03 Nov 2026 05:00:00 GMT-05":   time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		"Tue, 03 Nov 2026 10:00:00 GMT":      time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
	} {
		if got := parseTime(s); !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, want %v", s, got, want)
		}
	}
	if !parseTime("Tue, 03 Nov 2026 18:00:00 GMT+99").IsZero() {
		t.Error("an impossible offset was read")
	}
}

// --- instance, routes and the access key ---

func TestParseInstance(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "https://rsshub.app",
		"https://RSSHub.Example/":  "https://rsshub.example",
		"https://example.com/rss/": "https://example.com/rss",
		"http://127.0.0.1:1200":    "http://127.0.0.1:1200",
		"http://localhost:1200/":   "http://localhost:1200",
		"http://[::1]:1200":        "http://[::1]:1200",
	} {
		u, err := parseInstance(in)
		if err != nil || u.String() != want {
			t.Errorf("parseInstance(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	for _, in := range []string{"http://rsshub.example", "http://192.168.1.2:1200", "ftp://rsshub.example", "https://u:p@rsshub.example",
		"https://rsshub.example/?key=x", "https://rsshub.example/#x", "rsshub.example", "https://"} {
		if _, err := parseInstance(in); err == nil {
			t.Errorf("parseInstance(%q) taken", in)
		}
	}
}

func TestDialURLRewritesLoopbackOnlyBehindAProxy(t *testing.T) {
	none := func(string) string { return "" }
	proxy := func(k string) string {
		if k == "HTTPS_PROXY" {
			return "http://ext:x@127.0.0.1:3128"
		}
		return ""
	}
	for in, want := range map[string]string{
		"http://127.0.0.1:1200/a?key=k": "http://host.persona.internal:1200/a?key=k",
		"http://localhost/a":            "http://host.persona.internal/a",
		"http://[::1]:1200/a":           "http://host.persona.internal:1200/a",
		"https://localhost:8443/a":      "https://localhost:8443/a",
		"https://rsshub.app/a":          "https://rsshub.app/a",
	} {
		u, _ := url.Parse(in)
		if got := dialURL(u, proxy).String(); got != want {
			t.Errorf("dialURL(%s) behind a proxy = %s, want %s", in, got, want)
		}
		if got := dialURL(u, none).String(); got != in {
			t.Errorf("dialURL(%s) without a proxy = %s", in, got)
		}
	}
}

func TestParseSource(t *testing.T) {
	base, _ := parseInstance("https://rsshub.example/sub")
	for in, want := range map[string]source{
		"/github/issue/nwverse/persona":                        {Route: "/github/issue/nwverse/persona"},
		"github/issue/nwverse/persona/":                        {Route: "/github/issue/nwverse/persona"},
		"rsshub://bilibili/user/video/2267573":                 {Route: "/bilibili/user/video/2267573"},
		"/arxiv/search/query=llm?limit=5&key=secret":           {Route: "/arxiv/search/query=llm?limit=5"},
		"https://rsshub.example/sub/telegram/channel/x?code=1": {Route: "/telegram/channel/x"},
		"https://blog.example.com/feed.xml#top":                {URL: "https://blog.example.com/feed.xml"},
		"http://news.example.org/rss":                          {URL: "http://news.example.org/rss"},
	} {
		got, err := parseSource(in, base)
		if err != nil || got != want {
			t.Errorf("parseSource(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "/", "/a/../b", "/a//b", "ftp://x.example/feed", "http://127.0.0.1:8080/feed", "http://localhost/feed",
		"https://host.persona.internal/x", "http://169.254.169.254/latest/meta-data", "http://[fe80::1]/x", "/a b", "https://u:p@x.example/feed"} {
		if got, err := parseSource(in, base); err == nil {
			t.Errorf("parseSource(%q) taken as %+v", in, got)
		}
	}
}

func TestInstanceURLAddsKeyCodeAndFormat(t *testing.T) {
	base, _ := parseInstance("https://rsshub.example")
	src := source{Route: "/qdaily/column/59?limit=5"}
	u, _ := instance{base: base}.url(src)
	if u.String() != "https://rsshub.example/qdaily/column/59?limit=5" {
		t.Fatalf("no key: %s", u)
	}
	u, _ = instance{base: base, key: "ILoveRSSHub"}.url(src)
	if q := u.Query(); q.Get("key") != "ILoveRSSHub" || q.Get("limit") != "5" || q.Has("code") {
		t.Fatalf("key: %s", u)
	}
	// RSSHub's documented example: code = md5('/qdaily/column/59' + 'ILoveRSSHub')
	u, _ = instance{base: base, key: "ILoveRSSHub", code: true, json: true}.url(src)
	if q := u.Query(); q.Get("code") != "0f820530128805ffc10351f22b5fd121" || q.Has("key") || q.Get("format") != "json" {
		t.Fatalf("code: %s", u)
	}
	// a feed of its own never gets the key
	u, _ = instance{base: base, key: "ILoveRSSHub", json: true}.url(source{URL: "https://blog.example.com/feed?x=1"})
	if u.String() != "https://blog.example.com/feed?x=1" {
		t.Fatalf("feed URL: %s", u)
	}
	in := instance{base: base, key: "k e/y"}
	if got := in.redact("GET https://rsshub.example/a?key=k+e%2Fy failed: k e/y"); strings.Contains(got, "e/y") || strings.Contains(got, "e%2Fy") {
		t.Fatalf("redact: %s", got)
	}
	if in.link(src) != "https://rsshub.example/qdaily/column/59?limit=5" {
		t.Fatalf("link %s", in.link(src))
	}
}

// --- a fake RSSHub ---

type fakeHub struct {
	t  *testing.T
	mu sync.Mutex
	// path -> body; a body changes its ETag
	feeds    map[string]string
	requests []*http.Request
	key      string // when set, ?key= must match
	srv      *httptest.Server
}

func newFakeHub(t *testing.T) *fakeHub {
	f := &fakeHub{t: t, feeds: map[string]string{
		"/github/issue/example/repo": rssSample,
		"/example/atom":              atomSample,
		"/example/json":              jsonSample,
	}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Clone(context.Background()))
	body, ok := f.feeds[r.URL.Path]
	key := f.key
	f.mu.Unlock()
	if r.URL.Path == "/" {
		fmt.Fprint(w, "<html>Welcome to RSSHub!</html>")
		return
	}
	if r.URL.Path == "/redirect-home" {
		http.Redirect(w, r, "http://localhost:1/x", http.StatusFound)
		return
	}
	if key != "" && r.URL.Query().Get("key") != key && r.URL.Query().Get("code") != accessCode(r.URL.Path, key) {
		http.Error(w, "Authentication failed. Access denied.", http.StatusForbidden)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("format") == "json" && body == rssSample {
		body = jsonSample
	}
	etag := `"` + hashText(body) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", "Wed, 04 Nov 2026 00:00:00 GMT")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprint(w, body)
}

func (f *fakeHub) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[path] = body
}

func (f *fakeHub) last() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeHub) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.URL.Path == path {
			n++
		}
	}
	return n
}

// testHub records what the program tells the hub.
type testHub struct {
	mu     sync.Mutex
	badges []string
	inval  int
}

func (h *testHub) Badge(_, text, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.badges = append(h.badges, text)
	return nil
}

func (h *testHub) Invalidate(string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inval++
	return nil
}

func (h *testHub) lastBadge() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.badges) == 0 {
		return "<none>"
	}
	return h.badges[len(h.badges)-1]
}

func testApp(t *testing.T, f *fakeHub, cfg config, key string) (*app, *testHub, string) {
	t.Helper()
	a := newApp(t.Logf)
	a.getenv = func(string) string { return "" }
	if cfg.InstanceURL == "" && f != nil {
		cfg.InstanceURL = f.srv.URL
	}
	a.configure(cfg, key)
	dir := t.TempDir()
	a.store = openStore(dir, t.Logf)
	h := &testHub{}
	a.hub = h
	return a, h, dir
}

func TestRefreshUsesETagAndKeepsReadState(t *testing.T) {
	f := newFakeHub(t)
	a, hub, dir := testApp(t, f, config{}, "")
	ctx := context.Background()
	fd, added, n, err := a.subscribe(ctx, "/github/issue/example/repo", "Issues", "开发")
	if err != nil || !added || n != 2 {
		t.Fatalf("subscribe: %+v %v %d %v", fd, added, n, err)
	}
	if fd.Title != "Issues" || fd.Tag != "开发" || fd.FeedTitle != "Example issues" || fd.unread() != 2 || hub.lastBadge() != "2" {
		t.Fatalf("feed %+v badge %s", fd, hub.lastBadge())
	}
	// the same source again is not a second subscription
	if _, added, _, err := a.subscribe(ctx, "github/issue/example/repo/", "", ""); err != nil || added {
		t.Fatalf("second subscribe added %v %v", added, err)
	}
	first := fd.Entries[0].ID
	if _, err := a.markRead("", []string{first}, false, true); err != nil {
		t.Fatal(err)
	}
	// unchanged: a conditional request, answered 304
	if n, err := a.refresh(ctx, fd.ID); err != nil || n != 0 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	if r := f.last(); r.Header.Get("If-None-Match") == "" || r.Header.Get("If-Modified-Since") != "Wed, 04 Nov 2026 00:00:00 GMT" {
		t.Fatalf("not conditional: %v", r.Header)
	}
	// changed: one new entry, the old ones neither doubled nor marked unread again
	f.set("/github/issue/example/repo", strings.Replace(rssSample, "<item>", `<item><title>Third</title><guid>issue-3</guid><pubDate>Wed, 04 Nov 2026 10:00:00 GMT</pubDate></item><item>`, 1))
	if n, err := a.refresh(ctx, fd.ID); err != nil || n != 1 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	got, _ := a.feeds().get(fd.ID)
	if len(got.Entries) != 3 || got.Entries[0].Title != "Third" || got.unread() != 2 {
		t.Fatalf("entries %+v", got.Entries)
	}
	for _, e := range got.Entries {
		if e.ID == first && !e.Read {
			t.Fatal("the read entry came back unread")
		}
	}
	// the state survives a restart
	again := openStore(dir, t.Logf)
	if g, ok := again.get(fd.ID); !ok || len(g.Entries) != 3 || g.unread() != 2 || g.ETag == "" {
		t.Fatalf("reopened %+v", g)
	}
	// a failure is kept on the feed and leaves its entries
	f.set("/github/issue/example/repo", "<html>oops</html>")
	if _, err := a.refresh(ctx, fd.ID); err == nil {
		t.Fatal("a page that is not a feed refreshed")
	}
	got, _ = a.feeds().get(fd.ID)
	if got.LastError == "" || len(got.Entries) != 3 {
		t.Fatalf("after a failure %+v", got)
	}
}

func TestRefreshAllAndCap(t *testing.T) {
	f := newFakeHub(t)
	a, hub, _ := testApp(t, f, config{}, "")
	ctx := context.Background()
	for _, r := range []string{"/github/issue/example/repo", "/example/atom", "/example/json"} {
		if _, _, _, err := a.subscribe(ctx, r, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	fresh, failed := a.refreshAll(ctx)
	if fresh != 0 || failed != 0 || hub.lastBadge() != "6" {
		t.Fatalf("refreshAll %d %d badge %s", fresh, failed, hub.lastBadge())
	}
	// at most entriesMost are kept, newest first
	fd := &feed{ID: "fx"}
	var many []parsedEntry
	for i := range entriesMost + 20 {
		many = append(many, parsedEntry{Key: fmt.Sprint(i), Title: fmt.Sprint(i), Published: time.Unix(int64(i)*60, 0)})
	}
	if n := mergeEntries(fd, many, time.Now()); n != entriesMost+20 || len(fd.Entries) != entriesMost || fd.Entries[0].Title != fmt.Sprint(entriesMost+19) {
		t.Fatalf("merge %d %d %s", n, len(fd.Entries), fd.Entries[0].Title)
	}
	if _, err := a.markRead("", nil, true, true); err != nil || a.feeds().unread() != 0 || hub.lastBadge() != "" {
		t.Fatalf("mark all: %v unread %d badge %q", err, a.feeds().unread(), hub.lastBadge())
	}
}

func TestAccessKeyAndErrorsNeverQuoteIt(t *testing.T) {
	f := newFakeHub(t)
	f.key = "s3cret-key"
	ctx := context.Background()
	a, _, _ := testApp(t, f, config{}, "")
	if _, _, _, err := a.subscribe(ctx, "/example/atom", "", ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("without the key: %v", err)
	}
	if err := a.health().Err; err == nil || !strings.Contains(err.Error(), "Authentication failed") {
		t.Fatalf("the instance's own words are not in the error: %v", err)
	}
	if !a.health().failing() {
		t.Fatal("a refused key does not count as a failing instance")
	}
	for _, auth := range []string{"key", "code"} {
		a, _, _ := testApp(t, f, config{AccessAuth: auth}, "s3cret-key")
		if _, _, _, err := a.subscribe(ctx, "/example/atom", "", ""); err != nil {
			t.Fatalf("%s: %v", auth, err)
		}
		q := f.last().URL.Query()
		if auth == "code" && (q.Has("key") || q.Get("code") != accessCode("/example/atom", "s3cret-key")) {
			t.Fatalf("code: %v", q)
		}
		fd, _ := a.feeds().bySource("/example/atom")
		raw, _ := json.Marshal(fd)
		if strings.Contains(string(raw), "s3cret") {
			t.Fatal("the key was kept with the feed")
		}
		if a.health().failing() {
			t.Fatal("instance failing after a good answer")
		}
	}
	// a feed of its own does not get the key even on the instance's host when it is not under the instance's path
	base, _ := parseInstance("https://rsshub.example/sub")
	src, err := parseSource("https://rsshub.example/other/feed", base)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := (instance{base: base, key: "s3cret-key"}).url(src); src.URL == "" || strings.Contains(u.String(), "s3cret") {
		t.Fatalf("a link outside the instance's path read as a route: %+v", src)
	}
}

func TestFetchRefusesRedirectToLoopbackAndHugeBodies(t *testing.T) {
	f := newFakeHub(t)
	a, _, _ := testApp(t, f, config{}, "")
	_, _, err := a.read(context.Background(), a.instance(), source{Route: "/redirect-home"}, "", "")
	if err == nil || !strings.Contains(err.Error(), "本机") {
		t.Fatalf("redirect to localhost: %v", err)
	}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := strings.Repeat("x", 1<<20)
		for range 9 {
			fmt.Fprint(w, chunk)
		}
	}))
	defer big.Close()
	u, _ := url.Parse(big.URL)
	if _, err := fetch(context.Background(), a.client, u, "", "", "test"); err == nil || !strings.Contains(err.Error(), "太大") {
		t.Fatalf("huge body: %v", err)
	}
	link, _ := url.Parse("http://169.254.169.254/latest")
	if _, err := fetch(context.Background(), a.client, link, "", "", "test"); err == nil {
		t.Fatal("dialled a link-local address")
	}
}

func TestStoreMovesACorruptFileAside(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openStore(dir, t.Logf)
	if len(s.list()) != 0 {
		t.Fatal("feeds from a corrupt file")
	}
	if _, err := os.Stat(filepath.Join(dir, stateFile+".corrupt")); err != nil {
		t.Fatal(err)
	}
}

// --- the protocol, the tools and the page ---

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Runtime     struct{ Command map[string]string }
		Contributes struct {
			Connectors []struct {
				ID            string
				Capabilities  []string
				ConfirmAlways []string `json:"confirm_always"`
			}
			UI []struct{ ID, Placement string }
		}
		Configuration struct {
			Properties map[string]struct{ Default any }
		}
		Secrets     []struct{ Name string }
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "rsshub" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q", m.ID, m.Version)
	}
	c := m.Contributes.Connectors
	if len(c) != 1 || c[0].ID != connectorID || !slices.Equal(c[0].Capabilities, []string{"status", "tools", "actions"}) {
		t.Fatalf("connectors %+v", c)
	}
	if !slices.Equal(c[0].ConfirmAlways, []string{actSubscribe, actUnsubscribe}) {
		t.Fatalf("confirm_always %v", c[0].ConfirmAlways)
	}
	if u := m.Contributes.UI; len(u) != 1 || u[0].ID != pageUI || u[0].Placement != wire.PlacementNavPage {
		t.Fatalf("ui %+v", u)
	}
	if m.Configuration.Properties["instance_url"].Default != defaultInstance || m.Configuration.Properties["refresh_minutes"].Default != float64(defaultRefresh) {
		t.Fatalf("defaults %+v", m.Configuration.Properties)
	}
	if len(m.Secrets) != 1 || m.Secrets[0].Name != "access_key" {
		t.Fatalf("secrets %+v", m.Secrets)
	}
	def, _ := url.Parse(defaultInstance)
	if !slices.Equal(m.Permissions.Network, []string{def.Host, sandboxHost}) {
		t.Fatalf("network %v", m.Permissions.Network)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/rsshub-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	for _, list := range [][]tool{tools, actions} {
		for _, tl := range list {
			if !strings.HasPrefix(tl.Name, connectorID+"_") {
				t.Errorf("tool %s does not start with the connector id", tl.Name)
			}
		}
	}
}

func TestConformance(t *testing.T) {
	f := newFakeHub(t)
	a := newApp(t.Logf)
	cfg, _ := json.Marshal(config{InstanceURL: f.srv.URL, RefreshMinutes: 60})
	exttest.Check(t, manifest, exttest.Serve(t, program(a)), cfg)
	h := exttest.CheckConnectors(t, manifest, exttest.Serve(t, program(newApp(t.Logf))), cfg)
	var st wire.ConnectorStatus
	if err := h.Call("connector.status", wire.ConnectorParams{Contribution: connectorID}, &st); err != nil || st.State != wire.ConnectorStateOk {
		t.Fatalf("status %+v %v", st, err)
	}
}

func TestStatusNotConfigured(t *testing.T) {
	a, _, _ := testApp(t, nil, config{InstanceURL: "http://rsshub.example"}, "")
	st := a.status()
	if st["state"] != "not_configured" || st["guide"] == nil {
		t.Fatalf("status %+v", st)
	}
	if fresh, failed := a.refreshAll(context.Background()); fresh != 0 || failed != 0 {
		t.Fatal("refreshed with an unusable instance")
	}
}

func call(t *testing.T, a *app, method string, params any) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(params)
	res, err := a.connector(context.Background(), method, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func TestToolsAndActions(t *testing.T) {
	f := newFakeHub(t)
	a, _, _ := testApp(t, f, config{}, "")
	p := func(name string, args any) map[string]any {
		return map[string]any{"contribution": connectorID, "name": name, "args": args, "request_id": "req-" + name + fmt.Sprint(args)}
	}

	res, err := call(t, a, "tools.list", p("", nil))
	if err != nil || len(res["tools"].([]any)) != 3 {
		t.Fatalf("tools.list %v %v", res, err)
	}
	res, _ = call(t, a, "actions.list", p("", nil))
	if len(res["tools"].([]any)) != 3 {
		t.Fatalf("actions.list %v", res)
	}

	// preview without subscribing
	res, err = call(t, a, "tools.call", p(toolPreview, map[string]any{"route": "/github/issue/example/repo", "limit": 1}))
	if err != nil {
		t.Fatal(err)
	}
	text := res["text"].(string)
	if !strings.Contains(text, "Example issues") || !strings.Contains(text, "Second issue & more") || strings.Contains(text, "First issue") {
		t.Fatalf("preview %s", text)
	}
	if len(a.feeds().list()) != 0 {
		t.Fatal("preview subscribed")
	}
	if _, err := call(t, a, "tools.call", p(toolPreview, map[string]any{"route": "/no/such/route"})); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("preview of a missing route: %v", err)
	}

	// describe makes no request and says what will happen
	before := f.count("/example/atom")
	res, err = call(t, a, "actions.describe", p(actSubscribe, map[string]any{"route": "/example/atom", "title": "Atom", "tag": "新闻"}))
	if err != nil || !strings.Contains(res["summary"].(string), "/example/atom") || !strings.Contains(res["summary"].(string), "新闻") {
		t.Fatalf("describe %v %v", res, err)
	}
	if f.count("/example/atom") != before {
		t.Fatal("actions.describe reached the network")
	}
	if _, err := call(t, a, "actions.describe", p(actSubscribe, map[string]any{"route": "http://127.0.0.1:9/feed"})); err == nil {
		t.Fatal("describe took a loopback feed")
	}

	// subscribe runs once per request id
	sub := p(actSubscribe, map[string]any{"route": "/example/atom", "title": "Atom", "tag": "新闻"})
	res, err = call(t, a, "actions.do", sub)
	if err != nil || !strings.Contains(res["text"].(string), "订阅了 Atom") {
		t.Fatalf("subscribe %v %v", res, err)
	}
	again, err := call(t, a, "actions.do", sub)
	if err != nil || again["text"] != res["text"] || f.count("/example/atom") != before+1 {
		t.Fatalf("the same request ran twice: %v %v", again, err)
	}
	if _, err := call(t, a, "actions.do", p(actSubscribe, map[string]any{"route": "/example/json", "tag": "新闻"})); err != nil {
		t.Fatal(err)
	}

	res, _ = call(t, a, "tools.call", p(toolFeeds, nil))
	text = res["text"].(string)
	atom, _ := a.feeds().bySource("/example/atom")
	if !strings.Contains(text, "[新闻]") || !strings.Contains(text, atom.ID) || !strings.Contains(text, "未读 2 / 2") {
		t.Fatalf("feeds %s", text)
	}

	res, _ = call(t, a, "tools.call", p(toolEntries, map[string]any{"unread_only": true, "query": "numbered"}))
	text = res["text"].(string)
	if !strings.Contains(text, "Numbered") || strings.Contains(text, "Second") {
		t.Fatalf("entries %s", text)
	}
	res, _ = call(t, a, "tools.call", p(toolEntries, map[string]any{"feed_id": atom.ID, "content": true}))
	text = res["text"].(string)
	if !strings.Contains(text, "XHTML body") || strings.Contains(text, "Numbered") {
		t.Fatalf("feed entries %s", text)
	}
	res, _ = call(t, a, "tools.call", p(toolEntries, map[string]any{"entry_id": atom.Entries[0].ID}))
	if text = res["text"].(string); !strings.Contains(text, "https://example.org") || !strings.Contains(text, "来自 Atom") {
		t.Fatalf("one entry %s", text)
	}
	if _, err := call(t, a, "tools.call", p(toolEntries, map[string]any{"feed_id": "nope"})); err == nil {
		t.Fatal("entries of a feed that is not there")
	}

	res, err = call(t, a, "actions.do", p(actMarkRead, map[string]any{"entry_ids": []string{atom.Entries[0].ID}}))
	if err != nil || !strings.Contains(res["text"].(string), "1 条标为已读") {
		t.Fatalf("mark read %v %v", res, err)
	}
	res, _ = call(t, a, "tools.call", p(toolEntries, map[string]any{"feed_id": atom.ID, "unread_only": true}))
	if text = res["text"].(string); !strings.Contains(text, "：1 条") {
		t.Fatalf("after mark read %s", text)
	}
	if _, err := call(t, a, "actions.describe", p(actMarkRead, map[string]any{})); err == nil {
		t.Fatal("mark_read without a target")
	}

	res, err = call(t, a, "actions.describe", p(actUnsubscribe, map[string]any{"feed_id": atom.ID}))
	if err != nil || !strings.Contains(res["summary"].(string), "Atom") {
		t.Fatalf("describe unsubscribe %v %v", res, err)
	}
	if _, err := call(t, a, "actions.do", p(actUnsubscribe, map[string]any{"feed_id": atom.ID})); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.feeds().get(atom.ID); ok {
		t.Fatal("still subscribed")
	}
	if _, err := call(t, a, "actions.do", p("rsshub_delete_everything", nil)); err == nil {
		t.Fatal("an unknown action ran")
	}
	if _, err := call(t, a, "status", map[string]any{"contribution": "other"}); err == nil {
		t.Fatal("another connector answered")
	}
}

func uiRequest(version int, action string, state string, values map[string]string) *ext.UIRequest {
	return &ext.UIRequest{UIParams: wire.UIParams{Contribution: pageUI, Placement: wire.PlacementNavPage, Size: wire.SizeFull,
		UIVersion: version, TZ: "Asia/Shanghai", Action: action, State: state, Values: values}}
}

func checkView(t *testing.T, v *wire.View) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("view: %v\n%s", err, raw)
	}
	return string(raw)
}

func TestPage(t *testing.T) {
	f := newFakeHub(t)
	a, _, _ := testApp(t, f, config{}, "")
	ctx := context.Background()
	for _, version := range []int{1, 2, 3, 4} {
		v, err := a.render(ctx, uiRequest(version, "", "", nil))
		if err != nil {
			t.Fatal(err)
		}
		if raw := checkView(t, v); !strings.Contains(raw, "还没有条目") {
			t.Fatalf("empty page v%d: %s", version, raw)
		}
	}

	// preview from the form, then subscribe to what was previewed
	res, err := a.action(ctx, uiRequest(4, "preview", "", map[string]string{"route": "/github/issue/example/repo"}))
	if err != nil {
		t.Fatal(err)
	}
	raw := checkView(t, res.View)
	if !strings.Contains(raw, "预览 · /github/issue/example/repo") || !strings.Contains(raw, "Second issue \\u0026 more") {
		t.Fatalf("preview page %s", raw)
	}
	res, _ = a.action(ctx, uiRequest(4, "subscribe", res.View.State, map[string]string{"route": "/github/issue/example/repo", "tag": "开发"}))
	raw = checkView(t, res.View)
	fd, ok := a.feeds().bySource("/github/issue/example/repo")
	if !ok || !strings.Contains(res.Toast, "订阅了") || !strings.Contains(raw, "取消订阅") {
		t.Fatalf("subscribe from the page: %s %s", res.Toast, raw)
	}

	// the overview groups by tag and counts unread
	res, _ = a.action(ctx, uiRequest(4, "back", res.View.State, nil))
	raw = checkView(t, res.View)
	if !strings.Contains(raw, "订阅 · 开发") || !strings.Contains(raw, `"label":"未读","value":"2"`) {
		t.Fatalf("overview %s", raw)
	}

	// opening an entry marks it read; marking it unread again works
	id := fd.Entries[0].ID
	res, _ = a.action(ctx, uiRequest(4, "entry:"+id, res.View.State, nil))
	raw = checkView(t, res.View)
	if !strings.Contains(raw, "Full text of the second issue.") || !strings.Contains(raw, "https://example.com/issues/2") || a.feeds().unread() != 1 {
		t.Fatalf("entry page (unread %d) %s", a.feeds().unread(), raw)
	}
	res, _ = a.action(ctx, uiRequest(4, "unread:"+id, res.View.State, nil))
	if a.feeds().unread() != 2 {
		t.Fatal("not unread again")
	}
	checkView(t, res.View)

	// only unread, then mark all read
	res, _ = a.action(ctx, uiRequest(4, "only_unread", "", map[string]string{"filter": "unread"}))
	checkView(t, res.View)
	res, _ = a.action(ctx, uiRequest(4, "read", res.View.State, nil))
	raw = checkView(t, res.View)
	if a.feeds().unread() != 0 || !strings.Contains(raw, "没有未读的了") {
		t.Fatalf("after reading all %s", raw)
	}

	// refresh, ping and unsubscribe from the page; an old app gets no newer components
	res, _ = a.action(ctx, uiRequest(1, "refresh", "", nil))
	raw = checkView(t, res.View)
	if !strings.Contains(res.Toast, "刷新好了") || strings.Contains(raw, "stat_grid") || strings.Contains(raw, "segmented") || strings.Contains(raw, `"empty"`) {
		t.Fatalf("v1 page %s %s", res.Toast, raw)
	}
	res, _ = a.action(ctx, uiRequest(4, "ping", "", nil))
	if res.Toast != "实例连通" {
		t.Fatalf("ping %s", res.Toast)
	}
	res, _ = a.action(ctx, uiRequest(4, "unsubscribe:"+fd.ID, "", nil))
	checkView(t, res.View)
	if len(a.feeds().list()) != 0 {
		t.Fatal("still subscribed")
	}

	// the page keeps only a few previews in memory, and the tool none
	for i := range previewsKept + 4 {
		_, _ = a.action(ctx, uiRequest(4, "preview", "", map[string]string{"route": fmt.Sprintf("/example/json?n=%d", i)}))
	}
	_, _ = a.callTool(ctx, toolPreview, []byte(`{"route":"/example/atom"}`))
	kept := 0
	a.previews.Range(func(k, _ any) bool {
		kept++
		if k == "/example/atom" {
			t.Error("a tool's preview was kept")
		}
		return true
	})
	if kept > previewsKept {
		t.Fatalf("%d previews kept", kept)
	}

	// a state naming things that are gone draws the overview
	v, _ := a.render(ctx, uiRequest(4, "", `{"f":"gone","e":"gone-1","p":"/gone"}`, nil))
	checkView(t, v)
	if v.State != "{}" {
		t.Fatalf("stale state kept: %s", v.State)
	}
}
