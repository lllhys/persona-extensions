package curate

import (
	"context"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
)

// A card accepted before the privacy filter was released has only facts/commitments on the hub. Releasing the filter
// must create a migration proposal for the newly permitted views/inferences instead of reusing the old accepted hash.
func TestProfileViewsReleaseMigratesAcceptedCard(t *testing.T) {
	old := profileViewsReleased
	defer func() { profileViewsReleased = old }()
	profileViewsReleased = false
	c := profileCard(t)
	c.St.S.Cursors[profileCapKey] = "yes@" + c.today()
	ctx := context.Background()
	c.proposePeople(ctx, "2026-11-08", newRunT())
	first := cardProposals(c)[0]
	decide(t, c, first.ID)
	fx := c.Src.(*kbsrc.Fixture)
	got, _ := fx.ReadProfile(ctx, "lark-f0")
	if got == nil || len(got.Views) != 0 || len(got.Inferences) != 0 {
		t.Fatalf("before release: %+v", got)
	}

	profileViewsReleased = true
	current, _ := c.cardProfile(c.PersonCard(pA, "2026-11-08"), true)
	t.Logf("current views=%d inferences=%d; outgoing changed=%v", len(current.Views), len(current.Inferences), c.outgoingChanged("people/lark-f0", &current))
	c.proposePeople(ctx, "2026-11-08", newRunT())
	ps := cardProposals(c)
	if len(ps) != 2 || ps[1] == first || ps[1].Status != "pending" {
		t.Fatalf("no migration proposal for newly permitted views/inferences: %+v", ps)
	}
	decide(t, c, ps[1].ID)
	got, _ = fx.ReadProfile(ctx, "lark-f0")
	if got == nil || len(got.Views) == 0 || len(got.Inferences) == 0 {
		t.Fatalf("migration did not write views/inferences: %+v", got)
	}
}
