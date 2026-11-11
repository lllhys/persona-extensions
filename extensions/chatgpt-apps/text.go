package main

// What people read (the sign-in card, the connector's status, the phone's confirmation card, the plugin pages), in the
// user's language (initialize's hub.locale, or the app's for a page: zh-Hans by default, en, ja; Japanese falls back to
// English). What only the model reads (tool descriptions, tool errors) is English.

type texts struct {
	authHint, authExpired, authFailed, authCodeExpired, authStartAgain string
	notSignedIn, signInGuide, expiredError                             string
	card                                                               cardTexts
	ui                                                                 uiTexts
}

type cardTexts struct {
	callAs, account, args, install, uninstall, installSummary, uninstallSummary, developer, apps,
	needsConnect, skills, none string
}

var zhHans = texts{
	authHint:        "在打开的页面登录 ChatGPT，输入上面的验证码。授权页上写的是 Codex，这是正常的。",
	authExpired:     "ChatGPT 登录失效了，请重新登录。",
	authFailed:      "登录没有完成：%s",
	authCodeExpired: "验证码过期了，请重新开始。",
	authStartAgain:  "重新开始",
	notSignedIn:     "还没有登录 ChatGPT。",
	signInGuide:     "在 设置 › 扩展 › ChatGPT 应用 里点「登录」。",
	expiredError:    "ChatGPT 登录失效了。",
	card: cardTexts{
		callAs:           "以你的身份在 ChatGPT 连接的 %s 上调用 %s。",
		account:          "%s 上你的账号",
		args:             "参数：",
		install:          "安装插件「%s」",
		uninstall:        "卸载插件「%s」",
		installSummary:   "在你的 ChatGPT 账号上安装插件「%s」（%s）。",
		uninstallSummary: "从你的 ChatGPT 账号上卸载插件「%s」（%s）。它带的应用工具之后不能再用。",
		developer:        "开发者：%s",
		apps:             "带的应用：%s",
		needsConnect:     "%s（装好后要在 ChatGPT 里点一次连接）",
		skills:           "技能：%d 个",
		none:             "无",
	},
	ui: uiTexts{
		title:            "插件",
		search:           "搜索 ChatGPT 插件",
		installed:        "已安装 · %d",
		allInstalled:     "全部 %d 个已安装插件",
		installedTitle:   "已安装的插件",
		apps:             "ChatGPT 里连好的应用",
		appsRow:          "%d 个应用",
		appsRowDetail:    "%d 个工具，%d 个只读",
		appsRowPending:   "%d 个要在 ChatGPT 里连接",
		market:           "插件市场",
		results:          "「%s」的结果",
		more:             "加载更多",
		noResults:        "没有找到「%s」",
		noResultsDetail:  "换个词试试，比如应用的英文名。",
		installedBadge:   "已安装",
		version:          "版本 %s",
		pluginApps:       "带的应用 · %d",
		pluginSkills:     "技能 · %d",
		connected:        "已连接",
		connectedDetail:  "%d 个工具",
		needsConnect:     "要连接",
		needsDetail:      "在 ChatGPT 里点一次 Connect 就能用",
		afterInstall:     "装好后在 ChatGPT 里连接一次",
		connectFooter:    "在登录了 ChatGPT 的浏览器里打开上面的链接，点 Connect；连好后回到这页下拉刷新。",
		connectLink:      "在 ChatGPT 里连接",
		install:          "安装",
		uninstall:        "卸载",
		installConfirm:   "在你的 ChatGPT 账号上安装「%s」？它带 %d 个应用、%d 个技能，装好后助手就能用。",
		uninstallConfirm: "从你的 ChatGPT 账号上卸载「%s」？助手就不能再用它的应用和技能了。",
		installedToast:   "已安装「%s」",
		connectToast:     "已安装「%s」，还有 %d 个应用要在 ChatGPT 里连接",
		uninstalledToast: "已卸载「%s」",
		writeFailed:      "没有完成：%s",
		noInstalled:      "还没有装插件",
		noInstalledHint:  "在插件市场里搜一个装上。",
		appsTitle:        "连好的应用",
		appTools:         "%d 个工具",
		appToolsDetail:   "%d 个只读，%d 个会改动",
		appsFooter:       "助手调用会改动数据的工具时，每一次都要你在手机上批准。",
		noApps:           "ChatGPT 里还没有连好的应用",
		noAppsHint:       "在 chatgpt.com 的「应用」里连接，或者装一个带应用的插件。",
		readOnly:         "只读",
		changes:          "会改动",
		notConnectedApp:  "这个应用还没连接：在 ChatGPT 里点一次 Connect 后，它的工具才能用。",
		signedOut:        "还没有登录 ChatGPT",
		signedOutHint:    "回到上一页点「登录」，用设备码登录以后，这里会列出插件市场和你装的插件。",
		expired:          "ChatGPT 登录失效了",
		expiredHint:      "回到上一页点「重新登录」。",
		problem:          "读不到 ChatGPT",
		retry:            "重试",
	},
}

var english = texts{
	authHint:        "Sign in to ChatGPT on the page that opens and enter the code above. The page says Codex; that is expected.",
	authExpired:     "The ChatGPT sign-in has expired. Sign in again.",
	authFailed:      "Signing in did not finish: %s",
	authCodeExpired: "The code has expired. Start again.",
	authStartAgain:  "Start again",
	notSignedIn:     "Not signed in to ChatGPT.",
	signInGuide:     "Tap Sign In in Settings › Extensions › ChatGPT Apps.",
	expiredError:    "The ChatGPT sign-in has expired.",
	card: cardTexts{
		callAs:           "Calls %[2]s on %[1]s, connected to your ChatGPT account, as you.",
		account:          "Your account on %s",
		args:             "Arguments:",
		install:          "Install the plugin “%s”",
		uninstall:        "Uninstall the plugin “%s”",
		installSummary:   "Installs the plugin “%s” (%s) on your ChatGPT account.",
		uninstallSummary: "Uninstalls the plugin “%s” (%s) from your ChatGPT account. Its apps' tools stop working.",
		developer:        "Developer: %s",
		apps:             "Apps: %s",
		needsConnect:     "%s (connect it once in ChatGPT after installing)",
		skills:           "Skills: %d",
		none:             "none",
	},
	ui: uiTexts{
		title:            "Plugins",
		search:           "Search ChatGPT plugins",
		installed:        "Installed · %d",
		allInstalled:     "All %d installed plugins",
		installedTitle:   "Installed plugins",
		apps:             "Apps connected in ChatGPT",
		appsRow:          "%d apps",
		appsRowDetail:    "%d tools, %d read-only",
		appsRowPending:   "%d to connect in ChatGPT",
		market:           "Plugin marketplace",
		results:          "Results for “%s”",
		more:             "Load more",
		noResults:        "Nothing found for “%s”",
		noResultsDetail:  "Try another word, such as the app's name.",
		installedBadge:   "Installed",
		version:          "Version %s",
		pluginApps:       "Apps · %d",
		pluginSkills:     "Skills · %d",
		connected:        "Connected",
		connectedDetail:  "%d tools",
		needsConnect:     "Connect",
		needsDetail:      "Click Connect once in ChatGPT to use it",
		afterInstall:     "Connect it once in ChatGPT after installing",
		connectFooter:    "Open the link above in a browser signed in to ChatGPT and click Connect, then pull to refresh this page.",
		connectLink:      "Connect in ChatGPT",
		install:          "Install",
		uninstall:        "Uninstall",
		installConfirm:   "Install “%s” on your ChatGPT account? It brings %d apps and %d skills, which the assistant can then use.",
		uninstallConfirm: "Uninstall “%s” from your ChatGPT account? The assistant can no longer use its apps and skills.",
		installedToast:   "Installed “%s”",
		connectToast:     "Installed “%s”; %d apps still need connecting in ChatGPT",
		uninstalledToast: "Uninstalled “%s”",
		writeFailed:      "Did not finish: %s",
		noInstalled:      "No plugins installed",
		noInstalledHint:  "Search the marketplace and install one.",
		appsTitle:        "Connected apps",
		appTools:         "%d tools",
		appToolsDetail:   "%d read-only, %d change data",
		appsFooter:       "Every time the assistant calls a tool that changes data, you approve it on the phone.",
		noApps:           "No apps connected in ChatGPT",
		noAppsHint:       "Connect one under Apps on chatgpt.com, or install a plugin that brings apps.",
		readOnly:         "Read-only",
		changes:          "Changes data",
		notConnectedApp:  "This app is not connected: click Connect once in ChatGPT before its tools work.",
		signedOut:        "Not signed in to ChatGPT",
		signedOutHint:    "Go back and tap Sign In; once you sign in with the device code, the marketplace and your plugins show here.",
		expired:          "The ChatGPT sign-in has expired",
		expiredHint:      "Go back and tap Sign In Again.",
		problem:          "Could not reach ChatGPT",
		retry:            "Try again",
	},
}

// uiTexts are the plugin pages'.
type uiTexts struct {
	title, search, installed, allInstalled, installedTitle, apps, appsRow, appsRowDetail, appsRowPending, market, results, more,
	noResults, noResultsDetail, installedBadge, version, pluginApps, pluginSkills, connected, connectedDetail,
	needsConnect, needsDetail, afterInstall, connectFooter, connectLink, install, uninstall, installConfirm,
	uninstallConfirm, installedToast, connectToast, uninstalledToast, writeFailed, noInstalled, noInstalledHint,
	appsTitle, appTools, appToolsDetail, appsFooter, noApps, noAppsHint, readOnly, changes, notConnectedApp,
	signedOut, signedOutHint, expired, expiredHint, problem, retry string
}

func textsFor(locale string) texts {
	if locale == "" || locale == "zh-Hans" || len(locale) >= 2 && locale[:2] == "zh" {
		return zhHans
	}
	return english
}
