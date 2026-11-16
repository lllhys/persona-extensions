package entry

import "strings"

// Switches (design §8). Every default is a design-time default, not read back from any user's data. A key with a
// suffix is per item: "K2.<fork type>", "K13.<category>", "K16.<source>".
const (
	K1PerTurnBlock   = "K1" // exit A as a whole: on
	K2Judgement      = "K2" // how default judgements are used: suggest | act | off (per fork type: K2.<type>)
	K3Abstract       = "K3" // abstract entries in exit A: on
	K4Persona        = "K4" // ⑥ in exits A and E: on
	K5ToolsSkill     = "K5" // skill "machines and tools": on
	K6WorkflowSkill  = "K6" // skill "ways of working" and agent project context: on
	K7WorldFacts     = "K7" // facts about others written back: on
	K7GroupByProject = "K7.group"
	K8AutoEffective  = "K8"  // new entries take effect at once (weekly report says, undoable): on
	K9FixedFields    = "K9"  // rewrite judgement-based asks as fixed fields: off
	K10Knowledge     = "K10" // the plugin's own general knowledge output: off
	K11Recordings    = "K11" // recordings as a source: on
	K12Onboarding    = "K12" // first-run questions: on
	K13Sensitive     = "K13" // per category, K13.<category>: all off; only the user turns one on
	K14People        = "K14" // ⑤′ cards as a whole: on
	K15Inferred      = "K15" // inferred profiles on the cards: on
	K16LearnPeople   = "K16" // per source, K16.<source>: on; off automatically when attribution < 95%
	K17DigScope      = "K17" // tool segments: signal (only dig what a signal triggers) | wide (looser screen)
)

// Defaults of every switch. Values are "on"/"off", or a mode word for K2 and K17.
var Defaults = map[string]string{
	K1PerTurnBlock: "on", K2Judgement: "suggest", K3Abstract: "on", K4Persona: "on", K5ToolsSkill: "on",
	K6WorkflowSkill: "on", K7WorldFacts: "on", K7GroupByProject: "on", K8AutoEffective: "on", K9FixedFields: "off",
	K10Knowledge: "off", K11Recordings: "on", K12Onboarding: "on", K13Sensitive: "off", K14People: "on",
	K15Inferred: "on", K16LearnPeople: "on", K17DigScope: "signal",
}

// SwitchSetting is one switch as stored: its value, who set it (user | auto) and why (an auto change says which
// reading moved it).
type SwitchSetting struct {
	Value string `json:"value"`
	By    string `json:"by"`
	Why   string `json:"why,omitempty"`
	At    string `json:"at"`
}

// Switch reads a switch from the stored settings: an exact key first ("K13.family"), then its base ("K13"), then
// the default.
func Switch(set map[string]SwitchSetting, key string) string {
	if s, ok := set[key]; ok && s.Value != "" {
		return s.Value
	}
	base, _, _ := strings.Cut(key, ".")
	if base != key {
		if s, ok := set[base]; ok && s.Value != "" && base != K13Sensitive && base != K2Judgement {
			// K13 and K2 are per item: a base value never turns every category or fork type on.
			return s.Value
		}
	}
	if v, ok := Defaults[key]; ok {
		return v
	}
	return Defaults[base]
}

// On says whether a switch is on.
func On(set map[string]SwitchSetting, key string) bool { return Switch(set, key) == "on" }
