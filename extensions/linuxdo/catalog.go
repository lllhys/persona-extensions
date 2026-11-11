package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// The connector's catalog, for Persona's source subscriptions (connector.catalog.list / connector.catalog.call): the
// same two tools as tools.list, under the source the hub derived from them before the catalog was declared (id
// linuxdo, the names without "linuxdo_"), so subscriptions made then keep their recipes. hot answers JSON there, an
// object with one list of topics each with its id, which a subscription follows topic by topic; topic answers its text
// (one topic: a snapshot). The assistant's tools.call answers text, as before.

const catalogSource = "linuxdo"

func (p *plugin) cCatalogList(context.Context, *pluginkit.Hub, json.RawMessage) (any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{"name": strings.TrimPrefix(t.Name, "linuxdo_"), "description": t.Description,
			"input_schema": t.InputSchema, "read_only": true})
	}
	return map[string]any{"sources": []map[string]any{{"id": catalogSource, "name": "LINUX DO", "tools": out}}}, nil
}

func (p *plugin) cCatalogCall(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var in struct {
		Source string          `json:"source"`
		Tool   string          `json:"tool"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	known := false
	for _, t := range tools {
		known = known || t.Name == "linuxdo_"+in.Tool
	}
	if in.Source != catalogSource || !known {
		return nil, pluginkit.Errorf("not_read_only", "the source and tool must be in the read-only catalog")
	}
	text, err := p.run(ctx, "linuxdo_"+in.Tool, in.Args, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text}, nil
}

type hotTopicJSON struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Author   string `json:"author,omitempty"`
	Posts    int    `json:"posts,omitempty"`
	People   int    `json:"people,omitempty"`
	Time     string `json:"time,omitempty"` // when it was posted, RFC 3339
	Pinned   bool   `json:"pinned,omitempty"`
	Link     string `json:"link"`
	Body     string `json:"body,omitempty"` // the excerpt
}

type hotJSON struct {
	List   string         `json:"list"`
	Total  int            `json:"total"`            // the list's topics
	Filter string         `json:"filter,omitempty"` // what the user's interests and the call's keywords kept, in words
	Kept   int            `json:"kept"`             // the topics they kept
	More   bool           `json:"more"`             // more were kept than listed
	Topics []hotTopicJSON `json:"topics"`
}

func (a hot) data() hotJSON {
	out := hotJSON{List: a.list, Total: a.all, Filter: a.filter, Kept: a.kept, More: a.kept > len(a.topics),
		Topics: make([]hotTopicJSON, 0, len(a.topics))}
	for _, t := range a.topics {
		j := hotTopicJSON{ID: t.ID, Title: t.Title, Category: t.Category, Author: t.Author, Posts: t.Posts, People: t.People,
			Pinned: t.Pinned, Link: t.URL, Body: clip(t.Excerpt, 500)}
		if !t.At.IsZero() {
			j.Time = t.At.UTC().Format(time.RFC3339)
		}
		out.Topics = append(out.Topics, j)
	}
	return out
}
