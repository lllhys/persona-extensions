package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
)

// threadAccount is archiveAccount's with pictures: people's avatars, the group's, and a photo in the group.
func threadAccount(t *testing.T) (*Conn, *fakeTelegram, context.Context) {
	t.Helper()
	c, f, ctx := archiveAccount(t)
	f.mu.Lock()
	f.dialogs[0].Photo, f.dialogs[0].PhotoDC = 900, 2
	f.photos, f.photoDCs = map[string]int64{"u1": 501, "u7": 507}, map[string]int{"u1": 4, "u7": 1}
	f.media = map[string]*pageMedia{"c2/12": {Kind: "image", Mime: "image/jpeg", Width: 800, Height: 600, Full: &fileLoc{key: "photo 12", mime: "image/jpeg", dc: 3}}}
	f.files = map[string][]byte{"photo 12": []byte("made-up jpeg"), "avatar 501": []byte("made-up avatar 501"), "avatar 507": []byte("made-up avatar 507"),
		"avatar 900": []byte("made-up group picture")}
	f.history["u1"][2].ReplyTo = 1 // my reply to the oldest message, which a short page does not have
	f.mu.Unlock()
	c.refreshInbox(ctx) // the conversations are listed, as the inbox has them
	return c, f, ctx
}

func requests(f *fakeTelegram, method string) int {
	n := 0
	for _, r := range f.requests() {
		if r.method == method {
			n++
		}
	}
	return n
}

// A conversation's page: its messages oldest first, senders with their pictures, mine as mine, a photo with where to
// fetch it, a reply with what it answers (from the page itself).
func TestThreadPage(t *testing.T) {
	c, _, ctx := threadAccount(t)
	page, err := c.Thread(ctx, "c2", "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if ch := page.Chat; ch.ID != "c2" || ch.Name != "示例群" || ch.Kind != "group" || ch.Avatar != "a.c2.900" || ch.Reply != "telegram_send" {
		t.Fatalf("chat %+v", ch)
	}
	if len(page.Messages) != 2 || page.Before != "" {
		t.Fatalf("%d messages, before %q", len(page.Messages), page.Before)
	}
	first, photo := page.Messages[0], page.Messages[1]
	if first.ID != "11" || first.Text != "群里的示例消息" || first.Sender.Name != "示例用户" || first.Sender.Avatar != "a.u1.501" || first.Mine {
		t.Fatalf("first %+v", first)
	}
	if photo.Text != "" || len(photo.Media) != 1 || photo.Media[0].Kind != "image" || photo.Media[0].Ref != "m.c2.12.f" || photo.Media[0].Width != 800 ||
		photo.Sender.Avatar != "a.u7.507" {
		t.Fatalf("photo %+v", photo)
	}
	if q := photo.ReplyTo; q == nil || q.ID != "11" || q.From != "示例用户" || q.Text != "群里的示例消息" {
		t.Fatalf("quote %+v", photo.ReplyTo)
	}
}

// Pages go back from the newest; what a page's messages reply to that it does not have is read in one request.
func TestThreadPages(t *testing.T) {
	c, f, ctx := threadAccount(t)
	before := requests(f, "Messages")
	page, err := c.Thread(ctx, "u1", "", 2)
	if err != nil || len(page.Messages) != 2 || page.Before != "2" {
		t.Fatalf("newest page: %+v %v", page, err)
	}
	mine := page.Messages[1]
	if !mine.Mine || mine.Sender.Name != "我" || mine.Sender.Avatar != "" || mine.ReplyTo == nil || mine.ReplyTo.Text != "四十天前的示例消息" {
		t.Fatalf("mine %+v %+v", mine, mine.ReplyTo)
	}
	if n := requests(f, "Messages") - before; n != 1 {
		t.Fatalf("%d requests for the replies' messages", n)
	}
	page, err = c.Thread(ctx, "u1", page.Before, 2)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "1" || page.Before != "" {
		t.Fatalf("the oldest page: %+v %v", page, err)
	}
	if _, err := c.Thread(ctx, "u1", "abc", 2); err == nil {
		t.Fatal("a cursor that is no message id")
	}
	if _, err := c.Thread(ctx, "没有这个会话", "", 2); err == nil || !strings.Contains(err.Error(), "没有叫") {
		t.Fatalf("unknown chat: %v", err)
	}
}

// A picture comes from where its page said, or (the page gone: a restart) from the message read again; an avatar
// from its person; a ref the connector never made asks Telegram nothing.
func TestThreadMedia(t *testing.T) {
	c, f, ctx := threadAccount(t)
	if _, err := c.Thread(ctx, "c2", "", 30); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]string{"m.c2.12.f": "made-up jpeg", "a.u7.507": "made-up avatar 507", "a.c2.900": "made-up group picture"} {
		m, err := c.Media(ctx, ref)
		if err != nil || string(m.Data) != want || m.Mime != "image/jpeg" {
			t.Fatalf("%s: %q %q %v", ref, m.Data, m.Mime, err)
		}
	}
	if requests(f, "Locate") != 0 {
		t.Fatal("located a picture its page had")
	}
	f.mu.Lock()
	where := fmt.Sprint(f.fetched)
	f.mu.Unlock()
	if where != "map[avatar 507:1 avatar 900:2 photo 12:3]" { // a sender's and the group's from the page, a photo's its own
		t.Fatalf("asked on %s", where)
	}
	c.box.mu.Lock()
	c.box.locs = nil
	c.box.mu.Unlock()
	if m, err := c.Media(ctx, "m.c2.12.f"); err != nil || string(m.Data) != "made-up jpeg" || requests(f, "Locate") != 1 {
		t.Fatalf("after a restart: %q %v, %d locate(s)", m.Data, err, requests(f, "Locate"))
	}
	sent := len(f.requests())
	for _, ref := range []string{"", "x", "m.c2.12.z", "a.u9.1", "m.c2.abc.f"} {
		if _, err := c.Media(ctx, ref); err == nil {
			t.Errorf("%q fetched", ref)
		}
	}
	if len(f.requests()) != sent {
		t.Fatalf("refs it never made asked Telegram %d time(s)", len(f.requests())-sent)
	}
	if _, err := c.Media(ctx, "m.c2.11.f"); err == nil { // a message without a picture
		t.Fatal("a message without a picture gave one")
	}
}

// During a flood wait the page and its pictures send nothing and say how long to wait.
func TestThreadDuringAFloodWait(t *testing.T) {
	c, f, ctx := threadAccount(t)
	if _, err := c.Thread(ctx, "c2", "", 30); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail = func(method string) error {
		if method == "Fetch" {
			return tgerr.New(420, "FLOOD_WAIT_30")
		}
		return nil
	}
	f.mu.Unlock()
	if _, err := c.Media(ctx, "a.u7.507"); err == nil {
		t.Fatal("no flood wait")
	}
	sent := len(f.requests())
	if _, err := c.Thread(ctx, "c2", "", 30); err == nil || !strings.Contains(err.Error(), "Telegram 限流中") {
		t.Fatalf("page: %v", err)
	}
	if _, err := c.Media(ctx, "m.c2.12.f"); err == nil || !strings.Contains(err.Error(), "Telegram 限流中") {
		t.Fatalf("media: %v", err)
	}
	if len(f.requests()) != sent {
		t.Fatalf("%d request(s) during the wait", len(f.requests())-sent)
	}
	if st := c.Status(ctx); st.State != connector.StateRateLimited {
		t.Fatalf("status %+v", st)
	}
}

// A group's first page says how many are in it: as its chat said in the dialogs (no request), else Info's answer,
// asked once in membersFor, a failure too. Older pages and private chats do not ask.
func TestThreadMembers(t *testing.T) {
	c, f, ctx := threadAccount(t)
	f.mu.Lock()
	f.infos = map[string]chatInfo{"c2": {Members: 128}}
	f.mu.Unlock()
	for range 2 {
		if page, err := c.Thread(ctx, "c2", "", 30); err != nil || page.Chat.Members != 128 {
			t.Fatalf("members %+v %v", page.Chat, err)
		}
	}
	if n := requests(f, "Info"); n != 1 {
		t.Fatalf("asked %d times", n)
	}
	if page, err := c.Thread(ctx, "c2", "12", 30); err != nil || page.Chat.Members != 0 || requests(f, "Info") != 1 {
		t.Fatalf("an older page: %+v %v, asked %d times", page.Chat, err, requests(f, "Info"))
	}
	if page, err := c.Thread(ctx, "u1", "", 30); err != nil || page.Chat.Members != 0 || requests(f, "Info") != 1 {
		t.Fatalf("a private chat: %+v %v, asked %d times", page.Chat, err, requests(f, "Info"))
	}

	// After membersFor it asks again; a failure counts as asked.
	c.box.mu.Lock()
	c.box.members["c2"] = headcount{n: 128, at: time.Now().Add(-membersFor - time.Minute)}
	c.box.mu.Unlock()
	f.mu.Lock()
	f.fail = func(method string) error {
		if method == "Info" {
			return tgerr.New(400, "CHANNEL_PRIVATE")
		}
		return nil
	}
	f.mu.Unlock()
	for range 2 {
		if page, err := c.Thread(ctx, "c2", "", 30); err != nil || page.Chat.Members != 0 {
			t.Fatalf("a failed ask: %+v %v", page.Chat, err)
		}
	}
	if n := requests(f, "Info"); n != 2 {
		t.Fatalf("asked %d times after a failure", n)
	}

	// Said with the chat: no ask at all.
	c, f, ctx = threadAccount(t)
	f.mu.Lock()
	f.dialogs[0].Members = 42
	f.mu.Unlock()
	c.refreshInbox(ctx)
	if page, err := c.Thread(ctx, "c2", "", 30); err != nil || page.Chat.Members != 42 || requests(f, "Info") != 0 {
		t.Fatalf("from the dialogs: %+v %v, asked %d times", page.Chat, err, requests(f, "Info"))
	}
}
