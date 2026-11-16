package verify

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// DetailNotInEvidence: the claim names something literal (a name in Latin letters, a number, a time of day) that
// neither the lines it cites nor any other line the model was shown of that unit has. The model took it from a line it did not cite,
// or made it up; either way the entry would state more than its evidence shows.
const DetailNotInEvidence = "DETAIL_NOT_IN_EVIDENCE"

var (
	detailWord = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_.\-]*[A-Za-z0-9]`)
	detailNum  = regexp.MustCompile(`\d{1,2}:\d{2}|\d+(?:\.\d+)?`)
)

// Details are the literal pieces of a claim a citation has to carry: words in Latin letters of three or more
// characters (names of projects, tools, settings, models), but not the common words of software work a claim uses
// for a quote's own wording (common); times of day and numbers (a quote's Chinese numerals count: "两个" carries
// "2"). Dates are the date check's business. CJK descriptions are not checked word by word: a claim rewords them
// (tried: most of what a clause check refused was a correct fact in other words).
func Details(claim string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		k := strings.ToLower(d)
		if !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	for _, w := range detailWord.FindAllString(claim, -1) {
		if utf8.RuneCountInString(w) >= 3 && !common[strings.ToLower(w)] {
			add(w)
		}
	}
	rest := detailWord.ReplaceAllString(claim, " ")
	for _, loc := range detailNum.FindAllStringIndex(rest, -1) {
		n := rest[loc[0]:loc[1]]
		// Part of a date (2026-11-08, 11月8日, 11/08) is not a detail of its own.
		if loc[0] > 0 && strings.ContainsAny(rest[loc[0]-1:loc[0]], "-/") ||
			loc[1] < len(rest) && (strings.HasPrefix(rest[loc[1]:], "-") || strings.HasPrefix(rest[loc[1]:], "/") ||
				strings.HasPrefix(rest[loc[1]:], "月") || strings.HasPrefix(rest[loc[1]:], "年") || strings.HasPrefix(rest[loc[1]:], "日") ||
				strings.HasPrefix(rest[loc[1]:], "号")) {
			continue
		}
		add(n)
	}
	return out
}

func carries(text, detail string) bool {
	t := strings.ToLower(text)
	if strings.Contains(t, strings.ToLower(detail)) {
		return true
	}
	return detailNum.MatchString(detail) && strings.Contains(numerals.Replace(t), detail)
}

// numerals turns Chinese numerals one to nine into digits, for matching a claim's "2" against a quote's "两".
var numerals = strings.NewReplacer("一", "1", "二", "2", "两", "2", "三", "3", "四", "4", "五", "5", "六", "6", "七", "7", "八", "8", "九", "9")

// common are words of software work that a claim may use for the quote's own wording ("Issue #12" for "#12 关了").
var common = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`issue issues release releases bug bugs commit commits merge merged branch master main
		session sessions bot bots agent agents token tokens api apis url urls http https ssh pr prs mr mrs repo
		macos ios android mac app apps web cli docs readme json yaml log logs config test tests ci cd node hub
		server client bash shell git github`) {
		common[w] = true
	}
}

// Supply checks that every detail of the claim is in a cited line. A detail that is only in another shown line of
// the window adds the nearest such line to the evidence, the way a person checking would cite it; a detail the unit
// does not have anywhere (no shown line, not one of the names it goes by) rejects the claim.
// known are names the claim may use without a line spelling them (the person an entry is about).
func (w *Window) Supply(claim string, ev []store.Evidence, known ...string) ([]store.Evidence, string) {
	if len(ev) == 0 {
		return ev, ""
	}
	cited := map[int]bool{}
	for _, e := range ev {
		cited[e.N] = true
	}
	var near []int // every other shown line of the window, nearest first
	for n := range w.Shown {
		if !cited[n] {
			near = append(near, n)
		}
	}
	dist := func(n int) int {
		best := -1
		for c := range cited {
			d := n - c
			if d < 0 {
				d = -d
			}
			if best < 0 || d < best {
				best = d
			}
		}
		return best
	}
	sort.Slice(near, func(i, j int) bool {
		if di, dj := dist(near[i]), dist(near[j]); di != dj {
			return di < dj
		}
		return near[i] < near[j]
	})
	for _, d := range Details(claim) {
		if quoted(ev, d) || slicesCarry(known, d) || slicesCarry(w.Names, d) || w.ownerCarries(d) {
			continue
		}
		if e, ok := w.inCitedLine(ev, d); ok {
			// In a cited line, outside the part quoted: the part of the line around it is cited too, so whoever
			// checks the entry sees it.
			ev = append(ev, e)
			continue
		}
		found := false
		for _, n := range near {
			l := w.All[n]
			if r := voice.Role(l, w.Kind); r == voice.RoleAssistant || r == voice.RoleAgentDraft || !carries(l.Body, d) {
				continue // the AI's text is context, never what a fact or a card rests on
			}
			quote := strings.TrimSpace(l.Body)
			if utf8.RuneCountInString(quote) > MaxQuote {
				quote = string([]rune(quote)[:MaxQuote])
			}
			ev = append(ev, store.Evidence{Chat: w.Chat, Day: w.Day, N: n, Speaker: l.Speaker, Role: voice.Role(l, w.Kind), Quote: quote})
			found = true
			break
		}
		if !found {
			w.Missing = d
			return nil, DetailNotInEvidence
		}
	}
	return ev, ""
}

func slicesCarry(xs []string, d string) bool {
	for _, x := range xs {
		if carries(x, d) {
			return true
		}
	}
	return false
}

func (w *Window) ownerCarries(d string) bool { return slicesCarry(w.Owner, d) }

// quoted: a cited quote, or the name of the one who said it, carries the detail.
func quoted(ev []store.Evidence, d string) bool {
	for _, e := range ev {
		if carries(e.Quote, d) || carries(e.Speaker, d) {
			return true
		}
	}
	return false
}

// inCitedLine finds the detail in the rest of a cited line and gives that line's part around it as evidence.
func (w *Window) inCitedLine(ev []store.Evidence, d string) (store.Evidence, bool) {
	for _, e := range ev {
		l, ok := w.All[e.N]
		if !ok || e.Chat != w.Chat || !carries(l.Body, d) {
			continue
		}
		e.Quote = around(l.Body, d, MaxQuote)
		return e, true
	}
	return store.Evidence{}, false
}

// around is the part of body, at most n runes, with the detail in the middle.
func around(body, d string, n int) string {
	r := []rune(body)
	if len(r) <= n {
		return strings.TrimSpace(body)
	}
	at := strings.Index(strings.ToLower(body), strings.ToLower(d))
	start := utf8.RuneCountInString(body[:max(at, 0)]) - (n-utf8.RuneCountInString(d))/2
	start = min(max(start, 0), len(r)-n)
	return strings.TrimSpace(string(r[start : start+n]))
}
