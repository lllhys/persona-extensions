package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

var loc = time.FixedZone("UTC+8", 8*3600)

func nowAt() time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15", "2026-11-13 12", loc)
	return t
}

func fixture(t *testing.T) (*store.Store, *budget.File) {
	t.Helper()
	st := store.Open("")
	at := func(hour int) int64 { return nowAt().Add(time.Duration(hour-12) * time.Hour).Unix() }
	if _, err := st.Merge("m-a", []store.Bucket{
		{Minute: at(9), Model: "alpha", Launcher: "cli", Input: 1000, Requests: 1},
		{Minute: at(10), Model: "beta", Input: 300, Requests: 1},
	}, loc); err != nil {
		t.Fatal(err)
	}
	return st, budget.Open("")
}

// call runs one request through a fresh server and returns the decoded answer.
func call(t *testing.T, st *store.Store, bf *budget.File, line string) map[string]any {
	t.Helper()
	var out strings.Builder
	err := Serve(context.Background(), strings.NewReader(line), &out, st, bf, loc, nowAt)
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatalf("answer %q: %v", out.String(), err)
	}
	return res
}

func toolText(t *testing.T, res map[string]any) string {
	t.Helper()
	result, _ := res["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %+v", res)
	}
	first, _ := content[0].(map[string]any)
	return first["text"].(string)
}

func TestSummaryAndTop(t *testing.T) {
	st, bf := fixture(t)
	res := call(t, st, bf, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"usage_summary","arguments":{"range":"today"}}}`+"\n")
	text := toolText(t, res)
	if !strings.Contains(text, "\"in\":1300") && !strings.Contains(text, "1300") {
		t.Fatalf("summary does not total 1300: %s", text)
	}

	res = call(t, st, bf, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"usage_top","arguments":{"range":"today","dim":"model","n":1}}}`+"\n")
	text = toolText(t, res)
	if !strings.Contains(text, "alpha") || strings.Contains(text, "beta") {
		t.Fatalf("top 1 should be alpha only: %s", text)
	}
}

func TestBudgetToolsRoundTrip(t *testing.T) {
	st, bf := fixture(t)
	res := call(t, st, bf, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"budget_set","arguments":{"model":"alpha","span":"day","limit":10000}}}`+"\n")
	if strings.Contains(toolText(t, res), "true") && res["result"].(map[string]any)["isError"] == true {
		t.Fatalf("set failed: %s", toolText(t, res))
	}
	res = call(t, st, bf, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"budget_status","arguments":{}}}`+"\n")
	if !strings.Contains(toolText(t, res), "alpha") {
		t.Fatalf("status lost the budget: %s", toolText(t, res))
	}
	_ = call(t, st, bf, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"budget_delete","arguments":{"model":"alpha","span":"day"}}}`+"\n")
	res = call(t, st, bf, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"budget_status","arguments":{}}}`+"\n")
	if strings.Contains(toolText(t, res), "alpha") {
		t.Fatalf("the budget survived deletion: %s", toolText(t, res))
	}
}

func TestReadToolsDoNotWrite(t *testing.T) {
	var reads, writes []string
	for _, tool := range Tools() {
		if tool.Read {
			reads = append(reads, tool.Name)
		} else {
			writes = append(writes, tool.Name)
		}
	}
	if strings.Join(reads, ",") != "usage_summary,usage_top,budget_status" {
		t.Fatalf("read tools %v", reads)
	}
	if strings.Join(writes, ",") != "budget_set,budget_delete" {
		t.Fatalf("write tools %v", writes)
	}
}

func TestUnknownToolIsAnError(t *testing.T) {
	st, bf := fixture(t)
	res := call(t, st, bf, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`+"\n")
	result := res["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an unknown tool was not an error: %+v", result)
	}
}
