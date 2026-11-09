package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Feeds in the three shapes RSSHub writes (RSS 2.0 by default, Atom with ?format=atom, JSON Feed with ?format=json)
// and RSS 1.0 (RDF), told apart by their content, not by the Content-Type a server sends.

const (
	summaryMost = 400  // characters of an entry's summary
	contentMost = 8000 // characters of an entry's text
)

// parsedFeed is a feed read from any of the formats.
type parsedFeed struct {
	Title   string
	Link    string
	Format  string // rss, atom, json, rdf
	Entries []parsedEntry
}

// parsedEntry is one item of a feed, with its text already plain.
type parsedEntry struct {
	Key       string // guid / id / link: what tells it apart from the feed's other entries
	Title     string
	Link      string
	Author    string
	Published time.Time // zero when the feed does not say
	Summary   string
	Content   string
	Tags      []string
}

var errNotFeed = errors.New("读到的不是 RSS、Atom 或 JSON Feed")

// parseFeed reads body as a feed of any supported format.
func parseFeed(body []byte) (*parsedFeed, error) {
	b := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	if len(b) == 0 {
		return nil, errNotFeed
	}
	var f *parsedFeed
	var err error
	if b[0] == '{' {
		f, err = parseJSONFeed(b)
	} else {
		f, err = parseXMLFeed(b)
	}
	if err != nil {
		return nil, err
	}
	f.Title = clip(oneLine(f.Title), 200)
	for i := range f.Entries {
		e := &f.Entries[i]
		if e.Key == "" {
			e.Key = e.Link
		}
		if e.Key == "" {
			e.Key = "h:" + hashText(e.Title+"\x00"+e.Published.String()+"\x00"+e.Summary)
		}
		if e.Title == "" {
			e.Title = clip(oneLine(firstNonEmpty(e.Summary, e.Content)), 120)
		}
		if e.Summary == "" {
			e.Summary = e.Content
		}
		e.Title = clip(oneLine(e.Title), 300)
		e.Summary = clip(oneLine(e.Summary), summaryMost)
		e.Content = clip(e.Content, contentMost)
		e.Author = clip(oneLine(e.Author), 120)
	}
	return f, nil
}

// --- XML: RSS 2.0, RSS 1.0 and Atom ---

type xmlText struct {
	Type  string `xml:"type,attr"`
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

// html is the element's text as HTML: xhtml content is its inner markup, the rest its character data.
func (t xmlText) html() string {
	if strings.EqualFold(t.Type, "xhtml") {
		return t.Inner
	}
	return t.Text
}

type xmlLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

type xmlPerson struct {
	Name  string `xml:"name"`
	Email string `xml:"email"`
}

type xmlCategory struct {
	Term string `xml:"term,attr"`
	Text string `xml:",chardata"`
}

type rssItem struct {
	Title       string        `xml:"title"`
	Links       []xmlLink     `xml:"link"`
	GUID        string        `xml:"guid"`
	PubDate     string        `xml:"pubDate"`
	DCDate      string        `xml:"http://purl.org/dc/elements/1.1/ date"`
	Author      string        `xml:"author"`
	Creator     string        `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Description string        `xml:"description"`
	Encoded     string        `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Categories  []xmlCategory `xml:"category"`
	About       string        `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# about,attr"`
}

type atomEntry struct {
	ID         string        `xml:"id"`
	Title      xmlText       `xml:"title"`
	Links      []xmlLink     `xml:"link"`
	Published  string        `xml:"published"`
	Updated    string        `xml:"updated"`
	Authors    []xmlPerson   `xml:"author"`
	Summary    xmlText       `xml:"summary"`
	Content    xmlText       `xml:"content"`
	Categories []xmlCategory `xml:"category"`
}

type xmlFeed struct {
	XMLName xml.Name
	// RSS 2.0
	Channel struct {
		Title string    `xml:"title"`
		Links []xmlLink `xml:"link"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
	// RSS 1.0 keeps its items beside the channel
	Items []rssItem `xml:"item"`
	// Atom
	Title   xmlText     `xml:"title"`
	Links   []xmlLink   `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

func parseXMLFeed(b []byte) (*parsedFeed, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charsetReader
	var x xmlFeed
	if err := d.Decode(&x); err != nil {
		return nil, fmt.Errorf("%w（%v）", errNotFeed, err)
	}
	switch strings.ToLower(x.XMLName.Local) {
	case "rss":
		f := &parsedFeed{Title: x.Channel.Title, Link: rssLink(x.Channel.Links), Format: "rss"}
		for _, it := range x.Channel.Items {
			f.Entries = append(f.Entries, it.entry())
		}
		return f, nil
	case "rdf":
		f := &parsedFeed{Title: x.Channel.Title, Link: rssLink(x.Channel.Links), Format: "rdf"}
		for _, it := range x.Items {
			f.Entries = append(f.Entries, it.entry())
		}
		return f, nil
	case "feed":
		f := &parsedFeed{Title: htmlText(x.Title.html()), Link: atomLink(x.Links), Format: "atom"}
		for _, e := range x.Entries {
			f.Entries = append(f.Entries, e.entry())
		}
		return f, nil
	}
	return nil, errNotFeed
}

// charsetReader reads the encodings a feed's XML declaration may name that the standard library can turn into UTF-8
// by itself; others are refused (RSSHub always writes UTF-8).
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "iso-8859-1", "latin1", "latin-1", "windows-1252":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return strings.NewReader(string(r)), nil
	}
	return nil, fmt.Errorf("编码 %s 读不了", label)
}

func rssLink(ls []xmlLink) string {
	for _, l := range ls {
		if t := strings.TrimSpace(l.Text); t != "" && l.Href == "" {
			return t
		}
	}
	return atomLink(ls)
}

func atomLink(ls []xmlLink) string {
	best := ""
	for _, l := range ls {
		href := strings.TrimSpace(l.Href)
		if href == "" {
			continue
		}
		if l.Rel == "" || l.Rel == "alternate" {
			return href
		}
		if best == "" && l.Rel != "self" {
			best = href
		}
	}
	return best
}

func (it rssItem) entry() parsedEntry {
	e := parsedEntry{
		Key:       strings.TrimSpace(firstNonEmpty(it.GUID, it.About)),
		Title:     htmlText(it.Title),
		Link:      rssLink(it.Links),
		Author:    firstNonEmpty(strings.TrimSpace(it.Creator), strings.TrimSpace(it.Author)),
		Published: parseTime(firstNonEmpty(it.PubDate, it.DCDate)),
		Summary:   htmlText(it.Description),
		Content:   htmlText(firstNonEmpty(it.Encoded, it.Description)),
	}
	for _, c := range it.Categories {
		if t := strings.TrimSpace(firstNonEmpty(c.Text, c.Term)); t != "" {
			e.Tags = append(e.Tags, t)
		}
	}
	return e
}

func (a atomEntry) entry() parsedEntry {
	e := parsedEntry{
		Key:       strings.TrimSpace(a.ID),
		Title:     htmlText(a.Title.html()),
		Link:      atomLink(a.Links),
		Published: parseTime(firstNonEmpty(a.Published, a.Updated)),
		Summary:   htmlText(a.Summary.html()),
		Content:   htmlText(firstNonEmpty(a.Content.html(), a.Summary.html())),
	}
	var names []string
	for _, p := range a.Authors {
		if n := strings.TrimSpace(firstNonEmpty(p.Name, p.Email)); n != "" {
			names = append(names, n)
		}
	}
	e.Author = strings.Join(names, ", ")
	for _, c := range a.Categories {
		if t := strings.TrimSpace(firstNonEmpty(c.Term, c.Text)); t != "" {
			e.Tags = append(e.Tags, t)
		}
	}
	return e
}

// --- JSON Feed 1.0 / 1.1 ---

type jsonAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type jsonItem struct {
	ID            json.RawMessage `json:"id"`
	URL           string          `json:"url"`
	ExternalURL   string          `json:"external_url"`
	Title         string          `json:"title"`
	ContentHTML   string          `json:"content_html"`
	ContentText   string          `json:"content_text"`
	Summary       string          `json:"summary"`
	DatePublished string          `json:"date_published"`
	DateModified  string          `json:"date_modified"`
	Author        *jsonAuthor     `json:"author"`
	Authors       []jsonAuthor    `json:"authors"`
	Tags          []string        `json:"tags"`
}

type jsonFeed struct {
	Version     string     `json:"version"`
	Title       string     `json:"title"`
	HomePageURL string     `json:"home_page_url"`
	FeedURL     string     `json:"feed_url"`
	Items       []jsonItem `json:"items"`
}

func parseJSONFeed(b []byte) (*parsedFeed, error) {
	var j jsonFeed
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("%w（%v）", errNotFeed, err)
	}
	if !strings.HasPrefix(j.Version, "https://jsonfeed.org/version/") {
		return nil, errNotFeed
	}
	f := &parsedFeed{Title: j.Title, Link: firstNonEmpty(j.HomePageURL, j.FeedURL), Format: "json"}
	for _, it := range j.Items {
		e := parsedEntry{
			Key:       jsonID(it.ID),
			Title:     htmlText(it.Title),
			Link:      strings.TrimSpace(firstNonEmpty(it.URL, it.ExternalURL)),
			Published: parseTime(firstNonEmpty(it.DatePublished, it.DateModified)),
			Summary:   htmlText(it.Summary),
			Tags:      it.Tags,
		}
		if it.ContentHTML != "" {
			e.Content = htmlText(it.ContentHTML)
		} else {
			e.Content = strings.TrimSpace(it.ContentText)
		}
		var names []string
		for _, a := range it.Authors {
			if n := strings.TrimSpace(a.Name); n != "" {
				names = append(names, n)
			}
		}
		if len(names) == 0 && it.Author != nil {
			names = append(names, strings.TrimSpace(it.Author.Name))
		}
		e.Author = strings.Join(names, ", ")
		f.Entries = append(f.Entries, e)
	}
	return f, nil
}

func jsonID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// --- dates ---

var timeLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC3339Nano, time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "Mon, 02 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006 15:04 MST", "2 Jan 2006 15:04:05 -0700", "02 Jan 2006 15:04:05 MST",
	"Mon, 2 January 2006 15:04:05 -0700", time.RFC822Z, time.RFC822,
	"2006-01-02T15:04:05", "2006-01-02T15:04:05.000Z0700", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05", "2006-01-02",
}

// parseTime reads a feed's date in the many forms feeds write it; an unreadable one is the zero time.
func parseTime(s string) time.Time {
	s = numericZone(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return time.Time{}
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// rfc822Zones are the zone names RFC 822 defines beside GMT and UT, as offsets. time.Parse knows a zone name only when
// the computer's own zone uses it and reads any other as UTC, so EDT would be four hours off on most hubs. CST is left
// to time.Parse: RFC 822 makes it US Central, but Chinese feeds write it for UTC+8.
var rfc822Zones = map[string]string{
	"EST": "-0500", "EDT": "-0400", "CDT": "-0500", "MST": "-0700", "MDT": "-0600", "PST": "-0800", "PDT": "-0700",
}

// numericZone is s with its trailing zone written as an offset when it is an RFC 822 zone name (EDT) or GMT or UTC
// with an offset (GMT+8, GMT+0800, UTC+08:00); any other s is returned as it is.
func numericZone(s string) string {
	i := strings.LastIndexByte(s, ' ')
	if i < 0 {
		return s
	}
	zone := strings.ToUpper(s[i+1:])
	if off, ok := rfc822Zones[zone]; ok {
		return s[:i+1] + off
	}
	for _, p := range []string{"GMT", "UTC"} {
		rest, ok := strings.CutPrefix(zone, p)
		if !ok || len(rest) < 2 || (rest[0] != '+' && rest[0] != '-') {
			continue
		}
		hh, mm, _ := strings.Cut(rest[1:], ":")
		if mm == "" && len(hh) > 2 {
			hh, mm = hh[:len(hh)-2], hh[len(hh)-2:]
		}
		h, herr := strconv.Atoi(hh)
		m, merr := strconv.Atoi(cmp.Or(mm, "0"))
		if herr != nil || merr != nil || len(hh) > 2 || len(mm) > 2 || h > 14 || m > 59 {
			return s
		}
		return fmt.Sprintf("%s%c%02d%02d", s[:i+1], rest[0], h, m)
	}
	return s
}

// --- HTML to text ---

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "ul": true, "ol": true, "tr": true, "table": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true, "pre": true,
	"section": true, "article": true, "figure": true, "figcaption": true, "hr": true, "dd": true, "dt": true,
}

var skipTags = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "iframe": true, "svg": true}

// htmlText turns a fragment of HTML (or plain text) into plain text: tags dropped, script and style left out, blocks
// on lines of their own, entities decoded, runs of spaces made one.
func htmlText(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return tidy(s)
	}
	var b strings.Builder
	skip := ""
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			if skip == "" {
				b.WriteString(s)
			}
			break
		}
		if skip == "" {
			b.WriteString(s[:i])
		}
		s = s[i:]
		if strings.HasPrefix(s, "<!--") {
			end := strings.Index(s, "-->")
			if end < 0 {
				break
			}
			s = s[end+3:]
			continue
		}
		if strings.HasPrefix(s, "<![CDATA[") {
			end := strings.Index(s, "]]>")
			if end < 0 {
				break
			}
			if skip == "" {
				b.WriteString(html.EscapeString(s[9:end]))
			}
			s = s[end+3:]
			continue
		}
		end := strings.IndexByte(s, '>')
		if end < 0 {
			if skip == "" {
				b.WriteString(s)
			}
			break
		}
		tag := s[1:end]
		s = s[end+1:]
		closing := strings.HasPrefix(tag, "/")
		name := strings.ToLower(strings.TrimLeft(tag, "/"))
		if j := strings.IndexFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == '/' }); j >= 0 {
			name = name[:j]
		}
		if skip != "" {
			if closing && name == skip {
				skip = ""
			}
			continue
		}
		if !closing && skipTags[name] && !strings.HasSuffix(tag, "/") {
			skip = name
			continue
		}
		if blockTags[name] {
			b.WriteString("\n")
		} else if name == "td" || name == "th" {
			b.WriteString(" ")
		} else if name == "img" && !closing {
			if alt := attr(tag, "alt"); alt != "" {
				b.WriteString("[" + alt + "]")
			}
		}
	}
	return tidy(html.UnescapeString(b.String()))
}

// attr reads one attribute's value from a tag's inside.
func attr(tag, name string) string {
	lower := strings.ToLower(tag)
	i := strings.Index(lower, " "+name+"=")
	if i < 0 {
		return ""
	}
	v := tag[i+len(name)+2:]
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return strings.TrimSpace(html.UnescapeString(v[1 : end+1]))
		}
		return ""
	}
	if end := strings.IndexFunc(v, unicode.IsSpace); end >= 0 {
		v = v[:end]
	}
	return strings.TrimSpace(html.UnescapeString(strings.TrimSuffix(v, "/")))
}

// tidy makes runs of spaces one, drops empty lines, and trims: one paragraph a line.
func tidy(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// --- small helpers ---

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip cuts s to at most n characters, with … when cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max(n-1, 0)])) + "…"
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
