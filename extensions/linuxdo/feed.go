package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The lists, from the site's RSS (its JSON lists are behind a challenge for a server's address): each item is a
// topic with its title, link, author, category, time, an excerpt and how many posts and people it has.

// lists are the lists, in the order the page offers them.
var lists = []struct{ id, title, short, path string }{
	{"hot", "热门", "热门", "/hot.rss"},
	{"daily", "今日最热", "今日", "/top.rss?period=daily"},
	{"weekly", "本周最热", "本周", "/top.rss?period=weekly"},
	{"latest", "最新", "最新", "/latest.rss"},
}

func listOf(id string) (title, path string, ok bool) {
	for _, l := range lists {
		if l.id == id {
			return l.title, l.path, true
		}
	}
	return "", "", false
}

// hotTopic is one topic of a list.
type hotTopic struct {
	ID       int64
	Title    string
	URL      string
	Author   string
	Category string
	Excerpt  string
	Posts    int
	People   int
	At       time.Time
	Pinned   bool // a pinned announcement
}

type rssDoc struct {
	XMLName xml.Name `xml:"rss"` // anything else (a firewall's page) is not a list
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Creator     string `xml:"creator"`
			Category    string `xml:"category"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
			Pinned      string `xml:"topicPinned"` // discourse:topicPinned, Yes / No
		} `xml:"item"`
	} `xml:"channel"`
}

var (
	topicLinkRE = regexp.MustCompile(`/t/(?:[^/]+/)?(\d+)`)
	countsRE    = regexp.MustCompile(`(\d+)\s*(?:个帖子|posts?)\s*-\s*(\d+)\s*(?:位参与者|participants?)`)
	tagRE       = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRE     = regexp.MustCompile(`\s+`)
)

// parseFeed reads a list's RSS.
func parseFeed(raw []byte) ([]hotTopic, error) {
	var doc rssDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("LINUX DO 的列表读不懂：%w", err)
	}
	var out []hotTopic
	for _, it := range doc.Channel.Items {
		m := topicLinkRE.FindStringSubmatch(it.Link)
		if m == nil {
			continue
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		t := hotTopic{ID: id, Title: oneLine(html.UnescapeString(it.Title)), URL: topicURL(id), Author: strings.TrimSpace(it.Creator),
			Category: strings.TrimSpace(html.UnescapeString(it.Category)), Pinned: strings.EqualFold(strings.TrimSpace(it.Pinned), "yes")}
		if at, err := time.Parse(time.RFC1123Z, strings.TrimSpace(it.PubDate)); err == nil {
			t.At = at.UTC()
		}
		desc := oneLine(html.UnescapeString(tagRE.ReplaceAllString(it.Description, " ")))
		if c := countsRE.FindStringSubmatchIndex(desc); c != nil {
			t.Posts, _ = strconv.Atoi(desc[c[2]:c[3]])
			t.People, _ = strconv.Atoi(desc[c[4]:c[5]])
			desc = desc[:c[0]]
		}
		desc = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(desc), "阅读完整话题"))
		t.Excerpt = clip(desc, 200)
		out = append(out, t)
	}
	return out, nil
}

// webBase is where a topic is for people (the links in the page and the tools), whatever siteBase a test uses.
const webBase = "https://linux.do"

func topicURL(id int64) string { return fmt.Sprintf("%s/t/topic/%d", webBase, id) }

// hot reads a list (from the cache while it is fresh; refresh reads it again).
func (p *plugin) hot(ctx context.Context, list string, refresh bool) ([]hotTopic, error) {
	_, path, ok := listOf(list)
	if !ok {
		return nil, fmt.Errorf("没有「%s」这个列表：hot、daily、weekly、latest", list)
	}
	fresh := listFor
	if refresh {
		fresh = 0
	}
	raw, err := p.read(ctx, path, fresh)
	if err != nil {
		return nil, err
	}
	return parseFeed(raw)
}

// filter is the user's interests (the plugin's settings).
type filter struct {
	Categories        []string
	ExcludeCategories []string
	Keywords          []string
	ExcludeKeywords   []string
	HideNSFW          bool
	HidePinned        bool
}

func (f filter) keep(t hotTopic) bool {
	text := strings.ToLower(t.Title + "\n" + t.Excerpt)
	anyIn := func(words []string, s string) bool {
		for _, w := range words {
			if w = strings.ToLower(strings.TrimSpace(w)); w != "" && strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	category := strings.ToLower(t.Category)
	switch {
	case f.HideNSFW && strings.Contains(strings.ToUpper(t.Title), "NSFW"), f.HidePinned && t.Pinned:
		return false
	case len(nonEmpty(f.Categories)) > 0 && !anyIn(f.Categories, category):
		return false
	case anyIn(f.ExcludeCategories, category):
		return false
	case anyIn(f.ExcludeKeywords, text):
		return false
	case len(nonEmpty(f.Keywords)) > 0 && !anyIn(f.Keywords, text):
		return false
	}
	return true
}

// describe is the filter in a line for people ("" when it keeps everything).
func (f filter) describe() string {
	var parts []string
	if c := nonEmpty(f.Categories); len(c) > 0 {
		parts = append(parts, "只看 "+strings.Join(c, "、"))
	}
	if c := nonEmpty(f.ExcludeCategories); len(c) > 0 {
		parts = append(parts, "不看 "+strings.Join(c, "、"))
	}
	if k := nonEmpty(f.Keywords); len(k) > 0 {
		parts = append(parts, "关键词 "+strings.Join(k, "、"))
	}
	if k := nonEmpty(f.ExcludeKeywords); len(k) > 0 {
		parts = append(parts, "屏蔽 "+strings.Join(k, "、"))
	}
	if f.HideNSFW {
		parts = append(parts, "隐藏 NSFW")
	}
	if f.HidePinned {
		parts = append(parts, "隐藏置顶")
	}
	return strings.Join(parts, " · ")
}

func (f filter) apply(ts []hotTopic) []hotTopic {
	var out []hotTopic
	for _, t := range ts {
		if f.keep(t) {
			out = append(out, t)
		}
	}
	return out
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// meta summarizes a topic from a list in one line (translated example): Development and Optimization · alice · 75 posts · 59 people · 3 hours ago.
func (t hotTopic) meta(now time.Time) string {
	var parts []string
	for _, s := range []string{t.Category, t.Author} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if t.Posts > 0 {
		parts = append(parts, fmt.Sprintf("%d 帖 · %d 人", t.Posts, t.People))
	}
	if !t.At.IsZero() {
		parts = append(parts, ago(now.Sub(t.At)))
	}
	return strings.Join(parts, " · ")
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

func oneLine(s string) string { return strings.TrimSpace(spaceRE.ReplaceAllString(s, " ")) }

// clip cuts s to at most n characters, marking the cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}
