package main

import "testing"

// The WMO weather code mapping: every code 0-99 answers something, and the codes the standard uses say the right thing.

func TestWMOCoversAllCodes(t *testing.T) {
	for code := 0; code <= 99; code++ {
		desc, icon := wmo(code)
		if desc == "" || icon == "" {
			t.Errorf("code %d: desc %q icon %q", code, desc, icon)
		}
	}
}

func TestWMOKnownCodes(t *testing.T) {
	cases := map[int]string{
		0: "晴朗", 1: "大致晴朗", 2: "局部多云", 3: "阴天",
		45: "有雾", 48: "雾凇",
		51: "小毛毛雨", 53: "毛毛雨", 55: "大毛毛雨", 56: "冻毛毛雨（轻）", 57: "冻毛毛雨（重）",
		61: "小雨", 63: "中雨", 65: "大雨", 66: "冻雨（轻）", 67: "冻雨（重）",
		71: "小雪", 73: "中雪", 75: "大雪", 77: "雪粒",
		80: "小阵雨", 81: "阵雨", 82: "强阵雨", 85: "小阵雪", 86: "大阵雪",
		95: "雷暴", 96: "雷暴伴小冰雹", 99: "雷暴伴大冰雹",
	}
	for code, want := range cases {
		if got := wmoText(code); got != want {
			t.Errorf("code %d = %q, want %q", code, got, want)
		}
	}
}

// The reserved codes (for example 4 and 50) answer a generic description, not the neighbouring one.
func TestWMOReservedCodes(t *testing.T) {
	for _, code := range []int{4, 17, 44, 50, 58, 68, 76, 83, 90, 97} {
		if got := wmoText(code); got != "天气现象无法确定" {
			t.Errorf("reserved code %d = %q", code, got)
		}
	}
}
