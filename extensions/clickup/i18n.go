package main

import "strings"

type texts struct {
	tasks, noTasks, teams, spaces, lists, create, back, more, missingConfig string
	name, description, status, open, review, update, list                   string
}

var locales = map[string]*texts{
	"en": {
		tasks: "My ClickUp tasks", noTasks: "No assigned open tasks in this workspace.", teams: "Workspaces", spaces: "Spaces", lists: "Lists", create: "Create task", back: "Back", more: "Load more", missingConfig: "Set the ClickUp personal API key in Settings › Extensions first.",
		name: "Title", description: "Description", status: "Status", open: "Open in ClickUp", review: "Review", update: "Update status", list: "List",
	},
	"ja": {
		tasks: "ClickUp の自分のタスク", noTasks: "このワークスペースに自分の未完了タスクはありません。", teams: "ワークスペース", spaces: "スペース", lists: "リスト", create: "タスクを作成", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で ClickUp 個人 API キーを設定してください。",
		name: "タイトル", description: "説明", status: "ステータス", open: "ClickUp で開く", review: "確認", update: "ステータスを更新", list: "リスト",
	},
	"zh-Hans": {
		tasks: "我的 ClickUp 任务", noTasks: "这个工作区里没有分配给你的未完成任务。", teams: "工作区", spaces: "空间", lists: "列表", create: "创建任务", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 ClickUp 个人 API key。",
		name: "标题", description: "描述", status: "状态", open: "在 ClickUp 中打开", review: "预览", update: "更新状态", list: "列表",
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
