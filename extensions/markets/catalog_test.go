package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The catalog lists the same tools as tools.list under the source the hub derived before (markets: quote, search,
// candles); search answers JSON a subscription can follow instrument by instrument, the others their text, and
// anything else is refused unread.
func TestCatalog(t *testing.T) {
	f := newFakeOKX(t)
	p, _ := newTestPlugin(t, f, `{"watchlist": ["BTC-USDT"]}`)
	ctx := context.Background()

	res, err := p.cCatalogList(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	if err := wire.CheckConnectorCatalogJSON(raw); err != nil {
		t.Fatalf("catalog %s: %v", raw, err)
	}
	for _, want := range []string{`"id":"markets"`, `"name":"quote"`, `"name":"search"`, `"name":"candles"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("no %s in %s", want, raw)
		}
	}

	call := func(params string) (string, error) {
		t.Helper()
		res, err := p.cCatalogCall(ctx, nil, json.RawMessage(params))
		if err != nil {
			return "", err
		}
		return res.(map[string]any)["text"].(string), nil
	}
	text, err := call(`{"contribution":"markets","source":"markets","tool":"search","args":{"query":"btc","limit":2}}`)
	if err != nil {
		t.Fatal(err)
	}
	var found struct {
		Query       string
		Instruments []struct {
			ID, Title, Symbol, Body string
			Last                    float64
			Change                  *float64
		}
	}
	if err := json.Unmarshal([]byte(text), &found); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if found.Query != "btc" || len(found.Instruments) != 2 {
		t.Fatalf("search: %s", text)
	}
	for _, in := range found.Instruments {
		if !strings.HasPrefix(in.ID, "okx:BTC-") || in.Title == "" || in.Last == 0 || in.Change == nil || !strings.HasPrefix(in.Body, in.ID) {
			t.Errorf("an instrument: %+v", in)
		}
	}
	if _, err := call(`{"source":"markets","tool":"search","args":{"query":" "}}`); err == nil {
		t.Error("an empty query")
	}
	if text, err = call(`{"source":"markets","tool":"quote","args":{"symbols":["BTC-USDT"]}}`); err != nil || !strings.HasPrefix(text, "行情，") {
		t.Fatalf("quote: %s %v", text, err)
	}
	for _, bad := range []string{`{"source":"markets","tool":"alert"}`, `{"source":"other","tool":"search"}`} {
		var e *pluginkit.Error
		if _, err := call(bad); !errors.As(err, &e) || e.Code != "not_read_only" {
			t.Errorf("%s: %v", bad, err)
		}
	}

	// The assistant still reads the text.
	res, err = p.cToolsCall(ctx, nil, json.RawMessage(`{"name":"markets_search","args":{"query":"btc","limit":2}}`))
	if err != nil || !strings.HasPrefix(res.(map[string]any)["text"].(string), "和「btc」对得上的 2 个") {
		t.Fatalf("tools.call: %v %v", res, err)
	}
}
