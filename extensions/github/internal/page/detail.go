package page

import (
	"context"
	"fmt"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const (
	bodyMost    = 4000 // characters of the description shown
	commentMost = 1500 // characters of a comment shown
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
		items := []wire.Component{{Type: wire.ComponentEmpty, Key: "error", Title: "读不到 " + ref, Icon: wire.NavIconWarning,
			Detail: clip(github.Scrub(err.Error()), 2000), Button: &wire.EmptyButton{Title: "重试", Action: wire.Action{ID: "refresh"}}}}
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

	// What it is and where it stands.
	kind := "Issue"
	if d.PR {
		kind = "PR"
	}
	st := stateNames[d.State]
	if st.name == "" {
		st.name, st.tone = d.State, wire.ToneNeutral
	}
	head := []wire.Component{
		{Type: wire.ComponentRow, Key: "what", Title: ref, Subtitle: kind + " · " + d.Author + " 发起于 " + d.Created.Format("2006-01-02"),
			Badge: &wire.Badge{Text: st.name, Tone: st.tone}, Avatar: avatar(r, d.Avatar)},
	}
	if d.PR && d.Head != "" {
		head = append(head, wire.Component{Type: wire.ComponentRow, Key: "branch", Title: "分支", Value: d.Head + " → " + d.Base})
	}
	if len(d.Labels) > 0 {
		head = append(head, wire.Component{Type: wire.ComponentRow, Key: "labels", Title: "标签", Value: clip(strings.Join(d.Labels, "、"), 300)})
	}
	if len(d.Assignees) > 0 {
		head = append(head, wire.Component{Type: wire.ComponentRow, Key: "assignees", Title: "指派给", Value: clip(strings.Join(d.Assignees, "、"), 300)})
	}
	head = append(head, wire.Component{Type: wire.ComponentRow, Key: "updated", Title: "最后更新", Value: ago(p.now(), d.Updated)})
	v.Sections = append(v.Sections, wire.Section{Items: head})

	if d.PR {
		v.Sections = append(v.Sections, checksSection(d), reviewsSection(r, d))
	}
	if body := strings.TrimSpace(d.Body); body != "" {
		v.Sections = append(v.Sections, wire.Section{Title: "描述", Items: []wire.Component{
			{Type: wire.ComponentMarkdown, Key: "body", Text: clip(body, bodyMost)}}})
	}
	if len(d.Comments) > 0 {
		sec := wire.Section{Title: fmt.Sprintf("最近的评论 · 共 %d 条", d.CommentCount)}
		for i, cm := range d.Comments {
			sec.Items = append(sec.Items,
				wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("c%d-who", i), Title: cm.Author, Subtitle: ago(p.now(), cm.At), Avatar: avatar(r, cm.Avatar)},
				wire.Component{Type: wire.ComponentMarkdown, Key: fmt.Sprintf("c%d", i), Text: clip(nonEmpty(strings.TrimSpace(cm.Body), "（空）"), commentMost)})
		}
		v.Sections = append(v.Sections, sec)
	}

	tail := []wire.Component{}
	if u, ok := httpsURL(d.URL); ok {
		tail = append(tail, wire.Component{Type: wire.ComponentLink, Key: "web", Title: "在 GitHub 打开", URL: u})
	}
	tail = append(tail,
		wire.Component{Type: wire.ComponentText, Key: "kb", Style: wire.TextStyleCaption,
			Text: "和你有关的 PR / issue 会归档进知识库，会话是 " + archiveID(d.Repo, d.Number) + "（GitHub 连接器的「进知识库」开着时）。要回复或 review，在收件箱里点这一条，或者让 Persona 去做。"},
		wire.Component{Type: wire.ComponentButton, Key: "refresh", Title: "刷新", Action: &wire.Action{ID: "refresh"}})
	if r.UIVersion < 3 {
		tail = append(tail, back)
	}
	v.Sections = append(v.Sections, wire.Section{Items: tail})
	return v
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
	sec := wire.Section{Title: "Review"}
	for _, rv := range d.Reviews {
		n := reviewNames[rv.State]
		if n.name == "" {
			n.name, n.tone = rv.State, wire.ToneNeutral
		}
		sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Key: "review-" + rv.Login, Title: rv.Login,
			Badge: &wire.Badge{Text: n.name, Tone: n.tone}})
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
