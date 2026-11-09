package main

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	modeTranslation = "translation"
	modeVocabulary  = "vocabulary"
)

// hit is one underlined occurrence. Repeated lemmas each produce a hit.
type hit struct {
	Surface string
	Lemma   string
	GlossZH string
	GlossEN string
}

// renderOption describes one pass over the original article HTML.
type renderOption struct {
	Mode      string
	Locale    string
	Saved     map[string]bool
	Open      string
	Lang      string // language tag written on a translation block
	Translate func(text string) (string, error)
	Failed    func(error) string
}

var wordRE = regexp.MustCompile(`[A-Za-z]+(?:'[A-Za-z]+)?`)

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "textarea": true, "input": true,
	"select": true, "option": true, "button": true, "nav": true, "footer": true, "header": true,
	"code": true, "pre": true, "svg": true, "canvas": true, "template": true, "iframe": true,
	"a": true,
}

var blockTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "li": true, "blockquote": true,
}

// apply renders src for the mode. It always starts by removing annotations this
// package inserted, so a second pass does not nest underlines or translations.
func apply(src string, opt renderOption) (string, []hit, error) {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return "", nil, err
	}
	for _, n := range nodes {
		stripAnnotations(n)
	}
	var hits []hit
	for _, n := range nodes {
		walk(n, &opt, &hits)
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			return "", nil, err
		}
	}
	return buf.String(), hits, nil
}

// blocks is the reading text of each paragraph or heading, with annotations ignored
// and skipped regions left out.
func blocks(src string) ([]string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range nodes {
		collectBlocks(n, &out)
	}
	return out, nil
}

func collectBlocks(n *html.Node, out *[]string) {
	if n.Type == html.ElementNode && skipElement(n) {
		return
	}
	if n.Type == html.ElementNode && blockTags[n.Data] {
		if text := strings.TrimSpace(textOf(n)); text != "" {
			*out = append(*out, text)
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectBlocks(c, out)
	}
}

func walk(n *html.Node, opt *renderOption, hits *[]hit) {
	if n.Type == html.ElementNode && skipElement(n) {
		return
	}
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	for _, c := range kids {
		if c.Type == html.TextNode && opt.Mode == modeVocabulary && n.Type == html.ElementNode && !skipElement(n) {
			replaceText(n, c, opt, hits)
			continue
		}
		walk(c, opt, hits)
	}
	if opt.Mode == modeTranslation && n.Type == html.ElementNode && blockTags[n.Data] && !skipElement(n) {
		insertTranslation(n, opt)
	}
}

func replaceText(parent, text *html.Node, opt *renderOption, hits *[]hit) {
	parts := wordRE.FindAllStringIndex(text.Data, -1)
	if len(parts) == 0 {
		return
	}
	var nodes []*html.Node
	last := 0
	markedCurrent := false
	for _, existing := range *hits {
		if existing.Lemma == opt.Open {
			markedCurrent = true
			break
		}
	}
	for _, span := range parts {
		if span[0] > last {
			nodes = append(nodes, &html.Node{Type: html.TextNode, Data: text.Data[last:span[0]]})
		}
		surface := text.Data[span[0]:span[1]]
		saved := opt.Saved[strings.ToLower(surface)]
		lemma, g, ok := lexicon.lookup(surface, false)
		if ok {
			saved = saved || opt.Saved[lemma]
		}
		if !ok {
			if rememberLemma, rg, rok := lexicon.lookup(surface, true); rok && opt.Saved[rememberLemma] {
				lemma, g, ok = rememberLemma, rg, true
				saved = true
			}
		}
		if !ok {
			nodes = append(nodes, &html.Node{Type: html.TextNode, Data: surface})
			last = span[1]
			continue
		}
		class := "tw-term"
		if saved {
			class += " tw-saved"
		}
		current := !markedCurrent && lemma == opt.Open
		if current {
			class += " tw-current"
			markedCurrent = true
		}
		btn := &html.Node{Type: html.ElementNode, DataAtom: atom.Button, Data: "button", Attr: []html.Attribute{
			{Key: "type", Val: "submit"},
			{Key: "class", Val: class},
			{Key: "name", Val: "word"},
			{Key: "value", Val: lemma},
			{Key: "form", Val: "tw-open"},
			{Key: "data-lemma", Val: lemma},
			{Key: "aria-haspopup", Val: "dialog"},
			{Key: "aria-expanded", Val: boolAttr(current)},
		}}
		btn.AppendChild(&html.Node{Type: html.TextNode, Data: surface})
		nodes = append(nodes, btn)
		*hits = append(*hits, hit{Surface: surface, Lemma: lemma, GlossZH: g.ZH, GlossEN: g.EN})
		last = span[1]
	}
	if last < len(text.Data) {
		nodes = append(nodes, &html.Node{Type: html.TextNode, Data: text.Data[last:]})
	}
	for _, node := range nodes {
		parent.InsertBefore(node, text)
	}
	parent.RemoveChild(text)
}

func boolAttr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func insertTranslation(n *html.Node, opt *renderOption) {
	text := strings.TrimSpace(textOf(n))
	if !worthTranslation(text) || opt.Translate == nil {
		return
	}
	translated, err := opt.Translate(text)
	failed := err != nil || strings.TrimSpace(translated) == ""
	body := translated
	class := "tw-tr"
	if failed {
		class += " tw-tr-error"
		body = "translation failed"
		if opt.Failed != nil {
			body = opt.Failed(err)
		}
	}
	div := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div", Attr: []html.Attribute{
		{Key: "class", Val: class},
		{Key: "data-tw-ignore", Val: ""},
		{Key: "lang", Val: opt.Lang},
	}}
	div.AppendChild(&html.Node{Type: html.TextNode, Data: body})
	insertAfter(n, div)
}

func worthTranslation(text string) bool {
	if utf8.RuneCountInString(text) < 8 {
		return false
	}
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters >= 8
}

func insertAfter(n, add *html.Node) {
	if n.Parent == nil {
		return
	}
	if n.NextSibling != nil {
		n.Parent.InsertBefore(add, n.NextSibling)
		return
	}
	n.Parent.AppendChild(add)
}

func stripAnnotations(n *html.Node) {
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	for _, c := range kids {
		if c.Type != html.ElementNode {
			continue
		}
		switch {
		case hasClass(c, "tw-tr"):
			n.RemoveChild(c)
		case hasClass(c, "tw-term"):
			for c.FirstChild != nil {
				ch := c.FirstChild
				c.RemoveChild(ch)
				n.InsertBefore(ch, c)
			}
			n.RemoveChild(c)
		default:
			stripAnnotations(c)
		}
	}
}

func skipElement(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if skipTags[n.Data] || hasClass(n, "tw-ignore") || hasClass(n, "tw-tr") || hasClass(n, "tw-term") {
		return true
	}
	for _, attr := range n.Attr {
		switch attr.Key {
		case "data-tw-ignore":
			return true
		case "contenteditable":
			if !strings.EqualFold(attr.Val, "false") {
				return true
			}
		case "role":
			switch strings.ToLower(attr.Val) {
			case "navigation", "banner", "contentinfo":
				return true
			}
		}
	}
	return false
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode && skipElement(n) && !blockTags[n.Data] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, part := range strings.Fields(attr.Val) {
			if part == class {
				return true
			}
		}
	}
	return false
}

// placePopup puts the popup below the word, or above it when that would leave
// the viewport, and keeps it inside the viewport's horizontal edges.
// Coordinates are viewport CSS pixels; the returned top/left include scroll.
func placePopup(anchorX, anchorY, anchorW, anchorH, popW, popH, viewW, viewH, scrollX, scrollY float64) (top, left float64) {
	top = anchorY + anchorH + 8
	left = anchorX
	if top+popH > viewH-8 {
		top = anchorY - popH - 8
		if top < 8 {
			top = 8
		}
	}
	if left+popW > viewW-8 {
		left = viewW - popW - 8
		if left < 8 {
			left = 8
		}
	}
	if left < 8 {
		left = 8
	}
	return top + scrollY, left + scrollX
}

func queryWord(lemma string) string {
	return "?word=" + url.QueryEscape(lemma)
}
