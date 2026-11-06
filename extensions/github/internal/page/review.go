package page

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// A PR's files for apps of UI version 4 and 5 (nwverse/persona#2951, #3136): a file tree beside the diffs, each a
// diff component with its conversations at their lines; the first files with their lines, the rest loaded as the
// app gets to them (load_diff: UI version 5 puts the one file in place, 4 draws the page again with it). Version 5
// adds the lines a diff hides (context: the file at the head), GitHub's own Viewed marks, the user's pending review
// (drafts at their lines, and its bar), resolving a conversation, and suggestions. A file's page is a code component
// with links to its lines. Older apps keep filesTab and the markdown diff page (detail.go, diff.go).

const (
	reviewFilesFirst = 10   // files given with their lines at first
	reviewLinesFirst = 3000 // lines of those, at most
	contextMost      = 20000
)

// fileStatus is a GitHub file status as a diff's.
func fileStatus(s string) string {
	switch s {
	case "added":
		return "added"
	case "removed":
		return "deleted"
	case "renamed":
		return "renamed"
	}
	return "modified"
}

// hunksOf is a GitHub patch as a diff's hunks: each line as it is (its prefix kept), the numbers from its @@ line.
func hunksOf(patch string) []wire.Hunk {
	var out []wire.Hunk
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if strings.HasPrefix(line, "@@") {
			h := wire.Hunk{Header: clip(line, 2000)}
			rest := line[2:]
			if i := strings.Index(rest, "@@"); i >= 0 {
				rest = rest[:i]
			}
			for _, f := range strings.Fields(rest) {
				n, _, _ := strings.Cut(f[1:], ",")
				v, _ := strconv.Atoi(n)
				switch f[0] {
				case '-':
					h.OldStart = v
				case '+':
					h.NewStart = v
				}
			}
			out = append(out, h)
			continue
		}
		if len(out) == 0 {
			continue
		}
		if line == "" || !strings.ContainsAny(line[:1], " +-\\") {
			line = " " + line
		}
		h := &out[len(out)-1]
		h.Lines = append(h.Lines, clipBytes(line, 20000))
	}
	return out
}

// reviewData is what the diffs of a PR are drawn with: its conversations, the user's pending review, viewed files.
type reviewData struct {
	convs   []github.Conversation
	pending github.Pending
	viewed  map[string]bool
	v5      bool
}

// reviewData reads them side by side: each is a round trip or more to GitHub.
func (p *Page) reviewData(ctx context.Context, r *ext.UIRequest, d github.Detail) reviewData {
	rd := reviewData{v5: r.UIVersion >= 5}
	var wg sync.WaitGroup
	wg.Go(func() { rd.convs, _ = p.c.Conversations(ctx, d.Repo, d.Number) })
	if rd.v5 {
		wg.Go(func() { rd.pending, _ = p.c.PendingReview(ctx, d.Repo, d.Number) })
		wg.Go(func() { rd.viewed = p.c.Viewed(ctx, d.Repo, d.Number) })
	}
	wg.Wait()
	return rd
}

func comment(r *ext.UIRequest, c github.LineComment) wire.Comment {
	out := wire.Comment{ID: strconv.FormatInt(c.ID, 10), Author: cmp.Or(c.Author, "?"), Avatar: avatar(r, c.Avatar), Body: clip(c.Body, 19000)}
	if !c.At.IsZero() {
		out.At = c.At
	}
	return out
}

func side(s string) string {
	if s == "LEFT" {
		return wire.DiffSideOld
	}
	return wire.DiffSideNew
}

// threads are a file's conversations at their lines, and the user's drafts: a draft of its own is a pending thread,
// a draft reply goes in its conversation with a badge.
func (rd reviewData) threads(r *ext.UIRequest, file string) []wire.Thread {
	var out []wire.Thread
	at := map[int64]int{}
	for _, c := range rd.convs {
		root := c.Root()
		if root.Path != file || len(out) == 500 {
			continue
		}
		th := wire.Thread{ID: strconv.FormatInt(root.ID, 10), Side: side(root.Side), Line: max(root.Line, root.Original, 1), Resolved: c.Resolved,
			Outdated: c.Outdated || root.Line == 0, Reply: &wire.Action{ID: "reply"}}
		if rd.v5 {
			th.Resolve = &wire.Action{ID: "resolve"}
		}
		for _, cm := range c.Comments[:min(len(c.Comments), 200)] {
			th.Comments = append(th.Comments, comment(r, cm))
		}
		at[root.ID] = len(out)
		out = append(out, th)
	}
	for _, cm := range rd.pending.Comments {
		if cm.Path != file {
			continue
		}
		draft := comment(r, cm)
		draft.Badge = &wire.Badge{Text: "草稿", Tone: wire.ToneCaution}
		if i, ok := at[cm.ReplyTo]; ok && cm.ReplyTo != 0 {
			if len(out[i].Comments) < 200 {
				out[i].Comments = append(out[i].Comments, draft)
			}
			continue
		}
		if cm.Line <= 0 || len(out) == 500 {
			continue
		}
		th := wire.Thread{ID: "p" + strconv.FormatInt(cm.ID, 10), Side: side(cm.Side), Line: cm.Line, Pending: true, Comments: []wire.Comment{draft}}
		if cm.Original > 0 && cm.Original < cm.Line {
			th.StartLine = cm.Original
		}
		out = append(out, th)
	}
	return out
}

// diffOf is one file's diff component: with its lines (full), or folded with load to get them.
func (rd reviewData) diffOf(r *ext.UIRequest, f github.PRFile, full bool) wire.Component {
	c := wire.Component{Type: wire.ComponentDiff, Key: "diff:" + clip(f.Path, 120), Path: clip(f.Path, 4096), Status: fileStatus(f.Status),
		Additions: f.Adds, Deletions: f.Dels, Comment: &wire.Action{ID: "inline_comment", Confirm: "直接发到 GitHub，PR 里的人都会看到。发出？"}}
	if f.Previous != "" && f.Previous != f.Path {
		c.OldPath = clip(f.Previous, 4096)
	}
	switch {
	case f.Patch == "" && f.Status == "renamed" && f.Adds+f.Dels == 0:
		// renamed only: nothing to show, nothing to load
	case f.Patch == "":
		c.Binary = true
	case full:
		c.Hunks = hunksOf(f.Patch)
	default:
		c.Collapsed, c.Load = true, &wire.Action{ID: "load_diff"}
	}
	c.Threads = rd.threads(r, f.Path)
	if rd.v5 {
		c.Viewed, c.Revision = rd.viewed[f.Path], f.SHA
		c.MarkViewed = &wire.Action{ID: "mark_viewed"}
		c.Draft = &wire.Action{ID: "draft_comment"}
		c.Suggestions = true
		if f.Patch != "" && f.Status != "removed" && f.Status != "added" { // a new file is all in its diff
			c.Context = &wire.Action{ID: "context"}
		}
	}
	return c
}

// reviewTab is a PR's files: the tree on the side, the diffs beside it (v becomes a split view, with the pending
// review's bar for UI version 5).
func (p *Page) reviewTab(ctx context.Context, r *ext.UIRequest, v *wire.View, s state, d github.Detail) []wire.Section {
	var (
		files []github.PRFile
		err   error
		rd    reviewData
		wg    sync.WaitGroup
	)
	wg.Go(func() { files, err = p.allFiles(ctx, d) })
	wg.Go(func() { rd = p.reviewData(ctx, r, d) })
	wg.Wait()
	if err != nil && len(files) == 0 {
		return []wire.Section{{Items: []wire.Component{errorEmpty("读不到改动的文件", err)}}}
	}
	if len(files) == 0 {
		return []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "none", Title: "没有改动文件", Icon: wire.NavIconDoc}}}}
	}
	comments := map[string]int{}
	for _, c := range rd.convs {
		comments[c.Root().Path] += len(c.Comments)
	}
	tree := wire.Component{Type: wire.ComponentFileTree, Key: "files"}
	main := wire.Section{}
	lines, code := 0, 0
	for i, f := range files {
		e := wire.TreeEntry{Path: clip(f.Path, 4096), Status: fileStatus(f.Status), Additions: f.Adds, Deletions: f.Dels, Comments: comments[f.Path]}
		if rd.v5 {
			e.Viewed = rd.viewed[f.Path]
		}
		tree.Entries = append(tree.Entries, e)
		n := strings.Count(f.Patch, "\n") + 1
		full := f.Path == s.Open || (i < reviewFilesFirst && lines+n <= reviewLinesFirst)
		// what a view carries: the hub's limits (300 components, 4 MB of code)
		if code+len(f.Patch) > wire.CodeMost-(256<<10) || len(main.Items) >= 280 {
			full = false
		}
		if len(main.Items) >= 280 {
			continue
		}
		if full {
			lines += n
			code += len(f.Patch)
		}
		main.Items = append(main.Items, rd.diffOf(r, f, full))
	}
	if len(files) > len(main.Items) {
		main.Footer = fmt.Sprintf("还有 %d 个文件太多放不下，在 GitHub 上看", len(files)-len(main.Items))
	}
	v.Layout = wire.LayoutSplit
	if rd.v5 {
		v.Review = &wire.Review{Count: len(rd.pending.Comments), Name: "body", Placeholder: "写一句总结（可选）", Buttons: []wire.ComposerButton{
			{Title: "评论", Action: wire.Action{ID: "submit_review", Values: map[string]string{"event": "COMMENT"}, Confirm: "提交评审（只评论）？草稿会一起发出。"}},
			{Title: "请求修改", Tone: wire.ToneDanger, Action: wire.Action{ID: "submit_review", Values: map[string]string{"event": "REQUEST_CHANGES"}, Confirm: "提交评审并请求修改？"}},
			{Title: "批准", Tone: wire.TonePositive, Action: wire.Action{ID: "submit_review", Values: map[string]string{"event": "APPROVE"}, Confirm: "批准这个 PR？草稿会一起发出。"}},
		}}
		if rd.pending.ID != 0 {
			v.Review.Discard = &wire.Action{ID: "discard_review", Confirm: "放弃这次评审的全部草稿？"}
		}
	}
	return []wire.Section{
		{Title: fmt.Sprintf("%d 个文件 · +%d −%d", d.Files, d.Adds, d.Dels), Pane: wire.PaneSide, Items: []wire.Component{tree}},
		main,
	}
}

// allFiles is every file a PR changes (GitHub lists 3000 at most).
func (p *Page) allFiles(ctx context.Context, d github.Detail) ([]github.PRFile, error) {
	var files []github.PRFile
	for pg := 1; ; pg++ {
		batch, more, err := p.c.PRFiles(ctx, d.Repo, d.Number, pg)
		files = append(files, batch...)
		if err != nil || !more {
			return files, err
		}
	}
}

// diffPage is one file of a PR as a diff component (a narrow app's page, a link to a file).
func (p *Page) diffPage(ctx context.Context, r *ext.UIRequest, s state) *wire.View {
	v := &wire.View{Title: path.Base(s.Path), State: ext.EncodeState(s), RefreshAfter: refreshAfter}
	d, err := p.c.Detail(ctx, s.Repo, s.Number)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty(fmt.Sprintf("读不到 %s#%d", s.Repo, s.Number), err)}}}
		return v
	}
	f, err := p.c.PRFileAt(ctx, s.Repo, s.Number, s.Path)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty("读不到 "+s.Path, err)}}}
		return v
	}
	rd := p.reviewData(ctx, r, d)
	v.Sections = []wire.Section{{Items: []wire.Component{rd.diffOf(r, f, true)}}}
	if u, ok := httpsURL(f.BlobURL); ok {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u}}})
	}
	return v
}

// codeOf is a file as a code component: as much as fits, the rest with 加载更多 (more), lines linked on GitHub.
func codeOf(s state, f github.File) wire.Component {
	const most = 190000 // bytes, so never more characters either
	lines := strings.Split(strings.TrimRight(f.Text, "\n"), "\n")
	want := min(len(lines), s.page()*filePage*5)
	shown, size := 0, -1
	for shown < want && size+1+len(lines[shown]) <= most {
		size += 1 + len(lines[shown])
		shown++
	}
	text := strings.Join(lines[:shown], "\n")
	if shown == 0 { // one line over the cap (a minified bundle): its start, cut on a rune boundary
		n := most
		for n > 0 && !utf8.RuneStart(lines[0][n]) {
			n--
		}
		text = lines[0][:n]
	}
	c := wire.Component{Type: wire.ComponentCode, Key: "code:" + clip(s.Path, 120), Path: clip(s.Path, 4096), Language: language(s.Path), Text: text, StartLine: 1}
	if shown < len(lines) {
		c.Truncated = true
	}
	if shown == want && want < len(lines) { // stopped by the page, not the cap: every page starts at line 1
		c.Load = &wire.Action{ID: "more"}
	}
	if u, ok := httpsURL(f.HTMLURL); ok {
		c.LineURL = u + "#L{start}-L{end}"
	}
	return c
}

// reviewAction answers the review page's actions: a diff loaded, hidden lines, a viewed mark, a comment (alone or as
// a draft), a reply, a conversation resolved, the pending review sent or thrown away.
func (p *Page) reviewAction(ctx context.Context, r *ext.UIRequest, s state, kind string) (*wire.ActionResult, error) {
	vals := r.Values
	file := vals["path"]
	again := func(toast string) (*wire.ActionResult, error) {
		return &wire.ActionResult{View: p.view(ctx, r, s, false), Toast: toast}, nil
	}
	failed := func(what string, err error) (*wire.ActionResult, error) {
		return &wire.ActionResult{Toast: clip(what+"："+err.Error(), 300)}, nil
	}
	line, _ := strconv.Atoi(vals["line"])
	start, _ := strconv.Atoi(vals["start_line"])
	ghSide := "RIGHT"
	if vals["side"] == "old" {
		ghSide = "LEFT"
	}
	switch kind {
	case "load_diff":
		if r.UIVersion < 5 {
			s.Open = file
			return again("")
		}
		d, err := p.c.Detail(ctx, s.Repo, s.Number)
		if err != nil {
			return failed("读不到这个 PR", err)
		}
		f, err := p.c.PRFileAt(ctx, s.Repo, s.Number, file)
		if err != nil {
			return failed("读不到 "+file, err)
		}
		return &wire.ActionResult{Items: []wire.Component{p.reviewData(ctx, r, d).diffOf(r, f, true)}}, nil
	case "context":
		lines, err := p.c.HeadFile(ctx, s.Repo, s.Number, file)
		if err != nil {
			return failed("读不到 "+file, err)
		}
		return &wire.ActionResult{Lines: linesAround(file, lines, vals["from"], vals["to"])}, nil
	case "mark_viewed":
		if err := p.c.MarkViewed(ctx, s.Repo, s.Number, file, vals["viewed"] == "true"); err != nil {
			return failed("没有标上已看", err)
		}
		return &wire.ActionResult{Items: []wire.Component{}}, nil // the app marked it already: nothing to draw
	case "inline_comment":
		if _, err := p.c.PostLines(ctx, s.Repo, s.Number, file, start, line, ghSide, vals["body"]); err != nil {
			return failed("没有发出", err)
		}
		return again("已发出")
	case "draft_comment":
		if err := p.c.AddDraft(ctx, s.Repo, s.Number, file, ghSide, start, line, vals["body"]); err != nil {
			return failed("没有加进评审", err)
		}
		return again("已加进评审草稿")
	case "reply":
		id, _ := strconv.ParseInt(vals["thread"], 10, 64)
		if _, err := p.c.PostReply(ctx, s.Repo, s.Number, id, vals["body"]); err != nil {
			return failed("没有回复", err)
		}
		return again("已回复")
	case "resolve":
		id, _ := strconv.ParseInt(vals["thread"], 10, 64)
		if err := p.c.Resolve(ctx, s.Repo, s.Number, id, vals["resolved"] == "true"); err != nil {
			return failed("没有改成", err)
		}
		return again("")
	case "submit_review":
		if _, err := p.c.SubmitPending(ctx, s.Repo, s.Number, vals["event"], vals["body"]); err != nil {
			return failed("评审没有提交", err)
		}
		return again("评审已提交")
	case "discard_review":
		if err := p.c.DiscardPending(ctx, s.Repo, s.Number); err != nil {
			return failed("没有放弃", err)
		}
		return again("草稿已放弃")
	}
	return nil, nil
}

// linesAround is the lines a diff's context asked for (from..to, to "end": the rest): the whole file when it is small
// enough (the app keeps it, and asks no more), else the lines asked for.
func linesAround(file string, lines []string, from, to string) *wire.FileLines {
	size := 0
	for _, l := range lines {
		size += len(l)
	}
	if len(lines) <= contextMost && size <= wire.CodeMost/2 {
		return &wire.FileLines{Path: file, Side: wire.DiffSideNew, Start: 1, Lines: clipLines(lines), Total: len(lines)}
	}
	a, _ := strconv.Atoi(from)
	b, err := strconv.Atoi(to)
	if err != nil || to == "end" {
		b = len(lines)
	}
	a = min(max(a, 1), len(lines)+1)
	b = min(max(b, a-1), len(lines), a-1+contextMost)
	return &wire.FileLines{Path: file, Side: wire.DiffSideNew, Start: a, Lines: clipLines(lines[a-1 : b]), Total: len(lines)}
}

func clipLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = clipBytes(l, 20000)
	}
	return out
}
