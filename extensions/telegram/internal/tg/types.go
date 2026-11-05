package telegram

import (
	"fmt"
	"time"
)

// peer is a conversation as Telegram addresses it: a user, a basic group or a channel (a supergroup is a channel),
// with the access hash a request needs.
type peer struct {
	Kind byte // 'u' user, 'g' basic group, 'c' channel / supergroup
	ID   int64
	Hash int64
}

// key is its id in the knowledge base (chats/telegram-<key>) and in the cursor: no username (it changes), no phone.
func (p peer) key() string { return fmt.Sprintf("%c%d", p.Kind, p.ID) }

// dialog is one conversation in the account's list.
type dialog struct {
	Peer     peer
	Name     string
	Username string
	Kind     string // dm | group (kb.SourceChat.Kind)
	Top      int    // the newest message's id
	TopAt    time.Time
	TopOut   bool   // the newest message is mine
	TopText  string // the newest message, in a line
	TopFrom  string
	Unread   int // messages after ReadMax
	Mentions int // unread messages that @ me
	ReadMax  int // the newest message read in this account
	Muted    bool
	Archived bool  // in the archive folder
	Photo    int64 // the chat's (person's, group's) picture, 0 without one
	PhotoDC  int   // the data center Photo is on
	Members  int   // a group's members, a channel's subscribers, when Telegram said with the chat (0: not said)
	// What the archive leaves out: a bot, the account's own chat (Saved Messages), a broadcast channel.
	Bot, Self, Broadcast bool
}

// kept says whether the conversation goes into the knowledge base: private chats with people and groups
// (the connector's archive-selection contract).
func (d dialog) kept() bool { return !d.Bot && !d.Self && !d.Broadcast }

// message is one message, already in words.
type message struct {
	ID        int
	At        time.Time
	FromKey   string // the sender's peer key ("u123"); "" for the group itself
	From      string
	Out       bool // mine
	Mentioned bool // it @ me (or replies to me)
	Type      string
	Text      string // with a reply's, a forward's and a media's words
	ReplyTo   int    // the message it replies to
}

// chatInfo is a conversation's profile, beyond what the list has.
type chatInfo struct {
	About   string // the bio of a person, the description of a group
	Members int    // how many members a group has; 0 when Telegram does not say
}

// member is one of a group's members.
type member struct {
	Key      string // the peer key ("u123")
	Name     string
	Username string
	Bot      bool
}

// pageMessage is a message as the app's conversation page draws it (cli.go's thread): its words without the media's
// placeholder, who wrote it with their picture, and its media with where to fetch them.
type pageMessage struct {
	message
	Caption   string // the words: a forward's origin and the caption, or the placeholder of what has no media of its own
	FromPeer  peer   // the sender, with the access hash its picture needs
	FromPhoto int64  // the sender's picture, 0 without one
	FromDC    int    // the data center FromPhoto is on
	Edited    bool
	Event     bool // a group event: a line of its own
	Media     *pageMedia
}

// pageMedia is a message's media: what it is, and the pictures that can be fetched (Full: the media itself, a photo
// or a static sticker; Thumb: a smaller picture of it, a video's or an animated sticker's).
type pageMedia struct {
	Kind          string // image | sticker | video | voice | file
	Mime, Name    string
	Size          int64
	Duration      int
	Width, Height int
	Emoji         string
	Full, Thumb   *fileLoc
}
