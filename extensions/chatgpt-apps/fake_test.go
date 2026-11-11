package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChatGPT is a loopback stand-in for auth.openai.com and chatgpt.com's backend: device-code sign-in, single-use
// refresh tokens, the MCP endpoint of the connected apps and the plugin marketplace. Synthetic data only. It records
// what it was asked, so tests can say a write ran once (writes are tested only here).
type fakeChatGPT struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	approved  bool          // the user entered the device code
	access    string        // the access token in force
	refresh   string        // the refresh token in force (each works once)
	ttl       time.Duration // how long an access token lasts
	serial    int
	refreshes int
	used      map[string]bool // refresh tokens already spent
	refuse401 int             // answer this many backend calls 401 even with the right token
	sse       bool            // MCP answers as an event stream
	calls     []fakeCall      // tools/call, in order
	installed map[string]bool
	connected map[string]bool  // apps connected (else their tools are synthetic links)
	badBundle bool             // the plugin package holds a path out of it
	extra     []map[string]any // more marketplace plugins (load more, screenshots)
	failList  bool             // the marketplace listing answers 500
	asked     []string         // backend paths asked, in order
}

type fakeCall struct {
	Name string
	Args map[string]any
}

const (
	fakeAccount = "acct_example"
	fakeEmail   = "me@example.com"
)

func newFakeChatGPT(t *testing.T) *fakeChatGPT {
	f := &fakeChatGPT{t: t, ttl: time.Hour, used: map[string]bool{}, installed: map[string]bool{"plugin_example_notes": true},
		connected: map[string]bool{"connector_mail": true, "connector_broker": true}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeChatGPT) endpoints() endpoints {
	return endpoints{API: f.srv.URL + "/backend-api", Auth: f.srv.URL}
}

func (f *fakeChatGPT) approve() { f.mu.Lock(); f.approved = true; f.mu.Unlock() }

func (f *fakeChatGPT) toolCalls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

func jwt(claims map[string]any) string {
	enc := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	return enc(map[string]string{"alg": "none"}) + "." + enc(claims) + ".sig"
}

// issueLocked makes a new token set: a new access token and a new refresh token.
func (f *fakeChatGPT) issueLocked() map[string]string {
	f.serial++
	auth := map[string]any{"chatgpt_account_id": fakeAccount, "chatgpt_plan_type": "plus"}
	f.access = jwt(map[string]any{"exp": time.Now().Add(f.ttl).Unix(), "n": f.serial, "https://api.openai.com/auth": auth})
	f.refresh = fmt.Sprintf("refresh-%d", f.serial)
	return map[string]string{"access_token": f.access, "refresh_token": f.refresh,
		"id_token": jwt(map[string]any{"email": fakeEmail, "https://api.openai.com/auth": auth})}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeChatGPT) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("User-Agent") == "" {
		writeJSON(w, 403, map[string]string{"error": "no user agent"}) // as Cloudflare does
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/api/accounts/deviceauth/usercode":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != clientID {
			writeJSON(w, 400, map[string]string{"error": "client"})
			return
		}
		writeJSON(w, 200, map[string]any{"device_auth_id": "dev_example", "user_code": "ABCD-EFGH", "interval": "1",
			"expires_at": time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339)})
	case r.Method == "POST" && r.URL.Path == "/api/accounts/deviceauth/token":
		f.mu.Lock()
		ok := f.approved
		f.mu.Unlock()
		if !ok {
			writeJSON(w, 403, map[string]string{"error": "deviceauth_authorization_pending"})
			return
		}
		writeJSON(w, 200, map[string]string{"authorization_code": "code_example", "code_verifier": "verifier_example"})
	case r.Method == "POST" && r.URL.Path == "/oauth/token":
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("client_id") != clientID {
			writeJSON(w, 400, map[string]string{"error": "invalid_client"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "code_example" || r.Form.Get("code_verifier") != "verifier_example" ||
				r.Form.Get("redirect_uri") != f.srv.URL+"/deviceauth/callback" {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			writeJSON(w, 200, f.issueLocked())
		case "refresh_token":
			rt := r.Form.Get("refresh_token")
			if rt != f.refresh || f.used[rt] {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "refresh token already used"})
				return
			}
			f.used[rt] = true
			f.refreshes++
			writeJSON(w, 200, f.issueLocked())
		default:
			writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
		}
	case strings.HasPrefix(r.URL.Path, "/bundles/"):
		f.mu.Lock()
		bad := f.badBundle
		f.mu.Unlock()
		w.Write(bundle(f.t, bad))
	case strings.HasPrefix(r.URL.Path, "/backend-api/"):
		f.backend(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeChatGPT) backend(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	authorized := r.Header.Get("Authorization") == "Bearer "+f.access && f.access != "" && r.Header.Get("chatgpt-account-id") == fakeAccount
	if authorized && f.refuse401 > 0 {
		f.refuse401--
		authorized = false
	}
	f.mu.Unlock()
	if !authorized {
		writeJSON(w, 401, map[string]string{"detail": "unauthorized"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/backend-api")
	f.mu.Lock()
	f.asked = append(f.asked, r.Method+" "+path)
	f.mu.Unlock()
	switch {
	case r.Method == "POST" && path == "/ps/mcp":
		f.mcp(w, r)
	case r.Method == "GET" && path == "/ps/plugins/search":
		q := strings.ToLower(r.URL.Query().Get("q"))
		var hits []any
		for _, p := range f.catalog() {
			if strings.Contains(strings.ToLower(p["name"].(string)+" "+p["release"].(map[string]any)["description"].(string)), q) {
				hits = append(hits, p)
			}
		}
		writeJSON(w, 200, map[string]any{"plugins": hits})
	case r.Method == "GET" && path == "/ps/plugins/list":
		f.mu.Lock()
		fail := f.failList
		f.mu.Unlock()
		if fail {
			writeJSON(w, 500, map[string]string{"detail": "internal error"})
			return
		}
		all := f.catalog()
		from, limit := 0, 50
		fmt.Sscanf(r.URL.Query().Get("pageToken"), "%d", &from)
		fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
		to := min(from+limit, len(all))
		next := ""
		if to < len(all) {
			next = fmt.Sprint(to)
		}
		page := []any{}
		for _, p := range all[from:to] {
			page = append(page, p)
		}
		writeJSON(w, 200, map[string]any{"plugins": page, "pagination": map[string]any{"next_page_token": next}})
	case r.Method == "GET" && path == "/ps/plugins/installed":
		var out []any
		for _, p := range f.catalog() {
			f.mu.Lock()
			in := f.installed[p["id"].(string)]
			f.mu.Unlock()
			if in {
				out = append(out, p)
			}
		}
		writeJSON(w, 200, map[string]any{"plugins": out, "pagination": map[string]any{}})
	case r.Method == "POST" && path == "/ps/apps/batch":
		writeJSON(w, 200, map[string]any{"apps": []any{
			map[string]any{"id": "connector_mail", "name": "Example Mail", "supported_auth_types": []string{"OAUTH"}},
			map[string]any{"id": "connector_notes", "name": "Example Notes", "supported_auth_types": []string{"OAUTH"}},
		}})
	case strings.HasPrefix(path, "/ps/plugins/"):
		rest := strings.TrimPrefix(path, "/ps/plugins/")
		id, verb, _ := strings.Cut(rest, "/")
		for _, p := range f.catalog() {
			if p["id"] != id {
				continue
			}
			switch {
			case r.Method == "GET" && verb == "":
				writeJSON(w, 200, p)
			case r.Method == "POST" && verb == "install":
				f.mu.Lock()
				f.installed[id] = true
				f.mu.Unlock()
				writeJSON(w, 200, map[string]any{"enabled": true, "app_ids_needing_auth": []string{"connector_notes"}})
			case r.Method == "POST" && verb == "uninstall":
				f.mu.Lock()
				delete(f.installed, id)
				f.mu.Unlock()
				writeJSON(w, 200, map[string]any{"enabled": false})
			default:
				http.NotFound(w, r)
			}
			return
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeChatGPT) catalog() []map[string]any {
	return append([]map[string]any{
		{"id": "plugin_example_notes", "name": "example-notes", "share_url": "https://chatgpt.com/plugins/plugin_example_notes",
			"release": map[string]any{"display_name": "Example Notes", "description": "Notes for the example tests", "version": "1.2.0",
				"app_ids": []string{"connector_notes"}, "bundle_download_url": f.srv.URL + "/bundles/example-notes.tgz",
				"skills":    []any{map[string]any{"name": "notes", "description": "Work with notes"}},
				"interface": map[string]any{"developer_name": "Example Co", "long_description": "A plugin for tests."}}},
		{"id": "plugin_example_mail", "name": "example-mail",
			"release": map[string]any{"display_name": "Example Mail", "description": "Mail for the example tests", "version": "2.0.0",
				"app_ids": []string{"connector_mail"}, "skills": []any{},
				"interface": map[string]any{"developer_name": "Example Co"}}},
	}, f.extra...)
}

// morePlugins are n more marketplace plugins with nothing else to them.
func morePlugins(n int) []map[string]any {
	var out []map[string]any
	for i := range n {
		out = append(out, map[string]any{"id": fmt.Sprintf("plugin_example_%03d", i), "name": fmt.Sprintf("example-%03d", i),
			"release": map[string]any{"display_name": fmt.Sprintf("Example %03d", i), "description": "One more example plugin",
				"version": "1.0.0", "app_ids": []string{}, "skills": []any{}, "interface": map[string]any{"developer_name": "Example Co"}}})
	}
	return out
}

func boolp(b bool) *bool { return &b }

func (f *fakeChatGPT) tools() []map[string]any {
	ann := func(ro bool, destructive ...bool) map[string]any {
		m := map[string]any{"readOnlyHint": ro}
		if len(destructive) > 0 {
			m["destructiveHint"] = destructive[0]
		}
		return m
	}
	meta := func(app, id string) map[string]any {
		m := map[string]any{"connector_name": app, "connector_id": id}
		f.mu.Lock()
		connected := f.connected[id]
		f.mu.Unlock()
		if !connected {
			m["_codex_apps"] = map[string]any{"synthetic_link": true, "resource_uri": "connectors://" + id + "/x"}
		}
		return m
	}
	obj := func(props ...string) map[string]any {
		p := map[string]any{}
		for _, k := range props {
			p[k] = map[string]any{"type": "string"}
		}
		return map[string]any{"type": "object", "properties": p}
	}
	return []map[string]any{
		{"name": "example_mail.search_messages", "description": "Search mail.\nMore text.", "inputSchema": obj("query"),
			"annotations": ann(true), "_meta": meta("Example Mail", "connector_mail")},
		{"name": "example_mail.send_message", "description": "Send a mail message.", "inputSchema": obj("to", "subject", "body"),
			"annotations": ann(false), "_meta": meta("Example Mail", "connector_mail")},
		// Marked read-only by its app, but its name says it sends: still a write.
		{"name": "example_mail.forward_message", "description": "Forward a message.", "inputSchema": obj("id", "to"),
			"annotations": ann(true), "_meta": meta("Example Mail", "connector_mail")},
		{"name": "example_broker.get_positions", "description": "Positions.", "inputSchema": obj(),
			"annotations": ann(true), "_meta": meta("Example Broker", "connector_broker")},
		{"name": "example_broker.list_orders", "description": "Orders, marked destructive by mistake.", "inputSchema": obj(),
			"annotations": ann(true, true), "_meta": meta("Example Broker", "connector_broker")},
		{"name": "example_notes.read_note", "description": "Read a note.", "inputSchema": obj("id"),
			"annotations": ann(true), "_meta": meta("Example Notes", "connector_notes")},
	}
}

func (f *fakeChatGPT) mcp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var result any
	var rpcErr any
	switch req.Method {
	case "tools/list":
		result = map[string]any{"tools": f.tools()}
	case "tools/call":
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{Name: req.Params.Name, Args: req.Params.Arguments})
		f.mu.Unlock()
		switch req.Params.Name {
		case "example_mail.search_messages":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"messages":[{"subject":"Example subject"}]}`}}}
		case "example_mail.send_message":
			result = map[string]any{"structuredContent": map[string]any{"sent": true, "id": "msg_example"}}
		case "example_broker.get_positions":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "no positions"}}}
		default:
			result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Unknown tool"}}}
		}
	default:
		rpcErr = map[string]any{"code": -32601, "message": "method not found"}
	}
	msg := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	f.mu.Lock()
	sse := f.sse
	f.mu.Unlock()
	if sse {
		raw, _ := json.Marshal(msg)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\nevent: message\ndata: %s\n\n", raw)
		return
	}
	writeJSON(w, 200, msg)
}

// bundle is a plugin package: skills/notes/SKILL.md and a reference file; bad adds a path out of the package.
func bundle(t *testing.T, bad bool) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, body)
	}
	_ = tw.WriteHeader(&tar.Header{Name: "skills/", Mode: 0o755, Typeflag: tar.TypeDir})
	add("skills/notes/SKILL.md", "---\nname: notes\n---\nUse mcp__codex_apps__example_notes__read_note to read a note.\n")
	add("skills/notes/reference/fields.md", "id: the note's id\n")
	add(".app.json", `{"apps":["connector_notes"]}`)
	if bad {
		add("../outside.txt", "nope")
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}
