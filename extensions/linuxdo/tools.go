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
	var args struct {
		List     string          `json:"list"`
		Filtered *bool           `json:"filtered"`
		Keywords []string        `json:"keywords"`
		Limit    int             `json:"limit"`
		Topic    json.RawMessage `json:"topic"`
		Replies  *int            `json:"replies"`
	}
	if len(in.Args) > 0 && string(in.Args) != "null" {
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("参数不对：%w", err)
		}
	}
	var text string
	var err error
	switch in.Name {
	case "linuxdo_hot":
		list := args.List
		if list == "" {
			list = "hot"
		}
		limit := args.Limit
		if limit <= 0 || limit > 30 {
			limit = 15
		}
		text, err = p.toolHot(ctx, list, args.Filtered == nil || *args.Filtered, args.Keywords, limit)
	case "linuxdo_topic":
		var id int64
		if id, err = topicID(args.Topic); err == nil {
			replies := repliesShow
			if args.Replies != nil {
				replies = min(max(*args.Replies, 0), 20)
			}
			text, err = p.toolTopic(ctx, id, replies)
		}
	default:
		return nil, fmt.Errorf("没有工具 %s", in.Name)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text}, nil
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

func (p *plugin) toolHot(ctx context.Context, list string, filtered bool, keywords []string, limit int) (string, error) {
	topics, err := p.hot(ctx, list, false)
	if err != nil {
		return "", err
	}
	all := len(topics)
	f := filter{Keywords: keywords}
	if filtered {
		f = p.cfg.filter()
		f.Keywords = append(f.Keywords, keywords...)
	}
	topics = f.apply(topics)
	title, _, _ := listOf(list)
	var b strings.Builder
	fmt.Fprintf(&b, "LINUX DO %s：%d 条", title, all)
	if d := f.describe(); d != "" {
		fmt.Fprintf(&b, "，按「%s」过滤后 %d 条", d, len(topics))
	}
	if len(topics) > limit {
		fmt.Fprintf(&b, "，这里列前 %d 条", limit)
		topics = topics[:limit]
	}
	b.WriteString("\n")
	now := time.Now()
	for i, t := range topics {
		fmt.Fprintf(&b, "\n%d. %s（id %d）\n   %s · %s\n", i+1, t.Title, t.ID, t.meta(now), t.URL)
		if t.Excerpt != "" {
			b.WriteString("   " + clip(t.Excerpt, 160) + "\n")
		}
	}
	return strings.TrimSpace(b.String()), nil
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
