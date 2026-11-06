package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// fakeSite serves the API's paths from a map; a path set to "" answers 500, a missing one the API's null.
type fakeSite struct {
	srv  *httptest.Server
	mu   sync.Mutex
	body map[string]string
	hits map[string]int
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{body: map[string]string{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body, ok := f.body[r.URL.Path]
		f.hits[r.URL.Path]++
		f.mu.Unlock()
		switch {
		case !ok:
			fmt.Fprint(w, "null")
		case body == "":
			http.Error(w, "down", http.StatusInternalServerError)
		default:
			fmt.Fprint(w, body)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body[path] = body
}

func (f *fakeSite) hit(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeSite) item(it map[string]any) {
	b, _ := json.Marshal(it)
	f.set(fmt.Sprintf("/item/%v.json", it["id"]), string(b))
}

var testNow = time.Now().Unix()

// sample is a small front page: a link story with two comments (one deleted), an Ask HN with HTML text, a plain-http
// link, and a story that is gone.
func sample(f *fakeSite) {
	f.set("/topstories.json", "[1,2,3,4]")
	f.set("/askstories.json", "[2]")
	f.item(map[string]any{"id": 1, "type": "story", "by": "alice", "time": testNow - 7200, "title": "Rust in the kernel", "url": "https://www.example.com/rust", "score": 250, "descendants": 3, "kids": []int{11, 12, 13}})
	f.item(map[string]any{"id": 11, "type": "comment", "by": "bob", "time": testNow - 600, "text": "Great <i>news</i>.<p>See <a href=\"https://example.org/a/very/long/link\" rel=\"nofollow\">https://example.org/a/very/...</a> &amp; more"})
	f.item(map[string]any{"id": 12, "type": "comment", "deleted": true})
	f.item(map[string]any{"id": 13, "type": "comment", "by": "carol", "time": testNow - 60, "text": "<pre><code>  fn main() {}\n</code></pre>"})
	f.item(map[string]any{"id": 2, "type": "story", "by": "dave", "time": testNow - 86400*2, "title": "Ask HN: What are you reading?", "text": "Books &#x2F; papers?<p>Anything goes.", "score": 40, "descendants": 0})
	f.item(map[string]any{"id": 3, "type": "story", "by": "erin", "time": testNow - 30, "title": "An old site", "url": "http://plain.example.net/x", "score": 5})
	f.item(map[string]any{"id": 4, "type": "story", "dead": true})
}

func testApp(t *testing.T, f *fakeSite, cfg Config) *app {
	t.Helper()
	a := newApp()
	a.logf = t.Logf
	a.site = newSite(f.srv.URL)
	if cfg.List != "" {
		a.cfg = cfg
	} else {
		a.cfg, a.cfg.List = cfg, newApp().cfg.List
	}
	return a
}

func req(locale string, ui int, state any, action string, values map[string]string) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Contribution, r.Locale, r.UIVersion, r.Action, r.Values = pageUI, locale, ui, action, values
	if state != nil {
		r.State = ext.EncodeState(state)
	}
	return r
}

// texts collects every text a view shows, in order.
func viewTexts(v *wire.View) []string {
	out := []string{v.Title}
	for _, s := range v.Sections {
		out = append(out, s.Title, s.Footer)
		for _, c := range s.Items {
			out = append(out, c.Title, c.Subtitle, c.Text, c.URL)
			for _, ch := range c.Choices {
				out = append(out, ch.Title)
			}
			if c.Badge != nil {
				out = append(out, c.Badge.Text)
			}
		}
	}
	return out
}

func joined(v *wire.View) string { return strings.Join(viewTexts(v), "\n") }

func checkView(t *testing.T, v *wire.View) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("view: %v\n%s", err, raw)
	}
}

func TestConformance(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	exttest.Check(t, manifest, exttest.Serve(t, program(testApp(t, f, Config{}))), json.RawMessage(`{"keywords": ["rust"], "min_score": 10}`))
}

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Runtime     struct{ Command map[string]string }
		Contributes struct {
			Connectors []struct {
				ID                  string
				Capabilities, Hosts []string
			}
			UI []struct{ ID, Placement string }
		}
		Configuration struct {
			Properties map[string]struct {
				Default any
				OneOf   []struct{ Const string } `json:"oneOf"`
			}
		}
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "hackernews" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q, about %+v", m.ID, m.Version, about)
	}
	if c := m.Contributes.Connectors; len(c) != 1 || c[0].ID != connectorID || !slices.Equal(c[0].Capabilities, []string{"status", "tools"}) ||
		!slices.Equal(c[0].Hosts, []string{"news.ycombinator.com"}) {
		t.Fatalf("connectors %+v", c)
	}
	if u := m.Contributes.UI; len(u) != 1 || u[0].ID != pageUI || u[0].Placement != wire.PlacementPluginPage {
		t.Fatalf("ui %+v", u)
	}
	if !slices.Equal(m.Permissions.Network, []string{"hacker-news.firebaseio.com"}) || !strings.Contains(siteBase, "//hacker-news.firebaseio.com/") {
		t.Fatalf("network %v, siteBase %s", m.Permissions.Network, siteBase)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/hackernews-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	var keys []string
	ct := reflect.TypeOf(Config{})
	for i := range ct.NumField() {
		name, _, _ := strings.Cut(ct.Field(i).Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if got := slices.Sorted(maps.Keys(m.Configuration.Properties)); !slices.Equal(got, keys) {
		t.Fatalf("configuration %v, Config %v", got, keys)
	}
	list := m.Configuration.Properties["list"]
	var choices, ids []string
	for _, c := range list.OneOf {
		choices = append(choices, c.Const)
	}
	for _, l := range lists {
		ids = append(ids, l.id)
	}
	if !slices.Equal(choices, ids) || list.Default != "top" {
		t.Fatalf("list %v default %v, lists %v", choices, list.Default, ids)
	}
}

func TestEveryLocaleHasEveryText(t *testing.T) {
	if len(locales) != 3 {
		t.Fatalf("locales %v", slices.Sorted(maps.Keys(locales)))
	}
	for name, tx := range locales {
		v := reflect.ValueOf(*tx)
		for i := range v.NumField() {
			f, field := v.Field(i), v.Type().Field(i).Name
			switch f.Kind() {
			case reflect.String:
				if f.String() == "" {
					t.Errorf("%s: %s is empty", name, field)
				}
				// the same verbs as English, so a format never shows %!d
				if want := strings.Count(reflect.ValueOf(*locales["en"]).Field(i).String(), "%"); strings.Count(f.String(), "%") != want {
					t.Errorf("%s: %s has other verbs than en: %q", name, field, f.String())
				}
			case reflect.Func:
				if f.IsNil() {
					t.Errorf("%s: %s is nil", name, field)
				}
			}
		}
		for _, l := range lists {
			if tx.lists[l.id] == "" {
				t.Errorf("%s: no title for list %s", name, l.id)
			}
		}
	}
	for in, want := range map[string]string{"": "zh-Hans", "zh-Hans": "zh-Hans", "en": "en", "en-US": "en", "ja": "ja", "ja-JP": "ja", "fr": "zh-Hans"} {
		if textsFor(in) != locales[want] {
			t.Errorf("textsFor(%q) is not %s", in, want)
		}
	}
	if got := locales["en"].ago(3 * time.Hour); got != "3h ago" {
		t.Errorf("ago %q", got)
	}
}

func TestPlain(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "",
		"a<p>b":                            "a\n\nb",
		"x &amp; y &#x27;z&#x27; <i>w</i>": "x & y 'z' w",
		`see <a href="https://e.com/full">https://e.com/...</a>`: "see https://e.com/full",
		"<pre><code>  a\n  b\n</code></pre>":                     "a\n  b",
		"<p><p><p>q":                                             "q",
	} {
		if got := plain(in); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListPage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, Config{})
	v, _ := a.render(context.Background(), req("en", 3, nil, "", nil))
	checkView(t, v)
	all := joined(v)
	for _, want := range []string{"Top", "Show HN", "Top · 3", "Rust in the kernel", "250 points · 3 comments · example.com · alice · 2h ago", "Ask HN: What are you reading?", "An old site", "Refresh"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	if v.Sections[0].Items[0].Type != wire.ComponentSegmented {
		t.Fatalf("UI 3 gets a segmented control: %+v", v.Sections[0])
	}

	// Chinese by default, rows before UI version 2, the open list marked.
	v, _ = a.render(context.Background(), req("", 1, pageState{List: "ask"}, "", nil))
	checkView(t, v)
	all = joined(v)
	for _, want := range []string{"看什么", "正在看", "Ask HN · 1 条", "40 分 · 0 评论 · dave · 2 天前", "刷新"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	// Japanese.
	v, _ = a.render(context.Background(), req("ja-JP", 3, nil, "", nil))
	if all = joined(v); !strings.Contains(all, "トップ · 3 件") || !strings.Contains(all, "250 ポイント") {
		t.Errorf("ja:\n%s", all)
	}
}

func TestListPageFilter(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, Config{ExcludeKeywords: []string{"ask hn"}, MinScore: 10})
	v, _ := a.render(context.Background(), req("en", 3, nil, "", nil))
	all := joined(v)
	if !strings.Contains(all, "Top · 1 of 3") || strings.Contains(all, "An old site") || strings.Contains(all, "What are you reading") ||
		!strings.Contains(all, "blocked ask hn · at least 10 points") || !strings.Contains(all, "Show all 3") {
		t.Fatalf("filtered:\n%s", all)
	}
	res, _ := a.action(context.Background(), req("en", 3, pageState{List: "top"}, "all", nil))
	if all = joined(res.View); !strings.Contains(all, "Top · all 3") || !strings.Contains(all, "An old site") || !strings.Contains(all, "Filter by my interests") {
		t.Fatalf("all:\n%s", all)
	}
	// keywords match the domain too
	a.cfg = Config{Keywords: []string{"EXAMPLE.NET"}, List: "top"}
	v, _ = a.render(context.Background(), req("en", 3, nil, "", nil))
	if all = joined(v); !strings.Contains(all, "Top · 1 of 3") || !strings.Contains(all, "An old site") {
		t.Fatalf("domain keyword:\n%s", all)
	}
}

func TestListSwitchAndCache(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, Config{List: "ask"})
	res, _ := a.action(context.Background(), req("en", 3, nil, "list", map[string]string{"list": "top"}))
	var st pageState
	_ = json.Unmarshal([]byte(res.View.State), &st)
	if st.List != "top" || !strings.Contains(joined(res.View), "Top · 3") {
		t.Fatalf("switch: %+v\n%s", st, joined(res.View))
	}
	_, _ = a.render(context.Background(), req("en", 3, pageState{List: "top"}, "", nil))
	if n := f.hit("/topstories.json"); n != 1 {
		t.Fatalf("a second draw read the list again: %d", n)
	}
	if n := f.hit("/item/1.json"); n != 1 {
		t.Fatalf("a second draw read an item again: %d", n)
	}
	// an unknown list in the state falls back to the setting
	v, _ := a.render(context.Background(), req("en", 3, pageState{List: "jobs"}, "", nil))
	if !strings.Contains(joined(v), "Ask HN · 1") {
		t.Fatalf("fallback:\n%s", joined(v))
	}
}

func TestStaleCopy(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, Config{})
	if _, err := a.stories(context.Background(), "top", listMost, false); err != nil {
		t.Fatal(err)
	}
	f.set("/topstories.json", "") // the site fails from here on
	old := a.site.cache[listPath("topstories")]
	old.at = old.at.Add(-time.Hour)
	a.site.cache[listPath("topstories")] = old
	v, _ := a.render(context.Background(), req("en", 3, nil, "", nil))
	all := joined(v)
	if !strings.Contains(all, "This read failed: Hacker News answered HTTP 500. Showing what was read 1h ago") || !strings.Contains(all, "Rust in the kernel") {
		t.Fatalf("stale:\n%s", all)
	}
	if st := a.status(); st.State != "error" || !strings.Contains(st.Error, "HTTP 500") {
		t.Fatalf("status %+v", st)
	}
	// nothing cached: the page says so and offers a retry
	f.set("/newstories.json", "<html>")
	v, _ = a.render(context.Background(), req("en", 3, pageState{List: "new"}, "", nil))
	if all = joined(v); !strings.Contains(all, "Could not read: Hacker News sent an answer that could not be read") || !strings.Contains(all, "Retry") {
		t.Fatalf("failed:\n%s", all)
	}
	f.set("/topstories.json", "[1]")
	if _, err := a.stories(context.Background(), "top", listMost, true); err != nil {
		t.Fatal(err)
	}
	if st := a.status(); st.State != "ok" || st.LastSync == nil {
		t.Fatalf("status after a good read %+v", st)
	}
}

func TestStoryPage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, Config{})
	res, _ := a.action(context.Background(), req("en", 2, pageState{List: "top"}, "story:1", nil))
	v := res.View
	checkView(t, v)
	all := joined(v)
	for _, want := range []string{"Rust in the kernel", "Comments", "bob · 10m ago", "Great news.\n\nSee https://example.org/a/very/long/link & more",
		"carol · 1m ago", "fn main() {}", "First 2 top-level comments of 3", "Open article", "https://www.example.com/rust", "https://news.ycombinator.com/item?id=1", "Back to list"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	res, _ = a.action(context.Background(), req("en", 2, pageState{List: "top", Story: 1}, "back", nil))
	if !strings.Contains(joined(res.View), "Top · 3") {
		t.Fatalf("back:\n%s", joined(res.View))
	}

	// UI 3: no back button; a plain-http article is not a link; no comments
	v, _ = a.render(context.Background(), req("en", 3, pageState{List: "top", Story: 3}, "", nil))
	checkView(t, v)
	if all = joined(v); strings.Contains(all, "Back to list") || strings.Contains(all, "Open article") || !strings.Contains(all, "No comments yet") || !strings.Contains(all, "plain.example.net") {
		t.Fatalf("story 3:\n%s", all)
	}
	// Ask HN text, in Chinese
	v, _ = a.render(context.Background(), req("zh-Hans", 3, pageState{List: "ask", Story: 2}, "", nil))
	if all = joined(v); !strings.Contains(all, "Books / papers?\n\nAnything goes.") || !strings.Contains(all, "在 Hacker News 打开") {
		t.Fatalf("story 2:\n%s", all)
	}
	// gone and missing
	v, _ = a.render(context.Background(), req("en", 3, pageState{Story: 4}, "", nil))
	if !strings.Contains(joined(v), "deleted or hidden") {
		t.Fatalf("story 4:\n%s", joined(v))
	}
	v, _ = a.render(context.Background(), req("en", 3, pageState{Story: 99}, "", nil))
	checkView(t, v)
	if !strings.Contains(joined(v), "Could not read: Hacker News has no such item") {
		t.Fatalf("story 99:\n%s", joined(v))
	}
}

func TestLongStoryStaysInBounds(t *testing.T) {
	f := newFakeSite(t)
	long := strings.Repeat("word ", 5000)
	kids := []int{}
	for i := range 40 {
		kids = append(kids, 100+i)
		f.item(map[string]any{"id": 100 + i, "type": "comment", "by": "u", "time": testNow, "text": long})
	}
	f.item(map[string]any{"id": 5, "type": "story", "title": strings.Repeat("T", 500), "text": long, "kids": kids, "descendants": 40})
	a := testApp(t, f, Config{})
	v, _ := a.render(context.Background(), req("en", 3, pageState{Story: 5}, "", nil))
	checkView(t, v)
	if n := f.hit("/item/139.json"); n != 0 {
		t.Fatalf("read past what is shown")
	}
	if !strings.Contains(joined(v), "First 15 top-level comments of 40") {
		t.Fatalf("long:\n%s", joined(v))
	}
}

func TestTools(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, Config{MinScore: 10})
	call := func(name, args string) (string, error) {
		res, err := a.connector(context.Background(), "tools.call", json.RawMessage(fmt.Sprintf(`{"contribution":"hackernews","name":%q,"args":%s}`, name, args)))
		if err != nil {
			return "", err
		}
		return res.(map[string]any)["text"].(string), nil
	}
	out, err := call("hn_stories", `{}`)
	if err != nil || !strings.Contains(out, "Hacker News Top: 3 stories, 2 after filtering by at least 10 points") ||
		!strings.Contains(out, "1. Rust in the kernel (id 1)") || strings.Contains(out, "An old site") || !strings.Contains(out, "https://www.example.com/rust") {
		t.Fatalf("hn_stories: %v\n%s", err, out)
	}
	out, err = call("hn_stories", `{"filtered": false, "keywords": ["old"], "limit": 1}`)
	if err != nil || !strings.Contains(out, "3 stories, 1 after filtering by keywords old") || !strings.Contains(out, "An old site") {
		t.Fatalf("hn_stories unfiltered: %v\n%s", err, out)
	}
	if _, err := call("hn_stories", `{"list": "jobs"}`); err == nil {
		t.Fatal("an unknown list")
	}
	for _, arg := range []string{`1`, `"1"`, `"https://news.ycombinator.com/item?id=1"`, `"news.ycombinator.com/item?id=1#13"`} {
		out, err = call("hn_item", `{"item": `+arg+`, "comments": 1}`)
		if err != nil || !strings.Contains(out, `"Rust in the kernel" https://news.ycombinator.com/item?id=1`) || !strings.Contains(out, "Article: https://www.example.com/rust") ||
			!strings.Contains(out, "Top 1 top-level comments (3 comments in all):\n- bob (10m ago, id 11): Great news. See https://example.org/a/very/long/link & more") {
			t.Fatalf("hn_item %s: %v\n%s", arg, err, out)
		}
	}
	for _, arg := range []string{`"https://example.com/item?id=1"`, `"abc"`, `-3`, `{}`} {
		if _, err := call("hn_item", `{"item": `+arg+`}`); err == nil {
			t.Fatalf("hn_item %s answered", arg)
		}
	}
	if _, err := call("hn_item", `{"item": 4}`); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("a dead story: %v", err)
	}
	if _, err := call("nope", `{}`); err == nil {
		t.Fatal("an unknown tool")
	}
}

func TestConnector(t *testing.T) {
	f := newFakeSite(t)
	a := testApp(t, f, Config{})
	res, err := a.connector(context.Background(), "tools.list", nil)
	if err != nil || len(res.(map[string]any)["tools"].([]Tool)) != 2 {
		t.Fatalf("tools.list %v %v", res, err)
	}
	if st := a.status(); st.State != "ok" || st.LastSync != nil {
		t.Fatalf("status %+v", st)
	}
	var e *ext.Error
	if _, err := a.connector(context.Background(), "status", json.RawMessage(`{"contribution":"other"}`)); !errors.As(err, &e) {
		t.Fatalf("another contribution: %v", err)
	}
	if _, err := a.connector(context.Background(), "sync", nil); !errors.As(err, &e) || e.Code != wire.ErrorCodeUnknownMethod {
		t.Fatalf("unknown method: %v", err)
	}
}

func TestInit(t *testing.T) {
	a := newApp()
	in := &ext.Init{}
	in.InitializeParams.Config = json.RawMessage(`{"list": "jobs", "min_score": -5, "keywords": ["go"]}`)
	if _, err := a.init(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	if a.cfg.List != "top" || a.cfg.MinScore != 0 || !slices.Equal(a.cfg.Keywords, []string{"go"}) {
		t.Fatalf("cfg %+v", a.cfg)
	}
}

func TestCacheBound(t *testing.T) {
	s := newSite("")
	defer func(n int) { cacheEntries = n }(cacheEntries)
	cacheEntries = 3
	now := time.Now()
	for i := range 5 {
		s.store(fmt.Sprint(i), []byte("x"), now.Add(time.Duration(i)*time.Second))
	}
	if got := slices.Sorted(maps.Keys(s.cache)); !slices.Equal(got, []string{"2", "3", "4"}) {
		t.Fatalf("cache %v", got)
	}
}
