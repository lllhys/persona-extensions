package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A topic and its replies: two requests, because the API has them apart. It answers every reply at once, oldest first
// and without likes, so what is shown is cut from the end of the list.

var errNoTopic = errors.New("V2EX 上没有这个帖子（可能删了，或者要登录才能看）")

// reply is a reply as the API has it.
type reply struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
	Created int64  `json:"created"`
	Member  member `json:"member"`
}

func parseReplies(raw []byte) ([]reply, error) {
	var rs []reply
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("%w：%v", errBadAnswer, err)
	}
	return rs, nil
}

// thread is a topic and its replies as read.
type thread struct {
	topic      topic
	replies    []reply
	repliesErr error     // the topic is here, its replies are not
	at         time.Time // when its older part was read
	stale      error     // why an older copy stands in for a failed read
}

// thread reads a topic, and its replies when wanted and it has any. A topic whose replies fail is still a topic.
func (a *app) thread(ctx context.Context, id int64, wantReplies, refresh bool) (thread, error) {
	fresh := freshness(refresh, topicFor)
	l, err := a.readTopics(ctx, topicPath(id), fresh)
	switch {
	case errors.Is(err, errNotFound):
		return thread{}, errNoTopic
	case err != nil:
		return thread{}, err
	case len(l.topics) == 0: // what the API answers for a topic it does not have
		return thread{}, errNoTopic
	}
	th := thread{topic: l.topics[0], at: l.at, stale: l.stale}
	if !wantReplies || th.topic.Replies == 0 {
		return th, nil
	}
	rd, err := a.read(ctx, repliesPath(id), fresh)
	if err == nil {
		th.replies, err = parseReplies(rd.body)
	}
	if err != nil {
		th.repliesErr = err
		return th, nil
	}
	if rd.at.Before(th.at) {
		th.at = rd.at
	}
	if th.stale == nil {
		th.stale = rd.stale
	}
	return th, nil
}

// latestReplies is the last n replies, oldest first, with their floors: a reply's place in the list.
func latestReplies(rs []reply, n int) (floors []int, out []reply) {
	from := max(len(rs)-n, 0)
	for i := from; i < len(rs); i++ {
		floors = append(floors, i+1)
		out = append(out, rs[i])
	}
	return floors, out
}

// tidy is a topic's or a reply's text with its line breaks made plain.
func tidy(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }

// topicMeta summarizes an open topic (translated example): Programmer · alice · 11-03 17:37 · 36 replies.
func topicMeta(t topic, loc *time.Location) string {
	var parts []string
	for _, s := range []string{t.nodeTitle(), t.Member.Username} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if t.Created > 0 {
		parts = append(parts, time.Unix(t.Created, 0).In(loc).Format("01-02 15:04"))
	}
	parts = append(parts, fmt.Sprintf("%d 回复", t.Replies))
	return strings.Join(parts, " · ")
}
