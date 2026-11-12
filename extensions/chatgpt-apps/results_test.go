package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// longList is a synthetic mail listing of n messages, each with a full body of about 6 KB (with HTML in it, as mail
// bodies have), the way a mail app's list tool answers.
func longList(n int) []byte {
	body := strings.Repeat(`<p>Synthetic paragraph &amp; "quoted" text, line after line.</p>`+"\n", 96)
	var msgs []map[string]any
	for i := range n {
		msgs = append(msgs, map[string]any{
			"id":               fmt.Sprintf("msg-%03d", i),
			"subject":          fmt.Sprintf("Synthetic message %d", i),
			"receivedDateTime": fmt.Sprintf("2026-01-02T%02d:%02d:00Z", i/60%24, i%60),
			"bodyPreview":      "Synthetic paragraph",
			"body":             map[string]any{"contentType": "html", "content": body},
			"sender":           map[string]any{"emailAddress": map[string]any{"name": fmt.Sprintf("Sender %d", i), "address": "sender@example.com"}},
		})
	}
	raw, _ := json.Marshal(map[string]any{"value": msgs})
	return raw
}

// A list longer than the 200 KB that used to be the most comes back byte for byte, still valid JSON with every item,
// whether the app answers it as structured content or as one text.
func TestALongResultComesBackWhole(t *testing.T) {
	big := longList(60)
	if len(big) <= 300_000 {
		t.Fatalf("the synthetic list is only %d bytes", len(big))
	}
	for _, as := range []string{"structured", "text"} {
		t.Run(as, func(t *testing.T) {
			f := newFakeChatGPT(t)
			result := map[string]any{"structuredContent": json.RawMessage(big),
				"content": []any{map[string]any{"type": "text", "text": "a summary the structured content replaces"}}}
			if as == "text" {
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(big)}}}
			}
			f.mu.Lock()
			f.results = map[string]any{"example_mail.search_messages": result}
			f.mu.Unlock()
			h := start(t, f, "")
			h.signIn()
			text, err := h.tool("chatgpt_read", map[string]any{"name": "example_mail.search_messages", "arguments": map[string]any{"query": "x"}})
			if err != nil {
				t.Fatal(err)
			}
			if text != string(big) {
				t.Fatalf("got %d bytes, want the %d the app answered; ends %q", len(text), len(big), text[max(0, len(text)-80):])
			}
			var got struct {
				Value []struct {
					ID   string `json:"id"`
					Body struct {
						Content string `json:"content"`
					} `json:"body"`
				} `json:"value"`
			}
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("not JSON: %v", err)
			}
			if len(got.Value) != 60 || got.Value[59].ID != "msg-059" || !strings.HasSuffix(got.Value[59].Body.Content, "line.</p>\n") {
				t.Fatalf("%d items: %+v", len(got.Value), got.Value)
			}
		})
	}
}

// The extension's own JSON answers are not cut either.
func TestALongAnswerIsNotCut(t *testing.T) {
	long := strings.Repeat("Synthetic skill text <with> \"quotes\".\n", 10_000)
	res, err := answer(map[string]any{"text": long})
	if err != nil {
		t.Fatal(err)
	}
	var back struct{ Text string }
	if err := json.Unmarshal([]byte(res.Text), &back); err != nil || back.Text != long {
		t.Fatalf("%d bytes back, decode %v", len(back.Text), err)
	}
}

// An answer from ChatGPT over the most read is an error that says so, not a cut that fails to decode.
func TestAnAnswerOverTheMostReadIsAnError(t *testing.T) {
	f := newFakeChatGPT(t)
	f.mu.Lock()
	f.results = map[string]any{"example_mail.search_messages": map[string]any{"structuredContent": json.RawMessage(longList(60))}}
	f.mu.Unlock()
	h := startTuned(t, f, "", func(a *App) { a.answerMost = 256 << 10 })
	h.signIn()
	_, err := h.tool("chatgpt_read", map[string]any{"name": "example_mail.search_messages", "arguments": map[string]any{"query": "x"}})
	if err == nil || !strings.Contains(err.Error(), "is over 256 KB") || !strings.Contains(err.Error(), "ask for less") {
		t.Fatalf("err %v", err)
	}
	if calls := f.toolCalls(); len(calls) != 1 {
		t.Fatalf("calls: %+v", calls)
	}
	// What fits is still read whole.
	f.mu.Lock()
	f.results = map[string]any{"example_mail.search_messages": map[string]any{"structuredContent": json.RawMessage(longList(20))}}
	f.mu.Unlock()
	if text, err := h.tool("chatgpt_read", map[string]any{"name": "example_mail.search_messages", "arguments": map[string]any{"query": "x"}}); err != nil || !json.Valid([]byte(text)) {
		t.Fatalf("%d bytes, %v", len(text), err)
	}
}

// A result the hub could not take in one answer (its line limit) is refused, not sent; anything under passes as it is.
func TestWholeRefusesOnlyWhatTheHubCannotTake(t *testing.T) {
	ctx := context.Background()
	pass := func(text string) func(context.Context, struct{}) (wire.ConnectorCallResult, error) {
		return func(context.Context, struct{}) (wire.ConnectorCallResult, error) {
			return wire.ConnectorCallResult{Text: text}, nil
		}
	}
	n := maxResult/6 + 1
	plain := strings.Repeat("x", n) // a few bytes more once encoded: fits
	res, err := whole(pass(plain))(ctx, struct{}{})
	if err != nil || res.Text != plain {
		t.Fatalf("plain: %d bytes, %v", len(res.Text), err)
	}
	escaped := strings.Repeat("<", n) // six times as long once encoded: does not fit
	res, err = whole(pass(escaped))(ctx, struct{}{})
	if err == nil || res.Text != "" || !strings.Contains(err.Error(), "not passed on") || !strings.Contains(err.Error(), "48 MB") {
		t.Fatalf("escaped: %d bytes, %v", len(res.Text), err)
	}
}
