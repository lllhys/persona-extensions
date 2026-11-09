package main

import "strings"

type texts struct {
	knowledge, spaces, noSpaces, objects, noObjects, search, back, more, missingConfig string
	create, name, body, review, typeLabel, pageType, noteType                          string
}

var locales = map[string]*texts{
	"en": {
		knowledge: "Anytype knowledge", spaces: "Spaces", noSpaces: "No accessible spaces.", objects: "%d objects", noObjects: "No objects in this space.", search: "Search knowledge", back: "Back", more: "Load more", missingConfig: "Set the local/headless Anytype API key in Settings › Extensions first.",
		create: "Create page", name: "Title", body: "Markdown body", review: "Review", typeLabel: "Type", pageType: "Page", noteType: "Note",
	},
	"ja": {
		knowledge: "Anytype ナレッジ", spaces: "スペース", noSpaces: "アクセスできるスペースはありません。", objects: "%d 件のオブジェクト", noObjects: "このスペースにオブジェクトはありません。", search: "ナレッジを検索", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能でローカル／ヘッドレス Anytype API キーを設定してください。",
		create: "ページを作成", name: "タイトル", body: "Markdown 本文", review: "確認", typeLabel: "種類", pageType: "ページ", noteType: "ノート",
	},
	"zh-Hans": {
		knowledge: "Anytype 知识库", spaces: "空间", noSpaces: "没有可访问的空间。", objects: "%d 个对象", noObjects: "这个空间里没有对象。", search: "搜索知识库", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写本地或 headless Anytype API key。",
		create: "创建页面", name: "标题", body: "Markdown 内容", review: "预览", typeLabel: "类型", pageType: "页面", noteType: "笔记",
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
