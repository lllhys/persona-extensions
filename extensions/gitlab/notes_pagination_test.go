package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestMergeRequestNotesFollowPages(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total       int
		systemFirst bool
		wantNext    string
	}{
		{"101 ordinary notes", 101, false, ""},
		{"100 system notes then a human comment", 101, true, ""},
		{"bounded response names continuation", 501, false, "next notes page: 6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/projects/42/merge_requests/7":
					_ = json.NewEncoder(w).Encode(mergeRequest{ID: 700, IID: 7, ProjectID: 42, UserNotesCount: 1})
				case "/projects/42/merge_requests/7/notes":
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					if page == 0 {
						page = 1
					}
					start := (page - 1) * 100
					end := min(start+100, tc.total)
					var notes []note
					for i := start; i < end; i++ {
						body := fmt.Sprintf("note-%d", i+1)
						if i == tc.total-1 {
							body = "LATEST BLOCKER"
						}
						notes = append(notes, note{ID: int64(i + 1), Body: body, System: tc.systemFirst && i < 100})
					}
					if end < tc.total {
						w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
					}
					_ = json.NewEncoder(w).Encode(notes)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			a := &app{client: newClient(srv.URL, testToken), receipts: newWriteStore("")}
			text, err := a.callTool(t.Context(), "gitlab_merge_request", []byte(`{"project":"42","iid":7}`))
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantNext == "" && !strings.Contains(text, "LATEST BLOCKER") {
				t.Fatalf("later discussion is missing: %s", text)
			}
			if tc.wantNext != "" && !strings.Contains(text, tc.wantNext) {
				t.Fatalf("truncation has no continuation: %s", text)
			}
		})
	}
}
