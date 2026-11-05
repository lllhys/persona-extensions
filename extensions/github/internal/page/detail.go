package page

import (
	"cmp"
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

// A PR or issue opened on top of a list, laid out as nwverse/persona#2951 says for both code-hosting extensions: a PR
// in four tabs — 概览 (a failing CI first, the description, where it stands and who reviews it, the whole conversation,
// what can be done), 改动 (the files it changes, each opening its diff), 提交 and CI (the head commit's checks, each
// opening its log); an issue has only 概览.

const (
	bodyMost  = 20000 // characters of a description shown
	noteMost  = 6000  // characters of a comment shown
	notesPage = 30    // comments shown per 「更早的」
	filesPage = 100
	// how long the overview waits for a failing job's log (the rest is read after: the page draws again)
	overviewLogWait = 3 * time.Second
)

// Tabs of a PR / issue.
const (
	tabOverview = "overview"
	tabFiles    = "files"
	tabPRCommit = "commits"
	tabChecks   = "checks"
)

var stateNames = map[string]struct {
	name, tone string
}{
	"open":   {"开着", wire.TonePositive},
	"draft":  {"草稿", wire.ToneNeutral},
	"merged": {"已合并", wire.TonePositive},
	"closed": {"已关闭", wire.ToneNeutral},
}

var reviewNames = map[string]struct {
	name, tone string
}{
	"APPROVED":          {"批准了", wire.TonePositive},
	"CHANGES_REQUESTED": {"要求修改", wire.ToneDanger},
	"COMMENTED":         {"评论了", wire.ToneNeutral},
	"DISMISSED":         {"被撤销", wire.ToneNeutral},
	"REQUESTED":         {"等 TA review", wire.ToneCaution},
}

var fileStates = map[string]struct {
	name, tone string
}{
	"added":   {"新增", wire.TonePositive},
	"removed": {"删除", wire.ToneDanger},
	"renamed": {"改名", wire.ToneNeutral},
	"copied":  {"复制", wire.ToneNeutral},
}

// detail is a PR or issue opened on top of the list.
func (p *Page) detail(ctx context.Context, r *ext.UIRequest, s state, fresh bool) *wire.View {
	ref := fmt.Sprintf("%s#%d", s.Repo, s.Number)
	v := &wire.View{Title: ref, State: ext.EncodeState(s), RefreshAfter: refreshAfter}
	back := wire.Component{Type: wire.ComponentButton, Key: "back", Title: "回到列表", Action: &wire.Action{ID: "back"}}
	if empty, ok := p.unavailable(ctx); ok {
		v.Sections = []wire.Section{{Items: []wire.Component{empty}}}
		return v
	}
	d, err := p.c.Detail(ctx, s.Repo, s.Number)
	if err != nil {
		items := []wire.Component{errorEmpty("读不到 "+ref, err)}
		if u, ok := httpsURL(fmt.Sprintf("%s/%s/issues/%d", p.c.Web(), s.Repo, s.Number)); ok {
			items = append(items, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
		}
		if r.UIVersion < 3 {
			items = append(items, back)
		}
		v.Sections = []wire.Section{{Items: items}}
		return v
	}
	v.Title = clip(oneLine(d.Title), 200)
	tab := s.Tab
	var tabs []wire.Choice
	if d.PR {
		tabs = []wire.Choice{{Value: tabOverview, Title: "概览"}, {Value: tabFiles, Title: tabCount("改动", d.Files)},
			{Value: tabPRCommit, Title: tabCount("提交", d.Commits)}, {Value: tabChecks, Title: "CI"}}
		if d.Checks.Failed > 0 {
			tabs[3].Title = fmt.Sprintf("CI ✕%d", d.Checks.Failed)
		}
	}
	if !hasChoice(tabs, tab) {
		tab = tabOverview
	}
	if r.UIVersion >= 2 && len(tabs) > 0 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentSegmented, Key: "detail-tabs", Name: "tab",
			Value: tab, Choices: tabs, Action: &wire.Action{ID: "tab"}}}})
	} else {
		tab = tabOverview
	}
	switch tab {
	case tabFiles:
		v.Sections = append(v.Sections, p.filesTab(ctx, r, s, d)...)
	case tabPRCommit:
		v.Sections = append(v.Sections, p.prCommits(ctx, r, d)...)
	case tabChecks:
		v.Sections = append(v.Sections, p.checksTab(ctx, r, s, d)...)
	default:
		v.Sections = append(v.Sections, p.overview(ctx, r, s, d)...)
	}

	tail := []wire.Component{}
	if u, ok := httpsURL(d.URL); ok {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
	}
	if tab == tabOverview {
		tail = append(tail, wire.Component{Type: wire.ComponentText, Key: "kb", Style: wire.TextStyleCaption,
			Text: "和你有关的 PR / issue 会归档进知识库，会话是 " + archiveID(d.Repo, d.Number) + "（GitHub 连接器的「进知识库」开着时）。"})
	}
	tail = append(tail, refreshButton())
	if r.UIVersion < 3 {
		tail = append(tail, back)
	}
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
}

func tabCount(name string, n int) string {
	if n <= 0 {
		return name
	}
	if n > 999 {
		return name + " 999+"
	}
	return name + " " + strconv.Itoa(n)
}

// overview is where a PR / issue stands, its description, its reviewers and checks, the latest of its timeline, and
// what can be done.
func (p *Page) overview(ctx context.Context, r *ext.UIRequest, s state, d github.Detail) []wire.Section {
	kind := "Issue"
	if d.PR {
		kind = "PR"
	}
	st := stateNames[d.State]
	if st.name == "" {
		st.name, st.tone = d.State, wire.ToneNeutral
	}
	ref := fmt.Sprintf("%s#%d", d.Repo, d.Number)
	head := []wire.Component{
		{Type: wire.ComponentRow, Key: "what", Title: ref, Subtitle: kind + " · " + d.Author + " 发起于 " + d.Created.Format("2006-01-02"),
			Badge: &wire.Badge{Text: st.name, Tone: st.tone}, Avatar: avatar(r, d.Avatar),
			Action: ext.Open("repo", "", state{V: viewRepo, Repo: d.Repo}, d.Repo)},
	}
	if d.PR {
		if d.Head != "" {
			from := d.Head
			if d.HeadRepo != "" {
				from = strings.SplitN(d.HeadRepo, "/", 2)[0] + ":" + d.Head
			}
			head = append(head, wire.Component{Type: wire.ComponentRow, Key: "branch", Title: "分支", Subtitle: clip(from+" → "+d.Base, 300)})
		}
		if r.UIVersion >= 2 {
			head = append(head, wire.Component{Type: wire.ComponentStatGrid, Key: "size", Stats: []wire.Stat{
				{Label: "文件", Value: strconv.Itoa(d.Files), Tone: wire.ToneNeutral},
				{Label: "增加", Value: "+" + strconv.Itoa(d.Adds), Tone: wire.TonePositive},
				{Label: "删除", Value: "−" + strconv.Itoa(d.Dels), Tone: wire.ToneDanger},
				{Label: "提交", Value: strconv.Itoa(d.Commits), Tone: wire.ToneNeutral},
			}})
		}
		if m := mergeLine(d); m.text != "" {
			head = append(head, wire.Component{Type: wire.ComponentRow, Key: "mergeable", Title: "合并", Value: m.text,
				Badge: &wire.Badge{Text: m.badge, Tone: m.tone}})
		}
	}
	if len(d.Labels) > 0 {
		head = append(head, wire.Component{Type: wire.ComponentRow, Key: "labels", Title: "标签", Value: clip(strings.Join(d.Labels, "、"), 300)})
	}
	if len(d.Assignees) > 0 {
		head = append(head, wire.Component{Type: wire.ComponentRow, Key: "assignees", Title: "指派给", Value: clip(strings.Join(d.Assignees, "、"), 300)})
	}
	if d.Milestone != "" {
		head = append(head, wire.Component{Type: wire.ComponentRow, Key: "milestone", Title: "里程碑", Value: clip(d.Milestone, 300)})
	}
	head = append(head, wire.Component{Type: wire.ComponentRow, Key: "updated", Title: "最后更新", Value: ago(p.now(), d.Updated)})
	out := []wire.Section{{Items: head}}
	if d.PR && d.Checks.Failed > 0 {
		out = append(out, p.failingCI(ctx, r, s, d)...)
	}
	body := strings.TrimSpace(d.Body)
	if body == "" {
		body = "*没有写描述*"
	}
	base := fmt.Sprintf("%s/%s/blob/%s/", p.c.Web(), d.Repo, cmp.Or(d.Head, "HEAD"))
	if d.HeadRepo != "" || !d.PR {
		base = fmt.Sprintf("%s/%s/blob/HEAD/", p.c.Web(), d.Repo)
	}
	out = append(out, wire.Section{Title: "描述", Items: []wire.Component{markdownItem(r, "body", body, base, bodyMost)}})
	if d.PR {
		out = append(out, reviewsSection(r, d), checksSection(d))
	}
	out = append(out, p.discussion(ctx, r, s, d)...)
	out = append(out, wire.Section{Title: "操作", Items: p.writeButtons(r, s, d), Footer: "发出前会先给你看要发的内容，确认后才发到 GitHub。"})
	return out
}

// failingCI is what comes first on a PR whose checks fail: the failed ones, the error lines of the first failed
// Actions job's log (when it comes quickly), running the failed jobs again.
func (p *Page) failingCI(ctx context.Context, r *ext.UIRequest, s state, d github.Detail) []wire.Section {
	checks, err := p.c.Checks(ctx, d.Repo, d.HeadSHA)
	if err != nil {
		return nil
	}
	sec := wire.Section{Title: fmt.Sprintf("CI 失败 · %d 项", d.Checks.Failed)}
	var jobs []int64
	for i, ck := range checks {
		if ck.State != "failure" {
			continue
		}
		sec.Items = append(sec.Items, p.checkRow(r, s, ck, i))
		if ck.Actions && ck.ID > 0 {
			jobs = append(jobs, ck.ID)
		}
	}
	if len(jobs) > 0 {
		if log, pending := p.c.JobLog(d.Repo, jobs[0], overviewLogWait, p.invalidate); log != "" {
			if errs := github.ErrorLines(log); len(errs) > 0 {
				sec.Items = append(sec.Items, codeItem(r, "ci-errors", "text", strings.Join(errs, "\n")))
			}
		} else if pending && r.UIVersion >= 3 {
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentLoading, Key: "ci-loading", Title: "正在读失败的日志"})
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentButton, Key: "rerun", Title: "重跑失败的",
			Action: &wire.Action{ID: "rerun", Confirm: fmt.Sprintf("重跑 %s#%d 失败的 GitHub Actions？", d.Repo, d.Number)}})
	}
	return []wire.Section{sec}
}

// rerun runs the head commit's failed Actions jobs again (the overview's button, confirmed in the app).
func (p *Page) rerun(ctx context.Context, r *ext.UIRequest, s state) (*wire.ActionResult, error) {
	d, err := p.c.Detail(ctx, s.Repo, s.Number)
	if err != nil {
		return &wire.ActionResult{Toast: "读不到这个 PR"}, nil
	}
	checks, err := p.c.Checks(ctx, d.Repo, d.HeadSHA)
	if err != nil {
		return &wire.ActionResult{Toast: "读不到 CI"}, nil
	}
	var jobs []int64
	for _, ck := range checks {
		if ck.State == "failure" && ck.Actions && ck.ID > 0 {
			jobs = append(jobs, ck.ID)
		}
	}
	n, err := p.c.Rerun(ctx, d.Repo, jobs)
	if err != nil {
		return &wire.ActionResult{Toast: clip("没有重跑："+github.Scrub(err.Error()), 200)}, nil
	}
	p.c.ForgetDetail(s.Repo, s.Number)
	return &wire.ActionResult{View: p.view(ctx, r, s, true), Toast: fmt.Sprintf("重跑了 %d 个工作流里失败的任务", n)}, nil
}

type mergeState struct{ text, badge, tone string }

// mergeLine says whether a PR can be merged, in GitHub's words for it.
func mergeLine(d github.Detail) mergeState {
	switch {
	case d.State == "merged":
		m := mergeState{"由 " + nonEmpty(d.MergedBy, "有人") + " 合并", "已合并", wire.TonePositive}
		if !d.MergedAt.IsZero() {
			m.text += "于 " + d.MergedAt.Format("2006-01-02")
		}
		return m
	case d.State == "closed":
		return mergeState{}
	case d.Mergeable == "conflict" || d.MergeState == "dirty":
		return mergeState{"和 " + d.Base + " 有冲突", "有冲突", wire.ToneDanger}
	}
	switch d.MergeState {
	case "clean", "has_hooks":
		return mergeState{"可以合并", "可以", wire.TonePositive}
	case "unstable":
		return mergeState{"可以合并，但有检查没通过", "检查没过", wire.ToneCaution}
	case "blocked":
		return mergeState{"被分支保护规则挡着（要 review 或检查通过）", "还不行", wire.ToneCaution}
	case "behind":
		return mergeState{"落后于 " + d.Base + "，要先更新分支", "要更新", wire.ToneCaution}
	case "draft":
		return mergeState{"草稿，标为可以 review 后才能合并", "草稿", wire.ToneNeutral}
	}
	return mergeState{"GitHub 还在算", "未知", wire.ToneNeutral}
}

// writeButtons are what can be done from here: comment; on an open PR review and merge.
func (p *Page) writeButtons(r *ext.UIRequest, s state, d github.Detail) []wire.Component {
	w := func(kind string) state { return state{V: viewWrite, W: kind, Repo: d.Repo, Number: d.Number} }
	var out []wire.Component
	if !d.Locked {
		out = append(out, wire.Component{Type: wire.ComponentButton, Key: "w-comment", Title: "评论", Action: ext.Open("w:comment", "", w("comment"), "评论")})
	}
	if d.PR && (d.State == "open" || d.State == "draft") {
		out = append(out, wire.Component{Type: wire.ComponentButton, Key: "w-review", Title: "提交 review", Action: ext.Open("w:review", "", w("review"), "Review")})
		if d.State == "open" {
			out = append(out, wire.Component{Type: wire.ComponentButton, Key: "w-merge", Title: "合并", Action: ext.Open("w:merge", "", w("merge"), "合并")})
		}
	}
	if len(out) == 0 {
		out = append(out, wire.Component{Type: wire.ComponentText, Key: "w-none", Text: "讨论已锁定，不能评论", Style: wire.TextStyleSecondary})
	}
	return out
}

// filesTab is the files a PR changes, each opening its diff.
func (p *Page) filesTab(ctx context.Context, r *ext.UIRequest, s state, d github.Detail) []wire.Section {
	var files []github.PRFile
	more := false
	var err error
	for pg := 1; pg <= s.page(); pg++ {
		var batch []github.PRFile
		if batch, more, err = p.c.PRFiles(ctx, d.Repo, d.Number, pg); err != nil {
			break
		}
		files = append(files, batch...)
		if !more {
			break
		}
	}
	comments := map[string]int{}
	if convs, err := p.c.Conversations(ctx, d.Repo, d.Number); err == nil {
		for _, c := range convs {
			comments[c.Root().Path] += len(c.Comments)
		}
	}
	sec := wire.Section{Title: fmt.Sprintf("%d 个文件 · +%d −%d", d.Files, d.Adds, d.Dels)}
	for i, f := range files {
		sec.Items = append(sec.Items, fileRow(r, d, f, comments[f.Path], i))
	}
	if more && err == nil {
		sec.More = &wire.More{Title: fmt.Sprintf("还有 %d 个文件", max(0, d.Files-len(files))), Action: wire.Action{ID: "more"}}
	}
	switch {
	case err != nil && len(files) == 0:
		sec.Items = append(sec.Items, errorEmpty("读不到改动的文件", err))
	case len(files) == 0:
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "没有改动文件", Icon: wire.NavIconDoc})
	}
	return []wire.Section{sec}
}

func fileRow(r *ext.UIRequest, d github.Detail, f github.PRFile, comments, i int) wire.Component {
	dir := parentDir(f.Path)
	sub := []string{fmt.Sprintf("+%d −%d", f.Adds, f.Dels)}
	if dir != "" {
		sub = append([]string{dir}, sub...)
	}
	if f.Previous != "" {
		sub = append(sub, "原来是 "+f.Previous)
	}
	if comments > 0 {
		sub = append(sub, fmt.Sprintf("%d 条评论", comments))
	}
	row := wire.Component{Type: wire.ComponentRow, Key: "f-" + clip(f.Path, 120), Title: clip(path.Base(f.Path), 300), Subtitle: clip(strings.Join(sub, " · "), 2000),
		Icon: icon(r, wire.NavIconDoc), Action: ext.Open("diff:"+strconv.Itoa(i), "", state{V: viewDiff, Repo: d.Repo, Number: d.Number, Path: f.Path}, path.Base(f.Path))}
	if fs, ok := fileStates[f.Status]; ok {
		row.Badge = &wire.Badge{Text: fs.name, Tone: fs.tone}
	} else if comments > 0 {
		row.Badge = &wire.Badge{Text: strconv.Itoa(comments), Tone: wire.ToneCaution}
	}
	return row
}

// discussion is the whole conversation, oldest first: the latest notesPage, earlier ones on 「更早的」.
func (p *Page) discussion(ctx context.Context, r *ext.UIRequest, s state, d github.Detail) []wire.Section {
	notes, err := p.c.Notes(ctx, d.Repo, d.Number, d.PR)
	if err != nil {
		return []wire.Section{{Title: "讨论", Items: []wire.Component{errorEmpty("读不到评论", err)}}}
	}
	sec := wire.Section{Title: fmt.Sprintf("讨论 · %d 条", len(notes))}
	from := max(0, len(notes)-s.page()*notesPage)
	if from > 0 {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentButton, Key: "older", Title: fmt.Sprintf("更早的 %d 条", from),
			Action: &wire.Action{ID: "more"}})
	}
	budget := wire.ItemsMost - 80 // what else the overview has
	for i := from; i < len(notes); i++ {
		items := p.note(r, s, notes[i], fmt.Sprintf("n%d", i))
		if len(sec.Items)+len(items) > budget {
			break
		}
		sec.Items = append(sec.Items, items...)
	}
	if len(notes) == 0 {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "还没有评论", Icon: wire.NavIconChat})
	}
	return []wire.Section{sec}
}

// prCommits are a PR's commits, oldest first.
func (p *Page) prCommits(ctx context.Context, r *ext.UIRequest, d github.Detail) []wire.Section {
	commits, err := p.c.PRCommits(ctx, d.Repo, d.Number)
	if err != nil {
		return []wire.Section{{Items: []wire.Component{errorEmpty("读不到提交", err)}}}
	}
	sec := wire.Section{Title: fmt.Sprintf("%d 个提交", len(commits))}
	for _, c := range commits {
		sec.Items = append(sec.Items, p.commitRow(r, c))
	}
	if d.Commits > len(commits) {
		sec.Footer = fmt.Sprintf("GitHub 只列前 %d 个，其余在 GitHub 上看", len(commits))
	}
	return []wire.Section{sec}
}

// note is one entry of the conversation: who and when (a review's verdict, a line's place), then the words; an event
// or a commit is one line.
func (p *Page) note(r *ext.UIRequest, s state, n github.Note, key string) []wire.Component {
	if n.Kind == "event" || n.Kind == "commit" {
		return []wire.Component{p.noteLine(r, s, n, key)}
	}
	sub := ago(p.now(), n.At)
	row := wire.Component{Type: wire.ComponentRow, Key: key + "-who", Title: nonEmpty(n.Author, "有人"), Avatar: avatar(r, n.Avatar)}
	switch n.Kind {
	case "review":
		rv := reviewNames[n.Review]
		row.Badge = &wire.Badge{Text: nonEmpty(rv.name, "review"), Tone: cmp.Or(rv.tone, wire.ToneNeutral)}
	case "code":
		sub = fmt.Sprintf("%s:%d · %s", n.Path, n.Line, sub)
		row.Action = ext.Open("diff:"+key, "", state{V: viewDiff, Repo: s.Repo, Number: s.Number, Path: n.Path}, path.Base(n.Path))
	}
	row.Subtitle = clip(sub, 2000)
	out := []wire.Component{row}
	if strings.TrimSpace(n.Body) != "" {
		out = append(out, markdownItem(r, key, n.Body, "", noteMost))
	}
	return out
}

// noteLine is an entry as one line (the overview's latest, an event, a commit).
func (p *Page) noteLine(r *ext.UIRequest, s state, n github.Note, key string) wire.Component {
	title := n.Body
	ic := wire.NavIconClock
	switch n.Kind {
	case "comment":
		title = n.Author + "：" + n.Body
		ic = wire.NavIconChat
	case "review":
		title = n.Author + " " + nonEmpty(reviewNames[n.Review].name, "review 了")
		if b := strings.TrimSpace(n.Body); b != "" {
			title += "：" + b
		}
		ic = wire.NavIconCheck
	case "code":
		title = n.Author + " 在 " + path.Base(n.Path) + "：" + n.Body
		ic = wire.NavIconChat
	case "commit":
		ic = wire.NavIconTag
	}
	sub := ago(p.now(), n.At)
	if n.Kind == "commit" {
		sub = n.Author + " · " + sub
	}
	row := wire.Component{Type: wire.ComponentRow, Key: key, Title: clip(oneLine(title), 200), Subtitle: sub, Icon: icon(r, ic)}
	if n.Kind == "code" {
		row.Action = ext.Open("diff:"+key, "", state{V: viewDiff, Repo: s.Repo, Number: s.Number, Path: n.Path}, path.Base(n.Path))
	}
	return row
}

// checksTab is the head commit's checks: counts, then each (the failed first), a check run opening its details.
func (p *Page) checksTab(ctx context.Context, r *ext.UIRequest, s state, d github.Detail) []wire.Section {
	if d.HeadSHA == "" {
		return []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "none", Title: "没有提交", Icon: wire.NavIconCheck}}}}
	}
	checks, err := p.c.Checks(ctx, d.Repo, d.HeadSHA)
	if err != nil {
		return []wire.Section{{Items: []wire.Component{errorEmpty("读不到 CI", err)}}}
	}
	out := []wire.Section{checksSection(d)}
	sec := wire.Section{Title: "提交 " + shortSHA(d.HeadSHA) + " 的检查"}
	for i, ck := range checks {
		sec.Items = append(sec.Items, p.checkRow(r, s, ck, i))
	}
	if len(checks) == 0 {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "这个提交没有检查", Icon: wire.NavIconCheck})
	}
	return append(out, sec)
}

var checkNames = map[string]struct {
	name, tone string
}{
	"success": {"通过", wire.TonePositive},
	"failure": {"失败", wire.ToneDanger},
	"pending": {"进行中", wire.ToneCaution},
	"skipped": {"跳过", wire.ToneNeutral},
	"neutral": {"完成", wire.ToneNeutral},
}

func (p *Page) checkRow(r *ext.UIRequest, s state, ck github.Check, i int) wire.Component {
	n := checkNames[ck.State]
	if n.name == "" {
		n.name, n.tone = ck.State, wire.ToneNeutral
	}
	var sub []string
	if ck.App != "" {
		sub = append(sub, ck.App)
	}
	if ck.Title != "" {
		sub = append(sub, oneLine(ck.Title))
	}
	if !ck.Started.IsZero() && !ck.Completed.IsZero() {
		sub = append(sub, took(ck.Completed.Sub(ck.Started)))
	}
	row := wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("ck-%d-%d", i, ck.ID), Title: clip(ck.Name, 300), Subtitle: clip(strings.Join(sub, " · "), 2000),
		Badge: &wire.Badge{Text: n.name, Tone: n.tone}}
	switch {
	case ck.ID > 0:
		row.Action = ext.Open(fmt.Sprintf("check:%d", ck.ID), "", state{V: viewCheck, Repo: s.Repo, Number: s.Number, ID: ck.ID}, ck.Name)
	case len("web:"+ck.URL) <= 128:
		if u, ok := httpsURL(ck.URL); ok {
			row.Action = &wire.Action{ID: "web:" + u}
		}
	}
	return row
}

// checksSection is a PR head's checks: how many passed, failed, are running; which failed.
func checksSection(d github.Detail) wire.Section {
	c := d.Checks
	sec := wire.Section{Title: "CI"}
	if c.Total() == 0 {
		sec.Items = []wire.Component{{Type: wire.ComponentText, Key: "checks", Text: "这个 PR 的最新提交没有检查", Style: wire.TextStyleSecondary}}
		return sec
	}
	failTone := wire.ToneNeutral
	if c.Failed > 0 {
		failTone = wire.ToneDanger
	}
	sec.Items = []wire.Component{{Type: wire.ComponentStatGrid, Key: "checks", Stats: []wire.Stat{
		{Label: "通过", Value: fmt.Sprint(c.Passed), Tone: wire.TonePositive},
		{Label: "失败", Value: fmt.Sprint(c.Failed), Tone: failTone},
		{Label: "进行中", Value: fmt.Sprint(c.Pending), Tone: wire.ToneNeutral},
	}}}
	if len(c.FailedNames) > 0 {
		names := c.FailedNames
		if len(names) > 8 {
			names = append(names[:8:8], fmt.Sprintf("等 %d 个", len(c.FailedNames)))
		}
		sec.Footer = clip("失败的："+strings.Join(names, "、"), 2000)
	}
	return sec
}

// reviewsSection is each reviewer's latest word, and those asked who have not answered.
func reviewsSection(r *ext.UIRequest, d github.Detail) wire.Section {
	sec := wire.Section{Title: "Reviewer"}
	for _, rv := range d.Reviews {
		n := reviewNames[rv.State]
		if n.name == "" {
			n.name, n.tone = rv.State, wire.ToneNeutral
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Key: "review-" + rv.Login, Title: rv.Login,
			Badge: &wire.Badge{Text: n.name, Tone: n.tone}, Icon: icon(r, wire.NavIconPerson)})
	}
	for _, t := range d.Teams {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Key: "team-" + t, Title: "团队 " + t,
			Badge: &wire.Badge{Text: "等 review", Tone: wire.ToneCaution}, Icon: icon(r, wire.NavIconPerson)})
	}
	if len(sec.Items) == 0 {
		sec.Items = []wire.Component{{Type: wire.ComponentText, Key: "reviews", Text: "还没有人 review，也没有请人 review", Style: wire.TextStyleSecondary}}
	}
	return sec
}

func avatar(r *ext.UIRequest, u string) *wire.Avatar {
	if r.UIVersion < 3 || !strings.HasPrefix(u, "https://") {
		return nil
	}
	return &wire.Avatar{URL: u}
}

// archiveID is the knowledge base's conversation of repo#number (the hub names it github-<the connector's chat id>).
func archiveID(repo string, number int) string {
	return "github-" + strings.ReplaceAll(repo, "/", "-") + fmt.Sprintf("-%d", number)
}

func nonEmpty(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
