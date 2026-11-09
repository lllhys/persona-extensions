package main

import "strings"

type texts struct {
	papers, recent, search, results, noPapers, back, more, missingConfig string
	authors, category, published, abstract, open, noAbstract             string
}

var locales = map[string]*texts{
	"en": {
		papers: "arXiv research", recent: "Recent submissions", search: "Search papers by topic", results: "%d papers", noPapers: "No papers in this view.", back: "Back", more: "Load more", missingConfig: "arXiv could not be reached. Try again later.",
		authors: "Authors", category: "Category", published: "Published", abstract: "Abstract", open: "Open on arXiv", noAbstract: "No abstract.",
	},
	"ja": {
		papers: "arXiv 研究", recent: "最近の投稿", search: "トピックで論文を検索", results: "%d 件の論文", noPapers: "この表示に論文はありません。", back: "戻る", more: "さらに読み込む", missingConfig: "arXiv に接続できません。しばらくしてから再試行してください。",
		authors: "著者", category: "カテゴリ", published: "公開日", abstract: "要約", open: "arXiv で開く", noAbstract: "要約はありません。",
	},
	"zh-Hans": {
		papers: "arXiv 研究阅读", recent: "最近提交", search: "按主题搜索论文", results: "%d 篇论文", noPapers: "这个视图里没有论文。", back: "返回", more: "加载更多", missingConfig: "暂时无法连接 arXiv，请稍后重试。",
		authors: "作者", category: "分类", published: "发表日期", abstract: "摘要", open: "在 arXiv 中打开", noAbstract: "暂无摘要。",
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
