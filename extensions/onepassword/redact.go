package main

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// hidden stands in for anything secret the extension removes from text.
const hidden = "[hidden]"

// minScrub is the shortest value a scrubber looks for: shorter ones ("yes", a two-digit PIN) would mangle ordinary
// words and can't be told apart from them.
const minScrub = 4

// scrubber replaces known secret values in text. It is built for one item's answer and dropped with it, so values
// are held only while that answer is turned into metadata.
type scrubber struct{ values []string }

func newScrubber(values []string) scrubber {
	var keep []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, part := range append([]string{v}, strings.Split(v, "\n")...) {
			part = strings.TrimSpace(part)
			if utf8.RuneCountInString(part) >= minScrub && !seen[part] {
				seen[part] = true
				keep = append(keep, part)
			}
		}
	}
	// Longest first, so a value that contains another is replaced whole.
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return scrubber{values: keep}
}

func (s scrubber) clean(text string) string {
	for _, v := range s.values {
		text = strings.ReplaceAll(text, v, hidden)
	}
	return text
}

// tokenShape is a 1Password service account token (ops_ and a long base64 body), wherever one turns up.
var tokenShape = regexp.MustCompile(`ops_[A-Za-z0-9+/=_\-.]{16,}`)

// redact cleans text that leaves the program (tool results, errors, status, pages): the service account token, the
// token's last characters on their own, and anything shaped like a token.
func redact(text, token string) string {
	if token = strings.TrimSpace(token); token != "" {
		text = strings.ReplaceAll(text, token, hidden)
		// The hub replaces any 6-character prefix or suffix of a secret in a settings check's words; do the same
		// everywhere, for messages that quote part of the token.
		if len(token) >= 12 {
			text = strings.ReplaceAll(text, token[len(token)-12:], hidden)
			text = strings.ReplaceAll(text, token[:12], hidden)
		}
	}
	return tokenShape.ReplaceAllString(text, hidden)
}

// clip cuts text to at most n runes.
func clip(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	r := []rune(text)
	return string(r[:n-1]) + "…"
}

// oneLine is an error's words as they may be shown: on one line, without the SDK's own prefix, at most n runes.
func oneLine(msg string, n int) string {
	msg = strings.Join(strings.Fields(msg), " ")
	msg = strings.TrimPrefix(msg, "error initializing client: ")
	return clip(msg, n)
}
