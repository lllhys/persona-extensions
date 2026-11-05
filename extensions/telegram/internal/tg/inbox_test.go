package telegram

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/pluginkit"
)

func thingOf(things []connector.InboxThing, key string) *connector.InboxThing {
	for i := range things {
		if things[i].Thread == key {
			return &things[i]
		}
	}
	return nil
}

// The user's own triage rules: without them every thing needs the user, as the built-in connector said (a tier for
// sure, no rules); with them the hub's model sorts each by them. Either way every thing carries the fallback, so
// Telegram never waits for a model; pluginkit holds the things to the inbox rules as the hub's install check does.
func TestInboxTriageRules(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	f.mu.Lock()
	f.dialogs = []dialog{{Peer: peer{Kind: 'u', ID: 1, Hash: 11}, Name: "示例用户", Kind: "dm", Top: 3, TopAt: time.Now(), Unread: 1}}
	f.mu.Unlock()
	c.refreshInbox(ctx)
	for _, rules := range []string{"", "陌生人的私聊都算噪音\n机器人发的也是"} {
		c.WithRules(pluginkit.TriageRules([]byte(`{"triage_rules": ` + strconv.Quote(rules) + `}`)))
		things, _ := c.Collect(ctx)
		if len(things) != 1 || things[0].Extra == nil || things[0].Extra.RuleTier != "action" {
			t.Fatalf("rules %q: %+v", rules, things)
		}
		want := connector.TierAction
		if rules != "" {
			want = ""
		}
		if things[0].Tier != want || things[0].Rules != c.rules || rules != "" && !strings.Contains(c.rules, "\n") { // a "\\n" written out is a line break
			t.Fatalf("rules %q: tier %q, rules %q", rules, things[0].Tier, things[0].Rules)
		}
		if err := pluginkit.CheckThings(things, c.rules, true); err != nil {
			t.Fatalf("rules %q: %v", rules, err)
		}
	}
}

// What needs the user: an unread private message, a group's @ of the user; muted and archived ones stay out; what
// was answered or read closes.
func TestInbox(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	now := time.Now()
	f.mu.Lock()
	f.dialogs = []dialog{
		{Peer: peer{Kind: 'u', ID: 1, Hash: 11}, Name: "示例用户", Kind: "dm", Top: 3, TopAt: now, Unread: 1, TopFrom: "示例用户", TopText: "示例问题", Username: "example"},
		{Peer: peer{Kind: 'c', ID: 2, Hash: 22}, Name: "示例群", Kind: "group", Top: 12, TopAt: now, Unread: 5, Mentions: 1},
		{Peer: peer{Kind: 'c', ID: 6}, Name: "静音的示例群", Kind: "group", Top: 7, TopAt: now, Mentions: 2, Muted: true},
		{Peer: peer{Kind: 'u', ID: 7}, Name: "归档的示例用户", Kind: "dm", Top: 4, TopAt: now, Unread: 1, Archived: true},
		{Peer: peer{Kind: 'u', ID: 8}, Name: "我回过的示例用户", Kind: "dm", Top: 9, TopAt: now, TopOut: true},
		{Peer: peer{Kind: 'c', ID: 9}, Name: "没 @ 过我的示例群", Kind: "group", Top: 30, TopAt: now, Unread: 40},
		{Peer: peer{Kind: 'u', ID: 3}, Name: "示例机器人", Kind: "dm", Top: 5, TopAt: now, Unread: 1, Bot: true},
	}
	f.history["c2"][1].Mentioned = true // 12: the message that @ me
	f.mu.Unlock()
	c.refreshInbox(ctx)
	things, _ := c.Collect(ctx)

	dm := thingOf(things, "u1")
	if dm == nil || dm.Kind != "dm" || dm.Label != "私聊" || dm.Read || dm.Done != "" || dm.Tier != "action" || dm.Last != "3" ||
		dm.OpenURL != "tg://resolve?domain=example" || !strings.Contains(dm.Context, "我：示例回复") {
		t.Fatalf("dm %+v", dm)
	}
	group := thingOf(things, "c2")
	if group == nil || group.Kind != "mention" || group.Label != "@你" || group.Read || group.Last != "12" ||
		!strings.Contains(group.Context, "[图片]") || strings.Contains(group.Context, "群里的示例消息") {
		t.Fatalf("mention %+v", group)
	}
	for _, out := range []string{"c6", "u7", "c9", "u3"} {
		if thingOf(things, out) != nil {
			t.Errorf("%s is in the inbox", out)
		}
	}
	if r := thingOf(things, "u8"); r == nil || r.Done != connector.DoneReplied {
		t.Fatalf("answered %+v", r)
	}

	// Read on the phone: the dm is read; the group gets another mention later, then it is read.
	f.mu.Lock()
	f.dialogs[0].Unread = 0
	f.dialogs[1].Top, f.dialogs[1].Mentions = 15, 2
	f.mu.Unlock()
	c.refreshInbox(ctx)
	things, _ = c.Collect(ctx)
	if dm := thingOf(things, "u1"); !dm.Read {
		t.Fatalf("read dm %+v", dm)
	}
	if g := thingOf(things, "c2"); g.Last != "15" || g.Read {
		t.Fatalf("a new mention %+v", g)
	}
	f.mu.Lock()
	f.dialogs[1].Top, f.dialogs[1].Mentions = 16, 0
	f.mu.Unlock()
	c.refreshInbox(ctx)
	things, _ = c.Collect(ctx)
	if g := thingOf(things, "c2"); !g.Read || g.Last != "15" {
		t.Fatalf("read mentions %+v", g)
	}
	// Another mention after that is a new move (not the one already done).
	f.mu.Lock()
	f.dialogs[1].Top, f.dialogs[1].Mentions = 20, 1
	f.mu.Unlock()
	c.refreshInbox(ctx)
	things, _ = c.Collect(ctx)
	if g := thingOf(things, "c2"); g.Last != "20" || g.Read {
		t.Fatalf("a later mention %+v", g)
	}

	if err := c.Reply(context.Background(), connector.InboxReply{Thread: "u1", Text: "示例回复"}); err == nil {
		t.Fatal("replied: the connector only reads")
	}
}

func TestOpenURL(t *testing.T) {
	for _, c := range []struct {
		d    dialog
		want string
	}{
		{dialog{Peer: peer{Kind: 'u', ID: 1}, Username: "example"}, "tg://resolve?domain=example"},
		{dialog{Peer: peer{Kind: 'u', ID: 1}}, "tg://user?id=1"},
		{dialog{Peer: peer{Kind: 'c', ID: 2}, Top: 12}, "https://t.me/c/2/12"},
		{dialog{Peer: peer{Kind: 'g', ID: 6}}, ""},
	} {
		if got := openURL(c.d); got != c.want {
			t.Errorf("%+v: %q, want %q", c.d, got, c.want)
		}
	}
}

// The gap between the rounds Telegram's news sets off doubles after a flood wait, up to newsEveryMost, and halves back
// to newsEvery after each round without one.
func TestNewsGap(t *testing.T) {
	gap := newsEvery
	for _, c := range []struct {
		flooded bool
		want    time.Duration
	}{{false, time.Minute}, {true, 2 * time.Minute}, {true, 4 * time.Minute}, {true, 8 * time.Minute}, {true, 16 * time.Minute},
		{true, 16 * time.Minute}, {false, 8 * time.Minute}, {false, 4 * time.Minute}, {false, 2 * time.Minute}, {false, time.Minute},
		{false, time.Minute}} {
		if gap = newsGap(gap, c.flooded); gap != c.want {
			t.Fatalf("flooded %v: %s, want %s", c.flooded, gap, c.want)
		}
	}
}

// Telegram telling of something new all the time (an account in busy groups) does not set off work each time: the
// archive's kick and the inbox's refresh (which lists the conversations) come at most once a gap, and the gap doubles
// after a flood wait.
func TestNewsRoundsAreThrottled(t *testing.T) {
	newsEvery, newsEveryMost = 200*time.Millisecond, 800*time.Millisecond
	t.Cleanup(func() { newsEvery, newsEveryMost = time.Minute, 16*time.Minute })
	c, f, ctx := archiveAccount(t)
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var kicks []time.Time // the archive's
	go c.Watch(wctx, func(s connector.Scope) {
		if s == connector.ScopeChats {
			mu.Lock()
			kicks = append(kicks, time.Now())
			mu.Unlock()
		}
	})
	f.mu.Lock()
	updated := f.updated
	f.mu.Unlock()
	lists := func() []sentRequest {
		var out []sentRequest
		for _, r := range f.requests() {
			if r.method == "Dialogs" {
				out = append(out, r)
			}
		}
		return out
	}
	news := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			updated()
		}
	}
	spaced := func(what string, at []time.Time, least time.Duration) {
		t.Helper()
		for i := 1; i < len(at); i++ {
			if d := at[i].Sub(at[i-1]); d < least-5*time.Millisecond {
				t.Fatalf("%s %s apart", what, d)
			}
		}
	}
	before := len(lists())
	news(time.Second)
	got := lists()[before:]
	if len(got) < 3 || len(got) > 6 {
		t.Fatalf("%d refreshes in a second of news every 20 ms, with a 200 ms gap", len(got))
	}
	var starts []time.Time
	for _, r := range got {
		starts = append(starts, r.start)
	}
	spaced("refreshes", starts, newsEvery)
	mu.Lock()
	spaced("archive kicks", slices.Clone(kicks), newsEvery)
	n := len(kicks)
	mu.Unlock()
	if n < 3 || n > 6 {
		t.Fatalf("%d archive kicks in a second of news", n)
	}

	var once sync.Once
	flooded := make(chan struct{})
	f.mu.Lock()
	f.fail = func(method string) error {
		var err error
		if method == "Dialogs" {
			once.Do(func() { err = tgerr.New(420, "FLOOD_WAIT_0"); close(flooded) })
		}
		return err
	}
	f.mu.Unlock()
	go news(2 * time.Second)
	select {
	case <-flooded:
	case <-time.After(3 * time.Second):
		t.Fatal("no refresh met the flood wait")
	}
	at := len(lists())
	time.Sleep(1500 * time.Millisecond)
	after := lists()[at-1:] // the flooded one, then the next ones
	if len(after) < 2 {
		t.Fatalf("no refresh after the flood wait")
	}
	if d := after[1].start.Sub(after[0].start); d < 2*newsEvery-5*time.Millisecond {
		t.Fatalf("the round after a flood wait came %s later, not twice the gap", d)
	}
}
