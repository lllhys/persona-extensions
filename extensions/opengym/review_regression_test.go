package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestScopeRevocationRefreshesScopedDocument(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	f.with(func() { f.scopes["workouts"] = "off" }) // scopes change without a profile rev/wid change
	if err := a.sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if a.snapshot().Me.can("workouts", "view") {
		t.Fatal("new scopes were not read")
	}
	res, err := a.pageAction(context.Background(), uiRequest(pageUI, 4, "workout:w1109", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res.View)
	if strings.Contains(string(raw), "85 kg") {
		t.Fatal("workout sets remain visible after scope revocation")
	}
}

func TestEmptyWeekKeyIgnored(t *testing.T) {
	parseState(map[string]json.RawMessage{"week": json.RawMessage(`{"":[]}`)})
}

func TestExplicitTodayPlanBeatsCoach(t *testing.T) {
	for _, tc := range []struct {
		name, override, want string
	}{
		{"routine", "r_legs", "r_legs"},
		{"rest", "rest", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGym(t)
			a := testApp(t, f, "")
			f.bump(func(st map[string]any) {
				st["queue"] = map[string]any{"ids": []any{"r_push"}, "startsOn": "2026-11-12"}
				st["dayPlan"] = map[string]any{"2026-11-13": tc.override}
			})
			if err := a.sync(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			n := a.snapshot().State.next(today(), zone)
			if tc.want == "" && n != nil {
				t.Fatalf("rest override produced a coach session: %+v", n)
			}
			if tc.want != "" && (n == nil || n.Routine.ID != tc.want) {
				t.Fatalf("got %+v; explicit dayPlan requires %s", n, tc.want)
			}
		})
	}
}

func TestLogWorkoutRejectsFutureEnd(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "") // now 2026-11-13 19:30; this ends at 19:45
	args := map[string]any{"date": "2026-11-13", "start_time": "19:15", "duration_minutes": 30,
		"exercises": []any{map[string]any{"exercise": "0025", "sets": []any{map[string]any{"reps": 5, "weight": 80}}}}}
	out, err := call(t, a, "actions.do", actLogWorkout, args, "future-end")
	if err == nil {
		t.Fatalf("accepted a workout ending in the future: %s", out)
	}
}

func TestLogWorkoutRejectsFractionalReps(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	args := map[string]any{"date": "2026-11-13", "start_time": "19:00", "duration_minutes": 20,
		"exercises": []any{map[string]any{"exercise": "0025", "sets": []any{map[string]any{"reps": 2.5, "weight": 80}}}}}
	out, err := call(t, a, "actions.do", actLogWorkout, args, "fractional-reps")
	if err == nil {
		t.Fatalf("accepted fractional repetitions: %s", out)
	}
}
