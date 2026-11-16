package curate

import (
	"slices"
	"testing"
)

// The settings renamed in 0.2.0 (the hub's install preview took admit_all_below_tokens and private_keywords for
// secrets) still count when a hub kept a configuration saved under the old names; privacy keywords from both names
// are kept.
func TestConfigOldNames(t *testing.T) {
	c, err := ParseConfig([]byte(`{"admit_all_below_tokens": 5000, "private_keywords": ["体检", "工资"], "privacy_keywords": ["工资", "病历"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.AdmitAllBelowTokens != 5000 {
		t.Errorf("admit_all_below from the old name: %d", c.AdmitAllBelowTokens)
	}
	for _, k := range []string{"体检", "工资", "病历"} {
		if !slices.Contains(c.PrivateKeywords, k) {
			t.Errorf("privacy keyword %q lost: %v", k, c.PrivateKeywords)
		}
	}
	if len(c.PrivateKeywords) != 3 {
		t.Errorf("duplicates: %v", c.PrivateKeywords)
	}
	// A value set under the new name wins over the old one.
	c, _ = ParseConfig([]byte(`{"admit_all_below_tokens": 5000, "admit_all_below": 800}`))
	if c.AdmitAllBelowTokens != 800 {
		t.Errorf("the new name: %d", c.AdmitAllBelowTokens)
	}
	// Neither: the default.
	c, _ = ParseConfig([]byte(`{}`))
	if c.AdmitAllBelowTokens != Defaults().AdmitAllBelowTokens || len(c.PrivateKeywords) != 0 {
		t.Errorf("defaults: %+v", c)
	}
}
