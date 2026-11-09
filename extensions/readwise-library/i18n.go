package main

import "strings"

type texts struct {
	library, books, noBooks, highlights, noHighlights, back, more, missingConfig   string
	open, create, edit, save, addTag, review, text, title, author, note, sourceURL string
}

var locales = map[string]*texts{
	"en": {
		library: "Readwise highlights", books: "Books", noBooks: "No books in this view.", highlights: "%d highlights", noHighlights: "No highlights for this book.", back: "Back", more: "Load more", missingConfig: "Set the Readwise API token in Settings › Extensions first.",
		open: "Open in Readwise", create: "New highlight", edit: "Edit note", save: "Save", addTag: "Add book tag", review: "Review", text: "Highlight text", title: "Source title", author: "Author", note: "Note", sourceURL: "HTTPS source URL (optional)",
	},
	"ja": {
		library: "Readwise ハイライト", books: "ブック", noBooks: "この表示にブックはありません。", highlights: "%d 件のハイライト", noHighlights: "このブックにハイライトはありません。", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Readwise API トークンを設定してください。",
		open: "Readwise で開く", create: "新しいハイライト", edit: "メモを編集", save: "保存", addTag: "ブックにタグを追加", review: "確認", text: "ハイライト本文", title: "ソースのタイトル", author: "著者", note: "メモ", sourceURL: "HTTPS ソース URL（任意）",
	},
	"zh-Hans": {
		library: "Readwise 划线库", books: "书籍", noBooks: "这个视图里没有书籍。", highlights: "%d 条划线", noHighlights: "这本书里没有划线。", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Readwise API token。",
		open: "在 Readwise 中打开", create: "新建划线", edit: "编辑备注", save: "保存", addTag: "给书籍加标签", review: "预览", text: "划线文本", title: "来源标题", author: "作者", note: "备注", sourceURL: "HTTPS 来源链接（可选）",
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
