package curate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/signals"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
)

type cancelLedgerHub struct {
	hubapi.None
	sessions []hubapi.Session
	items    map[string][]hubapi.Item
}

func (h *cancelLedgerHub) Sessions(context.Context, time.Time) ([]hubapi.Session, error) {
	return h.sessions, nil
}

func (h *cancelLedgerHub) Items(_ context.Context, _, session string, _ hubapi.ItemsQuery) ([]hubapi.Item, error) {
	return slices.Clone(h.items[session]), nil
}

// The daily pass ends with the ledger, the signals and the exits; without a hub every exit waits for its change and
// nothing fails. A dry run writes no exit.
func TestDailyEndsWithSignalsAndExits(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow") // no model configured: triage and checks only, the exits still written
	c.Hub = hubapi.None{}
	c.St.S.Entries = map[string]*entry.Entry{"i-0000000001": {ID: "i-0000000001", Facet: entry.Interaction, Grain: entry.Class,
		Text: "汇报时先给结论再给细节（占位）", Status: entry.Stable, Origin: entry.FromOnboarding, Count: 2,
		Impact: entry.Impact{Moment: "report", Action: "先给结论"}}}
	if _, err := c.RunDaily(context.Background(), "2026-11-03", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.St.Dir, export.ManifestPath)); err == nil {
		t.Fatal("a dry run wrote the exits")
	}
	run, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(c.St.Dir, export.InstructionsPath))
	if err != nil || !strings.Contains(string(b), "汇报时先给结论") {
		t.Fatalf("instructions: %v %q", err, b)
	}
	if c.St.S.Exits.Waiting["A"] != "H1" || c.St.S.Entries["i-0000000001"].Effective == "" || run.Counts["exit_files_written"] == 0 {
		t.Fatalf("exits %+v, counts %v", c.St.S.Exits, run.Counts)
	}
	if _, err := os.Stat(filepath.Join(c.St.Dir, "signals.json")); err != nil {
		t.Fatalf("signals did not run: %v", err)
	}
}

func TestDailyCancellationPersistsSignalIssues(t *testing.T) {
	const day = "2026-11-05" // after every sample-fixture unit: RunDaily reaches signals without daily units
	at := func(hour int) time.Time {
		return time.Date(2026, 11, 5, hour, 0, 0, 0, time.UTC)
	}
	sess := func(id string, hour int) hubapi.Session {
		return hubapi.Session{Machine: "m1", ID: id, Title: "placeholder " + id,
			Started: at(hour), Updated: at(hour).Add(time.Minute)}
	}
	userItems := func(hour int, text string) []hubapi.Item {
		return []hubapi.Item{{Seq: 1, At: at(hour).Add(time.Minute), Kind: hubapi.KindUser, Text: text}}
	}
	h := &cancelLedgerHub{
		sessions: []hubapi.Session{sess("s1", 9), sess("s2", 10)},
		items: map[string][]hubapi.Item{
			"s1": userItems(9, "不对，占位纠正 A"),
			"s2": userItems(10, "不对，占位纠正 B"),
		},
	}

	c, _ := newCurator(t, nil, "shadow")
	c.Hub = h
	c.Workers = 1
	now := at(23)
	c.Now = func() time.Time { return now }
	// Keep the retry's new entry out of export rewriting; ledger behavior is unchanged apart from Stable -> Candidate.
	c.St.S.Switches = map[string]entry.SwitchSetting{entry.K8AutoEffective: {Value: "off"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnIssues := true
	fallback := &stub.Model{}
	c.Model = stub.Scripted(func(r model.Request) (string, error) {
		switch r.Step {
		case "signals_correct":
			var in struct {
				Messages []struct {
					I    int    `json:"i"`
					Text string `json:"text"`
				} `json:"messages"`
			}
			if err := json.Unmarshal([]byte(r.User), &in); err != nil {
				return "", err
			}
			verdicts := []map[string]any{}
			for _, msg := range in.Messages {
				key, text := "key-b", "占位要求乙"
				if strings.Contains(msg.Text, "A") {
					key, text = "key-a", "占位要求甲"
				}
				verdicts = append(verdicts, map[string]any{
					"i": msg.I, "correction": true, "target": "",
					"issue_key": key, "issue_text": text, "sensitive": "",
				})
			}
			b, err := json.Marshal(map[string]any{"verdicts": verdicts})
			return string(b), err
		case "signals_issues":
			if cancelOnIssues {
				cancelOnIssues = false
				if got := len(c.St.S.Corrections); got != 2 {
					t.Fatalf("cancellation point has %d corrections, want both keeps", got)
				}
				cancel() // both keeps and two open issues now exist; the side file has not been saved
			}
			return `{"groups":[]}`, nil // distinct keys stay distinct; neither is eligible to file yet
		default:
			resp, err := fallback.Complete(context.Background(), r)
			return resp.Text, err
		}
	})

	run, err := c.RunDaily(ctx, day, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(run.Notes, "\n"), context.Canceled.Error()) {
		t.Fatalf("daily did not take the signals cancellation path: %+v", run.Notes)
	}
	if got := len(c.St.S.Corrections); got != 2 {
		t.Fatalf("in-memory corrections = %d, want 2", got)
	}

	reopened, err := store.Open(c.St.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.S.Corrections); got != 2 {
		t.Fatalf("state.json corrections = %d, want 2", got)
	}
	issues, err := signals.Issues(c.St.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for key, occasion := range map[string]string{
		"key-a": "agents/m1/s1@" + day,
		"key-b": "agents/m1/s2@" + day,
	} {
		is := issues[key]
		if is == nil || len(is.Corrections) != 1 || len(is.Sessions) != 1 || is.Sessions[0] != occasion || is.Entry != "" {
			t.Errorf("persisted %s = %+v, want one correction in %s and no entry", key, is, occasion)
		}
	}

	// A new session supplies key-a's second sighting. Same-day occurrences are sufficient: keys are session@day.
	now = now.Add(time.Minute) // avoid reusing the first Run ID
	h.sessions = append(h.sessions, sess("s3", 11))
	h.items["s3"] = userItems(11, "不对，占位纠正 A，再次")
	c.St = reopened
	second, err := c.RunDaily(context.Background(), day, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Counts["signal_corrections"]; got != 1 {
		t.Fatalf("retry corrections = %d, want only s3 to be fresh", got)
	}
	if got := second.Counts["signal_new_entries"]; got != 1 {
		t.Errorf("retry new entries = %d, want key-a to complete", got)
	}
	var made *entry.Entry
	for _, e := range c.St.S.Entries {
		if slices.Contains(e.Flags, "ledger:key-a") {
			made = e
			break
		}
	}
	if made == nil {
		t.Fatal("key-a was not completed after reloading the canceled run")
	}
	if made.Status != entry.Candidate || made.Text != "占位要求甲" || made.Count != 2 || made.Sessions != 2 || len(made.Evidence) != 2 {
		t.Errorf("completed entry = %+v", made)
	}
	refs := map[string]bool{}
	for _, ev := range made.Evidence {
		refs[ev.Ref] = true
		if ev.Day != day || ev.Who != entry.WhoSelf || ev.Role != "user" {
			t.Errorf("bad ledger evidence: %+v", ev)
		}
	}
	if !refs["agents/m1/s1"] || !refs["agents/m1/s3"] {
		t.Errorf("entry evidence refs = %v", refs)
	}
	linked := 0
	for _, correction := range c.St.S.Corrections {
		if correction.Issue == "key-a" && correction.Entry == made.ID {
			linked++
		}
	}
	if len(c.St.S.Corrections) != 3 || linked != 2 {
		t.Errorf("corrections after retry: linked=%d, all=%+v", linked, c.St.S.Corrections)
	}
	after, err := signals.Issues(c.St.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if a := after["key-a"]; a == nil || a.Entry != made.ID || len(a.Corrections) != 2 || len(a.Sessions) != 2 {
		t.Errorf("completed persisted issue = %+v", a)
	}
	if b := after["key-b"]; b == nil || b.Entry != "" {
		t.Errorf("unrelated issue was filed: %+v", b)
	}
}
