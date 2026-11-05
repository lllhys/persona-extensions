package connector

import (
	"context"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/kb"
)

// ChatBatch is one conversation's new messages, oldest first.
type ChatBatch struct {
	// Source is the name the conversation is filed under: "" is the connector's id, else one of its manifest's
	// ArchiveSources (the chat is "<source>-<chat id>").
	Source   string             `json:"source,omitempty"`
	Chat     kb.SourceChat      `json:"chat,omitempty"`
	Messages []kb.SourceMessage `json:"messages,omitempty"`
}

// Archive is a connector whose conversations go into the knowledge base by one cursor for the whole account
// (GitHub's updated_at, mail's UID, iMessage's ROWID) rather than one per conversation: Changes gives what moved
// since cursor ("" the first time) and the cursor to go on from; more says another call has more now. The hub
// keeps the cursor (cursors/<id>.json in the data dir) and ingests every batch as a chat "<id>-<chat id>".
type Archive interface {
	Changes(ctx context.Context, cursor string, limit int) (batches []ChatBatch, next string, more bool, err error)
}

// ArchiveMedia is an Archive whose messages' Media (refs the connector made) can be fetched, so the knowledge base
// keeps a conversation's pictures and files with it as it does for the hub's own sources. Optional: without it the
// lines say [附件] and nothing is kept. At most ArchiveMediaMost bytes.
type ArchiveMedia interface {
	ArchiveMedia(ctx context.Context, ref string) (Media, error)
}

// ArchiveMediaMost is the largest attachment archive.media hands over.
const ArchiveMediaMost = 20 << 20
