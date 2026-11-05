package page

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// GitHub's Markdown (descriptions, comments, READMEs) and code drawn with the components there are: Markdown is held
// to the subset the hub takes (https links, no images, no HTML, no reference links), code goes in fenced blocks
// (```<language>, ```diff), cut to the markdown component's size. When hubs have code_view / diff_view
// (nwverse/persona#2951) these are what older apps get.

var (
	mdFenced   = regexp.MustCompile("(?s)(```|~~~).*?(```|~~~|$)|`[^`\n]*`")
	mdImg      = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)\s>]*)>?(?:\s+"[^"]*")?\s*\)`)
	mdLinkFull = regexp.MustCompile(`\[([^\]]*)\]\(\s*<?([^)\s>]*)>?(?:\s+"[^"]*")?\s*\)`)
	mdRefDef   = regexp.MustCompile(`(?m)^ {0,3}\[([^\]\n]+)\]:\s*<?(\S+?)>?(?:\s+.*)?$`)
	mdRefUse   = regexp.MustCompile(`\[([^\]\n]+)\]\[([^\]\n]*)\]`)
	mdRefImg   = regexp.MustCompile(`!\[([^\]\n]*)\]\[[^\]\n]*\]`)
	mdAuto     = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9+.-]*:[^<>\s]*)>`)
	htmlLink   = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*["']([^"']*)["'][^>]*>(.*?)</a>`)
	htmlImg    = regexp.MustCompile(`(?is)<img\s[^>]*>`)
	htmlAlt    = regexp.MustCompile(`(?is)\balt\s*=\s*["']([^"']*)["']`)
	htmlBreak  = regexp.MustCompile(`(?i)<br\s*/?>|</?p(\s[^>]*)?>|</?div(\s[^>]*)?>`)
	htmlTag    = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(\s[^<>]*)?/?>`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// cleanMarkdown holds text to the hub's Markdown: images become a link to them (or their words), relative links
// resolve against base (a directory's https URL; "" drops them to their words), links that are not https keep only
// their words, reference links become inline ones, HTML is taken out. Code is left as it is.
func cleanMarkdown(text, base string) string {
	text = dropComments(text)
	defs := map[string]string{}
	for _, m := range mdRefDef.FindAllStringSubmatch(text, -1) {
		defs[strings.ToLower(m[1])] = m[2]
	}
	var out strings.Builder
	last := 0
	for _, loc := range mdFenced.FindAllStringIndex(text, -1) {
		out.WriteString(cleanProse(text[last:loc[0]], base, defs))
		out.WriteString(text[loc[0]:loc[1]])
		last = loc[1]
	}
	out.WriteString(cleanProse(text[last:], base, defs))
	return strings.TrimSpace(blankLines.ReplaceAllString(out.String(), "\n\n"))
}

// dropComments takes out HTML comments outside code (GitHub shows none of them; templates leave them, code blocks in
// them too). An unclosed one runs to the end, as GitHub draws it.
func dropComments(text string) string {
	var out strings.Builder
	for {
		i := strings.Index(text, "<!--")
		if i < 0 {
			out.WriteString(text)
			return out.String()
		}
		if inCode(text[:i]) { // the <!-- is code: keep it, go on after it
			out.WriteString(text[:i+4])
			text = text[i+4:]
			continue
		}
		out.WriteString(text[:i])
		j := strings.Index(text[i:], "-->")
		if j < 0 {
			return out.String()
		}
		text = text[i+j+3:]
	}
}

// inCode says whether the end of before is inside a fenced block or an inline code span.
func inCode(before string) bool {
	fences := 0
	for _, line := range strings.Split(before, "\n") {
		t := strings.TrimLeft(line, " ")
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fences++
		}
	}
	if fences%2 == 1 {
		return true
	}
	last := before[strings.LastIndex(before, "\n")+1:]
	return strings.Count(last, "`")%2 == 1
}

func cleanProse(s, base string, defs map[string]string) string {
	s = mdRefDef.ReplaceAllString(s, "")
	s = htmlLink.ReplaceAllStringFunc(s, func(m string) string {
		g := htmlLink.FindStringSubmatch(m)
		words := strings.TrimSpace(htmlTag.ReplaceAllString(htmlImg.ReplaceAllStringFunc(g[2], imgAlt), ""))
		if words == "" {
			return ""
		}
		return "[" + words + "](" + g[1] + ")"
	})
	s = htmlImg.ReplaceAllStringFunc(s, imgAlt)
	s = htmlBreak.ReplaceAllString(s, "\n")
	s = htmlTag.ReplaceAllString(s, "")
	s = mdRefImg.ReplaceAllString(s, "$1")
	s = mdRefUse.ReplaceAllStringFunc(s, func(m string) string {
		g := mdRefUse.FindStringSubmatch(m)
		key := strings.ToLower(g[2])
		if key == "" {
			key = strings.ToLower(g[1])
		}
		if u, ok := defs[key]; ok {
			return "[" + g[1] + "](" + u + ")"
		}
		return m
	})
	s = mdImg.ReplaceAllStringFunc(s, func(m string) string {
		g := mdImg.FindStringSubmatch(m)
		alt := strings.TrimSpace(g[1])
		if u, ok := absolute(g[2], base); ok {
			return "[" + nonEmpty(alt, "图片") + "](" + u + ")"
		}
		return alt
	})
	// An image in a link (a badge) became a link in a link: keep the inner words.
	s = regexp.MustCompile(`\[\[([^\]]*)\]\([^)]*\)\]`).ReplaceAllString(s, "[$1]")
	s = mdLinkFull.ReplaceAllStringFunc(s, func(m string) string {
		g := mdLinkFull.FindStringSubmatch(m)
		if u, ok := absolute(g[2], base); ok {
			return "[" + g[1] + "](" + u + ")"
		}
		return g[1]
	})
	s = mdAuto.ReplaceAllStringFunc(s, func(m string) string {
		g := mdAuto.FindStringSubmatch(m)
		if u, ok := absolute(g[1], ""); ok {
			return "<" + u + ">"
		}
		return g[1]
	})
	return s
}

// imgAlt is an <img>'s alt text.
func imgAlt(tag string) string {
	if m := htmlAlt.FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

// absolute is target as an https URL: as it is, or resolved against base for a relative one.
func absolute(target, base string) (string, bool) {
	if target == "" || strings.HasPrefix(target, "#") {
		return "", false
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", false
	}
	if !u.IsAbs() {
		if base == "" || strings.HasPrefix(target, "//") {
			return "", false
		}
		b, err := url.Parse(strings.TrimSuffix(base, "/") + "/")
		if err != nil {
			return "", false
		}
		u = b.ResolveReference(u)
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", false
	}
	return u.String(), true
}

// markdownItem is text as a markdown component (cleaned, cut to most); an app before UI version 3 gets the text.
func markdownItem(r *ext.UIRequest, key, text, base string, most int) wire.Component {
	text = clip(cleanMarkdown(text, base), min(most, 20000))
	if text == "" {
		text = "（空）"
	}
	if r.UIVersion < 3 {
		return wire.Component{Type: wire.ComponentText, Key: key, Text: clip(text, 2000)}
	}
	return wire.Component{Type: wire.ComponentMarkdown, Key: key, Text: text}
}

// fileBody is a file's text: Markdown drawn, anything else as code.
func fileBody(r *ext.UIRequest, name, text, base string, src bool, most int) []wire.Component {
	if isMarkdown(name) && !src {
		return []wire.Component{markdownItem(r, "readme", text, base, most)}
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return codeBlocks(r, "readme", language(name), lines, 1)
}

// codeBlocks are lines of code as fenced blocks of chunkLines (and at most chunkMost characters) each.
func codeBlocks(r *ext.UIRequest, key, lang string, lines []string, first int) []wire.Component {
	var out []wire.Component
	for start := 0; start < len(lines); {
		end, n := start, 0
		for end < len(lines) && end-start < chunkLines && n+len(lines[end]) < chunkMost-200 {
			n += len(lines[end]) + 1
			end++
		}
		if end == start { // one line longer than a block
			lines[start] = clip(lines[start], chunkMost/4)
			end = start + 1
		}
		out = append(out, codeItem(r, fmt.Sprintf("%s-%d", key, first+start), lang, strings.Join(lines[start:end], "\n")))
		start = end
	}
	return out
}

// codeItem is a fenced block of code.
func codeItem(r *ext.UIRequest, key, lang, code string) wire.Component {
	if r.UIVersion < 3 {
		return wire.Component{Type: wire.ComponentText, Key: key, Text: clip(code, 2000), Style: wire.TextStyleSecondary}
	}
	return wire.Component{Type: wire.ComponentMarkdown, Key: key, Text: fence(lang, code)}
}

// fence is code in a fence longer than any run of backticks in it.
func fence(lang, code string) string {
	ticks := "```"
	for strings.Contains(code, ticks) {
		ticks += "`"
	}
	return ticks + lang + "\n" + code + "\n" + ticks
}

func isMarkdown(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdx":
		return true
	case "":
		return strings.HasPrefix(strings.ToLower(path.Base(name)), "readme")
	}
	return false
}

var languages = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "jsx", ".ts": "typescript",
	".tsx": "tsx", ".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".swift": "swift", ".rs": "rust", ".rb": "ruby", ".c": "c",
	".h": "c", ".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp", ".m": "objectivec", ".mm": "objectivec", ".php": "php",
	".sh": "bash", ".bash": "bash", ".zsh": "bash", ".yml": "yaml", ".yaml": "yaml", ".json": "json", ".toml": "toml", ".ini": "ini",
	".md": "markdown", ".html": "html", ".css": "css", ".scss": "scss", ".sql": "sql", ".xml": "xml", ".proto": "protobuf",
	".lua": "lua", ".dart": "dart", ".vue": "vue", ".svelte": "svelte", ".gradle": "groovy", ".scala": "scala", ".r": "r",
	".pl": "perl", ".ex": "elixir", ".exs": "elixir", ".erl": "erlang", ".hs": "haskell", ".tf": "hcl", ".mod": "go",
}

// language is a file's language for a fenced block, by its name.
func language(name string) string {
	base := strings.ToLower(path.Base(name))
	switch {
	case base == "dockerfile" || strings.HasPrefix(base, "dockerfile."):
		return "dockerfile"
	case base == "makefile" || base == "gnumakefile":
		return "makefile"
	}
	return languages[strings.ToLower(path.Ext(name))]
}

// fit makes v one the hub takes: a markdown component the hub would refuse (what cleanMarkdown missed) becomes its
// text in a code block, so one odd comment does not cost the whole page.
func fit(v *wire.View) *wire.View {
	if v == nil || wire.CheckView(v) == nil {
		return v
	}
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			it := &v.Sections[i].Items[j]
			if it.Type != wire.ComponentMarkdown {
				continue
			}
			one := &wire.View{Sections: []wire.Section{{Items: []wire.Component{*it}}}}
			if wire.CheckView(one) != nil {
				it.Text = fence("text", clip(it.Text, 19000))
			}
		}
	}
	return v
}

// clipBytes keeps s to n bytes, cut on a character.
func clipBytes(s string, n int) string {
	for len(s) > n {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
