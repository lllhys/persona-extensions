package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseTopics(t *testing.T) {
	ts, err := parseTopics([]byte(topicsJSON(goTopic, goneTopic, googleTopic)))
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(ts); !slices.Equal(got, []int64{1001, 1002}) {
		t.Fatalf("ids %v, want the deleted one dropped", got)
	}
	g := ts[0]
	if g.Title != goTopic.title || g.Content != goTopic.content || g.Replies != 25 || g.Node.Name != "go" ||
		g.Node.Title != "Go 编程语言" || g.Member.Username != "alice" || g.Created != g.LastTouched-3600 {
		t.Fatalf("parsed %+v", g)
	}
	if b, _ := json.Marshal(ts); strings.Contains(string(b), "PRIVATE") {
		t.Fatalf("a profile field was kept: %s", b)
	}
	if m := g.meta(time.Now()); m != "Go 编程语言 · alice · 25 回复 · 30 分钟前" {
		t.Fatalf("meta %q", m)
	}
	if s := (topic{ID: 9}).title(); s != "帖子 9" {
		t.Fatalf("an untitled topic is %q", s)
	}
	if _, err := parseTopics([]byte(`{`)); !errors.Is(err, errBadAnswer) {
		t.Fatalf("broken JSON: %v", err)
	}
}

func TestDeleted(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "null": false, "false": false, `"0"`: false, `""`: false,
		"1": true, "true": true, `"1"`: true, "2": true} {
		if got := (topic{Deleted: json.RawMessage(raw)}).deleted(); got != want {
			t.Errorf("deleted %s: %v, want %v", raw, got, want)
		}
	}
	if ts, err := parseTopics([]byte(`[{"id": 5, "title": "没有 deleted"}]`)); err != nil || len(ts) != 1 {
		t.Fatalf("a topic without the field: %v %v", ts, err)
	}
}

func TestFilter(t *testing.T) {
	ts, err := parseTopics([]byte(topicsJSON(goTopic, googleTopic, jobTopic, pyTopic)))
	if err != nil {
		t.Fatal(err)
	}
	all := []int64{1001, 1002, 1003, 1005}
	for _, c := range []struct {
		name string
		f    filter
		want []int64
	}{
		{"nothing", filter{}, all},
		{"blank words", filter{ExcludeNodes: []string{""}, Keywords: []string{" "}}, all},
		{"a node by its name, not a prefix", filter{ExcludeNodes: []string{"go"}}, []int64{1002, 1003, 1005}},
		{"a node by its title", filter{ExcludeNodes: []string{"酷工作"}}, []int64{1001, 1002, 1005}},
		{"in any case", filter{ExcludeNodes: []string{"GOOGLE"}}, []int64{1001, 1003, 1005}},
		{"a keyword in a title", filter{Keywords: []string{"python"}}, []int64{1005}},
		{"a keyword in the text", filter{Keywords: []string{"第二段"}}, []int64{1001}},
		{"a word kept out", filter{ExcludeKeywords: []string{"招"}}, []int64{1001, 1002, 1005}},
		{"out wins", filter{Keywords: []string{"python"}, ExcludeNodes: []string{"python"}}, nil},
	} {
		if got := idsOf(c.f.apply(ts)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	f := filter{ExcludeNodes: []string{"jobs", " "}, Keywords: []string{"go"}, ExcludeKeywords: []string{"广告"}}
	if d := f.describe(); d != "不看 jobs · 关键词 go · 屏蔽 广告" {
		t.Fatalf("describe %q", d)
	}
	if d := (filter{Keywords: []string{""}}).describe(); d != "" {
		t.Fatalf("an empty filter says %q", d)
	}
}

func TestCleanNodes(t *testing.T) {
	good, bad := cleanNodes([]string{" python ", "go", "", "python", "程序员", "a/b", "n1", "n2", "n3", "n4"})
	if !slices.Equal(good, []string{"python", "go", "n1", "n2", "n3"}) {
		t.Fatalf("good %v: trimmed, once each, at most %d", good, nodesMost)
	}
	if !slices.Equal(bad, []string{"程序员", "a/b"}) {
		t.Fatalf("bad %v", bad)
	}
}

func TestWords(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "刚刚", 5 * time.Minute: "5 分钟前",
		3 * time.Hour: "3 小时前", 50 * time.Hour: "2 天前"} {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
	if got := clip("一二三四五", 3); got != "一二三…" {
		t.Errorf("clip: %q", got)
	}
	if got := clip("abc", 3); got != "abc" {
		t.Errorf("clip of a short one: %q", got)
	}
	if got := oneLine(" a\r\n  b\t c "); got != "a b c" {
		t.Errorf("oneLine: %q", got)
	}
}
