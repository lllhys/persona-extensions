package main

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The page says the model is briefly unavailable for the first failures, paused after curate.PausedAfter, with the
// hub's reason, the next try and the day the run goes on from; nothing when no pass waits.
func TestPauseLine(t *testing.T) {
	a := &app{c: &curate.Curator{Loc: time.UTC}}
	st := &store.State{}
	if text, _ := a.pauseLine(tr{}, st, time.Now()); text != "" {
		t.Fatalf("no pause: %q", text)
	}
	st.ModelPause = &store.ModelPause{Since: "2026-11-13T20:00:00Z", Reason: "并发已满（占位）", Day: "2026-09-28", Fails: 1, NextTry: "2026-11-13T20:10:00Z"}
	text, _ := a.pauseLine(tr{}, st, time.Now())
	if !strings.Contains(text, "暂时不可用") || !strings.Contains(text, "2026-09-28") || !strings.Contains(text, "11-13 20:10") {
		t.Fatalf("first failure: %q", text)
	}
	st.ModelPause.Fails = curate.PausedAfter
	text, _ = a.pauseLine(tr{}, st, time.Now())
	if !strings.Contains(text, "暂停中") || !strings.Contains(text, "并发已满") || !strings.Contains(text, "从 2026-09-28 接着跑") {
		t.Fatalf("paused: %q", text)
	}
	if en, _ := a.pauseLine(tr{en: true}, st, time.Now()); !strings.Contains(en, "paused") {
		t.Fatalf("English: %q", en)
	}
}

func TestPauseLineAssistantAway(t *testing.T) {
	a := &app{c: &curate.Curator{Loc: time.UTC}}
	st := &store.State{ModelPause: &store.ModelPause{Since: "2026-11-13T20:00:00Z", Reason: "the assistant is away（占位）", Day: "2026-09-30",
		Fails: 1, NextTry: "2026-11-13T20:10:00Z", What: "assistant"}}
	text, _ := a.pauseLine(tr{}, st, time.Now())
	if !strings.Contains(text, "助手的电脑没连上") || strings.Contains(text, "模型") || !strings.Contains(text, "2026-09-30") {
		t.Fatalf("away: %q", text)
	}
}
