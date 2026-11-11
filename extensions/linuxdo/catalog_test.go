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

// The catalog lists the same tools as tools.list under the source the hub derived before (linuxdo: hot, topic); hot
// answers JSON a subscription can follow topic by topic, topic its text, and anything else is refused unread.
func TestCatalog(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	p := testPlugin(t, f, map[string]any{"categories": []string{"开发调优", "福利羊毛"}})
	ctx := context.Background()

	res, err := p.cCatalogList(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	if err := wire.CheckConnectorCatalogJSON(raw); err != nil {
		t.Fatalf("catalog %s: %v", raw, err)
	}
	if !strings.Contains(string(raw), `"id":"linuxdo"`) || !strings.Contains(string(raw), `"name":"hot"`) || !strings.Contains(string(raw), `"name":"topic"`) {
		t.Fatalf("catalog %s", raw)
	}

	call := func(params string) (string, error) {
		t.Helper()
		res, err := p.cCatalogCall(ctx, nil, json.RawMessage(params))
		if err != nil {
			return "", err
		}
		return res.(map[string]any)["text"].(string), nil
	}
	text, err := call(`{"contribution":"linuxdo","source":"linuxdo","tool":"hot","args":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	var hot struct {
		Total, Kept int
		More        bool
		Topics      []struct {
			ID                        int64
			Title, Author, Time, Link string
		}
	}
	if err := json.Unmarshal([]byte(text), &hot); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if hot.Total != 5 || hot.Kept != 2 || hot.More || len(hot.Topics) != 2 || hot.Topics[0].ID != 1001 || hot.Topics[0].Title == "" ||
		hot.Topics[0].Author != "alice" || hot.Topics[0].Time == "" || hot.Topics[1].Link != "https://linux.do/t/topic/1004" {
		t.Fatalf("hot: %s", text)
	}
	if text, err = call(`{"source":"linuxdo","tool":"hot","args":{"filtered":false,"limit":1}}`); err != nil || !strings.Contains(text, `"more":true`) {
		t.Fatalf("one of all: %s %v", text, err)
	}
	if text, err = call(`{"source":"linuxdo","tool":"topic","args":{"topic":"1001","replies":1}}`); err != nil || !strings.HasPrefix(text, "「Claude 新模型实测 & 对比」") {
		t.Fatalf("topic: %s %v", text, err)
	}
	for _, bad := range []string{`{"source":"linuxdo","tool":"send"}`, `{"source":"other","tool":"hot"}`} {
		var e *pluginkit.Error
		if _, err := call(bad); !errors.As(err, &e) || e.Code != "not_read_only" {
			t.Errorf("%s: %v", bad, err)
		}
	}

	// The assistant still reads the text.
	res, err = p.cToolsCall(ctx, nil, json.RawMessage(`{"name":"linuxdo_hot","args":{}}`))
	if err != nil || !strings.HasPrefix(res.(map[string]any)["text"].(string), "LINUX DO 热门：5 条") {
		t.Fatalf("tools.call: %v %v", res, err)
	}
}
