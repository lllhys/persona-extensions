package sync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/hub"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

// served records what the fake hub was asked and the notifications it got.
type served struct {
	mu     sync.Mutex
	ranges []string
	notes  []string
}

func (s *served) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/machines":
		write(w, map[string]any{"machines": []hub.Machine{{ID: "m-a", Name: "电脑 A", Online: true}}})
	case strings.HasPrefix(r.URL.Path, "/api/v1/machines/"):
		s.mu.Lock()
		s.ranges = append(s.ranges, r.URL.Query().Get("range")+" "+r.URL.Query().Get("model"))
		s.mu.Unlock()
		if r.URL.Query().Get("model") == "" {
			write(w, map[string]any{"usage": map[string]any{
				"models": []hub.Share{{Name: "alpha", In: 100}}, "launchers": []hub.Share{{Name: "cli"}},
			}})
			return
		}
		write(w, map[string]any{"usage": hub.Series{Step: 60, Points: []hub.Point{{T: time.Now().Unix() / 60 * 60, In: 100}}}})
	case r.URL.Path == "/api/v1/notify":
		var body struct{ Title string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.notes = append(s.notes, body.Title)
		s.mu.Unlock()
		write(w, map[string]any{"sent": true})
	default:
		http.NotFound(w, r)
	}
}

func write(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func TestLoopBackfillsThenPollsAndAlerts(t *testing.T) {
	var s served
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	defer srv.Close()

	st := store.Open("")
	bf := budget.Open("")
	if err := bf.Set(budget.Budget{Span: budget.Daily, Limit: 50}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, hub.New(srv.URL, "tok", nil), st, bf, time.UTC, 20*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.ranges)
		s.mu.Unlock()
		if n >= 4 { // two pulls, each an unfiltered read plus one filtered read
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ranges) < 2 || !strings.HasPrefix(s.ranges[0], "7d") {
		t.Fatalf("ranges %v, the first pull should ask for 7d", s.ranges)
	}
	polled := false
	for _, r := range s.ranges[2:] {
		polled = polled || strings.HasPrefix(r, "24h")
	}
	if !polled {
		t.Fatalf("no 24h pull after the backfill: %v", s.ranges)
	}
	var total int64
	_ = st.View(func(st *store.State) error {
		for _, d := range st.Days {
			for _, b := range d.Buckets {
				total += b.Total()
			}
		}
		return nil
	})
	if total == 0 {
		t.Fatal("nothing was stored")
	}
	if len(s.notes) == 0 {
		t.Fatal("the budget was crossed but no notification was sent")
	}
}

func TestLoopStopsWithoutAHub(t *testing.T) {
	done := make(chan struct{})
	go func() {
		Loop(context.Background(), hub.New("", "", nil), store.Open(""), budget.Open(""), time.UTC, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the loop ran without a hub address")
	}
}

// Keep io imported for the request body drain pattern used by other tests of this module.
var _ = io.Discard
