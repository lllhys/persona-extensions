package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

const testToken = "synthetic-monday-token"

type fakeMonday struct {
	srv        *httptest.Server
	mu         sync.Mutex
	queries    []string
	creates    int
	updates    int
	failWrite  bool
	badGraphQL bool
}

func sampleBoard() board {
	return board{ID: 7, Name: "Reading", URL: "https://example.monday.com/boards/7", Columns: []column{{ID: "status", Title: "Status", Type: "status", SettingsStr: `{"labels":{"0":"To Do","1":"In Progress","2":"Done"}}`}}}
}

func sampleItem() item {
	item := item{ID: 42, Name: "Review reading notes", URL: "https://example.monday.com/items/42", UpdatedAt: "2026-11-10T10:00:00Z"}
	item.ColumnValues = []struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Value string `json:"value"`
	}{{ID: "status", Text: "To Do", Value: `{"label":"To Do"}`}}
	return item
}

func newFakeMonday(t *testing.T) *fakeMonday {
	f := &fakeMonday{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.queries = append(f.queries, in.Query)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f.badGraphQL {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}, "errors": []map[string]string{{"message": "synthetic application error"}}})
			return
		}
		switch {
		case strings.Contains(in.Query, "me { id name }"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": mePage{Me: user{ID: 42, Name: "Reader"}}})
		case strings.Contains(in.Query, "boards { id name url }"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": boardPage{Boards: []board{sampleBoard()}}})
		case strings.Contains(in.Query, "boards(ids: [7])") && !strings.Contains(in.Query, "items_page"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": boardPage{Boards: []board{sampleBoard()}}})
		case strings.Contains(in.Query, "items_page"):
			page := itemsPage{Cursor: "next-cursor", Items: []item{sampleItem()}}
			if strings.Contains(in.Query, "cursor:") {
				page.Cursor = ""
				page.Items = nil
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"boards": []map[string]any{{"items_page": page}}}})
		case strings.Contains(in.Query, "create_item"):
			f.mu.Lock()
			fail := f.failWrite
			f.mu.Unlock()
			if fail {
				http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
				return
			}
			f.mu.Lock()
			f.creates++
			f.mu.Unlock()
			item := sampleItem()
			item.ID, item.Name = 43, "Created item"
			_ = json.NewEncoder(w).Encode(map[string]any{"data": createPage{CreateItem: item}})
		case strings.Contains(in.Query, "change_multiple_column_values"):
			f.mu.Lock()
			f.updates++
			f.mu.Unlock()
			item := sampleItem()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": updatePage{ChangeMultipleColumnValues: item}})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMonday) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeMonday(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "monday" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("create and status must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{"api.monday.com"}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestBoardsItemsCursorAndGraphQLErrors(t *testing.T) {
	f := newFakeMonday(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "monday_boards", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "Board #7") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "monday_items", json.RawMessage(`{"board_id":7,"cursor":"next-cursor"}`))
	if err != nil || !strings.Contains(text, "No items") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "monday_board", json.RawMessage(`{"board_id":7}`))
	if err != nil || !strings.Contains(text, "In Progress") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	queries := strings.Join(f.queries, "\n")
	f.mu.Unlock()
	for _, want := range []string{"items_page(limit: 50", "cursor: \"next-cursor\"", "column_values { id text value }"} {
		if !strings.Contains(queries, want) {
			t.Fatalf("missing %q in %s", want, queries)
		}
	}
	f.badGraphQL = true
	if _, err := a.callTool(t.Context(), "monday_boards", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "synthetic application error") {
		t.Fatal("GraphQL error was treated as success")
	}
}

func TestCreateStatusReplayAndExistingLabel(t *testing.T) {
	f := newFakeMonday(t)
	a := f.app()
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "monday_create_item", []byte(`{"board_id":7,"name":"Keep reading context"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "status-1", "monday_update_status", []byte(`{"board_id":7,"item_id":42,"column_id":"status","label":"Done"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if f.creates != 1 || f.updates != 1 {
		t.Fatalf("%d %d", f.creates, f.updates)
	}
	if _, err := a.doAction(t.Context(), "status-1", "monday_update_status", []byte(`{"board_id":7,"item_id":42,"column_id":"status","label":"In Progress"}`)); err == nil {
		t.Fatal("receipt accepted another status")
	}
	if _, err := a.doAction(t.Context(), "bad-label", "monday_update_status", []byte(`{"board_id":7,"item_id":42,"column_id":"status","label":"New Label"}`)); err == nil {
		t.Fatal("created a new label instead of selecting an existing one")
	}
	if f.updates != 1 {
		t.Fatal("invalid label reached GraphQL")
	}
	if _, err := describeAction("monday_create_item", []byte(`{"board_id":7,"name":"Keep context"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeMonday(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"board_id":7,"item_id":42,"column_id":"status","label":"Done"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "monday_update_status", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "monday_update_status", args); err != nil {
		t.Fatal(err)
	}
	if f.updates != 1 {
		t.Fatal("restart repeated status")
	}
	key, _ := json.Marshal(statusArgs{BoardID: 7, ItemID: 42, ColumnID: "status", Label: "Done"})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "monday_update_status", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown status was retried")
	}
	if f.updates != 1 {
		t.Fatal("unknown status caused another write")
	}
}

func TestLocalizedViewsAndEditFlow(t *testing.T) {
	f := newFakeMonday(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "items", "detail", "create", "review-create", "edit", "review-edit"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, BoardID: 7, BoardName: "Reading", ItemID: 42, DraftID: "draft-1", Name: "Keep context", ColumnID: "status", Label: "Done"}
				v, err := a.page(t.Context(), r, st)
				if err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
				if err := wire.CheckView(v); err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
			}
		}
	}
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"column_id": "status", "label": "Done"}}}
	result, err := a.action(t.Context(), withAction(r, "review-edit", pageState{Mode: "edit", BoardID: 7, ItemID: 42, DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-edit", pageState{Mode: "review-edit", BoardID: 7, ItemID: 42, DraftID: "draft-1", ColumnID: "status", Label: "Done"}))
	if err != nil || !result.Pop || f.updates != 1 {
		t.Fatalf("confirm: %v %+v %d", err, result, f.updates)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestNoConfigAndFailedFinalWrite(t *testing.T) {
	a := newApp()
	if a.status()["state"] != "not_configured" {
		t.Fatal(a.status())
	}
	v, err := a.render(t.Context(), &ext.UIRequest{})
	if err != nil || !strings.Contains(v.Sections[0].Items[0].Title, "API token") {
		t.Fatalf("%v %+v", err, v)
	}
	f := newFakeMonday(t)
	f.failWrite = true
	a = f.app()
	if _, err := a.doAction(t.Context(), "network-1", "monday_create_item", []byte(`{"board_id":7,"name":"Keep context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if f.creates != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
