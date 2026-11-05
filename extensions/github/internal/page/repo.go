package page

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The repository side of the page (nwverse/persona#2951): the list's 仓库 tab, a repository (its header, a directory
// of it in place, its README), a file, the branches, and a repository's PRs / issues.

const (
	viewFile   = "file"
	dirPage    = 100 // a directory's entries shown per 「加载更多」
	branchPage = 100
	filePage   = 400 // a file's lines per 「加载更多」
	chunkLines = 200 // lines per code block
	chunkMost  = 18000
	readmeMost = 20000
)

var repoLists = []wire.Choice{{Value: github.ReposRecent, Title: "最近"}, {Value: github.ReposMine, Title: "我的"}, {Value: github.ReposStarred, Title: "星标"}}

// repoList is the list's 仓库 tab: recent, mine, starred or an organization's; a search box searching all of GitHub.
func (p *Page) repoList(ctx context.Context, r *ext.UIRequest, s state, v *wire.View, fresh bool) *wire.View {
	list := s.F
	if list == "" {
		list = github.ReposRecent
	}
	if r.UIVersion >= 3 {
		choices := append([]wire.Choice(nil), repoLists...)
		if orgs, err := p.c.Orgs(ctx); err == nil {
			for _, o := range orgs {
				if len(choices) == 20 {
					break
				}
				choices = append(choices, wire.Choice{Value: github.ReposOrg + o, Title: o})
			}
		}
		if !hasChoice(choices, list) {
			list = github.ReposRecent
		}
		v.Filter = &wire.Filter{Name: "f", Title: "哪些仓库", Value: list, Choices: choices, Action: wire.Action{ID: "filter"}}
		v.Search = &wire.Search{Placeholder: "搜索 GitHub 上的仓库", Value: s.Q, Action: wire.Action{ID: "search"}}
	}
	var repos []github.Repo
	more := false
	var err error
	for pg := 1; pg <= s.page(); pg++ {
		var batch []github.Repo
		if s.Q != "" {
			batch, more, err = p.c.SearchRepos(ctx, s.Q, pg)
		} else {
			batch, more, err = p.c.Repos(ctx, list, pg)
		}
		if err != nil {
			break
		}
		repos = append(repos, batch...)
		if !more {
			break
		}
	}
	title := choiceTitle(repoLists, list, strings.TrimPrefix(list, github.ReposOrg)) + "的仓库"
	if s.Q != "" {
		title = "搜索「" + clip(s.Q, 40) + "」"
	}
	sec := wire.Section{Title: fmt.Sprintf("%s · %d", title, len(repos))}
	if more && err == nil {
		sec.Title = fmt.Sprintf("%s · %d+", title, len(repos))
		sec.More = &wire.More{Title: "加载更多", Action: wire.Action{ID: "more"}}
	}
	for _, rp := range repos {
		sec.Items = append(sec.Items, p.repoRow(r, rp))
	}
	switch {
	case err != nil && len(repos) == 0:
		sec.Items = append(sec.Items, errorEmpty("读不到仓库", err))
	case len(repos) == 0 && s.Q != "":
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "没有找到「" + clip(s.Q, 40) + "」", Icon: wire.NavIconSearch})
	case len(repos) == 0:
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "没有仓库", Icon: wire.NavIconFolder})
	}
	v.Sections = append(v.Sections, sec, wire.Section{Items: []wire.Component{refreshButton()}})
	_ = fresh // the repositories are read with If-None-Match: a refresh reads them again anyway
	return v
}

func (p *Page) repoRow(r *ext.UIRequest, rp github.Repo) wire.Component {
	var sub []string
	if d := oneLine(rp.Description); d != "" {
		sub = append(sub, clip(d, 120))
	}
	if rp.Language != "" {
		sub = append(sub, rp.Language)
	}
	if rp.Stars > 0 {
		sub = append(sub, "★ "+count(rp.Stars))
	}
	if !rp.PushedAt.IsZero() {
		sub = append(sub, "更新于 "+ago(p.now(), rp.PushedAt))
	}
	row := wire.Component{Type: wire.ComponentRow, Key: "repo-" + clip(rp.FullName, 100), Title: rp.FullName, Subtitle: strings.Join(sub, " · "),
		Action: ext.Open("repo:"+clip(rp.FullName, 100), "", state{V: viewRepo, Repo: rp.FullName}, rp.FullName)}
	switch {
	case rp.Archived:
		row.Badge = &wire.Badge{Text: "已归档", Tone: wire.ToneCaution}
	case rp.Private:
		row.Badge = &wire.Badge{Text: "私有", Tone: wire.ToneNeutral}
	case rp.Fork:
		row.Badge = &wire.Badge{Text: "fork", Tone: wire.ToneNeutral}
	}
	if r.UIVersion >= 3 {
		if a := avatar(r, rp.Owner.AvatarURL); a != nil {
			row.Avatar = a
		} else {
			row.Icon = wire.NavIconFolder
		}
	}
	return row
}

// Tabs of a repository.
const (
	tabCode     = "code"
	tabPulls    = "pulls"
	tabIssues   = "issues"
	tabCommits  = "commits"
	tabBranches = "branches"
)

var repoTabs = []wire.Choice{{Value: tabCode, Title: "代码"}, {Value: tabPulls, Title: "PR"}, {Value: tabIssues, Title: "Issue"},
	{Value: tabCommits, Title: "提交"}, {Value: tabBranches, Title: "分支"}}

// repo is a repository: its header, then its tabs — the code at a ref (a directory moved through in place, its
// README), its PRs, its issues, the ref's commits, its branches.
func (p *Page) repo(ctx context.Context, r *ext.UIRequest, s state) *wire.View {
	v := &wire.View{Title: s.Repo, State: ext.EncodeState(s)}
	info, err := p.c.RepoInfo(ctx, s.Repo)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty("读不到 "+s.Repo, err)}}}
		return v
	}
	if s.Ref == "" {
		s.Ref = info.DefaultBranch
	}
	if !hasChoice(repoTabs, s.Tab) {
		s.Tab = tabCode
	}
	v.Title, v.State = info.FullName, ext.EncodeState(s)

	head := wire.Component{Type: wire.ComponentRow, Key: "repo", Title: info.FullName, Subtitle: clip(oneLine(info.Description), 300), Avatar: avatar(r, info.Owner.AvatarURL)}
	switch {
	case info.Archived:
		head.Badge = &wire.Badge{Text: "已归档", Tone: wire.ToneCaution}
	case info.Private:
		head.Badge = &wire.Badge{Text: "私有", Tone: wire.ToneNeutral}
	}
	items := []wire.Component{head}
	if r.UIVersion >= 2 {
		items = append(items, wire.Component{Type: wire.ComponentStatGrid, Key: "stats", Stats: []wire.Stat{
			{Label: "星标", Value: count(info.Stars), Tone: wire.ToneNeutral},
			{Label: "fork", Value: count(info.Forks), Tone: wire.ToneNeutral},
			{Label: "语言", Value: nonEmpty(info.Language, "—"), Tone: wire.ToneNeutral},
			{Label: "默认分支", Value: clip(info.DefaultBranch, 40), Tone: wire.ToneNeutral},
		}})
	}
	v.Sections = append(v.Sections, wire.Section{Items: items})
	if r.UIVersion >= 2 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentSegmented, Key: "repo-tabs", Name: "tab",
			Value: s.Tab, Choices: repoTabs, Action: &wire.Action{ID: "tab"}}}})
	} else {
		s.Tab = tabCode
	}
	switch s.Tab {
	case tabPulls, tabIssues:
		v.Sections = append(v.Sections, p.itemsTab(ctx, r, v, s)...)
	case tabCommits:
		v.Sections = append(v.Sections, p.commitsOf(ctx, r, s)...)
	case tabBranches:
		v.Sections = append(v.Sections, p.branchesTab(ctx, r, v, s, info)...)
	default:
		v.Sections = append(v.Sections, p.codeTab(ctx, r, v, s, info)...)
	}
	tail := []wire.Component{}
	if u, ok := httpsURL(webTree(info.HTMLURL, s.Ref, s.Path)); ok && s.Tab == tabCode {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
	} else if u, ok := httpsURL(info.HTMLURL); ok {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
	}
	tail = append(tail, refreshButton())
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
}

// codeTab is the code at s.Ref: the branch to pick, the directory s.Path, its README.
func (p *Page) codeTab(ctx context.Context, r *ext.UIRequest, v *wire.View, s state, info github.Repo) []wire.Section {
	var out []wire.Section
	branches, moreBranches, _ := p.c.Branches(ctx, s.Repo, 1)
	if r.UIVersion >= 3 && len(branches) > 1 {
		choices := []wire.Choice{{Value: s.Ref, Title: clip(s.Ref, 60)}}
		if s.Ref != info.DefaultBranch {
			choices = append(choices, wire.Choice{Value: info.DefaultBranch, Title: clip(info.DefaultBranch, 60)})
		}
		for _, b := range branches {
			if len(choices) == 20 {
				moreBranches = true
				break
			}
			if !hasChoice(choices, b.Name) {
				choices = append(choices, wire.Choice{Value: b.Name, Title: clip(b.Name, 60)})
			}
		}
		v.Filter = &wire.Filter{Name: "ref", Title: "分支", Value: s.Ref, Choices: choices, Action: wire.Action{ID: "ref"}}
		if moreBranches {
			v.Filter.Title = "分支（全部在「分支」里）"
		}
	}
	entries, err := p.c.Dir(ctx, s.Repo, s.Ref, s.Path)
	dir := wire.Section{Title: clip(s.Ref+" · /"+s.Path, 300)}
	if s.Path != "" {
		dir.Items = append(dir.Items, wire.Component{Type: wire.ComponentRow, Key: "up", Title: "上一级", Subtitle: "/" + parentDir(s.Path),
			Icon: icon(r, wire.NavIconFolder), Action: &wire.Action{ID: "cd:.."}})
	}
	var readme string
	if err != nil {
		dir.Items = append(dir.Items, errorEmpty("读不到这个目录", err))
	}
	shown := min(len(entries), s.page()*dirPage)
	for i, e := range entries {
		if readme == "" && e.Type == "file" && strings.HasPrefix(strings.ToLower(e.Name), "readme") {
			readme = e.Path
		}
		if i < shown {
			dir.Items = append(dir.Items, p.entryRow(r, s, s.Ref, i, e))
		}
	}
	if shown < len(entries) {
		dir.More = &wire.More{Title: fmt.Sprintf("还有 %d 项", len(entries)-shown), Action: wire.Action{ID: "more"}}
	}
	if err == nil && len(entries) == 0 {
		dir.Items = append(dir.Items, wire.Component{Type: wire.ComponentEmpty, Key: "empty-dir", Title: "这个目录是空的", Icon: wire.NavIconFolder})
	}
	out = append(out, dir)
	if readme != "" {
		if name, text, err := p.c.Readme(ctx, s.Repo, s.Ref, s.Path); err == nil && strings.TrimSpace(text) != "" {
			base := fmt.Sprintf("%s/%s/blob/%s/%s", p.c.Web(), s.Repo, s.Ref, parentDir(name))
			out = append(out, wire.Section{Title: path.Base(name), Items: fileBody(r, name, text, base, false, readmeMost)})
		}
	}
	return out
}

func (p *Page) entryRow(r *ext.UIRequest, s state, ref string, i int, e github.TreeEntry) wire.Component {
	row := wire.Component{Type: wire.ComponentRow, Key: "e-" + clip(e.Path, 120), Title: clip(e.Name, 300)}
	switch e.Type {
	case "dir":
		row.Icon = icon(r, wire.NavIconFolder)
		row.Action = &wire.Action{ID: "cd:" + strconv.Itoa(i)}
	case "file":
		row.Icon = icon(r, wire.NavIconDoc)
		row.Value = size(e.Size)
		row.Action = ext.Open("file:"+strconv.Itoa(i), "", state{V: viewFile, Repo: s.Repo, Ref: ref, Path: e.Path}, e.Name)
	case "submodule":
		row.Icon = icon(r, wire.NavIconLink)
		row.Subtitle = "子模块"
	default:
		row.Icon = icon(r, wire.NavIconLink)
		row.Subtitle = "链接"
	}
	return row
}

// entry is the i-th entry of the directory s shows (a tap on it).
func (p *Page) entry(ctx context.Context, s state, i string) (github.TreeEntry, bool) {
	n, err := strconv.Atoi(i)
	if err != nil {
		return github.TreeEntry{}, false
	}
	entries, err := p.c.Dir(ctx, s.Repo, s.Ref, s.Path)
	if err != nil || n < 0 || n >= len(entries) {
		return github.TreeEntry{}, false
	}
	return entries[n], true
}

// file is a file's content: Markdown drawn (or its source), code in blocks of chunkLines, more on 「加载更多」.
func (p *Page) file(ctx context.Context, r *ext.UIRequest, s state) *wire.View {
	v := &wire.View{Title: path.Base(s.Path), State: ext.EncodeState(s)}
	f, err := p.c.FileAt(ctx, s.Repo, s.Ref, s.Path)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty("读不到 "+s.Path, err)}}}
		return v
	}
	head := []wire.Component{{Type: wire.ComponentRow, Key: "where", Title: clip(s.Path, 300), Subtitle: clip(s.Repo+" · "+s.Ref, 300), Value: size(f.Size)}}
	md := isMarkdown(s.Path)
	if md && f.Text != "" {
		title := "看源码"
		if s.Src {
			title = "看排版"
		}
		head = append(head, wire.Component{Type: wire.ComponentButton, Key: "src", Title: title, Action: &wire.Action{ID: "src"}})
	}
	v.Sections = append(v.Sections, wire.Section{Items: head})
	switch {
	case f.TooBig:
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "big", Title: "文件太大，不在这里显示",
			Detail: "超过 1 MB 的文件在 GitHub 上看", Icon: wire.NavIconDoc}}})
	case f.Binary:
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "binary", Title: "二进制文件",
			Detail: "图片、压缩包这类文件在 GitHub 上看", Icon: wire.NavIconDoc}}})
	case f.Text == "":
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "empty", Title: "空文件", Icon: wire.NavIconDoc}}})
	case md && !s.Src:
		base := fmt.Sprintf("%s/%s/blob/%s/%s", p.c.Web(), s.Repo, s.Ref, parentDir(s.Path))
		v.Sections = append(v.Sections, wire.Section{Items: fileBody(r, s.Path, f.Text, base, false, readmeMost)})
	case r.UIVersion >= 4: // a code component: line numbers, highlighting, line links (review.go)
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{codeOf(s, f)}})
	default:
		lines := strings.Split(strings.TrimRight(f.Text, "\n"), "\n")
		shown := min(len(lines), s.page()*filePage)
		sec := wire.Section{Title: fmt.Sprintf("%d 行", len(lines))}
		sec.Items = codeBlocks(r, "code", language(s.Path), lines[:shown], 1)
		if shown < len(lines) {
			sec.More = &wire.More{Title: fmt.Sprintf("还有 %d 行", len(lines)-shown), Action: wire.Action{ID: "more"}}
		}
		v.Sections = append(v.Sections, sec)
	}
	tail := []wire.Component{}
	if u, ok := httpsURL(f.HTMLURL); ok {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
	}
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
}

// branchesTab is all of a repository's branches, the default first; a tap shows the code at it.
func (p *Page) branchesTab(ctx context.Context, r *ext.UIRequest, v *wire.View, s state, info github.Repo) []wire.Section {
	if r.UIVersion >= 3 {
		v.Search = &wire.Search{Placeholder: "分支名", Value: s.Q, Action: wire.Action{ID: "search"}}
	}
	all, more, err := p.branchList(ctx, s)
	sec := wire.Section{}
	q := strings.ToLower(s.Q)
	for i, b := range all {
		if q != "" && !strings.Contains(strings.ToLower(b.Name), q) {
			continue
		}
		row := wire.Component{Type: wire.ComponentRow, Key: "b-" + clip(b.Name, 120), Title: clip(b.Name, 300), Icon: icon(r, wire.NavIconTag),
			Action: &wire.Action{ID: "ref:" + strconv.Itoa(i)}}
		switch {
		case b.Name == s.Ref:
			row.Badge = &wire.Badge{Text: "正在看", Tone: wire.TonePositive}
		case b.Name == info.DefaultBranch:
			row.Badge = &wire.Badge{Text: "默认", Tone: wire.ToneNeutral}
		case b.Protected:
			row.Badge = &wire.Badge{Text: "受保护", Tone: wire.ToneNeutral}
		}
		if b.Name == info.DefaultBranch {
			sec.Items = append([]wire.Component{row}, sec.Items...)
		} else {
			sec.Items = append(sec.Items, row)
		}
	}
	sec.Title = fmt.Sprintf("%d 个分支", len(sec.Items))
	if more && err == nil {
		sec.Title += "（还没读完）"
		sec.More = &wire.More{Title: "加载更多", Action: wire.Action{ID: "more"}}
	}
	if err != nil && len(all) == 0 {
		sec.Items = append(sec.Items, errorEmpty("读不到分支", err))
	} else if len(sec.Items) == 0 {
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "没有找到「" + clip(s.Q, 40) + "」", Icon: wire.NavIconSearch})
	}
	return []wire.Section{sec}
}

// branchList is the branches the 分支 tab has read (s.page() pages of them).
func (p *Page) branchList(ctx context.Context, s state) ([]github.Branch, bool, error) {
	var all []github.Branch
	for pg := 1; pg <= s.page(); pg++ {
		batch, more, err := p.c.Branches(ctx, s.Repo, pg)
		if err != nil {
			return all, false, err
		}
		all = append(all, batch...)
		if !more {
			return all, false, nil
		}
	}
	return all, true, nil
}

// branchAt is the i-th branch of the 分支 tab (a tap on it).
func (p *Page) branchAt(ctx context.Context, s state, i string) (string, bool) {
	n, err := strconv.Atoi(i)
	all, _, _ := p.branchList(ctx, s)
	if err != nil || n < 0 || n >= len(all) {
		return "", false
	}
	return all[n].Name, true
}

// commitsOf is the ref's history, newest first.
func (p *Page) commitsOf(ctx context.Context, r *ext.UIRequest, s state) []wire.Section {
	var all []github.Commit
	more := false
	var err error
	for pg := 1; pg <= s.page(); pg++ {
		var batch []github.Commit
		if batch, more, err = p.c.Commits(ctx, s.Repo, s.Ref, pg); err != nil {
			break
		}
		all = append(all, batch...)
		if !more {
			break
		}
	}
	sec := wire.Section{Title: clip(s.Ref+" 的提交", 300)}
	for _, c := range all {
		sec.Items = append(sec.Items, p.commitRow(r, c))
	}
	if more && err == nil {
		sec.More = &wire.More{Title: "加载更多", Action: wire.Action{ID: "more"}}
	}
	if err != nil && len(all) == 0 {
		sec.Items = append(sec.Items, errorEmpty("读不到提交", err))
	}
	return []wire.Section{sec}
}

func (p *Page) commitRow(r *ext.UIRequest, c github.Commit) wire.Component {
	row := wire.Component{Type: wire.ComponentRow, Key: "k-" + c.SHA, Title: clip(nonEmpty(c.Title, "（没有说明）"), 300),
		Subtitle: shortSHA(c.SHA) + " · " + nonEmpty(c.Author, "有人") + " · " + ago(p.now(), c.At), Avatar: avatar(r, c.Avatar)}
	if row.Avatar == nil {
		row.Icon = icon(r, wire.NavIconTag)
	}
	if u, ok := httpsURL(c.HTMLURL); ok && len("web:"+u) <= 128 {
		row.Action = &wire.Action{ID: "web:" + u}
	}
	return row
}

// itemsTab is a repository's PRs or issues, open or closed, searched within it.
func (p *Page) itemsTab(ctx context.Context, r *ext.UIRequest, v *wire.View, s state) []wire.Section {
	pr := s.Tab == tabPulls
	kind := map[bool]string{true: "PR", false: "issue"}[pr]
	closed := s.F == "closed"
	var out []wire.Section
	if r.UIVersion >= 2 {
		out = append(out, wire.Section{Items: []wire.Component{{Type: wire.ComponentSegmented, Key: "closed", Name: "f",
			Value: nonEmpty(s.F, "open"), Action: &wire.Action{ID: "filter"},
			Choices: []wire.Choice{{Value: "open", Title: "开着"}, {Value: "closed", Title: "已关闭"}}}}})
	}
	if r.UIVersion >= 3 {
		v.Search = &wire.Search{Placeholder: "标题、作者、标签", Value: s.Q, Action: wire.Action{ID: "search"}}
	}
	var all []github.Entry
	more := false
	var err error
	for pg := 1; pg <= s.page(); pg++ {
		var batch []github.Entry
		if batch, more, err = p.c.RepoItems(ctx, s.Repo, pr, closed, s.Q, pg); err != nil {
			break
		}
		all = append(all, batch...)
		if !more {
			break
		}
	}
	sec := wire.Section{Title: fmt.Sprintf("%d 个", len(all))}
	if more && err == nil {
		sec.Title += "+"
		sec.More = &wire.More{Title: "加载更多", Action: wire.Action{ID: "more"}}
	}
	for _, e := range all {
		sec.Items = append(sec.Items, p.row(r, s, e))
	}
	switch {
	case err != nil && len(all) == 0:
		sec.Items = append(sec.Items, errorEmpty("读不到 "+kind, err))
	case len(all) == 0:
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentEmpty, Key: "none", Title: "没有" + map[bool]string{false: "开着的 ", true: "关闭的 "}[closed] + kind, Icon: wire.NavIconCheck})
	}
	return append(out, sec)
}

func webTree(repoURL, ref, dir string) string {
	if dir == "" && ref == "" {
		return repoURL
	}
	return repoURL + "/tree/" + ref + "/" + dir
}

func parentDir(p string) string {
	d := path.Dir(strings.Trim(p, "/"))
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func hasChoice(cs []wire.Choice, v string) bool {
	for _, c := range cs {
		if c.Value == v {
			return true
		}
	}
	return false
}

func choiceTitle(cs []wire.Choice, v, or string) string {
	for _, c := range cs {
		if c.Value == v {
			return c.Title
		}
	}
	return or + " "
}

func icon(r *ext.UIRequest, name string) string {
	if r.UIVersion < 3 {
		return ""
	}
	return name
}

func count(n int) string {
	switch {
	case n >= 10000:
		return fmt.Sprintf("%.1f 万", float64(n)/10000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return strconv.Itoa(n)
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func errorEmpty(title string, err error) wire.Component {
	return wire.Component{Type: wire.ComponentEmpty, Key: "error", Title: clip(title, 200), Icon: wire.NavIconWarning,
		Detail: clip(github.Scrub(err.Error()), 2000), Button: &wire.EmptyButton{Title: "重试", Action: wire.Action{ID: "refresh"}}}
}

func refreshButton() wire.Component {
	return wire.Component{Type: wire.ComponentButton, Key: "refresh", Title: "刷新", Action: &wire.Action{ID: "refresh"}}
}
