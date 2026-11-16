package convsrc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona/sdk/go/wire"
)

const session = "0a1b2c3d-1111-2222-3333-444455556666"

// rec is one synthetic transcript record.
func rec(typ, at string, content any, extra ...string) string {
	m := map[string]any{"type": typ, "timestamp": at, "sessionId": session,
		"message": map[string]any{"role": typ, "content": content}}
	for _, e := range extra {
		m[e] = true
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// transcript writes a synthetic conversation (no real content) and returns its directory.
func transcript(t *testing.T) string {
	t.Helper()
	long := strings.Repeat("长", 700)
	paste := strings.Repeat("贴", 2500)
	lines := []string{
		`{"type":"queue-operation","timestamp":"2026-11-05T16:59:00Z"}`,
		rec("user", "2026-11-05T17:00:00Z", "<persona-context>\nnow: Thursday\n</persona-context>\n以后改代码前先跑一遍测试，别直接提交"),
		rec("assistant", "2026-11-05T17:00:05Z", []any{map[string]any{"type": "thinking", "thinking": "secret reasoning"}, text("好的，我先跑测试。")}),
		rec("assistant", "2026-11-05T17:00:06Z", []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{}}}),
		rec("user", "2026-11-05T17:00:07Z", []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}}),
		rec("assistant", "2026-11-05T17:00:09Z", []any{text("测试都过了。")}),
		rec("user", "2026-11-05T17:01:00Z", "Caveat: meta", "isMeta"),
		rec("user", "2026-11-05T17:02:00Z", "<command-name>/clear</command-name>\n<command-message>clear</command-message>"),
		rec("user", "2026-11-05T17:03:00Z", "🔔 任务「周报」完成了\n这是 Persona 的任务通知，不是用户发的消息。"),
		rec("user", "2026-11-05T17:04:00Z", "⏰ 提醒：跟进 PR"),
		rec("assistant", "2026-11-05T17:04:10Z", []any{text(long)}),
		rec("user", "2026-11-05T17:05:00Z", []any{text("[Request interrupted by user]")}),
		rec("user", "2026-11-05T17:06:00Z", []any{text("<system-reminder>be brief</system-reminder>结论要写在最前面"), text("[Request interrupted by user for tool use]")}),
		`not json at all`,
		rec("user", "2026-11-06T09:00:00Z", paste), // 01:00 local on the 6th
	}
	dir := t.TempDir()
	proj := filepath.Join(dir, "project-a")
	if err := os.MkdirAll(filepath.Join(proj, session, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, session+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A subagent's transcript is not a conversation with the user.
	sub := strings.ReplaceAll(rec("user", "2026-11-05T18:00:00Z", "subagent task"), session, "ffffffff-0000")
	if err := os.WriteFile(filepath.Join(proj, session, "subagents", "agent-1.jsonl"), []byte(sub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func source(t *testing.T) (kbsrc.Source, string) {
	t.Helper()
	dir := transcript(t)
	fx, err := kbsrc.LoadFixture(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return FromDir(fx, dir, map[string]string{session: "user", "ffffffff-0000": "user"}, loc), dir
}

// TestConversationDay: the user's turns are cleaned of injected blocks, commands and notifications are dropped,
// the assistant's text is joined per turn without thinking or tool calls, and the lines parse as 我 / 助手.
func TestConversationDay(t *testing.T) {
	src, _ := source(t)
	ctx := context.Background()
	r, err := src.Day(ctx, "persona-0a1b2c3d", "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	var got []norm.Line
	for i, m := range r.Messages {
		if m.N != i+1 {
			t.Errorf("message %d numbered %d", i, m.N)
		}
		got = append(got, norm.Parse(m.N, m.Text))
	}
	if len(got) != 4 {
		t.Fatalf("messages: %+v", r.Messages)
	}
	want := []struct{ time, speaker, body string }{
		{"09:00", "我", "以后改代码前先跑一遍测试，别直接提交"},
		{"09:00", "助手", "好的，我先跑测试。\n测试都过了。"},
		{"", "助手", ""}, // the long reply after a notification, head and tail only
		{"09:06", "我", "结论要写在最前面"},
	}
	for i, w := range want {
		if got[i].Speaker != w.speaker || (w.time != "" && got[i].Time != w.time) || (w.body != "" && got[i].Body != w.body) {
			t.Errorf("line %d: %+v, want %+v", i+1, got[i], w)
		}
	}
	if b := got[2].Body; !strings.Contains(b, "……") || len([]rune(b)) != 300+200+2 {
		t.Errorf("long reply kept %d runes: %q", len([]rune(b)), b)
	}
	for _, l := range got {
		for _, bad := range []string{"secret", "persona-context", "now: Thursday", "/clear", "🔔", "⏰", "Request interrupted", "be brief"} {
			if strings.Contains(l.Body, bad) {
				t.Errorf("line %d keeps %q: %q", l.N, bad, l.Body)
			}
		}
	}
	// The next local day: a pasted block is cut down to its head.
	r, err = src.Day(ctx, "persona-0a1b2c3d", "2026-11-06")
	if err != nil || len(r.Messages) != 1 {
		t.Fatalf("2026-11-06: %+v %v", r, err)
	}
	l := norm.Parse(1, r.Messages[0].Text)
	if l.Time != "01:00" || l.Speaker != "我" || !strings.HasSuffix(l.Body, "…[疑似粘贴材料，略去 1900 字]…") || len([]rune(l.Body)) > 650 {
		t.Errorf("pasted: %s %s %d runes", l.Time, l.Speaker, len([]rune(l.Body)))
	}
	if _, err := src.Day(ctx, "persona-0a1b2c3d", "2026-11-07"); err != kbsrc.ErrNotFound {
		t.Errorf("a day without turns: %v", err)
	}
}

// TestConversationListed: a conversation is a chat of Recent with the meta the triage uses (it is not in List: the
// hub lists conversations by time only); the subagent's transcript is not one; the directory is left as it was.
func TestConversationListed(t *testing.T) {
	src, dir := source(t)
	before := snapshot(t, dir)
	ctx := context.Background()
	in := func(since, until time.Time) []wire.KBEntry {
		r, err := src.Recent(ctx, since, until)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	all := in(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Time{})
	if len(all) != 1 || all[0].ID != "persona-0a1b2c3d" || all[0].Source != "persona" ||
		!all[0].Updated.Equal(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("recent: %+v", all)
	}
	if es, _ := src.List(ctx, "chats"); len(es) != 0 {
		t.Errorf("list: %+v", es)
	}
	d, err := src.Get(ctx, "chats", "persona-0a1b2c3d")
	if err != nil {
		t.Fatal(err)
	}
	m := kbsrc.DecodeMeta(d)
	if m.Source != "persona" || m.Kind != "assistant" || !m.Mine || m.Name != "Persona 对话 · 以后改代码前先跑一遍测试，别直接提交" {
		t.Errorf("meta: %+v", m)
	}
	if r := in(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 17, 30, 0, 0, time.UTC)); len(r) != 1 {
		t.Errorf("recent with a turn in the window: %+v", r)
	}
	if r := in(time.Date(2026, 11, 5, 17, 30, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); len(r) != 0 {
		t.Errorf("recent without a turn in the window: %+v", r)
	}
	if after := snapshot(t, dir); after != before {
		t.Errorf("the transcript directory changed")
	}
}

func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			b.WriteString(p + " " + info.Mode().String() + " " + info.ModTime().String() + "\n")
		}
		return nil
	})
	return b.String()
}

// A transcript is read only when the hub's list says the user opened its conversation: one a task opened (its role=user
// messages are Persona's prompts) is not, and with no list none is.
func TestTranscriptsOnlyUserOpened(t *testing.T) {
	_, dir := source(t)
	loc := time.UTC
	ctx := context.Background()
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for name, origins := range map[string]map[string]string{
		"task":    {session: "task"},
		"spawned": {session: "spawned"},
		"agent":   {session: "lark_agent"},
		"none":    nil,
	} {
		src := FromDir(emptySource{}, dir, origins, loc)
		if es, _ := src.Recent(ctx, since, time.Time{}); len(es) != 0 {
			t.Errorf("%s: read %+v", name, es)
		}
	}
	src := FromDir(emptySource{}, dir, map[string]string{session: "channel"}, loc)
	if es, _ := src.Recent(ctx, since, time.Time{}); len(es) != 1 {
		t.Errorf("channel: %+v", es)
	}
}

// The hub's own messages typed in as the user (supervision, inbox notes, wake-ups, a node restart) are not turns.
func TestInjectedAreNotTheUsers(t *testing.T) {
	for _, s := range []string{
		"📋 督办：你派出去的任务有 2 个要你处理\n这是 Persona 的督办消息\n<persona-supervision level=\"triage\" />",
		"📥 收件箱事件「报销」有了下文\n这是 Persona 的收件箱通知",
		"⏰（Persona 叫醒你，第 2 次）你上一轮停下时说在等「CI」",
		"⏰（Persona 叫你接着做）订阅的用量额度已经重置了",
		"⚠️ 运行你的 node 刚重启过，下面这些后台任务停了",
		"<persona-context>now</persona-context>🔔 任务「巡检」做完了\n这是 Persona 的任务通知，不是用户发的消息。",
	} {
		if text, ok := cleanUser(s); ok {
			t.Errorf("kept as the user's: %q", text)
		}
	}
	if text, ok := cleanUser("<persona-context>now</persona-context>以后先给结论"); !ok || text != "以后先给结论" {
		t.Errorf("the user's words: %q %v", text, ok)
	}
}
