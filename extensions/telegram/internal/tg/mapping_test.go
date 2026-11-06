package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

// Telegram's objects, made up: two people (one a bot), a supergroup, a broadcast channel, a basic group.
func exampleEntities() entities {
	group := &tg.Channel{ID: 2, AccessHash: 22, Title: "示例群", Megagroup: true}
	group.SetParticipantsCount(128) // said only sometimes: a flag
	return entitiesOf(
		[]tg.UserClass{
			&tg.User{ID: 1, AccessHash: 11, FirstName: "示例", LastName: "用户", Username: "example", Photo: &tg.UserProfilePhoto{PhotoID: 501, DCID: 4}},
			&tg.User{ID: 3, AccessHash: 33, FirstName: "示例机器人", Bot: true},
		},
		[]tg.ChatClass{
			group,
			&tg.Channel{ID: 5, AccessHash: 55, Title: "示例频道", Broadcast: true},
			&tg.Chat{ID: 6, Title: "示例小群", ParticipantsCount: 5},
		})
}

func TestDialogsOf(t *testing.T) {
	e := exampleEntities()
	now := time.Unix(1_800_000_000, 0)
	ds := []tg.DialogClass{
		&tg.Dialog{Peer: &tg.PeerUser{UserID: 1}, TopMessage: 10, UnreadCount: 2, ReadInboxMaxID: 8},
		&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 2}, TopMessage: 20, UnreadMentionsCount: 1,
			NotifySettings: tg.PeerNotifySettings{MuteUntil: int(now.Unix()) + 3600}},
		&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 5}, TopMessage: 30},
		&tg.Dialog{Peer: &tg.PeerChat{ChatID: 6}, TopMessage: 40, FolderID: 1},
		&tg.Dialog{Peer: &tg.PeerUser{UserID: 3}, TopMessage: 50},
	}
	msgs := []tg.MessageClass{
		&tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 1}, FromID: &tg.PeerUser{UserID: 1}, Date: int(now.Unix()), Message: "示例消息"},
		&tg.Message{ID: 20, PeerID: &tg.PeerChannel{ChannelID: 2}, Out: true, Date: int(now.Unix()), Message: "我的示例消息"},
	}
	got := dialogsOf(ds, msgs, e, false, now)
	if len(got) != 5 {
		t.Fatalf("%d dialogs", len(got))
	}
	dm, group, channel, small, bot := got[0], got[1], got[2], got[3], got[4]
	if dm.Kind != "dm" || dm.Peer != (peer{'u', 1, 11}) || dm.Name != "示例 用户" || dm.Unread != 2 || dm.ReadMax != 8 ||
		dm.TopText != "示例消息" || dm.TopFrom != "示例 用户" || dm.Username != "example" || !dm.kept() || dm.Photo != 501 || dm.PhotoDC != 4 {
		t.Fatalf("dm %+v", dm)
	}
	if group.Kind != "group" || group.Peer != (peer{'c', 2, 22}) || !group.Muted || group.Mentions != 1 || !group.TopOut || group.Broadcast || !group.kept() ||
		group.Members != 128 {
		t.Fatalf("supergroup %+v", group)
	}
	if !channel.Broadcast || channel.kept() {
		t.Fatalf("a broadcast channel is kept: %+v", channel)
	}
	if small.Peer.Kind != 'g' || !small.Archived || small.Name != "示例小群" || small.Members != 5 || channel.Members != 0 || dm.Members != 0 {
		t.Fatalf("basic group %+v", small)
	}
	if !bot.Bot || bot.kept() {
		t.Fatalf("a bot is kept: %+v", bot)
	}
}

func TestMessageOf(t *testing.T) {
	e := exampleEntities()
	at := time.Unix(1_800_000_000, 0)
	for _, c := range []struct {
		name, text, kind string
		m                tg.MessageClass
	}{
		{"a forwarded photo", "转发自 示例来源： 示例消息 [图片]", "IMAGE", &tg.Message{ID: 1, Date: int(at.Unix()), Message: "示例消息",
			FwdFrom: tg.MessageFwdHeader{FromName: "示例来源"}, Media: &tg.MessageMediaPhoto{}}},
		{"a file", "[文件 example.pdf · 2.0 MB]", "FILE", &tg.Message{ID: 2, Date: int(at.Unix()),
			Media: &tg.MessageMediaDocument{Document: &tg.Document{Size: 2 << 20, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "example.pdf"}}}}}},
		{"a voice message", "[语音]", "AUDIO", &tg.Message{ID: 3, Date: int(at.Unix()),
			Media: &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true}}}}}},
		{"a sticker", "[贴纸]", "STICKER", &tg.Message{ID: 4, Date: int(at.Unix()),
			Media: &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{}}}}}},
		{"a link preview says nothing", "https://example.com", "TEXT", &tg.Message{ID: 5, Date: int(at.Unix()), Message: "https://example.com",
			Media: &tg.MessageMediaWebPage{}}},
		{"a group's new name", "[群名改成「新的示例群」]", "SYSTEM", &tg.MessageService{ID: 6, Date: int(at.Unix()),
			Action: &tg.MessageActionChatEditTitle{Title: "新的示例群"}}},
		{"someone joins", "[示例 用户 进群]", "SYSTEM", &tg.MessageService{ID: 7, Date: int(at.Unix()),
			Action: &tg.MessageActionChatAddUser{Users: []int64{1}}}},
	} {
		m := messageOf(c.m, e)
		if m.Text != c.text || m.Type != c.kind || !m.At.Equal(at) {
			t.Errorf("%s: %q %s %v", c.name, m.Text, m.Type, m.At)
		}
	}
	reply := messageOf(&tg.Message{ID: 8, Date: int(at.Unix()), Message: "示例回复", Mentioned: true, FromID: &tg.PeerUser{UserID: 1},
		PeerID: &tg.PeerChannel{ChannelID: 2}, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 7}}, e)
	if reply.ReplyTo != 7 || !reply.Mentioned || reply.FromKey != "u1" || reply.From != "示例 用户" {
		t.Fatalf("reply %+v", reply)
	}
	post := messageOf(&tg.Message{ID: 9, Date: int(at.Unix()), Message: "频道的示例消息", PeerID: &tg.PeerChannel{ChannelID: 5}}, e)
	if post.FromKey != "c5" || post.From != "示例频道" {
		t.Fatalf("a post without a sender %+v", post)
	}
}

// A chat found by search keeps its @username, so a write can name it that way after tg search as after tg chats.
func TestSearchKeepsUsernames(t *testing.T) {
	user := &tg.User{ID: 42, AccessHash: 4242, FirstName: "示例", Username: "example_user"}
	group := &tg.Channel{ID: 2, AccessHash: 22, Title: "示例群", Megagroup: true, Username: "example_group"}
	now := int(time.Now().Unix())
	dm := &tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 42}, FromID: &tg.PeerUser{UserID: 42}, Date: now, Message: "示例发布说明"}
	post := &tg.Message{ID: 6, PeerID: &tg.PeerChannel{ChannelID: 2}, FromID: &tg.PeerUser{UserID: 42}, Date: now, Message: "示例发布说明"}
	gc := offlineGotd(func(input bin.Encoder, output bin.Decoder) error {
		switch r := input.(type) {
		case *tg.MessagesGetDialogsRequest:
			res := &tg.MessagesDialogs{Users: []tg.UserClass{user}}
			if r.FolderID == 0 {
				res.Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 42}, TopMessage: 5}}
				res.Messages = []tg.MessageClass{dm}
			}
			output.(*tg.MessagesDialogsBox).Dialogs = res
		case *tg.MessagesSearchGlobalRequest:
			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{dm, post},
				Users: []tg.UserClass{user}, Chats: []tg.ChatClass{group}}
		default:
			return fmt.Errorf("unexpected %T", input)
		}
		return nil
	})
	ctx := context.Background()
	found, err := gc.Search(ctx, "示例", nil, time.Time{}, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Dialog.Username != "example_user" || found[1].Dialog.Username != "example_group" {
		t.Fatalf("found %+v", found)
	}

	c := newConn(&memSecrets{}, nil)
	c.cl = gc
	send := json.RawMessage(`{"chat":"@example_user","text":"你好"}`)
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Describe(ctx, "telegram_send", send); err != nil {
		t.Fatalf("after tg chats: %v", err)
	}
	if _, err := c.Call(ctx, "telegram_search_messages", json.RawMessage(`{"query":"示例"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Describe(ctx, "telegram_send", send); err != nil {
		t.Fatalf("after tg search: %v", err)
	}
	if _, _, err := c.Describe(ctx, "telegram_send", json.RawMessage(`{"chat":"@example_group","text":"你好"}`)); err != nil {
		t.Fatalf("a group found only by tg search: %v", err)
	}
}
