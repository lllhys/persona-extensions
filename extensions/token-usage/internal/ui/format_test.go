package ui

import "testing"

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{9994, "9.99K"},
		{12_300, "12.3K"},
		{123_000, "123K"},
		{999_499, "999K"},
		{999_500, "1M"},
		{1_500_000, "1.5M"},
		{2_500_000_000, "2.5B"},
		{-1500, "-1.5K"},
	}
	for _, c := range cases {
		if got := formatTokens(c.n); got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatChange(t *testing.T) {
	if text, tone := formatChange(150, 100); text != "+50%" || tone != "caution" {
		t.Fatalf("%s %s", text, tone)
	}
	if text, tone := formatChange(50, 100); text != "-50%" || tone != "positive" {
		t.Fatalf("%s %s", text, tone)
	}
	if text, _ := formatChange(100, 0); text != "之前无用量" {
		t.Fatalf("%s", text)
	}
}

func TestFormatRatio(t *testing.T) {
	if _, tone := formatRatio(50, 100); tone != "positive" {
		t.Fatalf("%s", tone)
	}
	if text, tone := formatRatio(80, 100); text != "80%" || tone != "caution" {
		t.Fatalf("%s %s", text, tone)
	}
	if _, tone := formatRatio(150, 100); tone != "danger" {
		t.Fatalf("%s", tone)
	}
}
