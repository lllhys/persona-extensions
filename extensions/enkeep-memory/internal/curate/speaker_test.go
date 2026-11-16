package curate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// A Persona conversation where the assistant does most of the talking, quotes the user back ("你之前说过…"),
// proposes rules the user only says "好" to, and writes a text the user pastes on.
var mixed = []string{
	"09:00 我: 帮我看下这周的部署安排",            // 1
	"09:01 助手: 你之前说过以后部署都先灰度，我就按这个来排", // 2: the assistant quoting the user back
	"09:02 我: 好", // 3
	"09:03 助手: 另外建议每次改动都跑一遍全量测试，周五不上线", // 4: the assistant's proposal
	"09:04 我: 对", // 5
	"09:05 我: 以后给我汇报都先说结论，再说过程，别铺垫", // 6: the user's own rule
	"09:06 助手: 好的，以后先给结论。",          // 7
	"09:07 我: 下面这份可以直接转给她。文中的“我”是你，“你”是她。\n\n【一、眼下要做的事】\n你：先确认时间。\n【二、之后】\n你：再约见面。\n【三、注意】\n你：别着急。" + strings.Repeat("补充说明。", 40), // 8: pasted from the assistant
}

func mixedUnit() (*Unit, triage.Decision) {
	u := &Unit{Ref: "chats/persona-mix", Entity: "chats", Day: "2026-11-09",
		Meta: kbsrc.Meta{ID: "persona-mix", Name: "Persona 对话", Kind: "assistant", Source: "persona"}}
	d := triage.Decision{Chat: u.Ref, Shown: map[int]bool{}, Tier: triage.TierOwn}
	for i, t := range mixed {
		u.Lines = append(u.Lines, voice.Relabel(norm.Parse(i+1, t)))
		d.Shown[i+1] = true
	}
	return u, d
}

func newRun() *store.Run { return &store.Run{Counts: map[string]int{}} }

// The same conversation's digest: an item that rests on the assistant's words, or on the user's bare "好" to them,
// is refused (ASSISTANT_SOURCE); an item on the user's own line keeps only that line, with its role, as evidence.
// In a group, a bot that names the user is not a source either; a person who does is.
func TestAssistantWordsNeverBecomeItems(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	owner := []string{"用户", "Sam Rivera"}
	u, d := mixedUnit()
	out := schema.DailyOutput{Facts: []schema.Item{
		item("用户的部署都先灰度", 2, "以后部署都先灰度"),
		{Claim: "用户同意每次改动都跑全量测试", Evidence: []schema.Evidence{{N: 4, Quote: "每次改动都跑一遍全量测试"}, {N: 5, Quote: "对"}}, Confidence: "high"},
		item("用户要先确认时间再约见面", 8, "下面这份可以直接转给她"),
		{Claim: "用户要求汇报先说结论", Evidence: []schema.Evidence{{N: 6, Quote: "先说结论，再说过程"}, {N: 7, Quote: "好的，以后先给结论"}}, Confidence: "high"},
	}}
	raw, _ := json.Marshal(out)
	run := newRun()
	c.ingest(c.builder(u.Day, owner), u, d, owner, c.knownEntities(context.Background()), string(raw), &store.Done{Key: "k-mix"}, run, nil)
	for _, claim := range []string{"用户的部署都先灰度", "用户同意每次改动都跑全量测试", "用户要先确认时间再约见面"} {
		if got := rejected(c, claim); got != verify.AssistantSource {
			t.Errorf("%s: %q, want ASSISTANT_SOURCE", claim, got)
		}
	}
	var kept0 bool
	for _, it := range c.St.S.Items {
		if it.Claim == "用户要求汇报先说结论" {
			kept0 = true
			if len(it.Evidence) != 1 || it.Evidence[0].N != 6 || it.Evidence[0].Role != voice.RoleUser || it.Evidence[0].Chat != "chats/persona-mix" {
				t.Errorf("evidence %+v", it.Evidence)
			}
		}
	}
	if !kept0 {
		t.Errorf("the user's own item was refused: %q", rejected(c, "用户要求汇报先说结论"))
	}

	// A group: a bot naming the user is no source; a colleague naming the user is.
	c2, _ := newCurator(t, nil, "shadow")
	lines := []string{
		"10:00 CI Bot: @Sam Rivera 你的流水线失败了，已自动回滚", // 3
		"10:01 Avery: @Sam Rivera 周五前把压测报告发出来",     // 4
	}
	ingestOne(t, c2, owner, lines, schema.DailyOutput{Facts: []schema.Item{
		item("Sam 的流水线失败并已回滚", 3, "你的流水线失败了"),
		item("Sam 周五前要发压测报告", 4, "周五前把压测报告发出来"),
	}})
	if got := rejected(c2, "Sam 的流水线失败并已回滚"); got != verify.AssistantSource {
		t.Errorf("a bot naming the user: %q", got)
	}
	if !kept(c2, "Sam 周五前要发压测报告") {
		t.Errorf("a colleague naming the user: %q", rejected(c2, "Sam 周五前要发压测报告"))
	}
}
