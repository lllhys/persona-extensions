package export

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
)

type line struct {
	id   string
	text string
}

func (l line) String() string { return "- " + l.text + "\n" }

// fill takes lines in order while they fit in budget bytes (a heading included), and says what it used.
func fill(heading string, ls []line, budget int) (string, []string) {
	if len(ls) == 0 {
		return "", nil
	}
	var b strings.Builder
	var ids []string
	b.WriteString(heading + "\n")
	for _, l := range ls {
		s := l.String()
		if b.Len()+len(s) > budget {
			continue // a shorter line further down may still fit
		}
		b.WriteString(s)
		if l.id != "" {
			ids = append(ids, l.id)
		}
	}
	if b.Len() == len(heading)+1 {
		return "", nil // nothing fitted: the section is left out, never a heading alone
	}
	return b.String(), ids
}

type part struct {
	key, heading string
	lines        []line
}

// fillParts shares a budget between parts: each non-empty part first gets an equal floor (no facet crowds out
// another), then what is left goes to the parts that still have lines, in order.
func fillParts(head string, parts []part, budget int) (string, []string) {
	var live []part
	for _, p := range parts {
		if len(p.lines) > 0 {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		return "", nil
	}
	avail := budget - len(head) - 1
	floor := avail / len(live)
	taken := make([]int, len(live)) // lines taken per part
	size := make([]int, len(live))
	for i, p := range live {
		size[i] = len(p.heading) + 1
		for _, l := range p.lines {
			if size[i]+len(l.String()) > floor {
				break
			}
			size[i] += len(l.String())
			taken[i]++
		}
	}
	total := 0
	for _, s := range size {
		total += s
	}
	for i, p := range live { // leftover, in order
		for taken[i] < len(p.lines) && total+len(p.lines[taken[i]].String()) <= avail {
			total += len(p.lines[taken[i]].String())
			size[i] += len(p.lines[taken[i]].String())
			taken[i]++
		}
	}
	var b strings.Builder
	var ids []string
	b.WriteString(head + "\n")
	for i, p := range live {
		if taken[i] == 0 {
			continue
		}
		b.WriteString("\n" + p.heading + "\n")
		for _, l := range p.lines[:taken[i]] {
			b.WriteString(l.String())
			if l.id != "" {
				ids = append(ids, l.id)
			}
		}
	}
	out := b.String()
	for len(out) > budget && len(ids) > 0 { // the blank lines between parts: trim the last line if they overflow
		i := strings.LastIndex(strings.TrimRight(out, "\n"), "\n- ")
		if i < 0 {
			break
		}
		out, ids = out[:i+1], ids[:len(ids)-1]
	}
	return out, ids
}

func (c *compiler) lines(es []*entry.Entry, texts map[string]string, single bool) []line {
	var out []line
	for _, e := range es {
		if t := texts[e.ID]; t != "" {
			if single && e.SingleSource {
				t += c.fw.SingleSrc
			}
			out = append(out, line{id: e.ID, text: oneLine(t)})
		}
	}
	return dedupe(out)
}

// dedupe drops a line whose text an earlier line already says (two entries rewritten to the same words).
func dedupe(ls []line) []line {
	seen := map[string]bool{}
	out := ls[:0]
	for _, l := range ls {
		if !seen[l.text] {
			seen[l.text] = true
			out = append(out, l)
		}
	}
	return out
}

// commitmentLines are open commitments as titles with their due dates (no rewrite: the title is the promise).
func (c *compiler) commitmentLines(es []*entry.Entry) []line {
	var out []line
	for _, e := range es {
		if e.Commitment == nil {
			continue
		}
		t := oneLine(firstNonEmpty(e.Commitment.What, e.Shown()))
		if len(Check(t, c.o.Lang)) > 0 {
			continue
		}
		t = cutRunes(t, 80)
		if e.Commitment.Due != "" {
			t += fmt.Sprintf(c.fw.Due, e.Commitment.Due)
		}
		out = append(out, line{id: e.ID, text: t})
	}
	return out
}

func (c *compiler) render(p *plan, texts map[string]string) []file {
	var files []file
	// Exit A.
	files = append(files, c.instructions(p, texts))
	// Exit B.
	files = append(files, c.skills(p, texts)...)
	// Exit D.
	slugs := make([]string, 0, len(p.projects))
	for s := range p.projects {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		pr := p.projects[slug]
		var parts []part
		for _, f := range []string{"tools", "judgement", "workflow", "world"} {
			var ls []line
			for _, e := range pr.parts[f] {
				if openCommitment(e) {
					ls = append(ls, c.commitmentLines([]*entry.Entry{e})...)
				} else {
					ls = append(ls, c.lines([]*entry.Entry{e}, texts, f == "world")...)
				}
			}
			parts = append(parts, part{key: f, heading: c.fw.ProjParts[f], lines: ls})
		}
		content, ids := fillParts(fmt.Sprintf(c.fw.Project, pr.name), parts, ProjectMax)
		if content != "" {
			files = append(files, file{path: path.Join(ProjectsDir, slug+".md"), exit: "D", content: content, ids: ids})
		}
	}
	// Exit E.
	var world []line
	if len(p.contacts) > 0 {
		world = append(world, line{text: fmt.Sprintf(c.fw.Contacts, list(c.o.Lang, p.contacts))})
	}
	world = append(world, c.commitmentLines(p.inbox["world"])...)
	var people []line
	for _, e := range p.inbox["people"] {
		if t := texts[e.ID]; t != "" {
			people = append(people, line{id: e.ID, text: fmt.Sprintf(c.fw.With, c.name(e.Person)) + oneLine(t)})
		}
	}
	people = dedupe(people)
	parts := []part{
		{key: "world", heading: c.fw.InboxParts["world"], lines: world},
		{key: "people", heading: c.fw.InboxParts["people"], lines: people},
		{key: "persona", heading: c.fw.InboxParts["persona"], lines: c.lines(p.inbox["persona"], texts, false)},
		{key: "interaction", heading: c.fw.InboxParts["interaction"], lines: c.lines(p.inbox["interaction"], texts, false)},
	}
	content, ids := fillParts(strings.TrimSpace(strings.Split(c.fw.Title, "\n")[0]), parts, InboxMax)
	files = append(files, file{path: InboxContextPath, exit: "E", content: content, ids: ids})
	var nb strings.Builder
	for _, l := range c.never {
		if nb.Len()+len(l)+1 > NeverQuoteMax {
			break
		}
		nb.WriteString(l + "\n")
	}
	files = append(files, file{path: NeverQuotePath, exit: "E", content: nb.String()})
	return files
}

// instructions is exit A: sections in their order, each within its own budget (scaled down when the title and the
// fixed lines would push the whole past 6 KB), each ordered by count × source kinds; an empty section is left out.
func (c *compiler) instructions(p *plan, texts map[string]string) file {
	f := file{path: InstructionsPath, exit: "A"}
	if !c.on(entry.K1PerTurnBlock) {
		return f
	}
	sections := map[string][]line{
		"interaction": c.lines(p.a["interaction"], texts, false),
		"judgement":   c.lines(p.a["judgement"], texts, false),
		"workflow":    c.lines(p.a["workflow"], texts, false),
		"world":       c.commitmentLines(p.a["world"]),
		"persona":     c.lines(p.a["persona"], texts, false),
	}
	askHint := entry.Switch(c.st.Switches, entry.K2Judgement) != "off" && len(sections["judgement"]) > 0
	head := c.fw.Title + "\n"
	tail := "\n- " + c.fw.PeopleNote + "\n"
	if askHint {
		tail += "- " + c.fw.AskHint + "\n"
	}
	total := 0
	for _, s := range SectionOrder {
		total += SectionBudget[s]
	}
	avail := InstructionsMax - len(head) - len(tail) - len(SectionOrder) // a blank line before each section
	scale := 1.0
	if total > avail {
		scale = float64(avail) / float64(total)
	}
	var b strings.Builder
	var ids []string
	for _, s := range SectionOrder {
		budget := int(float64(SectionBudget[s]) * scale)
		var text string
		var got []string
		if s == "tools" {
			text = c.toolsIndex(p.objects, budget)
		} else {
			text, got = fill(c.fw.Sections[s], sections[s], budget)
		}
		if text == "" {
			continue
		}
		c.rep.Sections[s] = len(text)
		c.rep.SecLines[s] = len(got)
		b.WriteString("\n" + text)
		ids = append(ids, got...)
	}
	if b.Len() == 0 {
		return f // nothing to say: an empty file, no placeholder
	}
	f.content = head + b.String() + tail
	f.ids = ids
	return f
}

// toolsIndex is ②'s one line in exit A: where the details are, and which machines and tools they cover.
func (c *compiler) toolsIndex(objects []string, budget int) string {
	if len(objects) == 0 {
		return ""
	}
	heading := c.fw.Sections["tools"] + "\n"
	var out string
	for n := len(objects); n >= 1; n-- {
		out = heading + "- " + fmt.Sprintf(c.fw.ToolsIndex, list(c.o.Lang, objects[:n])) + "\n"
		if len(out) <= budget {
			return out
		}
	}
	return ""
}

// skills is exit B: the machines-and-tools skill (② by object) and one ways-of-working skill per kind of task (④),
// within 256 KB together (the least used lines go first when over).
func (c *compiler) skills(p *plan, texts map[string]string) []file {
	type skill struct {
		dir, desc, title string
		groups           []part
	}
	var ss []*skill
	if len(p.tools) > 0 {
		byObj := map[string][]*entry.Entry{} // by the object's name in lower case; shown as first written
		shown := map[string]string{}
		var order []string
		for _, e := range p.tools {
			o := objectOf(e)
			if o == "" {
				o = c.fw.General
			}
			k := strings.ToLower(o)
			if _, ok := byObj[k]; !ok {
				order, shown[k] = append(order, k), o
			}
			byObj[k] = append(byObj[k], e)
		}
		sk := &skill{dir: ToolsSkill, title: c.fw.ToolsSkill, desc: fmt.Sprintf(c.fw.ToolsDesc, list(c.o.Lang, capList(p.objects, 900)))}
		for _, k := range order {
			if ls := c.lines(byObj[k], texts, false); len(ls) > 0 {
				sk.groups = append(sk.groups, part{heading: "## " + shown[k], lines: ls})
			}
		}
		if len(sk.groups) > 0 {
			ss = append(ss, sk)
		}
	}
	types := make([]string, 0, len(p.work))
	for t := range p.work {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		name := t
		if name == "" {
			name = c.fw.General
		}
		ls := c.lines(p.work[t], texts, false)
		if len(ls) == 0 {
			continue
		}
		ss = append(ss, &skill{dir: WorkflowPrefix + Slug(name), title: fmt.Sprintf(c.fw.WorkTitle, name),
			desc: fmt.Sprintf(c.fw.WorkDesc, name), groups: []part{{lines: ls}}})
	}
	render := func(sk *skill) (string, []string) {
		desc, _ := json.Marshal(sk.desc) // a YAML double-quoted scalar
		var b strings.Builder
		var ids []string
		fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\n---\n\n%s\n", sk.dir, desc, sk.title)
		for _, g := range sk.groups {
			if len(g.lines) == 0 {
				continue
			}
			b.WriteString("\n")
			if g.heading != "" {
				b.WriteString(g.heading + "\n")
			}
			for _, l := range g.lines {
				b.WriteString(l.String())
				ids = append(ids, l.id)
			}
		}
		return b.String(), ids
	}
	for {
		total, biggest, at := 0, 0, -1
		for i, sk := range ss {
			s, _ := render(sk)
			total += len(s)
			if len(s) > biggest {
				biggest, at = len(s), i
			}
		}
		if total <= SkillsMax || at < 0 {
			break
		}
		g := ss[at].groups
		last := len(g) - 1
		for last >= 0 && len(g[last].lines) == 0 {
			last--
		}
		if last < 0 {
			break
		}
		g[last].lines = g[last].lines[:len(g[last].lines)-1]
	}
	var out []file
	for _, sk := range ss {
		s, ids := render(sk)
		if len(ids) == 0 {
			continue
		}
		out = append(out, file{path: path.Join(SkillsDir, sk.dir, "SKILL.md"), exit: "B", content: s, ids: ids})
	}
	return out
}

func capList(xs []string, bytes int) []string {
	n := 0
	for i, x := range xs {
		n += len(x) + 3
		if n > bytes {
			return xs[:i]
		}
	}
	return xs
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is a file-safe name: ASCII letters and digits with hyphens; a name with other characters keeps its ASCII part
// and a short hash, so two names never share a slug.
func Slug(name string) string {
	s := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	ascii := true
	for _, r := range name {
		if r > 127 {
			ascii = false
			break
		}
	}
	if ascii && s != "" {
		return cutRunes(s, 60)
	}
	h := sha1.Sum([]byte(name))
	tag := hex.EncodeToString(h[:])[:8]
	if s == "" {
		return tag
	}
	return cutRunes(s, 50) + "-" + tag
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
