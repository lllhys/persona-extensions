package main

import "strings"

// texts is UI copy, not the user's bookmarks (which retain their original language).
type texts struct {
	recent, all, unsorted, search, collection, results, more, tags      string
	save, saved, detail, note, highlights, favorite                     string
	open, back, missingToken, none, unavailable, writeDenied, savedHere string
	review, confirmSave, titleOptional, noteOptional                    string
	placeholders                                                        struct{ search, url, tags, title, note string }
}

var locales = map[string]*texts{
	"en": {
		recent: "Recent bookmarks", all: "All bookmarks", unsorted: "Unsorted", search: "Search bookmarks",
		collection: "Collection", results: "%d bookmarks", more: "Load more", tags: "Tags",
		save: "Save a link", saved: "Saved", detail: "Bookmark", note: "Note", highlights: "Highlights", favorite: "Favorite",
		open: "Open link", back: "Back", missingToken: "Add your Raindrop access token in Settings › Extensions first.",
		none: "No bookmarks found.", unavailable: "Raindrop could not be reached. Try again later.", writeDenied: "The save was not approved.",
		savedHere: "Saved to %s.", review: "Review and save", confirmSave: "Confirm save", titleOptional: "Title (optional)", noteOptional: "Note (optional)",
	},
	"ja": {
		recent: "最近のブックマーク", all: "すべてのブックマーク", unsorted: "未分類", search: "ブックマークを検索",
		collection: "コレクション", results: "%d 件のブックマーク", more: "さらに読み込む", tags: "タグ",
		save: "リンクを保存", saved: "保存しました", detail: "ブックマーク", note: "メモ", highlights: "ハイライト", favorite: "お気に入り",
		open: "リンクを開く", back: "戻る", missingToken: "設定 › 拡張機能で Raindrop アクセストークンを追加してください。",
		none: "ブックマークが見つかりません。", unavailable: "Raindrop に接続できません。しばらくしてから再試行してください。", writeDenied: "保存は承認されませんでした。",
		savedHere: "%s に保存しました。", review: "確認して保存", confirmSave: "保存を確認", titleOptional: "タイトル（任意）", noteOptional: "メモ（任意）",
	},
	"zh-Hans": {
		recent: "最近收藏", all: "全部书签", unsorted: "未分类", search: "搜索书签",
		collection: "集合", results: "%d 条书签", more: "加载更多", tags: "标签",
		save: "收藏链接", saved: "已收藏", detail: "书签", note: "笔记", highlights: "划线", favorite: "星标",
		open: "打开原链接", back: "返回", missingToken: "先在「设置 › 扩展」中填写 Raindrop 访问令牌。",
		none: "没有找到书签。", unavailable: "暂时连不上 Raindrop，请稍后再试。", writeDenied: "保存未获确认。",
		savedHere: "已收藏到%s。", review: "复核并收藏", confirmSave: "确认收藏", titleOptional: "标题（可选）", noteOptional: "笔记（可选）",
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

func init() {
	for _, t := range locales {
		t.placeholders.search = t.search
		t.placeholders.url = "https://example.com/article"
		t.placeholders.tags = "research, reading"
		t.placeholders.title = t.titleOptional
		t.placeholders.note = t.noteOptional
	}
}
