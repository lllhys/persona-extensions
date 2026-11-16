package main

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"
)

// The parts of openGym's profile document this extension reads (openGym api/openapi.yaml, the app's
// frontend/src/lib/finish-workout.js and log-workout.js). Each list item is read on its own, so one malformed item is
// skipped rather than losing the list. Weights are in the profile's unit (unit: kg or lb), times in epoch
// milliseconds, d a local YYYY-MM-DD date.

type gymState struct {
	Unit       string
	Workouts   []workout // oldest first, as the app keeps them
	Routines   []routine
	Week       map[int][]string // 0 Sunday … 6 Saturday -> routine ids
	DayPlan    map[string]string
	Queue      *queue
	Bodyweight []weighIn // oldest first
	CustomEx   []customEx
	Has        map[string]bool // the top-level keys the token read
}

type workout struct {
	ID         string   `json:"id"`
	D          string   `json:"d"`
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Name       string   `json:"name"`
	RoutineIDs []string `json:"routineIds"`
	RoutineID  string   `json:"routineId"`
	Vol        *float64 `json:"vol"`
	PRs        []string `json:"prs"`
	Note       string   `json:"note"`
	Entries    []entry  `json:"entries"`
}

type entry struct {
	ID   string   `json:"id"`
	Note string   `json:"note"`
	Sets []gymSet `json:"sets"`
}

type gymSet struct {
	W     float64 `json:"w"`
	R     float64 `json:"r"`
	Sec   float64 `json:"sec"`
	Min   float64 `json:"min"`
	Speed float64 `json:"speed"`
	Km    float64 `json:"km"`
	Done  bool    `json:"done"`
	Phase string  `json:"phase"`
}

type routine struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Ex    []slot `json:"ex"`
}

type slot struct {
	ID      string  `json:"id"`
	Sets    float64 `json:"sets"`
	Reps    float64 `json:"reps"`
	RepsMin float64 `json:"repsMin"`
	Sec     float64 `json:"sec"`
	Min     float64 `json:"min"`
	Speed   float64 `json:"speed"`
	Weight  float64 `json:"weight"`
	Mode    string  `json:"mode"`
	RestSec float64 `json:"restSec"`
	Note    string  `json:"note"`
}

type queue struct {
	IDs      []string `json:"ids"`
	Since    float64  `json:"since"`
	StartsOn string   `json:"startsOn"`
	Label    string   `json:"label"`
	Strict   bool     `json:"strict"`
}

type weighIn struct {
	D    string  `json:"d"`
	W    float64 `json:"w"`
	T    float64 `json:"t"`
	Gone bool    `json:"gone"`
}

type customEx struct {
	ID string `json:"id"`
	N  string `json:"n"`
	BP string `json:"bp"`
}

// items decodes a list item by item into T, skipping items that do not fit.
func items[T any](raw json.RawMessage, keep func(T) bool) []T {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := make([]T, 0, len(list))
	for _, r := range list {
		var v T
		if json.Unmarshal(r, &v) == nil && keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func parseState(raw map[string]json.RawMessage) gymState {
	s := gymState{Unit: "kg", Week: map[int][]string{}, DayPlan: map[string]string{}, Has: map[string]bool{}}
	for k := range raw {
		s.Has[k] = true
	}
	var unit string
	if json.Unmarshal(raw["unit"], &unit) == nil && (unit == "kg" || unit == "lb") {
		s.Unit = unit
	}
	s.Workouts = items(raw["workouts"], func(w workout) bool { return w.ID != "" && validDate(w.D) })
	sort.SliceStable(s.Workouts, func(i, j int) bool {
		a, b := s.Workouts[i], s.Workouts[j]
		return a.D < b.D || (a.D == b.D && a.Start < b.Start)
	})
	s.Routines = items(raw["routines"], func(r routine) bool { return r.ID != "" })
	s.Bodyweight = items(raw["bodyweight"], func(b weighIn) bool { return validDate(b.D) && b.W > 0 && !b.Gone })
	sort.SliceStable(s.Bodyweight, func(i, j int) bool { return s.Bodyweight[i].D < s.Bodyweight[j].D })
	s.CustomEx = items(raw["customEx"], func(c customEx) bool { return c.ID != "" })
	var week map[string]json.RawMessage
	if json.Unmarshal(raw["week"], &week) == nil {
		for k, v := range week {
			if len(k) != 1 {
				continue
			}
			d := int(k[0] - '0')
			if d < 0 || d > 6 {
				continue
			}
			var one string
			var many []string
			if json.Unmarshal(v, &one) == nil && one != "" {
				s.Week[d] = []string{one}
			} else if json.Unmarshal(v, &many) == nil {
				s.Week[d] = slices.DeleteFunc(many, func(x string) bool { return x == "" })
			}
		}
	}
	var plan map[string]json.RawMessage
	if json.Unmarshal(raw["dayPlan"], &plan) == nil {
		for k, v := range plan {
			var id string
			if validDate(k) && json.Unmarshal(v, &id) == nil && id != "" {
				s.DayPlan[k] = id
			}
		}
	}
	var q queue
	if json.Unmarshal(raw["queue"], &q) == nil && len(q.IDs) > 0 {
		s.Queue = &q
	}
	return s
}

func validDate(d string) bool {
	_, err := time.Parse(time.DateOnly, d)
	return err == nil
}

func (s gymState) workout(id string) *workout {
	for i := range s.Workouts {
		if s.Workouts[i].ID == id {
			return &s.Workouts[i]
		}
	}
	return nil
}

func (s gymState) routine(id string) *routine {
	for i := range s.Routines {
		if s.Routines[i].ID == id {
			return &s.Routines[i]
		}
	}
	return nil
}

func (s gymState) routineName(id string) string {
	if r := s.routine(id); r != nil && r.Name != "" {
		return r.Name
	}
	return "（已删除的计划）"
}

func (st gymSet) warmup() bool { return st.Phase == "warmup" }

// volume is the workout's volume as the app stored it, else Σ weight × reps over its done working sets.
func (w workout) volume() float64 {
	if w.Vol != nil {
		return *w.Vol
	}
	var v float64
	for _, e := range w.Entries {
		for _, st := range e.Sets {
			if st.Done && !st.warmup() && st.R > 0 && st.W > 0 {
				v += st.W * st.R
			}
		}
	}
	return v
}

func (w workout) hasPR() bool { return len(w.PRs) > 0 }

// minutes is the workout's length, 0 when its times are missing or odd.
func (w workout) minutes() int {
	if w.End <= w.Start || w.Start <= 0 {
		return 0
	}
	m := int((w.End - w.Start) / 60000)
	if m > 24*60 {
		return 0
	}
	return m
}

func (w workout) doneSets() int {
	n := 0
	for _, e := range w.Entries {
		for _, st := range e.Sets {
			if st.Done && !st.warmup() {
				n++
			}
		}
	}
	return n
}

func (w workout) routineIDs() []string {
	if len(w.RoutineIDs) > 0 {
		return w.RoutineIDs
	}
	if w.RoutineID != "" {
		return []string{w.RoutineID}
	}
	return nil
}

func (w workout) title(s gymState) string {
	if strings.TrimSpace(w.Name) != "" {
		return strings.TrimSpace(w.Name)
	}
	if ids := w.routineIDs(); len(ids) > 0 {
		return s.routineName(ids[0])
	}
	return "训练"
}

func (s gymState) latestWeight() (weighIn, bool) {
	if len(s.Bodyweight) == 0 {
		return weighIn{}, false
	}
	return s.Bodyweight[len(s.Bodyweight)-1], true
}

// weightAround is the last weigh-in on or before date.
func (s gymState) weightAround(date string) (weighIn, bool) {
	var hit weighIn
	ok := false
	for _, b := range s.Bodyweight {
		if b.D > date {
			break
		}
		hit, ok = b, true
	}
	return hit, ok
}

// weekTotal is one week (Monday to Sunday) of training.
type weekTotal struct {
	Start    string // the Monday
	Sessions int
	Volume   float64
	Minutes  int
}

// mondayOf is the Monday of t's week, at midnight in loc.
func mondayOf(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// weeklyTotals is the last n weeks, oldest first, the current one last.
func weeklyTotals(s gymState, now time.Time, loc *time.Location, n int) []weekTotal {
	first := mondayOf(now, loc).AddDate(0, 0, -7*(n-1))
	out := make([]weekTotal, n)
	idx := map[string]int{}
	for i := range out {
		day := first.AddDate(0, 0, 7*i)
		out[i].Start = day.Format(time.DateOnly)
		for k := range 7 {
			idx[day.AddDate(0, 0, k).Format(time.DateOnly)] = i
		}
	}
	for _, w := range s.Workouts {
		if i, ok := idx[w.D]; ok {
			out[i].Sessions++
			out[i].Volume += w.volume()
			out[i].Minutes += w.minutes()
		}
	}
	return out
}

// dateOf is t's local date.
func dateOf(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.DateOnly) }

func weekdayOf(date string) time.Weekday {
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return 0
	}
	return t.Weekday()
}

// The plan rules, as openGym's api/queue.js describes them: a date's own entry in dayPlan ("rest", or a routine)
// wins; a live coach week (queue) gives its sessions in order on whatever days the user trains, a session pinned to
// a date (a dayPlan entry naming a session still to do) belongs to that date; then the weekly plan, without the
// coach week's routines while that week is live.

// planDay is what is planned on one date and why: rest_override, pinned, override, weekday or rest.
type planDay struct {
	Date string
	IDs  []string
	By   string
}

// coachView is the coach week as of today.
type coachView struct {
	Label    string
	StartsOn string
	Waiting  bool // it starts later
	Items    []coachItem
}

type coachItem struct {
	ID       string
	Name     string
	State    string // done, next, pinned, later
	PinnedTo string
}

// liveQueue is the queue's sessions that name known routines (each once), and its start date; nil without one.
func (s gymState) liveQueue(loc *time.Location) (ids []string, startsOn string) {
	q := s.Queue
	if q == nil || (q.Strict && q.Since <= 0) {
		return nil, ""
	}
	startsOn = q.StartsOn
	if !validDate(startsOn) {
		if q.Since <= 0 {
			return nil, ""
		}
		startsOn = dateOf(time.UnixMilli(int64(q.Since)), loc)
	}
	seen := map[string]bool{}
	for _, id := range q.IDs {
		if !seen[id] && s.routine(id) != nil {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, ""
	}
	return ids, startsOn
}

// queueDone is the coach-week sessions done as of today: a finished workout on the routine started at or after the
// week was applied, or (unless strict) one dated within the week up to today whose name names the routine.
func (s gymState) queueDone(ids []string, startsOn, today string) map[string]bool {
	q := s.Queue
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	done := map[string]bool{}
	for _, w := range s.Workouts {
		byClock := q.Since > 0 && w.Start >= q.Since
		byName := !q.Strict && w.D >= startsOn && w.D <= today
		if !byClock && !byName {
			continue
		}
		for _, id := range w.routineIDs() {
			if want[id] && (byClock || namesRoutine(w.Name, s.routineName(id))) {
				done[id] = true
			}
		}
	}
	return done
}

// namesRoutine reports whether a workout's name names the routine, also inside a combined "A + B" name.
func namesRoutine(name, routine string) bool {
	if routine == "" {
		return false
	}
	return name == routine || strings.HasPrefix(name, routine+" + ") || strings.HasSuffix(name, " + "+routine) ||
		strings.Contains(name, " + "+routine+" + ")
}

// coachWeek is the live coach week's sessions with their state, nil without one or once it is all done.
func (s gymState) coachWeek(now time.Time, loc *time.Location) *coachView {
	ids, startsOn := s.liveQueue(loc)
	if ids == nil {
		return nil
	}
	today := dateOf(now, loc)
	done := s.queueDone(ids, startsOn, today)
	pins := map[string]string{} // id -> the date it is pinned to, today or later
	for d, id := range s.DayPlan {
		if d >= today && slices.Contains(ids, id) && !done[id] {
			if old, ok := pins[id]; !ok || d < old {
				pins[id] = d
			}
		}
	}
	v := &coachView{Label: s.Queue.Label, StartsOn: startsOn, Waiting: today < startsOn}
	next := ""
	if !v.Waiting {
		if p := s.DayPlan[today]; pins[p] == today {
			next = p
		} else {
			for _, id := range ids {
				if !done[id] && pins[id] == "" {
					next = id
					break
				}
			}
		}
	}
	all := true
	for _, id := range ids {
		it := coachItem{ID: id, Name: s.routineName(id), State: "later"}
		switch {
		case done[id]:
			it.State = "done"
		case id == next:
			it.State = "next"
			all = false
		case pins[id] != "":
			it.State, it.PinnedTo = "pinned", pins[id]
			all = false
		default:
			all = false
		}
		v.Items = append(v.Items, it)
	}
	if all {
		return nil
	}
	return v
}

// planFor is what is planned on date, without the coach week's floating sessions (see next).
func (s gymState) planFor(date, today string, live map[string]bool, open map[string]bool) planDay {
	p := planDay{Date: date, By: "rest"}
	if ov, ok := s.DayPlan[date]; ok {
		switch {
		case ov == "rest":
			p.By = "rest_override"
			return p
		case live[ov] && open[ov] && date >= today:
			p.IDs, p.By = []string{ov}, "pinned"
			return p
		case live[ov]:
			// a fulfilled or stale pin reads as no entry
		case s.routine(ov) != nil:
			p.IDs, p.By = []string{ov}, "override"
			return p
		}
	}
	for _, id := range s.Week[int(weekdayOf(date))] {
		if s.routine(id) != nil && !live[id] {
			p.IDs = append(p.IDs, id)
		}
	}
	if len(p.IDs) > 0 {
		p.By = "weekday"
	}
	return p
}

// planDays is the plan of the n dates from today.
func (s gymState) planDays(now time.Time, loc *time.Location, n int) []planDay {
	today := dateOf(now, loc)
	live, open := s.queueSets(now, loc)
	out := make([]planDay, n)
	d := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 12, 0, 0, 0, loc)
	for i := range out {
		out[i] = s.planFor(d.AddDate(0, 0, i).Format(time.DateOnly), today, live, open)
	}
	return out
}

// queueSets is the live coach week's routines and those still to do; both empty when no week is live today.
func (s gymState) queueSets(now time.Time, loc *time.Location) (live, open map[string]bool) {
	live, open = map[string]bool{}, map[string]bool{}
	ids, startsOn := s.liveQueue(loc)
	today := dateOf(now, loc)
	if ids == nil || today < startsOn {
		return live, open
	}
	done := s.queueDone(ids, startsOn, today)
	for _, id := range ids {
		if !done[id] {
			open[id] = true
		}
	}
	if len(open) == 0 {
		return map[string]bool{}, open
	}
	for _, id := range ids {
		live[id] = true
	}
	return live, open
}

// nextSession is the next planned session.
type nextSession struct {
	Date    string // the date it is planned on; today for a coach-week session
	Routine routine
	Also    []string // more routines planned on the same date
	By      string   // coach, pinned, override, weekday
}

// next is the next session from today: a pinned or planned one today, else the coach week's next, else the first
// planned date in the next two weeks. Today counts only while no workout was logged today.
func (s gymState) next(now time.Time, loc *time.Location) *nextSession {
	today := dateOf(now, loc)
	trained := false
	for _, w := range s.Workouts {
		if w.D == today {
			trained = true
		}
	}
	days := s.planDays(now, loc, 14)
	pick := func(p planDay) *nextSession {
		r := s.routine(p.IDs[0])
		return &nextSession{Date: p.Date, Routine: *r, Also: p.IDs[1:], By: p.By}
	}
	if p := days[0]; !trained && len(p.IDs) > 0 {
		return pick(p) // an explicit routine or today's ordinary weekday plan outranks the floating coach item
	}
	if p := days[0]; !trained && p.By == "rest_override" {
		return nil // an explicit rest day suppresses the coach item too
	}
	if cw := s.coachWeek(now, loc); cw != nil && !cw.Waiting {
		for _, it := range cw.Items {
			if it.State == "next" {
				return &nextSession{Date: today, Routine: *s.routine(it.ID), By: "coach"}
			}
		}
	}
	for i, p := range days {
		if i == 0 && trained {
			continue
		}
		if len(p.IDs) > 0 {
			return pick(p)
		}
	}
	return nil
}

// usedExercises is every exercise id in the workouts and routines, the most recent first.
func (s gymState) usedExercises() []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for i := len(s.Workouts) - 1; i >= 0; i-- {
		for _, e := range s.Workouts[i].Entries {
			add(e.ID)
		}
	}
	for _, r := range s.Routines {
		for _, x := range r.Ex {
			add(x.ID)
		}
	}
	for _, c := range s.CustomEx {
		add(c.ID)
	}
	return out
}

func (s gymState) custom(id string) *customEx {
	for i := range s.CustomEx {
		if s.CustomEx[i].ID == id {
			return &s.CustomEx[i]
		}
	}
	return nil
}
