package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// The connector's read tools, for the assistant: the hub's machine reaches the site where the user's computers and
// phone may not.

// Tool is a tool as connector.tools.list lists it.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

var tools = []Tool{
	{Name: "linuxdo_hot",
		Description: "读 LINUX DO（linux.do，中文技术社区）的帖子列表：热门（hot）、今日最热（daily）、本周最热（weekly）或最新（latest）。每条有标题、分类、作者、帖数和人数、时间、摘要、链接和 id（给 linuxdo_topic 用）。默认按用户在插件里设的兴趣（分类、关键词、隐藏 NSFW 和置顶公告）过滤，filtered 为 false 看全部。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"list":     map[string]any{"type": "string", "enum": []string{"hot", "daily", "weekly", "latest"}, "description": "哪个列表，默认 hot"},
			"filtered": map[string]any{"type": "boolean", "description": "按用户的兴趣过滤，默认 true"},
			"keywords": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "另外只看标题或摘要里有这些词的"},
			"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "description": "最多几条，默认 15"}}}},
	{Name: "linuxdo_topic",
		Description: "读 LINUX DO 的一个帖子：标题、作者、浏览 / 赞 / 帖数、标签、正文，以及前 20 楼里最热的回复（带楼层号）。topic 是帖子 id 或链接（https://linux.do/t/topic/123456）。",
		InputSchema: map[string]any{"type": "object", "required": []string{"topic"}, "properties": map[string]any{
			"topic":   map[string]any{"type": "string", "description": "帖子 id 或链接"},
			"replies": map[string]any{"type": "integer", "minimum": 0, "maximum": 20, "description": "带几条热门回复，默认 10"}}}},
}

func (p *plugin) cToolsList(context.Context, *pluginkit.Hub, json.RawMessage) (any, error) {
	return map[string]any{"tools": tools}, nil
}

func (p *plugin) cToolsCall(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var in struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	text, err := p.run(ctx, in.Name, in.Args, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text}, nil
}

// run is one tool: its text for the assistant, or with asJSON linuxdo_hot's JSON for subscriptions (catalog.go).
func (p *plugin) run(ctx context.Context, name string, raw json.RawMessage, asJSON bool) (string, error) {
	var args struct {
		List     string          `json:"list"`
		Filtered *bool           `json:"filtered"`
		Keywords []string        `json:"keywords"`
		Limit    int             `json:"limit"`
		Topic    json.RawMessage `json:"topic"`
		Replies  *int            `json:"replies"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("参数不对：%w", err)
		}
	}
	switch name {
	case "linuxdo_hot":
		list := args.List
		if list == "" {
			list = "hot"
		}
		limit := args.Limit
		if limit <= 0 || limit > 30 {
			limit = 15
		}
		a, err := p.hotAnswer(ctx, list, args.Filtered == nil || *args.Filtered, args.Keywords, limit)
		if err != nil {
			return "", err
		}
		if asJSON {
			b, err := json.Marshal(a.data())
			return string(b), err
		}
		return a.text(), nil
	case "linuxdo_topic":
		id, err := topicID(args.Topic)
		if err != nil {
			return "", err
		}
		replies := repliesShow
		if args.Replies != nil {
			replies = min(max(*args.Replies, 0), 20)
		}
		return p.toolTopic(ctx, id, replies)
	}
	return "", fmt.Errorf("没有工具 %s", name)
}

var topicIDRE = regexp.MustCompile(`(\d+)`)

// topicID reads a topic's id from an id (a number or a string) or a link (/t/topic/123, /t/some-slug/123/4, /t/123/4).
func topicID(raw json.RawMessage) (int64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return 0, errors.New("topic 要给帖子 id 或链接")
		}
		s = n.String()
	}
	s = strings.TrimSpace(s)
	if m := topicLinkRE.FindStringSubmatch(s); m != nil {
		return strconv.ParseInt(m[1], 10, 64)
	}
	if m := topicIDRE.FindStringSubmatch(s); m != nil && m[1] == s {
		return strconv.ParseInt(m[1], 10, 64)
	}
	return 0, fmt.Errorf("「%s」不是 LINUX DO 的帖子", clip(s, 80))
}

// hot is what linuxdo_hot found: a list's topics after the user's interests and the call's keywords.
type hot struct {
	list   string
	all    int    // the list's topics
	filter string // the filters that kept the rest, in words ("" none)
	kept   int    // topics the filters kept
	topics []hotTopic
	limit  int
}

func (p *plugin) hotAnswer(ctx context.Context, list string, filtered bool, keywords []string, limit int) (hot, error) {
	topics, err := p.hot(ctx, list, false)
	if err != nil {
		return hot{}, err
	}
	all := len(topics)
	f, narrow := filter{Keywords: keywords}, filter{}
	if filtered {
		// The call's keywords narrow what the settings keep; merged into the settings' OR list they would widen it.
		f, narrow = p.cfg.filter(), filter{Keywords: keywords}
	}
	topics = narrow.apply(f.apply(topics))
	d := f.describe()
	if k := nonEmpty(narrow.Keywords); len(k) > 0 {
		d = strings.TrimPrefix(d+" · 另外只看 "+strings.Join(k, "、"), " · ")
	}
	a := hot{list: list, all: all, filter: d, kept: len(topics), topics: topics, limit: limit}
	if len(topics) > limit {
		a.topics = topics[:limit]
	}
	return a, nil
}

func (a hot) text() string {
	title, _, _ := listOf(a.list)
	var b strings.Builder
	fmt.Fprintf(&b, "LINUX DO %s：%d 条", title, a.all)
	if a.filter != "" {
		fmt.Fprintf(&b, "，按「%s」过滤后 %d 条", a.filter, a.kept)
	}
	if a.kept > a.limit {
		fmt.Fprintf(&b, "，这里列前 %d 条", a.limit)
	}
	b.WriteString("\n")
	now := time.Now()
	for i, t := range a.topics {
		fmt.Fprintf(&b, "\n%d. %s（id %d）\n   %s · %s\n", i+1, t.Title, t.ID, t.meta(now), t.URL)
		if t.Excerpt != "" {
			b.WriteString("   " + clip(t.Excerpt, 160) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (p *plugin) toolTopic(ctx context.Context, id int64, replies int) (string, error) {
	t, err := p.topic(ctx, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "「%s」 %s\n%s\n", oneLine(t.Title), topicURL(t.ID), topicMeta(t, p.loc))
	if op, ok := t.opening(); ok {
		b.WriteString("\n" + clip(plainText(op.Cooked), 8000) + "\n")
	}
	if rs := t.bestReplies(replies); len(rs) > 0 {
		fmt.Fprintf(&b, "\n前 %d 楼里最热的回复：\n", len(t.PostStream.Posts))
		for _, r := range rs {
			fmt.Fprintf(&b, "#%d %s（%s，%d 赞）：%s\n", r.PostNumber, r.Username, r.CreatedAt.In(p.loc).Format("01-02 15:04"), r.likes(),
				clip(oneLine(plainText(r.Cooked)), 800))
		}
	}
	return clip(strings.TrimSpace(b.String()), 30000), nil
}
