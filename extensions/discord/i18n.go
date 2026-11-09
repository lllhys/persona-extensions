package main

import "strings"

// The extension's own words for people, in the app's languages (zh-Hans, en, ja), chosen by the locale the hub
// passes; an empty or unknown locale is zh-Hans, as the hub's own default. They are the status and sign-in texts and
// the notes the archive writes into a message (a reply, an attachment). What people wrote on Discord is kept as it
// is, and the tools answer the assistant in English (their notes too: the en texts).

type texts struct {
	// status
	starting, notSignedIn            string
	signInGuide                      []string
	tokenRevoked, intentOff          string
	intentGuide                      []string
	unreachable, proxyGuide          string // unreachable: %s the error
	proxyBad                         string // %s why
	rateLimited                      string // %d seconds
	noServers, invite, account       string // invite: %s the link; account: %s the bot's name
	secretUnreadable, secretRejected string
	// sign-in
	tokenLabel, tokenPlaceholder, tokenHint string
	tokenEmpty, tokenWrong                  string
	// archive notes
	replyTo, forwarded               string // replyTo: %s who, %s what; forwarded: what follows
	attachment, sticker, poll, embed string // %s the name / question / title
	threadStarted                    string // %s the thread
	nameSep                          string
}

var locales = map[string]*texts{
	"zh-Hans": {
		starting: "正在启动", notSignedIn: "还没填机器人的 token",
		signInGuide: []string{
			"在 Discord 开发者后台（https://discord.com/developers/applications）新建一个应用",
			"在 Bot 页打开 Message Content Intent，关掉 Public Bot，然后 Reset Token 复制 token",
			"在这里登录，把 token 填进去",
		},
		tokenRevoked: "Discord 不认这个机器人的 token 了（可能在开发者后台重置过或删了应用）：重新登录，填新的 token",
		intentOff:    "机器人没打开 Message Content Intent，Discord 不给它看消息内容",
		intentGuide: []string{
			"在 Discord 开发者后台 › 你的应用 › Bot 打开 Message Content Intent 并保存",
			"不用重新登录：一分钟内这里会自己变好",
		},
		unreachable: "hub 这台机器连不上 Discord：%s",
		proxyGuide:  "这台机器的网络到不了 Discord 时，在这个扩展的设置里填一个代理（proxy）",
		proxyBad:    "代理填得不对（%s）：要 http://、https:// 或 socks5:// 开头，带主机和端口",
		rateLimited: "Discord 让等 %d 秒再读",
		noServers:   "机器人还不在任何服务器里，没有能收的频道", invite: "把机器人拉进服务器：%s", account: "%s（机器人）",
		secretUnreadable: "读不到 hub 里存的登录，正在重试", secretRejected: "hub 里存的登录读不懂了：重新登录",
		tokenLabel: "Bot token", tokenPlaceholder: "开发者后台 › Bot › Reset Token 复制的那串",
		tokenHint: "用你自己建的 Discord 机器人：在开发者后台（https://discord.com/developers/applications）新建应用，" +
			"Bot 页打开 Message Content Intent、关掉 Public Bot，Reset Token 后把 token 填在这里。登录后在状态里拿邀请链接，把机器人拉进你的服务器；" +
			"只给它「查看频道」和「读取消息历史」，它看得到的频道就进知识库。token 加密存在 hub 的数据库里。",
		tokenEmpty: "填上 token", tokenWrong: "Discord 说这个 token 不对：在开发者后台 › Bot 里 Reset Token，复制新的再填",
		replyTo: "回复 %s：「%s」", forwarded: "转发：",
		attachment: "[附件 %s]", sticker: "[贴纸 %s]", poll: "[投票：%s]", embed: "[链接 %s]",
		threadStarted: "[开了话题「%s」]",
		nameSep:       " › ",
	},
	"en": {
		starting: "Starting", notSignedIn: "No bot token yet",
		signInGuide: []string{
			"Create an application in the Discord Developer Portal (https://discord.com/developers/applications)",
			"On its Bot page, turn on Message Content Intent, turn off Public Bot, then Reset Token and copy the token",
			"Sign in here and paste the token",
		},
		tokenRevoked: "Discord no longer accepts this bot's token (it may have been reset, or the application deleted): sign in again with a new token",
		intentOff:    "The bot's Message Content Intent is off, so Discord does not show it what messages say",
		intentGuide: []string{
			"In the Discord Developer Portal › your application › Bot, turn on Message Content Intent and save",
			"No need to sign in again: this turns ok by itself within a minute",
		},
		unreachable: "The hub's machine cannot reach Discord: %s",
		proxyGuide:  "If this machine's network does not reach Discord, set a proxy in this extension's settings",
		proxyBad:    "The proxy is not right (%s): it starts with http://, https:// or socks5:// and has a host and port",
		rateLimited: "Discord asks to wait %d seconds before reading again",
		noServers:   "The bot is not in any server yet, so there are no channels to read", invite: "Invite the bot to a server: %s", account: "%s (bot)",
		secretUnreadable: "Could not read the sign-in kept in the hub; trying again", secretRejected: "The sign-in kept in the hub cannot be read: sign in again",
		tokenLabel: "Bot token", tokenPlaceholder: "From Developer Portal › Bot › Reset Token",
		tokenHint: "Use your own Discord bot: create an application in the Developer Portal (https://discord.com/developers/applications), " +
			"turn on Message Content Intent and turn off Public Bot on its Bot page, then Reset Token and paste the token here. After signing in, the status has an invite link for your servers; " +
			"give the bot only View Channels and Read Message History, and the channels it can see go into the knowledge base. The token is encrypted in the hub's database.",
		tokenEmpty: "Enter the token", tokenWrong: "Discord says this token is wrong: Reset Token under Developer Portal › Bot and paste the new one",
		replyTo: "Reply to %s: “%s”", forwarded: "Forwarded: ",
		attachment: "[Attachment %s]", sticker: "[Sticker %s]", poll: "[Poll: %s]", embed: "[Link %s]",
		threadStarted: "[Started the thread “%s”]",
		nameSep:       " › ",
	},
	"ja": {
		starting: "起動中", notSignedIn: "ボットのトークンがまだありません",
		signInGuide: []string{
			"Discord Developer Portal（https://discord.com/developers/applications）でアプリケーションを作成します",
			"Bot ページで Message Content Intent をオンに、Public Bot をオフにして、Reset Token でトークンをコピーします",
			"ここでサインインしてトークンを貼り付けます",
		},
		tokenRevoked: "Discord がこのボットのトークンを受け付けなくなりました（リセットされたか、アプリケーションが削除された可能性があります）：新しいトークンでサインインし直してください",
		intentOff:    "ボットの Message Content Intent がオフのため、Discord がメッセージの内容を渡しません",
		intentGuide: []string{
			"Discord Developer Portal › アプリケーション › Bot で Message Content Intent をオンにして保存します",
			"サインインし直す必要はありません：1 分以内に自動で正常に戻ります",
		},
		unreachable: "ハブのマシンから Discord に接続できません：%s",
		proxyGuide:  "このマシンのネットワークから Discord に届かない場合は、この拡張機能の設定でプロキシを指定してください",
		proxyBad:    "プロキシの指定が正しくありません（%s）：http://、https:// または socks5:// で始まり、ホストとポートが必要です",
		rateLimited: "Discord から %d 秒待つよう求められています",
		noServers:   "ボットはまだどのサーバーにも参加していないため、読めるチャンネルがありません", invite: "ボットをサーバーに招待：%s", account: "%s（ボット）",
		secretUnreadable: "ハブに保存されたサインイン情報を読めませんでした。再試行しています", secretRejected: "ハブに保存されたサインイン情報を読み取れません：サインインし直してください",
		tokenLabel: "ボットトークン", tokenPlaceholder: "Developer Portal › Bot › Reset Token でコピーしたもの",
		tokenHint: "ご自身の Discord ボットを使います：Developer Portal（https://discord.com/developers/applications）でアプリケーションを作成し、" +
			"Bot ページで Message Content Intent をオン、Public Bot をオフにして、Reset Token でコピーしたトークンをここに貼り付けます。サインイン後、状態に表示される招待リンクでボットをサーバーに追加します。" +
			"ボットには「チャンネルを見る」と「メッセージ履歴を読む」だけを与えてください。見えるチャンネルがナレッジベースに入ります。トークンはハブのデータベースに暗号化して保存されます。",
		tokenEmpty: "トークンを入力してください", tokenWrong: "Discord によるとこのトークンは正しくありません：Developer Portal › Bot で Reset Token し、新しいトークンを貼り付けてください",
		replyTo: "%s への返信：「%s」", forwarded: "転送：",
		attachment: "[添付 %s]", sticker: "[スタンプ %s]", poll: "[投票：%s]", embed: "[リンク %s]",
		threadStarted: "[スレッド「%s」を作成しました]",
		nameSep:       " › ",
	},
}

// textsFor is the texts of a locale: zh-Hans, en or ja (zh-Hant and other Chinese are zh-Hans, other English en).
func textsFor(locale string) *texts {
	if t, ok := locales[locale]; ok {
		return t
	}
	switch l := strings.ToLower(locale); {
	case strings.HasPrefix(l, "en"):
		return locales["en"]
	case strings.HasPrefix(l, "ja"):
		return locales["ja"]
	}
	return locales["zh-Hans"]
}
