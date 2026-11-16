package curate

import "testing"

func TestListOfPoints(t *testing.T) {
	for text, want := range map[string]bool{
		"部署时先在低风险环境验证再推进":                              false,
		"部署时先清理凭证，或在低风险环境验证，或先导出备份":                    true,
		"when deploying, test in staging first":        false,
		"clean credentials first, or test, or back up": true,
		"选 A 或 B": false,
	} {
		if got := listOfPoints(text); got != want {
			t.Errorf("listOfPoints(%q) = %v, want %v", text, got, want)
		}
	}
}
