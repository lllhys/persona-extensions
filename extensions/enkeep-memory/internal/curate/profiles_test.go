package curate

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Placeholders only: person:lark-f0 / lark-f1 are people of the sample knowledge base; every text is a placeholder.

// profAct is a placeholder observation with one evidence line.
func profAct(c *Curator, part, text string, ev entry.Evidence) *entry.Entry {
	ev.Source, ev.N = entry.SrcChat, 1
	if ev.Quote == "" {
		ev.Quote = "quote " + text
	}
	e := &entry.Entry{Facet: entry.People, Grain: entry.Action, Person: pA, Part: part, Text: text, Domain: entry.Work,
		Inferred: part == entry.PartInferred, ReportedOnly: ev.Reported, Impact: entry.Impact{Moment: "kb_lookup"}, Evidence: []entry.Evidence{ev}}
	return c.St.S.Entries[c.Observe(e, "t")]
}

// profLine is a placeholder card line with its actions filed into it.
func profLine(c *Curator, part, typ, text string, acts ...*entry.Entry) *entry.Entry {
	l := c.create(&entry.Entry{Facet: entry.People, Grain: entry.Class, Person: pA, Part: part, Type: typ, Text: text,
		Inferred: part == entry.PartInferred}, "t")
	for _, a := range acts {
		if err := c.File(a.ID, l.ID, "t"); err != nil {
			panic(err)
		}
	}
	return l
}

// profileCard sets up a card with every part: a fact, a fact with no source the hub can follow, an inferred line
// resting on one reported line, the user's own words from a chat and from the assistant, a line the user worded,
// a sensitive fact, a deleted line, and promises either way.
func profileCard(t *testing.T) *Curator {
	c := cardCurator(t)
	f1 := profAct(c, entry.PartFact, "role-1", entry.Evidence{Ref: "chats/g1", Day: "2026-11-02", Who: pA})
	profLine(c, entry.PartFact, "role", "fact-line", f1)
	m1 := profAct(c, entry.PartFact, "bg-1", entry.Evidence{Ref: "memory/note-x", Day: "2026-11-02", Who: pA})
	profLine(c, entry.PartFact, "background", "fact-no-source", m1)
	i1 := profAct(c, entry.PartInferred, "style-1", entry.Evidence{Ref: "chats/g2", Day: "2026-11-03", Who: pB, Reported: true})
	profLine(c, entry.PartInferred, "style", "inferred-line", i1)
	u1 := profAct(c, entry.PartUserEval, "eval-1", entry.Evidence{Ref: "chats/persona-c1", Day: "2026-11-04", Who: entry.WhoSelf, Quote: "words-a"})
	u2 := profAct(c, entry.PartUserEval, "eval-2", entry.Evidence{Ref: "chats/g3", Day: "2026-11-05", Who: entry.WhoSelf, Quote: "words-b"})
	profLine(c, entry.PartUserEval, "", "eval-line", u1, u2)
	ue := profLine(c, entry.PartUserEval, "", "eval-model-words")
	ue.User = &entry.UserEdit{Text: "user-worded", At: "2026-11-06"}
	s1 := profAct(c, entry.PartFact, "health-1", entry.Evidence{Ref: "chats/g1", Day: "2026-11-02", Who: pA})
	sl := profLine(c, entry.PartFact, "background", "sensitive-line", s1)
	sl.Sensitive = entry.SensHealth
	s1.Sensitive = entry.SensHealth
	d1 := profAct(c, entry.PartFact, "gone-1", entry.Evidence{Ref: "chats/g1", Day: "2026-11-02", Who: pA})
	dl := profLine(c, entry.PartFact, "role", "deleted-line", d1)
	dl.Deleted = true
	mk := func(from, to, what, due, done string) {
		e := &entry.Entry{Facet: entry.People, Grain: entry.Action, Person: pA, Part: entry.PartCommitment, Text: what, Domain: entry.Work,
			Commitment: &entry.Commitment{From: from, To: to, What: what, Due: due, Done: done}, Impact: entry.Impact{Moment: "kb_lookup"},
			Evidence: []entry.Evidence{{Source: entry.SrcChat, Ref: "recordings/r1", Day: "2026-11-03", N: 1, Who: from, Quote: "quote " + what}}}
		c.Observe(e, "t")
	}
	mk(pA, entry.WhoSelf, "send-x", "2026-11-10", "")
	mk(entry.WhoSelf, pA, "review-y", "2026-11-01", "2026-11-02")
	mk(pA, pB, "third-z", "", "")
	return c
}

func cardProposals(c *Curator) []*store.Proposal {
	var out []*store.Proposal
	for _, p := range c.St.S.Proposals {
		if p.Kind == "person_card" {
			out = append(out, p)
		}
	}
	return out
}

func proposedProfile(t *testing.T, p *store.Proposal) *kbsrc.PersonProfile {
	t.Helper()
	var ch Change
	if err := json.Unmarshal(p.Change, &ch); err != nil || ch.Profile == nil {
		t.Fatalf("no profile in %s: %v", p.ID, err)
	}
	return ch.Profile
}

func texts(items []kbsrc.ProfileItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

// The card's four parts become the profile's four parts, every item with a source the hub can follow: a chat's day
// file, a recording, "user" for the user's words to the assistant; no source, not written.
func TestCardProfileMapping(t *testing.T) {
	c := profileCard(t)
	card := c.PersonCard(pA, "2026-11-08")
	prof, st := c.cardProfile(card, true)
	if err := kbsrc.CheckProfile(&prof); err != nil {
		t.Fatal(err)
	}
	if got := texts(prof.Facts); !slices.Equal(got, []string{"fact-line", "third-z（对方和别人之间的承诺，不是和用户）"}) {
		t.Fatalf("facts %q", got)
	}
	if got := prof.Facts[0].Sources; !slices.Equal(got, []string{"chats/g1/messages/2026-11-02.md"}) {
		t.Fatalf("fact sources %q", got)
	}
	views := map[string][]string{}
	for _, v := range prof.Views {
		views[v.Text] = v.Sources
	}
	if !slices.Equal(views["words-a"], []string{"user"}) || !slices.Equal(views["words-b"], []string{"chats/g3/messages/2026-11-05.md"}) ||
		!slices.Equal(views["user-worded"], []string{"user"}) || len(views) != 3 {
		t.Fatalf("views %v", views)
	}
	if len(prof.Inferences) != 1 || prof.Inferences[0].Text != "inferred-line" {
		t.Fatalf("inferences %+v", prof.Inferences)
	}
	if b := prof.Inferences[0].Basis; !strings.Contains(b, "证据少（1 条）") || !strings.Contains(b, "只来自转述") {
		t.Fatalf("basis %q", b)
	}
	if !slices.Equal(prof.Inferences[0].Sources, []string{"chats/g2/messages/2026-11-03.md"}) {
		t.Fatalf("inference sources %q", prof.Inferences[0].Sources)
	}
	cms := map[string]kbsrc.ProfileCommitment{}
	for _, cm := range prof.Commitments {
		cms[cm.Text] = cm
	}
	if cm := cms["send-x"]; cm.Owes != "them" || cm.Due != "2026-11-10" || cm.Done || !slices.Equal(cm.Sources, []string{"recordings/r1"}) {
		t.Fatalf("they owe %+v", cm)
	}
	if len(cms) != 2 {
		t.Fatalf("commitments %+v", prof.Commitments)
	}
	if cm := cms["review-y"]; cm.Owes != "user" || !cm.Done {
		t.Fatalf("user owes %+v", cm)
	}
	if st.NoSource != 1 {
		t.Fatalf("no source %d (fact-no-source)", st.NoSource)
	}
	all, _ := json.Marshal(prof)
	for _, never := range []string{"sensitive-line", "deleted-line", "eval-model-words", "fact-no-source"} {
		if strings.Contains(string(all), never) {
			t.Fatalf("%s written: %s", never, all)
		}
	}
	// K13: the sensitive category only when the user turned it on.
	c.St.S.Switches[entry.K13Sensitive+"."+entry.SensHealth] = entry.SwitchSetting{Value: "on", By: "user"}
	prof, _ = c.cardProfile(c.PersonCard(pA, "2026-11-08"), true)
	if !slices.Contains(texts(prof.Facts), "sensitive-line") {
		t.Fatalf("K13 on: %q", texts(prof.Facts))
	}
	// K15 off: no inferences.
	prof, _ = c.cardProfile(c.PersonCard(pA, "2026-11-08"), false)
	if len(prof.Inferences) != 0 || len(prof.Facts) == 0 {
		t.Fatalf("K15 off: %+v", prof)
	}
}

// The evidence a source is made of.
func TestProfileSource(t *testing.T) {
	for _, tc := range []struct {
		ev   entry.Evidence
		want string
	}{
		{entry.Evidence{Ref: "chats/oc_x", Day: "2026-11-01"}, "chats/oc_x/messages/2026-11-01.md"},
		{entry.Evidence{Ref: "chats/oc_x"}, ""},
		{entry.Evidence{Ref: "recordings/r1"}, "recordings/r1"},
		{entry.Evidence{Ref: "docs/d1"}, "docs/d1"},
		{entry.Evidence{Ref: "https://example.com/a"}, "https://example.com/a"},
		{entry.Evidence{Ref: "chats/persona-c1", Who: entry.WhoSelf}, "user"},
		{entry.Evidence{Ref: "chats/persona-c1", Who: "person:x"}, ""},
		{entry.Evidence{Ref: "agents/m1/s1", Role: "user"}, "user"},
		{entry.Evidence{Ref: "agents/m1/s1", Role: "agent"}, ""},
		{entry.Evidence{Ref: "memory/m", Who: entry.WhoSelf}, ""},
		{entry.Evidence{Ref: "about/me", Who: entry.WhoSelf}, "user"},
		{entry.Evidence{Ref: "nowhere"}, ""},
	} {
		if got := profileSource(tc.ev); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.ev, got, tc.want)
		}
	}
}

// Before the hub is found to take profiles (no probe yet, or a probe that says an older hub) the profile stays
// blocked and nothing is written.
func TestProfileBlockedUntilReleased(t *testing.T) {
	for _, tc := range []struct {
		name  string
		older bool
	}{{"not probed", false}, {"older hub", true}} {
		t.Run(tc.name, func(t *testing.T) {
			c := profileCard(t)
			fx := c.Src.(*kbsrc.Fixture)
			fx.NoProfiles = tc.older
			c.proposePeople(context.Background(), "2026-11-08", newRunT())
			ps := cardProposals(c)
			if len(ps) != 1 || ps[0].Status != "blocked" || ps[0].Needs != ProfileNeeds {
				t.Fatalf("proposals %+v", ps)
			}
			if tc.older {
				c.St.Unlock()
				c.CheckProfileCapability(context.Background())
				c.St.Lock()
			}
			if c.ReleaseProfiles() != 0 || ps[0].Status != "blocked" {
				t.Fatal("released before the hub takes profiles")
			}
			c.St.Unlock()
			_, err := c.Decide(context.Background(), ps[0].ID, "accept", "")
			c.St.Lock()
			if err == nil {
				t.Fatal("a blocked profile was accepted")
			}
			if p, _ := fx.ReadProfile(context.Background(), "lark-f0"); p != nil {
				t.Fatalf("written: %+v", p)
			}
		})
	}
}

// The probe finds a hub with profiles; it writes nothing to the person. Until the privacy filter is released only
// facts and commitments go out: evaluations and inferences stay in the plugin, never in the pinned facts either.
func TestProfileProbeAndViewsHeld(t *testing.T) {
	old := profileViewsReleased
	profileViewsReleased = false // the held path: a build without the hub's privacy filter
	t.Cleanup(func() { profileViewsReleased = old })
	c := profileCard(t)
	ctx := context.Background()
	fx := c.Src.(*kbsrc.Fixture)
	c.proposePeople(ctx, "2026-11-08", newRunT())
	c.St.Unlock()
	c.CheckProfileCapability(ctx)
	c.St.Lock()
	if p, _ := fx.ReadProfile(ctx, "lark-f0"); p != nil {
		t.Fatalf("the probe wrote: %+v", p)
	}
	if !c.ProfilesReleased() || c.ReleaseProfiles() != 1 {
		t.Fatal("a hub with profiles is not released")
	}
	p := cardProposals(c)[0]
	decide(t, c, p.ID)
	got, _ := fx.ReadProfile(ctx, "lark-f0")
	if got == nil || len(got.Facts) == 0 || len(got.Views) != 0 || len(got.Inferences) != 0 {
		t.Fatalf("written before the privacy filter: %+v", got)
	}
	for _, w := range fx.Writes() {
		if w.Pinned != nil && (strings.Contains(*w.Pinned, "style") || strings.Contains(*w.Pinned, "words")) {
			t.Fatalf("an inference or evaluation went to the pinned facts: %q", *w.Pinned)
		}
	}
}

// released plays a hub that takes profiles after the privacy filter: the whole card goes out.
func released(t *testing.T, c *Curator) {
	old := profileViewsReleased
	profileViewsReleased = true
	t.Cleanup(func() { profileViewsReleased = old })
	c.St.S.Cursors[profileCapKey] = "yes@" + c.today()
}

func decide(t *testing.T, c *Curator, id string) string {
	t.Helper()
	c.St.Unlock()
	msg, err := c.Decide(context.Background(), id, "accept", "")
	c.St.Lock()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func undo(t *testing.T, c *Curator, id string) {
	t.Helper()
	c.St.Unlock()
	err := c.Undo(context.Background(), id)
	c.St.Lock()
	if err != nil {
		t.Fatal(err)
	}
}

// Released: the blocked proposal becomes pending, accepting writes it (read back), undo writes back what it
// replaced: nothing (the profile is removed) the first time, the earlier profile the second time.
func TestProfileApplyReadBackUndo(t *testing.T) {
	c := profileCard(t)
	ctx := context.Background()
	fx := c.Src.(*kbsrc.Fixture)
	c.proposePeople(ctx, "2026-11-08", newRunT())
	first := cardProposals(c)[0]
	released(t, c)
	if n := c.ReleaseProfiles(); n != 1 || first.Status != "pending" || first.Needs != "" {
		t.Fatalf("released %d: %s %q", n, first.Status, first.Needs)
	}
	decide(t, c, first.ID)
	if first.Status != "accepted" || first.ApplyLog == "" {
		t.Fatalf("applied: %s %s", first.Status, first.ApplyLog)
	}
	got, _ := fx.ReadProfile(ctx, "lark-f0")
	want := proposedProfile(t, first)
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if string(gj) != string(wj) {
		t.Fatalf("read back %s\nwant %s", gj, wj)
	}
	pretty, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("written profile (placeholders):\n%s", pretty)
	a1 := c.St.S.Applied[len(c.St.S.Applied)-1]
	if string(a1.Before) != `{"profile":null}` || a1.Kind != "person_card" {
		t.Fatalf("before %s", a1.Before)
	}
	if ProfileDeliveryIn(c.St.S, "lark-f0").Status != "written" {
		t.Fatal("not shown as written")
	}

	// The same card again is not proposed again; a changed card is, and replaces the first.
	c.proposePeople(ctx, "2026-11-08", newRunT())
	if len(cardProposals(c)) != 1 {
		t.Fatal("the same profile proposed again")
	}
	f2 := profAct(c, entry.PartFact, "role-2", entry.Evidence{Ref: "chats/g9", Day: "2026-11-07", Who: pA})
	profLine(c, entry.PartFact, "projects", "fact-line-2", f2)
	c.proposePeople(ctx, "2026-11-08", newRunT())
	ps := cardProposals(c)
	second := ps[len(ps)-1]
	if second == first || second.Status != "pending" {
		t.Fatalf("second %+v", second)
	}
	decide(t, c, second.ID)
	a2 := c.St.S.Applied[len(c.St.S.Applied)-1]
	var b map[string]json.RawMessage
	_ = json.Unmarshal(a2.Before, &b)
	if string(b["profile"]) != string(wj) {
		t.Fatalf("second before %s", a2.Before)
	}

	// Undo the second: the first is back. Undo the first: none.
	undo(t, c, a2.ID)
	got, _ = fx.ReadProfile(ctx, "lark-f0")
	gj, _ = json.Marshal(got)
	if string(gj) != string(wj) {
		t.Fatalf("after undo %s", gj)
	}
	undo(t, c, a1.ID)
	if got, _ = fx.ReadProfile(ctx, "lark-f0"); got != nil {
		t.Fatalf("after undoing the first: %+v", got)
	}
	if ProfileDeliveryIn(c.St.S, "lark-f0").Status != "undone" {
		t.Fatalf("delivery %+v", ProfileDeliveryIn(c.St.S, "lark-f0"))
	}
}

// Undo refuses when the profile on the page is no longer the one written.
func TestProfileUndoChanged(t *testing.T) {
	c := profileCard(t)
	ctx := context.Background()
	released(t, c)
	c.proposePeople(ctx, "2026-11-08", newRunT())
	decide(t, c, cardProposals(c)[0].ID)
	a := c.St.S.Applied[len(c.St.S.Applied)-1]
	fx := c.Src.(*kbsrc.Fixture)
	if _, _, err := fx.UpdateProfile(ctx, "lark-f0", &kbsrc.PersonProfile{Facts: []kbsrc.ProfileItem{{Text: "other", Sources: []string{"user"}}}}); err != nil {
		t.Fatal(err)
	}
	c.St.Unlock()
	err := c.Undo(ctx, a.ID)
	c.St.Lock()
	if err != ErrChanged {
		t.Fatalf("undo over a change: %v", err)
	}
}

// A hub that answers and keeps nothing (before #5934): blocked again with a warning, released again only on another
// day. Fields the hub did not use are warned about.
func TestProfileReadBackFails(t *testing.T) {
	c := profileCard(t)
	ctx := context.Background()
	fx := c.Src.(*kbsrc.Fixture)
	fx.NoProfiles = true
	released(t, c)
	c.proposePeople(ctx, "2026-11-08", newRunT())
	p := cardProposals(c)[0]
	if p.Status != "pending" {
		t.Fatalf("released hub: %s", p.Status)
	}
	msg := decide(t, c, p.ID)
	if p.Status != "blocked" || readbackFailedOn(p) != c.today() || !strings.Contains(msg, "回读不到") {
		t.Fatalf("not kept: %s %q %q", p.Status, p.Needs, msg)
	}
	if len(c.ProfileWarnings()) != 1 || ProfileDeliveryIn(c.St.S, "lark-f0").Status != "readback_failed" {
		t.Fatalf("warnings %q", c.ProfileWarnings())
	}
	if notes := strings.Join(c.peopleNotes("2026-11-08", newRunT()), "\n"); !strings.Contains(notes, "人物画像没送到") {
		t.Fatalf("weekly notes %q", notes)
	}
	for _, a := range c.St.S.Applied {
		if a.Kind == "person_card" {
			t.Fatal("a write not kept was recorded as applied")
		}
	}
	if c.ReleaseProfiles() != 0 {
		t.Fatal("released again on the same build")
	}
	day := c.today()
	c.Now = func() time.Time { t, _ := time.Parse("2006-01-02", day); return t.AddDate(0, 0, 1).Add(12 * time.Hour) }
	c.St.S.Cursors[profileCapKey] = "yes@" + c.today()
	fx.NoProfiles = false
	fx.Ignored = []kbsrc.IgnoredField{{Field: "summary", Reason: "people have no summary"}}
	if c.ReleaseProfiles() != 1 || p.Status != "pending" {
		t.Fatalf("a newer hub: %s", p.Status)
	}
	decide(t, c, p.ID)
	if p.Status != "accepted" || !strings.Contains(p.Detail, "hub 没用上：summary（people have no summary）") {
		t.Fatalf("ignored not warned: %s %q", p.Status, p.Detail)
	}
}

// The proposal flow's modes hold: in shadow mode accepting only records the verdict.
func TestProfileShadowOnlyProposes(t *testing.T) {
	c := profileCard(t)
	c.Cfg.Mode = "shadow"
	released(t, c)
	c.proposePeople(context.Background(), "2026-11-08", newRunT())
	p := cardProposals(c)[0]
	decide(t, c, p.ID)
	if p.Verdict != "right" || p.Status != "pending" || ProfileDeliveryIn(c.St.S, "lark-f0").Status != "shadow" {
		t.Fatalf("shadow: %s %s", p.Status, p.Verdict)
	}
	if got, _ := c.Src.(*kbsrc.Fixture).ReadProfile(context.Background(), "lark-f0"); got != nil {
		t.Fatal("written in shadow mode")
	}
}

// A kind the user lets apply on its own is applied by the weekly pass once released (and read back).
func TestProfileAutoApplied(t *testing.T) {
	c := profileCard(t)
	ctx := context.Background()
	c.proposePeople(ctx, "2026-11-08", newRunT())
	c.Cfg.AutoApplyKinds = []string{"person_card"}
	c.Cfg.AutoApplyMinPrecision = 0.5
	for i := 0; i < 10; i++ { // the kind has proven itself
		c.St.S.Proposals = append(c.St.S.Proposals, &store.Proposal{ID: "K-x", Kind: "person_card", Status: "accepted", Target: "people/other"})
	}
	if n := c.autoApply(ctx, "w", false); n != 0 {
		t.Fatalf("applied before the release: %d", n)
	}
	released(t, c)
	if n := c.autoApply(ctx, "w", false); n != 1 {
		t.Fatalf("auto applied %d", n)
	}
	if got, _ := c.Src.(*kbsrc.Fixture).ReadProfile(ctx, "lark-f0"); got == nil {
		t.Fatal("not written")
	}
}

// K14 off: no profile is proposed; a card whose lines are all gone takes the written profile down.
func TestProfileK14AndTakeDown(t *testing.T) {
	c := profileCard(t)
	c.St.S.Switches[entry.K14People] = entry.SwitchSetting{Value: "off", By: "user"}
	c.proposePeople(context.Background(), "2026-11-08", newRunT())
	if len(cardProposals(c)) != 0 {
		t.Fatal("K14 off: proposed")
	}
	delete(c.St.S.Switches, entry.K14People)
	released(t, c)
	ctx := context.Background()
	c.proposePeople(ctx, "2026-11-08", newRunT())
	decide(t, c, cardProposals(c)[0].ID)
	for _, e := range c.St.S.Entries {
		if e.Facet == entry.People {
			e.Deleted = true
		}
	}
	c.proposePeople(ctx, "2026-11-09", newRunT())
	ps := cardProposals(c)
	down := ps[len(ps)-1]
	if !proposedProfile(t, down).Empty() || !strings.Contains(down.Detail, "撤下") {
		t.Fatalf("take down %+v", down)
	}
	decide(t, c, down.ID)
	if got, _ := c.Src.(*kbsrc.Fixture).ReadProfile(ctx, "lark-f0"); got != nil || down.Status != "accepted" {
		t.Fatalf("not taken down: %+v %s", got, down.Status)
	}
	c.proposePeople(ctx, "2026-11-10", newRunT())
	if len(cardProposals(c)) != len(ps) {
		t.Fatal("taken down twice")
	}
}

// The P9 proposals written as a summary and a log (which the hub ignores for people) expire.
func TestProfileOldSummaryProposalsExpire(t *testing.T) {
	c := profileCard(t)
	old := c.propose("person_card", "old", "", "people/lark-f0", Change{}, nil, nil, "hub：人物条目的 summary/log")
	c.ReleaseProfiles()
	if old.Status != "expired" {
		t.Fatalf("old proposal %s", old.Status)
	}
}
