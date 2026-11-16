package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var loc = time.FixedZone("UTC+8", 8*3600)

func bkt(minute int64, model, launcher string, in, out int64) Bucket {
	return Bucket{Minute: minute, Model: model, Launcher: launcher, Input: in, Output: out, Requests: 1}
}

// day is the Unix second of midnight on a date in loc, plus the given hour and minute.
func day(date string, hour, minute int) int64 {
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		panic(err)
	}
	return t.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute).Unix()
}

func TestMergeReplacesAndDedupes(t *testing.T) {
	s := Open("")
	m := day("2026-11-13", 9, 0)
	n, err := s.Merge("m1", []Bucket{bkt(m, "alpha", "cli", 100, 10), bkt(m, "alpha", "cli", 100, 10)}, loc)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("first merge changed %d, want 1 (the same minute twice is one bucket)", n)
	}
	n, err = s.Merge("m1", []Bucket{bkt(m, "alpha", "cli", 150, 20)}, loc)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("a changed minute changed %d, want 1", n)
	}
	n, err = s.Merge("m1", []Bucket{bkt(m, "alpha", "cli", 150, 20)}, loc)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the same total again changed %d, want 0", n)
	}
	var got Bucket
	var count int
	if err := s.View(func(st *State) error {
		count = len(st.Days[0].Buckets)
		got = st.Days[0].Buckets[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 || got.Input != 150 || got.Output != 20 {
		t.Fatalf("stored %d buckets, in %d out %d; want the replaced totals", count, got.Input, got.Output)
	}
}

func TestMergeSplitsByDayMachineModelLauncher(t *testing.T) {
	s := Open("")
	a := day("2026-11-12", 23, 30)
	b := day("2026-11-13", 0, 30)
	_, err := s.Merge("m1", []Bucket{
		bkt(a, "alpha", "cli", 1, 1),
		bkt(b, "alpha", "cli", 2, 2),
		bkt(b, "alpha", "", 3, 3),
		bkt(b, "beta", "cli", 4, 4),
	}, loc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Merge("m2", []Bucket{bkt(b, "alpha", "cli", 5, 5)}, loc)
	if err != nil {
		t.Fatal(err)
	}
	days := map[string]int{}
	if err := s.View(func(st *State) error {
		for _, d := range st.Days {
			days[d.Date+" "+d.Machine] = len(d.Buckets)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"2026-11-12 m1": 1, "2026-11-13 m1": 3, "2026-11-13 m2": 1}
	for k, n := range want {
		if days[k] != n {
			t.Errorf("%s has %d buckets, want %d", k, days[k], n)
		}
	}
	if len(days) != len(want) {
		t.Fatalf("days %v, want %v", days, want)
	}
}

func TestMergeRevisitsEarlierDayAfterAppend(t *testing.T) {
	s := Open("")
	yesterday := day("2026-11-12", 9, 0)
	today := day("2026-11-13", 9, 0)
	n, err := s.Merge("m1", []Bucket{
		bkt(yesterday, "alpha", "cli", 1, 0),
		bkt(today, "alpha", "cli", 2, 0),
		bkt(yesterday, "beta", "cli", 4, 0),
	}, loc)
	if err != nil || n != 3 {
		t.Fatalf("changed=%d, err=%v; want 3 buckets", n, err)
	}
	var yesterdayInput, todayInput int64
	models := map[string]bool{}
	if err := s.View(func(st *State) error {
		for _, d := range st.Days {
			for _, b := range d.Buckets {
				switch d.Date {
				case "2026-11-12":
					yesterdayInput += b.Input
					models[b.Model] = true
				case "2026-11-13":
					todayInput += b.Input
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if yesterdayInput != 5 || todayInput != 2 || !models["alpha"] || !models["beta"] {
		t.Fatalf("lost revisited-day buckets: yesterday=%d today=%d models=%v", yesterdayInput, todayInput, models)
	}
}

func TestPruneKeeps400Days(t *testing.T) {
	s := Open("")
	old := day("2025-01-01", 1, 0)
	new := day("2026-11-13", 1, 0)
	if _, err := s.Merge("m1", []Bucket{bkt(old, "alpha", "", 1, 1), bkt(new, "alpha", "", 2, 2)}, loc); err != nil {
		t.Fatal(err)
	}
	var dates []string
	if err := s.View(func(st *State) error {
		for _, d := range st.Days {
			dates = append(dates, d.Date)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(dates) != 1 || dates[0] != "2026-11-13" {
		t.Fatalf("kept %v, want only the day inside 400 days of the newest", dates)
	}
}

func TestFileRoundTripAndRefuse(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	m := day("2026-11-13", 8, 0)
	if _, err := s.Merge("m1", []Bucket{bkt(m, "alpha", "cli", 10, 1)}, loc); err != nil {
		t.Fatal(err)
	}
	var in int64
	if err := Open(dir).View(func(st *State) error {
		in = st.Days[0].Buckets[0].Input
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if in != 10 {
		t.Fatalf("re-read input %d, want 10", in)
	}

	// A file of another format is refused and left as it is.
	bad := []byte("{\"format\": 2, \"days\": []}\n")
	if err := os.WriteFile(filepath.Join(dir, fileName), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Open(dir).View(func(*State) error { return nil })
	if err == nil {
		t.Fatal("a format-2 file was read")
	}
	got, err2 := os.ReadFile(filepath.Join(dir, fileName))
	if err2 != nil {
		t.Fatal(err2)
	}
	if string(got) != string(bad) {
		t.Fatalf("the refused file was changed to %s", got)
	}
}

func TestResolveAndZone(t *testing.T) {
	base := t.TempDir()
	Locator = func() string { return filepath.Join(base, "config", locName) }
	t.Cleanup(func() { Locator = func() string { return "" } })

	data := filepath.Join(base, "hub-data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	Remember(data)
	RememberZone(data, "Asia/Shanghai")

	got, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got != data {
		t.Fatalf("resolved %q, want the recorded %q", got, data)
	}
	if Zone(data).String() != "Asia/Shanghai" {
		t.Fatalf("zone %s, want Asia/Shanghai", Zone(data))
	}
	if Zone(t.TempDir()) != time.Local {
		t.Fatal("a directory without a zone file should use the local zone")
	}
}
