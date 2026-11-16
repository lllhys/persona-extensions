package curate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
)

// TestForgetEntries: an action whose only source is forgotten goes, out of its class too; an action with another
// source keeps only that one; the class is counted again; no event in the log keeps the forgotten line, and the log
// still replays to the entries.
func TestForgetEntries(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.St.Lock()
	gone := c.Observe(act(entry.Judgement, "choice-gone", "chats/lark-g1", "2026-11-03", "forgotten line"), "r")
	kept := c.Observe(act(entry.Judgement, "choice-kept", "chats/wechat-g1", "2026-11-04", "kept line"), "r")
	both := act(entry.Judgement, "choice-both", "chats/wechat-g2", "2026-11-04", "kept line two")
	both.Evidence = append(both.Evidence, entry.Evidence{Source: entry.SrcChat, Ref: "chats/lark-g2", Day: "2026-11-05", Quote: "forgotten line two"})
	bothID := c.Observe(both, "r")
	k := c.create(&entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Text: "class-x"}, "r")
	for _, id := range []string{gone, kept, bothID} {
		if err := c.File(id, k.ID, "r"); err != nil {
			t.Fatal(err)
		}
	}
	if k.Count != 4 {
		t.Fatalf("setup count %d", k.Count)
	}
	if err := c.St.Save(); err != nil {
		t.Fatal(err)
	}
	c.St.Unlock()
	if _, err := c.Forget("lark"); err != nil {
		t.Fatal(err)
	}
	s := c.St.S
	if s.Entries[gone] != nil || slices.Contains(s.Entries[k.ID].Members, gone) || len(s.Entries[bothID].Evidence) != 1 || s.Entries[k.ID].Count != 2 {
		t.Fatalf("after forget: %v %v %v count %d", s.Entries[gone], s.Entries[k.ID].Members, s.Entries[bothID].Evidence, s.Entries[k.ID].Count)
	}
	raw, _ := os.ReadFile(filepath.Join(c.St.Dir, "events.jsonl"))
	evs, err := c.St.Events()
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if strings.Contains(string(ev.Data), "forgotten line") || strings.Contains(string(raw), "forgotten line") {
			t.Fatal("the event log keeps a forgotten line")
		}
	}
	replayed, err := ReplayEntries(evs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, s.Entries) {
		t.Errorf("the log does not replay to the entries after forgetting")
	}
	_ = kept
}

func TestForgetScrubsRawBackupsWithoutNormalization(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	affectedPath := filepath.Join(c.St.Dir, "state.json.bak-20260101T000000Z")
	unrelatedPath := filepath.Join(c.St.Dir, "state.json.bak-20260102T000000Z")
	affected := []byte(`{
  "version": 1,
  "future_top": { "keep": [1, 2] },
  "memory": {
    "future_book": {"keep":"book"},
    "entries": {
      "m-mixed": {
        "id": "m-mixed",
        "text": "mixed memory",
        "layer": "fact",
        "future_memory": {"keep":"memory"},
        "status":{"as_of":"2026-01-01","weeks":["2026-W01","2026-W02"],"future_status":{"keep":"status"}},
        "evidence": [
          {"day":"2026-11-06","source":"chats/lark-forget","anchor":"chats/lark-forget#3","quote":"forgotten memory quote","speaker":"user"},
          {"day":"2026-11-06","source":"chats/wechat-keep","anchor":"chats/wechat-keep#3","quote":"kept memory quote","speaker":"user","future_memory_evidence":{"keep":"memory evidence"}}
        ]
      }
    }
  },
  "items": {
    "i-gone": {
      "id":"i-gone","claim":"forgotten report","class":"fact","status":"candidate",
      "evidence":[{"chat":"chats/lark-forget","day":"2026-11-06","n":3,"quote":"forgotten item quote"}]
    },
    "i-mixed": {
      "id":"i-mixed","claim":"mixed claim","class":"fact","status":"candidate","future_item":{"keep":"item"},
      "evidence":[
        {"chat":"chats/lark-forget","day":"2026-11-06","n":3,"quote":"forgotten item quote"},
        {"chat":"chats/wechat-keep","day":"2026-11-06","n":3,"quote":"kept item quote","future_item_evidence":{"keep":"item evidence"}}
      ]
    }
  },
  "proposals": [{
    "id":"K-LARK","kind":"pin_fact","status":"pending","target":"chats/lark-forget","future_proposal":{"keep":"proposal"},
    "evidence":[
      {"chat":"chats/lark-forget","day":"2026-11-06","n":3,"quote":"forgotten proposal quote"},
      {"chat":"chats/wechat-keep","day":"2026-11-06","n":3,"quote":"kept proposal quote","future_proposal_evidence":{"keep":"proposal evidence"}}
    ]
  }],
  "runs": [{
    "id":"run","kind":"weekly","report":"forgotten report\nkept report","future_run":{"keep":"run"},
    "refusals":[
      {"unit":"chats/lark-forget","reason":"forgotten refusal"},
      {"unit":"chats/wechat-keep","reason":"kept refusal","future_refusal":{"keep":"refusal"}}
    ]
  }],
  "graph": {"nodes": {}, "assertions": {}}
}`)
	unrelated := []byte(`{
  "version": 1,
  "future_top": { "keep": [1, 2] },
  "memory": {"entries": {"m-other": {"id":"m-other","text":"kept memory","layer":"fact","evidence":[{"day":"2026-11-06","source":"chats/wechat-keep","anchor":"chats/wechat-keep#3","quote":"kept memory quote","speaker":"user"}]}}},
  "items": {"i-other": {"id":"i-other","claim":"kept claim","class":"fact","status":"candidate","evidence":[{"chat":"chats/wechat-keep","day":"2026-11-06","n":3,"quote":"kept item quote","future_item_evidence":{"keep":"item evidence"}}]}},
  "proposals": [{"id":"K-OTHER","kind":"pin_fact","status":"pending","target":"chats/wechat-keep","future_proposal":{"keep":"proposal"}}],
  "runs": [{"id":"other-run","kind":"weekly","report":"kept report","refusals":[{"unit":"chats/wechat-keep","reason":"kept refusal"}]}],
  "graph": {"nodes": {}, "assertions": {}}
}`)
	if err := os.WriteFile(affectedPath, affected, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedPath, unrelated, 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(1700000000, 0)
	if err := os.Chtimes(unrelatedPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Forget("lark"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(affectedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"future_top":{ "keep": [1, 2] }`,
		`"future_book":{"keep":"book"}`,
		`"future_memory":{"keep":"memory"}`,
		`"future_status":{"keep":"status"}`,
		`"future_memory_evidence":{"keep":"memory evidence"}`,
		`"future_item":{"keep":"item"}`,
		`"future_item_evidence":{"keep":"item evidence"}`,
		`"future_proposal":{"keep":"proposal"}`,
		`"future_proposal_evidence":{"keep":"proposal evidence"}`,
		`"future_run":{"keep":"run"}`,
		`"future_refusal":{"keep":"refusal"}`,
		"kept memory quote", "kept item quote", "kept proposal quote", "kept report", "kept refusal",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("affected backup lost %s", want)
		}
	}
	for _, unwanted := range []string{
		"forgotten memory quote", "forgotten item quote", "forgotten proposal quote", "forgotten report", "forgotten refusal",
		`"installed"`, `"done"`, `"digests"`, `"triage"`,
	} {
		if strings.Contains(string(got), unwanted) {
			t.Errorf("affected backup gained or retained %s", unwanted)
		}
	}
	gotUnrelated, err := os.ReadFile(unrelatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotUnrelated, unrelated) {
		t.Error("unrelated backup bytes changed")
	}
	info, err := os.Stat(unrelatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(mtime) {
		t.Errorf("unrelated backup mtime changed: %v", info.ModTime())
	}
}

// TestForgetClearsRejectionsAndReports is the review's probe: a rejection holding a claim read from a lark chat and an
// entry resting on a lark line. After Forget("lark") neither text is anywhere in the state on disk; what came from
// other sources stays.
func TestForgetClearsRejectionsAndReports(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	ctx := context.Background()
	if _, err := c.RunDaily(ctx, "2026-11-04", false); err != nil {
		t.Fatal(err)
	}
	c.St.Lock()
	const fromLark = "entry-from-lark-only"
	c.Observe(act(entry.Persona, fromLark, "chats/lark-dm-1", "2026-11-04", "lark quote"), "r")
	c.Observe(act(entry.Persona, "entry-from-wechat", "chats/wechat-x", "2026-11-04", "wechat quote"), "r")
	c.St.Unlock()
	if _, err := c.RunWeekly(ctx, "2026-W45", false); err != nil {
		t.Fatal(err)
	}
	var larkCall, otherCall *store.Done
	for _, d := range c.St.S.Done {
		switch {
		case strings.HasPrefix(d.Chat, "chats/lark-") && larkCall == nil:
			larkCall = d
		case d.Chat != "" && !strings.HasPrefix(d.Chat, "chats/lark-") && otherCall == nil:
			otherCall = d
		}
	}
	if larkCall == nil || otherCall == nil {
		t.Fatal("setup: calls for a lark chat and another chat")
	}
	const larkClaim, otherClaim = "飞书群里说下周三上线新版本", "微信里说周五聚餐"
	c.reject(larkCall, "daily", verify.QuoteNotFound, larkClaim, "2026-11-04")
	c.reject(otherCall, "daily", verify.QuoteNotFound, otherClaim, "2026-11-04")
	if err := c.St.Save(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Forget("lark"); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(filepath.Join(c.St.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fromLark, larkClaim} {
		if strings.Contains(string(disk), text) {
			t.Fatalf("%q is still in state.json after Forget(\"lark\")", text)
		}
	}
	for _, r := range c.St.S.Rejections {
		if r.Claim == larkClaim {
			t.Fatal("the lark rejection is kept")
		}
	}
	if !strings.Contains(string(disk), otherClaim) || !strings.Contains(string(disk), "entry-from-wechat") {
		t.Fatal("what came from other sources goes too")
	}
}
