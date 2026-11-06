package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCacheKeepsAListForAWhile(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	ctx := context.Background()
	for range 3 {
		l, err := a.list(ctx, "hot", false)
		if err != nil || len(l.topics) != 3 {
			t.Fatalf("hot: %d topics, %v", len(l.topics), err)
		}
	}
	if n := f.hit("/topics/hot.json"); n != 1 {
		t.Fatalf("three reads within listFor asked the site %d times", n)
	}

	set(t, &listFor, time.Nanosecond)
	if _, err := a.list(ctx, "hot", false); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/topics/hot.json"); n != 2 {
		t.Fatalf("a read after listFor asked the site %d times in all", n)
	}
}

func TestRefreshWaitsForTheFloor(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	ctx := context.Background()
	set(t, &refreshFloor, 30*time.Second)
	for range 3 {
		if _, err := a.list(ctx, "hot", true); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.hit("/topics/hot.json"); n != 1 {
		t.Fatalf("refreshes within refreshFloor asked the site %d times", n)
	}
	set(t, &refreshFloor, 0)
	if _, err := a.list(ctx, "hot", true); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/topics/hot.json"); n != 2 {
		t.Fatalf("a refresh after the floor asked the site %d times in all", n)
	}
}

func TestRequestsArePacedAndNamed(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	set(t, &requestGap, 80*time.Millisecond)
	ctx := context.Background()
	for _, id := range []string{"hot", "latest"} {
		if _, err := a.list(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.thread(ctx, 1001, true, false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.times) != 4 {
		t.Fatalf("%d requests, want 4", len(f.times))
	}
	for i := 1; i < len(f.times); i++ {
		if gap := f.times[i].Sub(f.times[i-1]); gap < 70*time.Millisecond {
			t.Fatalf("request %d came %v after the one before", i, gap)
		}
	}
	for _, ua := range f.agents {
		if ua != "persona-v2ex/"+about.Version || about.Version == "" {
			t.Fatalf("User-Agent %q", ua)
		}
	}
}

func TestWaitingForTheGateEndsWithTheCaller(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	s := newSite(f.srv.URL)
	s.gate <- struct{}{} // another request holds it
	defer func() { <-s.gate }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.get(ctx, "/topics/hot.json", listFor); !errors.Is(err, errSlow) {
		t.Fatalf("deadline while waiting: %v, want errSlow", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := s.get(ctx, "/topics/hot.json", listFor); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled while waiting: %v, want context.Canceled", err)
	}
	if f.total() != 0 {
		t.Fatalf("%d requests went out without the gate", f.total())
	}
}

func TestTheSameReadTogetherIsOneRequest(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	f.setDelay(50 * time.Millisecond)
	a := testApp(t, f, "")
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			l, err := a.list(context.Background(), "hot", false)
			if err == nil && len(l.topics) != 3 {
				err = errors.New("not the list")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.hit("/topics/hot.json"); n != 1 {
		t.Fatalf("five pages reading hot together asked the site %d times", n)
	}
}

func TestAFailedReadShowsTheOlderCopy(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer answer
		want   error
	}{
		{"server error", answer{status: http.StatusBadGateway, body: "<html>502</html>"}, nil},
		{"firewall", answer{status: http.StatusForbidden, body: "<html><title>Just a moment...</title></html>"}, errChallenged},
		{"firewall in a 200", answer{body: "<!DOCTYPE html><title>Just a moment...</title>"}, errChallenged},
		{"a page in a 200", answer{body: "<html>maintenance</html>"}, errBadAnswer},
		{"broken JSON", answer{body: `[{"id": 1001, "title": `}, errBadAnswer},
		{"an object", answer{body: `{"status": "ok"}`}, errBadAnswer},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeSite(t)
			sample(f)
			a := testApp(t, f, "")
			ctx := context.Background()
			first, err := a.list(ctx, "hot", false)
			if err != nil {
				t.Fatal(err)
			}
			f.put("/topics/hot.json", c.answer)
			l, err := a.list(ctx, "hot", true)
			if err != nil {
				t.Fatalf("a failed refresh with a copy: %v", err)
			}
			if l.stale == nil || (c.want != nil && !errors.Is(l.stale, c.want)) {
				t.Fatalf("stale %v, want %v", l.stale, c.want)
			}
			if c.want == nil && !strings.Contains(l.stale.Error(), "502") {
				t.Fatalf("stale %v, want the status", l.stale)
			}
			if !l.at.Equal(first.at) || len(l.topics) != len(first.topics) {
				t.Fatalf("the copy: %v %d, want %v %d", l.at, len(l.topics), first.at, len(first.topics))
			}
			st := a.status()
			if st.State != "error" || st.Error == "" {
				t.Fatalf("status after a stale copy: %+v", st)
			}
			if (len(st.Guide) > 0) != errors.Is(c.want, errChallenged) {
				t.Fatalf("guide %v for %v", st.Guide, c.want)
			}

			// The bad answer did not replace the good one.
			f.serve("/topics/hot.json", topicsJSON(goTopic))
			l, err = a.list(ctx, "hot", true)
			if err != nil || l.stale != nil || len(l.topics) != 1 {
				t.Fatalf("after the site is back: %d topics, stale %v, %v", len(l.topics), l.stale, err)
			}
			if st := a.status(); st.State != "ok" || st.LastSync == nil || !st.LastSync.Equal(l.at.UTC()) {
				t.Fatalf("status after the site is back: %+v", st)
			}
		})
	}
}

func TestAMissingThingHasNoOlderCopy(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	ctx := context.Background()
	if _, err := a.list(ctx, "hot", false); err != nil {
		t.Fatal(err)
	}
	f.put("/topics/hot.json", answer{status: http.StatusNotFound, body: notFound})
	if _, err := a.list(ctx, "hot", true); !errors.Is(err, errNotFound) {
		t.Fatalf("a 404 after a copy: %v, want errNotFound", err)
	}
	if st := a.status(); st.State != "ok" {
		t.Fatalf("a 404 is not the connector's state: %+v", st)
	}
}

func TestTooManyRequestsPauses(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	ctx := context.Background()
	if _, err := a.list(ctx, "hot", false); err != nil {
		t.Fatal(err)
	}
	f.answerAll(&answer{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "120"}})
	l, err := a.list(ctx, "hot", true)
	if err != nil || !errors.Is(l.stale, errRateLimited) || len(l.topics) != 3 {
		t.Fatalf("429 with a copy: %d topics, stale %v, %v", len(l.topics), l.stale, err)
	}
	if left := time.Until(a.site.paused); left < 110*time.Second || left > 121*time.Second {
		t.Fatalf("paused for %v, want Retry-After's 2 minutes", left)
	}
	if st := a.status(); st.State != "rate_limited" || st.Error == "" {
		t.Fatalf("status while paused: %+v", st)
	}

	f.answerAll(nil)
	before := f.total()
	if _, err := a.list(ctx, "latest", false); !errors.Is(err, errRateLimited) {
		t.Fatalf("a read while paused: %v, want errRateLimited", err)
	}
	if f.total() != before {
		t.Fatal("a request went out while paused")
	}
}

func TestLimitPause(t *testing.T) {
	soon := time.Now().Add(20 * time.Minute).Unix()
	late := time.Now().Add(5 * time.Hour).Unix()
	past := time.Now().Add(-time.Minute).Unix()
	for _, c := range []struct {
		name       string
		retryAfter string
		resetAt    int64
		want       time.Duration
	}{
		{"nothing said", "", 0, pauseFor},
		{"Retry-After", "120", soon, 2 * time.Minute},
		{"a short Retry-After", "5", 0, time.Minute},
		{"a long Retry-After", "86400", 0, pauseMost},
		{"Retry-After as a date", "Wed, 21 Oct 2026 07:28:00 GMT", 0, pauseFor},
		{"the reset", "", soon, 20 * time.Minute},
		{"a far reset", "", late, pauseMost},
		{"a past reset", "", past, pauseFor},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.retryAfter != "" {
				h.Set("Retry-After", c.retryAfter)
			}
			if got := limitPause(h, c.resetAt); got < c.want-2*time.Second || got > c.want {
				t.Fatalf("limitPause = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNoteQuota(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(15*time.Minute).Unix(), 10)
	for _, c := range []struct {
		name               string
		cache, left, reset string
		want               time.Duration // 0: no pause
	}{
		{"from the site, low", "", "5", reset, 15 * time.Minute},
		{"a miss, low", "MISS", "19", reset, 15 * time.Minute},
		{"dynamic, low", "DYNAMIC", "0", reset, 15 * time.Minute},
		{"expired, low", "EXPIRED", "3", reset, 15 * time.Minute},
		{"bypass, low", "BYPASS", "3", reset, 15 * time.Minute},
		{"from the CDN's cache", "HIT", "1", reset, 0},
		{"stale in the CDN", "STALE", "1", reset, 0},
		{"enough left", "", "20", reset, 0},
		{"no numbers", "", "", "", 0},
		{"no reset", "", "3", "", 0},
		{"a past reset", "", "3", strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10), 0},
		{"a far reset", "", "3", strconv.FormatInt(time.Now().Add(3*time.Hour).Unix(), 10), pauseMost},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSite("")
			h := http.Header{}
			for k, v := range map[string]string{"Cf-Cache-Status": c.cache, "X-Rate-Limit-Remaining": c.left, "X-Rate-Limit-Reset": c.reset} {
				if v != "" {
					h.Set(k, v)
				}
			}
			s.noteQuota(h)
			left := time.Until(s.paused)
			if c.want == 0 {
				if s.isPaused() {
					t.Fatalf("paused for %v", left)
				}
				return
			}
			if left < c.want-2*time.Second || left > c.want {
				t.Fatalf("paused for %v, want %v", left, c.want)
			}
		})
	}
}

func TestALowQuotaHoldsTheNextRequest(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	f.put("/topics/hot.json", answer{body: topicsJSON(goTopic), header: map[string]string{
		"X-Rate-Limit-Remaining": "4", "X-Rate-Limit-Reset": strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)}})
	a := testApp(t, f, "")
	ctx := context.Background()
	if l, err := a.list(ctx, "hot", false); err != nil || len(l.topics) != 1 {
		t.Fatalf("the answer that says the quota is low is still an answer: %d, %v", len(l.topics), err)
	}
	if _, err := a.list(ctx, "latest", false); !errors.Is(err, errRateLimited) {
		t.Fatalf("the next read: %v, want errRateLimited", err)
	}
	if f.hit("/topics/latest.json") != 0 {
		t.Fatal("asked the site with the quota nearly spent")
	}
	if st := a.status(); st.State != "rate_limited" {
		t.Fatalf("status: %+v", st)
	}
}

func TestRefusals(t *testing.T) {
	inHalfAnHour := time.Now().Add(30 * time.Minute).Unix()
	refusal := func(message string, used, quota int) string {
		return `{"status": "error", "message": "` + message + `", "rate_limit": {"used": ` + strconv.Itoa(used) +
			`, "quota": ` + strconv.Itoa(quota) + `, "reset_at": ` + strconv.FormatInt(inHalfAnHour, 10) + `}}`
	}
	for _, c := range []struct {
		name   string
		answer answer
		want   error
		pause  time.Duration // 0: none
	}{
		{"rate limit in a 403", answer{status: http.StatusForbidden, body: refusal("Rate Limit Exceeded", 3, 600)}, errRateLimited, 30 * time.Minute},
		{"rate limit in a 200", answer{body: refusal("rate limit exceeded", 3, 600)}, errRateLimited, 30 * time.Minute},
		{"quota spent", answer{status: http.StatusNotFound, body: refusal("Object Not Found", 600, 600)}, errRateLimited, 30 * time.Minute},
		{"a plain 404", answer{status: http.StatusNotFound, body: notFound}, errNotFound, 0},
		{"a bare 403", answer{status: http.StatusForbidden, body: "forbidden"}, errChallenged, 0},
		{"a 500", answer{status: http.StatusInternalServerError, body: "{}"}, nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeSite(t)
			f.put("/topics/hot.json", c.answer)
			s := newSite(f.srv.URL)
			_, err := s.get(context.Background(), "/topics/hot.json", listFor)
			if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if c.want == nil && !strings.Contains(err.Error(), "500") {
				t.Fatalf("got %v, want the status", err)
			}
			left := time.Until(s.paused)
			if c.pause == 0 {
				if s.isPaused() {
					t.Fatalf("paused for %v", left)
				}
				return
			}
			if left < c.pause-2*time.Second || left > c.pause {
				t.Fatalf("paused for %v, want %v", left, c.pause)
			}
		})
	}
}

func TestTheCacheIsBounded(t *testing.T) {
	set(t, &cacheEntries, 3)
	set(t, &cacheBytes, 100)
	s := newSite("")
	at := time.Now()
	for i, p := range []string{"/a", "/b", "/c", "/d"} {
		s.store(p, []byte("0123456789"), at.Add(time.Duration(i)*time.Second))
	}
	if _, ok := s.lookup("/a"); ok || len(s.cache) != 3 || s.size != 30 {
		t.Fatalf("by count: %d entries, %d bytes, /a kept %v", len(s.cache), s.size, ok)
	}
	s.store("/big", []byte(strings.Repeat("x", 81)), at.Add(10*time.Second))
	if s.size > 100 || len(s.cache) != 2 {
		t.Fatalf("by size: %d entries, %d bytes", len(s.cache), s.size)
	}
	if _, ok := s.lookup("/d"); !ok {
		t.Fatal("dropped the newest of the rest")
	}
	s.store("/huge", []byte(strings.Repeat("x", 500)), at.Add(20*time.Second))
	if _, ok := s.lookup("/huge"); !ok || len(s.cache) != 1 || s.size != 500 {
		t.Fatalf("one too big for the cache is kept alone: %d entries, %d bytes", len(s.cache), s.size)
	}
	s.store("/huge", []byte("[]"), at.Add(30*time.Second))
	if s.size != 2 {
		t.Fatalf("replacing an entry: %d bytes", s.size)
	}
}

func TestAnAnswerTooBigIsCut(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	ctx := context.Background()
	if _, err := a.list(ctx, "hot", false); err != nil {
		t.Fatal(err)
	}
	set(t, &answerMost, 16)
	l, err := a.list(ctx, "hot", true)
	if err != nil || !errors.Is(l.stale, errBadAnswer) || len(l.topics) != 3 {
		t.Fatalf("a cut answer: %d topics, stale %v, %v", len(l.topics), l.stale, err)
	}
}
