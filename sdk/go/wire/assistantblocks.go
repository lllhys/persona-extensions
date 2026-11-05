package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// HasAssistantView reports whether text mentions persona-view in any letter case, a cheap check before parsing fences.
func HasAssistantView(text string) bool { return hasPersonaHint(text, false) }

// hasPersonaHint matches what the regexps (?i)persona-view and, with card, (?i)persona-(view|card) match, without a
// regexp: case folding gives Go's regexp no literal prefix, so it stepped through every byte of a long reply. It finds
// each '-' with strings.IndexByte and checks the letters around it. Under (?i) Go folds by unicode.SimpleFold, and the
// only letter here with a non-ASCII fold is 's' (U+017F LATIN SMALL LETTER LONG S); TestPersonaHintMatchesRegexp pins
// the equivalence.
func hasPersonaHint(text string, card bool) bool {
	for at := 0; ; {
		n := strings.IndexByte(text[at:], '-')
		if n < 0 {
			return false
		}
		dash := at + n
		if personaBefore(text[:dash]) {
			rest := text[dash+1:]
			if len(rest) >= 4 && (equalFoldASCII(rest[:4], "view") || card && equalFoldASCII(rest[:4], "card")) {
				return true
			}
		}
		at = dash + 1
	}
}

// personaBefore reports whether s ends with "persona" in any case, the s also written as U+017F.
func personaBefore(s string) bool {
	if len(s) < 7 || !equalFoldASCII(s[len(s)-3:], "ona") {
		return false
	}
	s = s[:len(s)-3]
	switch {
	case s[len(s)-1]|0x20 == 's':
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "\u017f"):
		s = s[:len(s)-2]
	default:
		return false
	}
	return len(s) >= 3 && equalFoldASCII(s[len(s)-3:], "per")
}

// equalFoldASCII compares s with the lowercase ASCII letters in lower, ignoring case; s has the same length.
func equalFoldASCII(s, lower string) bool {
	for i := 0; i < len(lower); i++ {
		if s[i]|0x20 != lower[i] {
			return false
		}
	}
	return true
}

type AssistantViewBlock struct {
	Index    int
	Start    int
	End      int
	Language string
	Raw      json.RawMessage
	Complete bool
}

// AssistantViewBlocks recognizes standalone CommonMark fences, including tildes and longer outer fences.
// Fences inside another code block are examples, not executable cards. Invalid persona-view blocks still count.
func AssistantViewBlocks(text string) []AssistantViewBlock {
	var blocks []AssistantViewBlock
	var char byte
	length, start, content, index := 0, 0, 0, 0
	language := ""
	position := 0
	for position < len(text) {
		end := len(text)
		if n := strings.IndexAny(text[position:], "\r\n"); n >= 0 {
			end = position + n + 1
			if text[end-1] == '\r' && end < len(text) && text[end] == '\n' {
				end++
			}
		}
		line := text[position:end]
		c, n, info, ok := markdownFence(line)
		if char == 0 {
			if ok {
				char, length, language, start, content = c, n, info, position, position+len(line)
			}
		} else if ok && c == char && n >= length && info == "" {
			if language == "persona-view" || language == "persona-card" {
				ordinal := -1
				if language == "persona-view" {
					ordinal = index
					index++
				}
				blocks = append(blocks, AssistantViewBlock{Index: ordinal, Start: start, End: position + len(line), Language: language, Raw: json.RawMessage(text[content:position]), Complete: true})
			}
			char = 0
		}
		position += len(line)
	}
	if char != 0 && (language == "persona-view" || language == "persona-card") {
		ordinal := -1
		if language == "persona-view" {
			ordinal = index
		}
		blocks = append(blocks, AssistantViewBlock{Index: ordinal, Start: start, End: len(text), Language: language, Raw: json.RawMessage(text[content:]), Complete: false})
	}
	return blocks
}

func markdownFence(line string) (byte, int, string, bool) {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	spaces := len(line) - len(strings.TrimLeft(line, " "))
	if spaces > 3 {
		return 0, 0, "", false
	}
	line = line[spaces:]
	if len(line) < 3 || line[0] != '`' && line[0] != '~' {
		return 0, 0, "", false
	}
	char := line[0]
	n := 0
	for n < len(line) && line[n] == char {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	info := strings.TrimSpace(line[n:])
	if char == '`' && strings.Contains(info, "`") {
		return 0, 0, "", false
	}
	if fields := strings.Fields(info); len(fields) > 0 {
		info = fields[0]
	}
	return char, n, strings.ToLower(info), true
}

func AssistantBlockKey(itemID string, index int) string { return fmt.Sprintf("%s/%d", itemID, index) }

func (b AssistantViewBlock) Parse() (*View, error) {
	if b.Language != "persona-view" {
		return nil, errors.New("not a persona-view block")
	}
	if !b.Complete {
		return nil, errors.New("close the persona-view code fence")
	}
	if b.Index >= 3 {
		return nil, errors.New("at most three persona-view blocks per reply")
	}
	return ParseAssistantView(b.Raw)
}

// AssistantViewFallback recovers only a top-level text fallback, never HTML or raw JSON. A partial block may have
// finished writing its fallback before the rest became invalid; that text is safe to show without executing it.
func AssistantViewFallback(raw json.RawMessage) string {
	if len(raw) > AssistantViewMost {
		raw = raw[:AssistantViewMost]
	}
	if json.Valid(raw) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) == nil {
			if text, ok := assistantFallbackText(fields["fallback"]); ok {
				return text
			}
		}
		return "〔卡片〕"
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "〔卡片〕"
	}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return "〔卡片〕"
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "〔卡片〕"
		}
		if name == "fallback" {
			if text, ok := assistantFallbackText(value); ok {
				return text
			}
		}
	}
	return "〔卡片〕"
}

func assistantFallbackText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) == "" {
		return "", false
	}
	runes := []rune(text)
	if len(runes) > 2000 {
		text = string(runes[:2000])
	}
	return text, true
}

func AssistantCardText(markdown string) string {
	if !hasPersonaHint(markdown, true) {
		return markdown
	}
	blocks := AssistantViewBlocks(markdown)
	if len(blocks) == 0 {
		return markdown
	}
	var out strings.Builder
	at := 0
	for _, b := range blocks {
		out.WriteString(markdown[at:b.Start])
		fallback := AssistantViewFallback(b.Raw)
		if b.Language == "persona-card" {
			var old struct {
				Title string `json:"title"`
			}
			if len(b.Raw) <= AssistantViewMost && json.Unmarshal(b.Raw, &old) == nil && strings.TrimSpace(old.Title) != "" {
				fallback = old.Title
			}
		}
		out.WriteString(fallback)
		out.WriteByte('\n')
		at = b.End
	}
	out.WriteString(markdown[at:])
	return out.String()
}

func AssistantBlockErrors(text string) []string {
	var out []string
	for _, b := range AssistantViewBlocks(text) {
		if b.Language != "persona-view" {
			continue
		}
		if _, err := b.Parse(); err != nil {
			out = append(out, fmt.Sprintf("persona-view block %d: %v", b.Index, err))
		}
	}
	return out
}
