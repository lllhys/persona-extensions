package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDiscord is Discord's REST API for the tests, over synthetic servers, channels and messages, with switches for
// what can go wrong. Nothing in it is a real account, server or message.
type fakeDiscord struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	token     string
	appID     snowflake
	ownerID   snowflake
	intent    bool // the Message Content intent is on
	guilds    []*fakeGuild
	messages  map[snowflake][]map[string]any // by channel, oldest first
	forbidden map[snowflake]bool             // channels whose calls answer 403
	noHistory map[snowflake]bool             // channels whose messages answer [] (no Read Message History)
	archived  map[snowflake][]map[string]any // archived public threads by channel, newest first
	indexing  bool                           // search answers 202
	failing   string                         // a path prefix that answers 500
	limited   map[string]float64             // a path prefix that answers 429 once, with this retry_after
	global    map[string]float64             // a path prefix that answers a global 429 once
	badTime   map[string]bool                // message ids sent with a timestamp that cannot be read
	requests  []string
	next      int64
}

type fakeGuild struct {
	id       snowflake
	name     string
	channels []map[string]any
	threads  []map[string]any // active
}

const fakeToken = "fake-bot-token-0001"

func newFakeDiscord(t *testing.T) *fakeDiscord {
	f := &fakeDiscord{t: t, token: fakeToken, intent: true, messages: map[snowflake][]map[string]any{},
		forbidden: map[snowflake]bool{}, noHistory: map[snowflake]bool{}, archived: map[snowflake][]map[string]any{},
		limited: map[string]float64{}, global: map[string]float64{}, badTime: map[string]bool{}}
	f.appID = f.id(time.Now().Add(-1000 * time.Hour))
	f.ownerID = f.id(time.Now().Add(-999 * time.Hour))
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDiscord) endpoints() endpoints {
	return endpoints{API: f.srv.URL + "/api/v10", Web: "https://discord.example"}
}

func (f *fakeDiscord) set(do func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	do()
}

// id is a new snowflake for a moment.
func (f *fakeDiscord) id(at time.Time) snowflake {
	f.next++
	return snowflake(uint64(at.UnixMilli()-discordEpoch)<<22 | uint64(f.next&0xfff))
}

func (f *fakeDiscord) user(id snowflake, name string, bot bool) map[string]any {
	return map[string]any{"id": id.String(), "username": name, "global_name": nil, "discriminator": "0", "bot": bot}
}

func (f *fakeDiscord) addGuild(name string) *fakeGuild {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &fakeGuild{id: f.id(time.Now().Add(-900 * time.Hour)), name: name}
	f.guilds = append(f.guilds, g)
	return g
}

func (f *fakeDiscord) addChannel(g *fakeGuild, name string, kind int) snowflake {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id(time.Now().Add(-800 * time.Hour))
	g.channels = append(g.channels, map[string]any{"id": id.String(), "type": kind, "guild_id": g.id.String(), "name": name,
		"position": len(g.channels), "permission_overwrites": []any{}, "last_message_id": nil})
	return id
}

// addThread adds a thread to a channel: active, or archived at archivedAt.
func (f *fakeDiscord) addThread(g *fakeGuild, parent snowflake, name string, archivedAt time.Time) snowflake {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id(time.Now().Add(-700 * time.Hour))
	meta := map[string]any{"archived": !archivedAt.IsZero(), "auto_archive_duration": 1440, "locked": false,
		"archive_timestamp": time.Now().Add(-700 * time.Hour).Format(time.RFC3339)}
	if !archivedAt.IsZero() {
		meta["archive_timestamp"] = archivedAt.UTC().Format(time.RFC3339Nano)
	}
	th := map[string]any{"id": id.String(), "type": chPublicThread, "guild_id": g.id.String(), "parent_id": parent.String(),
		"name": name, "last_message_id": nil, "thread_metadata": meta}
	if archivedAt.IsZero() {
		g.threads = append(g.threads, th)
	} else {
		f.archived[parent] = append([]map[string]any{th}, f.archived[parent]...)
		slices.SortFunc(f.archived[parent], func(a, b map[string]any) int { return archivedAt2(b).Compare(archivedAt2(a)) })
	}
	return id
}

// say adds a message to a channel at a moment, by the owner (mine) or someone else.
func (f *fakeDiscord) say(ch snowflake, at time.Time, mine bool, content string, extra map[string]any) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	author := f.user(f.id(at.Add(-time.Hour)), "Alex Example", false)
	if mine {
		author = f.user(f.ownerID, "Owner Example", false)
	}
	m := map[string]any{"id": f.id(at).String(), "channel_id": ch.String(), "type": 0, "content": content, "author": author,
		"timestamp": at.UTC().Format(time.RFC3339Nano), "mentions": []any{}, "attachments": []any{}, "embeds": []any{}, "flags": 0}
	for k, v := range extra {
		m[k] = v
	}
	f.messages[ch] = append(f.messages[ch], m)
	for _, g := range f.guilds {
		for _, list := range [][]map[string]any{g.channels, g.threads} {
			for _, c := range list {
				if c["id"] == ch.String() {
					c["last_message_id"] = m["id"]
				}
			}
		}
	}
	for _, list := range f.archived {
		for _, c := range list {
			if c["id"] == ch.String() {
				c["last_message_id"] = m["id"]
			}
		}
	}
	return m
}

// requested counts the requests whose "GET path" starts with prefix.
func (f *fakeDiscord) requested(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeDiscord) serve(w http.ResponseWriter, r *http.Request) {
	// The node's exact unauthenticated discovery probe is not a Discord API call.
	if isNodePortProbe(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v10")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+path)
	if r.Header.Get("Authorization") != "Bot "+f.token {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "401: Unauthorized", "code": 0})
		return
	}
	if f.failing != "" && strings.HasPrefix(path, f.failing) {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"message": "Internal Server Error", "code": 0})
		return
	}
	for prefix, wait := range f.limited {
		if strings.HasPrefix(path, prefix) {
			delete(f.limited, prefix)
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"message": "You are being rate limited.", "retry_after": wait, "global": false})
			return
		}
	}
	for prefix, wait := range f.global {
		if strings.HasPrefix(path, prefix) {
			delete(f.global, prefix)
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"message": "You are being rate limited.", "retry_after": wait, "global": true})
			return
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	q := r.URL.Query()
	switch {
	case path == "/applications/@me":
		flags := 0
		if f.intent {
			flags = flagMessageContentLimited
		}
		writeJSON(w, 200, map[string]any{"id": f.appID.String(), "name": "Persona Test", "flags": flags,
			"bot": f.user(f.appID, "Persona Test Bot", true), "owner": f.user(f.ownerID, "Owner Example", false), "team": nil})
	case path == "/users/@me/guilds":
		limit, _ := strconv.Atoi(q.Get("limit"))
		after, _ := parseSnowflake(q.Get("after"))
		out := []map[string]any{}
		for _, g := range f.guilds {
			if g.id > after && len(out) < limit {
				out = append(out, map[string]any{"id": g.id.String(), "name": g.name, "permissions": "66560"})
			}
		}
		writeJSON(w, 200, out)
	case len(parts) == 3 && parts[0] == "guilds" && parts[2] == "channels":
		g := f.guild(parts[1])
		if g == nil {
			writeJSON(w, 404, map[string]any{"message": "Unknown Guild", "code": 10004})
			return
		}
		writeJSON(w, 200, g.channels)
	case len(parts) == 4 && parts[0] == "guilds" && parts[2] == "threads" && parts[3] == "active":
		g := f.guild(parts[1])
		threads := []map[string]any{}
		if g != nil {
			threads = append(threads, g.threads...)
		}
		writeJSON(w, 200, map[string]any{"threads": threads, "members": []any{}})
	case len(parts) == 3 && parts[0] == "guilds" && parts[2] == "roles":
		writeJSON(w, 200, []map[string]any{{"id": parts[1], "name": "@everyone"}})
	case len(parts) == 4 && parts[0] == "guilds" && parts[2] == "messages" && parts[3] == "search":
		f.search(w, q)
	case len(parts) == 5 && parts[0] == "channels" && parts[2] == "threads" && parts[3] == "archived":
		ch, _ := parseSnowflake(parts[1])
		limit, _ := strconv.Atoi(q.Get("limit"))
		var before time.Time
		if b := q.Get("before"); b != "" {
			before, _ = time.Parse(time.RFC3339Nano, b)
		}
		out := []map[string]any{}
		more := false
		for _, t := range f.archived[ch] { // newest archived first
			if at := archivedAt2(t); !before.IsZero() && !at.Before(before) {
				continue
			}
			if len(out) == limit {
				more = true
				break
			}
			out = append(out, t)
		}
		writeJSON(w, 200, map[string]any{"threads": out, "members": []any{}, "has_more": more})
	case len(parts) == 3 && parts[0] == "channels" && parts[2] == "messages":
		ch, _ := parseSnowflake(parts[1])
		switch {
		case f.forbidden[ch]:
			writeJSON(w, http.StatusForbidden, map[string]any{"message": "Missing Access", "code": 50001})
		case f.noHistory[ch]:
			writeJSON(w, 200, []any{})
		default:
			out := f.page(ch, q)
			for i, m := range out {
				if f.badTime[m["id"].(string)] {
					m = maps.Clone(m)
					m["timestamp"] = 12345 // not a time: this message cannot be read
					out[i] = m
				}
			}
			writeJSON(w, 200, out)
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "404: Not Found", "code": 0})
	}
}

func (f *fakeDiscord) guild(id string) *fakeGuild {
	for _, g := range f.guilds {
		if g.id.String() == id {
			return g
		}
	}
	return nil
}

func (f *fakeDiscord) search(w http.ResponseWriter, q map[string][]string) {
	if f.indexing {
		writeJSON(w, http.StatusAccepted, map[string]any{"message": "Index not yet available. Try again later", "code": 110000,
			"documents_indexed": 0, "retry_after": 2})
		return
	}
	text := strings.ToLower(q["content"][0])
	var hits [][]map[string]any
	for _, list := range f.messages {
		for _, m := range list {
			if strings.Contains(strings.ToLower(m["content"].(string)), text) {
				hits = append(hits, []map[string]any{m})
			}
		}
	}
	slices.SortFunc(hits, func(a, b []map[string]any) int { return strings.Compare(b[0]["id"].(string), a[0]["id"].(string)) })
	writeJSON(w, 200, map[string]any{"total_results": len(hits), "messages": hits, "threads": []any{}, "members": []any{}})
}

// page answers GET /channels/{id}/messages as Discord does: newest first; with after, the oldest ones after it.
func (f *fakeDiscord) page(ch snowflake, q map[string][]string) []map[string]any {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	limit, _ := strconv.Atoi(get("limit"))
	if limit <= 0 {
		limit = 50
	}
	before, _ := parseSnowflake(get("before"))
	after, _ := parseSnowflake(get("after"))
	all := f.messages[ch]
	out := []map[string]any{}
	if get("after") != "" {
		for _, m := range all {
			if id, _ := parseSnowflake(m["id"].(string)); id > after && len(out) < limit {
				out = append(out, m)
			}
		}
		slices.Reverse(out)
		return out
	}
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if id, _ := parseSnowflake(all[i]["id"].(string)); before == 0 || id < before {
			out = append(out, all[i])
		}
	}
	return out
}

// archivedAt2 is when a fake thread was archived.
func archivedAt2(t map[string]any) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, t["thread_metadata"].(map[string]any)["archive_timestamp"].(string))
	return at
}
