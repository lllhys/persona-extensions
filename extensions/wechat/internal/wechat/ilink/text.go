package ilink

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	cjk         = regexp.MustCompile(`[\x{2e80}-\x{9fff}\x{ac00}-\x{d7af}\x{f900}-\x{faff}]`)
	deepHeading = regexp.MustCompile(`(?m)^#{5,6}\s+`)
	strike      = regexp.MustCompile(`~~([^~]+)~~`)
	emphasis    = regexp.MustCompile(`(\*{1,3}|_{1,3})([^*_\n]+)(\*{1,3}|_{1,3})`)
	image       = regexp.MustCompile(`!\[[^\]\n]*\]\([^)\n]*\)`)
)

// FilterMarkdown removes what WeChat shows badly (images, strike-through, h5/h6, emphasis around CJK text other than
// bold), keeping code blocks and inline code as they are (openclaw-weixin's markdown filter).
func FilterMarkdown(md string) string {
	var b strings.Builder
	fenced := false
	for _, line := range strings.SplitAfter(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			b.WriteString(line)
			continue
		}
		if fenced {
			b.WriteString(line)
			continue
		}
		b.WriteString(filterInline(line))
	}
	return b.String()
}

// filterInline filters a line outside inline code spans.
func filterInline(line string) string {
	var b strings.Builder
	for line != "" {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			b.WriteString(filterPlain(line))
			break
		}
		b.WriteString(filterPlain(line[:start]))
		end := start
		for end < len(line) && line[end] == '`' {
			end++
		}
		marker := line[start:end]
		close := strings.Index(line[end:], marker)
		if close < 0 {
			b.WriteString(line[start:])
			break
		}
		close += end + len(marker)
		b.WriteString(line[start:close])
		line = line[close:]
	}
	return b.String()
}

func filterPlain(s string) string {
	s = image.ReplaceAllString(s, "")
	s = deepHeading.ReplaceAllString(s, "")
	s = strike.ReplaceAllString(s, "$1")
	return emphasis.ReplaceAllStringFunc(s, func(whole string) string {
		m := emphasis.FindStringSubmatch(whole)
		if m[1] != m[3] || !cjk.MatchString(m[2]) || len(m[1]) == 2 {
			return whole
		}
		return m[2]
	})
}

// Chunk splits text into pieces of at most max code points (MaxText when 0), preferring line and word breaks, and
// closes and reopens a code fence a split falls inside.
func Chunk(text string, max int) []string {
	if max <= 0 {
		max = MaxText
	}
	if text == "" {
		return nil
	}
	if max < 9 {
		return splitRaw([]rune(text), max)
	}
	var out []string
	open := false
	for _, raw := range splitRaw([]rune(text), max-8) {
		prefix := ""
		if open {
			prefix = "```\n"
		}
		if fences(raw)%2 == 1 {
			open = !open
		}
		suffix := ""
		if open {
			suffix = "\n```"
		}
		out = append(out, prefix+raw+suffix)
	}
	return out
}

func fences(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			n++
		}
	}
	return n
}

func splitRaw(p []rune, max int) []string {
	var out []string
	for off := 0; off < len(p); {
		end := min(off+max, len(p))
		if end < len(p) {
			// Do not split inside a run of backticks.
			for end > off && (p[end-1] == '`' || p[end] == '`') {
				end--
			}
			if end == off {
				end = min(off+max, len(p))
			}
			floor := off + max*6/10
			for c := end; c > floor; c-- {
				if unicode.IsSpace(p[c-1]) {
					end = c
					break
				}
			}
		}
		out = append(out, string(p[off:end]))
		off = end
	}
	return out
}
