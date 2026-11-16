package curate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The open type tables (design 5.2, 5.4): each facet's types grow from the user's own data. The starter pack, chosen
// by the first-run answer (work, life or both), is only a hint to the model; a type the model proposes joins the
// table once it has been seen twice; the user may rename and merge types. No pack names anything that holds for one
// user only. ⑤'s relation types are the graph's (internal/graph packs, learned there).

// Origins of a type.
const (
	typeSeed     = "seed"     // the starter pack
	typeModel    = "model"    // proposed by the model and seen ≥2 times
	typeProposed = "proposed" // proposed once: not in the table yet
	typeUser     = "user"     // the user named it
	minTypeSeen  = 2
)

// typePack is a starter pack in one language: facet → work types and life types.
type typePack map[entry.Facet][2][]string

var typePacks = map[string]typePack{
	"zh": {
		entry.Interaction: {{"请示", "汇报", "派活", "打扰时机", "格式和语气", "纠正方式"}, {"提醒", "日常问答", "格式和语气"}},
		entry.Tools:       {{"机器", "工具", "运行环境", "模型", "网关", "服务"}, {"设备", "应用", "账号和服务"}},
		entry.Judgement:   {{"技术选型", "范围取舍", "优先级", "风险取舍", "质量和速度", "找谁做"}, {"花钱", "时间安排", "取舍偏好"}},
		entry.Workflow:    {{"排查问题", "评审", "调研", "写文档", "安排协作", "发布上线"}, {"出行安排", "日常事务", "采购"}},
		entry.People:      {{"角色", "职责", "合作方式", "回应快慢", "说话风格", "靠不靠谱"}, {"角色", "相处方式", "说话风格"}},
		entry.Persona:     {{"角色", "职责", "能力", "处境", "在做的事", "目标", "看重什么", "沟通偏好"}, {"生活偏好", "看法", "目标", "看重什么"}},
	},
	"en": {
		entry.Interaction: {{"asking", "reporting", "delegating", "when to interrupt", "format and tone", "corrections"}, {"reminders", "everyday questions", "format and tone"}},
		entry.Tools:       {{"machine", "tool", "runtime", "model", "gateway", "service"}, {"device", "app", "account and service"}},
		entry.Judgement:   {{"technology choice", "scope", "priority", "risk", "quality vs speed", "who does it"}, {"spending", "scheduling", "trade-offs"}},
		entry.Workflow:    {{"troubleshooting", "review", "research", "writing", "coordinating", "release"}, {"travel", "errands", "purchases"}},
		entry.People:      {{"role", "responsibility", "collaboration", "responsiveness", "style", "reliability"}, {"role", "how to get along", "style"}},
		entry.Persona:     {{"role", "responsibilities", "abilities", "situation", "current work", "goals", "values", "communication"}, {"preferences", "views", "goals", "values"}},
	},
}

// pack is the starter types of a facet for the first-run answer ("" or both: both halves) and the language.
func pack(f entry.Facet, use, lang string) []string {
	p := typePacks["en"]
	if strings.HasPrefix(lang, "zh") {
		p = typePacks["zh"]
	}
	var out []string
	for i, half := range p[f] {
		if use == entry.Work && i == 1 || use == entry.Life && i == 0 {
			continue
		}
		for _, t := range half {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// seedTypes starts the tables of the facets that have none from the starter packs.
func (c *Curator) seedTypes() {
	s := c.St.S
	use := ""
	if s.Onboarding != nil {
		use = s.Onboarding.Use
	}
	for _, f := range entry.Facets {
		if f == entry.World || len(s.Types[f]) > 0 {
			continue
		}
		for _, t := range pack(f, use, c.language()) {
			s.Types[f] = append(s.Types[f], entry.TypeDef{Name: t, Origin: typeSeed})
		}
	}
}

// inTable says whether a type definition is in the table: a seed, the user's, or the model's seen twice; not one
// renamed or merged away.
func inTable(t entry.TypeDef) bool {
	return t.Renamed == "" && (t.Origin != typeProposed || t.Seen >= minTypeSeen)
}

// typeNames lists the facet's table for the model.
func (c *Curator) typeNames(f entry.Facet) []string {
	out := []string{}
	for _, t := range c.St.S.Types[f] {
		if inTable(t) {
			out = append(out, t.Name)
		}
	}
	return out
}

// resolveType follows renames and merges to the name in use.
func resolveType(s *store.State, f entry.Facet, name string) string {
	for i := 0; i < 8; i++ {
		j := slices.IndexFunc(s.Types[f], func(t entry.TypeDef) bool { return norm.Q(t.Name) == norm.Q(name) })
		if j < 0 || s.Types[f][j].Renamed == "" {
			return name
		}
		name = s.Types[f][j].Renamed
	}
	return name
}

// noteType counts one use of a type by a class the consolidation made: a new name is proposed, and joins the table
// at its second use.
func (c *Curator) noteType(f entry.Facet, name string) {
	name = strings.TrimSpace(name)
	if name == "" || f == entry.World {
		return
	}
	s := c.St.S
	i := slices.IndexFunc(s.Types[f], func(t entry.TypeDef) bool { return norm.Q(t.Name) == norm.Q(name) })
	if i < 0 {
		s.Types[f] = append(s.Types[f], entry.TypeDef{Name: name, Origin: typeProposed, Seen: 1})
		return
	}
	t := &s.Types[f][i]
	t.Seen++
	if t.Origin == typeProposed && t.Seen >= minTypeSeen {
		t.Origin = typeModel
	}
}

// learnTypes counts the types the action entries carry too (the daily extraction proposes them), once per entry, so
// a type the extraction keeps using joins the table.
func (c *Curator) learnTypes(run *store.Run) {
	s := c.St.S
	seen := map[entry.Facet]map[string]int{}
	for _, e := range s.Entries {
		if e.Type == "" || e.Facet == entry.World || !e.Live() {
			continue
		}
		if seen[e.Facet] == nil {
			seen[e.Facet] = map[string]int{}
		}
		seen[e.Facet][resolveType(s, e.Facet, e.Type)]++
	}
	for f, byName := range seen {
		for _, name := range sortedKeys(byName) {
			i := slices.IndexFunc(s.Types[f], func(t entry.TypeDef) bool { return norm.Q(t.Name) == norm.Q(name) })
			if i < 0 {
				s.Types[f] = append(s.Types[f], entry.TypeDef{Name: name, Origin: typeProposed})
				i = len(s.Types[f]) - 1
			}
			t := &s.Types[f][i]
			t.Seen = max(t.Seen, byName[name])
			if t.Origin == typeProposed && t.Seen >= minTypeSeen {
				t.Origin = typeModel
				run.Counts["types_learned"]++
			}
		}
	}
}

// RenameType is the user renaming a type (or merging it into another, when the new name is one already): the
// entries of the old type take the new one, and the old one is kept, pointing at it.
func (c *Curator) RenameType(f entry.Facet, from, to string) error {
	s := c.St.S
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" || norm.Q(from) == norm.Q(to) {
		return fmt.Errorf("rename %q to %q: nothing to do", from, to)
	}
	i := slices.IndexFunc(s.Types[f], func(t entry.TypeDef) bool { return norm.Q(t.Name) == norm.Q(from) })
	if i < 0 {
		return fmt.Errorf("no type %q in %s", from, f)
	}
	seen := s.Types[f][i].Seen
	s.Types[f][i].Renamed = to
	if j := slices.IndexFunc(s.Types[f], func(t entry.TypeDef) bool { return norm.Q(t.Name) == norm.Q(to) }); j >= 0 {
		s.Types[f][j].Seen += seen
		s.Types[f][j].Origin, s.Types[f][j].Renamed = typeUser, ""
	} else {
		s.Types[f] = append(s.Types[f], entry.TypeDef{Name: to, Origin: typeUser, Seen: seen})
	}
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		if e.Facet == f && norm.Q(e.Type) == norm.Q(from) {
			e.Type, e.Updated = to, c.stamp()
			c.commit(entry.EvUser, e, "", "type", "")
		}
	}
	return nil
}

// MergeTypes is the user saying several types are one: each is renamed into the first.
func (c *Curator) MergeTypes(f entry.Facet, into string, from ...string) error {
	for _, x := range from {
		if err := c.RenameType(f, x, into); err != nil {
			return err
		}
	}
	return nil
}
