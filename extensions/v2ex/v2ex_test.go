package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestMain(m *testing.M) {
	requestGap, refreshFloor = 0, 0
	os.Exit(m.Run())
}

// set changes a package setting for one test.
func set[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// answer is what the fake site says for a path (status 0 is 200).
type answer struct {
	status int
	header map[string]string
	body   string
}

const notFound = `{"status": "error", "message": "Object Not Found", "rate_limit": {"used": 1, "quota": 600, "reset_at": 1793891514}}`

// fakeSite is a small V2EX API: answers by path with its query, counting the requests.
type fakeSite struct {
	srv *httptest.Server

	mu      sync.Mutex
	answers map[string]answer
	all     *answer       // answer everything with this when set
	delay   time.Duration // before answering; a request that ends stops the wait
	hits    map[string]int
	agents  []string
	times   []time.Time
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{answers: map[string]answer{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		f.mu.Lock()
		f.hits[key]++
		f.agents = append(f.agents, r.Header.Get("User-Agent"))
		f.times = append(f.times, time.Now())
		a, ok := f.answers[key]
		if f.all != nil {
			a, ok = *f.all, true
		}
		delay := f.delay
		f.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if !ok {
			a = answer{status: http.StatusNotFound, body: notFound}
		}
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(cmp.Or(a.status, http.StatusOK))
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) put(path string, a answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[path] = a
}

func (f *fakeSite) serve(path, body string) { f.put(path, answer{body: body}) }

// answerAll answers every path with a (nil: each with its own again).
func (f *fakeSite) answerAll(a *answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.all = a
}

func (f *fakeSite) setDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *fakeSite) hit(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeSite) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		n += h
	}
	return n
}

// fixture is a topic of the fake site (synthetic); ago is how long since it was last touched.
type fixture struct {
	id              int64
	title, content  string
	node, nodeTitle string
	author          string
	replies         int
	ago             time.Duration
	deleted         string // the deleted field as sent ("" is 0)
}

// topicsJSON is a list as the API answers it, with the whole profile the API sends for each author.
func topicsJSON(fs ...fixture) string {
	now := time.Now()
	out := []map[string]any{}
	for _, f := range fs {
		touched := now.Add(-f.ago).Unix()
		out = append(out, map[string]any{
			"id": f.id, "title": f.title, "content": f.content, "content_rendered": "<p>" + f.content + "</p>",
			"url": fmt.Sprintf("https://www.v2ex.com/t/%d", f.id), "replies": f.replies, "deleted": json.RawMessage(cmp.Or(f.deleted, "0")),
			"created": touched - 3600, "last_modified": touched - 3600, "last_touched": touched, "last_reply_by": "someone",
			"node":   map[string]any{"id": 1, "name": f.node, "title": f.nodeTitle, "url": "https://www.v2ex.com/go/" + f.node},
			"member": map[string]any{"id": 7, "username": f.author, "bio": "PRIVATE-BIO", "location": "PRIVATE-LOCATION", "github": "PRIVATE-GITHUB"},
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// repliesJSON is n replies, oldest first, each saying its number over two lines, padded with size more characters.
func repliesJSON(topicID int64, n, size int) string {
	base := time.Now().Add(-time.Duration(n+1) * time.Minute).Unix()
	out := []map[string]any{}
	for i := 1; i <= n; i++ {
		content := fmt.Sprintf("第 %d 条回复\r\n第二行", i) + strings.Repeat("长", size)
		at := base + int64(i)*60
		out = append(out, map[string]any{"id": topicID*1000 + int64(i), "topic_id": topicID, "member_id": 7, "created": at, "last_modified": at,
			"content": content, "content_rendered": content, "member": map[string]any{"id": 7, "username": fmt.Sprintf("user%d", i), "bio": "PRIVATE-BIO"}})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

var (
	goTopic     = fixture{id: 1001, title: "用 Go 写一个命令行工具", content: "第一段\r\n第二段", node: "go", nodeTitle: "Go 编程语言", author: "alice", replies: 25, ago: 30 * time.Minute}
	googleTopic = fixture{id: 1002, title: "Google 新出的笔记本", content: "大家怎么看", node: "google", nodeTitle: "Google", author: "bob", replies: 3, ago: 2 * time.Hour}
	jobTopic    = fixture{id: 1003, title: "[深圳] 招后端", content: "薪资面议", node: "jobs", nodeTitle: "酷工作", author: "carol", ago: 3 * time.Hour}
	goneTopic   = fixture{id: 1004, title: "已删除的帖子", node: "qna", nodeTitle: "问与答", author: "dave", deleted: "1"}
	pyTopic     = fixture{id: 1005, title: "Python 3.14 有什么新东西", content: "看了更新说明", node: "python", nodeTitle: "Python", author: "erin", replies: 8, ago: 10 * time.Minute}
	pyOldTopic  = fixture{id: 1006, title: "Python 打包的老问题", content: "还是那几个工具", node: "python", nodeTitle: "Python", author: "frank", replies: 2, ago: 26 * time.Hour}
)

// sample fills the site: hot (one of its topics deleted), latest, the nodes python and go (which has python's newest
// too, to be merged once), and topic 1001 with 25 replies.
func sample(f *fakeSite) {
	f.serve("/topics/hot.json", topicsJSON(goTopic, googleTopic, jobTopic, goneTopic))
	f.serve("/topics/latest.json", topicsJSON(pyTopic, goTopic))
	f.serve(nodePath("python"), topicsJSON(pyTopic, pyOldTopic))
	f.serve(nodePath("go"), topicsJSON(goTopic, pyTopic))
	f.serve(topicPath(1001), topicsJSON(goTopic))
	f.serve(repliesPath(1001), repliesJSON(1001, 25, 0))
}

// testApp is the extension reading f, initialized with config (JSON; "" for none).
func testApp(t *testing.T, f *fakeSite, config string) *app {
	t.Helper()
	a := newApp()
	a.logf = t.Logf
	a.site = newSite(f.srv.URL)
	in := &ext.Init{}
	if config != "" {
		in.InitializeParams.Config = json.RawMessage(config)
	}
	if _, err := a.init(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	return a
}

func req(version int, st *pageState) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Contribution, r.Placement, r.Size, r.UIVersion, r.TZ = pageUI, wire.PlacementPluginPage, wire.SizeFull, version, "Asia/Shanghai"
	if st != nil {
		r.State = ext.EncodeState(*st)
	}
	return r
}

// checked holds v to what the apps draw: the SDK's check and exttest's.
func checked(t *testing.T, v *wire.View) *wire.View {
	t.Helper()
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("CheckView: %v\n%s", err, texts(v))
	}
	raw, _ := json.Marshal(v)
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("exttest.CheckView: %v\n%s", err, texts(v))
	}
	return v
}

func draw(t *testing.T, a *app, version int, st *pageState) *wire.View {
	t.Helper()
	v, err := a.render(context.Background(), req(version, st))
	if err != nil {
		t.Fatal(err)
	}
	return checked(t, v)
}

// tap is a tap on the action id of the page v (values: the page's).
func tap(t *testing.T, a *app, version int, v *wire.View, id string, values map[string]string) *wire.View {
	t.Helper()
	r := req(version, nil)
	r.State, r.Action, r.Values = v.State, id, values
	res, err := a.action(context.Background(), r)
	if err != nil || res == nil || res.View == nil {
		t.Fatalf("action %s: %+v %v", id, res, err)
	}
	return checked(t, res.View)
}

// texts is a view in lines for the tests to search: [type] its words, <choices>, (badge), →action.
func texts(v *wire.View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", v.Title)
	for _, s := range v.Sections {
		if s.Title != "" {
			fmt.Fprintf(&b, "## %s\n", s.Title)
		}
		for _, c := range s.Items {
			fmt.Fprintf(&b, "[%s]", c.Type)
			for _, part := range []string{c.Title, c.Subtitle, c.Text, c.Value, c.URL} {
				if part != "" {
					b.WriteString(" " + part)
				}
			}
			for _, ch := range c.Choices {
				fmt.Fprintf(&b, " <%s:%s>", ch.Value, ch.Title)
			}
			if c.Badge != nil {
				fmt.Fprintf(&b, " (%s)", c.Badge.Text)
			}
			if c.Action != nil {
				fmt.Fprintf(&b, " →%s", c.Action.ID)
			}
			b.WriteString("\n")
		}
		if s.Footer != "" {
			fmt.Fprintf(&b, "_%s_\n", s.Footer)
		}
	}
	return b.String()
}

func findRow(v *wire.View, title string) *wire.Component {
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			if c := &v.Sections[i].Items[j]; c.Type == wire.ComponentRow && c.Title == title {
				return c
			}
		}
	}
	return nil
}

func idsOf(ts []topic) []int64 {
	var ids []int64
	for _, x := range ts {
		ids = append(ids, x.ID)
	}
	return ids
}
