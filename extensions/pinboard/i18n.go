package main

import "strings"

type texts struct {
	bookmarks, recent, archive, noBookmarks, create, back, more, missingConfig string
	title, url, notes, tags, open, review                                      string
}

var locales = map[string]*texts{
	"en": {
		bookmarks: "Pinboard bookmarks", recent: "Recent", archive: "Archive", noBookmarks: "No bookmarks in this view.", create: "Add read-later", back: "Back", more: "Load more", missingConfig: "Set the Pinboard API token in Settings › Extensions first.",
		title: "Title", url: "HTTPS URL", notes: "Notes", tags: "Tags (space-separated)", open: "Open bookmark", review: "Review",
	},
	"ja": {
		bookmarks: "Pinboard ブックマーク", recent: "最近", archive: "アーカイブ", noBookmarks: "この表示にブックマークはありません。", create: "あとで読むに追加", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Pinboard API トークンを設定してください。",
		title: "タイトル", url: "HTTPS URL", notes: "メモ", tags: "タグ（空白区切り）", open: "ブックマークを開く", review: "確認",
	},
	"zh-Hans": {
		bookmarks: "Pinboard 书签", recent: "最近", archive: "归档", noBookmarks: "这个视图里没有书签。", create: "添加稍后读", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Pinboard API token。",
		title: "标题", url: "HTTPS URL", notes: "备注", tags: "标签（空格分隔）", open: "打开书签", review: "预览",
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
