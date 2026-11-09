package ilink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fake routes every request, whatever its host, to a handler; it records the hosts asked for.
type fake struct {
	t       *testing.T
	srv     *httptest.Server
	handler func(w http.ResponseWriter, r *http.Request, body map[string]any)
	hosts   []string
}

func newFake(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body map[string]any)) *fake {
	f := &fake{t: t, handler: h}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" { // a node's port probe (node/ports.go), not this test's client
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			if err := json.Unmarshal(b, &body); err != nil {
				t.Errorf("request body is not JSON: %s", b)
			}
		}
		f.handler(w, r, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) RoundTrip(r *http.Request) (*http.Response, error) {
	f.hosts = append(f.hosts, r.URL.Host)
	u, _ := url.Parse(f.srv.URL)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host, r2.Host = u.Scheme, u.Host, u.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func (f *fake) client(token, base string) *Client {
	c, err := New(Options{BaseURL: base, Token: token, Agent: "Persona/1 bad<agent>", HTTP: &http.Client{Transport: f}})
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestValidateBaseURL(t *testing.T) {
	for _, ok := range []string{"https://ilinkai.weixin.qq.com", "https://ilinkai.weixin.qq.com/", "https://weixin.qq.com:443"} {
		if _, err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://ilinkai.weixin.qq.com", "https://evil.com", "https://weixin.qq.com.evil.com",
		"https://u:p@ilinkai.weixin.qq.com", "https://ilinkai.weixin.qq.com:8443", "https://ilinkai.weixin.qq.com/x",
		"https://ilinkai.weixin.qq.com/?a=1", "https://evilweixin.qq.com"} {
		if _, err := ValidateBaseURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSanitizeAgentAndVersion(t *testing.T) {
	if got := sanitizeAgent("Persona/1 bad<agent> x/y+z"); got != "Persona/1 x/y+z" {
		t.Errorf("agent %q", got)
	}
	if got := sanitizeAgent("nothing"); got != "Persona/unknown" {
		t.Errorf("empty agent %q", got)
	}
	if got := versionNumber("2.4.6"); got != 2<<16|4<<8|6 {
		t.Errorf("version %d", got)
	}
}

func TestQRAuthorization(t *testing.T) {
	polls := 0
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			if r.URL.Query().Get("bot_type") != "3" || r.Header.Get("Authorization") != "" {
				t.Errorf("begin: %s %q", r.URL, r.Header.Get("Authorization"))
			}
			if toks := body["local_token_list"].([]any); len(toks) != 10 || toks[9] != "t11" {
				t.Errorf("local tokens %v", toks)
			}
			writeJSON(w, map[string]any{"qrcode": "QC", "qrcode_img_content": "https://liteapp.weixin.qq.com/q/abc"})
		case "/ilink/bot/get_qrcode_status":
			polls++
			if r.URL.Query().Get("qrcode") != "QC" {
				t.Errorf("poll %s", r.URL)
			}
			switch polls {
			case 1:
				writeJSON(w, map[string]any{"status": "scaned_but_redirect", "redirect_host": "sh.ilinkai.weixin.qq.com"})
			case 2:
				if r.URL.Query().Get("verify_code") != "123456" {
					t.Errorf("verify code %s", r.URL)
				}
				writeJSON(w, map[string]any{"status": "confirmed", "bot_token": "BT", "ilink_bot_id": "bot@im", "ilink_user_id": "owner@im", "baseurl": "https://ilinkai.weixin.qq.com"})
			}
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	})
	c := f.client("", "")
	toks := []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10", "t11"}
	q, err := c.BeginQR(context.Background(), toks[:12])
	if err != nil || q.Code != "QC" || !strings.HasPrefix(q.Content, "https://liteapp.weixin.qq.com/") {
		t.Fatalf("begin %+v %v", q, err)
	}
	st, err := c.PollQR(context.Background(), q.Code, "", "", time.Second)
	if err != nil || st.State != QRRedirect || st.RedirectBase != "https://sh.ilinkai.weixin.qq.com/" {
		t.Fatalf("redirect %+v %v", st, err)
	}
	st, err = c.PollQR(context.Background(), q.Code, "123456", st.RedirectBase, time.Second)
	if err != nil || st.State != QRConfirmed || st.Credentials != (Credentials{Token: "BT", BotID: "bot@im", UserID: "owner@im", BaseURL: "https://ilinkai.weixin.qq.com/"}) {
		t.Fatalf("confirmed %+v %v", st, err)
	}
	if f.hosts[0] != "ilinkai.weixin.qq.com" || f.hosts[2] != "sh.ilinkai.weixin.qq.com" {
		t.Errorf("hosts %v", f.hosts)
	}
}

func TestQRRejectsForeignContentAndRedirect(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		if r.URL.Path == "/ilink/bot/get_bot_qrcode" {
			writeJSON(w, map[string]any{"qrcode": "QC", "qrcode_img_content": "https://evil.example/q"})
			return
		}
		writeJSON(w, map[string]any{"status": "scaned_but_redirect", "redirect_host": "evil.example"})
	})
	c := f.client("", "")
	if _, err := c.BeginQR(context.Background(), nil); CodeOf(err) != CodeBadResponse {
		t.Errorf("foreign QR content: %v", err)
	}
	if _, err := c.PollQR(context.Background(), "QC", "", "", time.Second); CodeOf(err) != CodeProtocol {
		t.Errorf("foreign redirect: %v", err)
	}
}

func TestPollQRTimeoutIsWaiting(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		<-r.Context().Done()
	})
	st, err := f.client("", "").PollQR(context.Background(), "QC", "", "", 50*time.Millisecond)
	if err != nil || st.State != QRWaiting {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestGetUpdates(t *testing.T) {
	var calls int
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		calls++
		if r.Header.Get("Authorization") != "Bearer BT" || r.Header.Get("AuthorizationType") != "ilink_bot_token" ||
			r.Header.Get("iLink-App-Id") != "bot" || r.Header.Get("X-WECHAT-UIN") == "" {
			t.Errorf("headers %v", r.Header)
		}
		bi := body["base_info"].(map[string]any)
		if bi["bot_agent"] != "Persona/1" || bi["channel_version"] != ClientVersion {
			t.Errorf("base_info %v", bi)
		}
		if calls == 2 {
			writeJSON(w, map[string]any{"errcode": -14})
			return
		}
		if body["get_updates_buf"] != "C0" {
			t.Errorf("cursor %v", body["get_updates_buf"])
		}
		writeJSON(w, map[string]any{"ret": 0, "get_updates_buf": "C1", "longpolling_timeout_ms": 25000, "msgs": []any{
			map[string]any{"message_id": 42, "from_user_id": "owner@im", "context_token": "CT", "message_type": 1, "create_time_ms": 1000,
				"item_list": []any{
					map[string]any{"type": 1, "text_item": map[string]any{"text": "你好"},
						"ref_msg": map[string]any{"title": "引用", "message_item": map[string]any{"type": 1, "text_item": map[string]any{"text": "原文"}}}},
					map[string]any{"type": 3, "voice_item": map[string]any{"text": "语音转写", "media": map[string]any{"encrypt_query_param": "q", "aes_key": "k"}}},
					map[string]any{"type": 2, "image_item": map[string]any{"aeskey": "00112233445566778899aabbccddeeff", "media": map[string]any{"full_url": "https://novac2c.cdn.weixin.qq.com/x"}}},
				}},
			map[string]any{"client_id": "c-1", "from_user_id": "x", "context_token": "CT2", "item_list": []any{}},
			map[string]any{"message_id": "7", "from_user_id": "bot@im", "context_token": "CT", "message_type": 2},
			map[string]any{"seq": 9, "from_user_id": "y"},
		}})
	})
	c := f.client("BT", "https://ilinkai.weixin.qq.com")
	p, err := c.GetUpdates(context.Background(), "C0")
	if err != nil {
		t.Fatal(err)
	}
	if p.Cursor != "C1" || p.Timeout != 25*time.Second || len(p.Messages) != 2 {
		t.Fatalf("%+v", p)
	}
	m := p.Messages[0]
	if m.ID != "message:42" || m.From != "owner@im" || m.ContextToken != "CT" || m.Text != "你好\n语音转写" || m.Quote != "引用\n原文" || m.At != 1000 {
		t.Errorf("message %+v", m)
	}
	if len(m.Media) != 2 || m.Media[0].Kind != MediaVoice || m.Media[1].Kind != MediaImage || m.Media[1].KeyHex == "" {
		t.Errorf("media %+v", m.Media)
	}
	if p.Messages[1].ID != "client:c-1" {
		t.Errorf("client id %+v", p.Messages[1])
	}
	if _, err := c.GetUpdates(context.Background(), "C1"); CodeOf(err) != CodeReplaced || Retryable(err) {
		t.Errorf("replaced: %v", err)
	}
}

func TestGetUpdatesTimeoutKeepsCursor(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		writeJSON(w, map[string]any{"ret": -1})
	})
	if _, err := f.client("BT", "").GetUpdates(context.Background(), "C"); CodeOf(err) != CodeProtocol || !Retryable(err) {
		t.Errorf("ret -1: %v", err)
	}
}

func TestSendTextAndTyping(t *testing.T) {
	var got []map[string]any
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		body["path"] = r.URL.Path
		got = append(got, body)
		if r.URL.Path == "/ilink/bot/getconfig" {
			writeJSON(w, map[string]any{"ret": 0, "typing_ticket": "TT"})
			return
		}
		writeJSON(w, map[string]any{"ret": 0})
	})
	c := f.client("BT", "")
	ctx := context.Background()
	if err := c.SendText(ctx, "owner@im", "CT", "cid", "回答"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendText(ctx, "owner@im", "", "cid", "x"); CodeOf(err) != CodeProtocol {
		t.Errorf("no context token: %v", err)
	}
	if err := c.SendText(ctx, "owner@im", "CT", "cid", strings.Repeat("字", MaxText+1)); CodeOf(err) != CodeProtocol {
		t.Errorf("too long: %v", err)
	}
	tk, err := c.TypingTicket(ctx, "owner@im", "CT")
	if err != nil || tk != "TT" {
		t.Fatal(tk, err)
	}
	if err := c.SetTyping(ctx, "owner@im", tk, true); err != nil {
		t.Fatal(err)
	}
	msg := got[0]["msg"].(map[string]any)
	item := msg["item_list"].([]any)[0].(map[string]any)
	if msg["to_user_id"] != "owner@im" || msg["client_id"] != "cid" || msg["context_token"] != "CT" ||
		msg["message_type"] != float64(typeBot) || msg["message_state"] != float64(stateFinish) || item["text_item"].(map[string]any)["text"] != "回答" {
		t.Errorf("send %v", msg)
	}
	if got[2]["path"] != "/ilink/bot/sendtyping" || got[2]["status"] != float64(typingOn) || got[2]["typing_ticket"] != "TT" {
		t.Errorf("typing %v", got[2])
	}
}

func TestDownload(t *testing.T) {
	key, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	plain := []byte("picture bytes, not a multiple of sixteen")
	enc, err := EncryptECB(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		if r.URL.Path != "/c2c/download" || r.URL.Query().Get("encrypted_query_param") != "Q" {
			t.Errorf("download %s", r.URL)
		}
		_, _ = w.Write(enc)
	})
	c := f.client("BT", "")
	for _, m := range []Media{
		{Query: "Q", KeyHex: hex.EncodeToString(key)},
		{Query: "Q", KeyB64: base64.StdEncoding.EncodeToString(key)},
		{Query: "Q", KeyB64: base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key)))},
	} {
		got, err := c.Download(context.Background(), m)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("%+v: %q %v", m, got, err)
		}
	}
	if f.hosts[0] != "novac2c.cdn.weixin.qq.com" {
		t.Errorf("host %v", f.hosts)
	}
	if _, err := c.Download(context.Background(), Media{URL: "https://evil.example/x", KeyHex: hex.EncodeToString(key)}); CodeOf(err) != CodeProtocol {
		t.Errorf("foreign URL: %v", err)
	}
	if _, err := DecryptECB(enc[:16], key); err == nil {
		t.Error("bad padding accepted")
	}
}

func TestFilterMarkdown(t *testing.T) {
	in := "##### 小标题\n**粗体** *斜体* *italic* ~~删除~~ ![图](https://x/y.png) `*代码*`\n```\n~~原样~~ *斜*\n```\n"
	want := "小标题\n**粗体** 斜体 *italic* 删除  `*代码*`\n```\n~~原样~~ *斜*\n```\n"
	if got := FilterMarkdown(in); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestChunk(t *testing.T) {
	if Chunk("", 0) != nil {
		t.Error("empty text has chunks")
	}
	text := strings.Repeat("一二三四五 ", 30) + "\n```\n" + strings.Repeat("code line\n", 30) + "```\nend"
	chunks := Chunk(text, 100)
	if len(chunks) < 3 {
		t.Fatalf("%d chunks", len(chunks))
	}
	var joined []string
	for i, c := range chunks {
		if n := len([]rune(c)); n > 100 {
			t.Errorf("chunk %d has %d code points", i, n)
		}
		if fences(c)%2 != 0 {
			t.Errorf("chunk %d leaves a fence open: %q", i, c)
		}
		joined = append(joined, c)
	}
	if !strings.Contains(strings.Join(joined, ""), "end") {
		t.Error("lost the end")
	}
}
