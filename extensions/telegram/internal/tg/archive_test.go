package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
)

// archiveAccount is a signed-in made-up account: a private chat, a supergroup, and three conversations the
// archive leaves out (a bot, Saved Messages, a broadcast channel).
func archiveAccount(t *testing.T) (*Conn, *fakeTelegram, context.Context) {
	t.Helper()
	newsSettle = 10 * time.Millisecond
	t.Cleanup(func() { newsSettle = 3 * time.Second })
	now := time.Now()
	f := &fakeTelegram{
		dialogs: []dialog{
			{Peer: peer{Kind: 'c', ID: 2, Hash: 22}, Name: "示例群", Kind: "group", Top: 12, TopAt: now},
			{Peer: peer{Kind: 'u', ID: 1, Hash: 11}, Name: "示例用户", Kind: "dm", Top: 3, TopAt: now.Add(-time.Hour)},
			{Peer: peer{Kind: 'u', ID: 3}, Name: "示例机器人", Kind: "dm", Top: 9, Bot: true},
			{Peer: peer{Kind: 'u', ID: 4}, Name: "我", Kind: "dm", Top: 9, Self: true},
			{Peer: peer{Kind: 'c', ID: 5}, Name: "示例频道", Kind: "group", Top: 9, Broadcast: true},
		},
		history: map[string][]message{
			"u1": {
				{ID: 1, At: now.Add(-40 * 24 * time.Hour), FromKey: "u1", From: "示例用户", Type: "TEXT", Text: "四十天前的示例消息"},
				{ID: 2, At: now.Add(-2 * time.Hour), FromKey: "u1", From: "示例用户", Type: "TEXT", Text: "示例消息一"},
				{ID: 3, At: now.Add(-time.Hour), Out: true, Type: "TEXT", Text: "示例回复"},
			},
			"c2": {
				{ID: 11, At: now.Add(-time.Minute), FromKey: "u1", From: "示例用户", Type: "TEXT", Text: "群里的示例消息"},
				{ID: 12, At: now, FromKey: "u7", From: "另一个示例用户", Type: "IMAGE", Text: "[图片]", ReplyTo: 11},
			},
		},
	}
	raw, _ := json.Marshal(stored{APIID: 1234567, APIHash: exampleHash, Session: []byte("example session")})
	c, ctx := running(t, &memSecrets{data: raw}, f)
	waitState(t, c, connector.StateOK)
	return c, f, ctx
}

// The first read: private chats and groups only, 30 days back; a reply says what it answers.
func TestArchiveFirstRead(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	batches, next, more, err := c.Changes(ctx, "", 20)
	if err != nil || more {
		t.Fatalf("%v more=%v", err, more)
	}
	var ids []string
	for _, b := range batches {
		ids = append(ids, b.Chat.ID)
	}
	if !slices.Equal(ids, []string{"c2", "u1"}) {
		t.Fatalf("chats %v (a bot, Saved Messages or a channel got in?)", ids)
	}
	u1 := batches[1].Messages
	if len(u1) != 2 || u1[0].Text != "示例消息一" || !u1[1].Mine {
		t.Fatalf("u1 %+v (older than 30 days kept?)", u1)
	}
	if g := batches[0].Messages[1].Text; g != "回复「群里的示例消息」：[图片]" {
		t.Fatalf("reply %q", g)
	}
	if batches[0].Chat.Kind != "group" || batches[1].Chat.Kind != "dm" || batches[0].Messages[0].SenderID != "u1" {
		t.Fatalf("chats %+v", batches)
	}
	var cur archiveCursor
	_ = json.Unmarshal([]byte(next), &cur)
	if cur.Seen["c2"] != 12 || cur.Seen["u1"] != 3 || len(cur.Seen) != 2 {
		t.Fatalf("cursor %s", next)
	}

	// Nothing new: no history is read at all.
	reads := len(f.reads)
	if batches, _, _, err := c.Changes(ctx, next, 20); err != nil || len(batches) != 0 || len(f.reads) != reads {
		t.Fatalf("again: %d batches, %d reads, %v", len(batches), len(f.reads)-reads, err)
	}

	// A new message in the group: only it, read after the cursor.
	f.mu.Lock()
	f.history["c2"] = append(f.history["c2"], message{ID: 13, At: time.Now(), FromKey: "u1", From: "示例用户", Type: "TEXT", Text: "新的示例消息"})
	f.dialogs[0].Top = 13
	f.mu.Unlock()
	batches, _, _, err = c.Changes(ctx, next, 20)
	if err != nil || len(batches) != 1 || len(batches[0].Messages) != 1 || batches[0].Messages[0].ID != "13" || f.reads[len(f.reads)-1] != "c2 after 12" {
		t.Fatalf("new: %+v %v %v", batches, f.reads, err)
	}
}

// A round takes at most limit conversations and says there is more; a first read takes at most firstMax.
func TestArchiveLimits(t *testing.T) {
	c, _, ctx := archiveAccount(t)
	batches, next, more, err := c.Changes(ctx, "", 1)
	if err != nil || len(batches) != 1 || !more {
		t.Fatalf("%d batches, more=%v, %v", len(batches), more, err)
	}
	if batches, _, more, _ = c.Changes(ctx, next, 1); len(batches) != 1 || batches[0].Chat.ID != "u1" || more {
		t.Fatalf("the rest %+v more=%v", batches, more)
	}
	firstMax = 1
	t.Cleanup(func() { firstMax = 500 })
	batches, _, _, _ = c.Changes(ctx, "", 20)
	if len(batches[0].Messages) != 1 || batches[0].Messages[0].ID != "12" {
		t.Fatalf("first read past firstMax: %+v", batches[0].Messages)
	}
}

// Telegram's flood wait ends the round that met it (what was read before it is kept) and holds everything until it is
// over: the archive, the inbox and the tools send nothing, and the status says when it came, what Telegram asked for
// and how long is left. Once it is over, the next round goes on from where the last one stopped.
func TestArchiveFloodWait(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	c.zone = time.FixedZone("UTC+8", 8*3600)
	histories := 0
	f.mu.Lock()
	f.fail = func(method string) error {
		if method == "History" {
			if histories++; histories == 2 {
				return tgerr.New(420, "FLOOD_WAIT_30")
			}
		}
		return nil
	}
	f.mu.Unlock()
	batches, cursor, more, err := c.Changes(ctx, "", 20)
	if err != nil || len(batches) != 1 || batches[0].Chat.ID != "c2" || more {
		t.Fatalf("the round that met the wait: %d batches, more %v, %v", len(batches), more, err)
	}
	st := c.Status(ctx)
	if st.State != connector.StateRateLimited || !strings.Contains(st.Error, "要求等 30 秒") || !strings.Contains(st.Error, "还要 3") ||
		!strings.Contains(st.Error, "恢复") || strings.Contains(st.Error, "UTC") {
		t.Fatalf("status %+v", st)
	}
	sent := len(f.requests())
	if _, _, _, err := c.Changes(ctx, cursor, 20); err == nil || !strings.Contains(err.Error(), "限流中") {
		t.Fatalf("archive during the wait: %v", err)
	}
	if _, err := c.Call(ctx, "telegram_chats", nil); err == nil || !strings.Contains(err.Error(), "Telegram 限流中") {
		t.Fatalf("tool during the wait: %v", err)
	}
	c.refreshInbox(ctx)
	if p, err := c.LoginStep(ctx, "", nil); err != nil || !p.Done {
		t.Fatalf("still signed in during the wait: %+v %v", p, err)
	}
	if got := f.requests()[sent:]; len(got) != 0 {
		t.Fatalf("%d request(s) during the wait: %+v", len(got), got)
	}

	c.gate.mu.Lock()
	c.gate.until = time.Now().Add(-time.Millisecond)
	c.gate.mu.Unlock()
	if st := c.Status(ctx); st.State != connector.StateOK || st.Error != "" {
		t.Fatalf("after the wait %+v", st)
	}
	batches, _, _, err = c.Changes(ctx, cursor, 20)
	if err != nil || len(batches) != 1 || batches[0].Chat.ID != "u1" {
		t.Fatalf("after the wait: %d batches %v", len(batches), err)
	}
}

// What Telegram pushes kicks the archive and the inbox once the burst settles.
func TestWatchKicks(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	select { // connecting asked for a catch-up kick already
	case <-c.news:
	default:
	}
	got := make(chan string, 8)
	watch(t, ctx, c, func(scope connector.Scope) { got <- string(scope) })
	f.mu.Lock()
	updated := f.updated
	f.mu.Unlock()
	for i := 0; i < 5; i++ {
		updated()
	}
	var scopes []string
	for len(scopes) < 2 {
		select {
		case s := <-got:
			scopes = append(scopes, s)
		case <-time.After(5 * time.Second):
			t.Fatalf("kicks %v", scopes)
		}
	}
	slices.Sort(scopes)
	if !slices.Equal(scopes, []string{string(connector.ScopeChats), string(connector.ScopeInbox)}) {
		t.Fatalf("kicks %v", scopes)
	}
	select {
	case s := <-got:
		t.Fatalf("a burst kicked more than once: %s", s)
	case <-time.After(100 * time.Millisecond):
	}
}

// offlineGotd is the real gotdClient on a gotd client whose every request is answered locally by answer (a
// middleware that never calls the network).
func offlineGotd(answer func(input bin.Encoder, output bin.Decoder) error) *gotdClient {
	cl := telegram.NewClient(1, "example", telegram.Options{Middlewares: []telegram.Middleware{
		telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc {
			return func(_ context.Context, input bin.Encoder, output bin.Decoder) error { return answer(input, output) }
		})}})
	return &gotdClient{c: cl, done: make(chan struct{})}
}

// A busy group gets more than firstMax messages between two rounds (the hub was off for a day): a round archives the
// oldest firstMax after the cursor and moves it only that far, says there is more, and the next round goes on from
// there. Nothing in between is skipped (#78).
func TestArchiveCatchUp(t *testing.T) {
	const seen, top = 1000, 1700 // 700 new messages since the last round
	now := time.Now()
	chat := &tg.Chat{ID: 9, Title: "示例群", ParticipantsCount: 3}
	user := &tg.User{ID: 7, AccessHash: 77, FirstName: "示例"}
	msg := func(id int) tg.MessageClass {
		return &tg.Message{ID: id, PeerID: &tg.PeerChat{ChatID: 9}, FromID: &tg.PeerUser{UserID: 7},
			Date: int(now.Add(-time.Duration(top-id) * time.Second).Unix()), Message: fmt.Sprintf("示例消息 %d", id)}
	}
	gc := offlineGotd(func(input bin.Encoder, output bin.Decoder) error {
		switch r := input.(type) {
		case *tg.MessagesGetDialogsRequest:
			res := &tg.MessagesDialogs{Users: []tg.UserClass{user}, Chats: []tg.ChatClass{chat}}
			if r.FolderID == 0 {
				res.Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChat{ChatID: 9}, TopMessage: top}}
				res.Messages = []tg.MessageClass{msg(top)}
			}
			output.(*tg.MessagesDialogsBox).Dialogs = res
		case *tg.MessagesGetHistoryRequest: // like Telegram: newest first, above MinID, Limit from below OffsetID moved by AddOffset
			var all []int
			for id := top; id > r.MinID; id-- {
				all = append(all, id)
			}
			from := 0
			if r.OffsetID != 0 {
				for from < len(all) && all[from] >= r.OffsetID {
					from++
				}
			}
			from = min(max(from+r.AddOffset, 0), len(all))
			var ms []tg.MessageClass
			for _, id := range all[from:min(from+r.Limit, len(all))] {
				ms = append(ms, msg(id))
			}
			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{Count: len(all), Messages: ms,
				Users: []tg.UserClass{user}, Chats: []tg.ChatClass{chat}}
		default:
			return fmt.Errorf("unexpected %T", input)
		}
		return nil
	})
	c := newConn(&memSecrets{}, nil)
	c.cl = gc
	got := map[int]bool{}
	cursor := fmt.Sprintf(`{"seen":{"g9":%d}}`, seen)
	for round, want := range []struct {
		first, last int
		more        bool
	}{{1001, 1500, true}, {1501, 1700, false}} {
		batches, next, more, err := c.Changes(context.Background(), cursor, 20)
		if err != nil || len(batches) != 1 {
			t.Fatalf("round %d: %d batches, %v", round, len(batches), err)
		}
		ms := batches[0].Messages
		if ms[0].ID != strconv.Itoa(want.first) || ms[len(ms)-1].ID != strconv.Itoa(want.last) || len(ms) != want.last-want.first+1 {
			t.Fatalf("round %d: %d messages %s..%s, want %d..%d", round, len(ms), ms[0].ID, ms[len(ms)-1].ID, want.first, want.last)
		}
		if more != want.more {
			t.Fatalf("round %d: more=%v", round, more)
		}
		for _, m := range ms {
			id, _ := strconv.Atoi(m.ID)
			got[id] = true
		}
		var cur archiveCursor
		_ = json.Unmarshal([]byte(next), &cur)
		if cur.Seen["g9"] != want.last {
			t.Fatalf("round %d: cursor %s, want %d", round, next, want.last)
		}
		cursor = next
	}
	if len(got) != top-seen {
		t.Fatalf("archived %d of %d new messages", len(got), top-seen)
	}
}
