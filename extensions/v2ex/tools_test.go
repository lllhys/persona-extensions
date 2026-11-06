package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func callTool(t *testing.T, a *app, name, args string) string {
	t.Helper()
	out, err := a.callTool(context.Background(), name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s %s: %v", name, args, err)
	}
	return out
}

func toolSays(t *testing.T, out string, words ...string) {
	t.Helper()
	for _, w := range words {
		if !strings.Contains(out, w) {
			t.Errorf("no %q in:\n%s", w, out)
		}
	}
}

func toolSaysNot(t *testing.T, out string, words ...string) {
	t.Helper()
	for _, w := range words {
		if strings.Contains(out, w) {
			t.Errorf("%q in:\n%s", w, out)
		}
	}
}

func TestToolHot(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, `{"exclude_nodes": ["jobs"], "nodes": ["python", "go"]}`)
	out := callTool(t, a, "v2ex_hot", `{}`)
	toolSays(t, out, "V2EX 热门：3 条，按「不看 jobs」过滤后 2 条\n", goTopic.title+"（id 1001）", "https://www.v2ex.com/t/1001", "第一段 第二段")
	toolSaysNot(t, out, jobTopic.title, goneTopic.title, "PRIVATE", "这次没读到")

	toolSays(t, callTool(t, a, "v2ex_hot", `{"filtered": false}`), "V2EX 热门：3 条\n", jobTopic.title)
	out = callTool(t, a, "v2ex_hot", `{"source": "latest", "keywords": ["python"]}`)
	toolSays(t, out, "V2EX 最新：2 条，按「不看 jobs · 关键词 python」过滤后 1 条", pyTopic.title)
	toolSaysNot(t, out, goTopic.title)
	toolSays(t, callTool(t, a, "v2ex_hot", `{"source": "nodes"}`), "V2EX 我的节点：3 条")
	out = callTool(t, a, "v2ex_hot", `{"source": "python", "limit": 1}`)
	toolSays(t, out, "V2EX 节点 python：2 条，按「不看 jobs」过滤后 2 条，这里列前 1 条", pyTopic.title)
	toolSaysNot(t, out, pyOldTopic.title)

	for args, want := range map[string]string{
		`{"source": "nosuch"}`: errNoNode.Error(),
		`{"source": "a/b"}`:    "不是节点的英文名",
		`[1]`:                  "参数不对",
	} {
		if _, err := a.callTool(context.Background(), "v2ex_hot", json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
	if _, err := testApp(t, f, "").callTool(context.Background(), "v2ex_hot", json.RawMessage(`{"source": "nodes"}`)); err == nil || !strings.Contains(err.Error(), "没有设关注的节点") {
		t.Errorf("nodes without any: %v", err)
	}
	if _, err := testApp(t, f, `{"nodes": ["nosuch"]}`).callTool(context.Background(), "v2ex_hot", json.RawMessage(`{"source": "nodes"}`)); err == nil || !strings.Contains(err.Error(), "这些节点没读到：nosuch") {
		t.Errorf("nodes none read: %v", err)
	}
	toolSays(t, callTool(t, testApp(t, f, `{"nodes": ["nosuch", "python"]}`), "v2ex_hot", `{"source": "nodes"}`), "（这些节点没读到：nosuch（"+errNoNode.Error())
	if _, err := a.callTool(context.Background(), "v2ex_nope", nil); err == nil {
		t.Error("an unknown tool answered")
	}
}

func TestToolHotLimitAndStale(t *testing.T) {
	var many []fixture
	for i := range 35 {
		many = append(many, fixture{id: int64(3000 + i), title: fmt.Sprintf("帖子标题 %d", i), node: "go", author: "alice", ago: time.Duration(i) * time.Minute})
	}
	f := newFakeSite(t)
	f.serve("/topics/latest.json", topicsJSON(many...))
	a := testApp(t, f, "")
	out := callTool(t, a, "v2ex_hot", `{"source": "latest"}`)
	toolSays(t, out, "V2EX 最新：35 条，这里列前 15 条", "\n15. ")
	toolSaysNot(t, out, "\n16. ")
	out = callTool(t, a, "v2ex_hot", `{"source": "latest", "limit": 100}`)
	toolSays(t, out, "这里列前 30 条", "\n30. ")
	toolSaysNot(t, out, "\n31. ")

	set(t, &listFor, time.Nanosecond)
	f.put("/topics/latest.json", answer{status: http.StatusBadGateway})
	toolSays(t, callTool(t, a, "v2ex_hot", `{"source": "latest"}`), "（这次没读到：V2EX：HTTP 502。这是 ", "\n1. 帖子标题 0")
}

func TestToolTopic(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	out := callTool(t, a, "v2ex_topic", `{"topic": "https://www.v2ex.com/t/1001"}`)
	if !strings.HasPrefix(out, "「"+goTopic.title+"」 https://www.v2ex.com/t/1001\nGo 编程语言 · alice · ") {
		t.Fatalf("the head:\n%s", out)
	}
	toolSays(t, out, "第一段\n第二段", "最新 10 条回复（共 25 条）：", "#16 user16（", "#25 user25（", "：第 25 条回复 第二行")
	toolSaysNot(t, out, "#15 user15", "PRIVATE", "\r")

	toolSays(t, callTool(t, a, "v2ex_topic", `{"topic": 1001, "replies": 99}`), "最新 25 条回复（共 25 条）")

	g := newFakeSite(t)
	sample(g)
	b := testApp(t, g, "")
	for _, args := range []string{`{"topic": 1001, "replies": 0}`, `{"topic": "1001", "replies": -4}`} {
		toolSaysNot(t, callTool(t, b, "v2ex_topic", args), "条回复")
	}
	if g.hit(repliesPath(1001)) != 0 {
		t.Fatal("asked for replies nobody wanted")
	}
	if _, err := a.callTool(context.Background(), "v2ex_topic", json.RawMessage(`{"topic": 1009}`)); !errors.Is(err, errNoTopic) {
		t.Errorf("a missing topic: %v", err)
	}
	for _, args := range []string{`{}`, `{"topic": "abc"}`, `{"topic": "https://example.com/t/1"}`} {
		if _, err := a.callTool(context.Background(), "v2ex_topic", json.RawMessage(args)); err == nil {
			t.Errorf("%s answered", args)
		}
	}
}

func TestToolTopicKeepsTheLatestReplies(t *testing.T) {
	long := fixture{id: 3001, title: "很长的帖子", content: strings.Repeat("字", 20000), node: "go", author: "alice", replies: 60}
	f := newFakeSite(t)
	f.serve(topicPath(3001), topicsJSON(long))
	f.serve(repliesPath(3001), repliesJSON(3001, 60, 2000))
	out := callTool(t, testApp(t, f, ""), "v2ex_topic", `{"topic": 3001, "replies": 50}`)
	if n := utf8.RuneCountInString(out); n > toolTextMost {
		t.Fatalf("%d characters", n)
	}
	toolSays(t, out, "最新 50 条回复（共 60 条）", "#11 user11（", "#60 user60（")
	toolSaysNot(t, out, "#10 user10（")
}

func TestToolsStayWithinTheirBudget(t *testing.T) {
	set(t, &toolBudget, 300*time.Millisecond)
	f := newFakeSite(t)
	sample(f)
	f.setDelay(time.Second)
	a := testApp(t, f, "")
	start := time.Now()
	_, err := a.connector(context.Background(), "tools.call", json.RawMessage(`{"contribution": "v2ex", "name": "v2ex_hot", "args": {}}`))
	if !errors.Is(err, errSlow) || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("%v after %v, want errSlow within the budget", err, time.Since(start))
	}

	set(t, &toolBudget, 800*time.Millisecond)
	f.setDelay(500 * time.Millisecond)
	b := testApp(t, f, `{"nodes": ["python", "go"]}`)
	res, err := b.connector(context.Background(), "tools.call", json.RawMessage(`{"name": "v2ex_hot", "args": {"source": "nodes"}}`))
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)["text"].(string)
	toolSays(t, out, pyOldTopic.title, "（这些节点没读到：go（"+errSlow.Error())
}
