package curate

// Writing a person's card to the knowledge base as the hub's sectioned person profile (kb.update profile,
// nwverse/persona#5934): the person_card proposal carries the profile built from the card (cards.go cardProfile);
// accepting it (or applying it on its own, for a kind the user lets through) writes it with a write-ahead record,
// reads it back, and keeps what it replaced for undo.
//
// Release, by what the hub can do (not by its build number: hubs built from a container image report build 0):
//   - The hub takes profiles: probed at most once a day, only while a card waits, by a kb.update carrying a profile
//     that breaks the hub's rules (an inference without its basis) for the person the waiting card is about. A hub
//     with profiles refuses it before writing anything ("invalid profile"); an older one ignores the field, which
//     kb.update's ignored says or the absence of a refusal shows. Nothing is written to a person to find out.
//   - The user's evaluations (views) and the inferred profile (inferences) are written only with the hub's privacy
//     filter that keeps them from conversations writing to other people (ProfileViewsReleased, #6119); without it a
//     written profile carries only facts and commitments and the page says the rest can be seen only there.
// Until the hub takes profiles the proposals stay blocked and the page says so. A write that cannot be read back is
// taken as a hub without profiles: blocked again, with a warning, probed again the next day. Evaluations and
// inferences never fall back to the pinned facts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// ProfileViewsReleased: the user's evaluations and the inferred profile are written to the knowledge base too, not
// only facts and commitments. The hub's privacy filter (nwverse/persona#6119: conversations that write to other
// people, such as inbox drafting, the Lark agent and extension-opened conversations, never read a profile's private
// parts) is what makes this safe; the manifest's engines.persona asks for a hub built with it (>=202611122324).
const ProfileViewsReleased = true

// profileViewsReleased is ProfileViewsReleased; tests set it.
var profileViewsReleased = ProfileViewsReleased

// profileCapKey keeps the last probe in State.Cursors: "yes@<day>" or "no@<day>".
const profileCapKey = "hub.person_profile"

// ProfileNeeds is what a blocked person_card proposal waits for.
const ProfileNeeds = "hub：人物分区块画像（kb.update profile）"

// profileReadbackNeeds is the Needs of a proposal whose write could not be read back on the day given.
const profileReadbackNeeds = "写入人物画像后回读不到（%s），当作 hub 还不支持，第二天再试"

// ProfilesReleased says whether the hub takes person profiles, as the last probe found.
func (c *Curator) ProfilesReleased() bool {
	return strings.HasPrefix(c.St.S.Cursors[profileCapKey], "yes@")
}

// ProfileViewsWritten says whether evaluations and inferences go to the knowledge base (the privacy filter's release).
func ProfileViewsWritten() bool { return profileViewsReleased }

// CheckProfileCapability probes the hub for person profiles, at most once a day and only while a card waits for
// it. It takes the store's lock itself, around the state only.
func (c *Curator) CheckProfileCapability(ctx context.Context) {
	today := c.today()
	c.St.Lock()
	last := c.St.S.Cursors[profileCapKey]
	target := ""
	for _, p := range c.St.S.Proposals {
		if p.Kind == "person_card" && p.Status == "blocked" {
			var ch Change
			if json.Unmarshal(p.Change, &ch) == nil && ch.Profile != nil && ch.Update != nil {
				target = ch.Update.ID
				break
			}
		}
	}
	c.St.Unlock()
	if target == "" || strings.HasSuffix(last, "@"+today) {
		return
	}
	pr := kbsrc.ProberOf(c.Src)
	if pr == nil {
		return
	}
	ok, err := pr.ProbeProfile(ctx, target)
	if err != nil {
		c.logf("person profile probe: %v", err)
		return
	}
	v := "no@" + today
	if ok {
		v = "yes@" + today
	}
	c.St.Lock()
	c.St.S.Cursors[profileCapKey] = v
	c.St.Unlock()
}

// readbackFailedOn is the day a proposal's write could not be read back on ("": not that).
func readbackFailedOn(p *store.Proposal) string {
	prefix, _, _ := strings.Cut(profileReadbackNeeds, "%s")
	rest, ok := strings.CutPrefix(p.Needs, prefix)
	if !ok {
		return ""
	}
	day, _, _ := strings.Cut(rest, "）")
	return day
}

// ReleaseProfiles makes blocked person_card proposals pending once the hub is released for them (a proposal that
// could not be read back waits for another hub build), and expires the older ones written as a summary and log.
// It answers how many it released. Call it with the store's lock held.
func (c *Curator) ReleaseProfiles() int {
	n := 0
	released := c.ProfilesReleased()
	for _, p := range c.St.S.Proposals {
		if p.Kind != "person_card" || p.Status != "blocked" {
			continue
		}
		var ch Change
		_ = json.Unmarshal(p.Change, &ch)
		if ch.Profile == nil {
			p.Status, p.Decided = "expired", c.today() // P9's summary-and-log card: the hub never took it
			continue
		}
		if !released {
			continue
		}
		if p.Needs == ProfileNeeds || (readbackFailedOn(p) != "" && readbackFailedOn(p) != c.today()) {
			p.Status, p.Needs = "pending", ""
			n++
		}
	}
	return n
}

// ProfileWarnings are the warnings of profile writes: ones the hub did not keep, and fields it did not use.
func (c *Curator) ProfileWarnings() []string {
	var out []string
	for _, p := range c.St.S.Proposals {
		if p.Kind == "person_card" && p.Status == "blocked" && readbackFailedOn(p) != "" {
			out = append(out, p.Target+"："+p.Needs)
		}
	}
	return out
}

// errProfileNotKept: the hub answered a profile write but kb.get shows no profile of this extension.
var errProfileNotKept = errors.New("profile not read back")

// applyProfile is the person_card part of apply.
func (c *Curator) applyProfile(ctx context.Context, p *store.Proposal, ch Change, auto bool, run string, durable bool) (string, error) {
	id, err := c.writeProfile(ctx, p, ch, auto, run, durable)
	if errors.Is(err, errProfileNotKept) {
		p.Status, p.Needs = "blocked", fmt.Sprintf(profileReadbackNeeds, c.today())
		c.St.S.Cursors[profileCapKey] = "no@" + c.today()
		c.logf("person profile %s: written, not read back; taken as a hub without profiles", p.Target)
		return "hub 没有存下画像（回读不到），当作还不支持，已改回等待", nil
	}
	if err != nil {
		return "", err
	}
	p.ApplyLog = id
	return "", nil
}

// profileTarget is the person id of a person_card proposal's change.
func profileTarget(ch Change) (string, error) {
	if ch.Update == nil || ch.Update.Kind != "people" || ch.Update.ID == "" {
		return "", errors.New("提议里没有要写的人物")
	}
	return ch.Update.ID, nil
}

func (c *Curator) profileWriter() (kbsrc.ProfileWriter, error) {
	w := kbsrc.ProfilesOf(c.Src)
	if w == nil {
		return nil, errors.New("知识库来源不能写人物画像")
	}
	return w, nil
}

// writeProfile writes a person_card proposal's profile with a write-ahead record (before: this extension's
// profile as read before the write, replaced by kb.update's own before.profile once it answers), reads it back,
// and records the write. Fields the hub did not use are warned about.
func (c *Curator) writeProfile(ctx context.Context, p *store.Proposal, ch Change, auto bool, run string, durable bool) (string, error) {
	pid, err := profileTarget(ch)
	if err != nil {
		return "", err
	}
	pw, err := c.profileWriter()
	if err != nil {
		return "", err
	}
	var w *store.ProposalWAL
	recovering := false
	if durable {
		if w, err = c.St.ReadProposalWAL(p.ID); err != nil {
			return "", err
		}
		recovering = w != nil
	}
	if w == nil {
		cur, err := pw.ReadProfile(ctx, pid)
		if err != nil {
			return "", fmt.Errorf("读 people/%s 的画像：%w", pid, err)
		}
		bj, _ := json.Marshal(map[string]json.RawMessage{"profile": profileJSON(cur)})
		next := c.St.S.NextApplied
		if durable {
			pending, err := c.St.PendingProposalWALs()
			if err != nil {
				return "", err
			}
			for _, x := range pending {
				next = max(next, x.NextApplied)
			}
		}
		next++
		status := "accepted"
		if auto {
			status = "auto"
		}
		proposal := *p
		w = &store.ProposalWAL{Proposal: p.ID, ProposalState: &proposal, Status: status, NextProp: c.St.S.NextProp, NextApplied: next,
			Applied: &store.Applied{ID: fmt.Sprintf("A-%04d", next), Proposal: p.ID, Run: run, At: c.now().UTC().Format(time.RFC3339),
				Kind: p.Kind, Entity: "people/" + pid, Before: bj, Update: kbsrc.ProfileUpdateJSON(pid, ch.Profile)}}
		if durable {
			if err := c.St.WriteProposalWAL(w); err != nil {
				return "", err
			}
		}
	}
	before, ignored, err := pw.UpdateProfile(ctx, pid, outgoingProfile(ch.Profile))
	if err != nil {
		return "", err
	}
	if !recovering && len(before) > 0 {
		// The hub's own before.profile is what undo writes back; a replayed write would answer with itself.
		w.Applied.Before, _ = json.Marshal(map[string]json.RawMessage{"profile": before})
	}
	if len(ignored) > 0 {
		var parts []string
		for _, f := range ignored {
			parts = append(parts, f.Field+"（"+f.Reason+"）")
		}
		msg := "hub 没用上：" + strings.Join(parts, "、")
		c.logf("person profile %s: %s", p.Target, msg)
		if !strings.Contains(p.Detail, msg) {
			p.Detail = strings.TrimSpace(p.Detail + "；" + msg)
		}
	}
	back, err := pw.ReadProfile(ctx, pid)
	if err != nil {
		return "", fmt.Errorf("回读 people/%s 的画像：%w", pid, err)
	}
	if back == nil && !outgoingProfile(ch.Profile).Empty() {
		return "", errProfileNotKept
	}
	aj := profileJSON(back)
	a := w.Applied
	a.After = aj
	a.StateSum = hash(string(aj))
	if w.NextApplied > c.St.S.NextApplied {
		c.St.S.NextApplied = w.NextApplied
	}
	c.St.S.Applied = append(c.St.S.Applied, a)
	return a.ID, nil
}

// profileJSON is a profile as recorded (null for none).
func profileJSON(p *kbsrc.PersonProfile) json.RawMessage {
	if p == nil || p.Empty() {
		return json.RawMessage("null")
	}
	b, _ := json.Marshal(p)
	return b
}

// undoProfile writes back the profile a person_card write replaced (none: an empty profile, which removes it),
// after checking the profile is still the one written, and reads it back.
func (c *Curator) undoProfile(ctx context.Context, a *store.Applied) error {
	var up struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(a.Update, &up); err != nil || up.ID == "" {
		return fmt.Errorf("撤销 %s：没有人物", a.ID)
	}
	pw, err := c.profileWriter()
	if err != nil {
		return err
	}
	cur, err := pw.ReadProfile(ctx, up.ID)
	if err != nil {
		return err
	}
	if hash(string(profileJSON(cur))) != a.StateSum {
		return ErrChanged
	}
	var before map[string]json.RawMessage
	_ = json.Unmarshal(a.Before, &before)
	prev := &kbsrc.PersonProfile{}
	if raw := before["profile"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, prev); err != nil {
			return fmt.Errorf("撤销 %s：%w", a.ID, err)
		}
	}
	if _, _, err := pw.UpdateProfile(ctx, up.ID, prev); err != nil {
		return err
	}
	back, err := pw.ReadProfile(ctx, up.ID)
	if err != nil {
		return err
	}
	if (back == nil) != prev.Empty() {
		return fmt.Errorf("撤销 %s：回读的画像和撤销后应有的不一致", a.ID)
	}
	return nil
}

// ProfileDelivery is whether a person's card reached their person page, as the plugin page shows it.
type ProfileDelivery struct {
	Status  string // none | blocked | readback_failed | shadow | pending | written | undone | rejected
	Day     string // when written, undone or decided
	Applied string // the write (A-…), when written
}

// ProfileDeliveryOf is the latest person_card proposal's state for a person entry. Call it with the store's lock held.
func (c *Curator) ProfileDeliveryOf(personID string) ProfileDelivery {
	return ProfileDeliveryIn(c.St.S, personID)
}

// ProfileDeliveryIn is ProfileDeliveryOf on a state.
func ProfileDeliveryIn(s *store.State, personID string) ProfileDelivery {
	var last *store.Proposal
	for _, p := range s.Proposals {
		if p.Kind == "person_card" && p.Target == "people/"+personID && p.Status != "expired" {
			last = p
		}
	}
	if last == nil {
		return ProfileDelivery{Status: "none"}
	}
	switch last.Status {
	case "blocked":
		if readbackFailedOn(last) != "" {
			return ProfileDelivery{Status: "readback_failed"}
		}
		return ProfileDelivery{Status: "blocked"}
	case "pending", "later":
		if last.Shadow {
			return ProfileDelivery{Status: "shadow"}
		}
		return ProfileDelivery{Status: "pending"}
	case "accepted", "auto":
		return ProfileDelivery{Status: "written", Day: last.Decided, Applied: last.ApplyLog}
	case "undone":
		return ProfileDelivery{Status: "undone", Day: last.Decided}
	}
	return ProfileDelivery{Status: last.Status, Day: last.Decided}
}

// outgoingProfile is the part of a card's profile written to the hub: facts and commitments always; the user's
// evaluations and the inferred profile only once the hub's privacy filter is released (ProfileViewsReleased).
func outgoingProfile(p *kbsrc.PersonProfile) *kbsrc.PersonProfile {
	if p == nil || profileViewsReleased {
		return p
	}
	out := *p
	out.Views, out.Inferences = nil, nil
	return &out
}
