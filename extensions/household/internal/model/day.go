package model

import "time"

// Day is a calendar date in the form "YYYY-MM-DD"; the empty Day is unset. Valid days compare with < and > as
// strings, because the zero-padded form keeps the calendar order.
type Day string

// DayOf is the date of t in loc. A nil loc is time.Local.
func DayOf(t time.Time, loc *time.Location) Day {
	if loc == nil {
		loc = time.Local
	}
	return Day(t.In(loc).Format(time.DateOnly))
}

// ParseDay reads a "YYYY-MM-DD" date. It reports false for anything else, including dates that do not exist.
func ParseDay(s string) (Day, bool) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Format(time.DateOnly) != s {
		return "", false
	}
	return Day(s), true
}

// Add is the day n days after d. Unset (or invalid) stays unset.
func (d Day) Add(n int) Day {
	t, ok := d.utc()
	if !ok {
		return ""
	}
	return Day(t.AddDate(0, 0, n).Format(time.DateOnly))
}

// Sub is the number of whole days d − o; 0 when either is unset.
func (d Day) Sub(o Day) int {
	dt, ok1 := d.utc()
	ot, ok2 := o.utc()
	if !ok1 || !ok2 {
		return 0
	}
	return int(dt.Sub(ot) / (24 * time.Hour))
}

// utc is the midnight of d in UTC, which keeps day arithmetic free of daylight saving changes.
func (d Day) utc() (time.Time, bool) {
	if d == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.DateOnly, string(d))
	return t, err == nil
}
