package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

func stateOf(t *testing.T, raw string) pageState {
	t.Helper()
	r := &ext.UIRequest{}
	r.State = raw
	var st pageState
	if err := r.DecodeState(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// says fails unless the page has each of words.
func says(t *testing.T, v *wire.View, words ...string) {
	t.Helper()
	s := texts(v)
	for _, w := range words {
		if !strings.Contains(s, w) {
			t.Errorf("no %q in:\n%s", w, s)
		}
	}
}

// saysNot fails if the page has any of words.
func saysNot(t *testing.T, v *wire.View, words ...string) {
	t.Helper()
	s := texts(v)
	for _, w := range words {
		if strings.Contains(s, w) {
			t.Errorf("%q in:\n%s", w, s)
		}
	}
}

func TestListPage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	v := draw(t, a, 3, nil)
	says(t, v, "<hot:热门>", "<latest:最新>", "## 热门 · 3 条", "读于 ", "刷新")
	saysNot(t, v, "nodes:", goneTopic.title, "PRIVATE", "这次没读到")
	row := findRow(v, goTopic.title)
	if row == nil || row.Subtitle != "Go 编程语言 · alice · 25 回复 · 30 分钟前" {
		t.Fatalf("the row: %+v", row)
	}
	if row.Action == nil || row.Action.ID != "topic:1001" || row.Action.Push == nil || row.Action.Push.Title != goTopic.title {
		t.Fatalf("the row's action: %+v", row.Action)
	}
	if st := stateOf(t, row.Action.Push.State); st != (pageState{List: "hot", Topic: 1001}) {
		t.Fatalf("the row opens %+v", st)
	}
}

func TestPickerBeforeUIVersion2(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	v := draw(t, a, 1, nil)
	saysNot(t, v, "[segmented]")
	if hot, latest := findRow(v, "热门"), findRow(v, "最新"); hot == nil || hot.Badge == nil || latest == nil || latest.Badge != nil {
		t.Fatalf("the rows: %+v %+v", hot, latest)
	}
	v = tap(t, a, 1, v, "list:latest", nil)
	says(t, v, "## 最新 · 2 条")
	if hot, latest := findRow(v, "热门"), findRow(v, "最新"); hot.Badge != nil || latest.Badge == nil {
		t.Fatalf("the rows after a tap: %+v %+v", hot, latest)
	}
}

func TestSwitchingLists(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, `{"nodes": ["python", "go"]}`)
	v := draw(t, a, 3, nil)
	says(t, v, "<nodes:我的节点>", "## 热门")
	v = tap(t, a, 3, v, "list", map[string]string{"list": "nodes"})
	says(t, v, "## 我的节点 · 3 条")
	if st := stateOf(t, v.State); st.List != "nodes" {
		t.Fatalf("state %+v", st)
	}
	v = tap(t, a, 3, v, "list", map[string]string{"list": "bogus"})
	says(t, v, "## 我的节点 · 3 条")

	// nodes with none to read is not on offer, whoever asks for it
	b := testApp(t, f, `{"list": "nodes"}`)
	says(t, draw(t, b, 3, nil), "## 热门")
	says(t, draw(t, b, 3, &pageState{List: "nodes"}), "## 热门")

	c := testApp(t, f, `{"list": "latest"}`)
	says(t, draw(t, c, 3, nil), "## 最新 · 2 条")
	says(t, draw(t, c, 3, &pageState{List: "hot"}), "## 热门")

	r := req(3, nil)
	r.State = "%%% not a state"
	got, err := c.render(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	says(t, checked(t, got), "## 最新")
}

func TestFilterOnThePage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, `{"exclude_nodes": ["jobs"]}`)
	v := draw(t, a, 3, nil)
	says(t, v, "## 热门 · 2 / 3 条", "_按你的兴趣：不看 jobs。在这个扩展的设置里改_", "看全部 3 条 →all")
	if findRow(v, jobTopic.title) != nil {
		t.Fatal("the node kept out is on the page")
	}
	v = tap(t, a, 3, v, "all", nil)
	says(t, v, "## 热门 · 全部 3 条", "_没按你的兴趣过滤_", "按我的兴趣过滤 →all")
	row := findRow(v, jobTopic.title)
	if row == nil {
		t.Fatal("the whole list has no such row")
	}
	if st := stateOf(t, row.Action.Push.State); !st.All {
		t.Fatalf("a topic opened from the whole list forgets it: %+v", st)
	}
	v = tap(t, a, 3, v, "all", nil)
	says(t, v, "## 热门 · 2 / 3 条")

	b := testApp(t, f, `{"keywords": ["没有这个词"]}`)
	says(t, draw(t, b, 3, nil), "## 热门 · 0 / 3 条", "这里没有可看的帖子", "看全部 3 条")
}

func TestNotesOnThePage(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	v := draw(t, testApp(t, f, `{"nodes": ["程序员"]}`), 3, nil)
	says(t, v, "这些不是节点名，没有读：程序员")
	saysNot(t, v, "<nodes:")
	says(t, draw(t, testApp(t, f, `{"nodes": ["nosuch", "python"], "list": "nodes"}`), 3, nil), "这些节点没读到：nosuch（", "## 我的节点 · 2 条")

	a := testApp(t, f, "")
	v = draw(t, a, 3, nil)
	f.put("/topics/hot.json", answer{status: http.StatusBadGateway})
	v = tap(t, a, 3, v, "refresh", nil)
	says(t, v, "这次没读到：V2EX：HTTP 502", "## 热门 · 3 条")

	b := testApp(t, f, "")
	v = draw(t, b, 3, nil)
	says(t, v, "V2EX：HTTP 502", "重试 →refresh", "## 热门")
	saysNot(t, v, "读于 ", "这次没读到")
}
