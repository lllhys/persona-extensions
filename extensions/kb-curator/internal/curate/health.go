package curate

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Finding is a health check result that is reported rather than proposed (or proposed but blocked).
type Finding struct {
	Check  string
	Target string
	Text   string
}

// MemberBloat is the project size at which members are checked against who actually took part.
const MemberBloat = 50

var pseudoPerson = regexp.MustCompile(`(?i)系统消息|机器人|助手$|bot\b|no-?reply|notifications?|mailer|daemon|^system$`)

// health runs the deterministic checks over the knowledge base: no model, no writes, only proposals.
func (c *Curator) health(ctx context.Context, owner []string, today string, run *store.Run) []Finding {
	var out []Finding
	people, _ := c.Src.List(ctx, "people")
	for _, p := range people {
		switch {
		case pseudoPerson.MatchString(p.Name):
			c.propose("not_a_person", "「"+p.Name+"」不是一个人", "系统消息、机器人或通知地址被建成了人", "people/"+p.ID,
				Change{Update: &wire.KBUpdateParams{Kind: "people", ID: p.ID}}, nil, nil, "kb.update people status=ignored")
			out = append(out, Finding{"pseudo_person", "people/" + p.ID, p.Name})
		case slices.ContainsFunc(owner, func(o string) bool { return norm.Q(o) == norm.Q(p.Name) }):
			c.propose("owner_as_person", "「"+p.Name+"」就是你", "你自己被建成了一个人", "people/"+p.ID,
				Change{Update: &wire.KBUpdateParams{Kind: "people", ID: p.ID}}, nil, nil, "kb.update people status=self")
			out = append(out, Finding{"owner_as_person", "people/" + p.ID, p.Name})
		}
	}
	chats, _ := c.Src.List(ctx, "chats")
	chatNames := map[string]bool{}
	chatUpdated := map[string]string{}
	for _, ch := range chats {
		chatNames[norm.Q(ch.Name)] = true
		chatUpdated[ch.ID] = ch.Updated.In(c.Loc).Format("2006-01-02")
		if ch.Relevance >= 40 && chatUpdated[ch.ID] >= addDays(today, -7) && !c.digestedSince(ch.ID, addDays(today, -7)) {
			out = append(out, Finding{"coverage", "chats/" + ch.ID, fmt.Sprintf("「%s」相关性 %d，最近 7 天有消息但没有日摘要", ch.Name, ch.Relevance)})
		}
	}
	projects, _ := c.Src.List(ctx, "projects")
	for _, pe := range projects {
		d, err := c.Src.Get(ctx, "projects", pe.ID)
		if err != nil {
			continue
		}
		m := kbsrc.DecodeMeta(d)
		if m.Status == "merged" || m.Status == "ignored" {
			continue
		}
		var bad []string
		for _, a := range m.Aliases {
			if chatNames[norm.Q(a)] || isGenericAlias(a) {
				bad = append(bad, a)
			}
		}
		if len(bad) > 0 && !slices.Contains(m.Locked, "aliases") {
			c.propose("remove_alias", fmt.Sprintf("「%s」的别名里有群名或泛称：%s", m.Name, strings.Join(bad, "、")), "群名、泛称会把整个群的话题吸进这个项目",
				"projects/"+pe.ID, Change{Update: &wire.KBUpdateParams{Kind: "projects", ID: pe.ID, RemoveAliases: bad}}, nil, nil, "")
			run.Counts["proposal_remove_alias"]++
		}
		if len(m.People) > MemberBloat {
			c.memberBloat(pe.ID, m, today, run, &out)
		}
		if m.Status == "active" && len(m.Chats) > 0 {
			quiet := true
			for _, ch := range m.Chats {
				if chatUpdated[ch] >= addDays(today, -28) {
					quiet = false
				}
			}
			if quiet {
				c.propose("project_pause", "「"+m.Name+"」4 周没有动静", "关联的会话 28 天内都没有新消息；改成暂停（不是删除）",
					"projects/"+pe.ID, Change{Update: &wire.KBUpdateParams{Kind: "projects", ID: pe.ID, Status: "paused"}}, nil, nil, "")
				run.Counts["proposal_project_pause"]++
			}
		}
	}
	for _, ch := range chats {
		if d, err := c.Src.Get(ctx, "chats", ch.ID); err == nil {
			if m := kbsrc.DecodeMeta(d); m.Kind == "dm" && len(m.People) > 2 {
				out = append(out, Finding{"dm_members", "chats/" + ch.ID, fmt.Sprintf("私聊「%s」挂了 %d 个人", m.Name, len(m.People))})
			}
		}
	}
	run.Counts["findings"] = len(out)
	return out
}

func (c *Curator) digestedSince(chat, since string) bool {
	for _, d := range c.St.S.Digests {
		if d.Chat == "chats/"+chat && d.Day >= since {
			return true
		}
	}
	return false
}

// memberBloat proposes removing members who never spoke in the project's chats in the curator's digests of the
// last 30 days, but only once those digests cover at least a week (otherwise it is only a finding).
func (c *Curator) memberBloat(id string, m kbsrc.Meta, today string, run *store.Run, out *[]Finding) {
	since := addDays(today, -30)
	days := map[string]bool{}
	spoke := map[string]bool{}
	for _, d := range c.St.S.Digests {
		if d.Day < since || !slices.ContainsFunc(m.Chats, func(ch string) bool { return d.Chat == "chats/"+ch }) {
			continue
		}
		days[d.Day] = true
		for _, p := range d.Points {
			for _, e := range p.Evidence {
				spoke[norm.Q(e.Speaker)] = true
			}
		}
	}
	text := fmt.Sprintf("「%s」挂了 %d 个人", m.Name, len(m.People))
	if len(days) < 7 {
		*out = append(*out, Finding{"member_bloat", "projects/" + id, text + "；日摘要还不够一周，先不提议"})
		return
	}
	people, _ := c.Src.List(context.Background(), "people")
	names := map[string]string{}
	for _, p := range people {
		names[p.ID] = p.Name
	}
	var drop []string
	for _, pid := range m.People {
		if !spoke[norm.Q(names[pid])] {
			drop = append(drop, pid)
		}
	}
	if len(drop) == 0 || slices.Contains(m.Locked, "people") {
		return
	}
	c.propose("remove_members", fmt.Sprintf("%s，其中 %d 人 30 天里没在这件事上出现过", text, len(drop)), "只解除和项目的关联，人还在",
		"projects/"+id, Change{Update: &wire.KBUpdateParams{Kind: "projects", ID: id, RemovePeople: drop}}, nil, nil, "")
	run.Counts["proposal_remove_members"]++
}

func isGenericAlias(a string) bool {
	q := norm.Q(a)
	for _, g := range []string{"周会", "例会", "周知群", "通知群", "大群", "交流群", "项目群", "这个需求", "需求", "项目", "weekly", "standup", "general"} {
		if q == norm.Q(g) || strings.HasSuffix(q, norm.Q(g)) && strings.Contains(g, "群") {
			return true
		}
	}
	return strings.HasSuffix(a, "群")
}
