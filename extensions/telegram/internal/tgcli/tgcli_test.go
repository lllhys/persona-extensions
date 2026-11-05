package tgcli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixedNow(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
}

// Each command plans its tool with the arguments the connector reads; flags go anywhere, times are turned into what
// the connector takes, a message's text may come from stdin.
func TestCommandsBuildTheirCalls(t *testing.T) {
	fixedNow(t)
	for _, c := range []struct{ line, stdin, tool, args string }{
		{"chats", "", "telegram_list_chats", `{"unread_only":false}`},
		{"chats --unread --limit 5", "", "telegram_list_chats", `{"limit":5,"unread_only":true}`},
		{"unread", "", "telegram_list_chats", `{"unread_only":true}`},
		{"messages c12 --limit=20 --before 991 --since 2h", "", "telegram_read_messages",
			`{"before":991,"chat":"c12","limit":20,"since":"2026-11-03T10:00:00Z"}`},
		{"messages --until 2026-11-01 u7", "", "telegram_read_messages", `{"chat":"u7","until":"2026-11-01"}`},
		{"messages u7 --since 1793696400", "", "telegram_read_messages", `{"chat":"u7","since":"2026-11-03T09:00:00Z"}`},
		{"search 示例 方案 --chat c12 --since 2026-11-01T09:00:00+08:00", "", "telegram_search_messages",
			`{"chat":"c12","query":"示例 方案","since":"2026-11-01T09:00:00+08:00"}`},
		{"message u7 991", "", "telegram_get_message", `{"chat":"u7","id":991}`},
		{"chat c12", "", "telegram_get_chat", `{"chat":"c12"}`},
		{"members c12 --limit 30", "", "telegram_list_members", `{"chat":"c12","limit":30}`},
		{"send u7 好的 周五见", "", "telegram_send", `{"chat":"u7","text":"好的 周五见"}`},
		{"send @example -", "第一行\n第二行\n", "telegram_send", `{"chat":"@example","text":"第一行\n第二行"}`},
		{"reply c12 991 收到", "", "telegram_send", `{"chat":"c12","reply_to":991,"text":"收到"}`},
		{"forward c12 991,992 --to u7", "", "telegram_forward", `{"chat":"c12","ids":[991,992],"to":"u7"}`},
		{"react c12 991 👍", "", "telegram_react", `{"chat":"c12","emoji":"👍","id":991}`},
		{"read u7 --up-to 991", "", "telegram_mark_read", `{"chat":"u7","up_to":991}`},
		{"read u7", "", "telegram_mark_read", `{"chat":"u7"}`},
	} {
		p := PlanOf(strings.Fields(c.line), c.stdin)
		if p.Exit != nil || p.Call == nil {
			t.Errorf("tg %s: %+v", c.line, p)
			continue
		}
		args, _ := json.Marshal(p.Call.Args)
		if p.Call.Tool != c.tool || string(args) != c.args || p.Name != "tg" || p.Render != strings.Fields(c.line)[0] {
			t.Errorf("tg %s: plans %s %s (render %q); want %s %s", c.line, p.Call.Tool, args, p.Render, c.tool, c.args)
		}
	}
}

// A wrong command line plans nothing: what is wrong, with exit 2; help is exit 0.
func TestUsage(t *testing.T) {
	for _, line := range []string{"", "nope", "messages", "message u7 x", "chats --limit 0", "chats --limitt 5", "send u7",
		"forward c12 991", "forward c12 a,b --to u7", "messages u7 --since yesterday", "chats --unread=yes",
		"reply u7 0 hi", "chat c1 c2"} {
		p := PlanOf(strings.Fields(line), "")
		if p.Call != nil || p.Exit == nil || *p.Exit != 2 {
			t.Errorf("tg %q: %+v", line, p)
		}
		if line != "" && p.Stderr == "" {
			t.Errorf("tg %q: says nothing", line)
		}
	}
	if p := PlanOf([]string{"--help"}, ""); p.Exit == nil || *p.Exit != 0 || !strings.Contains(p.Stdout, "tg send <chat>") {
		t.Fatalf("help: %+v", p)
	}
	if p := PlanOf(strings.Fields("send u7 help"), ""); p.Call == nil {
		t.Fatal("a message that says help was taken for --help")
	}
}

// --format text reads the hub's JSON for people, as tg did.
func TestOutput(t *testing.T) {
	answer := `{"chats":[{"id":"c12","name":"示例群","kind":"group","unread":3,"mentions":1,` +
		`"last":{"id":991,"at":"2026-11-03T09:40:00+08:00","from":"示例用户","text":"周末的\n示例活动"}},{"id":"u7","name":"示例用户","kind":"dm"}],"more":true}`
	out, err := Render("chats", answer)
	want := "c12  示例群  群  未读 3（@你 1）\n    2026-11-03 09:40 [991] 示例用户：周末的 示例活动\nu7  示例用户  私聊\n（还有更多：--limit 加大）\n"
	if err != nil || out != want {
		t.Fatalf("text:\n%s\nwant\n%s (%v)", out, want, err)
	}
	out, _ = Render("messages", `{"chat":{"id":"u7","name":"示例用户","kind":"dm"},"messages":[{"id":990,"at":"2026-11-03T09:00:00+08:00","mine":true,"text":"在吗"},`+
		`{"id":991,"at":"2026-11-03T09:40:00+08:00","from":"示例用户","text":"在","reply_to":990}],"next_before":990}`)
	if out != "示例用户（u7）\n2026-11-03 09:00 [990] 我：在吗\n2026-11-03 09:40 [991] 示例用户（回复 990）：在\n（更早的：--before 990）\n" {
		t.Fatalf("messages as text:\n%s", out)
	}
	if out, _ = Render("send", `{"sent":{"id":992,"at":"2026-11-03T09:41:00+08:00","mine":true,"text":"好的"},"chat":"u7"}`); out != "已发送到 u7（消息 992）\n" {
		t.Fatalf("send as text: %q", out)
	}
	if _, err := Render("chats", "not json"); err == nil {
		t.Fatal("an answer that does not read")
	}
}
