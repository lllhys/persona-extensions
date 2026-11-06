package curate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/verify"
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
	return b.String()
}

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
