package sources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// dayCards are the cards raised on day (S2), read once per pass. Without H3 the hub gives only its newest ≤500:
// what falls in the day is used, and the source is noted as waiting for H3.
func (s *Source) dayCards(ctx context.Context, day string) []hubapi.Interaction {
	s.mu.Lock()
	cs, ok := s.cards[day]
	s.mu.Unlock()
	if ok {
		return cs
	}
	start, end, err := s.bounds(day)
	if err != nil {
		return nil
	}
	cs, err = s.Hub.Interactions(ctx, hubapi.InteractionsQuery{Since: start, Before: end})
	var w *hubapi.WaitingHub
	switch {
	case err == nil:
		s.note(entry.SrcAsk, KindAsks, nil)
	case errors.As(err, &w):
		s.partial(entry.SrcAsk, KindAsks, w.Change, "只能取到最新的 500 张卡片，更早的要等 hub 的翻页（"+w.Change+"）")
	default:
		s.note(entry.SrcAsk, KindAsks, err)
		cs = nil
	}
	var kept []hubapi.Interaction
	for _, c := range cs {
		if !c.Private { // the hub does not filter them for an extension's token: never read
			kept = append(kept, c)
		}
	}
	raw, _ := json.Marshal(kept)
	s.addBytes(entry.SrcAsk, KindAsks, len(raw))
	s.mu.Lock()
	s.cards[day] = kept
	s.mu.Unlock()
	return kept
}

// looseCards are the day's cards not raised in an agent session listed for the day (the assistant's own, or a
// session not listed): those are read in asks/<day>; the others in their session's unit.
func (s *Source) looseCards(ctx context.Context, day string) []hubapi.Interaction {
	var out []hubapi.Interaction
	for _, c := range s.dayCards(ctx, day) {
		s.mu.Lock()
		_, inSession := s.sessions[c.Machine+"/"+c.Session]
		if !inSession && c.Machine == "" {
			for id := range s.sessions {
				if strings.HasSuffix(id, "/"+c.Session) {
					inSession = true
				}
			}
		}
		s.mu.Unlock()
		if !inSession {
			out = append(out, c)
		}
	}
	return out
}

func (s *Source) askEntries(ctx context.Context, day string) []wire.KBEntry {
	cs := s.looseCards(ctx, day)
	if len(cs) == 0 {
		return nil
	}
	return []wire.KBEntry{{Kind: KindAsks, ID: day, Name: "卡片问答 · " + day, Updated: cs[len(cs)-1].At, Source: "persona"}}
}

func (s *Source) askDay(ctx context.Context, id, day string) ([]string, error) {
	if id != day {
		return nil, nil
	}
	var out []string
	for _, c := range s.looseCards(ctx, day) {
		out = append(out, cardLines(c, s.Loc)...)
	}
	if len(out) > 0 {
		s.count(entry.SrcAsk, KindAsks, KindAsks+"/"+id, day, len(out), 0)
	}
	return out, nil
}

// cardLines are a card as a day's lines: the question with its options and the suggested one ("卡片"), then the
// answer under whoever gave it: the user ("我": a choice the user made), or the assistant answering for the user
// (voice.AI, with its reason). An unanswered card says how it ended.
func cardLines(c hubapi.Interaction, loc *time.Location) []string {
	at := hhmm(c.At, loc)
	var b strings.Builder
	b.WriteString("问「" + oneLine(clip(c.Question, 400)) + "」")
	if c.Title != "" {
		b.WriteString("（" + oneLine(clip(c.Title, 30)) + "）")
	}
	if len(c.Options) > 0 {
		var os []string
		for _, o := range c.Options {
			os = append(os, oneLine(clip(o, 80)))
		}
		b.WriteString("；选项：" + strings.Join(os, " / "))
	}
	if c.Recommend != "" {
		b.WriteString("；建议：" + oneLine(clip(c.Recommend, 80)))
	}
	if len(c.Answers) == 0 && c.Status != "" {
		b.WriteString("；未回答（" + c.Status + "）")
	}
	out := []string{at + " " + SpeakerCard + ": " + b.String()}
	if len(c.Answers) > 0 {
		answer := "选了：" + oneLine(clip(strings.Join(c.Answers, " / "), 300))
		if c.AnsweredBy == "assistant" {
			if c.Reason != "" {
				answer += "；理由：" + oneLine(clip(c.Reason, 200))
			}
			out = append(out, at+" "+voice.AI+": "+answer)
		} else if c.AnsweredBy == "default" {
			// Nobody answered: the recommendation was taken at the deadline (hub #5876). Not the user's choice.
			out = append(out, at+" "+SpeakerCard+": 无人回答，到期按建议默认"+strings.TrimPrefix(answer, "选了"))
		} else {
			out = append(out, at+" "+SpeakerUser+": "+answer)
		}
	}
	return out
}
