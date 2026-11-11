package model

import (
	"testing"
	"time"
)

func TestParseDay(t *testing.T) {
	tests := []struct {
		in   string
		want Day
		ok   bool
	}{
		{"2026-11-10", "2026-11-10", true},
		{"2024-02-29", "2024-02-29", true},
		{"2026-02-29", "", false}, // not a leap year
		{"2026-02-30", "", false},
		{"2026-1-5", "", false}, // not zero-padded
		{"2026/01/05", "", false},
		{"2026-01-05T00:00:00Z", "", false},
		{"", "", false},
		{"abc", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseDay(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseDay(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDayOf(t *testing.T) {
	shanghai := time.FixedZone("UTC+8", 8*3600)
	tests := []struct {
		name string
		t    time.Time
		loc  *time.Location
		want Day
	}{
		{"utc instant in its own zone", time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC), time.UTC, "2026-05-01"},
		{"late evening utc is next day in utc+8", time.Date(2026, 5, 1, 23, 30, 0, 0, time.UTC), shanghai, "2026-05-02"},
		{"early morning utc+8 is previous day in utc", time.Date(2026, 1, 1, 0, 30, 0, 0, shanghai), time.UTC, "2025-12-31"},
		{"new year in the zone", time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC), shanghai, "2027-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DayOf(tt.t, tt.loc); got != tt.want {
				t.Errorf("DayOf(%v, %v) = %q, want %q", tt.t, tt.loc, got, tt.want)
			}
		})
	}
}

func TestDayAdd(t *testing.T) {
	tests := []struct {
		name string
		d    Day
		n    int
		want Day
	}{
		{"zero", "2026-03-10", 0, "2026-03-10"},
		{"next day", "2026-03-10", 1, "2026-03-11"},
		{"previous day", "2026-03-10", -1, "2026-03-09"},
		{"end of january", "2026-01-31", 1, "2026-02-01"},
		{"end of february in a common year", "2026-02-28", 1, "2026-03-01"},
		{"end of february in a leap year", "2024-02-28", 1, "2024-02-29"},
		{"leap day plus a year", "2024-02-29", 365, "2025-02-28"},
		{"end of year", "2026-12-31", 1, "2027-01-01"},
		{"back across the year", "2027-01-01", -1, "2026-12-31"},
		{"back to a month end", "2026-03-01", -1, "2026-02-28"},
		{"many weeks", "2026-11-10", 70, "2027-01-19"},
		{"unset stays unset", "", 5, ""},
		{"unset stays unset when negative", "", -5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Add(tt.n); got != tt.want {
				t.Errorf("%q.Add(%d) = %q, want %q", tt.d, tt.n, got, tt.want)
			}
		})
	}
}

func TestDaySub(t *testing.T) {
	tests := []struct {
		name string
		d, o Day
		want int
	}{
		{"same day", "2026-03-10", "2026-03-10", 0},
		{"one day", "2026-03-10", "2026-03-09", 1},
		{"negative", "2026-03-09", "2026-03-10", -1},
		{"across february in a common year", "2026-03-01", "2026-02-28", 1},
		{"across february in a leap year", "2024-03-01", "2024-02-28", 2},
		{"a common year", "2026-01-01", "2025-01-01", 365},
		{"a leap year", "2025-01-01", "2024-01-01", 366},
		{"across the year end", "2027-01-02", "2026-12-30", 3},
		{"unset on the left", "", "2026-03-10", 0},
		{"unset on the right", "2026-03-10", "", 0},
		{"both unset", "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Sub(tt.o); got != tt.want {
				t.Errorf("%q.Sub(%q) = %d, want %d", tt.d, tt.o, got, tt.want)
			}
		})
	}
}

func TestDayAddSubRoundTrip(t *testing.T) {
	start := Day("2025-12-20")
	for n := -400; n <= 400; n += 37 {
		if got := start.Add(n).Sub(start); got != n {
			t.Errorf("start.Add(%d).Sub(start) = %d, want %d", n, got, n)
		}
	}
}

func TestDayStringOrder(t *testing.T) {
	// Days compare as strings: the zero-padded form keeps the calendar order.
	if !(Day("2026-01-09") < Day("2026-01-10")) || !(Day("2026-12-31") < Day("2027-01-01")) {
		t.Fatal("zero-padded days do not compare in calendar order")
	}
}
