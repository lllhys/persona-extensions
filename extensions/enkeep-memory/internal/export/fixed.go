package export

import (
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
)

// Fixed wording of the exits: headings and the fixed lines, by language. A language without its own set uses
// English; the entries' lines are always in the target language (they are rewritten into it).
type words struct {
	Title      string
	Sections   map[string]string // exit A section → heading
	ToolsIndex string            // "%s": the objects
	PeopleNote string            // fixed line: people pages' inferred and evaluation lines are for reference only
	AskHint    string            // exit F
	Due        string            // "%s": the due date
	SingleSrc  string
	ToolsSkill string // heading of the tools skill
	ToolsDesc  string // "%s": machine and tool names
	WorkTitle  string // "%s": the kind of task
	WorkDesc   string // "%s": the kind of task
	General    string // the object of ② entries without one
	Project    string // "%s": the project
	ProjParts  map[string]string
	InboxParts map[string]string
	With       string // "%s": a person (a tip's lead)
	Contacts   string // "%s": the people the user deals with most
}

var fixedWords = map[string]words{
	"zh": {
		Title: "# 关于用户（由 enkeep-memory 整理）",
		Sections: map[string]string{"interaction": "## 和用户打交道", "tools": "## 机器和工具", "judgement": "## 默认判断",
			"workflow": "## 做事方式", "world": "## 开着的承诺", "persona": "## 关于用户"},
		ToolsIndex: "机器和工具的用法、坑和边界在技能 enkeep-machines-and-tools 里：%s。",
		PeopleNote: "人物页里标了推断和评价的内容只供参考，不对外转述。",
		AskHint:    "请示用户时，如果上面的默认判断里有同类岔口，附一句「按你以往会选 X，因为……」，X 和理由取自那条判断。",
		Due:        "（到期 %s）", SingleSrc: "（单一来源）",
		ToolsSkill: "# 机器和工具", ToolsDesc: "用户的机器和工具手册：怎么连、怎么用、哪里会踩坑、不能用来做什么。涉及这些机器或工具时读：%s",
		WorkTitle: "# 处理「%s」这类事情", WorkDesc: "用户处理「%s」这类事情时的做法和理由。开始这类事情时读。",
		General: "通用", Project: "# 项目背景：%s",
		ProjParts: map[string]string{"tools": "## 机器和工具", "judgement": "## 技术取舍", "workflow": "## 做事方式", "world": "## 进展、卡点、负责人和承诺"},
		InboxParts: map[string]string{"world": "## 重要的人和未完的承诺", "people": "## 和这些人沟通要注意", "persona": "## 用户的身份和沟通方式",
			"interaction": "## 沟通上的要求"},
		With: "和 %s：", Contacts: "来往最多的人：%s",
	},
	"en": {
		Title: "# About the user (curated by enkeep-memory)",
		Sections: map[string]string{"interaction": "## Working with the user", "tools": "## Machines and tools", "judgement": "## Default judgements",
			"workflow": "## Ways of working", "world": "## Open commitments", "persona": "## About the user"},
		ToolsIndex: "How to use the machines and tools, their pitfalls and limits are in the skill enkeep-machines-and-tools: %s.",
		PeopleNote: "Lines marked inferred or evaluation on people pages are for reference only; do not pass them on.",
		AskHint:    "When asking the user, if a default judgement above covers the same kind of fork, add \"you would usually pick X, because …\", with X and the reason from that judgement.",
		Due:        " (due %s)", SingleSrc: " (single source)",
		ToolsSkill: "# Machines and tools", ToolsDesc: "The user's handbook of machines and tools: how to reach and use them, pitfalls, limits. Read when working with: %s",
		WorkTitle: "# Handling %s", WorkDesc: "How the user handles %s, and why. Read when starting this kind of task.",
		General: "General", Project: "# Project context: %s",
		ProjParts: map[string]string{"tools": "## Machines and tools", "judgement": "## Technical trade-offs", "workflow": "## Ways of working",
			"world": "## Progress, blockers, owners and commitments"},
		InboxParts: map[string]string{"world": "## People who matter and open commitments", "people": "## Communicating with them",
			"persona": "## Who the user is and how they communicate", "interaction": "## Communication requirements"},
		With: "With %s: ", Contacts: "People the user deals with most: %s",
	},
}

func fixed(lang string) words {
	if w, ok := fixedWords[norm.Base(lang)]; ok {
		return w
	}
	return fixedWords["en"]
}

// list joins names the language's way.
func list(lang string, xs []string) string {
	if norm.Base(lang) == "zh" || norm.Base(lang) == "ja" {
		return strings.Join(xs, "、")
	}
	return strings.Join(xs, ", ")
}
