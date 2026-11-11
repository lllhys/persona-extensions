package main

import (
	"testing"
	"time"
)

func TestDayTitleAcrossRequestLocalMidnight(t *testing.T) {
	loc := time.FixedZone("test UTC+8", 8*60*60)
	now := time.Date(2026, 11, 11, 0, 10, 0, 0, loc)
	for _, tt := range []struct {
		name string
		day  time.Time
		want string
	}{
		{"same day", now.Add(-5 * time.Minute), "今天"},
		{"minutes cross midnight", now.Add(-30 * time.Minute), "昨天"},
		{"previous calendar day", now.AddDate(0, 0, -1), "昨天"},
		{"earlier calendar day", now.AddDate(0, 0, -2), "11-09 周一"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Event timestamps arrive in UTC; the view converts them to the request's location before grouping.
			if got := dayTitle(tt.day.UTC().In(loc), now); got != tt.want {
				t.Fatalf("dayTitle(%s, %s) = %q, want %q", tt.day, now, got, tt.want)
			}
		})
	}
}
