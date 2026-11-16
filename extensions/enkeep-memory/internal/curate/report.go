package curate

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
)

func sortedKeys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (c *Curator) dailyReport(day string, run *store.Run, f store.Funnel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 日级整理 %s\n\n", day)
	if run.DryRun {
		b.WriteString("（演练：没有写知识库）\n\n")
	}
	if c.Cfg.Shadow(c.St.S.Installed, c.now()) {
		b.WriteString("影子期：只提议，不改知识库。\n\n")
	}
	total, totalTok := 0, 0
	for _, n := range f.Chats {
		total += n
	}
	for _, t := range f.Tokens {
		totalTok += t
	}
	fmt.Fprintf(&b, "## 分拣\n\n%d 个「条目 × 日」（所有来源一条流水线），约 %d token；读了 %d 个。\n\n", total, totalTok, f.Admitted)
	b.WriteString("| 规则 → 结论 | 个数 |\n| --- | --- |\n")
	for _, k := range sortedKeys(f.Chats) {
		fmt.Fprintf(&b, "| %s | %d |\n", k, f.Chats[k])
	}
	b.WriteString("\n| 结论 | 约 token | 占比 |\n| --- | --- | --- |\n")
	for _, k := range sortedKeys(f.Tokens) {
		pct := 0.0
		if totalTok > 0 {
			pct = float64(f.Tokens[k]) * 100 / float64(totalTok)
		}
		fmt.Fprintf(&b, "| %s | %d | %.0f%% |\n", k, f.Tokens[k], pct)
	}
	b.WriteString("\n来源（只是分拣的一个特征）：")
	for _, k := range sortedKeys(f.Sources) {
		fmt.Fprintf(&b, " %s %d", k, f.Sources[k])
	}
	b.WriteString("\n\n## 摘要和条目\n\n")
	fmt.Fprintf(&b, "整理：待归档 %d 条，归到已有 %d、新的一类 %d、反例 %d、失效 %d、单独一条 %d、同样内容删过而丢掉 %d。\n",
		run.Counts["consolidate_pending"], run.Counts["consolidate_filed"], run.Counts["consolidate_new_class"], run.Counts["consolidate_counter"],
		run.Counts["consolidate_replaced"], run.Counts["consolidate_standalone"], run.Counts["consolidate_tombstoned"])
	fmt.Fprintf(&b, "摘要 %d 个；事实 %d、做事方式 %d、知识 %d；通过 %d、标记 %d、拒绝 %d；图谱新关系 %d、补证据 %d、取代 %d、冲突 %d。模型调用 %d 次，约 %d token。\n",
		run.Counts["digested"], run.Counts[store.Fact], run.Counts[store.Practice], run.Counts[store.Knowledge],
		run.Counts["passed"], run.Counts["flagged"], run.Counts["rejected"],
		run.Counts["graph_new"], run.Counts["graph_duplicate"], run.Counts["graph_superseded"], run.Counts["graph_conflict"],
		run.Counts["model_calls"], run.Tokens)
	if codes := c.rejectionCounts(day); len(codes) > 0 {
		b.WriteString("\n拒绝原因：")
		for _, k := range verify.RejectCodes {
			if codes[k] > 0 {
				fmt.Fprintf(&b, " %s %d", k, codes[k])
			}
		}
		b.WriteString("\n")
	}
	for _, n := range run.Notes {
		fmt.Fprintf(&b, "\n- %s", n)
	}
	b.WriteString("\n")
	writeRefusals(&b, run)
	return b.String()
}

// writeRefusals lists the batches the model declined: each with the hub's reason and what became of it, and the
// lines that were not read in the end. One line each, naming the unit by its ref (forgetting a source scrubs it).
func writeRefusals(b *strings.Builder, run *store.Run) {
	if len(run.Refusals) == 0 {
		return
	}
	skipped := 0
	for _, r := range run.Refusals {
		if r.Outcome == store.RefusalSkipped {
			skipped++
		}
	}
	fmt.Fprintf(b, "\n## 模型拒答\n\n拒答 %d 批，拆小以后仍被拒、跳过 %d 批（摘要没读 %d 行）。\n\n",
		len(run.Refusals), skipped, run.Counts["daily_refused_lines"])
	b.WriteString("| 来源 | 步骤 | 行 | 原因 | 结果 |\n| --- | --- | --- | --- | --- |\n")
	for _, r := range run.Refusals {
		fmt.Fprintf(b, "| 「%s」%s | %s | %d–%d（%d 行） | %s | %s |\n", cell(r.Name), r.Unit, StepLabel(r.Step), r.From, r.To, r.Lines,
			cell(r.Reason), RefusalOutcome(r.Outcome))
	}
}

func cell(s string) string { return strings.NewReplacer("|", "／", "\n", " ").Replace(s) }

func (c *Curator) rejectionCounts(day string) map[string]int {
	out := map[string]int{}
	for _, r := range c.St.S.Rejections {
		if day == "" || r.Day == day {
			out[r.Code]++
		}
	}
	return out
}

func (c *Curator) weeklyReport(week string, days []string, run *store.Run, findings []Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 周级整理 %s（%s 到 %s）\n\n", week, days[0], days[6])
	if run.DryRun {
		b.WriteString("（演练：没有写知识库）\n\n")
	}
	count := func(class, status string) int {
		n := 0
		for _, it := range c.St.S.Items {
			if it.Class == class && (status == "" || it.Status == status) {
				n++
			}
		}
		return n
	}
	b.WriteString("## 健康度\n\n| | 有效 | 候选 / 偶现 | 其他 |\n| --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| 事实 | %d | %d | 过时 %d |\n", count(store.Fact, "active"), count(store.Fact, "candidate"), count(store.Fact, "stale"))
	fmt.Fprintf(&b, "| 做事方式 | 固定 %d | 偶现 %d、反复 %d | 休眠 %d |\n", count(store.Practice, "established"), count(store.Practice, "observed"), count(store.Practice, "recurring"), count(store.Practice, "dormant"))
	fmt.Fprintf(&b, "| 知识 | %d | %d | 被取代 %d |\n", count(store.Knowledge, "active"), count(store.Knowledge, "candidate"), count(store.Knowledge, "superseded"))
	active := 0
	for _, a := range c.St.S.Graph.Assertions {
		if a.Status == "active" {
			active++
		}
	}
	fmt.Fprintf(&b, "\n图谱：节点 %d，有效关系 %d，本周变旧 %d。\n", len(c.St.S.Graph.Nodes), active, run.Counts["graph_staled"])
	fmt.Fprintf(&b, "\n本周合并 %d 条重复；新提议：记录 %d、矛盾 %d、图谱合并 %d、别名 %d、成员 %d、暂停项目 %d；自动合入 %d；过期 %d。\n",
		run.Counts["merged"], run.Counts["proposal_pin_fact"]+run.Counts["proposal_pin_practice"]+run.Counts["proposal_pin_knowledge"],
		run.Counts["proposal_item_conflict"], run.Counts["proposal_graph_merge"], run.Counts["proposal_remove_alias"],
		run.Counts["proposal_remove_members"], run.Counts["proposal_project_pause"], run.Counts["auto_applied"], run.Counts["expired"])
	pending := 0
	for _, p := range c.St.S.Proposals {
		if p.Status == "pending" || p.Status == "later" {
			pending++
		}
	}
	fmt.Fprintf(&b, "待确认 %d 条；自动处理但你还没看过的 %d 条。模型调用 %d 次，本周约 %d token。\n", pending, len(c.Unacked()), run.Counts["model_calls"], run.Tokens)
	if prec := c.Precision(); len(prec) > 0 {
		b.WriteString("\n## 各类提议的精度（判过的）\n\n")
		for _, k := range sortedKeys(prec) {
			if v := prec[k]; v[1] > 0 {
				fmt.Fprintf(&b, "- %s：%d/%d\n", k, v[0], v[1])
			}
		}
	}
	c.entriesReport(&b, run)
	if len(findings) > 0 {
		b.WriteString("\n## 体检\n\n")
		for _, f := range findings {
			fmt.Fprintf(&b, "- [%s] %s\n", f.Check, f.Text)
		}
	}
	for _, n := range run.Notes {
		fmt.Fprintf(&b, "\n- %s", n)
	}
	b.WriteString("\n")
	return b.String()
}

// entriesReport is the weekly section on the entries (design 5.0, 4.4): every facet's entries by grain and
// status, what the abstract gate held back, and the quiet entries the user confirmed, asked about once. Sensitive
// entries are counted, never quoted.
func (c *Curator) entriesReport(b *strings.Builder, run *store.Run) {
	s := c.St.S
	if len(s.Entries) == 0 {
		return
	}
	b.WriteString("\n## 条目\n\n| 方面 | 粒度 | 候选 | 稳定 | 在变 | 休眠 |\n| --- | --- | --- | --- | --- | --- |\n")
	type key struct {
		f entry.Facet
		g entry.Grain
	}
	n := map[key]map[entry.Status]int{}
	var asks []string
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		if !e.Live() {
			continue
		}
		k := key{e.Facet, e.Grain}
		if n[k] == nil {
			n[k] = map[entry.Status]int{}
		}
		n[k][e.Status]++
		if slices.Contains(e.Flags, flagAskedDormant) && e.Sensitive == "" && e.Status != entry.Dormant {
			asks = append(asks, e.Shown())
		}
	}
	for _, f := range entry.Facets {
		for _, g := range []entry.Grain{entry.Action, entry.Class, entry.Abstract} {
			if x := n[key{f, g}]; len(x) > 0 {
				fmt.Fprintf(b, "| %s | %s | %d | %d | %d | %d |\n", f, g, x[entry.Candidate], x[entry.Stable], x[entry.Shifting], x[entry.Dormant])
			}
		}
	}
	fmt.Fprintf(b, "\n本周归档 %d 条、新的一类 %d 条、反例 %d 条、失效 %d 条、类合并 %d 个；抽象层提出 %d 条，门槛拦下 %d 条（其中「换成任何能干的人也成立」%d 条），新增 %d 条；影响复核 %d 条，退回候选 %d 条；去掉不成立的关系 %d 条。\n",
		run.Counts["consolidate_filed"], run.Counts["consolidate_new_class"], run.Counts["consolidate_counter"], run.Counts["consolidate_replaced"], run.Counts["classes_merged"],
		run.Counts["abstract_proposed"], run.Counts["abstract_blocked"], run.Counts["abstract_blocked:"+gateGeneric], run.Counts["abstract_new"],
		run.Counts["impact_reviewed"], run.Counts["impact_none"],
		run.Counts["relation_retired:bot"]+run.Counts["relation_retired:courtesy"])
	if len(asks) > 0 {
		b.WriteString("\n你确认过、但很久没再出现的（还成立吗？不回答就照旧）：\n\n")
		for i, t := range asks {
			if i >= 20 {
				fmt.Fprintf(b, "- ……另有 %d 条\n", len(asks)-i)
				break
			}
			fmt.Fprintf(b, "- %s\n", t)
		}
	}
}
