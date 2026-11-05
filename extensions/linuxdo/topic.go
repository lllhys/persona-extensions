package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A topic, from its public JSON (/t/topic/<id>.json): its words and the first page of posts (20), as text — the page
// draws them, the phone never reaches the site.

type topicDoc struct {
	ID               int64           `json:"id"`
	Title            string          `json:"title"`
	PostsCount       int             `json:"posts_count"`
	LikeCount        int             `json:"like_count"`
	Views            int             `json:"views"`
	ParticipantCount int             `json:"participant_count"`
	CreatedAt        time.Time       `json:"created_at"`
	Tags             json.RawMessage `json:"tags"`
	PostStream       struct {
		Posts []postDoc `json:"posts"`
	} `json:"post_stream"`
}

type postDoc struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	CreatedAt          time.Time `json:"created_at"`
	Cooked             string    `json:"cooked"`
	PostNumber         int       `json:"post_number"`
	PostType           int       `json:"post_type"` // 1 regular; others are moderator actions and whispers
	Hidden             bool      `json:"hidden"`
	Score              float64   `json:"score"`
	ReactionUsersCount int       `json:"reaction_users_count"`
	ActionsSummary     []struct {
		ID    int `json:"id"`
		Count int `json:"count"`
	} `json:"actions_summary"`
}

// likes are a post's likes (Discourse's like action, or the reactions when the site counts those).
func (p postDoc) likes() int {
	n := p.ReactionUsersCount
	for _, a := range p.ActionsSummary {
		if a.ID == 2 && a.Count > n {
			n = a.Count
		}
	}
	return n
}

// tags are the topic's tag names (objects on newer Discourse, strings on older).
func (t topicDoc) tags() []string {
	var names []string
	var strs []string
	if json.Unmarshal(t.Tags, &strs) == nil {
		return strs
	}
	var objs []struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(t.Tags, &objs)
	for _, o := range objs {
		if o.Name != "" {
			names = append(names, o.Name)
		}
	}
	return names
}

func (p *plugin) topic(ctx context.Context, id int64) (topicDoc, error) {
	var t topicDoc
	raw, err := p.read(ctx, fmt.Sprintf("/t/topic/%d.json", id), topicFor)
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, fmt.Errorf("LINUX DO 的帖子读不懂：%w", err)
	}
	if t.ID == 0 || len(t.PostStream.Posts) == 0 {
		return t, errNoTopic
	}
	return t, nil
}

// opening is the topic's first post.
func (t topicDoc) opening() (postDoc, bool) {
	for _, p := range t.PostStream.Posts {
		if p.PostNumber == 1 {
			return p, true
		}
	}
	return postDoc{}, false
}

// bestReplies are up to n replies of the first page, the most liked (by the site's score) first. A reply whose
// cooked HTML renders to no words (an emoji-only reaction, a video- or audio-only post) is skipped: as a text
// item it would be empty, which the host's view check refuses, and the tool's reply list would show a bare line.
func (t topicDoc) bestReplies(n int) []postDoc {
	var rs []postDoc
	for _, p := range t.PostStream.Posts {
		if p.PostNumber > 1 && p.PostType == 1 && !p.Hidden && strings.TrimSpace(plainText(p.Cooked)) != "" {
			rs = append(rs, p)
		}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Score != rs[j].Score {
			return rs[i].Score > rs[j].Score
		}
		return rs[i].PostNumber < rs[j].PostNumber
	})
	if len(rs) > n {
		rs = rs[:n]
	}
	return rs
}

// segment is a piece of a post as text: words, or a block of code.
type segment struct {
	Code bool
	Text string
}

var (
	dropRE    = regexp.MustCompile(`(?is)<(svg|script|style)\b.*?</(svg|script|style)>|<div class="meta">.*?</div>`)
	oneboxRE  = regexp.MustCompile(`(?is)<aside\b[^>]*\bclass="[^"]*\bonebox\b[^"]*"[^>]*>.*?</aside>`)
	srcRE     = regexp.MustCompile(`data-onebox-src="([^"]*)"`)
	emojiRE   = regexp.MustCompile(`(?is)<img[^>]*\bclass="[^"]*\b(?:emoji|avatar)\b[^"]*"[^>]*>`)
	imgRE     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	preRE     = regexp.MustCompile(`(?is)<pre\b[^>]*>(.*?)</pre>`)
	breakRE   = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|h[1-6]|blockquote|tr|td|th|details|summary|aside|ul|ol)>`)
	itemRE    = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	quoteInRE = regexp.MustCompile(`(?i)<blockquote\b[^>]*>`)
	blankRE   = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)
)

// postText is a post's cooked HTML as segments: paragraphs and lists as text, code blocks kept as they are, a picture
// as the localized image placeholder [图片] (emojis and avatars dropped), a link preview as its link.
func postText(cooked string) []segment {
	s := dropRE.ReplaceAllString(cooked, "")
	s = oneboxRE.ReplaceAllStringFunc(s, func(m string) string {
		if sub := srcRE.FindStringSubmatch(m); sub != nil && sub[1] != "" {
			return "\n[链接] " + html.UnescapeString(sub[1]) + "\n"
		}
		return "\n[链接预览]\n"
	})
	s = emojiRE.ReplaceAllString(s, "")
	s = imgRE.ReplaceAllString(s, "[图片]")
	var out []segment
	add := func(code bool, text string) {
		if strings.TrimSpace(text) != "" {
			out = append(out, segment{Code: code, Text: text})
		}
	}
	last := 0
	for _, m := range preRE.FindAllStringSubmatchIndex(s, -1) {
		add(false, prose(s[last:m[0]]))
		add(true, strings.Trim(html.UnescapeString(tagRE.ReplaceAllString(s[m[2]:m[3]], "")), "\n"))
		last = m[1]
	}
	add(false, prose(s[last:]))
	return out
}

func prose(s string) string {
	s = itemRE.ReplaceAllString(s, "\n• ")
	s = quoteInRE.ReplaceAllString(s, "\n> ")
	s = breakRE.ReplaceAllString(s, "\n")
	s = html.UnescapeString(tagRE.ReplaceAllString(s, ""))
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaceRE.ReplaceAllString(l, " "))
	}
	s = strings.Join(lines, "\n")
	s = blankRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// plainText is a post as one text (for the tools and the replies in the page).
func plainText(cooked string) string {
	var parts []string
	for _, sg := range postText(cooked) {
		parts = append(parts, sg.Text)
	}
	return strings.Join(parts, "\n")
}
