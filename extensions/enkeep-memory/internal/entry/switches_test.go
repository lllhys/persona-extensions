package entry

import "testing"

func TestSwitchDefaultsAndPerItem(t *testing.T) {
	set := map[string]SwitchSetting{}
	if On(set, K13Sensitive+"."+SensFamily) {
		t.Fatal("K13 categories default off")
	}
	if Switch(set, K2Judgement+".release") != "suggest" || !On(set, K16LearnPeople+".lark") || !On(set, K7GroupByProject) {
		t.Fatal("defaults")
	}
	set[K13Sensitive] = SwitchSetting{Value: "on"}
	if On(set, K13Sensitive+"."+SensHealth) {
		t.Fatal("a base K13 value must not turn every category on")
	}
	set[K13Sensitive+"."+SensFamily] = SwitchSetting{Value: "on"}
	if !On(set, K13Sensitive+"."+SensFamily) || On(set, K13Sensitive+"."+SensFinance) {
		t.Fatal("per category")
	}
	set[K7WorldFacts] = SwitchSetting{Value: "off"}
	if On(set, K7GroupByProject) {
		t.Fatal("K7 off turns its group branch off")
	}
}
