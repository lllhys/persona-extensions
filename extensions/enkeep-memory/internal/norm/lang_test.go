package norm

import "testing"

func TestUserWord(t *testing.T) {
	for lang, want := range map[string]string{"zh-Hans": "用户", "zh_CN": "用户", "en-US": "the user", "ja": "ユーザー", "xx": "the user", "": "用户"} {
		if got := UserWord(lang); got != want {
			t.Errorf("%s: %q", lang, got)
		}
	}
}

func TestInLanguage(t *testing.T) {
	for _, c := range []struct {
		lang, text string
		ok         bool
	}{
		{"zh-Hans", "用户要求给选项时写清楚每个选项是什么", true},
		{"zh-Hans", "连测试机用 jump-dev 别名，用 model-x-1.0 跑 tool-x --provider provider-x", true},
		{"zh-Hans", "派 Agent 时带上 project-x 的 deploy checklist", true},
		{"zh-Hans", "The user wants every option spelled out", false},
		{"zh-Hans", "ユーザーは選択肢を全部書いてほしい", false},
		{"en", "The user wants every option spelled out, as in 「不要只写代号」", true},
		{"en", "Ask Alex Park before merging into project-x", true},
		{"en", "用户要求给选项时写清楚每个选项是什么", false},
		{"ja", "ユーザーは選択肢を全部書いてほしい", true},
		{"ja", "用户要求给选项时写清楚", false},
		{"en", "OK", true}, // too little to judge
	} {
		if got := InLanguage(c.text, c.lang); got != c.ok {
			t.Errorf("%s %q: %v", c.lang, c.text, got)
		}
	}
}

func TestSelfReference(t *testing.T) {
	for _, c := range []struct {
		lang, text string
		bad        bool
	}{
		{"zh-Hans", "我要求选项写全称", true},
		{"zh-Hans", "主人不喜欢被打断", true},
		{"zh-Hans", "用户的 owner 身份", true},
		{"zh-Hans", "用户说「我最近很忙」，这周少打扰", false},
		{"zh-Hans", "用户做自我介绍时先讲项目", false},
		{"en", "I prefer options spelled out", true},
		{"en", "Remind me before the deadline", true},
		{"en", "The owner wants options spelled out", true},
		{"en", "Ask the owner of the repository first", false},
		{"en", "The user said \"I am swamped\"", false},
		{"en", "The user wants options spelled out", false},
	} {
		if got := SelfReference(c.text, c.lang); got != c.bad {
			t.Errorf("%s %q: %v", c.lang, c.text, got)
		}
	}
}
