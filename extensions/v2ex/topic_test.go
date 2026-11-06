package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestThread(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	th, err := a.thread(context.Background(), 1001, true, false)
	if err != nil || th.repliesErr != nil || th.stale != nil || len(th.replies) != 25 || th.topic.ID != 1001 {
		t.Fatalf("thread: %d replies, %v %v %v", len(th.replies), th.repliesErr, th.stale, err)
	}
	floors, rs := latestReplies(th.replies, 20)
	if len(rs) != 20 || floors[0] != 6 || floors[19] != 25 || rs[0].Member.Username != "user6" || !strings.HasPrefix(rs[19].Content, "第 25 条回复") {
		t.Fatalf("the latest 20: floors %v, first %+v", floors, rs[0])
	}
	if floors, rs := latestReplies(th.replies[:3], 20); !slices.Equal(floors, []int{1, 2, 3}) || len(rs) != 3 {
		t.Fatalf("fewer than asked: floors %v", floors)
	}
	if b, _ := json.Marshal(th.replies); strings.Contains(string(b), "PRIVATE") {
		t.Fatalf("a profile field was kept: %s", b)
	}
}

func TestNoSuchTopic(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	f.serve(topicPath(1002), "[]")
	f.serve(topicPath(1004), topicsJSON(goneTopic))
	a := testApp(t, f, "")
	for _, id := range []int64{1002, 1004, 1009} { // an empty list, a deleted one, a 404
		if _, err := a.thread(context.Background(), id, true, false); !errors.Is(err, errNoTopic) {
			t.Errorf("topic %d: %v, want errNoTopic", id, err)
		}
	}
	if st := a.status(); st.State != "ok" {
		t.Fatalf("a missing topic is not the connector's state: %+v", st)
	}
}

func TestRepliesAreAskedForOnlyWhenWanted(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	f.serve(topicPath(1003), topicsJSON(jobTopic))
	a := testApp(t, f, "")
	ctx := context.Background()
	if th, err := a.thread(ctx, 1001, false, false); err != nil || th.replies != nil {
		t.Fatalf("not wanted: %v %v", th.replies, err)
	}
	if f.hit(repliesPath(1001)) != 0 {
		t.Fatal("asked for replies nobody wanted")
	}
	if _, err := a.thread(ctx, 1003, true, false); err != nil {
		t.Fatal(err)
	}
	if f.hit(repliesPath(1003)) != 0 {
		t.Fatal("asked for the replies of a topic that has none")
	}
}

func TestATopicWhoseRepliesFailIsStillATopic(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	f.put(repliesPath(1001), answer{status: http.StatusBadGateway})
	a := testApp(t, f, "")
	th, err := a.thread(context.Background(), 1001, true, false)
	if err != nil || th.topic.ID != 1001 || th.repliesErr == nil || !strings.Contains(th.repliesErr.Error(), "502") {
		t.Fatalf("topic %d, replies error %v, %v", th.topic.ID, th.repliesErr, err)
	}
}

func TestOlderRepliesStandIn(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	ctx := context.Background()
	if _, err := a.thread(ctx, 1001, true, false); err != nil {
		t.Fatal(err)
	}
	f.put(repliesPath(1001), answer{status: http.StatusBadGateway})
	th, err := a.thread(ctx, 1001, true, true)
	if err != nil || th.repliesErr != nil || len(th.replies) != 25 || th.stale == nil {
		t.Fatalf("%d replies, replies error %v, stale %v, %v", len(th.replies), th.repliesErr, th.stale, err)
	}
	c, ok := a.site.lookup(repliesPath(1001))
	if !ok || !th.at.Equal(c.at) {
		t.Fatalf("at %v, want the replies' %v: its older part", th.at, c.at)
	}
}

func TestTopicID(t *testing.T) {
	for _, raw := range []string{`1001`, `"1001"`, `" 1001 "`, `"/t/1001"`, `"https://www.v2ex.com/t/1001"`, `"http://v2ex.com/t/1001#reply3"`,
		`"www.v2ex.com/t/1001?p=2"`, `"HTTPS://WWW.V2EX.COM/t/1001/"`} {
		if id, err := topicID(json.RawMessage(raw)); err != nil || id != 1001 {
			t.Errorf("topicID(%s) = %d, %v", raw, id, err)
		}
	}
	for _, raw := range []string{``, `null`, `0`, `-3`, `1.5`, `{}`, `true`, `""`, `"abc"`, `"/t/0"`, `"https://example.com/t/1001"`,
		`"https://www.v2ex.com/go/python"`, `"https://www.v2ex.com.example/t/1001"`, `"99999999999999999999"`} {
		if id, err := topicID(json.RawMessage(raw)); err == nil {
			t.Errorf("topicID(%s) = %d, want an error", raw, id)
		}
	}
}
