package main

import (
	"fmt"
	"strings"
	"time"
)

// The page's own text in the app's languages (zh-Hans, en, ja), chosen by the locale the hub passes; an empty or
// unknown locale is zh-Hans, as the hub's own default. What the site says (titles, comments) is shown as it is, and
// the tools answer the assistant in English.

type texts struct {
	lists                       map[string]string
	pick, viewing               string
	count, countOf, countAll    string // list title, shown, total
	byInterests, unfiltered     string
	showAll, filterAgain        string
	nothing, missed, failed     string
	stale, readAgo              string
	refresh, retry, back        string
	openArticle, openSite       string
	comments, commentsFoot      string
	noComments, commentsMissed  string
	gone, textOnly              string
	points, nComments           string
	keywords, blocked, minScore string
	listSep                     string
	ago                         func(time.Duration) string
}

var locales = map[string]*texts{
	"zh-Hans": {
		lists: map[string]string{"top": "热门", "new": "最新", "best": "最佳", "ask": "Ask HN", "show": "Show HN"},
		pick:  "看什么", viewing: "正在看",
		count: "%s · %d 条", countOf: "%s · %d / %d 条", countAll: "%s · 全部 %d 条",
		byInterests: "按你的兴趣：%s。在这个扩展的设置里改", unfiltered: "没按你的兴趣过滤",
		showAll: "看全部 %d 条", filterAgain: "按我的兴趣过滤",
		nothing: "这里没有可看的帖子", missed: "有 %d 条没读到", failed: "没读到：%s",
		stale: "这次没读到：%s。下面是 %s读到的", readAgo: "%s读到（几分钟内不重读）",
		refresh: "刷新", retry: "重试", back: "返回列表",
		openArticle: "打开原文", openSite: "在 Hacker News 打开",
		comments: "评论", commentsFoot: "前 %d 条顶层评论，共 %d 条评论；其余的在网页里看",
		noComments: "还没有评论", commentsMissed: "有 %d 条评论没读到",
		gone: "这个帖子已被删除或隐藏", textOnly: "网页要手机能访问 news.ycombinator.com",
		points: "%d 分", nComments: "%d 评论",
		keywords: "关键词 %s", blocked: "屏蔽 %s", minScore: "至少 %d 分", listSep: "、",
		ago: func(d time.Duration) string { return agoIn(d, "刚刚", "%d 分钟前", "%d 小时前", "%d 天前") },
	},
	"en": {
		lists: map[string]string{"top": "Top", "new": "New", "best": "Best", "ask": "Ask HN", "show": "Show HN"},
		pick:  "Lists", viewing: "Viewing",
		count: "%s · %d", countOf: "%s · %d of %d", countAll: "%s · all %d",
		byInterests: "By your interests: %s. Change this in the extension's settings", unfiltered: "Not filtered by your interests",
		showAll: "Show all %d", filterAgain: "Filter by my interests",
		nothing: "Nothing to show here", missed: "%d could not be read", failed: "Could not read: %s",
		stale: "This read failed: %s. Showing what was read %s", readAgo: "Read %s (not read again for a few minutes)",
		refresh: "Refresh", retry: "Retry", back: "Back to list",
		openArticle: "Open article", openSite: "Open on Hacker News",
		comments: "Comments", commentsFoot: "First %d top-level comments of %d in all; the rest are on the website",
		noComments: "No comments yet", commentsMissed: "%d comments could not be read",
		gone: "This story was deleted or hidden", textOnly: "The website needs the phone to reach news.ycombinator.com",
		points: "%d points", nComments: "%d comments",
		keywords: "keywords %s", blocked: "blocked %s", minScore: "at least %d points", listSep: ", ",
		ago: func(d time.Duration) string { return agoIn(d, "just now", "%dm ago", "%dh ago", "%dd ago") },
	},
	"ja": {
		lists: map[string]string{"top": "トップ", "new": "新着", "best": "ベスト", "ask": "Ask HN", "show": "Show HN"},
		pick:  "表示するもの", viewing: "表示中",
		count: "%s · %d 件", countOf: "%s · %d / %d 件", countAll: "%s · 全 %d 件",
		byInterests: "あなたの興味で絞り込み：%s。この拡張機能の設定で変更できます", unfiltered: "興味で絞り込んでいません",
		showAll: "全 %d 件を表示", filterAgain: "興味で絞り込む",
		nothing: "表示できる投稿はありません", missed: "%d 件を読み込めませんでした", failed: "読み込めませんでした：%s",
		stale: "今回は読み込めませんでした：%s。以下は %sに読み込んだものです", readAgo: "%sに読み込み（数分間は再読み込みしません）",
		refresh: "更新", retry: "再試行", back: "一覧に戻る",
		openArticle: "記事を開く", openSite: "Hacker News で開く",
		comments: "コメント", commentsFoot: "トップレベルのコメント先頭 %d 件（全 %d 件）。残りはウェブで",
		noComments: "まだコメントはありません", commentsMissed: "%d 件のコメントを読み込めませんでした",
		gone: "この投稿は削除または非表示になっています", textOnly: "ウェブを開くには端末から news.ycombinator.com に接続できる必要があります",
		points: "%d ポイント", nComments: "%d コメント",
		keywords: "キーワード %s", blocked: "除外 %s", minScore: "%d ポイント以上", listSep: "、",
		ago: func(d time.Duration) string {
			return agoIn(d, "たった今", "%d 分前", "%d 時間前", "%d 日前")
		},
	},
}

// textsFor picks the texts for a locale such as "en", "en-US", "ja-JP" or "zh-Hans".
func textsFor(locale string) *texts {
	l := strings.ToLower(locale)
	switch {
	case l == "en" || strings.HasPrefix(l, "en-") || strings.HasPrefix(l, "en_"):
		return locales["en"]
	case l == "ja" || strings.HasPrefix(l, "ja-") || strings.HasPrefix(l, "ja_"):
		return locales["ja"]
	}
	return locales["zh-Hans"]
}

func agoIn(d time.Duration, now, minutes, hours, days string) string {
	switch {
	case d < time.Minute:
		return now
	case d < time.Hour:
		return fmt.Sprintf(minutes, int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf(hours, int(d/time.Hour))
	}
	return fmt.Sprintf(days, int(d/(24*time.Hour)))
}

// meta is a story's line under its title: points, comments, domain or author, and age.
func (t *texts) meta(it item, now time.Time) string {
	parts := []string{fmt.Sprintf(t.points, it.Score), fmt.Sprintf(t.nComments, it.Descendants)}
	if d := it.domain(); d != "" {
		parts = append(parts, d)
	}
	if it.By != "" {
		parts = append(parts, it.By)
	}
	parts = append(parts, t.ago(now.Sub(time.Unix(it.Time, 0))))
	return strings.Join(parts, " · ")
}

// describe says the filter in words, empty when it does nothing.
func (t *texts) describe(f filter) string {
	var parts []string
	if kw := nonEmpty(f.Keywords); len(kw) > 0 {
		parts = append(parts, fmt.Sprintf(t.keywords, strings.Join(kw, t.listSep)))
	}
	if ex := nonEmpty(f.ExcludeKeywords); len(ex) > 0 {
		parts = append(parts, fmt.Sprintf(t.blocked, strings.Join(ex, t.listSep)))
	}
	if f.MinScore > 0 {
		parts = append(parts, fmt.Sprintf(t.minScore, f.MinScore))
	}
	return strings.Join(parts, " · ")
}
