package main

import "strings"

type texts struct {
	notes, recent, pinned, search, results, noNotes, back, more, missingConfig string
	create, edit, save, pin, unpin, review, content, noContent                 string
}

var locales = map[string]*texts{
	"en": {
		notes: "Memos notes", recent: "Recent", pinned: "Pinned", search: "Search your notes", results: "%d notes", noNotes: "No notes in this view.", back: "Back", more: "Load more", missingConfig: "Set the Memos instance URL, username and access token in Settings › Extensions first.",
		create: "New private note", edit: "Edit note", save: "Save note", pin: "Pin", unpin: "Unpin", review: "Review", content: "Markdown content", noContent: "No content.",
	},
	"ja": {
		notes: "Memos ノート", recent: "最近", pinned: "ピン留め", search: "自分のノートを検索", results: "%d 件のノート", noNotes: "この表示にノートはありません。", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Memos インスタンス URL、ユーザー名とアクセストークンを設定してください。",
		create: "新しいプライベートノート", edit: "ノートを編集", save: "ノートを保存", pin: "ピン留め", unpin: "ピン留めを解除", review: "確認", content: "Markdown 本文", noContent: "本文はありません。",
	},
	"zh-Hans": {
		notes: "Memos 笔记", recent: "最近", pinned: "置顶", search: "搜索自己的笔记", results: "%d 条笔记", noNotes: "这个视图里没有笔记。", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Memos 实例地址、用户名和访问令牌。",
		create: "新建私有笔记", edit: "编辑笔记", save: "保存笔记", pin: "置顶", unpin: "取消置顶", review: "预览", content: "Markdown 内容", noContent: "暂无内容。",
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
