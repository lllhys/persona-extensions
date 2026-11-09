package main

import "strings"

type texts struct {
	reading, unread, starred, search, results, noEntries, back, more, missingConfig string
	open, markRead, bookmark, unbookmark, author, feed, article, noContent          string
}

var locales = map[string]*texts{
	"en": {
		reading: "Miniflux reading", unread: "Unread", starred: "Starred", search: "Search entries", results: "%d entries", noEntries: "No entries in this reading view.", back: "Back", more: "Load more", missingConfig: "Set the Miniflux instance URL and API key in Settings › Extensions first.",
		open: "Open original article", markRead: "Mark read", bookmark: "Bookmark", unbookmark: "Remove bookmark", author: "Author", feed: "Feed", article: "Article", noContent: "No readable article text.",
	},
	"ja": {
		reading: "Miniflux リーディング", unread: "未読", starred: "スター付き", search: "記事を検索", results: "%d 件の記事", noEntries: "この表示に記事はありません。", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Miniflux インスタンス URL と API キーを設定してください。",
		open: "元の記事を開く", markRead: "既読にする", bookmark: "ブックマーク", unbookmark: "ブックマークを解除", author: "著者", feed: "フィード", article: "記事", noContent: "読める本文はありません。",
	},
	"zh-Hans": {
		reading: "Miniflux 阅读", unread: "未读", starred: "收藏", search: "搜索文章", results: "%d 篇文章", noEntries: "这个阅读视图里没有文章。", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Miniflux 实例地址和 API key。",
		open: "打开原文", markRead: "标记已读", bookmark: "收藏", unbookmark: "取消收藏", author: "作者", feed: "订阅", article: "正文", noContent: "没有可阅读的正文。",
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
