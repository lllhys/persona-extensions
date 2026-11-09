package main

import (
	"errors"
	"strings"

	"golang.org/x/net/html"
)

// articleText turns untrusted feed HTML into bounded plain text, never executable markup.
// Script/style/form content is excluded even if it contains instructions to the assistant.
func articleText(source string) (string, error) {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", err
	}
	var blocks []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "form", "button", "nav", "footer", "iframe", "template":
				return
			}
		}
		if n.Type == html.TextNode {
			text := strings.Join(strings.Fields(n.Data), " ")
			if text != "" && len(blocks) < 600 {
				blocks = append(blocks, text)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && isBlock(n.Data) && len(blocks) > 0 {
			blocks = append(blocks, "\n")
		}
	}
	walk(root)
	var out strings.Builder
	line := strings.Builder{}
	for _, block := range blocks {
		if block == "\n" {
			if line.Len() > 0 {
				out.WriteString(line.String() + "\n\n")
				line.Reset()
			}
			continue
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(block)
	}
	if line.Len() > 0 {
		out.WriteString(line.String())
	}
	return clipText(strings.TrimSpace(out.String()), 60000), nil
}

func isBlock(tag string) bool {
	switch tag {
	case "article", "aside", "blockquote", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "main", "p", "pre", "section", "tr":
		return true
	}
	return false
}

func entryIDValid(id int64) error {
	if id <= 0 {
		return errors.New("entry ID must be positive")
	}
	return nil
}
