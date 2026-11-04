package pluginkit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type ownExtra struct {
	ThingExtra
	Avatar string `json:"avatar,omitempty"`
}

func TestInboxThingShape(t *testing.T) {
	th := InboxThing[ownExtra]{Thread: "repo#1", Kind: "review", Last: "3", Rules: "r",
		Extra: &ownExtra{ThingExtra: ThingExtra{RuleTier: TierFYI}, Avatar: "a.png"}}
	raw, _ := json.Marshal(th)
	for _, want := range []string{`"thread":"repo#1"`, `"rules":"r"`, `"extra":{"rule_tier":"fyi","avatar":"a.png"}`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s has no %s", raw, want)
		}
	}
	for _, tier := range []Tier{TierAction, TierFYI, TierNoise} {
		if !tier.Known() {
			t.Fatalf("%s unknown", tier)
		}
	}
	if Tier("").Known() || Tier("urgent").Known() {
		t.Fatal("only the hub's three tiers")
	}
}

func TestTriageRules(t *testing.T) {
	for config, want := range map[string]string{
		``:                        "",
		`null`:                    "",
		`{}`:                      "",
		`{"triage_rules": "   "}`: "",
		`{"triage_rules": " 机器人都是噪音 "}`:      "机器人都是噪音",
		`{"triage_rules": "一\\n二"}`:          "一\n二",
		`{"triage_rules": 3, "other": true}`: "",
	} {
		if got := TriageRules([]byte(config)); got != want {
			t.Errorf("%s: %q, want %q", config, got, want)
		}
	}
	long, _ := json.Marshal(map[string]string{"triage_rules": strings.Repeat("长", TriageRulesMost+10)})
	if got := TriageRules(long); len([]rune(got)) != TriageRulesMost {
		t.Fatalf("clipped to %d", len([]rune(got)))
	}
	things := []InboxThing[ThingExtra]{{Thread: "a"}, {Thread: "b"}}
	StampRules(things, "规则")
	if things[0].Rules != "规则" || things[1].Rules != "规则" {
		t.Fatalf("stamped %+v", things)
	}
}

func manifestWith(t *testing.T, caps []string, rules map[string]any) []byte {
	t.Helper()
	props := map[string]any{"other": map[string]any{"type": "boolean"}}
	if rules != nil {
		props[TriageRulesSetting] = rules
	}
	raw, _ := json.Marshal(map[string]any{
		"contributes":   map[string]any{"connectors": []any{map[string]any{"id": "src", "capabilities": caps}}},
		"configuration": map[string]any{"type": "object", "properties": props},
	})
	return raw
}

func TestCheckManifest(t *testing.T) {
	standard := TriageRulesProperty("示例来源", "机器人发的都算噪音")
	if d := standard["description"].(string); !strings.Contains(d, "示例来源的条目") || !strings.Contains(d, `\n 换行`) {
		t.Fatalf("description = %s", d)
	}
	m, err := CheckManifest(manifestWith(t, []string{"status", "inbox"}, standard))
	if err != nil || len(m.Connectors) != 1 || m.Fallback("src") {
		t.Fatalf("%+v %v", m, err)
	}
	if m, err := CheckManifest(manifestWith(t, []string{"inbox", "fallback"}, standard)); err != nil || !m.Fallback("src") {
		t.Fatalf("fallback: %+v %v", m, err)
	}
	if m, err := CheckManifest(manifestWith(t, []string{"status", "tools"}, nil)); err != nil || len(m.Connectors) != 0 {
		t.Fatalf("no inbox needs no rules: %+v %v", m, err)
	}
	for name, c := range map[string]struct {
		caps  []string
		rules map[string]any
		want  string
	}{
		"no rules":           {[]string{"inbox"}, nil, "need triage_rules, a string"},
		"not a string":       {[]string{"inbox"}, map[string]any{"type": "integer"}, "need triage_rules, a string"},
		"fallback, no inbox": {[]string{"tools", "fallback"}, nil, "without inbox"},
	} {
		if _, err := CheckManifest(manifestWith(t, c.caps, c.rules)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The hub takes any string; the standard look is CheckStandardSettings'.
	plain := map[string]any{"type": "string"}
	if _, err := CheckManifest(manifestWith(t, []string{"inbox"}, plain)); err != nil {
		t.Fatalf("a plain string: %v", err)
	}
	if err := CheckStandardSettings(manifestWith(t, []string{"inbox"}, standard)); err != nil {
		t.Fatalf("the standard setting: %v", err)
	}
	if err := CheckStandardSettings(manifestWith(t, []string{"tools"}, nil)); err != nil {
		t.Fatalf("no setting, nothing to look like: %v", err)
	}
	for name, c := range map[string]struct {
		rules map[string]any
		want  string
	}{
		"plain":         {plain, "title"},
		"another title": {map[string]any{"type": "string", "title": "规则", "maxLength": 10, "description": "d"}, "title"},
		"too long":      {map[string]any{"type": "string", "title": TriageRulesTitle, "maxLength": 9000, "description": "d"}, "maxLength"},
		"no maxLength":  {map[string]any{"type": "string", "title": TriageRulesTitle, "description": "d"}, "maxLength"},
		"no words":      {map[string]any{"type": "string", "title": TriageRulesTitle, "maxLength": 10}, "description"},
	} {
		if err := CheckStandardSettings(manifestWith(t, []string{"inbox"}, c.rules)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckThings(t *testing.T) {
	good := []InboxThing[ThingExtra]{
		{Thread: "a", Rules: "r", Extra: &ThingExtra{RuleTier: TierAction}},
		{Thread: "b", Rules: "r", Extra: &ThingExtra{RuleTier: TierNoise}},
	}
	if err := CheckThings(good, "r", true); err != nil {
		t.Fatal(err)
	}
	answer, _ := json.Marshal(map[string]any{"things": good})
	if err := CheckThings(json.RawMessage(answer), "r", true); err != nil {
		t.Fatalf("collect's answer as JSON: %v", err)
	}
	batch := json.RawMessage(`[{"thread":"c","rules":"r","group":{"batch":"c@1","new":["1"]}}]`)
	if err := CheckThings(batch, "r", true); err != nil {
		t.Fatalf("a batch needs no tier: %v", err)
	}
	for name, c := range map[string]struct {
		things   any
		rules    string
		fallback bool
		want     string
	}{
		"rules missing": {[]InboxThing[ThingExtra]{{Thread: "a"}}, "r", false, "no rules"},
		"other rules":   {[]InboxThing[ThingExtra]{{Thread: "a", Rules: "x"}}, "r", false, "not the ones set"},
		"rules unset":   {[]InboxThing[ThingExtra]{{Thread: "a", Rules: "x"}}, "", false, "none are set"},
		"no tier":       {[]InboxThing[ThingExtra]{{Thread: "a"}}, "", true, "extra.rule_tier"},
		"a wrong tier":  {[]InboxThing[ThingExtra]{{Thread: "a", Extra: &ThingExtra{RuleTier: "urgent"}}}, "", true, `"urgent"`},
	} {
		if err := CheckThings(c.things, c.rules, c.fallback); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := CheckThings([]InboxThing[ThingExtra]{{Thread: "a"}}, "", false); err != nil {
		t.Fatalf("no rules, no fallback: %v", err)
	}
	// A tier its rules decided counts as the fallback too (the hub takes either); a wrong rule_tier beside it does not.
	if err := CheckThings([]InboxThing[ThingExtra]{{Thread: "a", Tier: TierNoise}}, "", true); err != nil {
		t.Fatalf("a rules' tier: %v", err)
	}
	if err := CheckThings([]InboxThing[ThingExtra]{{Thread: "a", Tier: TierNoise, Extra: &ThingExtra{RuleTier: "urgent"}}}, "", true); err == nil {
		t.Fatal("a wrong rule_tier beside a tier")
	}
}

func TestInboxChanged(t *testing.T) {
	var out bytes.Buffer
	h := &Hub{w: &out}
	if err := h.InboxChanged("src"); err != nil {
		t.Fatal(err)
	}
	if err := h.InboxChanged(""); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || lines[0] != `{"event":"connector.changed","params":{"contribution":"src","scope":"inbox"}}` ||
		lines[1] != `{"event":"connector.changed","params":{"scope":"inbox"}}` {
		t.Fatalf("events:\n%s", out.String())
	}
}
