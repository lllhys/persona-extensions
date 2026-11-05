package page

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// One file of a PR: its diff a hunk a section, each line with its number (the new file's; a removed line the old
// file's), and the conversations on its lines right under the line they are on; conversations on lines the PR no
// longer has at the end. Then a check run: what failed (the error lines of its log, its annotations, its steps) and
// the end of its log.

const (
	diffPage = 600             // lines of a diff per 「加载更多」
	logLines = 150             // lines of a failed log shown
	logWait  = 6 * time.Second // how long the check page waits for a log (the rest is read after: the page draws again)
)

type diffLine struct {
	kind     byte // '+', '-', ' '
	old, new int
	text     string
}

type hunk struct {
	header string
	lines  []diffLine
}

// parsePatch reads a unified diff's hunks (GitHub's patch of one file: no file headers).
func parsePatch(patch string) []hunk {
	var out []hunk
	var old, nw int
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if strings.HasPrefix(line, "@@") {
			var a, b int
			rest := line
			if i := strings.Index(line[2:], "@@"); i >= 0 {
				rest = line[2 : 2+i]
			}
			for _, f := range strings.Fields(rest) {
				n, _, _ := strings.Cut(f[1:], ",")
				v, _ := strconv.Atoi(n)
				switch f[0] {
				case '-':
					a = v
				case '+':
					b = v
				}
			}
			old, nw = a, b
			out = append(out, hunk{header: line})
			continue
		}
		if len(out) == 0 {
			continue
		}
		h := &out[len(out)-1]
		switch {
		case strings.HasPrefix(line, "+"):
			h.lines = append(h.lines, diffLine{'+', 0, nw, line[1:]})
			nw++
		case strings.HasPrefix(line, "-"):
			h.lines = append(h.lines, diffLine{'-', old, 0, line[1:]})
			old++
		case strings.HasPrefix(line, `\`): // "\ No newline at end of file"
		default:
			h.lines = append(h.lines, diffLine{' ', old, nw, strings.TrimPrefix(line, " ")})
			old++
			nw++
		}
	}
	return out
}

// number is the line's number as shown: the new file's, a removed line's the old file's.
func (l diffLine) number() int {
	if l.kind == '-' {
		return l.old
	}
	return l.new
}

func (l diffLine) String() string { return fmt.Sprintf("%c%5d │ %s", l.kind, l.number(), l.text) }

// on says whether a conversation is on this line (on its side).
func (l diffLine) on(c github.LineComment) bool {
	if c.Side == "LEFT" {
		return l.kind != '+' && l.old == c.Line
	}
	return l.kind != '-' && l.new == c.Line
}

// hasLine says whether a patch has a line one can comment on: line on side.
func hasLine(patch string, line int, side string) bool {
	for _, h := range parsePatch(patch) {
		for _, l := range h.lines {
			if l.on(github.LineComment{Line: line, Side: side}) {
				return true
			}
		}
	}
	return false
}

// diff is one file of a PR.
func (p *Page) diff(ctx context.Context, r *ext.UIRequest, s state) *wire.View {
	v := &wire.View{Title: path.Base(s.Path), State: ext.EncodeState(s), RefreshAfter: refreshAfter}
	f, err := p.c.PRFileAt(ctx, s.Repo, s.Number, s.Path)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty("读不到 "+s.Path, err)}}}
		return v
	}
	convs, _ := p.c.Conversations(ctx, s.Repo, s.Number)
	var here []github.Conversation
	for _, c := range convs {
		if c.Root().Path == s.Path {
			here = append(here, c)
		}
	}
	head := wire.Component{Type: wire.ComponentRow, Key: "file", Title: clip(s.Path, 300), Subtitle: fmt.Sprintf("+%d −%d · %s#%d", f.Adds, f.Dels, s.Repo, s.Number)}
	if fs, ok := fileStates[f.Status]; ok {
		head.Badge = &wire.Badge{Text: fs.name, Tone: fs.tone}
	}
	items := []wire.Component{head}
	if f.Previous != "" {
		items = append(items, wire.Component{Type: wire.ComponentRow, Key: "previous", Title: "原来的路径", Subtitle: clip(f.Previous, 300)})
	}
	if f.Patch != "" {
		items = append(items, wire.Component{Type: wire.ComponentButton, Key: "w-line", Title: "评论一行",
			Action: ext.Open("w:line", "", state{V: viewWrite, W: "line", Repo: s.Repo, Number: s.Number, Path: s.Path, Side: "RIGHT"}, "评论一行")})
	}
	v.Sections = append(v.Sections, wire.Section{Items: items})

	if f.Patch == "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "no-patch", Icon: wire.NavIconDoc,
			Title: "这里看不到改动", Detail: "二进制文件，或者改动太大、GitHub 不给出 diff；在 GitHub 上看"}}})
	}
	// A diff block is ```diff: the apps colour its + and - lines.
	placed := map[int64]bool{}
	budget, n := s.page()*diffPage, 0
	items0 := 0
	cut := false
	for hi, h := range parsePatch(f.Patch) {
		if n >= budget || items0 > wire.ItemsMost-40 {
			cut = true
			break
		}
		sec := wire.Section{Title: clip(h.header, 300)}
		var buf []string
		k := 0
		flush := func() {
			if len(buf) > 0 {
				sec.Items = append(sec.Items, codeBlocks(r, fmt.Sprintf("h%d-%d", hi, k), "diff", buf, 0)...)
				k++
				buf = nil
			}
		}
		for _, l := range h.lines {
			if n >= budget {
				cut = true
				break
			}
			buf = append(buf, l.String())
			n++
			for _, c := range here {
				if root := c.Root(); !placed[root.ID] && !c.Outdated && l.on(root) {
					placed[root.ID] = true
					flush()
					sec.Items = append(sec.Items, p.conversation(r, s, c)...)
				}
			}
		}
		flush()
		items0 += len(sec.Items)
		v.Sections = append(v.Sections, sec)
		if cut {
			break
		}
	}
	if cut {
		v.Sections[len(v.Sections)-1].More = &wire.More{Title: "加载更多改动", Action: wire.Action{ID: "more"}}
	}
	var rest []wire.Component
	for _, c := range here {
		if !placed[c.Root().ID] && len(rest) < wire.ItemsMost-items0-30 {
			root := c.Root()
			if hunk := lastLinesOf(root.DiffHunk, 4); hunk != "" {
				rest = append(rest, codeItem(r, fmt.Sprintf("old-%d", root.ID), "diff", hunk))
			}
			rest = append(rest, p.conversation(r, s, c)...)
		}
	}
	if len(rest) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: "不在现在的改动里的评论", Items: rest,
			Footer: "这些评论写在之前的提交上，后来那几行又改了"})
	}
	tail := []wire.Component{}
	if u, ok := httpsURL(f.BlobURL); ok {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 看这个文件", URL: u})
	}
	tail = append(tail, refreshButton())
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
}

// conversation is a conversation on a line: each comment (who, when, the words), whether it is resolved, a reply
// button.
func (p *Page) conversation(r *ext.UIRequest, s state, c github.Conversation) []wire.Component {
	root := c.Root()
	var out []wire.Component
	for i, cm := range c.Comments {
		key := fmt.Sprintf("t%d-%d", root.ID, i)
		sub := ago(p.now(), cm.At)
		if i == 0 {
			sub = fmt.Sprintf("第 %d 行 · %s", cmpLine(cm), sub)
		}
		row := wire.Component{Type: wire.ComponentRow, Key: key + "-who", Title: nonEmpty(cm.Author, "有人"), Subtitle: sub, Avatar: avatar(r, cm.Avatar)}
		if i == 0 {
			switch {
			case c.Resolved:
				row.Badge = &wire.Badge{Text: "已解决", Tone: wire.TonePositive}
			case c.Outdated:
				row.Badge = &wire.Badge{Text: "已过时", Tone: wire.ToneNeutral}
			default:
				row.Badge = &wire.Badge{Text: "待解决", Tone: wire.ToneCaution}
			}
		}
		out = append(out, row, markdownItem(r, key, cm.Body, "", noteMost))
	}
	out = append(out, wire.Component{Type: wire.ComponentButton, Key: fmt.Sprintf("t%d-reply", root.ID), Title: "回复",
		Action: ext.Open(fmt.Sprintf("w:reply:%d", root.ID), "", state{V: viewWrite, W: "reply", Repo: s.Repo, Number: s.Number, Path: root.Path, ID: root.ID, Line: cmpLine(root)}, "回复")})
	return out
}

func cmpLine(c github.LineComment) int {
	if c.Line > 0 {
		return c.Line
	}
	return c.Original
}

func lastLinesOf(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "@@") {
		lines = lines[1:]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// check is one check run: where it stands, what failed, its steps, the end of its log, its summary.
func (p *Page) check(ctx context.Context, r *ext.UIRequest, s state) *wire.View {
	v := &wire.View{Title: "CI", State: ext.EncodeState(s), RefreshAfter: refreshAfter}
	ck, err := p.c.CheckRunAt(ctx, s.Repo, s.ID)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty("读不到这个检查", err)}}}
		return v
	}
	v.Title = clip(ck.Name, 200)
	n := checkNames[ck.State]
	if n.name == "" {
		n.name, n.tone = ck.State, wire.ToneNeutral
	}
	sub := []string{nonEmpty(ck.App, "checks")}
	if !ck.Started.IsZero() && !ck.Completed.IsZero() {
		sub = append(sub, "用时 "+took(ck.Completed.Sub(ck.Started)))
	}
	if !ck.Completed.IsZero() {
		sub = append(sub, ago(p.now(), ck.Completed))
	}
	head := []wire.Component{{Type: wire.ComponentRow, Key: "check", Title: clip(ck.Name, 300), Subtitle: strings.Join(sub, " · "),
		Badge: &wire.Badge{Text: n.name, Tone: n.tone}}}
	if ck.Title != "" {
		head = append(head, wire.Component{Type: wire.ComponentText, Key: "title", Text: clip(ck.Title, 2000)})
	}
	v.Sections = append(v.Sections, wire.Section{Items: head})

	var log string
	pending := false
	if ck.Actions && ck.State == "failure" {
		log, pending = p.c.JobLog(s.Repo, ck.ID, logWait, p.invalidate)
	}
	if errs := github.ErrorLines(log); len(errs) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: "出错的地方", Items: []wire.Component{codeItem(r, "errors", "text", strings.Join(errs, "\n"))},
			Footer: "从日志里挑出来的报错行"})
	}
	if len(ck.Annotations) > 0 {
		sec := wire.Section{Title: fmt.Sprintf("注解 · %d", len(ck.Annotations))}
		for i, a := range ck.Annotations {
			tone := map[string]string{"failure": wire.ToneDanger, "warning": wire.ToneCaution}[a.Level]
			row := wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("a%d", i), Title: clip(oneLine(nonEmpty(a.Title, a.Message)), 300),
				Subtitle: clip(fmt.Sprintf("%s:%d", a.Path, a.Line), 300),
				Badge:    &wire.Badge{Text: map[string]string{"failure": "错误", "warning": "警告"}[a.Level], Tone: tone}}
			if row.Badge.Text == "" {
				row.Badge = nil
			}
			sec.Items = append(sec.Items, row)
			if a.Title != "" && a.Message != "" {
				sec.Items = append(sec.Items, codeItem(r, fmt.Sprintf("a%d-msg", i), "text", clip(a.Message, 4000)))
			}
		}
		v.Sections = append(v.Sections, sec)
	}
	if len(ck.Steps) > 0 {
		// The steps that did not pass; those that did are one line.
		sec := wire.Section{Title: "步骤"}
		passed := 0
		for _, st := range ck.Steps {
			state := st.Conclusion
			if st.Status != "completed" {
				state = "pending"
			}
			if state == "success" || state == "skipped" {
				passed++
				continue
			}
			cn := checkNames[state]
			row := wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("step-%d", st.Number), Title: clip(fmt.Sprintf("%d. %s", st.Number, st.Name), 300)}
			if cn.name == "" {
				cn.name, cn.tone = nonEmpty(state, "—"), wire.ToneNeutral
			}
			row.Badge = &wire.Badge{Text: cn.name, Tone: cn.tone}
			sec.Items = append(sec.Items, row)
		}
		if passed > 0 {
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentText, Key: "steps-passed", Style: wire.TextStyleSecondary,
				Text: fmt.Sprintf("其余 %d 步通过或跳过了", passed)})
		}
		v.Sections = append(v.Sections, sec)
	}
	switch {
	case log != "":
		v.Sections = append(v.Sections, wire.Section{Title: "日志", Items: codeBlocks(r, "log", "text", github.LogAround(log, logLines), 0),
			Footer: "最后一个报错和它前面的部分；没有报错标记时是日志的最后一段"})
	case pending:
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentLoading, Key: "log-loading", Title: "正在读日志"}}})
	case ck.State == "failure" && ck.Actions:
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentText, Key: "no-log", Style: wire.TextStyleSecondary,
			Text: "读不到日志（日志过期了，或者没有权限），在 GitHub 上看"}}})
	}
	if strings.TrimSpace(ck.Summary) != "" {
		v.Sections = append(v.Sections, wire.Section{Title: "说明", Items: []wire.Component{markdownItem(r, "summary", ck.Summary, "", 8000)}})
	}
	tail := []wire.Component{}
	if u, ok := httpsURL(ck.URL); ok {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
	}
	tail = append(tail, refreshButton())
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
}

// took is a duration the way people say it.
func took(d time.Duration) string {
	switch {
	case d < 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d 小时 %d 分", int(d.Hours()), int(d.Minutes())%60)
}
