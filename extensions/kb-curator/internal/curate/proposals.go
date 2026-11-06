package curate

import (
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

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
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
}

func (c *Curator) propose(kind, title, detail, target string, ch Change, items []string, ev []store.Evidence, needs string) *store.Proposal {
	spec := ProposalKinds[kind]
	raw, _ := json.Marshal(ch)
	key := hash(kind, target, string(raw))[:16]
	for _, p := range c.St.S.Proposals {
		if p.Key == key {
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
		msg, err = c.apply(ctx, p, action, false, "manual")
		if err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("不认识的操作 %q", action)
	}
	return msg, c.St.Save()
}

// apply carries out a proposal: a write to the knowledge base with a snapshot of what it replaces, or a change to
// the curator's own items and graph.
func (c *Curator) apply(ctx context.Context, p *store.Proposal, action string, auto bool, run string) (string, error) {
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
	default:
		if ch.Update == nil {
			return "", errors.New("提议里没有要写的内容")
		}
		id, err := c.write(ctx, p, ch, auto, run)
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

// fields a write touches, by kb.update parameter.
func touched(u wire.KBUpdateParams) []string {
	var out []string
	if u.Pinned != nil {
		out = append(out, "pinned")
	}
	if u.Relevance != nil {
		out = append(out, "relevance")
	}
	for f, v := range map[string]string{"status": u.Status, "note": u.Note, "importance": u.Importance} {
		if v != "" {
			out = append(out, f)
		}
	}
	if len(u.RemovePeople) > 0 || len(u.People) > 0 {
		out = append(out, "people")
	}
	if len(u.RemoveAliases) > 0 || len(u.Aliases) > 0 {
		out = append(out, "aliases")
	}
	if u.Tags != nil {
		out = append(out, "tags")
	}
	sort.Strings(out)
	return out
}

func pick(meta json.RawMessage, fields []string) map[string]any {
	all := map[string]any{}
	_ = json.Unmarshal(meta, &all)
	out := map[string]any{}
	for _, f := range fields {
		out[f] = all[f]
	}
	return out
}

func (c *Curator) write(ctx context.Context, p *store.Proposal, ch Change, auto bool, run string) (string, error) {
	up := *ch.Update
	cur, err := c.Src.Get(ctx, up.Kind, up.ID)
	if err != nil {
		return "", fmt.Errorf("读 %s/%s：%w", up.Kind, up.ID, err)
	}
	if ch.PinLine != "" {
		old := kbsrc.DecodeMeta(cur).Pinned
		next := old
		if !strings.Contains(old, ch.PinLine) {
			next = strings.TrimSpace(strings.TrimSpace(old) + "\n" + ch.PinLine)
		}
		up.Pinned = &next
	}
	if auto {
		f := false
		up.Lock = &f // what the curator did on its own stays open to the hub's later maintenance
	}
	fields := touched(up)
	before := pick(cur.Meta, fields)
	if err := c.Src.Update(ctx, up); err != nil {
		return "", err
	}
	after := before
	if d, err := c.Src.Get(ctx, up.Kind, up.ID); err == nil {
		after = pick(d.Meta, fields)
	}
	bj, _ := json.Marshal(before)
	aj, _ := json.Marshal(after)
	uj, _ := json.Marshal(up)
	c.St.S.NextApplied++
	a := &store.Applied{ID: fmt.Sprintf("A-%04d", c.St.S.NextApplied), Proposal: p.ID, Run: run, At: c.now().UTC().Format(time.RFC3339),
		Kind: p.Kind, Entity: up.Kind + "/" + up.ID, Before: bj, After: aj, Update: uj, StateSum: hash(string(aj))}
	c.St.S.Applied = append(c.St.S.Applied, a)
	return a.ID, nil
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
		before := map[string]any{}
		_ = json.Unmarshal(a.Before, &before)
		f := false
		inv := wire.KBUpdateParams{Kind: up.Kind, ID: up.ID, Lock: &f}
		if up.Pinned != nil {
			s, _ := before["pinned"].(string)
			inv.Pinned = &s
		}
		if up.Relevance != nil {
			r := 0
			if v, ok := before["relevance"].(float64); ok {
				r = int(v)
			}
			inv.Relevance = &r
		}
		if up.Status != "" {
			inv.Status, _ = before["status"].(string)
		}
		inv.People = up.RemovePeople
		inv.Aliases = up.RemoveAliases
		if err := c.Src.Update(ctx, inv); err != nil {
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

// autoApply applies pending proposals of the kinds the user switched on, once that kind has proven itself (at
// least 10 judged, precision at or above the threshold), never high-risk ones, never in shadow mode.
func (c *Curator) autoApply(ctx context.Context, run string) (applied int) {
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
		if _, err := c.apply(ctx, p, "accept", true, run); err == nil {
			applied++
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
// chats' and people's evidence, items left without evidence, digests, ledger entries and raw answers, graph
// relations resting only on it, and pending proposals about it. Ids of a source start with "<source>-".
func (c *Curator) Forget(source string) (removed int, err error) {
	c.St.Lock()
	defer c.St.Unlock()
	removed = c.forget(func(ref string) bool {
		_, id, _ := strings.Cut(ref, "/")
		return strings.HasPrefix(id, source+"-")
	})
	return removed, c.St.Save()
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
	for id, it := range c.St.S.Items {
		n := len(it.Evidence)
		it.Evidence = keepEv(it.Evidence)
		removed += n - len(it.Evidence)
		if len(it.Evidence) == 0 {
			delete(c.St.S.Items, id)
		} else {
			it.Sources = sources(it.Evidence)
		}
	}
	for k, d := range c.St.S.Digests {
		if from(d.Chat) {
			delete(c.St.S.Digests, k)
			removed++
		}
	}
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
	g := c.St.S.Graph
	for id, a := range g.Assertions {
		a.Evidence = keepEv(a.Evidence)
		if len(a.Evidence) == 0 && a.Source == "extract" {
			delete(g.Assertions, id)
			removed++
		}
	}
	for _, n := range g.Nodes {
		n.Refs = slices.DeleteFunc(n.Refs, from)
		n.Aliases = slices.DeleteFunc(n.Aliases, func(a store.Alias) bool { return a.Evidence != nil && from(a.Evidence.Chat) })
	}
	for _, p := range c.St.S.Proposals {
		if (p.Status == "pending" || p.Status == "later" || p.Status == "blocked") && from(p.Target) {
			p.Status, p.Decided = "expired", c.today()
		}
		p.Evidence = keepEv(p.Evidence)
	}
	return removed
}
