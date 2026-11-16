package sources

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// TestCardLinesWhoAnswered: only the user's own answer (answered_by absent or user, a late answer included) is a
// line by the user; the assistant's answer is the AI's and a default at the deadline is the card's own line, so
// neither teaches the user's judgement (hub #5876).
func TestCardLinesWhoAnswered(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	base := hubapi.Interaction{At: at, Question: "ship project-x now?", Options: []string{"now", "later"}, Recommend: "now", Answers: []string{"now"}}
	for _, c := range []struct {
		by, status string
		late       bool
		speaker    string
	}{
		{"", "resolved", false, SpeakerUser},
		{"user", "resolved", false, SpeakerUser},
		{"user", "expired", true, SpeakerUser},
		{"assistant", "resolved", false, voice.AI},
		{"default", "expired", false, SpeakerCard},
	} {
		card := base
		card.AnsweredBy, card.Status, card.Late = c.by, c.status, c.late
		ls := cardLines(card, time.UTC)
		if len(ls) != 2 || !strings.Contains(ls[1], " "+c.speaker+": ") {
			t.Errorf("answered_by %q: %q", c.by, ls)
		}
		if c.by == "default" && !strings.Contains(ls[1], "到期按建议默认") {
			t.Errorf("default not said: %q", ls[1])
		}
	}
}
