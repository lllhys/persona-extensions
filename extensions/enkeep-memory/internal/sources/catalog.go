package sources

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Catalog sources have no days of their own: "about me" (S7) and the hand-written memory (S6) are read whole, and a
// unit is made on the first day they are read and again on a day they changed (their content hash, kept in
// State.Cursors). A memory file counts from the day it was made (its created time, else its modified time).

const maxCatalogLines = 300

func (s *Source) empty(code, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusOf(code, name)
	st.Available, st.Missing = true, "empty"
}

func (s *Source) aboutEntries(ctx context.Context, since time.Time) []wire.KBEntry {
	text, err := s.Hub.AboutMe(ctx)
	s.note(entry.SrcAbout, KindAbout, err)
	if err != nil {
		return nil
	}
	s.addBytes(entry.SrcAbout, KindAbout, len(text))
	text = strings.TrimSpace(text)
	if text == "" {
		s.empty(entry.SrcAbout, KindAbout) // empty stays empty: nothing generic fills it
		return nil
	}
	h := hubapi.Hash(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.about = text
	if s.cursors[entry.SrcAbout] == h {
		return nil
	}
	s.pending[entry.SrcAbout] = h
	return []wire.KBEntry{{Kind: KindAbout, ID: "me", Name: "关于我", Updated: since, Source: "persona"}}
}

// aboutDay is "about me" a line at a time, as the user's own words: what the user wrote of themself (self_report).
func (s *Source) aboutDay(day string) []string {
	s.mu.Lock()
	text := s.about
	s.mu.Unlock()
	out := catalogLines(text, "00:00", SpeakerUser)
	if len(out) > 0 {
		s.count(entry.SrcAbout, KindAbout, KindAbout+"/me", day, len(out), len(text))
	}
	return out
}

func memoryKey(name string) string { return entry.SrcMemory + "/" + name }

func (s *Source) memoryEntries(ctx context.Context, since, end time.Time) []wire.KBEntry {
	ms, err := s.Hub.Memories(ctx, true)
	s.note(entry.SrcMemory, KindMemory, err)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(ms)
	s.addBytes(entry.SrcMemory, KindMemory, len(raw))
	if len(ms) == 0 {
		s.empty(entry.SrcMemory, KindMemory) // memory off or not used: the curator works the same (5.1)
		return nil
	}
	var out []wire.KBEntry
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range ms {
		if m.Truncated {
			// A partial body is not a complete hand-written memory. Leave its cursor unset so a later full response is read.
			continue
		}
		made := m.Created
		if made.IsZero() {
			made = m.Modified
		}
		if !made.IsZero() && !made.Before(end) {
			continue // not there yet on this day
		}
		h := hubapi.Hash(m.Description + "\n" + m.Type + "\n" + m.Content)
		if s.cursors[memoryKey(m.Name)] == h {
			continue
		}
		s.memories[m.Name] = m
		s.pending[memoryKey(m.Name)] = h
		out = append(out, wire.KBEntry{Kind: KindMemory, ID: m.Name, Name: "助手记忆 · " + m.Name, Updated: since, Source: "persona"})
	}
	return out
}

// memoryDay is a memory file as lines: its description and type first, then its body. They are the assistant's
// words the user approved (handwritten): the speaker says so, the meta's origin says how to file them.
func (s *Source) memoryDay(name, day string) []string {
	s.mu.Lock()
	m, ok := s.memories[name]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	at := "00:00"
	if start, end, err := s.bounds(day); err == nil && !m.Created.Before(start) && m.Created.Before(end) {
		at = hhmm(m.Created, s.Loc)
	}
	var out []string
	if m.Description != "" || m.Type != "" {
		head := oneLine(m.Description)
		if m.Type != "" {
			head += "（" + m.Type + "）"
		}
		out = append(out, at+" "+SpeakerMemory+": "+head)
	}
	out = append(out, catalogLines(m.Content, at, SpeakerMemory)...)
	if len(out) > 0 {
		s.count(entry.SrcMemory, KindMemory, KindMemory+"/"+name, day, len(out), len(m.Content))
	}
	return out
}

func catalogLines(text, at, speaker string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l == "---" {
			continue
		}
		out = append(out, at+" "+speaker+": "+l)
		if len(out) >= maxCatalogLines {
			break
		}
	}
	return out
}
