package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The writes. Each is read and checked from the cached document without the network (actions.describe must not
// reach out), then built again over the document as it is when it runs: PUT /api/token/data replaces the top-level
// keys it sends, over the revision it read. A conflict (someone else wrote meanwhile) is built once more over the
// document the conflict carries; a second one gives up.

// write is one checked write.
type write struct {
	name    string
	summary string
	scope   string // the token's category that must be at apply
	// build makes the keys to replace over s, and what to answer; no keys means nothing to write (answered as is).
	build func(s *snapshot, requestID string) (map[string]any, string, error)
}

func (a *app) readWrite(name string, raw json.RawMessage) (write, error) {
	var w write
	var err error
	switch name {
	case actLogWorkout:
		w, err = a.logWorkout(raw)
	case actCreateRoutine:
		w, err = a.createRoutine(raw)
	case actScheduleDay:
		w, err = a.scheduleDay(raw)
	default:
		return write{}, ext.UnknownMethod("connector.action " + name)
	}
	if err != nil {
		return write{}, err
	}
	w.name = name
	return w, nil
}

// cached is the document a write is checked against, and the scope check.
func (a *app) cached(scope string) (*snapshot, error) {
	if err := a.health().Config; err != nil {
		return nil, bad(err.Error())
	}
	s := a.snapshot()
	switch {
	case s == nil:
		return nil, bad("还没有读到 openGym 的数据，先用 opengym_summary 看看能不能连上")
	case s.Empty:
		return nil, bad("这个 openGym 账号还没有从手机同步过，先在 App 里同步一次")
	case !s.Me.can(scope, "apply"):
		what := map[string]string{"workouts": "训练", "plans": "计划"}[scope]
		return nil, bad(fmt.Sprintf("API 令牌没有改%s的权限：在 openGym 的「设置 › 集成 › API 令牌」里给%s开「应用」（现在是「%s」），或新建一个令牌",
			what, what, scopeWord(s.Me.Token.Scopes[scope])))
	}
	return s, nil
}

func scopeWord(s string) string {
	switch s {
	case "view":
		return "查看"
	case "apply":
		return "应用"
	}
	return "关"
}

func hashID(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

var clockRE = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)

func cleanText(s string, most int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return clip(strings.TrimSpace(s), most)
}

// maxWeight is the largest weight this unit allows (openGym's routine limit is 2000 kg).
func maxWeight(unit string) float64 {
	if unit == "lb" {
		return 4400
	}
	return 2000
}

func inRange(what string, v *float64, lo, hi float64) (float64, error) {
	if v == nil {
		return 0, nil
	}
	if math.IsNaN(*v) || *v < lo || *v > hi {
		return 0, bad(fmt.Sprintf("%s要在 %s 到 %s 之间", what, num(lo), num(hi)))
	}
	return *v, nil
}

// exRef is how an action names an exercise.
type exRef struct {
	Exercise     string `json:"exercise"`
	CreateCustom bool   `json:"create_custom"`
	BodyPart     string `json:"body_part"`
}

// resolved is an exercise an action uses: its id, its name, and the custom exercise to add with it.
type resolved struct {
	ID     string
	Name   string
	Custom map[string]any
}

func (a *app) resolveRef(s *snapshot, at string, r exRef) (resolved, error) {
	id, err := a.resolveExercise(r.Exercise)
	if err == nil {
		return resolved{ID: id, Name: a.exName(id)}, nil
	}
	name := cleanText(r.Exercise, 80)
	if !r.CreateCustom || name == "" {
		return resolved{}, bad(at + "：" + err.Error())
	}
	for _, c := range s.State.CustomEx {
		if strings.EqualFold(strings.TrimSpace(c.N), name) {
			return resolved{ID: c.ID, Name: c.N}, nil
		}
	}
	cid := hashID("c_pg", strings.ToLower(name))
	c := map[string]any{"id": cid, "n": name, "bp": firstNonEmpty(cleanText(r.BodyPart, 40), "other"), "eq": "", "custom": true}
	return resolved{ID: cid, Name: name, Custom: c}, nil
}

// addCustoms appends the new custom exercises to the document's list; nil when there are none.
func addCustoms(s *snapshot, list []resolved) ([]json.RawMessage, error) {
	var add []map[string]any
	seen := map[string]bool{}
	for _, r := range list {
		if r.Custom != nil && !seen[r.ID] && s.State.custom(r.ID) == nil {
			seen[r.ID] = true
			add = append(add, r.Custom)
		}
	}
	if len(add) == 0 {
		return nil, nil
	}
	cur, err := rawList(s.Raw["customEx"])
	if err != nil {
		return nil, err
	}
	for _, c := range add {
		b, _ := json.Marshal(c)
		cur = append(cur, b)
	}
	return cur, nil
}

func rawList(raw json.RawMessage) ([]json.RawMessage, error) {
	var list []json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return list, nil
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errors.New("openGym 的数据里这一项不是列表，没有改")
	}
	return slices.DeleteFunc(list, func(r json.RawMessage) bool { return string(r) == "null" }), nil
}

// --- opengym_log_workout

type logArgs struct {
	Date      string   `json:"date"`
	Name      string   `json:"name"`
	StartTime string   `json:"start_time"`
	Duration  *float64 `json:"duration_minutes"`
	Note      string   `json:"note"`
	Exercises []struct {
		exRef
		Note string `json:"note"`
		Sets []struct {
			Reps    *float64 `json:"reps"`
			Weight  *float64 `json:"weight"`
			Seconds *float64 `json:"seconds"`
			Minutes *float64 `json:"minutes"`
			Warmup  bool     `json:"warmup"`
		} `json:"sets"`
	} `json:"exercises"`
}

func (a *app) logWorkout(raw json.RawMessage) (write, error) {
	var in logArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		return write{}, bad("参数不对：" + err.Error())
	}
	s, err := a.cached("workouts")
	if err != nil {
		return write{}, err
	}
	loc, now := a.zone(), a.now()
	today := dateOf(now, loc)
	unit := s.State.Unit
	date := firstNonEmpty(strings.TrimSpace(in.Date), today)
	if !validDate(date) {
		return write{}, bad("date 要写成 YYYY-MM-DD")
	}
	if date > today {
		return write{}, bad("只能记已经练完的训练：" + date + " 还没到")
	}
	dur := 60.0
	if in.Duration != nil {
		if dur, err = inRange("duration_minutes ", in.Duration, 1, 600); err != nil {
			return write{}, err
		}
	}
	day, _ := time.ParseInLocation(time.DateOnly, date, loc)
	var start time.Time
	switch {
	case strings.TrimSpace(in.StartTime) != "":
		m := clockRE.FindStringSubmatch(strings.TrimSpace(in.StartTime))
		if m == nil {
			return write{}, bad("start_time 要写成 HH:MM")
		}
		var h, mi int
		fmt.Sscanf(m[1]+" "+m[2], "%d %d", &h, &mi)
		start = time.Date(day.Year(), day.Month(), day.Day(), h, mi, 0, 0, loc)
	case date == today:
		start = now.Add(-time.Duration(dur) * time.Minute).Truncate(time.Minute)
	default:
		start = time.Date(day.Year(), day.Month(), day.Day(), 18, 0, 0, 0, loc)
	}
	if start.After(now) {
		return write{}, bad("start_time 还没到")
	}
	end := start.Add(time.Duration(dur) * time.Minute)
	if end.After(now) {
		return write{}, bad("只能记已经练完的训练：结束时间还没到")
	}
	name := firstNonEmpty(cleanText(in.Name, 80), "训练")
	note := cleanText(in.Note, 280)
	if len(in.Exercises) == 0 || len(in.Exercises) > 40 {
		return write{}, bad("exercises 要有 1 到 40 个动作")
	}
	var refs []resolved
	var entries []map[string]any
	var lines []string
	var vol float64
	for i, ex := range in.Exercises {
		at := fmt.Sprintf("第 %d 个动作", i+1)
		r, err := a.resolveRef(s, at, ex.exRef)
		if err != nil {
			return write{}, err
		}
		if len(ex.Sets) == 0 || len(ex.Sets) > 30 {
			return write{}, bad(at + "要有 1 到 30 组")
		}
		refs = append(refs, r)
		var sets []map[string]any
		var texts []string
		var top float64
		working, warm := 0, 0
		for j, st := range ex.Sets {
			where := fmt.Sprintf("%s第 %d 组的", at, j+1)
			reps, err := inRange(where+"次数", st.Reps, 1, 1000)
			if err != nil {
				return write{}, err
			}
			if reps != math.Trunc(reps) {
				return write{}, bad(where + "次数要是整数")
			}
			w, err := inRange(where+"重量", st.Weight, 0, maxWeight(unit))
			if err != nil {
				return write{}, err
			}
			sec, err := inRange(where+"秒数", st.Seconds, 1, 3600)
			if err != nil {
				return write{}, err
			}
			mins, err := inRange(where+"分钟", st.Minutes, 1, 600)
			if err != nil {
				return write{}, err
			}
			given := 0
			for _, v := range []float64{reps, sec, mins} {
				if v > 0 {
					given++
				}
			}
			if given != 1 {
				return write{}, bad(where + "要给 reps、seconds、minutes 中的一个")
			}
			set := map[string]any{"done": true}
			gs := gymSet{Done: true}
			switch {
			case reps > 0:
				set["w"], set["r"] = w, reps
				gs.W, gs.R = w, reps
			case sec > 0:
				set["sec"], set["w"] = sec, w
				gs.Sec, gs.W = sec, w
			default:
				set["min"] = mins
				gs.Min = mins
			}
			if st.Warmup {
				set["phase"] = "warmup"
				warm++
			} else {
				working++
				if reps > 0 {
					vol += w * reps
				}
				top = math.Max(top, w)
			}
			sets = append(sets, set)
			if !st.Warmup {
				texts = append(texts, setText(gs, unit))
			}
		}
		e := map[string]any{"id": r.ID, "topW": nil, "target": nil, "sets": sets}
		if top > 0 {
			e["topW"] = top
		}
		if n := cleanText(ex.Note, 200); n != "" {
			e["note"] = n
		}
		entries = append(entries, e)
		label := r.Name
		if r.Custom != nil {
			label += "（新建自定义动作）"
		}
		line := fmt.Sprintf("%s %d 组", label, working)
		if warm > 0 {
			line += fmt.Sprintf("（另有热身 %d 组）", warm)
		}
		if len(texts) > 0 {
			line += "：" + strings.Join(texts, "、")
		}
		lines = append(lines, "- "+line)
	}
	summary := fmt.Sprintf("在 openGym 记一次训练：%s %s 起 %s 分钟「%s」\n%s\n训练量 %s", date, start.Format("15:04"), num(dur), name,
		strings.Join(lines, "\n"), weight(vol, unit))
	if note != "" {
		summary += "；备注：" + note
	}
	return write{summary: summary, scope: "workouts", build: func(s *snapshot, requestID string) (map[string]any, string, error) {
		w := map[string]any{"id": hashID("pg", requestID, date, name), "d": date, "start": start.UnixMilli(), "end": end.UnixMilli(),
			"name": name, "routineIds": []any{}, "routineId": nil, "bw": nil, "vol": vol, "prs": []any{}, "entries": entries}
		if note != "" {
			w["note"] = note
		}
		if dup := sameWorkout(s.State, date, entries); dup != nil {
			return nil, fmt.Sprintf("已经有一条一样的训练了：%s「%s」（id %s），没有再记", dup.D, dup.title(s.State), dup.ID), nil
		}
		list, err := rawList(s.Raw["workouts"])
		if err != nil {
			return nil, "", err
		}
		b, _ := json.Marshal(w)
		list = insertByTime(list, b, date, float64(start.UnixMilli()))
		patch := map[string]any{"workouts": list}
		customs, err := addCustoms(s, refs)
		if err != nil {
			return nil, "", err
		}
		if customs != nil {
			patch["customEx"] = customs
		}
		return patch, fmt.Sprintf("记好了：%s「%s」，%d 个动作，训练量 %s（id %s）。手机上的 openGym 同步后就能看到；App 不会给这次补 PR 标记。",
			date, name, len(entries), weight(vol, unit), w["id"]), nil
	}}, nil
}

// sameWorkout is a workout already on date with the same exercises and sets.
func sameWorkout(st gymState, date string, entries []map[string]any) *workout {
	b, _ := json.Marshal(entries)
	var want []entry
	_ = json.Unmarshal(b, &want)
	for i := range st.Workouts {
		w := &st.Workouts[i]
		if w.D != date || len(w.Entries) != len(want) {
			continue
		}
		same := true
		for k := range want {
			if w.Entries[k].ID != want[k].ID || !slices.Equal(w.Entries[k].Sets, want[k].Sets) {
				same = false
				break
			}
		}
		if same {
			return w
		}
	}
	return nil
}

// insertByTime puts item into the list in (d, start) order, after those at the same time, as the app files them.
func insertByTime(list []json.RawMessage, item json.RawMessage, d string, start float64) []json.RawMessage {
	i := len(list)
	for i > 0 {
		var k struct {
			D     string  `json:"d"`
			Start float64 `json:"start"`
		}
		_ = json.Unmarshal(list[i-1], &k)
		if k.D < d || (k.D == d && k.Start <= start) {
			break
		}
		i--
	}
	return slices.Insert(list, i, item)
}

// --- opengym_create_routine

type routineArgs struct {
	Name      string `json:"name"`
	Date      string `json:"date"`
	Exercises []struct {
		exRef
		Sets    *float64 `json:"sets"`
		Reps    *float64 `json:"reps"`
		Seconds *float64 `json:"seconds"`
		Weight  *float64 `json:"weight"`
		Rest    *float64 `json:"rest_seconds"`
		Note    string   `json:"note"`
	} `json:"exercises"`
}

func (a *app) createRoutine(raw json.RawMessage) (write, error) {
	var in routineArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		return write{}, bad("参数不对：" + err.Error())
	}
	s, err := a.cached("plans")
	if err != nil {
		return write{}, err
	}
	unit := s.State.Unit
	name := cleanText(in.Name, 80)
	if name == "" {
		return write{}, bad("要给计划起个名字（name）")
	}
	today := dateOf(a.now(), a.zone())
	date := strings.TrimSpace(in.Date)
	if date != "" && (!validDate(date) || date < today) {
		return write{}, bad("date 要是今天或以后，写成 YYYY-MM-DD")
	}
	if len(in.Exercises) == 0 || len(in.Exercises) > 40 {
		return write{}, bad("exercises 要有 1 到 40 个动作")
	}
	var refs []resolved
	var ex []map[string]any
	var lines []string
	for i, e := range in.Exercises {
		at := fmt.Sprintf("第 %d 个动作", i+1)
		r, err := a.resolveRef(s, at, e.exRef)
		if err != nil {
			return write{}, err
		}
		refs = append(refs, r)
		if e.Sets == nil {
			return write{}, bad(at + "要给组数（sets）")
		}
		sets, err := inRange(at+"的组数", e.Sets, 1, 20)
		if err != nil {
			return write{}, err
		}
		reps, err := inRange(at+"的次数", e.Reps, 1, 100)
		if err != nil {
			return write{}, err
		}
		sec, err := inRange(at+"的秒数", e.Seconds, 1, 3600)
		if err != nil {
			return write{}, err
		}
		w, err := inRange(at+"的重量", e.Weight, 0, maxWeight(unit))
		if err != nil {
			return write{}, err
		}
		rest, err := inRange(at+"的组间休息", e.Rest, 0, 900)
		if err != nil {
			return write{}, err
		}
		if (reps > 0) == (sec > 0) {
			return write{}, bad(at + "要给 reps 或 seconds 中的一个")
		}
		sl := map[string]any{"id": r.ID, "sets": math.Round(sets), "weight": w}
		gs := slot{Sets: math.Round(sets), Weight: w}
		if reps > 0 {
			sl["reps"], sl["mode"] = math.Round(reps), "reps"
			gs.Reps, gs.Mode = math.Round(reps), "reps"
		} else {
			sl["sec"], sl["mode"] = math.Round(sec), "time"
			gs.Sec, gs.Mode = math.Round(sec), "time"
		}
		if e.Rest != nil {
			sl["restSec"] = math.Round(rest)
		}
		if n := cleanText(e.Note, 280); n != "" {
			sl["note"] = n
		}
		ex = append(ex, sl)
		label := r.Name
		if r.Custom != nil {
			label += "（新建自定义动作）"
		}
		lines = append(lines, "- "+label+" "+slotText(gs, unit))
	}
	summary := fmt.Sprintf("在 openGym 新建训练计划「%s」：\n%s", name, strings.Join(lines, "\n"))
	if date != "" {
		summary += fmt.Sprintf("\n排到 %s（%s）", date, weekdayName(date))
	}
	return write{summary: summary, scope: "plans", build: func(s *snapshot, requestID string) (map[string]any, string, error) {
		id := hashID("pg", requestID, name)
		if s.State.routine(id) != nil {
			return nil, "这个计划已经建好了（id " + id + "）", nil
		}
		list, err := rawList(s.Raw["routines"])
		if err != nil {
			return nil, "", err
		}
		b, _ := json.Marshal(map[string]any{"id": id, "name": name, "emoji": "figureStrength", "ex": ex})
		patch := map[string]any{"routines": append(list, b)}
		answer := fmt.Sprintf("建好了训练计划「%s」（id %s），%d 个动作", name, id, len(ex))
		if date != "" {
			plan, err := rawMap(s.Raw["dayPlan"])
			if err != nil {
				return nil, "", err
			}
			plan[date] = mustJSON(id)
			patch["dayPlan"] = plan
			answer += fmt.Sprintf("，排到了 %s（%s）", date, weekdayName(date))
		}
		customs, err := addCustoms(s, refs)
		if err != nil {
			return nil, "", err
		}
		if customs != nil {
			patch["customEx"] = customs
		}
		return patch, answer + "。用户在 App 里同步后就能开始练。", nil
	}}, nil
}

func rawMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(raw) == 0 || string(raw) == "null" {
		return m, nil
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("openGym 的数据里这一项不是对象，没有改")
	}
	return m, nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// --- opengym_schedule_day

type scheduleArgs struct {
	Date      string `json:"date"`
	RoutineID string `json:"routine_id"`
	Rest      bool   `json:"rest"`
	Clear     bool   `json:"clear"`
}

func (a *app) scheduleDay(raw json.RawMessage) (write, error) {
	var in scheduleArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		return write{}, bad("参数不对：" + err.Error())
	}
	date, id := strings.TrimSpace(in.Date), strings.TrimSpace(in.RoutineID)
	today := dateOf(a.now(), a.zone())
	if !validDate(date) || date < today {
		return write{}, bad("date 要是今天或以后，写成 YYYY-MM-DD")
	}
	n := 0
	for _, b := range []bool{id != "", in.Rest, in.Clear} {
		if b {
			n++
		}
	}
	if n != 1 {
		return write{}, bad("routine_id、rest: true、clear: true 要给且只给一个")
	}
	s, err := a.cached("plans")
	if err != nil {
		return write{}, err
	}
	label := fmt.Sprintf("%s（%s）", date, weekdayName(date))
	var summary, answer string
	switch {
	case id != "":
		r := s.State.routine(id)
		if r == nil {
			return write{}, bad("没有计划 " + id + "（id 从 opengym_plan 拿）")
		}
		summary, answer = "把 "+label+" 安排成练「"+r.Name+"」", label+" 改成了练「"+r.Name+"」"
	case in.Rest:
		summary, answer = "把 "+label+" 改成休息", label+" 改成了休息"
	default:
		summary, answer = "去掉 "+label+" 的单独安排，回到每周计划", label+" 回到了每周计划"
	}
	return write{summary: summary, scope: "plans", build: func(s *snapshot, _ string) (map[string]any, string, error) {
		plan, err := rawMap(s.Raw["dayPlan"])
		if err != nil {
			return nil, "", err
		}
		switch {
		case id != "":
			if s.State.routine(id) == nil {
				return nil, "", errors.New("计划 " + id + " 刚被删掉了，没有改")
			}
			plan[date] = mustJSON(id)
		case in.Rest:
			plan[date] = mustJSON("rest")
		default:
			if _, ok := plan[date]; !ok {
				return nil, label + " 本来就没有单独安排", nil
			}
			delete(plan, date)
		}
		return map[string]any{"dayPlan": plan}, answer, nil
	}}, nil
}

// --- running a write

// done remembers the answers of actions.do by request id: the hub may send the same one again.
var (
	doneMu sync.Mutex
	done   = map[string]string{}
)

func (a *app) runWrite(ctx context.Context, requestID string, w write) (any, error) {
	if requestID == "" || len(requestID) > 256 {
		return nil, bad("没有 request_id")
	}
	key := w.name + "\x00" + requestID
	doneMu.Lock()
	text, ok := done[key]
	doneMu.Unlock()
	if ok {
		return map[string]any{"text": text}, nil
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if err := a.sync(ctx, true); err != nil {
		return nil, err
	}
	s, err := a.cached(w.scope)
	if err != nil {
		return nil, err
	}
	text, err = a.put(ctx, s, requestID, w)
	if err != nil {
		return nil, err
	}
	doneMu.Lock()
	if len(done) > 500 {
		done = map[string]string{}
	}
	done[key] = text
	doneMu.Unlock()
	return map[string]any{"text": text}, nil
}

// put builds the write over s and sends it, building it once more over the document a conflict carries.
func (a *app) put(ctx context.Context, s *snapshot, requestID string, w write) (string, error) {
	for attempt := 0; ; attempt++ {
		patch, answer, err := w.build(s, requestID)
		if err != nil || patch == nil {
			return answer, err
		}
		body := map[string]any{"state": patch, "baseRev": s.Rev}
		if s.Wid != "" {
			body["baseWid"] = s.Wid
		}
		var res struct {
			Rev int    `json:"rev"`
			Wid string `json:"wid"`
		}
		err = a.do(ctx, http.MethodPut, "/api/token/data", body, &res)
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusConflict && ae.Code == "" && len(ae.Body) > 0 {
			if attempt > 0 {
				return "", errors.New("和手机上同时的改动冲突了两次，没有写；过一会儿再试")
			}
			var d dataAnswer
			if json.Unmarshal(ae.Body, &d) != nil {
				return "", errors.New("openGym 说有冲突，但没给出当前的数据")
			}
			next, err := a.parse(d)
			if err != nil {
				return "", err
			}
			next.Me = s.Me
			s = next
			continue
		}
		if err != nil {
			return "", err
		}
		// read what was written, so the page, the card and the tools see it
		if err := a.sync(ctx, true); err != nil {
			a.logf("opengym: reading back after a write: %v", err)
		}
		return answer, nil
	}
}
