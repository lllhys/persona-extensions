package ui

import (
	"fmt"
	"math"
	"strings"
)

// formatTokens writes a token count the way the apps do (apple/Shared/TokenUsage.swift's TokenFormat): K / M / B,
// three significant digits, no trailing zeros, below 1,000 as is, and a value that rounds to 1,000 of a unit steps up
// to the next (999,500 is 1M).
func formatTokens(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return sign + formatPositive(n)
}

// formatPositive formats a non-negative count. Each step returns when the number is below the following unit, so a
// value that rounds to 1,000 of one unit falls through to the next.
func formatPositive(n int64) string {
	switch {
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 999_500:
		return digits(n, 1_000) + "K"
	case n < 999_500_000:
		return digits(n, 1_000_000) + "M"
	case n < 999_500_000_000:
		return digits(n, 1_000_000_000) + "B"
	default:
		return digits(n, 1_000_000_000_000) + "T"
	}
}

// digits writes n/unit with three significant digits and no trailing zeros.
func digits(n, unit int64) string {
	x := float64(n) / float64(unit)
	pow := 100.0
	switch {
	case x >= 100:
		pow = 1
	case x >= 10:
		pow = 10
	}
	s := fmt.Sprintf("%g", math.Round(x*pow)/pow)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// formatChange writes the change of now against before as a signed percent. A before of zero has no percent.
func formatChange(now, before int64) (text, tone string) {
	if before <= 0 {
		if now > 0 {
			return "之前无用量", "neutral"
		}
		return "持平", "neutral"
	}
	pct := (float64(now) - float64(before)) / float64(before) * 100
	switch {
	case pct > 0.5:
		return fmt.Sprintf("+%.0f%%", pct), "caution"
	case pct < -0.5:
		return fmt.Sprintf("%.0f%%", pct), "positive"
	default:
		return "持平", "neutral"
	}
}

// formatRatio writes a budget's used share.
func formatRatio(used, limit int64) (text, tone string) {
	if limit <= 0 {
		return "—", "neutral"
	}
	pct := float64(used) / float64(limit) * 100
	tone = "positive"
	switch {
	case pct >= 100:
		tone = "danger"
	case pct >= 80:
		tone = "caution"
	}
	return fmt.Sprintf("%.0f%%", math.Min(pct, 999)), tone
}

// launcherName is how a launcher with no name is shown.
func launcherName(name string) string {
	if name == "" {
		return "其他"
	}
	return name
}

// modelName is how a model with no name is shown.
func modelName(name string) string {
	if name == "" {
		return "（未命名）"
	}
	return name
}
