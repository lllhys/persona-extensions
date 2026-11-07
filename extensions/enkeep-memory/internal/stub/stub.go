// Package stub is a stand-in model for dry runs and tests while the hub has no model.complete: it answers the
// curator's three calls from simple keyword rules over the input it is given, quoting lines verbatim. It is not
// meant to be good, only deterministic, honest about its quotes, and shaped like a real answer.
package stub

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

// Model is the stub.
type Model struct{ Calls map[string]int }

var (
	marketing = regexp.MustCompile(`(?i)退订|促销|优惠|限时|折扣|unsubscribe|sale|% off|newsletter|webinar`)
	factRe    = regexp.MustCompile(`(?i)定了|决定|确定|上线|截止|发布|deadline|decided|ships?\b|due\b|负责`)
	practRe   = regexp.MustCompile(`(?i)流程|步骤|规范|以后都|每次|记得先|必须先|每周|always|before you`)
	knowRe    = regexp.MustCompile(`(?i)是指|指的是|意思是|上限|限流|limit is|means|原理`)
	ownsRe    = regexp.MustCompile(`([A-Za-z]{2,12}|[\p{Han}]{2,4})\s*负责([\p{Han}A-Za-z0-9]{2,12})`)
	ownedByRe = regexp.MustCompile(`([\p{Han}A-Za-z0-9]{2,12})由(我|[A-Za-z]{2,12}|[\p{Han}]{2,4})负责`)
	blockedRe = regexp.MustCompile(`([\p{Han}A-Za-z0-9]{2,12})卡在([\p{Han}A-Za-z]{2,12})`)
	commitRe  = regexp.MustCompile(`我来([\p{Han}A-Za-z0-9]{2,20})`)
)

func (m *Model) Complete(_ context.Context, r model.Request) (model.Response, error) {
	if m.Calls == nil {
		m.Calls = map[string]int{}
	}
	m.Calls[r.Step]++
	var out any
	switch r.Step {
	case "triage":
		var in schema.TriageInput
		if err := json.Unmarshal([]byte(r.User), &in); err != nil {
			return model.Response{}, err
		}
		out = triage(in)
	case "daily":
		var in schema.DailyInput
		if err := json.Unmarshal([]byte(r.User), &in); err != nil {
			return model.Response{}, err
		}
		out = daily(in)
	case "weekly":
		var in schema.WeeklyInput
		if err := json.Unmarshal([]byte(r.User), &in); err != nil {
			return model.Response{}, err
		}
		out = weekly(in)
	default:
		return model.Response{}, fmt.Errorf("stub: unknown step %q", r.Step)
	}
	b, _ := json.Marshal(out)
	return model.Response{Text: string(b), Model: "stub", InTokens: norm.Tokens(r.System + r.User), OutTokens: norm.Tokens(string(b))}, nil
}

func cut(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func triage(in schema.TriageInput) schema.TriageOutput {
	for _, l := range in.Lines {
		if marketing.MatchString(l.Text) {
			cat := "marketing"
			if strings.Contains(strings.ToLower(l.Text), "newsletter") {
				cat = "newsletter"
			}
			return schema.TriageOutput{Admit: "drop", Category: cat, Importance: "P3", Relevance: 5, Reason: "营销 / 订阅内容",
				Evidence: []schema.Evidence{{N: l.N, Quote: cut(l.Text, 20)}}}
		}
	}
	out := schema.TriageOutput{Admit: "include", Category: "work_fyi", Importance: "P2", Relevance: 50, Reason: "和工作有关"}
	if len(in.Lines) > 0 {
		out.Evidence = []schema.Evidence{{N: in.Lines[0].N, Quote: cut(in.Lines[0].Text, 20)}}
	}
	return out
}

func daily(in schema.DailyInput) schema.DailyOutput {
	out := schema.DailyOutput{Summary: []schema.Point{}, Facts: []schema.Item{}, Practices: []schema.Item{}, Knowledge: []schema.Item{},
		Mentions: []schema.Mention{}, Assertions: []schema.Assertion{}}
	for _, l := range in.Lines {
		if len([]rune(norm.Q(l.Text))) < 6 {
			continue
		}
		ev := []schema.Evidence{{N: l.N, Quote: cut(l.Text, 40)}}
		if in.Graph {
			graphLine(&out, l, ev)
		}
		item := schema.Item{Claim: cut(l.Text, 60), Evidence: ev, Confidence: "high"}
		switch {
		case knowRe.MatchString(l.Text):
			out.Knowledge = append(out.Knowledge, item)
		case practRe.MatchString(l.Text):
			item.Scope = in.ChatName
			out.Practices = append(out.Practices, item)
		case factRe.MatchString(l.Text):
			out.Facts = append(out.Facts, item)
		default:
			continue
		}
		if len(out.Summary) < 8 {
			out.Summary = append(out.Summary, schema.Point{Text: cut(l.Text, 60), Evidence: ev})
		}
	}
	return out
}

func graphLine(out *schema.DailyOutput, l schema.MsgLine, ev []schema.Evidence) {
	who := func(s string) string {
		if s == "我" || s == "" {
			if l.Speaker == norm.Me {
				return "我"
			}
			return l.Speaker
		}
		return s
	}
	objType := func(obj string) string {
		if strings.ContainsAny(obj, "系统服务平台网关") || strings.Contains(strings.ToLower(obj), "service") {
			return "system"
		}
		return "project"
	}
	if m := commitRe.FindStringSubmatch(l.Text); m != nil && l.Speaker == norm.Me {
		out.Mentions = append(out.Mentions, schema.Mention{Surface: m[1], Type: "task", N: l.N})
		out.Assertions = append(out.Assertions, schema.Assertion{S: "我", P: "committed", O: m[1], Evidence: ev, Confidence: "high"})
	}
	if m := ownedByRe.FindStringSubmatch(l.Text); m != nil {
		out.Mentions = append(out.Mentions, schema.Mention{Surface: m[1], Type: objType(m[1]), N: l.N})
		out.Assertions = append(out.Assertions, schema.Assertion{S: who(m[2]), P: "owns", O: m[1], Evidence: ev, Confidence: "high"})
	}
	for _, m := range ownsRe.FindAllStringSubmatch(l.Text, -1) {
		if m[1] == "由我" || strings.HasSuffix(m[1], "由") {
			continue
		}
		out.Mentions = append(out.Mentions, schema.Mention{Surface: m[2], Type: objType(m[2]), N: l.N})
		out.Assertions = append(out.Assertions, schema.Assertion{S: who(m[1]), P: "owns", O: m[2], Evidence: ev, Confidence: "high"})
	}
	if m := blockedRe.FindStringSubmatch(l.Text); m != nil {
		out.Mentions = append(out.Mentions, schema.Mention{Surface: m[1], Type: "project", N: l.N}, schema.Mention{Surface: m[2], Type: "person", N: l.N})
		out.Assertions = append(out.Assertions, schema.Assertion{S: m[1], P: "blocked_by", O: m[2], Evidence: ev, Confidence: "medium"})
	}
}

func weekly(in schema.WeeklyInput) schema.WeeklyOutput {
	out := schema.WeeklyOutput{Merges: []schema.Merge{}, Conflicts: []schema.Conflict{}}
	used := map[string]bool{}
	for i, a := range in.Items {
		if used[a.ID] {
			continue
		}
		mg := schema.Merge{Keep: a.ID}
		for _, b := range in.Items[i+1:] {
			if !used[b.ID] && !b.Pinned && norm.Jaccard(a.Claim, b.Claim) >= 0.7 {
				mg.Drop = append(mg.Drop, b.ID)
				used[b.ID] = true
			}
		}
		if len(mg.Drop) > 0 {
			out.Merges = append(out.Merges, mg)
		}
	}
	return out
}

// Scripted answers each step with a function, for tests that need a particular (or wrong) answer.
type Scripted func(r model.Request) (string, error)

func (s Scripted) Complete(_ context.Context, r model.Request) (model.Response, error) {
	t, err := s(r)
	return model.Response{Text: t, InTokens: 100, OutTokens: 50}, err
}
