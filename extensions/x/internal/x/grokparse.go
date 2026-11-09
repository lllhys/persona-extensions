package x

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
)

// What grok answers is a model's text about third-party posts: untrusted. It is held to one JSON shape (the prompts
// ask for it), every post must have a real permalink (x.com/<handle>/status/<id>), its author is the permalink's,
// and every text is capped. A post without a permalink is dropped: it may be made up.

// GrokResult is one grok run's answer, checked.
type GrokResult struct {
	Summary string      `json:"summary,omitempty"`
	Topics  []GrokTopic `json:"topics,omitempty"`
	Posts   []GrokPost  `json:"posts,omitempty"`
}

// GrokTopic is a topic Grok found and the kept posts about it (their ids).
type GrokTopic struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary,omitempty"`
	PostIDs []string `json:"post_ids,omitempty"`
}

// GrokPost is a post Grok found: its permalink's id and handle, the text as Grok quoted it, why it matters.
type GrokPost struct {
	ID     string    `json:"id"`
	Author string    `json:"author"` // the handle, without @
	Name   string    `json:"name,omitempty"`
	Text   string    `json:"text,omitempty"`
	At     time.Time `json:"at,omitzero"`
	Why    string    `json:"why,omitempty"`
	Seen   time.Time `json:"seen,omitzero"`
}

// Permalink is the post on x.com.
func (p GrokPost) Permalink() string { return permalink(p.Author, p.ID) }

// Caps of what is kept of an answer.
const (
	grokPostsMost    = 15
	grokTopicsMost   = 10
	grokSummaryMost  = 4000
	grokTitleMost    = 200
	grokTopicMost    = 1000
	grokTextMost     = 10000
	grokNameMost     = 100
	grokWhyMost      = 300
	grokTopicPosts   = 10
	grokScanStarts   = 64 // '{' positions tried when looking for the object
	grokHandlePatStr = `^[A-Za-z0-9_]{1,15}$`
)

var (
	statusURL   = regexp.MustCompile(`^https://(?:www\.|mobile\.)?(?:x|twitter)\.com/([A-Za-z0-9_]{1,15})/status/(\d{1,20})$`)
	handlePat   = regexp.MustCompile(grokHandlePatStr)
	reservedURL = map[string]bool{"i": true, "home": true, "search": true, "intent": true, "share": true}
)

// statusOf is a post permalink's handle and id: twitter.com becomes x.com, a query or fragment is dropped.
func statusOf(raw string) (handle, id string, ok bool) {
	u := strings.TrimSpace(raw)
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimSuffix(u, "/")
	m := statusURL.FindStringSubmatch(u)
	if m == nil || reservedURL[strings.ToLower(m[1])] {
		return "", "", false
	}
	return m[1], m[2], true
}

// validHandle says whether h is an X handle (without @).
func validHandle(h string) bool { return handlePat.MatchString(h) }

// ParseGrok reads grok's answer: the outermost JSON object in text (in a code fence, after a sentence, …), held to
// the shape and the rules above. seen is when it was read.
func ParseGrok(text string, seen time.Time) (GrokResult, bool) {
	var raw struct {
		Summary any `json:"summary"`
		Topics  []struct {
			Title    any   `json:"title"`
			Summary  any   `json:"summary"`
			PostURLs []any `json:"post_urls"`
		} `json:"topics"`
		Posts []struct {
			URL  any `json:"url"`
			Name any `json:"name"`
			Text any `json:"text"`
			At   any `json:"at"`
			Why  any `json:"why"`
		} `json:"posts"`
	}
	if !firstObject(text, &raw) {
		return GrokResult{}, false
	}
	out := GrokResult{Summary: clip(strings.TrimSpace(str(raw.Summary)), grokSummaryMost)}
	kept := map[string]bool{}
	for _, p := range raw.Posts {
		if len(out.Posts) >= grokPostsMost {
			break
		}
		handle, id, ok := statusOf(str(p.URL))
		if !ok || kept[id] {
			continue
		}
		// The author is the permalink's, whatever Grok wrote as "author" (the two differ when it made one up).
		kept[id] = true
		out.Posts = append(out.Posts, GrokPost{ID: id, Author: handle, Name: clip(oneLine(str(p.Name)), grokNameMost),
			Text: clip(strings.TrimSpace(str(p.Text)), grokTextMost), At: parseAt(str(p.At)), Why: clip(oneLine(str(p.Why)), grokWhyMost), Seen: seen})
	}
	for _, t := range raw.Topics {
		if len(out.Topics) >= grokTopicsMost {
			break
		}
		title := clip(oneLine(str(t.Title)), grokTitleMost)
		if title == "" {
			continue
		}
		topic := GrokTopic{Title: title, Summary: clip(strings.TrimSpace(str(t.Summary)), grokTopicMost)}
		for _, u := range t.PostURLs {
			if _, id, ok := statusOf(str(u)); ok && kept[id] && len(topic.PostIDs) < grokTopicPosts && !slices.Contains(topic.PostIDs, id) {
				topic.PostIDs = append(topic.PostIDs, id)
			}
		}
		out.Topics = append(out.Topics, topic)
	}
	return out, true
}

// firstObject decodes into v the first JSON object in s that decodes: grok may wrap it in a fence or a sentence.
func firstObject(s string, v any) bool {
	from := 0
	for range grokScanStarts {
		i := strings.IndexByte(s[from:], '{')
		if i < 0 {
			return false
		}
		from += i
		dec := json.NewDecoder(strings.NewReader(s[from:]))
		var obj json.RawMessage
		if dec.Decode(&obj) == nil && json.Unmarshal(obj, v) == nil {
			return true
		}
		from++
	}
	return false
}

// str is a JSON value as text: a string as is, anything else "".
func str(v any) string {
	s, _ := v.(string)
	return s
}

// parseAt reads a post's time as Grok wrote it; zero when it cannot.
func parseAt(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
