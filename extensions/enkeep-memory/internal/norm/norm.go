// Package norm holds the text rules every check in the curator shares: how a quote is compared with the message
// it cites, how a knowledge-base message line splits into time, speaker and body, and how text is sized in tokens.
//
// The comparison is deliberately forgiving about form and strict about content: width, case, spaces, punctuation
// and invisible characters are ignored, but every remaining character of the quote must appear, in order, in the
// one line it cites.
package norm

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Q is the canonical form for comparing a quote with its source: NFKC, lower case, with white space, punctuation,
// symbols and zero-width characters removed.
func Q(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r): // zero-width characters, BOM and other invisible format characters
		case unicode.IsSpace(r), unicode.IsPunct(r), unicode.IsSymbol(r), unicode.IsControl(r):
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// ellipsis splits a quote that skips part of its line ("A……B", "A...B", "A…B").
var ellipsis = regexp.MustCompile(`……|…|\.\.\.+|\[\.\.\.\]`)

// MinFragment is the shortest fragment (in canonical runes) a quote may consist of, unless the fragment is the
// whole line: a two-character quote matches almost anything.
const MinFragment = 4

// Match says whether quote is in line: every fragment of the quote (split at an ellipsis) is in the canonical
// line, in order, without overlapping. A fragment shorter than MinFragment counts only when it is the whole line.
// partial is true when the quote used an ellipsis.
func Match(quote, line string) (ok, partial bool) {
	frags := ellipsis.Split(quote, -1)
	canon := Q(line)
	at := 0
	used := 0
	for _, f := range frags {
		qf := Q(f)
		if qf == "" {
			continue
		}
		if utf8.RuneCountInString(qf) < MinFragment && qf != canon {
			return false, false
		}
		i := strings.Index(canon[at:], qf)
		if i < 0 {
			return false, false
		}
		at += i + len(qf)
		used++
	}
	return used > 0, used > 1
}

// Line is a knowledge-base message line ("14:03 Alice: text", as kb.day gives it without the leading "- ").
type Line struct {
	N       int
	Time    string
	Speaker string
	Body    string
}

var lineRe = regexp.MustCompile(`^(\d{1,2}:\d{2}) ([^:\n]{1,80}?): ?(?s)(.*)$`)

// Parse splits a message line. A line in another shape keeps its text as the body, with no speaker.
func Parse(n int, text string) Line {
	if m := lineRe.FindStringSubmatch(text); m != nil {
		return Line{N: n, Time: m[1], Speaker: strings.TrimSpace(m[2]), Body: m[3]}
	}
	return Line{N: n, Body: text}
}

// Me is the speaker name the knowledge base writes for the owner's own messages.
const Me = "我"

// Tokens estimates the model tokens of a text: about 1.1 a CJK character and one per 3.5 other characters, the
// conservative estimate for mixed Chinese and English text.
func Tokens(s string) int {
	han, other := 0, 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			han++
		} else {
			other++
		}
	}
	return (han*11)/10 + (other*2)/7
}

// Contains says whether the canonical forms of s and sub contain one another (s ⊇ sub), for names in text.
func Contains(s, sub string) bool {
	q := Q(sub)
	return q != "" && strings.Contains(Q(s), q)
}

// Jaccard is the similarity of two texts' character bigrams (canonical form), 0–1.
func Jaccard(a, b string) float64 {
	ba, bb := bigrams(Q(a)), bigrams(Q(b))
	if len(ba) == 0 || len(bb) == 0 {
		if Q(a) == Q(b) {
			return 1
		}
		return 0
	}
	inter := 0
	for k := range ba {
		if bb[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(ba)+len(bb)-inter)
}

func bigrams(s string) map[string]bool {
	rs := []rune(s)
	out := map[string]bool{}
	for i := 0; i+1 < len(rs); i++ {
		out[string(rs[i:i+2])] = true
	}
	if len(rs) == 1 {
		out[s] = true
	}
	return out
}
