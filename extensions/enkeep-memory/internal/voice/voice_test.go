package voice

import (
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
)

func TestGhostWritten(t *testing.T) {
	for _, s := range []string{
		"## Summary\n- fix\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)",
		"fix it\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>",
		"Generated with Codex",
		"（AI 代写）周报如下",
	} {
		if !GhostWritten(s) {
			t.Errorf("not caught: %q", s)
		}
	}
	for _, s := range []string{"这个 PR 先别合，CI 没跑", "Co-Authored-By: Alice <alice@example.com>", "我觉得 claude code 写得太啰嗦"} {
		if GhostWritten(s) {
			t.Errorf("false positive: %q", s)
		}
	}
	l := Relabel(norm.Line{N: 1, Speaker: norm.Me, Body: "LGTM\n\n🤖 Generated with [Claude Code](x)"})
	if l.Speaker != Ghost || Own(l) {
		t.Errorf("relabel: %+v", l)
	}
}

// The hub's owner can be named "用户" (its default) and have aliases like "我" or "owner": those words are in many
// messages that have nothing to do with the user. Only an @-mention or a distinctive real name counts.
func TestMentionsIgnoresGenericNames(t *testing.T) {
	names := []string{"用户", "我", "owner", "quillmoss", "张三"}
	no := []string{
		"这个功能对用户来说太复杂了",
		"我觉得可以",
		"the owner of this repo",
		"quillmossbot 推送",
		"用户反馈说加载慢",
	}
	for _, s := range no {
		if Mentions(s, names) {
			t.Errorf("mentions %q", s)
		}
	}
	yes := []string{"@quillmoss 看下这个", "@张三 你来定", "张三说下周上线", "ping Quillmoss please"}
	for _, s := range yes {
		if !Mentions(s, names) {
			t.Errorf("missed %q", s)
		}
	}
	if Mentions("@用户 你好", []string{"用户"}) {
		t.Error("a generic name never counts, not even after @")
	}
	if got := Names([]string{"用户", " quillmoss ", "Owner", "我"}); len(got) != 1 || got[0] != "quillmoss" {
		t.Errorf("Names = %v", got)
	}
}

func TestSelect(t *testing.T) {
	var lines []norm.Line
	for i, sp := range []string{"A", "B", "C", norm.Me, "D", "E", "F", "G", Ghost, "H"} {
		lines = append(lines, norm.Line{N: i + 1, Speaker: sp, Body: "x"})
	}
	shown := map[int]bool{}
	for _, l := range lines {
		shown[l.N] = true
	}
	got := Select(lines, shown, 2, 1)
	for _, n := range []int{2, 3, 4, 5} {
		if !got[n] {
			t.Errorf("line %d not selected: %v", n, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("selected %v; a ghost-written line is not a turn of the user's", got)
	}
}

func TestFinance(t *testing.T) {
	for _, s := range []string{"您的账户可用余额为 <金额>", "工资已入账", "Your payslip for October is ready", "RSU vesting on 11/15", "信用卡账单已出"} {
		if !Finance(s) {
			t.Errorf("not caught: %q", s)
		}
	}
	for _, s := range []string{"CI 不调度就按本地验证合并", "接受一些误伤，调激进点"} {
		if Finance(s) {
			t.Errorf("false positive: %q", s)
		}
	}
}

// In a pull request the agent opened for the user (its body carries the trace), the user's later comments of the day
// are the agent's too; a thread without any trace is left alone.
func TestAgentThread(t *testing.T) {
	ls := []norm.Line{
		Relabel(norm.Line{N: 1, Speaker: norm.Me, Body: "Fix the window\n\n🤖 Generated with [Claude Code](x)"}),
		{N: 2, Speaker: "Casey", Body: "why AddDate?"},
		{N: 3, Speaker: norm.Me, Body: "Under the rules for CI that never runs, merged on local results."},
	}
	got := AgentThread(ls)
	if got[0].Speaker != Ghost || got[2].Speaker != Ghost || got[1].Speaker != "Casey" {
		t.Errorf("agent thread: %+v", got)
	}
	if ls[2].Speaker != norm.Me {
		t.Error("the input was changed")
	}
	plain := []norm.Line{{N: 1, Speaker: norm.Me, Body: "先别合，等 CI"}, {N: 2, Speaker: norm.Me, Body: "LGTM, but rename the flag"}}
	for _, l := range AgentThread(plain) {
		if l.Speaker != norm.Me {
			t.Errorf("a short comment of the user's was relabelled: %q", l.Body)
		}
	}
	// Untraced drafts: an agent's status update, a structured reply, a long write-up.
	for _, body := range []string{
		"Picking this up.\nPlan: as in the issue body.\nETA: today.\nAgent: yes",
		"Fixed in the new head:\n- the window ends at the next midnight\n- tests cover DST days",
		strings.Repeat("The non-chat filter added a fixed window. ", 10),
	} {
		if got := AgentThread([]norm.Line{{N: 1, Speaker: norm.Me, Body: body}}); got[0].Speaker != Ghost {
			t.Errorf("draft not caught: %q", body)
		}
	}
}

// An assistant's text the user pastes into a chat is not the user's: framed for passing on, or a structured
// write-up. A long message the user dictated, without either, stays the user's.
func TestPasted(t *testing.T) {
	framed := "下面这份可以直接转给她。文中的“我”是你，“你”是她。" + strings.Repeat("先确认时间，再约见面。", 25)
	structured := "【一、眼下要做的事】\n先确认时间。\n【二、之后】\n再约见面。\n【三、注意】\n别着急。" + strings.Repeat("补充。", 70)
	dictated := "我的判断啊，这个东西思路上是在卖算力，只不过算力是一个目前无法存储的事情，" + strings.Repeat("所以要看波峰波谷和可用算力，", 15)
	for _, s := range []string{framed, structured} {
		if !Pasted(s) {
			t.Errorf("not caught: %.20s…", s)
		}
		if l := Relabel(norm.Line{Speaker: norm.Me, Body: s}); l.Speaker != Ghost {
			t.Errorf("not relabelled: %.20s…", s)
		}
	}
	if Pasted(dictated) || Pasted("下面这份可以直接转给她") {
		t.Error("the user's own message taken for pasted")
	}
}

func TestRoleAndAgreement(t *testing.T) {
	for _, c := range []struct {
		l    norm.Line
		kind string
		want string
	}{
		{norm.Line{Speaker: norm.Me}, "group", RoleUser},
		{norm.Line{Speaker: AI}, "assistant", RoleAssistant},
		{norm.Line{Speaker: Ghost}, "pr", RoleAgentDraft},
		{norm.Line{Speaker: "CI Bot"}, "group", RoleBot},
		{norm.Line{Speaker: "Avery"}, "bot", RoleBot},
		{norm.Line{Speaker: "Avery"}, "group", RoleOther},
	} {
		if got := Role(c.l, c.kind); got != c.want {
			t.Errorf("%+v in %s: %s, want %s", c.l, c.kind, got, c.want)
		}
	}
	if !Source(RoleUser) || !Source(RoleOther) || Source(RoleAssistant) || Source(RoleAgentDraft) || Source(RoleBot) {
		t.Error("sources")
	}
	for _, s := range []string{"好", "好的！", "对", "嗯嗯", "就这样", "LGTM", "OK."} {
		if !Agreement(s) {
			t.Errorf("agreement not caught: %q", s)
		}
	}
	for _, s := range []string{"好，但先灰度", "以后先给结论", "对，以后都这样记"} {
		if Agreement(s) {
			t.Errorf("a statement taken for agreement: %q", s)
		}
	}
}

// An assistant relaying the user ("用户要求你…") is not the user, whoever posted it; a product's users are not the user.
func TestRelayed(t *testing.T) {
	for _, s := range []string{"用户要求你先跑测试再提交", "按用户的交代，先做 iOS", "我替用户回复一下：可以", "以用户名义发出", "用户之前说过你不用问"} {
		if !Relayed(s) {
			t.Errorf("not caught: %q", s)
		}
		if Relabel(norm.Line{Speaker: norm.Me, Body: s}).Speaker != Ghost {
			t.Errorf("not relabelled: %q", s)
		}
	}
	for _, s := range []string{"用户反馈说加载慢", "用户要求能导出 Excel", "这个功能对用户来说太复杂"} {
		if Relayed(s) {
			t.Errorf("false positive: %q", s)
		}
	}
}
