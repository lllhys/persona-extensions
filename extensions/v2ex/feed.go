package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The lists, from the API: hot and latest, and a node's topics. Each topic of a list already has its text.

// listDef is one of the lists; nodes has no path: it is the user's nodes, read in turn.
type listDef struct{ id, title, path string }

// lists are the lists, in the order the page offers them.
var lists = []listDef{
	{"hot", "热门", "/topics/hot.json"},
	{"latest", "最新", "/topics/latest.json"},
	{"nodes", "我的节点", ""},
}

func listTitle(id string) string {
	for _, l := range lists {
		if l.id == id {
			return l.title
		}
	}
	return id
}

var errNoNode = errors.New("V2EX 上没有这个节点")

// member is who wrote something. The API sends a whole profile (bio, location, links); only the name is kept.
type member struct {
	Username string `json:"username"`
}

// topic is a topic as the API has it; times are Unix seconds.
type topic struct {
	ID          int64           `json:"id"`
	Title       string          `json:"title"`
	Content     string          `json:"content"`
	Replies     int             `json:"replies"`
	Created     int64           `json:"created"`
	LastTouched int64           `json:"last_touched"`
	Deleted     json.RawMessage `json:"deleted"` // always 0 so far: what else it can be is unknown, and must not lose the list
	Node        struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	} `json:"node"`
	Member member `json:"member"`
}

func (t topic) deleted() bool {
	switch string(t.Deleted) {
	case "", "0", "null", "false", `"0"`, `""`:
		return false
	}
	return true
}

func (t topic) nodeTitle() string { return cmp.Or(t.Node.Title, t.Node.Name) }

func (t topic) title() string { return cmp.Or(oneLine(t.Title), fmt.Sprintf("帖子 %d", t.ID)) }

// meta summarizes a topic of a list in one line (translated example): Programmer · alice · 12 replies · 3 hours ago.
func (t topic) meta(now time.Time) string {
	var parts []string
	for _, s := range []string{t.nodeTitle(), t.Member.Username} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	parts = append(parts, fmt.Sprintf("%d 回复", t.Replies))
	if at := cmp.Or(t.LastTouched, t.Created); at > 0 {
		parts = append(parts, ago(now.Sub(time.Unix(at, 0))))
	}
	return strings.Join(parts, " · ")
}

func parseTopics(raw []byte) ([]topic, error) {
	var ts []topic
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil, fmt.Errorf("%w：%v", errBadAnswer, err)
	}
	return slices.DeleteFunc(ts, topic.deleted), nil
}

// listing is a list as read.
type listing struct {
	topics []topic
	at     time.Time // when its oldest part was read; zero when none of it was
	stale  error     // why an older copy stands in for a failed read
	missed []string  // the nodes that could not be read, in words
}

// freshness is how old a list may be: a refresh asks for a new one, but not within refreshFloor of the last.
func freshness(refresh bool, usual time.Duration) time.Duration {
	if refresh {
		return refreshFloor
	}
	return usual
}

// list reads a list by its id. Only an error means nothing to show: a node that fails is one of missed.
func (a *app) list(ctx context.Context, id string, refresh bool) (listing, error) {
	fresh := freshness(refresh, listFor)
	if id == "nodes" {
		return a.merged(ctx, a.nodes, fresh), nil
	}
	for _, l := range lists {
		if l.id == id {
			return a.readTopics(ctx, l.path, fresh)
		}
	}
	return listing{}, fmt.Errorf("没有「%s」这个列表", clip(id, 80))
}

func (a *app) readTopics(ctx context.Context, path string, fresh time.Duration) (listing, error) {
	rd, err := a.read(ctx, path, fresh)
	if err != nil {
		return listing{}, err
	}
	ts, err := parseTopics(rd.body)
	if err != nil {
		return listing{}, err
	}
	return listing{topics: ts, at: rd.at, stale: rd.stale}, nil
}

// node reads one node's topics.
func (a *app) node(ctx context.Context, name string, fresh time.Duration) (listing, error) {
	if !nodeNameRE.MatchString(name) {
		return listing{}, errNoNode
	}
	l, err := a.readTopics(ctx, nodePath(name), fresh)
	if errors.Is(err, errNotFound) {
		return listing{}, errNoNode
	}
	return l, err
}

// merged reads the nodes in turn into one list, latest activity first. When the time runs out the nodes left answer
// from the cache or join missed, and what was read stays cached, so the next try gets further.
func (a *app) merged(ctx context.Context, names []string, fresh time.Duration) listing {
	var out listing
	seen := map[int64]bool{}
	for _, name := range names {
		l, err := a.node(ctx, name, fresh)
		if err != nil {
			out.missed = append(out.missed, fmt.Sprintf("%s（%s）", name, err))
			continue
		}
		if out.at.IsZero() || l.at.Before(out.at) {
			out.at = l.at
		}
		out.stale = cmp.Or(out.stale, l.stale)
		for _, t := range l.topics {
			if !seen[t.ID] {
				seen[t.ID] = true
				out.topics = append(out.topics, t)
			}
		}
	}
	slices.SortStableFunc(out.topics, func(x, y topic) int { return cmp.Compare(y.LastTouched, x.LastTouched) })
	return out
}

// filter is the user's interests (the extension's settings).
type filter struct {
	ExcludeNodes    []string
	Keywords        []string
	ExcludeKeywords []string
}

func (f filter) keep(t topic) bool {
	text := strings.ToLower(t.Title + "\n" + t.Content)
	anyIn := func(words []string) bool {
		return slices.ContainsFunc(nonEmpty(words), func(w string) bool { return strings.Contains(text, strings.ToLower(w)) })
	}
	switch {
	case slices.ContainsFunc(nonEmpty(f.ExcludeNodes), func(n string) bool {
		return strings.EqualFold(n, t.Node.Name) || strings.EqualFold(n, t.Node.Title)
	}):
		return false
	case anyIn(f.ExcludeKeywords):
		return false
	case len(nonEmpty(f.Keywords)) > 0 && !anyIn(f.Keywords):
		return false
	}
	return true
}

// describe is the filter in a line for people ("" when it keeps everything).
func (f filter) describe() string {
	var parts []string
	if c := nonEmpty(f.ExcludeNodes); len(c) > 0 {
		parts = append(parts, "不看 "+strings.Join(c, "、"))
	}
	if k := nonEmpty(f.Keywords); len(k) > 0 {
		parts = append(parts, "关键词 "+strings.Join(k, "、"))
	}
	if k := nonEmpty(f.ExcludeKeywords); len(k) > 0 {
		parts = append(parts, "屏蔽 "+strings.Join(k, "、"))
	}
	return strings.Join(parts, " · ")
}

func (f filter) apply(ts []topic) []topic {
	var out []topic
	for _, t := range ts {
		if f.keep(t) {
			out = append(out, t)
		}
	}
	return out
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天前", int(d.Hours()/24))
}

var spaceRE = regexp.MustCompile(`\s+`)

func oneLine(s string) string { return strings.TrimSpace(spaceRE.ReplaceAllString(s, " ")) }

// clip cuts s to at most n characters, marking the cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}
