package page

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page's writes (nwverse/persona#2951「写操作」): a comment, a review (comment, approve, request changes), a comment
// on a line, a reply to a conversation on a line, a merge. Each is filled in, then shown as it will be sent (where,
// what, who will see it), and sent only with the preview's button, which the app asks about once more (its confirm).
// The assistant's writes still go through the hub's gate.

// writeMost is how many characters a write has (a field's value is at most 2000).
const writeMost = 2000

var reviewEvents = []wire.Choice{{Value: "COMMENT", Title: "只评论"}, {Value: "APPROVE", Title: "批准"}, {Value: "REQUEST_CHANGES", Title: "要求修改"}}

var mergeMethods = []wire.Choice{{Value: "squash", Title: "Squash 合并"}, {Value: "merge", Title: "合并提交"}, {Value: "rebase", Title: "Rebase 合并"}}

var sides = []wire.Choice{{Value: "RIGHT", Title: "新文件（加的、没改的行）"}, {Value: "LEFT", Title: "旧文件（删掉的行）"}}

// write is the page of a write: the form, or (s.Preview) what will be sent. problem is why the last try did not go.
func (p *Page) write(ctx context.Context, r *ext.UIRequest, s state, problem string) *wire.View {
	ref := fmt.Sprintf("%s#%d", s.Repo, s.Number)
	v := &wire.View{Title: writeTitle(s), State: ext.EncodeState(s)}
	d, err := p.c.Detail(ctx, s.Repo, s.Number)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorEmpty("读不到 "+ref, err)}}}
		return v
	}
	target := []wire.Component{{Type: wire.ComponentRow, Key: "target", Title: clip(oneLine(d.Title), 300), Subtitle: ref, Avatar: avatar(r, d.Avatar)}}
	switch s.W {
	case "line", "reply":
		target = append(target, wire.Component{Type: wire.ComponentRow, Key: "where", Title: "位置", Value: clip(fmt.Sprintf("%s:%d", s.Path, s.Line), 300)})
	case "merge":
		if m := mergeLine(d); m.text != "" {
			target = append(target, wire.Component{Type: wire.ComponentRow, Key: "mergeable", Title: "现在", Value: m.text, Badge: &wire.Badge{Text: m.badge, Tone: m.tone}})
		}
		target = append(target, wire.Component{Type: wire.ComponentRow, Key: "into", Title: "合并", Value: clip(d.Head+" → "+d.Base, 300)})
	}
	if s.W == "reply" {
		if c, ok := p.conversationOf(ctx, s); ok {
			root := c.Root()
			target = append(target, wire.Component{Type: wire.ComponentText, Key: "quote", Style: wire.TextStyleSecondary,
				Text: clip(root.Author+"："+oneLine(root.Body), 300)})
		}
	}
	v.Sections = append(v.Sections, wire.Section{Title: "发到", Items: target})
	if problem != "" {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "problem", Title: "没有发出",
			Detail: clip(problem, 2000), Icon: wire.NavIconWarning}}})
	}
	if s.Preview {
		v.Sections = append(v.Sections, p.preview(r, s, d)...)
		return v
	}

	var form []wire.Component
	switch s.W {
	case "review":
		form = append(form, wire.Component{Type: wire.ComponentField, Key: "f-e", Name: "e", Label: "结论", Kind: wire.FieldKindChoice,
			Value: nonEmpty(s.Event, "COMMENT"), Choices: reviewEvents})
	case "merge":
		form = append(form, wire.Component{Type: wire.ComponentField, Key: "f-e", Name: "e", Label: "怎么合并", Kind: wire.FieldKindChoice,
			Value: nonEmpty(s.Event, "squash"), Choices: mergeMethods})
	case "line":
		line := ""
		if s.Line > 0 {
			line = strconv.Itoa(s.Line)
		}
		form = append(form,
			wire.Component{Type: wire.ComponentField, Key: "f-l", Name: "l", Label: "第几行（diff 里左边的行号）", Kind: wire.FieldKindNumber, Value: line, Placeholder: "比如 42"},
			wire.Component{Type: wire.ComponentField, Key: "f-s", Name: "s", Label: "哪一边", Kind: wire.FieldKindChoice, Value: nonEmpty(s.Side, "RIGHT"), Choices: sides})
	}
	if s.W != "merge" {
		label := "内容（Markdown）"
		if s.W == "review" {
			label = "总评（批准时可以不写）"
		}
		form = append(form, wire.Component{Type: wire.ComponentField, Key: "f-b", Name: "b", Label: label, Kind: wire.FieldKindText,
			Value: clip(s.Body, writeMost), Placeholder: "写点什么"})
	}
	form = append(form, wire.Component{Type: wire.ComponentButton, Key: "preview", Title: "预览", Action: &wire.Action{ID: "preview", Submit: true}})
	v.Sections = append(v.Sections, wire.Section{Items: form, Footer: "先预览，确认后才发到 GitHub"})
	return v
}

// preview is what will be sent, word for word, and the button that sends it.
func (p *Page) preview(r *ext.UIRequest, s state, d github.Detail) []wire.Section {
	var items []wire.Component
	switch s.W {
	case "review":
		items = append(items, wire.Component{Type: wire.ComponentRow, Key: "event", Title: "结论", Value: choiceTitle(reviewEvents, s.Event, s.Event)})
	case "merge":
		items = append(items, wire.Component{Type: wire.ComponentRow, Key: "event", Title: "方式", Value: choiceTitle(mergeMethods, s.Event, s.Event)})
	case "line":
		items = append(items, wire.Component{Type: wire.ComponentRow, Key: "side", Title: "哪一边", Value: choiceTitle(sides, s.Side, s.Side)})
	}
	if strings.TrimSpace(s.Body) != "" {
		items = append(items, markdownItem(r, "body", s.Body, "", writeMost))
	}
	who := "能看到 " + s.Repo + " 的人都能看到，以你的 GitHub 账号发出"
	if s.W == "merge" {
		who = "合并后 " + d.Base + " 会多出这些改动，GitHub 上撤不回来（只能再提一个 revert）"
	}
	items = append(items,
		wire.Component{Type: wire.ComponentText, Key: "who", Text: who, Style: wire.TextStyleCaption},
		wire.Component{Type: wire.ComponentButton, Key: "send", Title: sendTitle(s), Action: &wire.Action{ID: "send", Confirm: confirmText(s, d)}},
		wire.Component{Type: wire.ComponentButton, Key: "edit", Title: "改一改", Action: &wire.Action{ID: "edit"}})
	return []wire.Section{{Title: "要发的", Items: items}}
}

// writeAction answers the write page's buttons: preview, edit, send.
func (p *Page) writeAction(ctx context.Context, r *ext.UIRequest, s state, kind string) (*wire.ActionResult, error) {
	if s.V != viewWrite {
		return nil, nil
	}
	switch kind {
	case "edit":
		s.Preview = false
		return &wire.ActionResult{View: p.write(ctx, r, s, "")}, nil
	case "preview":
		s.Body = strings.TrimSpace(strings.ReplaceAll(r.Values["b"], "\r\n", "\n"))
		if e, ok := r.Values["e"]; ok {
			s.Event = e
		}
		if l, ok := r.Values["l"]; ok {
			s.Line, _ = strconv.Atoi(strings.TrimSpace(l))
		}
		if side, ok := r.Values["s"]; ok {
			s.Side = side
		}
		if problem := p.check0(ctx, &s); problem != "" {
			s.Preview = false
			return &wire.ActionResult{View: p.write(ctx, r, s, problem)}, nil
		}
		s.Preview = true
		return &wire.ActionResult{View: p.write(ctx, r, s, "")}, nil
	}
	// send
	if !s.Preview {
		return &wire.ActionResult{View: p.write(ctx, r, s, ""), Toast: "先预览再发"}, nil
	}
	if problem := p.check0(ctx, &s); problem != "" {
		s.Preview = false
		return &wire.ActionResult{View: p.write(ctx, r, s, problem)}, nil
	}
	var err error
	switch s.W {
	case "comment":
		_, err = p.c.PostComment(ctx, s.Repo, s.Number, s.Body)
	case "review":
		_, err = p.c.PostReview(ctx, s.Repo, s.Number, s.Event, s.Body)
	case "line":
		_, err = p.c.PostLineComment(ctx, s.Repo, s.Number, s.Path, s.Line, s.Side, s.Body)
	case "reply":
		_, err = p.c.PostReply(ctx, s.Repo, s.Number, s.ID, s.Body)
	case "merge":
		_, err = p.c.Merge(ctx, s.Repo, s.Number, s.Event, s.Ref)
	default:
		return nil, nil
	}
	if err != nil {
		return &wire.ActionResult{View: p.write(ctx, r, s, github.Scrub(err.Error())), Toast: "没有发出"}, nil
	}
	p.c.ForgetDetail(s.Repo, s.Number)
	p.invalidate()
	return &wire.ActionResult{Pop: true, Toast: nonEmpty(map[string]string{"merge": "已合并", "review": "review 已提交"}[s.W], "已发出")}, nil
}

// check0 is why s cannot be sent as it is ("" when it can); a merge also takes the head it was shown with.
func (p *Page) check0(ctx context.Context, s *state) string {
	if utf8.RuneCountInString(s.Body) > writeMost {
		return fmt.Sprintf("最多 %d 字，长的去 GitHub 上写", writeMost)
	}
	switch s.W {
	case "comment", "reply":
		if s.Body == "" {
			return "还没写内容"
		}
	case "review":
		if !hasChoice(reviewEvents, s.Event) {
			return "选一个结论"
		}
		if s.Body == "" && s.Event != "APPROVE" {
			return "只评论、要求修改时要写总评"
		}
	case "line":
		if s.Body == "" {
			return "还没写内容"
		}
		if s.Line <= 0 {
			return "填上第几行"
		}
		if s.Side != "LEFT" && s.Side != "RIGHT" {
			return "选哪一边"
		}
		f, err := p.c.PRFileAt(ctx, s.Repo, s.Number, s.Path)
		if err != nil {
			return github.Scrub(err.Error())
		}
		if !hasLine(f.Patch, s.Line, s.Side) {
			return fmt.Sprintf("第 %d 行不在这次的改动里：只能评论 diff 里出现的行（删掉的行选「旧文件」）", s.Line)
		}
	case "merge":
		if !hasChoice(mergeMethods, s.Event) {
			return "选一种合并方式"
		}
		d, err := p.c.Detail(ctx, s.Repo, s.Number)
		if err != nil {
			return github.Scrub(err.Error())
		}
		if d.State != "open" {
			return "这个 PR 现在不是开着的"
		}
		if !s.Preview { // the head the preview shows is the one merged
			s.Ref = d.HeadSHA
		} else if s.Ref != d.HeadSHA {
			s.Ref = d.HeadSHA
			return "预览之后又有人推了新提交，再看一眼再合并"
		}
	default:
		return "不认识的操作"
	}
	return ""
}

// conversationOf is the conversation a reply answers.
func (p *Page) conversationOf(ctx context.Context, s state) (github.Conversation, bool) {
	convs, err := p.c.Conversations(ctx, s.Repo, s.Number)
	if err != nil {
		return github.Conversation{}, false
	}
	for _, c := range convs {
		if c.Root().ID == s.ID {
			return c, true
		}
	}
	return github.Conversation{}, false
}

func writeTitle(s state) string {
	return map[string]string{"comment": "评论", "review": "提交 review", "line": "评论一行", "reply": "回复", "merge": "合并"}[s.W]
}

func sendTitle(s state) string {
	switch s.W {
	case "merge":
		return "合并"
	case "review":
		return "提交 review"
	}
	return "发出"
}

// confirmText is what the app asks before sending: what, where.
func confirmText(s state, d github.Detail) string {
	ref := fmt.Sprintf("%s#%d", s.Repo, s.Number)
	switch s.W {
	case "review":
		return fmt.Sprintf("在 %s 上%s？", ref, map[string]string{"COMMENT": "提交 review（只评论）", "APPROVE": "批准", "REQUEST_CHANGES": "要求修改"}[s.Event])
	case "merge":
		return fmt.Sprintf("用「%s」把 %s 合并进 %s？", choiceTitle(mergeMethods, s.Event, s.Event), ref, d.Base)
	case "line":
		return fmt.Sprintf("在 %s 的 %s 第 %d 行发评论？", ref, s.Path, s.Line)
	case "reply":
		return fmt.Sprintf("在 %s 的 %s 回复？", ref, s.Path)
	}
	return "评论发到 " + ref + "？"
}
