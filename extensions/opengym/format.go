package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var weekdayNames = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

func weekdayName(date string) string { return weekdayNames[weekdayOf(date)] }

// num writes v with at most one decimal and thousands separators: 3,845 · 82.5 · 0.
func num(v float64) string {
	neg := v < 0
	v = math.Abs(v)
	v = math.Round(v*10) / 10
	whole := int64(v)
	frac := int64(math.Round((v - float64(whole)) * 10))
	if frac == 10 {
		whole, frac = whole+1, 0
	}
	s := strconv.FormatInt(whole, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != 0 {
		out += "." + strconv.FormatInt(frac, 10)
	}
	if neg && out != "0" {
		out = "-" + out
	}
	return out
}

func weight(v float64, unit string) string { return num(v) + " " + unit }

// signed is a change with its sign: +1.5 · -0.9 · ±0.
func signed(v float64) string {
	switch s := num(v); {
	case s == "0":
		return "±0"
	case v > 0:
		return "+" + s
	default:
		return s
	}
}

// setText is one set: 85 kg × 5 · 热身 40 kg × 10 · 30 秒 @ 100 kg · 20 分钟 8 km/h.
func setText(st gymSet, unit string) string {
	var s string
	switch {
	case st.Min > 0:
		s = num(st.Min) + " 分钟"
		if st.Speed > 0 {
			s += " " + num(st.Speed) + " km/h"
		}
		if st.Km > 0 {
			s += " " + num(st.Km) + " km"
		}
	case st.Sec > 0 && st.R == 0:
		s = num(st.Sec) + " 秒"
		if st.W > 0 {
			s += " @ " + weight(st.W, unit)
		}
	case st.W > 0:
		s = weight(st.W, unit) + " × " + num(st.R)
	default:
		s = "自重 × " + num(st.R)
	}
	if st.warmup() {
		s = "热身 " + s
	}
	if !st.Done {
		s += "（没做完）"
	}
	return s
}

// slotText is a planned exercise: 3 组 × 5 次 @ 85 kg · 3 组 × 30 秒 @ 100 kg · 6-8 次 · 20 分钟.
func slotText(sl slot, unit string) string {
	sets := int(math.Max(sl.Sets, 1))
	var s string
	switch {
	case sl.Min > 0 && sl.Reps == 0:
		s = fmt.Sprintf("%d 组 × %s 分钟", sets, num(sl.Min))
		if sl.Speed > 0 {
			s += " " + num(sl.Speed) + " km/h"
		}
		return s
	case sl.Mode == "time" || (sl.Sec > 0 && sl.Reps == 0):
		s = fmt.Sprintf("%d 组 × %s 秒", sets, num(sl.Sec))
	case sl.RepsMin > 0 && sl.RepsMin < sl.Reps:
		s = fmt.Sprintf("%d 组 × %s-%s 次", sets, num(sl.RepsMin), num(sl.Reps))
	default:
		s = fmt.Sprintf("%d 组 × %s 次", sets, num(sl.Reps))
	}
	if sl.Weight > 0 {
		s += " @ " + weight(sl.Weight, unit)
	}
	return s
}

// byText is why a date has its plan.
func byText(by string) string {
	switch by {
	case "coach":
		return "教练周的下一项"
	case "pinned":
		return "教练周，排在这天"
	case "override":
		return "当天单独安排"
	case "rest_override":
		return "当天改成休息"
	case "weekday":
		return "每周计划"
	}
	return ""
}

// dayLabel is a date for a person: 今天 / 明天 / 11-16 周一.
func dayLabel(date, today string) string {
	t, _ := time.Parse(time.DateOnly, today)
	switch date {
	case today:
		return "今天"
	case t.AddDate(0, 0, 1).Format(time.DateOnly):
		return "明天"
	}
	return date[5:] + " " + weekdayName(date)
}

func clockOf(ms float64, loc *time.Location) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(int64(ms)).In(loc).Format("15:04")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
