package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPullSplitsByModelAndLauncher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		model, launcher := q.Get("model"), q.Get("launcher")
		switch {
		case model == "" && launcher == "":
			write(w, map[string]any{"usage": map[string]any{
				"models":    []Share{{Name: "alpha", In: 100}, {Name: "beta", In: 40}},
				"launchers": []Share{{Name: "cli", In: 100}, {Name: "-", In: 40}},
			}})
		case model == "alpha" && launcher == "cli":
			write(w, series(600, 60, 100, 0))
		case model == "alpha" && launcher == "-":
			write(w, series(600, 60, 0, 0))
		case model == "beta" && launcher == "cli":
			write(w, series(600, 60, 0, 0))
		case model == "beta" && launcher == "-":
			write(w, series(600, 600, 40, 0))
		default:
			http.Error(w, "unexpected "+r.URL.RawQuery, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	got, err := New(srv.URL, "tok", nil).Pull(context.Background(), "m-a", "1h")
	if err != nil {
		t.Fatal(err)
	}
	// The empty pairs are dropped. beta's point covers 10 minutes, so it becomes 10 buckets summing to 40.
	sums := map[string]int64{}
	for _, b := range got {
		if b.Model == "alpha" && b.Launcher != "cli" || b.Model == "beta" && b.Launcher != "" {
			t.Errorf("an empty pair was kept: %+v", b)
		}
		sums[b.Model+"|"+b.Launcher] += b.Total()
	}
	if sums["alpha|cli"] != 100 || sums["beta|"] != 40 || len(sums) != 2 {
		t.Fatalf("sums %v, want alpha|cli 100 and beta| 40", sums)
	}
}

func TestBucketsSpreadAPointOverItsMinutes(t *testing.T) {
	s := Series{Step: 600, Points: []Point{{T: 1000, Step: 600, In: 605, Out: 3, Req: 2}}}
	got := bucketsOf(s, "alpha", "cli")
	if len(got) != 10 {
		t.Fatalf("got %d minutes, want 10", len(got))
	}
	var in, out int64
	var req int
	for i, b := range got {
		in += b.Input
		out += b.Output
		req += b.Requests
		if b.Minute != int64(1000+i*60) || b.Model != "alpha" || b.Launcher != "cli" {
			t.Errorf("minute %d: %+v", i, b)
		}
	}
	if in != 605 || out != 3 || req != 2 {
		t.Fatalf("minutes sum to in %d out %d req %d, want 605, 3, 2", in, out, req)
	}
	if got[0].Input != 65 { // 605 = 65 + 9*60
		t.Fatalf("first minute keeps the remainder: %d, want 65", got[0].Input)
	}
}

func TestGatewayAccountsHiddenWhenUnavailable(t *testing.T) {
	c := New("http://example.invalid", "tok", func(ctx context.Context, method string, out any) error {
		return errNope
	})
	if _, ok := c.GatewayAccounts(context.Background()); ok {
		t.Fatal("a failing method was shown")
	}
	ok := New("http://example.invalid", "tok", func(ctx context.Context, method string, out any) error {
		*(out.(*GatewayAccounts)) = GatewayAccounts{Accounts: []GatewayAccount{{Name: "one"}}}
		return nil
	})
	got, shown := ok.GatewayAccounts(context.Background())
	if !shown || len(got.Accounts) != 1 {
		t.Fatalf("shown %v accounts %+v", shown, got.Accounts)
	}
}

var errNope = &nopError{}

type nopError struct{}

func (nopError) Error() string { return "nope" }

func series(t, step int64, in, out int64) map[string]any {
	return map[string]any{"usage": Series{Step: int(step), Points: []Point{{T: t, In: in, Out: out}}}}
}

func write(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}
