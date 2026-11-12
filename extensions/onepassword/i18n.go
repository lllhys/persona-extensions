package main

import "strings"

type texts struct {
	connection, connected, connectedDetail, checkAgain                   string
	notSetUp, notSetUpDetail, setUpTitle                                 string
	setUpSteps, can                                                      []string
	canTitle, vaultsTitle, vaultsFooter, items, moreVaults, noVaults     string
	noVaultsDetail, unreachable, unreachableGuide, refused, refusedGuide string
	rateLimited, rateLimitedGuide, timedOut                              string
	checkNotToken, checkRefused, says                                    string
	// the secret provider (secrets.go): its errors, and the page's list of uses
	secretNoToken, secretNotFound, secretEmpty, secretInvalid string
	usesTitle, usesFooter, noUses, noUsesDetail               string
	useExtension, useLauncher, useAgent, useOn                string
	bySettings, byRule, byDevice                              string
	// op on one of the user's computers (#376): node.go
	opConnectedDetail, opVaultsFooter, opNoVaultsDetail, opRateLimitedGuide string
	opSetUpTitle                                                            string
	opSetUpSteps                                                            []string
	opMissing, opMissingGuide, opNoAccount, opNoAccountGuide                string
	opLocked, opLockedGuide, opOffline, opOfflineGuide, opUnreachableGuide  string
}

var locales = map[string]*texts{
	"en": {
		connection: "Connection", connected: "Connected",
		connectedDetail: "Service account · vaults it can read: %d", checkAgain: "Check again",
		notSetUp:       "Not connected yet",
		notSetUpDetail: "Add a 1Password service account token, or pick a computer where op is signed in, in this extension's settings.",
		setUpTitle:     "Set it up",
		setUpSteps: []string{
			"1. Sign in on 1Password.com, select Developer in the sidebar and create a service account.",
			"2. Grant it only the vaults Persona may look into, with read access.",
			"3. Copy the token (it starts with ops_) and paste it into this extension's settings. Persona checks it with 1Password before saving.",
		},
		canTitle: "What the assistant can do",
		can: []string{
			"List the vaults Persona can read, and find items in them by title, tag or website.",
			"See which fields an item has, and each field's op:// reference.",
			"Passwords, keys, notes and one-time codes never reach the assistant, and nothing here can change your vaults.",
		},
		vaultsTitle: "Vaults", vaultsFooter: "Change which vaults these are on 1Password.com, in the service account's settings.",
		items: "%d items", moreVaults: "%d more vaults not shown.", noVaults: "No vaults",
		noVaultsDetail:    "The service account can't read any vault yet. Grant it vaults on 1Password.com.",
		unreachable:       "Can't reach 1Password",
		unreachableGuide:  "Check that the hub can reach 1Password, then check again.",
		refused:           "1Password refused the token",
		refusedGuide:      "The token may have been revoked or may have expired. Create a new one and paste it into this extension's settings.",
		rateLimited:       "1Password's rate limit reached",
		rateLimitedGuide:  "Service accounts may make a limited number of requests an hour. Try again later.",
		timedOut:          "1Password did not answer in time.",
		says:              "1Password says: %s",
		checkNotToken:     "This is not a 1Password service account token: those start with ops_.",
		checkRefused:      "1Password did not accept this token: %s",
		secretNoToken:     "1Password isn't set up: add a service account token, or pick a computer with op, in the 1Password extension's settings.",
		secretNotFound:    "1Password has no such vault, item or field here: %s",
		secretEmpty:       "This field is empty in 1Password.",
		secretInvalid:     "This is not a 1Password reference Persona can read: write it as op://vault/item/field.",
		usesTitle:         "Used by Persona",
		usesFooter:        "The uses Persona reported since this extension last started, newest first. A key Persona answers again from its own short memory is not reported twice. Values are never kept here; Persona keeps its own record.",
		noUses:            "Not used yet",
		noUsesDetail:      "Turn on Use a reference in another extension's secret or a model provider's key and enter op://vault/item/field. Persona then asks this extension for the value each time it is used.",
		useExtension:      "Extension secret · %s",
		useLauncher:       "Model provider key · %s",
		useAgent:          "For an Agent run · %s",
		useOn:             " on %s",
		bySettings:        "saved in Settings",
		byRule:            "an Always rule",
		byDevice:          "approved on a device",
		opConnectedDetail: "op on your computer · vaults it can read: %d",
		opSetUpTitle:      "Or use op on one of your computers",
		opSetUpSteps: []string{
			"1. On a Mac or Linux computer connected to Persona, install the 1Password app and the 1Password CLI (op).",
			"2. In the 1Password app, open Settings › Developer and turn on Integrate with 1Password CLI.",
			"3. Pick that computer in this extension's settings. The first time Persona asks, approve op in the 1Password app there.",
		},
		opVaultsFooter:     "These are the vaults of the account op is signed in to on your computer.",
		opNoVaultsDetail:   "op on your computer can't read any vault.",
		opRateLimitedGuide: "1Password is limiting this account's requests. Try again later.",
		opMissing:          "op isn't installed on that computer",
		opMissingGuide:     "Install the 1Password CLI (op) there and turn on Integrate with 1Password CLI in the 1Password app's Developer settings, then check again.",
		opNoAccount:        "op on that computer isn't signed in",
		opNoAccountGuide:   "In the 1Password app on that computer, open Settings › Developer and turn on Integrate with 1Password CLI, then check again.",
		opLocked:           "1Password on that computer is locked",
		opLockedGuide:      "Unlock the 1Password app on that computer and approve op when it asks, then check again. Persona doesn't retry by itself.",
		opOffline:          "That computer is offline",
		opOfflineGuide:     "Persona can use op again once the computer is back.",
		opUnreachableGuide: "Check that the computer is on and this extension is turned on for it, then check again.",
	},
	"ja": {
		connection: "接続", connected: "接続済み",
		connectedDetail: "サービスアカウント · 読み取れる保管庫：%d", checkAgain: "もう一度確認",
		notSetUp:       "まだ接続されていません",
		notSetUpDetail: "この拡張機能の設定で、1Password のサービスアカウントトークンを入力するか、op がサインインしているコンピュータを選んでください。",
		setUpTitle:     "設定方法",
		setUpSteps: []string{
			"1. 1Password.com にサインインし、サイドバーの「開発者」からサービスアカウントを作成します。",
			"2. Persona に見せてよい保管庫だけを、読み取り権限で割り当てます。",
			"3. トークン（ops_ で始まります）をコピーし、この拡張機能の設定に貼り付けます。保存前に 1Password で確認します。",
		},
		canTitle: "アシスタントにできること",
		can: []string{
			"Persona が読み取れる保管庫を一覧し、タイトル・タグ・ウェブサイトでアイテムを探す。",
			"アイテムにどんなフィールドがあるかと、各フィールドの op:// 参照を見る。",
			"パスワード・キー・メモ・ワンタイムコードがアシスタントに渡ることはなく、保管庫を変更することもありません。",
		},
		vaultsTitle: "保管庫", vaultsFooter: "対象の保管庫は 1Password.com のサービスアカウント設定で変更できます。",
		items: "%d 件のアイテム", moreVaults: "ほかに %d 件の保管庫があります。", noVaults: "保管庫がありません",
		noVaultsDetail:    "サービスアカウントが読み取れる保管庫がまだありません。1Password.com で保管庫を割り当ててください。",
		unreachable:       "1Password に接続できません",
		unreachableGuide:  "hub から 1Password に接続できるか確認してから、もう一度確認してください。",
		refused:           "1Password がトークンを拒否しました",
		refusedGuide:      "トークンが取り消されたか期限切れの可能性があります。新しいトークンを作成し、この拡張機能の設定に貼り付けてください。",
		rateLimited:       "1Password のリクエスト上限に達しました",
		rateLimitedGuide:  "サービスアカウントは 1 時間あたりのリクエスト数に上限があります。しばらくしてからお試しください。",
		timedOut:          "1Password が時間内に応答しませんでした。",
		says:              "1Password からのメッセージ：%s",
		checkNotToken:     "1Password のサービスアカウントトークンではありません（ops_ で始まります）。",
		checkRefused:      "1Password がこのトークンを受け付けませんでした：%s",
		secretNoToken:     "1Password が設定されていません。1Password 拡張機能の設定で、サービスアカウントトークンを入力するか、op のあるコンピュータを選んでください。",
		secretNotFound:    "該当する保管庫・アイテム・フィールドが 1Password に見つかりません：%s",
		secretEmpty:       "このフィールドは 1Password で空です。",
		secretInvalid:     "Persona が読める 1Password の参照ではありません。op://保管庫/アイテム/フィールド の形で書いてください。",
		usesTitle:         "Persona による使用",
		usesFooter:        "この拡張機能の前回の起動以降に Persona から報告された使用です（新しい順）。Persona が短時間の記憶から再利用したキーは重ねて報告されません。値はここに保存されません。Persona 側にも記録があります。",
		noUses:            "まだ使用されていません",
		noUsesDetail:      "ほかの拡張機能のシークレットやモデルプロバイダのキーで「参照を使う」をオンにし、op://保管庫/アイテム/フィールド を入力してください。使われるたびに Persona がこの拡張機能に値を問い合わせます。",
		useExtension:      "拡張機能のシークレット · %s",
		useLauncher:       "モデルプロバイダのキー · %s",
		useAgent:          "Agent の実行用 · %s",
		useOn:             "（%s）",
		bySettings:        "設定で保存",
		byRule:            "「常に許可」ルール",
		byDevice:          "デバイスで承認",
		opConnectedDetail: "コンピュータの op · 読み取れる保管庫：%d",
		opSetUpTitle:      "または、お使いのコンピュータの op を使う",
		opSetUpSteps: []string{
			"1. Persona につないだ Mac か Linux のコンピュータに、1Password アプリと 1Password CLI（op）をインストールします。",
			"2. 1Password アプリの「設定」›「開発者」で「1Password CLI と連携」（Integrate with 1Password CLI）をオンにします。",
			"3. この拡張機能の設定でそのコンピュータを選びます。初めて使うときは、そのコンピュータの 1Password アプリで op を承認してください。",
		},
		opVaultsFooter:     "コンピュータの op がサインインしているアカウントの保管庫です。",
		opNoVaultsDetail:   "コンピュータの op が読み取れる保管庫がありません。",
		opRateLimitedGuide: "1Password がこのアカウントのリクエストを制限しています。しばらくしてからお試しください。",
		opMissing:          "そのコンピュータに op がインストールされていません",
		opMissingGuide:     "そこに 1Password CLI（op）をインストールし、1Password アプリの開発者設定で「1Password CLI と連携」をオンにしてから、もう一度確認してください。",
		opNoAccount:        "そのコンピュータの op がサインインしていません",
		opNoAccountGuide:   "そのコンピュータの 1Password アプリで「設定」›「開発者」を開き、「1Password CLI と連携」をオンにしてから、もう一度確認してください。",
		opLocked:           "そのコンピュータの 1Password がロックされています",
		opLockedGuide:      "そのコンピュータの 1Password アプリのロックを解除し、op の承認を求められたら承認してから、もう一度確認してください。Persona が自動で再試行することはありません。",
		opOffline:          "そのコンピュータはオフラインです",
		opOfflineGuide:     "コンピュータがオンラインに戻ると、Persona は再び op を使えます。",
		opUnreachableGuide: "コンピュータの電源が入っていて、この拡張機能がそのコンピュータでオンになっているか確認してから、もう一度確認してください。",
	},
	"zh-Hans": {
		connection: "连接", connected: "已连接",
		connectedDetail: "服务账号 · 可读的保险库：%d 个", checkAgain: "重新检查",
		notSetUp:       "还没有连接",
		notSetUpDetail: "在这个扩展的设置里填 1Password 服务账号令牌，或者选一台 op 已登录的电脑。",
		setUpTitle:     "怎么设置",
		setUpSteps: []string{
			"1. 登录 1Password.com，在侧边栏选「开发者」，创建一个服务账号。",
			"2. 只把 Persona 可以查看的保险库授权给它，给「读取」权限即可。",
			"3. 复制令牌（以 ops_ 开头），粘贴到这个扩展的设置里。保存前会先向 1Password 检查一次。",
		},
		canTitle: "助手能做什么",
		can: []string{
			"列出 Persona 能读的保险库，按标题、标签或网站查找里面的条目。",
			"看一个条目有哪些字段，以及每个字段的 op:// 引用。",
			"密码、密钥、备注和一次性验证码从不交给助手，也不会改动你的保险库。",
		},
		vaultsTitle: "保险库", vaultsFooter: "要换哪些保险库，在 1Password.com 这个服务账号的设置里改。",
		items: "%d 个条目", moreVaults: "还有 %d 个保险库没有列出。", noVaults: "没有保险库",
		noVaultsDetail:    "这个服务账号还读不到任何保险库。去 1Password.com 给它授权保险库。",
		unreachable:       "连不上 1Password",
		unreachableGuide:  "确认 hub 能访问 1Password，然后重新检查。",
		refused:           "1Password 拒绝了这个令牌",
		refusedGuide:      "令牌可能已被撤销或已过期。新建一个令牌，粘贴到这个扩展的设置里。",
		rateLimited:       "达到 1Password 的请求上限",
		rateLimitedGuide:  "服务账号每小时的请求数有上限，稍后再试。",
		timedOut:          "1Password 没有及时回应。",
		says:              "1Password 的原话：%s",
		checkNotToken:     "这不是 1Password 服务账号令牌（应以 ops_ 开头）。",
		checkRefused:      "1Password 不接受这个令牌：%s",
		secretNoToken:     "1Password 还没设置：去 1Password 扩展的设置里填服务账号令牌，或者选一台装了 op 的电脑。",
		secretNotFound:    "1Password 里找不到对应的保险库、条目或字段：%s",
		secretEmpty:       "这个字段在 1Password 里是空的。",
		secretInvalid:     "这不是 Persona 能读的 1Password 引用：写成 op://保险库/条目/字段。",
		usesTitle:         "Persona 的取用",
		usesFooter:        "这个扩展这次启动以来 Persona 报来的取用，最新的在前。Persona 从自己短时间的记忆里再次用到的 Key 不会重复报。值从不在这里保存；Persona 自己也有记录。",
		noUses:            "还没有取用",
		noUsesDetail:      "在别的扩展的密钥或模型服务商的 Key 里打开「使用引用」，填 op://保险库/条目/字段。之后每次用到，Persona 都会向这个扩展取值。",
		useExtension:      "扩展密钥 · %s",
		useLauncher:       "模型服务商 Key · %s",
		useAgent:          "交给 Agent 运行 · %s",
		useOn:             "（%s）",
		bySettings:        "设置里保存的",
		byRule:            "「始终允许」规则",
		byDevice:          "在设备上批准",
		opConnectedDetail: "电脑上的 op · 可读的保险库：%d 个",
		opSetUpTitle:      "或者用你电脑上的 op",
		opSetUpSteps: []string{
			"1. 在连着 Persona 的一台 Mac 或 Linux 电脑上，装好 1Password App 和 1Password CLI（op）。",
			"2. 在 1Password App 的「设置」›「开发者」里，打开「与 1Password CLI 集成」（Integrate with 1Password CLI）。",
			"3. 在这个扩展的设置里选这台电脑。第一次用时，在那台电脑的 1Password App 里批准 op。",
		},
		opVaultsFooter:     "这些是那台电脑上 op 登录的账号里的保险库。",
		opNoVaultsDetail:   "电脑上的 op 读不到任何保险库。",
		opRateLimitedGuide: "1Password 限制了这个账号的请求次数，稍后再试。",
		opMissing:          "那台电脑上没装 op",
		opMissingGuide:     "在那台电脑上装 1Password CLI（op），并在 1Password App 的开发者设置里打开「与 1Password CLI 集成」，然后重新检查。",
		opNoAccount:        "那台电脑上的 op 没有登录",
		opNoAccountGuide:   "在那台电脑的 1Password App 里打开「设置」›「开发者」，打开「与 1Password CLI 集成」，然后重新检查。",
		opLocked:           "那台电脑上的 1Password 锁着",
		opLockedGuide:      "解锁那台电脑上的 1Password App，它请你批准 op 时点批准，然后重新检查。Persona 不会自己反复重试。",
		opOffline:          "那台电脑离线了",
		opOfflineGuide:     "电脑回来后，Persona 会再用上面的 op。",
		opUnreachableGuide: "看看那台电脑开着没有、这个扩展有没有在那台电脑上打开，然后重新检查。",
	},
}

func textsFor(locale string) *texts {
	if t := locales[locale]; t != nil {
		return t
	}
	if strings.HasPrefix(locale, "zh") {
		return locales["zh-Hans"]
	}
	if i := strings.IndexAny(locale, "-_"); i > 0 {
		if t := locales[locale[:i]]; t != nil {
			return t
		}
	}
	return locales["en"]
}
