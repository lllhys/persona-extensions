package main

import "strings"

type texts struct {
	reading, unread, starred, search, results, noLinks, back, more, missingConfig string
	open, markRead, star, unstar, article, highlights, noHighlights, noContent    string
}

var locales = map[string]*texts{
	"en": {
		reading: "GoodLinks reading", unread: "Unread", starred: "Starred", search: "Search your library", results: "%d links", noLinks: "No links in this reading view.", back: "Back", more: "Load more", missingConfig: "Set the local GoodLinks API token in Settings › Extensions first.",
		open: "Open original article", markRead: "Mark read", star: "Star", unstar: "Remove star", article: "Cached article", highlights: "Highlights", noHighlights: "No highlights.", noContent: "No cached plaintext content is available for this link.",
	},
	"ja": {
		reading: "GoodLinks リーディング", unread: "未読", starred: "スター付き", search: "ライブラリを検索", results: "%d 件のリンク", noLinks: "この表示にリンクはありません。", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能でローカル GoodLinks API トークンを設定してください。",
		open: "元の記事を開く", markRead: "既読にする", star: "スターを付ける", unstar: "スターを解除", article: "キャッシュされた記事", highlights: "ハイライト", noHighlights: "ハイライトはありません。", noContent: "このリンクにはキャッシュされた本文がありません。",
	},
	"zh-Hans": {
		reading: "GoodLinks 阅读", unread: "未读", starred: "收藏", search: "搜索自己的阅读库", results: "%d 个链接", noLinks: "这个阅读视图里没有链接。", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写本机 GoodLinks API token。",
		open: "打开原文", markRead: "标记已读", star: "收藏", unstar: "取消收藏", article: "缓存正文", highlights: "划线", noHighlights: "暂无划线。", noContent: "这个链接没有可读取的缓存纯文本正文。",
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
