package connector

import (
	"context"
	"encoding/json"
	"time"
)

// Tier is the inbox's tier a connector's rules decide for a thing (InboxThing.Tier).
type Tier string

// Tiers: needs the user, worth knowing, noise.
const (
	TierAction Tier = "action"
	TierFYI    Tier = "fyi"
	TierNoise  Tier = "noise"
)

// Known says whether t is one of the tiers ("" is not: it leaves the tier to the model).
func (t Tier) Known() bool { return t == TierAction || t == TierFYI || t == TierNoise }

// Done reasons of an InboxThing (the inbox item's done_by).
const (
	DoneRead    = "read"    // over without the user: merged, closed, the request withdrawn
	DoneReplied = "replied" // the user answered (here or elsewhere)
)

// InboxThing is one thread a connector thinks may need the user. The hub turns things into inbox items: one open
// item per thread gathers its later moves until the user deals with it; a move after that starts a new item. What
// happens to an item afterwards (triage, the AI alter ego, done, kept for a while) is the hub's, the same for every source.
type InboxThing struct {
	// Thread is the connector's stable key of the thread ("example-org/example-repo#12").
	Thread string `json:"thread"`
	// Kind is the connector's kind of thing ("review_requested"): prompts/inbox/kinds/<id>.md tells the models
	// what it means; Label is the row's tag for people ("Review requested").
	Kind    string `json:"kind"`
	Label   string `json:"label,omitempty"`
	Title   string `json:"title,omitempty"`
	Sender  string `json:"sender,omitempty"`  // who made the newest move
	Preview string `json:"preview,omitempty"` // the newest move in a line
	Context string `json:"context,omitempty"` // the thread as text, for triage, drafting and the AI alter ego
	// At is when the newest move happened; Last marks it, and a different Last adds to the open item.
	At   time.Time `json:"at,omitempty"`
	Last string    `json:"last,omitempty"`
	URL  string    `json:"url,omitempty"` // the thread on the web, opened in the app
	// OpenURL is a link the system opens instead (sms:…: reply in Messages), kept apart from URL.
	OpenURL string `json:"open_url,omitempty"`
	// ReplyTo says where a reply goes and who sees it ("Comment on example-org/example-repo#12 (visible to anyone with repository access)");
	// CanReply: Reply works for this thread.
	ReplyTo  string `json:"reply_to,omitempty"`
	CanReply bool   `json:"can_reply,omitempty"`
	// Tier is the tier the connector's rules decide (action, fyi, noise): the item gets it without a model. "" leaves it
	// to the model (a human's ordinary comment). Why says which rule, for people ("Rule: a person mentioned you").
	Tier Tier   `json:"tier,omitempty"`
	Why  string `json:"why,omitempty"`
	// Rules are the user's own triage rules for this thing, in their words (a source's or an account's): when set
	// the model gets them instead of the hub's inbox.rules; the default criteria stay underneath. "" uses inbox.rules.
	Rules string `json:"rules,omitempty"`
	// Done ends the open item (DoneRead, DoneReplied): the thread is over; Why says how when it is not obvious
	// ("GitHub: subsequently succeeded").
	Done string `json:"done,omitempty"`
	// Read: seen somewhere else (the notification read on GitHub); an item that does not need the user is done.
	Read bool `json:"read,omitempty"`
	// Tags are what the thing is about, for muting and rules: its repository, chat, sender (TagRepo, TagChat,
	// TagSender: the row's menu offers to mute those), a CI failure's branch. The hub mutes by them for every source.
	Tags []Tag `json:"tags,omitempty"`
	// Extra is what an item the hub used to make itself had besides the generic fields (ThingExtra): only a connector
	// taking over one of the hub's own (MigratableConnectors) sends it, and only for it does the hub read it.
	Extra *ThingExtra `json:"extra,omitempty"`
}

// MigratableConnectors are the hub's own connectors a plugin may take over while they move out of the hub (Feishu
// through larkintegration, under the host's connector migration contract): a plugin's connector with the same id replaces the hub's while
// the plugin is on, and its inbox things may carry the items' own blocks (ThingExtra).
var MigratableConnectors = []string{"lark"}

// ThingExtra is an inbox item's own fields that the generic thing has no place for: the item keeps the shape the
// apps know (the Feishu item's lark / doc / moment block, its avatar and count) instead of the generic connector block.
type ThingExtra struct {
	Source      string    `json:"source,omitempty"`
	Account     string    `json:"account,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	LegacyID    string    `json:"legacy_id,omitempty"` // the id the hub would have given the item itself
	Avatar      string    `json:"avatar,omitempty"`
	Count       int       `json:"count,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	RuleTier    string    `json:"rule_tier,omitempty"`
	// The item's block, as the hub's own item had it (one of them).
	Lark   json.RawMessage `json:"lark,omitempty"`
	Doc    json.RawMessage `json:"doc,omitempty"`
	Moment json.RawMessage `json:"moment,omitempty"`
}

// Tag is one thing an inbox thing is about: Key (TagRepo…), the Value it matches by (an id), the Name people read.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// Tag keys. The user can mute by the first three (the inbox row's mute-this-repository / conversation / sender actions).
const (
	TagRepo   = "repo"
	TagChat   = "chat"
	TagSender = "sender"
	TagBranch = "branch"
)

// Inbox is a connector with things for the inbox. Collect answers from the connector's memory (it is called on
// every inbox collection; a connector that polls does so in its own Watch and calls changed(ScopeInbox)). Reply
// sends text the user confirmed (the send button is the confirmation) to the thread.
type Inbox interface {
	Collect(ctx context.Context) ([]InboxThing, error)
	Reply(ctx context.Context, r InboxReply) error
}

// InboxReply is a reply to one of a connector's inbox items.
type InboxReply struct {
	Thread string `json:"thread"`
	ItemID string `json:"item_id,omitempty"` // the hub's inbox item
	Last   string `json:"last,omitempty"`    // the newest move the item holds (InboxThing.Last): what the reply answers
	Text   string `json:"text,omitempty"`
	// ClientID is the app's id for the send: a retry with the same one goes out once (empty: the connector derives one).
	ClientID string `json:"client_id,omitempty"`
	// Changes move a document row's status first (a document item's Change Status action, the AI alter ego's proposal); Text may then be empty.
	Changes []StatusChange `json:"changes,omitempty"`
}

// StatusChange moves one row's status column to a value it offers (the hub's DocChange).
type StatusChange struct {
	Ref   string `json:"ref"`
	Field string `json:"field"`
	Value string `json:"value"`
}

// InboxSettler is an inbox whose rules can close items the hub still has open (made under older rules, or of a
// thread the connector no longer brings in): Settle gets the open items' threads and returns, for each that is
// over, why. Nothing is deleted: the hub files them as done (read).
type InboxSettler interface {
	Settle(ctx context.Context, threads []string) map[string]string
}
