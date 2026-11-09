package main

import "strings"

type texts struct {
	knowledge, books, noBooks, pages, noPages, search, more, back, missingConfig string
	open, create, edit, save, review, name, content, noContent, noMarkdown       string
}

var locales = map[string]*texts{
	"en": {
		knowledge: "BookStack knowledge", books: "Books", noBooks: "No visible books.", pages: "%d pages", noPages: "No pages in this book.", search: "Search documentation", more: "Load more", back: "Back", missingConfig: "Set the BookStack instance URL, Token ID and Token Secret in Settings › Extensions first.",
		open: "Open in BookStack", create: "New page", edit: "Edit Markdown", save: "Save page", review: "Review", name: "Title", content: "Markdown", noContent: "No readable content.", noMarkdown: "This page has no Markdown source. Edit it in BookStack to avoid an implicit HTML conversion.",
	},
	"ja": {
		knowledge: "BookStack ナレッジ", books: "ブック", noBooks: "表示できるブックはありません。", pages: "%d 件のページ", noPages: "このブックにページはありません。", search: "ドキュメントを検索", more: "さらに読み込む", back: "戻る", missingConfig: "設定 › 拡張機能で BookStack インスタンス URL、Token ID と Token Secret を設定してください。",
		open: "BookStack で開く", create: "新しいページ", edit: "Markdown を編集", save: "ページを保存", review: "確認", name: "タイトル", content: "Markdown", noContent: "読める本文はありません。", noMarkdown: "このページには Markdown ソースがありません。HTML を暗黙に変換しないよう BookStack で編集してください。",
	},
	"zh-Hans": {
		knowledge: "BookStack 知识库", books: "书籍", noBooks: "没有可见的书籍。", pages: "%d 个页面", noPages: "这本书里没有页面。", search: "搜索文档", more: "加载更多", back: "返回", missingConfig: "先在「设置 › 扩展」中填写 BookStack 实例地址、Token ID 和 Token Secret。",
		open: "在 BookStack 中打开", create: "新建页面", edit: "编辑 Markdown", save: "保存页面", review: "预览", name: "标题", content: "Markdown", noContent: "没有可阅读的内容。", noMarkdown: "这个页面没有 Markdown 源文。为避免隐式转换 HTML，请在 BookStack 中编辑。",
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
