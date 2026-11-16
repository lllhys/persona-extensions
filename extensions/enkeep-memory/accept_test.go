package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// itemsModel answers every acceptance batch with one answer per item: a stand-in for the judging model in tests.
type itemsModel struct{ calls int }

func (m *itemsModel) Complete(_ context.Context, r model.Request) (model.Response, error) {
	m.calls++
	var in struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.User), &in); err != nil {
		return model.Response{}, err
	}
	var out []map[string]any
	for i, it := range in.Items {
		id := it["id"]
		switch r.Step {
		case "accept_empty":
			out = append(out, map[string]any{"id": id, "answerable": i%5 != 0, "moment": "m", "difference": "d"})
		case "accept_abstract":
			out = append(out, map[string]any{"id": id, "generic": false, "reason": "r"})
		case "accept_f1":
			out = append(out, map[string]any{"id": id, "verdict": []string{"right", "wrong", "unsupported"}[i%3], "reason": "r"})
		case "accept_speakers":
			out = append(out, map[string]any{"id": id, "verdict": "yes", "reason": "r"})
		}
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return model.Response{Text: string(b)}, nil
}

// placeholderState is a state with entries in every facet (placeholder text), an exits manifest, speaker lines and
// cards, as a replay leaves it.
func placeholderState(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var a, b []string
	n := 0
	for _, f := range entry.Facets {
		for i := 0; i < 8; i++ {
			n++
			id := fmt.Sprintf("%c-%03d", f[0], n)
			e := &entry.Entry{ID: id, Facet: f, Grain: entry.Class, Text: fmt.Sprintf("placeholder %s line %d for user-a on project-x", f, i),
				Status: entry.Stable, Type: "type-" + string(f), Count: 2,
				Evidence: []entry.Evidence{{Source: entry.SrcChat, Ref: "chats/lark-g1", Day: "2026-11-10", N: n, Who: "person:p1", Speaker: "user-b", Quote: "placeholder quote"}}}
			if f == entry.People {
				e.Part, e.Person = entry.PartFact, "person:p1"
			}
			if i == 0 {
				e.Grain, e.Default, e.Situations = entry.Abstract, "a default", []string{"s1", "s2"}
			}
			st.S.Entries[id] = e
			if f != entry.People {
				if f == entry.Tools || f == entry.Workflow {
					b = append(b, id)
				} else {
					a = append(a, id)
				}
			}
		}
	}
	st.S.SpeakerChecks = []entry.SpeakerCheck{{Source: "lark", Ref: "chats/lark-g1", Day: "2026-11-10", N: 3, Who: "person:p1", Name: "user-b", Quote: "placeholder"}}
	st.S.ActiveDays = []string{"2026-11-09", "2026-11-10"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	man := map[string]any{"exits": map[string]any{"A": map[string]any{"entries": a}, "B": map[string]any{"entries": b}}}
	if err := writeJSON(filepath.Join(dir, "exits.json"), man); err != nil {
		t.Fatal(err)
	}
	cards := []hubapi.Interaction{{ID: "c1", At: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), Recommend: "go", Answers: []string{"go"}},
		{ID: "c2", At: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC), Recommend: "go", Answers: []string{"go"}, AnsweredBy: "default"}}
	if err := writeJSON(filepath.Join(dir, cardsFile), cards); err != nil {
		t.Fatal(err)
	}
	return dir, st
}

// TestAcceptFromOneStateDir: one command over a state directory gives the readings, the empty-talk sample (30, at
// least 4 per facet of exits A and B), the abstract sample, the F1 sample and the speaker lines, each pointing back
// to an entry id or a unit line; the model only pre-judges, and nothing it says lands in an entry's verdicts.
func TestAcceptFromOneStateDir(t *testing.T) {
	dir, st := placeholderState(t)
	var cards []hubapi.Interaction
	raw, _ := os.ReadFile(filepath.Join(dir, cardsFile))
	_ = json.Unmarshal(raw, &cards)
	m := &itemsModel{}
	rep, err := runAccept(context.Background(), acceptInput{St: st.S, Dir: dir, Day: "2026-11-10", Loc: time.UTC, Cards: cards, Model: m,
		Rand: rand.New(rand.NewSource(1))})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Empty) != 30 || !strings.HasPrefix(rep.Exits, "exits.json") {
		t.Fatalf("empty-talk sample: %d from %s", len(rep.Empty), rep.Exits)
	}
	per := map[entry.Facet]int{}
	for _, e := range rep.Empty {
		if st.S.Entries[e.ID] == nil || e.Exit == "" || e.Answerable == nil {
			t.Errorf("sample does not point back or was not judged: %+v", e)
		}
		per[e.Facet]++
	}
	for _, f := range []entry.Facet{entry.Interaction, entry.Tools, entry.Judgement, entry.Workflow, entry.World, entry.Persona} {
		if per[f] < 4 {
			t.Errorf("%s: %d in the sample", f, per[f])
		}
	}
	if per[entry.People] != 0 {
		t.Errorf("people cards are not in exits A or B: %d sampled", per[entry.People])
	}
	// F1 is the check page's own sample (single ⑤ facts and the facts on the cards): here the 8 card facts.
	if len(rep.Abstract) != 7 || len(rep.Facts) != 8 {
		t.Errorf("abstract %d (want every one of 7), facts %d (want the 8 card facts)", len(rep.Abstract), len(rep.Facts))
	}
	for _, f := range rep.Facts {
		if st.S.Entries[f.ID] == nil || f.Verdict == "" {
			t.Errorf("fact does not point back or was not judged: %+v", f)
		}
	}
	if len(rep.Speakers) != 1 || rep.Speakers[0].Key != "chats/lark-g1@2026-11-10#3" {
		// evidence lines carry "person:p1" too: the speaker check comes first, evidence tops up to 30 per source
		if len(rep.Speakers) == 0 || rep.Speakers[0].Key != "chats/lark-g1@2026-11-10#3" {
			t.Errorf("speakers: %+v", rep.Speakers)
		}
	}
	got := map[string]entry.Reading{}
	for _, r := range st.S.Readings {
		got[r.Code] = r
	}
	if r := got["R4"]; r.Num != 1 || r.Den != 1 || !strings.Contains(r.Note, "到期默认 1") {
		t.Errorf("R4: %+v", r)
	}
	if r := got["F1.model"]; r.Den != len(rep.Facts) {
		t.Errorf("F1.model: %+v", r)
	}
	for _, e := range st.S.Entries {
		if len(e.Verdicts) > 0 {
			t.Errorf("model verdict written into %s", e.ID)
		}
	}
	for _, code := range []string{"R1", "R3", "R5", "R6", "R7", "R8", "U8"} {
		if _, ok := got[code]; !ok {
			t.Errorf("no %s", code)
		}
	}
	if !strings.Contains(rep.markdown(), "Empty talk") || m.calls == 0 {
		t.Error("no report")
	}
}

func TestAcceptRefusesAnthropicModels(t *testing.T) {
	for _, m := range []string{"claude-sonnet-5-5", "claude-opus-5-5", "fable-5-2"} {
		if refuseAnthropic(m) == nil {
			t.Errorf("%s accepted", m)
		}
	}
	if err := refuseAnthropic("model-x"); err != nil {
		t.Error(err)
	}
}

// fakeCLI is a model command line answering every call with an empty structured answer.
func fakeCLI(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	cmd := filepath.Join(t.TempDir(), "fake-model")
	script := "#!/bin/sh\ncat > /dev/null\necho '{\"is_error\":false,\"structured_output\":{},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// TestColdStartEmpty: design 10.3, "completely empty": no history at all (an empty knowledge base, no sessions, no
// memory), only the first-run answers. The replay runs the days, the week and the exits without an error, and the
// exits hold the answers and nothing else; in English the answers go out in English framing.
func TestColdStartEmpty(t *testing.T) {
	cmd := fakeCLI(t)
	for _, lang := range []string{"zh-Hans", "en"} {
		t.Run(lang, func(t *testing.T) {
			dir := t.TempDir()
			kb := filepath.Join(dir, "kb")
			if err := os.MkdirAll(filepath.Join(kb, "chats"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(kb, "owner.json"), []byte(`{"name":"user-a"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			ob := filepath.Join(dir, "onboarding.json")
			must, focus := "不要用英文缩写", "推进 project-x"
			if lang == "en" {
				must, focus = "Never use abbreviations", "Push project-x"
			}
			if err := os.WriteFile(ob, []byte(`{"use":"work","musts":["`+must+`"],"focus":["`+focus+`"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(dir, "state")
			err := offline([]string{"-kb", kb, "-from", "2026-11-09", "-to", "2026-11-11", "-tz", "UTC", "-state", state,
				"-out", filepath.Join(dir, "out"), "-model-cmd", cmd, "-model", "model-x", "-workers", "1", "-cold", "-onboarding", ob, "-language", lang,
				"-agents-dir", filepath.Join(dir, "no-agents")})
			if err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(state)
			if err != nil {
				t.Fatal(err)
			}
			if st.S.Backfill == nil || st.S.Backfill.Finished == "" {
				t.Fatalf("replay not finished: %+v", st.S.Backfill)
			}
			for _, e := range st.S.Entries {
				if e.Origin != entry.FromOnboarding {
					t.Errorf("an entry not from the answers: %+v", e)
				}
			}
			a, err := os.ReadFile(filepath.Join(state, "assistant", "instructions.md"))
			if err != nil {
				t.Fatalf("exit A not written after the replay: %v", err)
			}
			if !strings.Contains(string(a), must) || !strings.Contains(string(a), focus) {
				t.Errorf("exit A misses the answers:\n%s", a)
			}
			// Nothing else: no section but the answers' own (①, ⑥) and the fixed lines.
			sections := 0
			for _, l := range strings.Split(string(a), "\n") {
				if strings.HasPrefix(l, "## ") {
					sections++
				}
			}
			if sections > 2 {
				t.Errorf("exit A has %d sections, the answers make two:\n%s", sections, a)
			}
			if lang == "en" && (!strings.Contains(string(a), "Working on: Push project-x") || strings.ContainsAny(string(a), "关于用户")) {
				t.Errorf("English framing missing:\n%s", a)
			}
			var files []string
			_ = filepath.WalkDir(filepath.Join(state, "assistant", "skills"), func(p string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					files = append(files, p)
				}
				return nil
			})
			for _, f := range files {
				b, _ := os.ReadFile(f)
				if len(strings.TrimSpace(string(b))) > 0 {
					t.Errorf("a skill was written with no data: %s\n%s", f, b)
				}
			}
		})
	}
}
