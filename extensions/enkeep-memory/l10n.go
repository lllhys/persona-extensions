package main

import (
	"fmt"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
)

// tr is the page's language: the request's (ui.render carries the user's language), else the hub's, else Chinese
// (the hub's default). Every page text of the v4 pages is written in both languages side by side (zh, en): there is
// no l10n table in this extension, and keeping the two next to each other keeps them saying the same thing.
type tr struct{ en bool }

// trOf picks the language of a locale ("zh-Hans", "en", "en-US", "ja"…): Chinese for zh, English for anything else
// that is set (the hub falls back to English for languages it has no text in); empty is the hub's default, Chinese.
func trOf(locales ...string) tr {
	for _, l := range locales {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		return tr{en: !strings.HasPrefix(l, "zh")}
	}
	return tr{}
}

// s is the text in the page's language.
func (t tr) s(zh, en string) string {
	if t.en {
		return en
	}
	return zh
}

// f formats the text in the page's language.
func (t tr) f(zh, en string, args ...any) string { return fmt.Sprintf(t.s(zh, en), args...) }

// code is the language code written into entries made from the page (the first-run answers).
func (t tr) code() string { return map[bool]string{true: "en", false: "zh"}[t.en] }

// facetLabel names a facet with its number (①…⑥, ⑤′).
func (t tr) facetLabel(f entry.Facet) string {
	switch f {
	case entry.Interaction:
		return t.s("① 和 AI 的交互", "① Working with the AI")
	case entry.Tools:
		return t.s("② AI 和工具", "② The AI and its tools")
	case entry.Judgement:
		return t.s("③ 默认判断", "③ Default judgements")
	case entry.Workflow:
		return t.s("④ 处理方式", "④ Ways of handling things")
	case entry.World:
		return t.s("⑤ 人、项目和事实", "⑤ People, projects and facts")
	case entry.People:
		return t.s("⑤′ 有来往的人", "⑤′ People you deal with")
	case entry.Persona:
		return t.s("⑥ 关于你", "⑥ About you")
	}
	return string(f)
}

// facetShort is the facet's number alone, for compact controls.
func facetShort(f entry.Facet) string {
	return map[entry.Facet]string{entry.Interaction: "①", entry.Tools: "②", entry.Judgement: "③", entry.Workflow: "④",
		entry.World: "⑤", entry.People: "⑤′", entry.Persona: "⑥"}[f]
}

func (t tr) grainLabel(g entry.Grain) string {
	switch g {
	case entry.Abstract:
		return t.s("抽象", "Abstract")
	case entry.Class:
		return t.s("一类", "Kinds of situations")
	case entry.Action:
		return t.s("具体", "Specific")
	}
	return string(g)
}

func (t tr) statusLabel(s entry.Status) string {
	switch s {
	case entry.Candidate:
		return t.s("正在攒", "collecting")
	case entry.Stable:
		return t.s("稳定", "stable")
	case entry.Shifting:
		return t.s("在变", "shifting")
	case entry.Dormant:
		return t.s("休眠", "dormant")
	case entry.Retired:
		return t.s("废弃", "retired")
	}
	return string(s)
}

func (t tr) markLabel(m string) string {
	switch m {
	case "right":
		return t.s("对", "Right")
	case "wrong":
		return t.s("不对", "Wrong")
	case "unsure":
		return t.s("说不清", "Not sure")
	case "unsupported":
		return t.s("出处不支持", "Not in the source")
	}
	return m
}

// sourceLabel names a source on the sources card: its code's kind, and the connector for messages.
func (t tr) sourceLabel(code, name string) string {
	switch code {
	case entry.SrcAssistant:
		return t.s("Persona 助手对话", "Persona assistant chats")
	case entry.SrcAsk:
		return t.s("请示卡片的问答", "Questions the AI asked you (cards)")
	case entry.SrcAgent:
		return t.s("Agent 会话", "Agent sessions")
	case entry.SrcTools:
		return t.s("工具调用片段（按不顺信号挖）", "Tool-call segments (dug on friction signals)")
	case entry.SrcTodo:
		return t.s("待办和决定", "To-dos and decisions")
	case entry.SrcMemory:
		return t.s("助手的手写记忆", "The assistant's hand-written memory")
	case entry.SrcAbout:
		return t.s("「关于我」", "\"About me\"")
	case entry.SrcChat:
		return t.s("消息来源 · ", "Messages · ") + name
	case entry.SrcKB:
		return t.s("知识库", "Knowledge base")
	case entry.SrcRecording:
		return t.s("录音和纪要", "Recordings and minutes")
	}
	if name != "" {
		return code + " " + name
	}
	return code
}

// missingLabel says why a source is not there (entry.SourceStatus.Missing), in plain words.
func (t tr) missingLabel(st *entry.SourceStatus) string {
	switch st.Missing {
	case "no_scope":
		return t.s("没授权", "not granted")
	case "waiting_hub":
		return t.f("等 hub（%s）", "waiting for the hub (%s)", st.Waiting)
	case "offline":
		return t.s("助手电脑离线", "the assistant's computer is offline")
	case "not_connected":
		return t.s("没接", "not connected")
	case "empty":
		return t.s("还没有内容", "nothing in it yet")
	case "off":
		return t.s("你关掉了", "turned off by you")
	case "error":
		return t.s("读取出错", "could not be read")
	}
	if st.Waiting != "" {
		return t.f("等 hub（%s）", "waiting for the hub (%s)", st.Waiting)
	}
	return st.Missing
}

func (t tr) exitLabel(x string) string {
	switch x {
	case "A":
		return t.s("A 每轮说明块", "A per-turn instructions")
	case "B":
		return t.s("B 技能", "B skills")
	case "C":
		return t.s("C 知识库", "C knowledge base")
	case "D":
		return t.s("D Agent 项目背景", "D agent project context")
	case "E":
		return t.s("E 收件箱分拣和起草", "E inbox triage and drafting")
	case "F":
		return t.s("F 请示时附建议", "F suggestions on questions")
	}
	return x
}

func (t tr) partLabel(p string) string {
	switch p {
	case entry.PartFact:
		return t.s("事实", "Facts")
	case entry.PartUserEval:
		return t.s("你说过的评价（原话）", "What you said about them (your words)")
	case entry.PartInferred:
		return t.s("推断的侧写", "Inferred profile")
	case entry.PartCommitment:
		return t.s("承诺", "Commitments")
	}
	return p
}

func (t tr) sensitiveLabel(c string) string {
	switch c {
	case entry.SensEmotion:
		return t.s("情感", "emotions")
	case entry.SensFinance:
		return t.s("财务", "money")
	case entry.SensHealth:
		return t.s("健康", "health")
	case entry.SensFamily:
		return t.s("家庭", "family")
	}
	return c
}

func (t tr) tieLabel(k string) string {
	switch k {
	case "dm":
		return t.s("私聊", "direct chats")
	case "group":
		return t.s("群里来回", "group exchanges")
	case "mail":
		return t.s("邮件", "mail")
	case "meeting":
		return t.s("同会", "meetings")
	case "commitment":
		return t.s("承诺", "commitments")
	}
	return k
}
