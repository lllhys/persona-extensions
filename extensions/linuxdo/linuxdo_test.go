package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/pluginkit"
)

func TestMain(m *testing.M) {
	requestGap = 0
	os.Exit(m.Run())
}

// fakeSite is a small LINUX DO: the four RSS lists and topics, counting requests by path.
type fakeSite struct {
	srv *httptest.Server

	mu     sync.Mutex
	feeds  map[string]string // path with query → RSS
	topics map[int64]string  // id → JSON
	status int               // answer everything with this when set
	body   string
	hits   map[string]int
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{feeds: map[string]string{}, topics: map[int64]string{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		f.hits[key]++
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.body))
			return
		}
		if rss, ok := f.feeds[key]; ok {
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(rss))
			return
		}
		var id int64
		if n, _ := fmt.Sscanf(r.URL.Path, "/t/topic/%d.json", &id); n == 1 {
			if js, ok := f.topics[id]; ok {
				_, _ = w.Write([]byte(js))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":["not found"]}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) hit(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

type item struct {
	id                 int64
	title, author, cat string
	excerpt            string
	posts, people      int
	ago                time.Duration
	pinned             bool
}

func rss(items ...item) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?><rss version="2.0" xmlns:discourse="http://www.discourse.org/" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>LINUX DO</title>`)
	for _, it := range items {
		pinned := "No"
		if it.pinned {
			pinned = "Yes"
		}
		fmt.Fprintf(&b, `<item><discourse:topicPinned>%s</discourse:topicPinned>`, pinned)
		fmt.Fprintf(&b, `<title>%s</title><dc:creator><![CDATA[%s]]></dc:creator><category>%s</category>`+
			`<description><![CDATA[<p>%s</p><p><a href="https://linux.do/t/topic/%d">%d 个帖子 - %d 位参与者</a></p> <p><a href="https://linux.do/t/topic/%d">阅读完整话题</a></p>]]></description>`+
			`<link>https://linux.do/t/topic/%d</link><pubDate>%s</pubDate><guid isPermaLink="false">linux.do-topic-%d</guid></item>`,
			xmlEscape(it.title), it.author, it.cat, it.excerpt, it.id, it.posts, it.people, it.id, it.id,
			time.Now().Add(-it.ago).UTC().Format(time.RFC1123Z), it.id)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func topicJSON(id int64, title string, opening string, replies ...string) string {
	posts := []map[string]any{{"id": id * 100, "username": "op", "created_at": "2026-11-03T09:37:02Z", "cooked": opening, "post_number": 1,
		"post_type": 1, "score": 500, "actions_summary": []any{map[string]any{"id": 2, "count": 89}}}}
	for i, r := range replies {
		posts = append(posts, map[string]any{"id": id*100 + int64(i) + 1, "username": fmt.Sprintf("u%d", i+2), "created_at": "2026-11-03T10:00:00Z",
			"cooked": r, "post_number": i + 2, "post_type": 1, "score": float64(10 * (i + 1)), "reaction_users_count": i})
	}
	b, _ := json.Marshal(map[string]any{"id": id, "title": title, "posts_count": len(posts), "like_count": 110, "views": 1098,
		"participant_count": 13, "created_at": "2026-11-03T09:37:02Z", "tags": []any{map[string]any{"id": 1, "name": "人工智能"}},
		"post_stream": map[string]any{"posts": posts, "stream": []int64{id * 100}}})
	return string(b)
}

func testPlugin(t *testing.T, f *fakeSite, cfg map[string]any) *plugin {
	p := newPlugin(t.Logf)
	p.site = newSite(f.srv.URL)
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["tz"]; !ok {
		cfg["tz"] = "Asia/Shanghai"
	}
	raw, _ := json.Marshal(cfg)
	if err := p.configure(pluginkit.InitParams{Protocol: 1, Config: raw}); err != nil {
		t.Fatal(err)
	}
	return p
}

// checkView is Persona's plugin.CheckView (server/internal/plugin/ui.go), so a page the hub would refuse fails here.
func checkView(t *testing.T, v pluginkit.View) {
	t.Helper()
	long := func(s string) bool { return utf8.RuneCountInString(s) > 2000 }
	if len(v.State) > 16<<10 || long(v.Title) {
		t.Fatalf("state or title too long")
	}
	n := 0
	for i, s := range v.Sections {
		if long(s.Title) || long(s.Footer) {
			t.Fatalf("section %d: title or footer too long", i)
		}
		for j, e := range s.Items {
			n++
			at := fmt.Sprintf("section %d item %d (%s)", i, j, e.Type)
			for _, str := range []string{e.Text, e.Label, e.Value, e.Detail, e.Title, e.Subtitle, e.Placeholder} {
				if long(str) {
					t.Fatalf("%s: a string longer than 2000", at)
				}
			}
			if !slices.Contains([]string{"", "neutral", "positive", "caution", "danger"}, e.Tone) ||
				e.Badge != nil && !slices.Contains([]string{"", "neutral", "positive", "caution", "danger"}, e.Badge.Tone) {
				t.Fatalf("%s: tone", at)
			}
			if e.Action != nil && (e.Action.ID == "" || len(e.Action.ID) > 128) {
				t.Fatalf("%s: action id", at)
			}
			switch e.Type {
			case "text":
				if e.Text == "" || !slices.Contains([]string{"", "body", "secondary", "caption", "code"}, e.Style) {
					t.Fatalf("%s: text %q style %q", at, e.Text, e.Style)
				}
			case "row":
				if e.Title == "" {
					t.Fatalf("%s: row needs a title", at)
				}
			case "button":
				if e.Title == "" || e.Action == nil {
					t.Fatalf("%s: button needs a title and an action", at)
				}
			case "link":
				if u, err := url.Parse(e.URL); e.Title == "" || err != nil || u.Scheme != "https" || u.Host == "" {
					t.Fatalf("%s: link %q", at, e.URL)
				}
			case "segmented": // UI version 2
				ok := e.Name != "" && e.Action != nil && len(e.Choices) >= 2 && len(e.Choices) <= 5 &&
					slices.ContainsFunc(e.Choices, func(c pluginkit.Choice) bool { return c.Value == e.Value }) &&
					!slices.ContainsFunc(e.Choices, func(c pluginkit.Choice) bool { return utf8.RuneCountInString(cmp.Or(c.Title, c.Value)) > 12 })
				if !ok {
					t.Fatalf("%s: segmented %+v", at, e)
				}
			case "divider":
			default:
				t.Fatalf("%s: a type the page does not use", at)
			}
		}
	}
	if n > 300 {
		t.Fatalf("%d items", n)
	}
}

func texts(v pluginkit.View) string {
	var b strings.Builder
	b.WriteString(v.Title + "\n")
	for _, s := range v.Sections {
		b.WriteString("[" + s.Title + "|" + s.Footer + "]\n")
		for _, e := range s.Items {
			parts := []string{e.Type + ":"}
			for _, x := range []string{e.Title + e.Text, e.Subtitle, e.URL} {
				if x != "" {
					parts = append(parts, x)
				}
			}
			if e.Badge != nil {
				parts = append(parts, "<"+e.Badge.Text+">")
			}
			if e.Action != nil {
				parts = append(parts, "→"+e.Action.ID)
			}
			b.WriteString(strings.Join(parts, " ") + "\n")
		}
	}
	return b.String()
}

func sample(f *fakeSite) {
	f.feeds["/hot.rss"] = rss(
		item{id: 1000, title: "请不要把戾气带来这里", author: "neo", cat: "运营反馈", posts: 7818, people: 6052, ago: 600 * 24 * time.Hour, pinned: true},
		item{id: 1001, title: "Claude 新模型实测 & 对比", author: "alice", cat: "开发调优", excerpt: "跑了一晚上的基准", posts: 75, people: 59, ago: 3 * time.Hour},
		item{id: 1002, title: "[NSFW] 某客户端", author: "bob", cat: "搞七捻三", excerpt: "不多说", posts: 40, people: 30, ago: 2 * time.Hour},
		item{id: 1003, title: "速报：某大选", author: "carol", cat: "前沿快讯", excerpt: "感觉", posts: 20, people: 18, ago: time.Hour},
		item{id: 1004, title: "白嫖一个月 GPU", author: "dan", cat: "福利羊毛", excerpt: "AI 训练用", posts: 12, people: 10, ago: 30 * time.Minute})
	f.feeds["/top.rss?period=daily"] = rss(item{id: 1003, title: "速报：某大选", author: "carol", cat: "前沿快讯", posts: 20, people: 18, ago: time.Hour})
	f.topics[1001] = topicJSON(1001, "Claude 新模型实测 & 对比", `<h2>结论</h2><p>快了 <strong>30%</strong>。<img src="https://cdn.example/x.png" class="emoji" alt=":smile:"></p>`+
		`<div class="lightbox-wrapper"><a class="lightbox" href="https://cdn.example/big.png"><img src="https://cdn.example/s.png"><div class="meta">image 1920×1080</div></a></div>`+
		`<ul><li>一</li><li>二</li></ul><pre><code class="lang-go">fmt.Println("a &lt; b")
x := 1</code></pre><aside class="onebox allowlistedgeneric" data-onebox-src="https://example.com/post"><header>example.com</header><article>预览</article></aside>`,
		`<p>第二楼</p>`, `<aside class="quote no-group" data-username="op"><div class="title"><img class="avatar" src="a.png"> op:</div><blockquote><p>快了 30%</p></blockquote></aside><p>真的吗</p>`, `<p>最热的一楼</p>`)
}

func TestParseFeed(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	ts, err := parseFeed([]byte(f.feeds["/hot.rss"]))
	if err != nil || len(ts) != 5 || !ts[0].Pinned || ts[1].Pinned {
		t.Fatalf("%v %v", ts, err)
	}
	a := ts[1]
	if a.ID != 1001 || a.Title != "Claude 新模型实测 & 对比" || a.Author != "alice" || a.Category != "开发调优" || a.Excerpt != "跑了一晚上的基准" ||
		a.Posts != 75 || a.People != 59 || a.URL != "https://linux.do/t/topic/1001" || time.Since(a.At) < 2*time.Hour {
		t.Fatalf("%+v", a)
	}
	if m := a.meta(time.Now()); m != "开发调优 · alice · 75 帖 · 59 人 · 3 小时前" && m != "开发调优 · alice · 75 帖 · 59 人 · 2 小时前" {
		t.Fatal(m)
	}
	if _, err := parseFeed([]byte("<html>Just a moment</html>")); err == nil {
		t.Fatal("a page that is not RSS read as one")
	}
}

func TestFilter(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	ts, _ := parseFeed([]byte(f.feeds["/hot.rss"]))
	ids := func(fl filter) []int64 {
		var out []int64
		for _, x := range fl.apply(ts) {
			out = append(out, x.ID)
		}
		return out
	}
	cases := []struct {
		f    filter
		want []int64
	}{
		{filter{}, []int64{1000, 1001, 1002, 1003, 1004}},
		{filter{HideNSFW: true}, []int64{1000, 1001, 1003, 1004}},
		{filter{HidePinned: true}, []int64{1001, 1002, 1003, 1004}},
		{filter{HideNSFW: true, Categories: []string{"开发调优", " 福利羊毛 "}}, []int64{1001, 1004}},
		{filter{HideNSFW: true, ExcludeCategories: []string{"前沿快讯"}}, []int64{1000, 1001, 1004}},
		{filter{HideNSFW: true, Keywords: []string{"claude", "ai"}}, []int64{1001, 1004}},
		{filter{HideNSFW: true, ExcludeKeywords: []string{"GPU"}}, []int64{1000, 1001, 1003}},
		{filter{Categories: []string{""}, Keywords: []string{" "}}, []int64{1000, 1001, 1002, 1003, 1004}},
	}
	for i, c := range cases {
		if got := ids(c.f); !slices.Equal(got, c.want) {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
	}
	if d := (filter{Categories: []string{"开发调优"}, HideNSFW: true}).describe(); d != "只看 开发调优 · 隐藏 NSFW" {
		t.Fatal(d)
	}
}

func TestPostText(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	var td topicDoc
	_ = json.Unmarshal([]byte(f.topics[1001]), &td)
	op, ok := td.opening()
	if !ok {
		t.Fatal("no first post")
	}
	sg := postText(op.Cooked)
	if len(sg) != 3 || sg[1].Code != true || sg[1].Text != "fmt.Println(\"a < b\")\nx := 1" {
		t.Fatalf("%+v", sg)
	}
	for _, want := range []string{"结论", "快了 30%。", "[图片]", "• 一\n• 二"} {
		if !strings.Contains(sg[0].Text, want) {
			t.Errorf("no %q in %q", want, sg[0].Text)
		}
	}
	if strings.Contains(sg[0].Text, "smile") || strings.Contains(sg[0].Text, "1920") {
		t.Errorf("an emoji or a picture's caption in %q", sg[0].Text)
	}
	if sg[2].Text != "[链接] https://example.com/post" {
		t.Errorf("onebox: %q", sg[2].Text)
	}
	if got := plainText(td.PostStream.Posts[2].Cooked); got != "op:\n> 快了 30%\n\n真的吗" && !strings.Contains(got, "> 快了 30%") {
		t.Errorf("quote: %q", got)
	}
	if strings.Contains(plainText(td.PostStream.Posts[2].Cooked), "[图片]") {
		t.Error("an avatar became a picture")
	}
	rs := td.bestReplies(2)
	if len(rs) != 2 || rs[0].PostNumber != 4 || rs[1].PostNumber != 3 {
		t.Fatalf("best replies: %+v", rs)
	}
	if op.likes() != 89 || td.tags()[0] != "人工智能" {
		t.Fatal("likes or tags")
	}
}

func render(t *testing.T, p *plugin, state string) pluginkit.View {
	t.Helper()
	res, err := p.uiRender(context.Background(), nil, json.RawMessage(fmt.Sprintf(`{"contribution":"hot","state":%q}`, state)))
	if err != nil {
		t.Fatal(err)
	}
	v := res.(pluginkit.View)
	checkView(t, v)
	return v
}

func act(t *testing.T, p *plugin, state, action string) (pluginkit.View, string) {
	t.Helper()
	res, err := p.uiAction(context.Background(), nil, json.RawMessage(fmt.Sprintf(`{"contribution":"hot","state":%q,"action":%q}`, state, action)))
	if err != nil {
		t.Fatal(err)
	}
	r := res.(pluginkit.ActionResult)
	if r.View == nil {
		t.Fatal("no view")
	}
	checkView(t, *r.View)
	return *r.View, r.Toast
}

func TestPage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := testPlugin(t, f, map[string]any{"exclude_categories": []string{"前沿快讯"}})

	v := render(t, p, "")
	page := texts(v)
	for _, want := range []string{"row: 热门 <正在看> →list:hot", "row: 今日最热 →list:daily", "[热门 · 2 / 5 条|按你的兴趣：不看 前沿快讯 · 隐藏 NSFW · 隐藏置顶。",
		"row: Claude 新模型实测 & 对比 开发调优 · alice · 75 帖 · 59 人", "→topic:1001", "→topic:1004", "row: 看全部 5 条 →all", "button: 刷新 →refresh"} {
		if !strings.Contains(page, want) {
			t.Errorf("the list has no %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "topic:1000") || strings.Contains(page, "topic:1002") || strings.Contains(page, "topic:1003") {
		t.Errorf("filtered out topics are shown:\n%s", page)
	}

	all, _ := act(t, p, v.State, "all")
	if page := texts(all); !strings.Contains(page, "热门 · 全部 5 条") || !strings.Contains(page, "→topic:1002") || !strings.Contains(page, "row: 按我的兴趣过滤") {
		t.Errorf("all:\n%s", page)
	}

	daily, _ := act(t, p, v.State, "list:daily")
	if page := texts(daily); !strings.Contains(page, "今日最热 · 0 / 1 条") || !strings.Contains(page, "没有符合你兴趣的帖子") || !strings.Contains(page, "<正在看>") {
		t.Errorf("daily:\n%s", page)
	}

	topic, _ := act(t, p, v.State, "topic:1001")
	page = texts(topic)
	for _, want := range []string{"Claude 新模型实测 & 对比\n", "op · 11-03 17:37 · 1098 浏览 · 110 赞 · 4 帖 · 13 人 · 标签 人工智能",
		"[热门回复|前 4 楼里最热的 3 条（共 4 帖）]", "#4 u4 · 11-03 18:00 · 2 赞", "最热的一楼", "link: 在网页打开 https://linux.do/t/topic/1001", "button: 返回列表 →back"} {
		if !strings.Contains(page, want) {
			t.Errorf("the topic has no %q:\n%s", want, page)
		}
	}
	var code bool
	for _, e := range topic.Sections[0].Items {
		code = code || e.Style == "code"
	}
	if !code {
		t.Error("the code block is not drawn as code")
	}
	back, _ := act(t, p, topic.State, "back")
	if !strings.Contains(texts(back), "→topic:1001") {
		t.Error("back is not the list")
	}

	if f.hit("/hot.rss") != 1 || f.hit("/t/topic/1001.json") != 1 {
		t.Fatalf("read again from the cache's time: %v", f.hits)
	}
	if _, toast := act(t, p, v.State, "refresh"); toast != "已刷新" || f.hit("/hot.rss") != 2 {
		t.Fatalf("refresh: %q %v", toast, f.hits)
	}

	gone, _ := act(t, p, v.State, "topic:9999")
	if page := texts(gone); !strings.Contains(page, errNoTopic.Error()) || !strings.Contains(page, "→back") {
		t.Errorf("a missing topic:\n%s", page)
	}
}

func TestLongTopicFitsThePage(t *testing.T) {
	f := newFakeSite(t)
	long := strings.Repeat("<p>"+strings.Repeat("长", 700)+"</p>", 40)
	var replies []string
	for i := 0; i < 19; i++ {
		replies = append(replies, "<p>"+strings.Repeat("回", 3000)+"</p>")
	}
	f.topics[7] = topicJSON(7, strings.Repeat("标题", 200), long, replies...)
	p := testPlugin(t, f, nil)
	v, _ := act(t, p, "", "topic:7")
	total := 0
	for _, e := range v.Sections[0].Items {
		if e.Style == "body" {
			total += utf8.RuneCountInString(e.Text)
		}
	}
	if total > openingMost+50 || !strings.Contains(texts(v), "正文太长，后面的在网页里看") {
		t.Fatalf("%d characters of the first post", total)
	}
}

// Every <pre> starts a new segment, so a short post of many code blocks is many items; the view still fits in 300.
func TestManyCodeBlocksFitThePage(t *testing.T) {
	f := newFakeSite(t)
	var replies []string
	for i := 0; i < 19; i++ {
		replies = append(replies, "<p>回复</p>")
	}
	f.topics[8] = topicJSON(8, "命令合集", strings.Repeat("<p>步骤</p><pre><code>ls</code></pre>", 200), replies...)
	p := testPlugin(t, f, nil)
	v, _ := act(t, p, "", "topic:8") // checkView: at most 300 items
	if !strings.Contains(texts(v), "正文太长，后面的在网页里看") || !strings.Contains(texts(v), "热门回复") {
		t.Fatalf("no caption or no replies:\n%s", texts(v))
	}
}

func TestSiteFailures(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := testPlugin(t, f, nil)

	f.status, f.body = http.StatusForbidden, `<html><head><title>Just a moment...</title></head></html>`
	if _, err := p.hot(context.Background(), "hot", false); !errors.Is(err, errChallenged) {
		t.Fatalf("challenge: %v", err)
	}
	st, _ := p.cStatus(context.Background(), nil, nil)
	if s := st.(connectorStatus); s.State != "error" || len(s.Guide) == 0 {
		t.Fatalf("status: %+v", s)
	}

	f.status = 0
	if _, err := p.hot(context.Background(), "hot", false); err != nil {
		t.Fatal(err)
	}
	if s, _ := p.cStatus(context.Background(), nil, nil); s.(connectorStatus).State != "ok" || s.(connectorStatus).LastSync == nil {
		t.Fatalf("status after a good read: %+v", s)
	}

	// A plain 403/404 on a list feed is an ordinary fetch failure, not a deleted topic: the generic
	// HTTP error reaches the caller, the connector goes to "error", and a cached copy still stands in.
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		f.status, f.body = code, "blocked"
		want := fmt.Sprintf("HTTP %d", code)
		if _, err := p.site.fetch(context.Background(), "/hot.rss"); err == nil || errors.Is(err, errNoTopic) || !strings.Contains(err.Error(), want) {
			t.Fatalf("a plain %d on /hot.rss read as a missing topic: %v", code, err)
		}
		if _, err := p.hot(context.Background(), "latest", false); err == nil || errors.Is(err, errNoTopic) || !strings.Contains(err.Error(), want) {
			t.Fatalf("an uncached list under %d: %v", code, err)
		}
		if s, _ := p.cStatus(context.Background(), nil, nil); s.(connectorStatus).State != "error" {
			t.Fatalf("status after %d: %+v", code, s)
		}
		if ts, err := p.hot(context.Background(), "hot", true); err != nil || len(ts) != 5 {
			t.Fatalf("the cached list should stand in for %d: %v (%d topics)", code, err, len(ts))
		}
	}

	// The same statuses on a topic path still read as "this topic is gone".
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		f.status = code
		if _, err := p.site.fetch(context.Background(), "/t/topic/9999.json"); !errors.Is(err, errNoTopic) {
			t.Fatalf("a topic under %d: %v", code, err)
		}
	}

	// A 429 pauses every read, and an older copy stands in meanwhile.
	f.status, f.body = http.StatusTooManyRequests, "slow down"
	if _, err := p.hot(context.Background(), "hot", true); err != nil {
		t.Fatalf("the cached list should stand in: %v", err)
	}
	before := f.hit("/top.rss?period=daily")
	if _, err := p.hot(context.Background(), "daily", false); !errors.Is(err, errRateLimited) || f.hit("/top.rss?period=daily") != before {
		t.Fatalf("paused: %v, %d requests", err, f.hit("/top.rss?period=daily")-before)
	}
	if s, _ := p.cStatus(context.Background(), nil, nil); s.(connectorStatus).State != "rate_limited" {
		t.Fatalf("status: %+v", s)
	}
}

// A good answer that only mentions the challenge page is still a good answer.
func TestJustAMomentInContent(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	f.topics[2001] = topicJSON(2001, "Help: page stuck on Just a moment", `<p>Cloudflare keeps showing "Just a moment..." on every site, any idea?</p>`)
	f.feeds["/hot.rss"] = rss(item{id: 3001, title: "Just a moment 验证页一直转圈怎么办", author: "a", cat: "开发调优", posts: 3, people: 2})
	p := testPlugin(t, f, nil)

	if _, err := p.topic(context.Background(), 2001); err != nil {
		t.Errorf("topic: %v", err)
	}
	if ts, err := p.hot(context.Background(), "hot", false); err != nil || len(ts) != 1 {
		t.Errorf("hot: %d topics, %v", len(ts), err)
	}
	if s, _ := p.cStatus(context.Background(), nil, nil); s.(connectorStatus).State != "ok" {
		t.Fatalf("status: %+v", s)
	}
}

func TestTools(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := testPlugin(t, f, map[string]any{"categories": []string{"开发调优", "福利羊毛"}})
	call := func(args string) string {
		t.Helper()
		res, err := p.cToolsCall(context.Background(), nil, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return res.(map[string]any)["text"].(string)
	}
	text := call(`{"name":"linuxdo_hot","args":{}}`)
	for _, want := range []string{"LINUX DO 热门：5 条，按「只看 开发调优、福利羊毛 · 隐藏 NSFW · 隐藏置顶」过滤后 2 条", "1. Claude 新模型实测 & 对比（id 1001）", "https://linux.do/t/topic/1004", "跑了一晚上的基准"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q:\n%s", want, text)
		}
	}
	if text = call(`{"name":"linuxdo_hot","args":{"filtered":false,"limit":1}}`); !strings.Contains(text, "这里列前 1 条") || strings.Contains(text, "2. ") {
		t.Errorf("unfiltered, one:\n%s", text)
	}
	text = call(`{"name":"linuxdo_topic","args":{"topic":"https://linux.do/t/topic/1001/3","replies":1}}`)
	for _, want := range []string{"「Claude 新模型实测 & 对比」 https://linux.do/t/topic/1001", "fmt.Println(\"a < b\")", "#4 u4（11-03 18:00，2 赞）：最热的一楼"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "#3 u3") {
		t.Error("more replies than asked")
	}
	if _, err := p.cToolsCall(context.Background(), nil, json.RawMessage(`{"name":"linuxdo_hot","args":{"list":"monthly"}}`)); err == nil {
		t.Error("a list it has not")
	}
}

func TestTopicID(t *testing.T) {
	for raw, want := range map[string]int64{`123`: 123, `"456"`: 456, `"https://linux.do/t/topic/789"`: 789, `"https://linux.do/t/some-slug/42/7"`: 42} {
		if got, err := topicID(json.RawMessage(raw)); err != nil || got != want {
			t.Errorf("%s: %d %v", raw, got, err)
		}
	}
	for _, raw := range []string{`"hello"`, `"12 楼"`, ``} {
		if _, err := topicID(json.RawMessage(raw)); err == nil {
			t.Errorf("%s read as a topic", raw)
		}
	}
}

func TestChunks(t *testing.T) {
	s := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 10)
	if got := chunks(s, 15); len(got) != 2 || got[0] != strings.Repeat("a", 10) || got[1] != strings.Repeat("b", 10) {
		t.Fatalf("%q", got)
	}
	if got := chunks(strings.Repeat("c", 25), 10); len(got) != 3 {
		t.Fatalf("%q", got)
	}
	if got := chunks("\n\n\n", 2); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}

// An app with UI version 2 picks the list with a segmented control; the choice comes back in the values.
func TestPageV2(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := testPlugin(t, f, nil)
	res, err := p.uiRender(context.Background(), nil, json.RawMessage(`{"contribution":"hot","ui_version":2}`))
	if err != nil {
		t.Fatal(err)
	}
	v := res.(pluginkit.View)
	checkView(t, v)
	seg := v.Sections[0].Items[0]
	if seg.Type != "segmented" || seg.Value != "hot" || len(seg.Choices) != 4 || seg.Choices[1].Title != "今日" || strings.Contains(texts(v), "→list:hot") {
		t.Fatalf("%+v\n%s", seg, texts(v))
	}
	res, err = p.uiAction(context.Background(), nil, json.RawMessage(fmt.Sprintf(`{"contribution":"hot","ui_version":2,"state":%q,"action":"list","values":{"list":"daily"}}`, v.State)))
	if err != nil {
		t.Fatal(err)
	}
	daily := *res.(pluginkit.ActionResult).View
	checkView(t, daily)
	if daily.Sections[0].Items[0].Value != "daily" || !strings.Contains(texts(daily), "今日最热 · 1 / 1 条") {
		t.Fatalf("%s", texts(daily))
	}
}

// Without its own time zone, the page uses the one the hub says is the user's.
func TestPageTimeZone(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := testPlugin(t, f, map[string]any{"tz": ""})
	p.loc = time.UTC
	res, err := p.uiAction(context.Background(), nil, json.RawMessage(`{"contribution":"hot","tz":"Asia/Tokyo","action":"topic:1001"}`))
	if err != nil {
		t.Fatal(err)
	}
	if page := texts(*res.(pluginkit.ActionResult).View); !strings.Contains(page, "op · 11-03 18:37") {
		t.Fatalf("not in the user's zone:\n%s", page)
	}
}
