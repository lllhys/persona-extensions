package curate

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	srcs "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
)

func TestAgentReportIsAnAgentsOwnLineInItsSession(t *testing.T) {
	agent := &Unit{Ref: "agents/local/session-x"}
	chat := &Unit{Ref: "chats/group-x"}
	for _, c := range []struct {
		u       *Unit
		speaker string
		want    bool
	}{
		{agent, srcs.SpeakerAgent, true},
		{agent, srcs.SpeakerWork, true},
		{agent, norm.Me, false},
		{agent, srcs.SpeakerPaste, false},
		{chat, srcs.SpeakerAgent, false}, // someone in a group named "Agent" is a person
	} {
		if got := agentReport(c.u, norm.Line{Speaker: c.speaker}); got != c.want {
			t.Errorf("agentReport(%s, %q) = %v, want %v", c.u.Ref, c.speaker, got, c.want)
		}
	}
}
