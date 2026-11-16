package curate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Proposal kinds, their risk, and whether they write to the knowledge base.
var ProposalKinds = map[string]struct {
	Risk  string
	Title string
	KB    bool
}{
	"pin_fact":        {"medium", "记成已确认的事实", true},
	"pin_practice":    {"medium", "记成做事方式", true},
	"pin_knowledge":   {"medium", "记成知识", true},
	"mail_downgrade":  {"low", "营销 / 订阅邮件降级", true},
	"remove_alias":    {"low", "删掉不像名字的别名", true},
	"remove_members":  {"medium", "项目成员瘦身", true},
	"project_pause":   {"high", "项目长期没动静，改成暂停", true},
	"not_a_person":    {"high", "这不是一个人", true},
	"owner_as_person": {"high", "这个人其实是你", true},
	"item_conflict":   {"medium", "两条说法矛盾", false},
	"graph_conflict":  {"medium", "关系矛盾", false},
	"graph_merge":     {"medium", "可能是同一个", false},
}

// Change is what a proposal does when accepted.
type Change struct {
	Update  *wire.KBUpdateParams `json:"update,omitempty"`
	PinLine string               `json:"pin_line,omitempty"` // pin_*: the line appended to the entity's confirmed facts
	From    string               `json:"from,omitempty"`     // graph_merge
	Into    string               `json:"into,omitempty"`
	IDs     []string             `json:"ids,omitempty"` // conflicts: the items or assertions to choose among
	// Profile is person_card's: the person's profile written with Update's kind and id (profiles.go).
	Profile *kbsrc.PersonProfile `json:"profile,omitempty"`
}

func (c *Curator) propose(kind, title, detail, target string, ch Change, items []string, ev []store.Evidence, needs string) *store.Proposal {
	spec := ProposalKinds[kind]
	raw, _ := json.Marshal(ch)
	key := hash(kind, target, string(raw))[:16]
	for _, p := range c.St.S.Proposals {
		if p.Key == key {
			if (p.Status == "accepted" || p.Status == "auto") && c.outgoingChanged(target, ch.Profile) {
				break // a privacy-release migration needs a new proposal, not the old accepted hash
			}
			return p // the same proposal is never made twice, whatever became of the first
		}
	}
	c.St.S.NextProp++
	p := &store.Proposal{ID: fmt.Sprintf("K-%04d", c.St.S.NextProp), Kind: kind, Risk: spec.Risk, Title: title, Detail: detail,
		Target: target, Change: raw, Items: items, Evidence: ev, Key: key, Status: "pending", Created: c.today(),
		Shadow: c.Cfg.Shadow(c.St.S.Installed, c.now())}
	if needs != "" {
		p.Status, p.Needs = "blocked", needs
	}
	c.St.S.Proposals = append(c.St.S.Proposals, p)
	return p
}

// outgoingChanged says whether the profile actually left on the hub by an accepted write differs from the payload now
// allowed. The stored full Change can be identical while the privacy filter changed what may actually be written.
func (c *Curator) outgoingChanged(target string, now *kbsrc.PersonProfile) bool {
	last := c.lastProfile(target)
	if last == nil {
		return now != nil && !now.Empty()
	}
	a, _ := json.Marshal(last)
	b, _ := json.Marshal(outgoingProfile(now))
	return string(a) != string(b)
}

func (c *Curator) proposeMailDowngrade(u *Unit, category string) {
	if u.Meta.Relevance <= 10 {
		return
	}
	rel := 5
	lock := false
	c.propose("mail_downgrade", "「"+u.Meta.Name+"」是"+map[string]string{"marketing": "营销邮件", "newsletter": "订阅邮件"}[category],
		fmt.Sprintf("相关性 %d → %d，不锁定（hub 以后还能改）", u.Meta.Relevance, rel), u.Ref,
		Change{Update: &wire.KBUpdateParams{Kind: "chats", ID: u.Meta.ID, Relevance: &rel, Lock: &lock}}, nil, nil, "")
}

func (c *Curator) proposeGraphConflict(b *graph.Builder, id string) {
	a := b.G.Assertions[id]
	var ids []string
	var ev []store.Evidence
	for _, o := range b.G.Assertions {
		if o.S == a.S && o.P == a.P && o.Status == "candidate" && o.ValidFrom == a.ValidFrom {
			ids = append(ids, o.ID)
			ev = append(ev, o.Evidence...)
		}
	}
	sort.Strings(ids)
	if len(ids) < 2 {
		return
	}
	c.propose("graph_conflict", fmt.Sprintf("「%s」%s 同一天有 %d 种说法", nodeName(b.G, a.S), a.P, len(ids)), "选一条为准，或都不对",
		"graph/"+a.S, Change{IDs: ids}, nil, ev, "")
}

func nodeName(g *store.Graph, id string) string {
	if n := g.Nodes[g.Resolve(id)]; n != nil {
		return n.Name
	}
	return id
}

// expire: a proposal shown and left alone for proposal_expiry_days lapses, and nothing changes (the clock starts
// when it was first shown, so nothing lapses unseen).
func (c *Curator) expire() int {
	n := 0
	cut := addDays(c.today(), -c.Cfg.ProposalExpiryDays)
	for _, p := range c.St.S.Proposals {
		if (p.Status == "pending" || p.Status == "later") && p.Shown != "" && p.Shown <= cut {
			p.Status, p.Decided = "expired", c.today()
			n++
		}
	}
	return n
}

// MarkShown starts the expiry clock of proposals a page displayed.
func (c *Curator) MarkShown(ids []string) {
	c.St.Lock()
	defer c.St.Unlock()
	changed := false
	for _, id := range ids {
		if p := c.St.S.Proposal(id); p != nil && p.Shown == "" {
			p.Shown = c.today()
			changed = true
		}
	}
	if changed {
		_ = c.St.Save()
	}
}

// Decide records the user's answer: accept, reject, later, right / wrong (a verdict in shadow mode, or a spot
// check), keep:<id> (conflicts), or edit with a new wording for a pin.
func (c *Curator) Decide(ctx context.Context, id, action, value string) (string, error) {
	c.St.Lock()
	defer c.St.Unlock()
	p := c.St.S.Proposal(id)
	if p == nil {
		return "", fmt.Errorf("没有 %s", id)
	}
	var msg string
	checkpoint := false
	switch {
	case action == "right" || action == "wrong":
		p.Verdict = action
		msg = "记下了"
	case action == "later":
		p.Status = "later"
		msg = "稍后再看"
	case action == "reject":
		p.Status, p.Decided = "rejected", c.today()
		if p.Shadow && p.Verdict == "" {
			p.Verdict = "wrong"
		}
		msg = "不改"
	case action == "accept" || action == "edit" || strings.HasPrefix(action, "keep:"):
		if p.Status != "pending" && p.Status != "later" {
			return "", fmt.Errorf("%s 已经是「%s」", id, p.Status)
		}
		if p.Shadow && ProposalKinds[p.Kind].KB {
			p.Verdict = "right"
			msg = "影子期：记成判对，不写知识库"
			break
		}
		if action == "edit" {
			var ch Change
			_ = json.Unmarshal(p.Change, &ch)
			ch.PinLine = strings.TrimSpace(value)
			p.Change, _ = json.Marshal(ch)
		}
		var err error
		msg, err = c.apply(ctx, p, action, false, "manual", true)
		var conflict *kbsrc.Conflict
		if errors.As(err, &conflict) {
			return conflict.Error() + "。已记下，交给你决定", c.checkpointProposal(p)
		}
		if err != nil {
			return "", err
		}
		checkpoint = true
	default:
		return "", fmt.Errorf("不认识的操作 %q", action)
	}
	if checkpoint {
		return msg, c.checkpointProposal(p)
	}
	return msg, c.St.Save()
}

// checkpointProposal makes an application durable before clearing its write-ahead record. Call it with the lock held.
func (c *Curator) checkpointProposal(p *store.Proposal) error {
	if err := c.St.Save(); err != nil {
		return err
	}
	return c.St.CompleteProposalWAL(p.ID)
}

// apply carries out a proposal: a write to the knowledge base with a snapshot of what it replaces, or a change to
// the curator's own items and graph. durable is false only for a dry weekly pass backed by a recording source.
func (c *Curator) apply(ctx context.Context, p *store.Proposal, action string, auto bool, run string, durable bool) (string, error) {
	var ch Change
	if err := json.Unmarshal(p.Change, &ch); err != nil {
		return "", err
	}
	if p.Needs != "" {
		return "", fmt.Errorf("要等 hub 支持 %s", p.Needs)
	}
	status := "accepted"
	if auto {
		status = "auto"
	}
	switch p.Kind {
	case "item_conflict":
		keep := strings.TrimPrefix(action, "keep:")
		for _, id := range ch.IDs {
			if it := c.St.S.Items[id]; it != nil && id != keep {
				it.Status = "superseded"
			} else if it != nil {
				it.Status = "active"
				it.Pinned = true
			}
		}
	case "graph_conflict":
		keep := strings.TrimPrefix(action, "keep:")
		for _, id := range ch.IDs {
			if a := c.St.S.Graph.Assertions[id]; a != nil {
				if id == keep {
					a.Status, a.Source = "active", "user"
				} else {
					a.Status = "rejected"
				}
			}
		}
	case "graph_merge":
		b := &graph.Builder{G: c.St.S.Graph, Today: c.today()}
		if err := b.Merge(ch.From, ch.Into, p.ID); err != nil {
			return "", err
		}
		c.St.S.NextApplied++
		a := &store.Applied{ID: fmt.Sprintf("A-%04d", c.St.S.NextApplied), Proposal: p.ID, Run: run, At: c.now().UTC().Format(time.RFC3339),
			Kind: "graph_merge", Entity: "graph/" + ch.From}
		c.St.S.Applied = append(c.St.S.Applied, a)
		p.ApplyLog = a.ID
	case "person_card":
		if ch.Profile == nil {
			return "", errors.New("提议里没有画像（旧的摘要写法，hub 不收）")
		}
		msg, err := c.applyProfile(ctx, p, ch, auto, run, durable)
		if err != nil {
			return "", err
		}
		if msg != "" {
			return msg, nil // not kept by the hub: blocked again
		}
	default:
		if ch.Update == nil {
			return "", errors.New("提议里没有要写的内容")
		}
		id, err := c.write(ctx, p, ch, auto, run, durable)
		if err != nil {
			return "", err
		}
		p.ApplyLog = id
		for _, iid := range p.Items {
			if it := c.St.S.Items[iid]; it != nil {
				it.Pinned = true
			}
		}
	}
	p.Status, p.Decided = status, c.today()
	return "已合入", nil
}

func touched(u wire.KBUpdateParams) []string { return kbsrc.Fields(u) }

func pick(meta json.RawMessage, fields []string) map[string]any {
	all := map[string]any{}
	_ = json.Unmarshal(meta, &all)
	out := map[string]any{}
	for _, f := range fields {
		out[f] = all[f]
	}
	return out
}

func (c *Curator) prepareWrite(ctx context.Context, p *store.Proposal, ch Change, auto bool, run string, durable bool) (*store.ProposalWAL, error) {
	up := *ch.Update
	var cur wire.KBDetail
	haveCur := false
	if ch.PinLine != "" {
		var err error
		cur, err = c.Src.Get(ctx, up.Kind, up.ID)
		if err != nil {
			return nil, fmt.Errorf("读 %s/%s：%w", up.Kind, up.ID, err)
		}
		haveCur = true
		old := kbsrc.DecodeMeta(cur).Pinned
		next := old
		if !strings.Contains(old, ch.PinLine) {
			next = strings.TrimSpace(strings.TrimSpace(old) + "\n" + ch.PinLine)
		}
		up.Pinned = &next
	}
	if auto {
		f := false
		up.Lock = &f // what the curator did on its own is a suggestion the hub's pipeline may replace
	}
	// The lines the value rests on, so the entity shows where the curator's write came from.
	for _, e := range p.Evidence {
		kind, id, _ := strings.Cut(e.Chat, "/")
		if kind == "chats" && len(up.Evidence) < 8 {
			up.Evidence = append(up.Evidence, wire.KBEvidence{Chat: id, Day: e.Day, N: e.N})
		}
	}
	if !haveCur {
		var err error
		cur, err = c.Src.Get(ctx, up.Kind, up.ID)
		if err != nil {
			return nil, fmt.Errorf("读 %s/%s：%w", up.Kind, up.ID, err)
		}
	}
	fields := touched(up)
	before := pick(cur.Meta, fields)
	free := map[string]bool{} // fields nobody held before this write: undo hands them back
	for _, f := range fields {
		free[f] = !c.holds(up.Kind+"/"+up.ID, f)
	}
	bj, _ := json.Marshal(before)
	uj, _ := json.Marshal(up)
	fj, _ := json.Marshal(free)
	next := c.St.S.NextApplied
	if durable {
		pending, err := c.St.PendingProposalWALs()
		if err != nil {
			return nil, err
		}
		for _, w := range pending {
			if w.NextApplied > next {
				next = w.NextApplied
			}
		}
	}
	next++
	status := "accepted"
	if auto {
		status = "auto"
	}
	proposal := *p
	w := &store.ProposalWAL{Proposal: p.ID, ProposalState: &proposal, Status: status, NextProp: c.St.S.NextProp, NextApplied: next, Applied: &store.Applied{
		ID: fmt.Sprintf("A-%04d", next), Proposal: p.ID, Run: run, At: c.now().UTC().Format(time.RFC3339),
		Kind: p.Kind, Entity: up.Kind + "/" + up.ID, Before: bj, Update: uj, Free: fj,
	}}
	if durable {
		if err := c.St.WriteProposalWAL(w); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (c *Curator) write(ctx context.Context, p *store.Proposal, ch Change, auto bool, run string, durable bool) (string, error) {
	var w *store.ProposalWAL
	var err error
	if durable {
		w, err = c.St.ReadProposalWAL(p.ID)
		if err != nil {
			return "", err
		}
	}
	if w == nil {
		w, err = c.prepareWrite(ctx, p, ch, auto, run, durable)
		if err != nil {
			return "", err
		}
	}
	var up wire.KBUpdateParams
	if err := json.Unmarshal(w.Applied.Update, &up); err != nil {
		return "", err
	}
	// Replaying the recorded update is safe; its returned before-image may describe the first write, so the WAL's
	// original before-image remains authoritative for undo. A dry pass keeps the recording source's old response.
	updateBefore, err := c.Src.Update(ctx, up)
	var conflict *kbsrc.Conflict
	if errors.As(err, &conflict) {
		// A question for the user, not an error to retry: the proposal says who holds what.
		p.Status, p.Needs = "held", conflict.Error()
		return "", conflict
	}
	if err != nil {
		return "", err
	}
	if !durable {
		bj, _ := json.Marshal(updateBefore)
		w.Applied.Before = bj
	}
	after := map[string]any{}
	if d, err := c.Src.Get(ctx, up.Kind, up.ID); err == nil {
		after = pick(d.Meta, touched(up))
	}
	aj, _ := json.Marshal(after)
	a := w.Applied
	a.After = aj
	a.StateSum = hash(string(aj))
	if w.NextApplied > c.St.S.NextApplied {
		c.St.S.NextApplied = w.NextApplied
	}
	c.St.S.Applied = append(c.St.S.Applied, a)
	return a.ID, nil
}

// holds says whether an earlier write of the curator (with a lock, not undone) still holds a field of an entity.
func (c *Curator) holds(entity, field string) bool {
	for _, a := range c.St.S.Applied {
		if a.Entity != entity || a.Undone != "" {
			continue
		}
		var up wire.KBUpdateParams
		if json.Unmarshal(a.Update, &up) != nil || (up.Lock != nil && !*up.Lock) {
			continue
		}
		if slices.Contains(touched(up), field) {
			return true
		}
	}
	return false
}

// ErrChanged: the entity is not as the curator left it; undo would overwrite someone else's change.
var ErrChanged = errors.New("这一处在合入之后又被改过，没有覆盖")

// Undo reverses one write. It first checks that the touched fields are still what the curator wrote.
func (c *Curator) Undo(ctx context.Context, appliedID string) error {
	c.St.Lock()
	defer c.St.Unlock()
	var a *store.Applied
	for _, x := range c.St.S.Applied {
		if x.ID == appliedID {
			a = x
		}
	}
	if a == nil || a.Undone != "" {
		return fmt.Errorf("没有可撤销的 %s", appliedID)
	}
	if err := c.undo(ctx, a); err != nil {
		return err
	}
	return c.St.Save()
}

// UndoRun reverses every write of a run, newest first; writes changed since are skipped and reported.
func (c *Curator) UndoRun(ctx context.Context, run string) (undone int, skipped []string, err error) {
	c.St.Lock()
	defer c.St.Unlock()
	for i := len(c.St.S.Applied) - 1; i >= 0; i-- {
		a := c.St.S.Applied[i]
		if a.Run != run || a.Undone != "" {
			continue
		}
		if e := c.undo(ctx, a); e != nil {
			skipped = append(skipped, a.ID+"："+e.Error())
			continue
		}
		undone++
	}
	return undone, skipped, c.St.Save()
}

func (c *Curator) undo(ctx context.Context, a *store.Applied) error {
	p := c.St.S.Proposal(a.Proposal)
	if a.Kind == "graph_merge" {
		var ch Change
		if p != nil {
			_ = json.Unmarshal(p.Change, &ch)
		}
		b := &graph.Builder{G: c.St.S.Graph, Today: c.today()}
		if err := b.Unmerge(ch.From); err != nil {
			return err
		}
	} else if a.Kind == "person_card" {
		if err := c.undoProfile(ctx, a); err != nil {
			return err
		}
	} else {
		var up wire.KBUpdateParams
		if err := json.Unmarshal(a.Update, &up); err != nil {
			return err
		}
		fields := touched(up)
		cur, err := c.Src.Get(ctx, up.Kind, up.ID)
		if err != nil {
			return err
		}
		now, _ := json.Marshal(pick(cur.Meta, fields))
		if hash(string(now)) != a.StateSum {
			return ErrChanged
		}
		// Undo is another write with the previous values (kb.update's before), handing back the fields that were free.
		before := map[string]json.RawMessage{}
		_ = json.Unmarshal(a.Before, &before)
		free := map[string]bool{}
		_ = json.Unmarshal(a.Free, &free)
		f := false
		inv := wire.KBUpdateParams{Kind: up.Kind, ID: up.ID, Lock: &f}
		unlock := func(field string) {
			if free[field] {
				inv.Unlock = append(inv.Unlock, field)
			}
		}
		if up.Pinned != nil {
			var old string
			_ = json.Unmarshal(before["pinned"], &old)
			if old == "" && free["pinned"] {
				inv.Unlock = append(inv.Unlock, "pinned") // unlocking pinned clears it
			} else {
				inv.Pinned = &old
			}
		}
		if up.Relevance != nil {
			var r float64
			_ = json.Unmarshal(before["relevance"], &r)
			ri := int(r)
			inv.Relevance = &ri
			unlock("relevance")
		}
		if up.Status != "" {
			_ = json.Unmarshal(before["status"], &inv.Status)
			unlock("status")
		}
		if len(up.RemovePeople) > 0 {
			inv.People = up.RemovePeople
			unlock("people")
		}
		if len(up.RemoveAliases) > 0 {
			inv.Aliases = up.RemoveAliases
			unlock("aliases")
		}
		if _, err := c.Src.Update(ctx, inv); err != nil {
			var conflict *kbsrc.Conflict
			if errors.As(err, &conflict) {
				return ErrChanged
			}
			return err
		}
	}
	a.Undone = c.today()
	if p != nil {
		p.Status = "undone"
		for _, iid := range p.Items {
			if it := c.St.S.Items[iid]; it != nil {
				it.Pinned = false
			}
		}
	}
	return nil
}

// Precision is, per proposal kind, the share judged right among those judged (verdicts, and accepts / rejects).
func (c *Curator) Precision() map[string][2]int {
	out := map[string][2]int{}
	for _, p := range c.St.S.Proposals {
		v := out[p.Kind]
		switch {
		case p.Verdict == "right" || p.Status == "accepted":
			v[0]++
			v[1]++
		case p.Verdict == "wrong" || p.Status == "rejected":
			v[1]++
		}
		out[p.Kind] = v
	}
	return out
}

// Recover finishes proposal writes left between kb.update and their state checkpoint. One failed record stays on
// disk for a later retry and does not stop the others. Callers log a store-level read failure; per-record failures
// are logged here.
func (c *Curator) Recover(ctx context.Context) error {
	c.St.Lock()
	defer c.St.Unlock()
	pending, err := c.St.PendingProposalWALs()
	if err != nil {
		return err
	}
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].NextApplied != pending[j].NextApplied {
			return pending[i].NextApplied < pending[j].NextApplied
		}
		return pending[i].Proposal < pending[j].Proposal
	})
	for _, w := range pending {
		if err := c.recoverProposal(ctx, w); err != nil {
			c.logf("proposal %s recovery failed; write-ahead record kept for retry: %v", w.Proposal, err)
		}
	}
	return nil
}

func (c *Curator) recoverProposal(ctx context.Context, w *store.ProposalWAL) error {
	p := c.St.S.Proposal(w.Proposal)
	if p == nil {
		// A weekly pass can make its first automatic write before the new proposal itself reached state.json.
		p = w.ProposalState
		c.St.S.Proposals = append(c.St.S.Proposals, p)
	}
	if w.NextProp > c.St.S.NextProp {
		c.St.S.NextProp = w.NextProp
	}
	// An Applied entry already in state.json means the checkpoint won the race with cleanup, so only the stale
	// write-ahead record needs removing.
	for _, a := range c.St.S.Applied {
		if a.ID == w.Applied.ID && a.Proposal == w.Proposal {
			return c.St.CompleteProposalWAL(w.Proposal)
		}
	}
	_, err := c.apply(ctx, p, "accept", w.Status == "auto", w.Applied.Run, true)
	var conflict *kbsrc.Conflict
	if err != nil && !errors.As(err, &conflict) {
		return fmt.Errorf("恢复提议 %s：%w", w.Proposal, err)
	}
	return c.checkpointProposal(p)
}

// autoApply applies pending proposals of the kinds the user switched on, once that kind has proven itself (at
// least 10 judged, precision at or above the threshold), never high-risk ones, never in shadow mode.
func (c *Curator) autoApply(ctx context.Context, run string, checkpoint bool) (applied int) {
	c.ReleaseProfiles()
	if c.Cfg.Shadow(c.St.S.Installed, c.now()) || len(c.Cfg.AutoApplyKinds) == 0 {
		return 0
	}
	prec := c.Precision()
	for _, p := range c.St.S.Proposals {
		if p.Status != "pending" || p.Shadow || p.Risk == "high" || !slices.Contains(c.Cfg.AutoApplyKinds, p.Kind) {
			continue
		}
		v := prec[p.Kind]
		if v[1] < 10 || float64(v[0])/float64(v[1]) < c.Cfg.AutoApplyMinPrecision {
			continue
		}
		if flagged(c.St.S, p) {
			continue
		}
		_, err := c.apply(ctx, p, "accept", true, run, checkpoint)
		if err == nil && p.Status == "auto" {
			applied++
		}
		var conflict *kbsrc.Conflict
		if checkpoint && (err == nil || errors.As(err, &conflict)) {
			if err := c.checkpointProposal(p); err != nil {
				c.logf("proposal %s checkpoint: %v", p.ID, err)
			}
		}
	}
	return applied
}

func flagged(s *store.State, p *store.Proposal) bool {
	for _, id := range p.Items {
		if it := s.Items[id]; it != nil && len(it.Flags) > 0 {
			return true
		}
	}
	return false
}

// Unacked are what the curator applied by itself and the user has not looked at.
func (c *Curator) Unacked() []*store.Proposal {
	var out []*store.Proposal
	for _, p := range c.St.S.Proposals {
		if p.Status == "auto" && !p.Acked {
			out = append(out, p)
		}
	}
	return out
}

// Forget removes everything derived from a source the user had the knowledge base forget (kb.forgotten): its
// chats' and people's evidence, items left without evidence, digests, ledger entries and raw answers, the rejections
// of those calls, the report lines quoting what was removed, graph relations resting only on it, and pending
// proposals about it. Automatic migration backups are scrubbed with the same predicate. Ids of a source start with
// "<source>-".
func (c *Curator) Forget(source string) (removed int, err error) {
	c.St.Lock()
	defer c.St.Unlock()
	from := func(ref string) bool {
		_, id, _ := strings.Cut(ref, "/")
		return strings.HasPrefix(id, source+"-")
	}
	removed = c.forget(from)
	err = errors.Join(c.St.Save(), c.St.EditBackups(func(raw []byte) ([]byte, error) {
		return c.forgetBackup(raw, from)
	}))
	c.Publish()
	return removed, err
}

// forgetBackup uses the typed before/after states only to discover what Forget changed. The original JSON supplies
// every retained value, so unknown fields and absent optional fields stay as the rollback snapshot had them.
func (c *Curator) forgetBackup(raw []byte, from func(ref string) bool) ([]byte, error) {
	raw = forgetLegacyMemory(raw, from)
	before := &store.State{}
	if err := json.Unmarshal(raw, before); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	after := &store.State{}
	if err := json.Unmarshal(raw, after); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	base, err := json.Marshal(before)
	if err != nil {
		return nil, err
	}
	bc := *c
	bc.St = &store.Store{Dir: c.St.Dir, S: after, Now: c.St.Now}
	bc.forget(from)
	changed, err := json.Marshal(after)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(base, changed) {
		return raw, nil
	}
	return mergeBackupJSON(raw, base, changed)
}

// mergeBackupJSON applies known-field changes from before to after without discarding JSON fields unknown to this
// build. Original raw values are embedded directly whenever their known representation did not change.
func mergeBackupJSON(original, before, after json.RawMessage) (json.RawMessage, error) {
	if bytes.Equal(before, after) {
		return original, nil
	}
	if len(original) == 0 {
		return after, nil
	}
	originalObject, originalOK := backupObject(original)
	beforeObject, beforeOK := backupObject(before)
	afterObject, afterOK := backupObject(after)
	if originalOK && beforeOK && afterOK {
		out := make(map[string]json.RawMessage, len(originalObject))
		for k, v := range originalObject {
			out[k] = v
		}
		for k, b := range beforeObject {
			a, keep := afterObject[k]
			if !keep {
				delete(out, k)
				continue
			}
			o, exists := originalObject[k]
			if !exists && bytes.Equal(b, a) {
				continue // an absent optional field or zero value, not a change to the snapshot
			}
			merged, err := mergeBackupJSON(o, b, a)
			if err != nil {
				return nil, err
			}
			out[k] = merged
		}
		for k, a := range afterObject {
			if _, existed := beforeObject[k]; existed {
				continue
			}
			o, exists := originalObject[k]
			if !exists {
				out[k] = a
				continue
			}
			merged, err := mergeBackupJSON(o, nil, a)
			if err != nil {
				return nil, err
			}
			out[k] = merged
		}
		return marshalBackupObject(out), nil
	}

	originalArray, originalOK := backupArray(original)
	beforeArray, beforeOK := backupArray(before)
	afterArray, afterOK := backupArray(after)
	if originalOK && beforeOK && afterOK {
		return mergeBackupArray(originalArray, beforeArray, afterArray)
	}
	return after, nil
}

func backupObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return nil, false
	}
	return out, true
}

func backupArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var out []json.RawMessage
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return nil, false
	}
	return out, true
}

func mergeBackupArray(original, before, after []json.RawMessage) (json.RawMessage, error) {
	if len(original) != len(before) {
		return nil, fmt.Errorf("original and typed backup arrays differ in length")
	}
	// Most Forget edits only remove elements. Matching the unchanged typed values keeps each original element,
	// including any unknown fields in it, exactly as it was.
	merged := make([]json.RawMessage, 0, len(after))
	cursor := 0
	subsequence := true
	for _, a := range after {
		found := -1
		for i := cursor; i < len(before); i++ {
			if bytes.Equal(before[i], a) {
				found = i
				break
			}
		}
		if found < 0 {
			subsequence = false
			break
		}
		merged = append(merged, original[found])
		cursor = found + 1
	}
	if subsequence {
		return marshalBackupArray(merged), nil
	}

	// Memory events are the one array Forget can both remove from and append to while settling contradictions.
	// AddEvent identifies an event by day, kind, anchor and the other entry; use the same identity to merge it.
	if index, ids, ok := backupEventIDs(before, after); ok {
		merged = merged[:0]
		for i, a := range after {
			if j, exists := index[ids[i]]; exists {
				m, err := mergeBackupJSON(original[j], before[j], a)
				if err != nil {
					return nil, err
				}
				merged = append(merged, m)
			} else {
				merged = append(merged, a)
			}
		}
		return marshalBackupArray(merged), nil
	}

	// Proposals and runs are edited in place, never removed or reordered by Forget.
	if len(before) == len(after) {
		merged = merged[:0]
		for i, a := range after {
			m, err := mergeBackupJSON(original[i], before[i], a)
			if err != nil {
				return nil, err
			}
			merged = append(merged, m)
		}
		return marshalBackupArray(merged), nil
	}
	// A known array of scalars (for example memory status weeks) has no unknown object fields to preserve.
	scalars := true
	for _, value := range after {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			scalars = false
			break
		}
	}
	if scalars {
		return marshalBackupArray(after), nil
	}
	return nil, fmt.Errorf("cannot preserve unknown fields in changed backup array")
}

func backupEventIDs(before, after []json.RawMessage) (map[string]int, []string, bool) {
	index := make(map[string]int, len(before))
	for i, raw := range before {
		id, ok := backupEventID(raw)
		if !ok {
			return nil, nil, false
		}
		if _, duplicate := index[id]; duplicate {
			return nil, nil, false
		}
		index[id] = i
	}
	ids := make([]string, len(after))
	seen := make(map[string]bool, len(after))
	for i, raw := range after {
		id, ok := backupEventID(raw)
		if !ok || seen[id] {
			return nil, nil, false
		}
		ids[i], seen[id] = id, true
	}
	return index, ids, true
}

func backupEventID(raw json.RawMessage) (string, bool) {
	object, ok := backupObject(raw)
	if !ok {
		return "", false
	}
	day, hasDay := object["day"]
	kind, hasKind := object["kind"]
	if !hasDay || !hasKind {
		return "", false
	}
	return string(day) + "\x00" + string(kind) + "\x00" + string(object["anchor"]) + "\x00" + string(object["with"]), true
}

func marshalBackupObject(object map[string]json.RawMessage) json.RawMessage {
	keys := make([]string, 0, len(object))
	for k := range object {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []byte{'{'}
	for i, k := range keys {
		if i > 0 {
			out = append(out, ',')
		}
		key, _ := json.Marshal(k)
		out = append(out, key...)
		out = append(out, ':')
		out = append(out, object[k]...)
	}
	return append(out, '}')
}

func marshalBackupArray(array []json.RawMessage) json.RawMessage {
	out := []byte{'['}
	for i, value := range array {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, value...)
	}
	return append(out, ']')
}

// reconcile forgets every entity the curator derived data from that the knowledge base no longer has.
func (c *Curator) reconcile(ctx context.Context) int {
	gone := map[string]bool{}
	for _, d := range c.St.S.Done {
		if d.Chat == "" || gone[d.Chat] {
			continue
		}
		kind, id, _ := strings.Cut(d.Chat, "/")
		if _, err := c.Src.Get(ctx, kind, id); errors.Is(err, kbsrc.ErrNotFound) {
			gone[d.Chat] = true
		}
	}
	if len(gone) == 0 {
		return 0
	}
	return c.forget(func(ref string) bool { return gone[ref] })
}

func (c *Curator) forget(from func(ref string) bool) (removed int) {
	keepEv := func(ev []store.Evidence) []store.Evidence {
		return slices.DeleteFunc(ev, func(e store.Evidence) bool { return from(e.Chat) })
	}
	// gone is the text derived from the forgotten sources that this removes; reports that quote it lose the lines.
	var gone []string
	for id, it := range c.St.S.Items {
		n := len(it.Evidence)
		it.Evidence = keepEv(it.Evidence)
		removed += n - len(it.Evidence)
		if len(it.Evidence) == 0 {
			gone = append(gone, it.Claim)
			delete(c.St.S.Items, id)
		} else if len(it.Evidence) != n {
			it.Sources = sources(it.Evidence)
		}
	}
	// A judgement is the owner's own words about how they chose: it goes with its source, and the patterns and traits
	// built on it are counted again without it. A rule loses the lines it rests on, and goes when none is left.
	for id, j := range c.St.S.Judgements {
		if from(j.Chat) {
			gone = append(gone, j.Claim, j.Choice, j.Why)
			delete(c.St.S.Judgements, id)
			removed++
		}
	}
	for id, r := range c.St.S.Rules {
		n := len(r.Evidence)
		r.Evidence = keepEv(r.Evidence)
		removed += n - len(r.Evidence)
		if len(r.Evidence) == 0 && !r.Edited {
			gone = append(gone, r.Text)
			delete(c.St.S.Rules, id)
		}
	}
	n, texts := c.forgetEntries(from)
	removed += n
	gone = append(gone, texts...)
	for k, d := range c.St.S.Digests {
		if from(d.Chat) {
			for _, pt := range d.Points {
				gone = append(gone, pt.Text)
			}
			delete(c.St.S.Digests, k)
			removed++
		}
	}
	// A rejection is keyed by the call it came from, so it goes with that call's ledger entry; its claim is the
	// model's reading of the source.
	c.St.S.Rejections = slices.DeleteFunc(c.St.S.Rejections, func(r store.Rejection) bool {
		d := c.St.S.Done[r.Key]
		if d == nil || !from(d.Chat) {
			return false
		}
		gone = append(gone, r.Claim)
		removed++
		return true
	})
	for k, d := range c.St.S.Done {
		if from(d.Chat) {
			_ = os.Remove(filepath.Join(c.St.Dir, "raw", filepath.Base(d.Raw)))
			delete(c.St.S.Done, k)
		}
	}
	for k := range c.St.S.Triage {
		if from(k) {
			delete(c.St.S.Triage, k)
		}
	}
	if g := c.St.S.Graph; g != nil {
		for id, a := range g.Assertions {
			n := len(a.Evidence)
			a.Evidence = keepEv(a.Evidence)
			if n > 0 && len(a.Evidence) == 0 && a.Source == "extract" {
				delete(g.Assertions, id)
				removed++
			}
		}
		for _, n := range g.Nodes {
			n.Refs = slices.DeleteFunc(n.Refs, from)
			n.Aliases = slices.DeleteFunc(n.Aliases, func(a store.Alias) bool { return a.Evidence != nil && from(a.Evidence.Chat) })
		}
	}
	// The attribution checks quote the source's lines; the intersection list counted its dealings. A contact the
	// user added or removed by hand stays with its choice, without the counts.
	c.St.S.SpeakerChecks = slices.DeleteFunc(c.St.S.SpeakerChecks, func(chk entry.SpeakerCheck) bool {
		if from(chk.Ref) {
			removed++
			return true
		}
		return false
	})
	for who, ct := range c.St.S.Contacts {
		if !from("people/" + speaker.PersonID(who)) {
			continue
		}
		if ct.Manual == "" {
			delete(c.St.S.Contacts, who)
		} else {
			ct.Ties, ct.Score = map[string]int{}, 0
		}
		removed++
	}
	for _, p := range c.St.S.Proposals {
		if (p.Status == "pending" || p.Status == "later" || p.Status == "blocked") && from(p.Target) {
			p.Status, p.Decided = "expired", c.today()
		}
		p.Evidence = keepEv(p.Evidence)
	}
	for _, r := range c.St.S.Runs {
		r.Refusals = slices.DeleteFunc(r.Refusals, func(x store.Refusal) bool {
			if from(x.Unit) {
				gone = append(gone, x.Unit) // its report line names the unit by its ref
				return true
			}
			return false
		})
	}
	for _, r := range c.St.S.Runs {
		r.Report = scrubReport(r.Report, gone)
	}
	return removed
}

// forgottenNote ends a report that lost lines to a forgotten source.
const forgottenNote = "（有内容来自已经遗忘的来源，已从这份报告里删掉。）"

// scrubReport drops every line of a stored report that quotes any of the texts. A report is written from the
// counts and the state of its day, so it cannot be written again later; the lines that quoted the forgotten
// source go, and a note at the end says so.
func scrubReport(report string, texts []string) string {
	var needles []string
	for _, t := range texts {
		if t = strings.TrimSpace(t); len([]rune(t)) >= 2 {
			needles = append(needles, t)
		}
	}
	if report == "" || len(needles) == 0 {
		return report
	}
	lines := strings.Split(report, "\n")
	out := lines[:0]
	for _, l := range lines {
		if !slices.ContainsFunc(needles, func(n string) bool { return strings.Contains(l, n) }) {
			out = append(out, l)
		}
	}
	if len(out) == len(lines) {
		return report
	}
	text := strings.Join(out, "\n")
	if !strings.Contains(text, forgottenNote) {
		text = strings.TrimRight(text, "\n") + "\n\n" + forgottenNote + "\n"
	}
	return text
}

// World write-back (5.4, exit C). A ⑤ fact of the week that rests on the original words goes to the person or
// project it is about, even with a single source: then it is marked "单一来源" (entry.SingleSource) on the entry and
// in the written line, and becomes a plain fact once a second independent source says it. A fact without a quote, or
// about nobody the knowledge base has, is not written. K7 off writes nothing about others.

// singleSourceMark ends a written line that rests on one source so far.
const singleSourceMark = "单一来源"

// kbLine is the words of a line written into the knowledge base, in the output language (5.0): the knowledge base
// is read by the assistant and the user in their language, whatever language the plugin's own page uses.
type kbLine struct{ Fact, Single, Sep, Open, Close string }

func kbWords(lang string) kbLine {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return kbLine{Fact: "事实：", Single: singleSourceMark, Sep: "；", Open: "（", Close: "）"}
	case strings.HasPrefix(lang, "ja"):
		return kbLine{Fact: "事実：", Single: "単一ソース", Sep: "、", Open: "（", Close: "）"}
	}
	return kbLine{Fact: "Fact: ", Single: "single source", Sep: "; ", Open: " (", Close: ")"}
}

// worldSources counts an entry's independent sources: distinct units, one unit however often it is cited, and units
// carrying the same words (a cross-post, a forward) as one.
func worldSources(ev []entry.Evidence) int {
	var se []store.Evidence
	for _, e := range ev {
		se = append(se, store.Evidence{Chat: e.Ref, Quote: e.Quote})
	}
	return sources(se)
}

// worldTarget is the knowledge-base entity a ⑤ entry is written to: its subject, else its person, else its object
// ("person:<id>" → people/<id>, "project:<id>" → projects/<id>), when the knowledge base has it.
func worldTarget(e *entry.Entry, k *known) string {
	for _, ref := range []string{e.Subject, e.Person, e.Target, e.Object} {
		kind, id, ok := strings.Cut(ref, ":")
		if !ok {
			continue
		}
		switch {
		case kind == "person" && k.people[id] != "":
			return "people/" + id
		case kind == "project" && k.projects[id] != "":
			return "projects/" + id
		}
	}
	return ""
}

// proposeWorld proposes writing the week's ⑤ facts to their people and projects.
func (c *Curator) proposeWorld(ctx context.Context, days []string, run *store.Run) {
	if !entry.On(c.St.S.Switches, entry.K7WorldFacts) || len(days) == 0 {
		return
	}
	var k *known
	n := 0
	for _, e := range EntriesOf(c.St.S, entry.World, nil) {
		if n >= PinsPerWeek {
			break
		}
		if e.Last < days[0] || e.Last > days[len(days)-1] || e.Commitment != nil {
			continue
		}
		var quoted *entry.Evidence
		for i := range e.Evidence {
			if strings.TrimSpace(e.Evidence[i].Quote) != "" {
				quoted = &e.Evidence[i]
				break
			}
		}
		if quoted == nil {
			continue // no original words: nothing to write
		}
		if k == nil {
			k = c.knownEntities(ctx)
		}
		target := worldTarget(e, k)
		if target == "" {
			continue
		}
		e.SingleSource = worldSources(e.Evidence) < 2
		text := e.Shown()
		w := kbWords(c.language())
		mark := fmt.Sprintf("%s %s #%d", refName(quoted.Ref), quoted.Day, quoted.N)
		if e.SingleSource {
			mark += w.Sep + w.Single
		}
		kind, id, _ := strings.Cut(target, "/")
		line := "- " + w.Fact + text + w.Open + mark + w.Close
		var ev []store.Evidence
		for _, x := range e.Evidence {
			ev = append(ev, store.Evidence{Chat: x.Ref, Day: x.Day, N: x.N, Speaker: x.Speaker, Role: x.Role, Quote: x.Quote})
		}
		detail := fmt.Sprintf("写进「%s」：%d 个独立来源", target, worldSources(e.Evidence))
		if e.SingleSource {
			detail = fmt.Sprintf("写进「%s」：目前只有一个来源，标「%s」，有第二个来源后去掉", target, singleSourceMark)
		}
		p := c.propose("pin_fact", "事实："+text, detail, target,
			Change{Update: &wire.KBUpdateParams{Kind: kind, ID: id}, PinLine: line}, []string{e.ID}, ev, "")
		if p.Created == c.today() {
			n++
			run.Counts["proposal_world"]++
			if e.SingleSource {
				run.Counts["proposal_world_single_source"]++
			}
		}
	}
}

// Commitments (5.4 "值得记的事", 5.7): due within CommitmentWarnDays they go into the weekly report; past due with no
// sign of being done they are flagged PossiblyOverdue, never closed by the curator.
const (
	CommitmentWarnDays = 3
	PossiblyOverdue    = "possibly_overdue" // entry flag; the page says 「可能逾期」
)

// CommitmentsDue lists the live commitments not done: due within CommitmentWarnDays of today (the weekly report's
// "coming up"), and past due (flagged possibly overdue). Both are ordered by due date.
func CommitmentsDue(s *store.State, today string) (due, overdue []*entry.Entry) {
	for _, e := range s.Entries {
		cm := e.Commitment
		if cm == nil || cm.Due == "" || cm.Done != "" || !e.Live() {
			continue
		}
		switch {
		case cm.Due < today:
			overdue = append(overdue, e)
		case cm.Due <= addDays(today, CommitmentWarnDays):
			due = append(due, e)
		}
	}
	byDue := func(xs []*entry.Entry) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].Commitment.Due != xs[j].Commitment.Due {
				return xs[i].Commitment.Due < xs[j].Commitment.Due
			}
			return xs[i].ID < xs[j].ID
		})
	}
	byDue(due)
	byDue(overdue)
	return due, overdue
}

// markCommitments flags past-due commitments with no sign of being done as possibly overdue, and takes the flag off
// one that has since been done or moved; the status is left as it is. It returns how many are due soon and overdue.
func (c *Curator) markCommitments(today string, run *store.Run) (due, overdue int) {
	d, o := CommitmentsDue(c.St.S, today)
	late := map[string]bool{}
	now := c.now().UTC().Format(time.RFC3339)
	for _, e := range o {
		late[e.ID] = true
		if !slices.Contains(e.Flags, PossiblyOverdue) {
			e.Flags = append(e.Flags, PossiblyOverdue)
			e.Updated = now
			c.event(entry.Event{At: now, Run: run.ID, Kind: entry.EvStatus, Entry: e.ID, From: string(e.Status), To: string(e.Status), Detail: PossiblyOverdue})
		}
	}
	for _, e := range c.St.S.Entries {
		if !late[e.ID] && slices.Contains(e.Flags, PossiblyOverdue) {
			e.Flags = slices.DeleteFunc(e.Flags, func(f string) bool { return f == PossiblyOverdue })
			e.Updated = now
			c.event(entry.Event{At: now, Run: run.ID, Kind: entry.EvStatus, Entry: e.ID, From: string(e.Status), To: string(e.Status), Detail: "not_overdue"})
		}
	}
	run.Counts["commitments_due_soon"] = len(d)
	run.Counts["commitments_possibly_overdue"] = len(o)
	return len(d), len(o)
}
