package page

import "strings"

// text is the page's words in one language (the app's locale; Chinese when it says none).
type text struct {
	signInTitle, signInDetail                             string
	account, budget, lastSync, state, never, feedOff      string
	stateOK, stateRateLimited, stateError, syncing        string
	window, w24h, w3d, current                            string
	top, links, threads, active, none                     string
	repostedBy, sharedBy, threadOf, postsN, counts        string
	openOnX, moreOnX, totals, refresh, refreshing, footer string
	// Grok mode, and the page while neither way is set up.
	source, lastRefresh, grokRuns, grokNote, grokSummary, grokPosts, grokNoTime                string
	grokSearching, grokRefreshing, grokFresh, grokNothing, grokFooter, setUpTitle, setUpDetail string
}

func texts(locale string) text {
	switch {
	case strings.HasPrefix(locale, "en"):
		return text{
			signInTitle:  "Not signed in to X",
			signInDetail: "Sign in with your own X developer app's keys under Settings › Connectors › X. Reading uses your pay-per-use credits, within the daily post budget in this extension's settings.",
			account:      "Account", budget: "Posts read today (UTC)", lastSync: "Last sync", state: "State", never: "Not yet", feedOff: "feed off",
			stateOK: "OK", stateRateLimited: "Rate limited", stateError: "Needs attention", syncing: "Syncing…",
			window: "Window", w24h: "24 hours", w3d: "3 days", current: "Showing",
			top: "Top posts", links: "Most shared links", threads: "Threads", active: "Most active accounts you follow", none: "Nothing in this window yet.",
			repostedBy: "reposted by %d", sharedBy: "shared by %d", threadOf: "thread of %d posts", postsN: "%d posts",
			counts:  "%d likes · %d reposts · %d replies · %d quotes",
			openOnX: "Open on X", moreOnX: "The rest of this post is on X.",
			totals: "%d posts seen in this window · budget %d / %d today (UTC)", refresh: "Refresh", refreshing: "Syncing with X…",
			footer: "A digest of what was read, not a timeline. Posts are kept a few days, then deleted.",
			source: "Source", lastRefresh: "Last refresh", grokRuns: "Grok runs today (UTC)",
			grokNote:    "Found and organized by Grok's search; it may be incomplete.",
			grokSummary: "Summary", grokPosts: "Posts", grokNoTime: "time unknown",
			grokSearching: "Searching…", grokRefreshing: "Asking Grok to search X…", grokFresh: "Grok searched X less than 30 minutes ago: this is what it found.",
			grokNothing: "Grok has nothing to watch yet: add accounts (handles) or topics in this extension's settings, or sign in with X API keys once so it can use who you follow.",
			grokFooter:  "A digest of what Grok found on X in the last 24 hours, not a timeline. It uses your Grok subscription, within the daily run cap in this extension's settings.",
			setUpTitle:  "Set up X",
			setUpDetail: "Two ways, either is enough. Grok: install the grok CLI on the hub machine (curl -fsSL https://x.ai/cli/install.sh | bash) and run grok login with a SuperGrok or X Premium+ subscription; no X keys needed. " +
				"X API: sign in under Settings › Connectors › X with your own X developer app's keys (pay-per-use; the complete feed and your mentions).",
		}
	case strings.HasPrefix(locale, "ja"):
		return text{
			signInTitle:  "X にサインインしていません",
			signInDetail: "設定 › コネクタ › X で、自分の X 開発者アプリのキーでサインインしてください。読み込みには従量課金のクレジットを使い、この拡張の設定の 1 日の投稿予算内に収めます。",
			account:      "アカウント", budget: "今日読んだ投稿（UTC）", lastSync: "最終同期", state: "状態", never: "まだ", feedOff: "フィード オフ",
			stateOK: "正常", stateRateLimited: "レート制限中", stateError: "要対応", syncing: "同期中…",
			window: "期間", w24h: "24 時間", w3d: "3 日", current: "表示中",
			top: "注目の投稿", links: "よく共有されたリンク", threads: "スレッド", active: "よく投稿したフォロー中のアカウント", none: "この期間にはまだありません。",
			repostedBy: "%d 人がリポスト", sharedBy: "%d アカウントが共有", threadOf: "%d 件のスレッド", postsN: "%d 件",
			counts:  "いいね %d · リポスト %d · 返信 %d · 引用 %d",
			openOnX: "X で開く", moreOnX: "この投稿の続きは X で読めます。",
			totals: "この期間に読んだ投稿 %d 件 · 今日（UTC）の予算 %d / %d", refresh: "更新", refreshing: "X と同期しています…",
			footer: "読んだ投稿のまとめで、タイムラインではありません。投稿は数日保存したあと削除します。",
			source: "ソース", lastRefresh: "最終更新", grokRuns: "今日の Grok 実行（UTC）",
			grokNote:    "Grok の検索で集めて整理したもので、漏れがあるかもしれません。",
			grokSummary: "まとめ", grokPosts: "投稿", grokNoTime: "時刻不明",
			grokSearching: "検索中…", grokRefreshing: "Grok に X を検索させています…", grokFresh: "Grok が X を検索してから 30 分たっていません。その結果を表示しています。",
			grokNothing: "Grok が見る対象がまだありません。この拡張の設定でアカウント（handles）かトピックを追加するか、一度 X API のキーでサインインしてフォロー中のアカウントを使えるようにしてください。",
			grokFooter:  "Grok が過去 24 時間の X で見つけたもののまとめで、タイムラインではありません。Grok のサブスクリプションを使い、この拡張の設定の 1 日の実行上限内に収めます。",
			setUpTitle:  "X を設定する",
			setUpDetail: "方法は 2 つで、どちらか一方で十分です。Grok：hub マシンに grok CLI を入れ（curl -fsSL https://x.ai/cli/install.sh | bash）、SuperGrok または X Premium+ のサブスクリプションで grok login します。X のキーは不要です。" +
				"X API：設定 › コネクタ › X で、自分の X 開発者アプリのキーでサインインします（従量課金。フィードとメンションを完全に取得）。",
		}
	}
	return text{
		signInTitle:  "还没有登录 X",
		signInDetail: "在 设置 › 连接器 › X 里用你自己的 X 开发者应用的密钥登录。读取用的是你按量付费的额度，不超过这个扩展设置里的每日帖子预算。",
		account:      "账号", budget: "今天读的帖子（UTC）", lastSync: "上次同步", state: "状态", never: "还没有", feedOff: "动态已关",
		stateOK: "正常", stateRateLimited: "请求频率受限", stateError: "需要处理", syncing: "正在同步…",
		window: "时间范围", w24h: "24 小时", w3d: "3 天", current: "正在看",
		top: "热门帖子", links: "分享最多的链接", threads: "长串", active: "发得最多的关注账号", none: "这段时间里还没有。",
		repostedBy: "%d 人转发", sharedBy: "%d 个账号分享", threadOf: "%d 条的长串", postsN: "%d 条",
		counts:  "%d 赞 · %d 转发 · %d 回复 · %d 引用",
		openOnX: "在 X 上打开", moreOnX: "这条帖子剩下的内容在 X 上看。",
		totals: "这段时间读到 %d 条 · 今天（UTC）预算 %d / %d", refresh: "刷新", refreshing: "正在和 X 同步…",
		footer: "读到的帖子整理出的摘要，不是时间线。帖子只保留几天，之后删除。",
		source: "来源", lastRefresh: "上次刷新", grokRuns: "今天 Grok 搜索次数（UTC）",
		grokNote:    "由 Grok 搜索整理，可能不完整。",
		grokSummary: "摘要", grokPosts: "帖子", grokNoTime: "时间不详",
		grokSearching: "正在搜索…", grokRefreshing: "正在让 Grok 搜索 X…", grokFresh: "Grok 不到 30 分钟前刚搜过 X，显示的就是那次的结果。",
		grokNothing: "Grok 还没有要看的：在这个扩展的设置里加上账号（handles）或话题，或者用 X API 的密钥登录一次，让它能用你关注的人。",
		grokFooter:  "Grok 在 X 上找到的过去 24 小时的摘要，不是时间线。用的是你的 Grok 订阅，不超过这个扩展设置里的每日搜索上限。",
		setUpTitle:  "设置 X",
		setUpDetail: "两种方式，任选其一。Grok：在 hub 这台机器上装 grok CLI（curl -fsSL https://x.ai/cli/install.sh | bash），再用 SuperGrok 或 X Premium+ 订阅运行 grok login，不需要 X 的密钥。" +
			"X API：在 设置 › 连接器 › X 里用你自己的 X 开发者应用的密钥登录（按量付费，动态和 @我 读得全）。",
	}
}
