package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/proto"
)

// The connector's catalog, for Persona's source subscriptions (connector.Catalog): every tool of tools.list, under the
// source the hub derived from them before the catalog was declared (id telegram, the names without "telegram_"), so
// subscriptions made then keep their recipes. messages and search answer JSON there: an object with one list of
// messages, each with an id that is unique across chats (the chat's key and the message's id), the chat as its thread
// and title, its sender, time and text, which a subscription follows message by message. Every other tool answers as
// it does for the assistant (the CLI's are JSON already, the rest text). The assistant's tools.call is unchanged.

const catalogSource = "telegram"

func (c *Conn) Catalog(context.Context) ([]connector.CatalogSource, error) {
	tools := c.Tools()
	out := make([]connector.CatalogTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, connector.CatalogTool{Name: strings.TrimPrefix(t.Name, "telegram_"), Description: t.Description,
			InputSchema: t.InputSchema, ReadOnly: true})
	}
	return []connector.CatalogSource{{ID: catalogSource, Name: c.Name(), Tools: out}}, nil
}

// CatalogCall runs a catalog tool (serve has refused one the catalog does not list, as the hub does).
func (c *Conn) CatalogCall(ctx context.Context, q connector.CatalogQuery) (string, error) {
	name := "telegram_" + q.Tool
	if q.Source != catalogSource || !slices.ContainsFunc(c.Tools(), func(t proto.Tool) bool { return t.Name == name }) {
		return "", fmt.Errorf("no tool %s in source %s", q.Tool, q.Source)
	}
	return c.call(ctx, name, q.Args, true)
}

// itemJSON is one message as the catalog's messages and search answer it.
type itemJSON struct {
	ID      string `json:"id"`      // <chat key>:<message id>
	Thread  string `json:"thread"`  // the chat's key (telegram_messages' chat)
	Title   string `json:"title"`   // the chat's name
	Message int    `json:"message"` // its id in the chat (telegram_messages' before)
	Sender  string `json:"sender,omitempty"`
	Mine    bool   `json:"mine,omitempty"`
	Type    string `json:"type,omitempty"`
	Time    string `json:"time"` // RFC 3339, in the user's zone (UTC while the hub does not know it)
	Body    string `json:"body"`
	ReplyTo int    `json:"reply_to,omitempty"`
}

func (c *Conn) itemOf(d dialog, m message) itemJSON {
	mo := c.messageOf(m)
	key := d.Peer.key()
	return itemJSON{ID: key + ":" + strconv.Itoa(m.ID), Thread: key, Title: d.Name, Message: m.ID, Sender: mo.From, Mine: mo.Mine,
		Type: mo.Type, Time: mo.At, Body: mo.Text, ReplyTo: mo.ReplyTo}
}

// messagesJSON is telegram_messages for subscriptions: the same page, oldest first.
func (c *Conn) messagesJSON(ctx context.Context, cl client, chat string, limit, before int) (string, error) {
	d, err := c.find(ctx, cl, chat)
	if err != nil {
		return "", err
	}
	msgs, err := cl.Older(ctx, d, before, time.Time{}, limit)
	if err != nil {
		return "", err
	}
	out := struct {
		Chat     chatOut    `json:"chat"`
		More     bool       `json:"more"`
		Before   int        `json:"before,omitempty"` // the older page's before
		Messages []itemJSON `json:"messages"`
	}{Chat: ref(d), Messages: []itemJSON{}}
	for _, m := range msgs {
		out.Messages = append(out.Messages, c.itemOf(d, m))
	}
	if len(msgs) == limit && limit > 0 {
		out.More, out.Before = true, msgs[0].ID
	}
	return jsonOf(out)
}

// searchJSON is telegram_search for subscriptions: the same messages found, without channels' posts and bots.
func (c *Conn) searchJSON(ctx context.Context, cl client, query, chat string, limit int) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	var in *dialog
	if chat != "" {
		d, err := c.find(ctx, cl, chat)
		if err != nil {
			return "", err
		}
		in = &d
	}
	found, err := cl.Search(ctx, query, in, time.Time{}, time.Time{}, limit)
	if err != nil {
		return "", err
	}
	out := struct {
		Query    string     `json:"query"`
		Messages []itemJSON `json:"messages"`
	}{Query: query, Messages: []itemJSON{}}
	for _, f := range found {
		if f.Dialog.kept() {
			out.Messages = append(out.Messages, c.itemOf(f.Dialog, f.Message))
		}
	}
	b, err := json.Marshal(out)
	return string(b), err
}
