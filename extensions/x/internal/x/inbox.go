package x

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
)

// The inbox: one thing per mention (a post that mentions the user or replies to them), the conversation as its
// thread, always "needs you" by the connector's own rule (the fallback tier, so no model has to sort it). A mention is
// done once the user posted in that conversation after it (their own posts, read while a mention is open). The
// timeline never comes into the inbox: only what concerns the user.

// Collect answers from memory, newest first.
func (c *Connector) Collect(context.Context) ([]connector.InboxThing, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.keys.complete() {
		return []connector.InboxThing{}, nil
	}
	things := []connector.InboxThing{}
	for _, m := range sortedPosts(c.mentions) {
		things = append(things, c.stamped(c.thingLocked(m)))
	}
	return things, nil
}

func (c *Connector) thingLocked(m Post) connector.InboxThing {
	sender := "@" + m.Username
	if m.Username == "" {
		sender = m.AuthorID
	}
	thread := m.ConversationID
	if thread == "" {
		thread = m.ID
	}
	t := connector.InboxThing{
		Thread: thread, Kind: "mention", Label: c.tx.mentionLabel, Title: fmt.Sprintf(c.tx.mentionTitle, sender), Sender: sender,
		Preview: clip(oneLine(m.Text), 200), Context: untrustedNote + "\n\n" + sender + " (" + m.Name + "):\n" + clip(m.Text, 8000),
		At: m.CreatedAt, Last: m.ID, URL: m.Permalink(), Tier: connector.TierAction, Why: c.tx.mentionWhy,
		Tags: []connector.Tag{{Key: connector.TagSender, Value: m.AuthorID, Name: sender}},
	}
	if c.answeredLocked(m) {
		t.Done = connector.DoneReplied
	}
	return t
}

// stamped is t with the user's rules and the fallback tier (the rules' tier; fyi when they leave it to the model).
func (c *Connector) stamped(t connector.InboxThing) connector.InboxThing {
	t.Rules = c.rules
	tier := t.Tier
	if !tier.Known() {
		tier = connector.TierFYI
	}
	extra := connector.ThingExtra{}
	if t.Extra != nil {
		extra = *t.Extra
	}
	extra.RuleTier = string(tier)
	t.Extra = &extra
	return t
}

// errNoReplies: the connector does not write to X (its things have no can_reply).
var errNoReplies = errors.New("X: replying from Persona is not supported; open the post on X")

// Reply is refused: the connector only reads.
func (c *Connector) Reply(context.Context, connector.InboxReply) error { return errNoReplies }

// oneLine is s with its line breaks as spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
