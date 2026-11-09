package x

import "strings"

// text is what the connector says to people (status, sign-in, inbox rows) in one language: the hub's locale
// (initialize's hub.locale), Chinese when it says none.
type text struct {
	notSignedIn, notSignedInGuide                           string
	unauthorized                                            string
	unauthorizedGuide                                       []string
	credits                                                 string
	creditsGuide                                            []string
	rateLimited                                             string // %s: the time it resets
	failed                                                  string // %s: what went wrong
	apiKey, apiSecret, accessToken, accessTokenSecret, hint string
	missing, wrongKeys, noCredits, unreachable              string
	mentionLabel, mentionWhy, mentionTitle                  string // mentionTitle %s: @username
	// The Grok source.
	grokVia, grokMissing, nothingSetUp, grokAuth, grokRate string
	grokFailed                                             string // %s: what went wrong
	grokMissingGuide, grokAuthGuide                        []string
}

func texts(locale string) text {
	switch {
	case strings.HasPrefix(locale, "en"):
		return text{
			notSignedIn:      "Not signed in to X",
			notSignedInGuide: "Sign in with your own X developer app's keys (Settings › Connectors › X).",
			unauthorized:     "X refused the keys: they were revoked, regenerated or mistyped.",
			unauthorizedGuide: []string{"In console.x.com, open your app's Keys and tokens.",
				"Regenerate the Access Token and Secret for your account (Read permission).", "Sign in to X here again with the new keys."},
			credits: "X refused the request for your developer account: no credits left, or the app is not enrolled for the X API.",
			creditsGuide: []string{"In console.x.com, check your app belongs to a project with pay-per-use access.",
				"Add credits (Billing) or raise your spending limit; reading resumes on the next sync."},
			rateLimited: "X's rate limit was reached; reading resumes after %s.",
			failed:      "Could not read X: %s",
			apiKey:      "API Key", apiSecret: "API Key Secret", accessToken: "Access Token", accessTokenSecret: "Access Token Secret",
			hint: "Create a developer app at console.x.com, buy pay-per-use credits and set the app's permission to Read. Then, under Keys and tokens, " +
				"copy the API Key and Secret and generate the Access Token and Secret for your own account: you are the app's owner. The keys stay sealed on the hub. " +
				"Grok mode needs no keys at all: just the grok CLI on the hub machine, signed in with a SuperGrok or X Premium+ subscription (grok login).",
			missing: "Fill in all four values.", wrongKeys: "X did not accept these keys. Check them, or regenerate the Access Token and Secret.",
			noCredits:    "X accepted the keys but refused the request: add credits in console.x.com, or check the app is enrolled for the X API.",
			unreachable:  "Could not reach X. Check the hub's network or the proxy setting.",
			mentionLabel: "Mention", mentionWhy: "Rule: someone mentioned you or replied to you on X", mentionTitle: "%s mentioned you on X",
			grokVia:      "grok on the hub machine",
			grokMissing:  "grok is not installed on the hub machine.",
			nothingSetUp: "X is not set up: sign in with X API keys, or use Grok.",
			grokMissingGuide: []string{"Grok (no X keys, on your SuperGrok or X Premium+ subscription): on the hub machine run curl -fsSL https://x.ai/cli/install.sh | bash, then grok login.",
				"X API (pay-per-use, complete feed and mentions): sign in with your own X developer app's keys (Settings › Connectors › X)."},
			grokAuth:      "grok on the hub machine is not signed in, or its sign-in expired.",
			grokAuthGuide: []string{"On the hub machine, run grok login (with a SuperGrok or X Premium+ subscription). Sign-ins expire after 7 days."},
			grokRate:      "Grok's rate limit or your subscription's quota was reached; the digest tries again later.",
			grokFailed:    "Grok's search failed: %s",
		}
	case strings.HasPrefix(locale, "ja"):
		return text{
			notSignedIn:      "X にサインインしていません",
			notSignedInGuide: "自分の X 開発者アプリのキーでサインインしてください（設定 › コネクタ › X）。",
			unauthorized:     "X がキーを受け付けませんでした。取り消された、再生成された、または入力に誤りがあります。",
			unauthorizedGuide: []string{"console.x.com でアプリの Keys and tokens を開きます。",
				"自分のアカウントの Access Token と Secret を再生成します（権限は Read）。", "新しいキーでここからもう一度 X にサインインします。"},
			credits: "X が開発者アカウントのリクエストを拒否しました。クレジットが残っていないか、アプリが X API に登録されていません。",
			creditsGuide: []string{"console.x.com で、アプリが従量課金のプロジェクトに属しているか確認します。",
				"クレジットを追加する（Billing）か、利用上限を上げます。次の同期から読み込みを再開します。"},
			rateLimited: "X のレート制限に達しました。%s 以降に再開します。",
			failed:      "X を読み込めませんでした：%s",
			apiKey:      "API Key", apiSecret: "API Key Secret", accessToken: "Access Token", accessTokenSecret: "Access Token Secret",
			hint: "console.x.com で開発者アプリを作り、従量課金のクレジットを購入して、アプリの権限を Read にします。Keys and tokens で API Key と Secret をコピーし、" +
				"自分のアカウントの Access Token と Secret を生成してください（あなたがアプリのオーナーです）。キーは hub で暗号化して保存されます。" +
				"Grok モードならキーは不要です。hub マシンに grok CLI を入れて、SuperGrok または X Premium+ のサブスクリプションでサインイン（grok login）するだけです。",
			missing: "4 つの値をすべて入力してください。", wrongKeys: "X がこのキーを受け付けませんでした。確認するか、Access Token と Secret を再生成してください。",
			noCredits:    "キーは有効ですが X がリクエストを拒否しました。console.x.com でクレジットを追加するか、アプリが X API に登録されているか確認してください。",
			unreachable:  "X に接続できませんでした。hub のネットワークかプロキシ設定を確認してください。",
			mentionLabel: "メンション", mentionWhy: "ルール：X であなたへのメンションか返信", mentionTitle: "%s が X であなたをメンションしました",
			grokVia:      "hub マシンの grok",
			grokMissing:  "hub マシンに grok がインストールされていません。",
			nothingSetUp: "X が設定されていません。X API のキーでサインインするか、Grok を使ってください。",
			grokMissingGuide: []string{"Grok（X のキー不要、SuperGrok または X Premium+ のサブスクリプションで）：hub マシンで curl -fsSL https://x.ai/cli/install.sh | bash を実行し、grok login します。",
				"X API（従量課金、フィードとメンションを完全に取得）：自分の X 開発者アプリのキーでサインインします（設定 › コネクタ › X）。"},
			grokAuth:      "hub マシンの grok がサインインしていないか、サインインの期限が切れています。",
			grokAuthGuide: []string{"hub マシンで grok login を実行します（SuperGrok または X Premium+ のサブスクリプションが必要）。サインインは 7 日で期限切れになります。"},
			grokRate:      "Grok のレート制限かサブスクリプションの上限に達しました。ダイジェストはあとで再試行します。",
			grokFailed:    "Grok の検索に失敗しました：%s",
		}
	}
	return text{
		notSignedIn:      "还没有登录 X",
		notSignedInGuide: "用你自己的 X 开发者应用的密钥登录（设置 › 连接器 › X）。",
		unauthorized:     "X 不认这组密钥：可能被撤销、重新生成过，或者填错了。",
		unauthorizedGuide: []string{"在 console.x.com 打开你的应用的 Keys and tokens。",
			"给你自己的账号重新生成 Access Token 和 Secret（权限选 Read）。", "在这里用新的密钥重新登录 X。"},
		credits: "X 拒绝了你的开发者账号的请求：额度用完了，或者应用没有开通 X API。",
		creditsGuide: []string{"在 console.x.com 确认应用属于一个按量付费的项目。",
			"充值额度（Billing）或调高消费上限；下一次同步会接着读。"},
		rateLimited: "X 的请求频率到上限了，%s 之后接着读。",
		failed:      "读不到 X：%s",
		apiKey:      "API Key", apiSecret: "API Key Secret", accessToken: "Access Token", accessTokenSecret: "Access Token Secret",
		hint: "在 console.x.com 建一个开发者应用，购买按量付费的额度，把应用权限设成 Read；然后在 Keys and tokens 里复制 API Key 和 Secret，" +
			"再给你自己的账号生成 Access Token 和 Secret（你就是这个应用的所有者）。密钥加密存在 hub 上。" +
			"用 Grok 的话不用任何密钥：只要 hub 这台机器上装了 grok CLI，并用 SuperGrok 或 X Premium+ 订阅登录（grok login）。",
		missing: "四项都要填。", wrongKeys: "X 不认这组密钥。检查一下，或者重新生成 Access Token 和 Secret。",
		noCredits:    "密钥是对的，但 X 拒绝了请求：在 console.x.com 充值额度，或确认应用开通了 X API。",
		unreachable:  "连不上 X。检查 hub 的网络或者代理设置。",
		mentionLabel: "@我", mentionWhy: "规则：X 上有人 @ 了你或回复了你", mentionTitle: "%s 在 X 上 @ 了你",
		grokVia:      "hub 这台机器上的 grok",
		grokMissing:  "hub 这台机器上没有装 grok。",
		nothingSetUp: "X 还没设置：用 X API 的密钥登录，或者用 Grok。",
		grokMissingGuide: []string{"Grok（不用 X 的密钥，用你的 SuperGrok 或 X Premium+ 订阅）：在 hub 这台机器上运行 curl -fsSL https://x.ai/cli/install.sh | bash，再运行 grok login。",
			"X API（按量付费，动态和 @我 读得全）：用你自己的 X 开发者应用的密钥登录（设置 › 连接器 › X）。"},
		grokAuth:      "hub 这台机器上的 grok 没有登录，或者登录过期了。",
		grokAuthGuide: []string{"在 hub 这台机器上运行 grok login（要 SuperGrok 或 X Premium+ 订阅）。登录 7 天后过期。"},
		grokRate:      "Grok 的请求频率或者订阅的额度到上限了，摘要稍后再试。",
		grokFailed:    "Grok 搜索失败：%s",
	}
}
