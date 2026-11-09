package main

// WMO weather interpretation codes (0-99) to a Chinese description and an icon. Every code the standard uses is named;
// the reserved codes between them answer a generic "无法确定" rather than a blank, so a bad code never loses the page.

// wmo describes one code: a short Chinese phrase and an emoji icon for the page and tools.
func wmo(code int) (string, string) {
	switch code {
	case 0:
		return "晴朗", "☀️"
	case 1:
		return "大致晴朗", "🌤️"
	case 2:
		return "局部多云", "⛅"
	case 3:
		return "阴天", "☁️"
	case 45:
		return "有雾", "🌫️"
	case 48:
		return "雾凇", "🌫️"
	case 51:
		return "小毛毛雨", "🌦️"
	case 53:
		return "毛毛雨", "🌦️"
	case 55:
		return "大毛毛雨", "🌧️"
	case 56:
		return "冻毛毛雨（轻）", "🌧️"
	case 57:
		return "冻毛毛雨（重）", "🌧️"
	case 61:
		return "小雨", "🌦️"
	case 63:
		return "中雨", "🌧️"
	case 65:
		return "大雨", "🌧️"
	case 66:
		return "冻雨（轻）", "🌧️"
	case 67:
		return "冻雨（重）", "🌧️"
	case 71:
		return "小雪", "🌨️"
	case 73:
		return "中雪", "🌨️"
	case 75:
		return "大雪", "❄️"
	case 77:
		return "雪粒", "❄️"
	case 80:
		return "小阵雨", "🌦️"
	case 81:
		return "阵雨", "🌧️"
	case 82:
		return "强阵雨", "⛈️"
	case 85:
		return "小阵雪", "🌨️"
	case 86:
		return "大阵雪", "❄️"
	case 95:
		return "雷暴", "⛈️"
	case 96:
		return "雷暴伴小冰雹", "⛈️"
	case 99:
		return "雷暴伴大冰雹", "⛈️"
	}
	// The codes the standard reserves (4-44, 46, 47, 49, 50, 58-60, 68-70, 76, 78, 79, 83, 84, 87-94, 97, 98).
	return "天气现象无法确定", "🌡️"
}

// wmoText is the description alone, for the tools' one-line summaries.
func wmoText(code int) string {
	s, _ := wmo(code)
	return s
}
