package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
)

func TestTopicPage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	st := pageState{List: "latest", Topic: 1001}
	v := draw(t, a, 3, &st)
	if v.Title != goTopic.title || stateOf(t, v.State) != st {
		t.Fatalf("title %q, state %q", v.Title, v.State)
	}
	says(t, v, "Go 编程语言 · alice", "第一段\n第二段", "## 最新回复", "#6 user6", "#25 user25", "第 25 条回复\n第二行",
		"_最新 20 条，共 25 条；更早的在网页里看_", "https://www.v2ex.com/t/1001", "刷新")
	saysNot(t, v, "#5 user5", "返回列表", "\r", "PRIVATE", "正文太长", "这次没读到")
}

func TestBackBeforeUIVersion3(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	v := draw(t, a, 2, &pageState{List: "latest", Topic: 1001})
	says(t, v, "返回列表 →back")
	v = tap(t, a, 2, v, "back", nil)
	says(t, v, "## 最新 · 2 条")
	if st := stateOf(t, v.State); st != (pageState{List: "latest"}) {
		t.Fatalf("back to %+v", st)
	}
}

func TestOpeningATopicWithoutAUIVersion(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	v := draw(t, a, 0, nil)
	if got := tap(t, a, 0, v, "topic:nope", nil); stateOf(t, got.State).Topic != 0 {
		t.Fatalf("topic:nope opened %q", got.State)
	}
	v = tap(t, a, 0, v, "topic:1001", nil)
	if v.Title != goTopic.title {
		t.Fatalf("title %q", v.Title)
	}
	says(t, v, "返回列表 →back")
	says(t, tap(t, a, 0, v, "back", nil), "## 热门 · 3 条")
}

func TestRefreshReadsATopicAgain(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	st := pageState{Topic: 1001}
	v := draw(t, a, 3, &st)
	draw(t, a, 3, &st)
	if f.hit(topicPath(1001)) != 1 || f.hit(repliesPath(1001)) != 1 {
		t.Fatalf("drawn twice: topic %d, replies %d requests", f.hit(topicPath(1001)), f.hit(repliesPath(1001)))
	}
	tap(t, a, 3, v, "refresh", nil)
	if f.hit(topicPath(1001)) != 2 || f.hit(repliesPath(1001)) != 2 {
		t.Fatalf("refreshed: topic %d, replies %d requests", f.hit(topicPath(1001)), f.hit(repliesPath(1001)))
	}
}

func TestTimeZones(t *testing.T) {
	f := newFakeSite(t)
	r := req(3, nil)
	if got := testApp(t, f, `{"tz": "UTC"}`).locFor(r).String(); got != "UTC" {
		t.Fatalf("the extension's setting wins: %s", got)
	}
	a := testApp(t, f, "")
	loc := a.locFor(r)
	if loc.String() != "Asia/Shanghai" {
		t.Fatalf("the user's zone: %s", loc)
	}
	if got := topicMeta(topic{Created: 1700000000}, loc); got != "11-15 06:13 · 0 回复" {
		t.Fatalf("topicMeta: %q", got)
	}
	r.TZ = "Mars/Olympus"
	if got := a.locFor(r); got != a.loc {
		t.Fatalf("a zone that is not one: %s", got)
	}
}

func TestProblemsOnATopicPage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	v := draw(t, a, 3, &pageState{Topic: 1009})
	says(t, v, errNoTopic.Error(), "https://www.v2ex.com/t/1009", "刷新")

	b := testApp(t, f, "")
	v = draw(t, b, 3, &pageState{Topic: 1001})
	f.put(topicPath(1001), answer{status: http.StatusBadGateway})
	v = tap(t, b, 3, v, "refresh", nil)
	says(t, v, "这次没读到：V2EX：HTTP 502", "第一段", "## 最新回复")

	sample(f)
	f.put(repliesPath(1001), answer{status: http.StatusBadGateway})
	v = draw(t, testApp(t, f, ""), 3, &pageState{Topic: 1001})
	says(t, v, "第一段", "回复这次没读到：V2EX：HTTP 502")
	saysNot(t, v, "## 最新回复")
}

func TestALongTopicFits(t *testing.T) {
	long := fixture{id: 2001, title: strings.Repeat("题", 400), content: strings.Repeat("字", 33000), node: "go", nodeTitle: "Go", author: "alice", replies: 3}
	f := newFakeSite(t)
	f.serve("/topics/hot.json", topicsJSON(long))
	f.serve(topicPath(2001), topicsJSON(long))
	f.serve(repliesPath(2001), repliesJSON(2001, 3, 3000))
	a := testApp(t, f, "")

	row := findRow(draw(t, a, 3, nil), clip(long.title, 300))
	if row == nil || utf8.RuneCountInString(row.Action.Push.Title) > 201 {
		t.Fatalf("the row: %+v", row)
	}
	v := draw(t, a, 3, &pageState{Topic: 2001})
	says(t, v, "正文太长，后面的在网页里看")
	if n := utf8.RuneCountInString(v.Title); n > 201 {
		t.Fatalf("a title of %d characters", n)
	}
	body := 0
	for _, it := range v.Sections[0].Items {
		n := utf8.RuneCountInString(it.Text)
		if n > chunkMost {
			t.Fatalf("a text of %d characters", n)
		}
		if it.Style == wire.TextStyleBody {
			body += n
		}
	}
	if body != openingMost+1 { // the cut's mark
		t.Fatalf("%d characters of the body, want %d", body, openingMost+1)
	}
	replies := 0
	for _, it := range v.Sections[1].Items {
		if it.Style == wire.TextStyleBody {
			replies++
			if n := utf8.RuneCountInString(it.Text); n > replyMost+1 {
				t.Fatalf("a reply of %d characters", n)
			}
		}
	}
	if replies != 3 {
		t.Fatalf("%d replies", replies)
	}
}

func TestChunks(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want []string
	}{
		{"aaa\nbbb\nccc", 5, []string{"aaa", "bbb", "ccc"}},
		{"一二三四五六七", 3, []string{"一二三", "四五六", "七"}},
		{"a\nbcdefgh", 4, []string{"a\nbc", "defg", "h"}}, // a break too early is not worth it
		{"  \n ", 10, nil},
	} {
		if got := chunks(c.s, c.n); !slices.Equal(got, c.want) {
			t.Errorf("chunks(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestAPageStaysWithinItsBudget(t *testing.T) {
	set(t, &renderBudget, 800*time.Millisecond)
	f := newFakeSite(t)
	sample(f)
	f.setDelay(500 * time.Millisecond)
	a := testApp(t, f, `{"nodes": ["python", "go", "n1", "n2", "n3"], "list": "nodes"}`)

	start := time.Now()
	v := draw(t, a, 3, nil)
	if took := time.Since(start); took > 2*time.Second { // five nodes one after another take 2.5
		t.Fatalf("the page took %v", took)
	}
	says(t, v, pyOldTopic.title, "这些节点没读到：go（"+errSlow.Error(), "n3（"+errSlow.Error())

	// What was read stays cached, so the next draw gets further.
	f.setDelay(0)
	for _, n := range []string{"n1", "n2", "n3"} {
		f.serve(nodePath(n), topicsJSON(jobTopic))
	}
	v = draw(t, a, 3, nil)
	says(t, v, pyOldTopic.title, goTopic.title, jobTopic.title)
	saysNot(t, v, "这些节点没读到")
	if n := f.hit(nodePath("python")); n != 1 {
		t.Fatalf("python asked for %d times", n)
	}
}
