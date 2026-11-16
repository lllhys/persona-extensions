package signals

import (
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
)

// Word lists of the cheap screen (design 3.1), by language. The screen only decides what a model looks at: a list is
// wide on purpose (a miss is never looked at again, a wrong hit costs one judgement). Every language's list is used
// on every text: people mix languages, and a word of another language is a cheap extra hit at worst. A user adds
// words of their own (signals.json "words", AddWord); they are used the same way.
var lexicon = map[string]map[string][]string{
	// The user corrects the AI: the ledger's screen (5.1).
	entry.SigCorrection: {
		"zh": {"不对", "不是", "错了", "搞错", "弄错", "理解错", "说过", "说了", "讲过", "回答过", "答过", "又", "还是", "怎么", "为啥", "为什么", "啥",
			"漏了", "漏掉", "忘了", "别", "不要", "不用", "不行", "没用", "重新", "应该", "跑偏", "走歪", "歪了", "不准", "有问题", "问题",
			"我让你", "我要的是", "不是说", "早就", "明明", "？？", "??", "能不能", "先别", "停", "干嘛", "什么鬼", "瞎", "乱"},
		"en": {"wrong", "not what", "that's not", "isn't", "don't", "didn't", "doesn't", "again", "already", "i said", "i told",
			"i asked", "why did", "why are", "instead", "should have", "should be", "stop", "missed", "forgot", "still",
			"incorrect", "no,", "no.", "nope", "not right", "??"},
		"ja": {"違う", "ちがう", "言った", "また", "なんで", "やめて", "間違", "じゃなくて", "もう一度"},
	},
	// The AI asks the user about infrastructure: a machine, port, account, path, which tool (needs a question too).
	entry.SigAskInfra: {
		"zh": {"哪台", "哪个机器", "哪台机器", "端口", "账号", "帐号", "密码", "用哪个", "哪个工具", "地址", "主机", "服务器", "路径", "凭据", "令牌",
			"密钥", "ssh", "登录", "环境变量", "哪个目录", "装在哪"},
		"en": {"which machine", "which host", "which server", "port", "account", "password", "credential", "which tool",
			"where is", "hostname", "ip address", "ssh", "login", "token", "api key", "which directory", "path to"},
		"ja": {"どのマシン", "ポート", "アカウント", "パスワード", "どのツール"},
	},
	// The agent says it is stuck.
	entry.SigStuck: {
		"zh": {"卡住", "卡在", "没法", "无法", "失败了", "绕不过", "做不到", "搞不定", "一直报错", "还是报错", "还是失败", "解决不了"},
		"en": {"stuck", "unable to", "i can't", "i cannot", "cannot proceed", "keeps failing", "still failing", "giving up",
			"not able to", "blocked on", "no way to"},
		"ja": {"できません", "失敗しました", "詰まって"},
	},
	// A permission prompt or an intercepted action, in a notice.
	entry.SigBlocked: {
		"zh": {"拦截", "拒绝", "没有权限", "无权限", "权限", "被拦", "不允许"},
		"en": {"denied", "blocked", "permission", "not allowed", "rejected", "intercept", "refused"},
		"ja": {"拒否", "権限", "ブロック"},
	},
	// The user approves a named way of doing it.
	entry.SigPraise: {
		"zh": {"这样就对了", "就按这个", "就这么做", "就是这样", "很好", "不错", "可以了", "完美", "对了", "正是", "这个好"},
		"en": {"perfect", "exactly", "that's right", "this works", "that works", "good job", "well done", "keep doing"},
		"ja": {"完璧", "その通り", "いいね"},
	},
	// The user says to always do it this way, or to remember.
	entry.SigExplicit: {
		"zh": {"以后", "记住", "每次都", "一律", "永远", "都这么", "都要", "下次", "默认", "统一"},
		"en": {"from now on", "always", "never ", "remember", "next time", "by default", "every time"},
		"ja": {"今後", "覚えて", "いつも", "毎回"},
	},
}

// alreadySaid: a correction that says the question was answered before (R2).
var alreadySaid = []string{"说过", "说了", "讲过", "回答过", "答过", "不是说", "我没说过吗", "already told", "i said", "i told", "already answered", "言った"}

// questionMarks: an ask_infra hit in the agent's text needs a question.
var questionMarks = []string{"?", "？", "请提供", "请告诉", "告诉我", "please provide", "can you tell", "could you tell", "let me know"}

// match returns the first word of a signal's lists (and the user's words) found in s, or "".
func match(signal, s string, extra map[string][]string) string {
	low := strings.ToLower(s)
	for _, words := range lexicon[signal] {
		for _, w := range words {
			if strings.Contains(low, strings.ToLower(w)) {
				return w
			}
		}
	}
	for _, w := range extra[signal] {
		if w = strings.TrimSpace(w); w != "" && strings.Contains(low, strings.ToLower(w)) {
			return w
		}
	}
	return ""
}

func containsAny(s string, words []string) bool {
	low := strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(low, strings.ToLower(w)) {
			return true
		}
	}
	return false
}
