package wire

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

// benchReply is an 18 KB assistant reply with lists, dashes and CJK but no card: the common case on the hot path.
var benchReply = func() string {
	line := "- Updated server/internal/node/sessions.go so the read-only check runs first; 已经确认 go test 通过。\n"
	return strings.Repeat(line, 18*1024/len(line)+1)
}()

func BenchmarkAssistantCardText(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		AssistantCardText(benchReply)
	}
}

// TestPersonaHintMatchesRegexp checks hasPersonaHint against the regexps it replaced: every BMP rune in every letter
// position (so all of unicode.SimpleFold's orbits, U+017F included), and random mixes of the pieces.
func TestPersonaHintMatchesRegexp(t *testing.T) {
	view := regexp.MustCompile(`(?i)persona-view`)
	card := regexp.MustCompile(`(?i)persona-(view|card)`)
	check := func(s string) {
		t.Helper()
		if got, want := HasAssistantView(s), view.MatchString(s); got != want {
			t.Fatalf("HasAssistantView(%q) = %v, regexp %v", s, got, want)
		}
		if got, want := hasPersonaHint(s, true), card.MatchString(s); got != want {
			t.Fatalf("hasPersonaHint(%q, true) = %v, regexp %v", s, got, want)
		}
	}
	for _, word := range []string{"persona-view", "persona-card"} {
		letters := []rune(word)
		for i := range letters {
			for r := rune(0); r < 0x10000; r++ {
				swapped := append([]rune(nil), letters...)
				swapped[i] = r
				check(string(swapped))
			}
		}
	}
	pieces := []string{"p", "P", "e", "E", "r", "R", "s", "S", "ſ", "o", "O", "n", "N", "a", "A", "-", "--",
		"v", "V", "i", "I", "İ", "ı", "w", "W", "c", "C", "d", "D", "k", "K", "persona", "PERSONA",
		"perſona", "ona-", "view", "CARD", "\xc5", "\xbf", "\xff", " ", "\n", "界"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200000 {
		var b strings.Builder
		for range rng.IntN(12) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		check(b.String())
	}
	for _, s := range []string{"", "-", "persona-", "ersona-view", "persona-vie", "PeRſOnA-ViEw", "xpersona-cardx",
		"persona-Kard", "per\xc5\xbfona-card", "pers-ona-view", "persona--view"} {
		check(s)
	}
}

func TestAssistantCardTextKeepsCaseAndLongS(t *testing.T) {
	// The fence language is lowered with strings.ToLower, so upper case still converts; the hint must not skip it.
	text := "Done\n```PERSONA-CARD\n{\"title\":\"Build\"}\n```\nafter"
	if got := AssistantCardText(text); got != "Done\nBuild\nafter" {
		t.Fatalf("got %q", got)
	}
	// U+017F passes the hint like (?i) did, but ToLower keeps it, so it is no card and the text stays as is.
	if long := "```perſona-card\n{\"title\":\"Build\"}\n```\n"; AssistantCardText(long) != long {
		t.Fatalf("long s changed %q", AssistantCardText(long))
	}
}
