package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// A rule removed with homeassistant_watch, or dropped from the settings, while its for_seconds hold runs or while its
// firing waits for the batch, delivers nothing afterwards: its holds are stopped and its queued firings dropped.
func TestWatchRemovedRuleDeliversNothing(t *testing.T) {
	batchFor, holdUnit = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { batchFor, holdUnit = 3*time.Second, time.Second })
	r := newRichHA(t)
	hub := newFakeHub(t)
	a := richApp(t, r, Config{Watches: []string{"binary_sensor.leak =on", "binary_sensor.back_door =on for=20"}})
	a.hub = &hubAPI{url: hub.srv.URL, token: "pext_test", scopes: []string{"notify", "assistant.ask"}, http: http.DefaultClient}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.watch.run(ctx)
	waitFor(t, "the subscription", func() bool { return len(r.asked("subscribe_events")) == 1 })
	waitFor(t, "connected", func() bool { a.watch.mu.Lock(); defer a.watch.mu.Unlock(); return a.watch.connected })

	req := fmt.Sprintf("watch-remove-%d-", time.Now().UnixNano()) // actions.do answers are kept by request id for the whole run
	pending := func() (holds, queued int) {
		a.watch.mu.Lock()
		defer a.watch.mu.Unlock()
		return len(a.watch.timers), len(a.watch.queue)
	}
	open := func(e string) {
		door := map[string]any{"friendly_name": "入户门"}
		r.push(e, st(e, "off", door), st(e, "on", door))
	}
	removeMine := func(rid string) {
		t.Helper()
		for _, x := range a.watch.rules() {
			if x.Source == "assistant" {
				if _, err := doAction(t, a, watchAction, map[string]any{"op": "remove", "id": x.ID}, req+rid); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	nothingSent := func(what string) {
		t.Helper()
		time.Sleep(20*holdUnit + batchFor + 200*time.Millisecond) // the hold and the batch are over
		hub.mu.Lock()
		defer hub.mu.Unlock()
		if len(hub.notified) > 0 || len(hub.asked) > 0 {
			t.Errorf("%s: notified %+v, asked %q", what, hub.notified, hub.asked)
		}
		hub.notified, hub.asked = nil, nil
	}

	// the assistant's rule, removed on the phone during its hold
	add := map[string]any{"op": "add", "name": "门开了", "entity_id": "binary_sensor.front_door", "to": "on", "for_seconds": 20, "do": "both", "message": "把门锁上"}
	if _, err := doAction(t, a, watchAction, add, req+"add1"); err != nil {
		t.Fatal(err)
	}
	open("binary_sensor.front_door")
	waitFor(t, "the hold", func() bool { h, _ := pending(); return h == 1 })
	removeMine("rm1")
	if h, _ := pending(); h != 0 {
		t.Errorf("%d holds left after the remove", h)
	}
	nothingSent("removed during its hold")

	// a settings rule, dropped by new settings during its hold
	open("binary_sensor.back_door")
	waitFor(t, "the hold", func() bool { h, _ := pending(); return h == 1 })
	a.watch.setSettings([]string{"binary_sensor.leak =on"})
	if h, _ := pending(); h != 0 {
		t.Errorf("%d holds left after the settings dropped the rule", h)
	}
	nothingSent("dropped from the settings during its hold")

	// the assistant's rule without a hold, removed while its firing waits for the batch
	add = map[string]any{"op": "add", "name": "门开了", "entity_id": "binary_sensor.front_door", "to": "on"}
	if _, err := doAction(t, a, watchAction, add, req+"add2"); err != nil {
		t.Fatal(err)
	}
	open("binary_sensor.front_door")
	waitFor(t, "the firing", func() bool { _, q := pending(); return q == 1 })
	removeMine("rm2")
	if _, q := pending(); q != 0 {
		t.Errorf("%d firings queued after the remove", q)
	}
	nothingSent("removed while its firing waited for the batch")

	// the rule that is kept still fires
	leak, _ := parseRule("binary_sensor.leak =on")
	r.push("binary_sensor.leak", st("binary_sensor.leak", "off", nil), st("binary_sensor.leak", "on", nil))
	waitFor(t, "the leak's notification", func() bool { n, _ := hub.counts(); return n == 1 })
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if n := hub.notified[0]; n.ID != "ha-"+leak.ID {
		t.Errorf("notice %+v, want the leak rule's", n)
	}
}
