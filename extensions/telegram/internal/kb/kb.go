// Package kb is the part of Persona's knowledge base types (server/internal/kb) the connector protocol carries: the
// chats and messages a connector archives, a sender's handles, and the source interfaces the connector package names.
// Copied as they are (the same JSON): the hub files them.
package kb

import (
	"context"
	"time"
)

// SourceChat is a chat as a source lists it, newest activity first.
type SourceChat struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // dm | group | bot
	Topic     bool      `json:"topic,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// SourceMessage is one message as a source returns it.
type SourceMessage struct {
	ID         string    `json:"id"`
	Position   string    `json:"position,omitempty"`
	SenderID   string    `json:"sender_id,omitempty"`
	SenderName string    `json:"sender_name,omitempty"`
	Mine       bool      `json:"mine,omitempty"`
	Type       string    `json:"type,omitempty"` // TEXT, POST, IMAGE, FILE, CARD, SYSTEM…
	Text       string    `json:"text,omitempty"`
	At         time.Time `json:"at,omitempty"`
	Media      []string  `json:"media,omitempty"` // source paths of images / files, for Download
	// SenderHandles are hints of who the sender is (their address, number, login), raw and in memory only: the
	// hub keeps them hashed.
	SenderHandles []Handle `json:"sender_handles,omitempty"`
}

// Handle kinds.
const (
	HandleEmail    = "email"
	HandlePhone    = "phone"
	HandleGitHub   = "github"
	HandleTelegram = "telegram"
	HandleLark     = "lark"
)

// Handle is a raw hint as a connector gives it, in memory only: hashed before the hub writes it.
type Handle struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Source is where chats come from.
type Source interface {
	Name() string
	Chats(ctx context.Context, limit int) ([]SourceChat, error)
	Messages(ctx context.Context, chat SourceChat, after string, limit int) (msgs []SourceMessage, newer string, more bool, err error)
	Download(ctx context.Context, media, dst string) error
}

// Backfiller is a source that can read a chat backwards.
type Backfiller interface {
	Older(ctx context.Context, chat SourceChat, before string, limit int) (msgs []SourceMessage, older string, more bool, err error)
}

// Item is a document, meeting or calendar event the owner came across (the hub's fields; Telegram has none).
type Item struct {
	Kind     string    `json:"kind"`
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	URL      string    `json:"url,omitempty"`
	At       time.Time `json:"at,omitempty"`
	Where    string    `json:"where,omitempty"`
	Projects []string  `json:"projects,omitempty"`
	Note     string    `json:"note,omitempty"`
	Seen     time.Time `json:"seen"`
}

// ItemSource lists documents, meetings and calendar events.
type ItemSource interface {
	Items(ctx context.Context) ([]Item, error)
	DocTitle(ctx context.Context, url string) string
}
