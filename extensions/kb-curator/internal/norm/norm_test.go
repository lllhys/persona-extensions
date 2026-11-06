package norm

import "testing"

func TestQ(t *testing.T) {
	cases := map[string]string{
		"Ｈｅｌｌｏ, World!": "helloworld",
		"上线​时间：周五。":     "上线时间周五",
		"  A-B  c ":     "abc",
	}
	for in, want := range cases {
		if got := Q(in); got != want {
			t.Errorf("Q(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	line := "我们定了：发布窗口改到下周三晚上，回滚由值班同学负责。"
	cases := []struct {
		quote       string
		ok, partial bool
		name        string
	}{
		{"发布窗口改到下周三晚上", true, false, "substring"},
		{"发布窗口改到下周三晚上 ，", true, false, "punctuation ignored"},
		{"发布窗口……值班同学负责", true, true, "fragments in order"},
		{"值班同学负责……发布窗口", false, false, "fragments out of order"},
		{"发布窗口改到下周四", false, false, "changed content"},
		{"下周", false, false, "too short"},
	}
	for _, c := range cases {
		ok, partial := Match(c.quote, line)
		if ok != c.ok || partial != c.partial {
			t.Errorf("%s: Match(%q) = %v,%v want %v,%v", c.name, c.quote, ok, partial, c.ok, c.partial)
		}
	}
	if ok, _ := Match("好的", "好的"); !ok {
		t.Error("a short quote that is the whole line matches")
	}
}

func TestParse(t *testing.T) {
	l := Parse(7, "14:03 Avery Lin: the deploy is on Friday: confirmed")
	if l.N != 7 || l.Time != "14:03" || l.Speaker != "Avery Lin" || l.Body != "the deploy is on Friday: confirmed" {
		t.Fatalf("%+v", l)
	}
	if l := Parse(3, "09:00 我: 收到"); l.Speaker != Me || l.Body != "收到" {
		t.Fatalf("%+v", l)
	}
	if l := Parse(1, "no time here"); l.Speaker != "" || l.Body != "no time here" {
		t.Fatalf("%+v", l)
	}
}

func TestTokensAndJaccard(t *testing.T) {
	if n := Tokens("你好世界"); n != 4 {
		t.Errorf("Tokens(han) = %d", n)
	}
	if n := Tokens("abcdefg"); n != 2 {
		t.Errorf("Tokens(latin) = %d", n)
	}
	if j := Jaccard("每周二例会同步进度", "每周二例会同步进展"); j < 0.6 {
		t.Errorf("near duplicates: %v", j)
	}
	if j := Jaccard("每周二例会", "发布前需要审批"); j > 0.1 {
		t.Errorf("unrelated: %v", j)
	}
}
