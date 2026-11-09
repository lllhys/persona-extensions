package main

import "strings"

type texts struct {
	items, boards, noBoards, noItems, create, back, more, missingConfig string
	name, status, open, review, update, column, label                   string
}

var locales = map[string]*texts{
	"en": {
		items: "monday items", boards: "Boards", noBoards: "No accessible boards.", noItems: "No items in this board.", create: "Create item", back: "Back", more: "Load more", missingConfig: "Set the monday personal V2 API token in Settings › Extensions first.",
		name: "Title", status: "Status", open: "Open in monday", review: "Review", update: "Update status", column: "Status column ID", label: "Existing label",
	},
	"ja": {
		items: "monday 作業項目", boards: "ボード", noBoards: "アクセスできるボードはありません。", noItems: "このボードに項目はありません。", create: "項目を作成", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で monday 個人 V2 API トークンを設定してください。",
		name: "タイトル", status: "ステータス", open: "monday で開く", review: "確認", update: "ステータスを更新", column: "ステータス列 ID", label: "既存のラベル",
	},
	"zh-Hans": {
		items: "monday 工作项", boards: "Boards", noBoards: "没有可访问的 board。", noItems: "这个 board 里没有工作项。", create: "创建工作项", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 monday 个人 V2 API token。",
		name: "标题", status: "状态", open: "在 monday 中打开", review: "预览", update: "更新状态", column: "状态列 ID", label: "已有标签",
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
