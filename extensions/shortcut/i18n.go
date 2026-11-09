package main

import "strings"

type texts struct {
	stories, noStories, create, back, more, missingConfig, name, description   string
	state, deadline, typeLabel, open, edit, review, save, comments, noComments string
	feature, bug, chore                                                        string
}

var locales = map[string]*texts{
	"en": {
		stories: "My Shortcut stories", noStories: "No incomplete stories owned by you.", create: "Create story", back: "Back", more: "Load more", missingConfig: "Set the Shortcut API token in Settings › Extensions first.", name: "Title", description: "Description",
		state: "Workflow state", deadline: "Deadline (YYYY-MM-DD)", typeLabel: "Type", open: "Open in Shortcut", edit: "Update state", review: "Review", save: "Save state", comments: "Comments", noComments: "No comments.",
		feature: "Feature", bug: "Bug", chore: "Chore",
	},
	"ja": {
		stories: "Shortcut の自分のストーリー", noStories: "自分が担当する未完了のストーリーはありません。", create: "ストーリーを作成", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Shortcut API トークンを設定してください。", name: "タイトル", description: "説明",
		state: "ワークフロー状態", deadline: "期限 (YYYY-MM-DD)", typeLabel: "種類", open: "Shortcut で開く", edit: "状態を更新", review: "確認", save: "状態を保存", comments: "コメント", noComments: "コメントはありません。",
		feature: "機能", bug: "バグ", chore: "雑務",
	},
	"zh-Hans": {
		stories: "我的 Shortcut 故事", noStories: "没有你负责的未完成故事。", create: "创建故事", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Shortcut API token。", name: "标题", description: "描述",
		state: "工作流状态", deadline: "截止日期 (YYYY-MM-DD)", typeLabel: "类型", open: "在 Shortcut 中打开", edit: "更新状态", review: "预览", save: "保存状态", comments: "评论", noComments: "暂无评论。",
		feature: "功能", bug: "缺陷", chore: "杂务",
	},
}

func textsFor(locale string) *texts {
	if t := locales[locale]; t != nil {
		return t
	}
	if i := strings.IndexAny(locale, "-_"); i > 0 {
		if t := locales[locale[:i]]; t != nil {
			return t
		}
	}
	return locales["en"]
}
