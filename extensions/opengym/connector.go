package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The connector: its status, the assistant's read tools, and the writes. Writes run only from actions.do, which the
// hub calls after its gate; every one is confirmed on the phone each time (confirm_always in the manifest).

const (
	toolSummary      = connectorID + "_summary"
	toolWorkouts     = connectorID + "_workouts"
	toolPlan         = connectorID + "_plan"
	toolBodyweight   = connectorID + "_bodyweight"
	toolExercises    = connectorID + "_exercises"
	actLogWorkout    = connectorID + "_log_workout"
	actCreateRoutine = connectorID + "_create_routine"
	actScheduleDay   = connectorID + "_schedule_day"
	toolBudget       = 40 * time.Second
	toolTextMost     = 30000
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func obj(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func number(desc string, lo, hi float64) map[string]any {
	return map[string]any{"type": "number", "minimum": lo, "maximum": hi, "description": desc}
}

const unitNote = "重量都是用户在 openGym 里设的单位（kg 或 lb，工具的回答里写着）。"

var tools = []tool{
	{Name: toolSummary,
		Description: "用户在 openGym 的训练概况：本周和上周的训练次数、训练量（重量 × 次数）、时长，下一次计划练什么（含动作），最近体重，最近 12 周每周的训练量。问「这周练得怎么样」「下次练什么」先用它。" + unitNote,
		InputSchema: obj(nil, map[string]any{})},
	{Name: toolWorkouts,
		Description: "列出 openGym 里完成的训练，新的在前：日期、名字、时长、组数、训练量、有没有破纪录（PR），以及训练 id。给 workout_id 看一次训练的每个动作、每组的重量和次数。" + unitNote,
		InputSchema: obj(nil, map[string]any{
			"from":       str("从这天起（YYYY-MM-DD）"),
			"to":         str("到这天为止（YYYY-MM-DD）"),
			"limit":      number("最多几条，默认 10", 1, 50),
			"workout_id": str("一次训练的 id（列表里给的）：看它的每一组"),
		})},
	{Name: toolPlan,
		Description: "用户的训练计划：未来 7 天每天练什么（以及为什么：每周计划、当天单独安排、改成休息、教练周），下一次训练，正在进行的教练周的进度，以及每个训练计划（routine）的动作、组数、次数和重量，带计划 id。" + unitNote,
		InputSchema: obj(nil, map[string]any{})},
	{Name: toolBodyweight,
		Description: "用户在 openGym 记的体重，新的在前，以及这段时间的变化。" + unitNote,
		InputSchema: obj(nil, map[string]any{"days": number("最近几天，默认 90", 1, 3650)})},
	{Name: toolExercises,
		Description: "按名字找动作和它的编号：先列用户练过、计划里有的和自建的，再列 openGym 动作库里的。记训练、建计划前用它确认动作编号，不要猜。",
		InputSchema: obj(nil, map[string]any{
			"query": str("动作名里的字，如 卧推、深蹲；不给就列用户用过的"),
			"limit": number("最多几条，默认 20", 1, 50),
		})},
}

var exerciseRef = map[string]any{
	"exercise":      str("动作编号（opengym_exercises 给的，如 0025）或动作的准确名字"),
	"create_custom": boolean("找不到这个动作时新建一个自定义动作（用 exercise 当名字），默认 false"),
	"body_part":     str("新建自定义动作时的部位，如 chest、back、upper legs、shoulders"),
}

func with(base map[string]any, more map[string]any) map[string]any {
	m := map[string]any{}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range more {
		m[k] = v
	}
	return m
}

var actions = []tool{
	{Name: actLogWorkout,
		Description: "在 openGym 里记一次已经完成的训练（写进训练记录，手机上同步后看得到）。每个动作给每一组的次数和重量（或秒数、分钟），可以标热身组。App 自己的 PR 标记不会重算。每次都要用户在手机上确认；令牌要有训练的「应用」权限。" + unitNote,
		InputSchema: obj([]string{"exercises"}, map[string]any{
			"date":             str("哪天练的（YYYY-MM-DD），默认今天，不能是以后"),
			"name":             str("训练的名字，如 推、腿日；默认「训练」"),
			"start_time":       str("开始时间 HH:MM，默认今天就是现在往前推时长，以前的日子是 18:00"),
			"duration_minutes": number("练了多少分钟，默认 60", 1, 600),
			"note":             str("备注"),
			"exercises": map[string]any{"type": "array", "description": "做了的动作，按顺序",
				"items": obj([]string{"exercise", "sets"}, with(exerciseRef, map[string]any{
					"note": str("这个动作的备注"),
					"sets": map[string]any{"type": "array", "description": "每一组",
						"items": obj(nil, map[string]any{
							"reps":    number("次数", 1, 1000),
							"weight":  number("重量，自重就不给", 0, 5000),
							"seconds": number("计时的组：秒数", 1, 3600),
							"minutes": number("有氧：分钟", 1, 600),
							"warmup":  boolean("热身组，不算训练量"),
						})},
				}))},
		})},
	{Name: actCreateRoutine,
		Description: "在 openGym 里建一个训练计划（routine，用户在 App 里能直接开始练），可以同时把它排到某一天。用来给用户建议计划。每次都要用户在手机上确认；令牌要有计划的「应用」权限。" + unitNote,
		InputSchema: obj([]string{"name", "exercises"}, map[string]any{
			"name": str("计划的名字，如 上肢 A"),
			"date": str("同时排到这天（YYYY-MM-DD），可不给"),
			"exercises": map[string]any{"type": "array", "description": "计划里的动作，按顺序",
				"items": obj([]string{"exercise", "sets"}, with(exerciseRef, map[string]any{
					"sets":         number("组数", 1, 20),
					"reps":         number("每组次数（和 seconds 二选一）", 1, 100),
					"seconds":      number("计时动作每组秒数", 1, 3600),
					"weight":       number("重量，自重就不给", 0, 5000),
					"rest_seconds": number("组间休息秒数", 0, 900),
					"note":         str("备注"),
				}))},
		})},
	{Name: actScheduleDay,
		Description: "改某一天的训练安排：练某个计划（routine_id，opengym_plan 给的），改成休息（rest: true），或去掉这天的单独安排、回到每周计划（clear: true）。每次都要用户在手机上确认；令牌要有计划的「应用」权限。",
		InputSchema: obj([]string{"date"}, map[string]any{
			"date":       str("哪天（YYYY-MM-DD），今天或以后"),
			"routine_id": str("这天练的计划 id"),
			"rest":       boolean("这天休息"),
			"clear":      boolean("去掉这天的单独安排"),
		})},
}

func bad(msg string) error { return &ext.Error{Code: "invalid_params", Message: msg} }

func (a *app) connector(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p struct {
		Contribution string          `json:"contribution"`
		Name         string          `json:"name"`
		Args         json.RawMessage `json:"args"`
		RequestID    string          `json:"request_id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, bad(err.Error())
		}
	}
	if p.Contribution != "" && p.Contribution != connectorID {
		return nil, ext.NoContribution(p.Contribution)
	}
	if len(p.Args) == 0 || string(p.Args) == "null" {
		p.Args = json.RawMessage("{}")
	}
	switch method {
	case "status":
		return a.status(), nil
	case "tools.list":
		return map[string]any{"tools": tools}, nil
	case "tools.call":
		ctx, cancel := context.WithTimeout(ctx, toolBudget)
		defer cancel()
		text, err := a.callTool(ctx, p.Name, p.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": clip(text, toolTextMost)}, nil
	case "actions.list":
		return map[string]any{"tools": actions}, nil
	case "actions.describe":
		w, err := a.readWrite(p.Name, p.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"action": map[string]string{"name": p.Name, "visible": "你的 openGym 账号：同步后手机和手表上都看得到"}, "summary": w.summary}, nil
	case "actions.do":
		w, err := a.readWrite(p.Name, p.Args)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, toolBudget)
		defer cancel()
		return a.runWrite(ctx, p.RequestID, w)
	}
	return nil, ext.UnknownMethod("connector." + method)
}

// status answers from memory: not_configured without a usable address or a token, error while the server's last
// answer was a refusal or no answer, ok otherwise.
func (a *app) status() map[string]any {
	h := a.health()
	if h.Config != nil {
		return map[string]any{"state": "not_configured", "error": h.Config.Error(), "guide": []string{
			"在扩展设置里填 openGym 服务器地址（你自建的 openGym 的网址）",
			"在 openGym App 的「设置 › 集成 › API 令牌」里新建一个令牌，填到扩展设置的「API 令牌」里"}}
	}
	st := map[string]any{"state": "ok", "account": h.Server}
	if s := a.snapshot(); s != nil && s.Me.User.Name != "" {
		st["account"] = s.Me.User.Name + " @ " + h.Server
	}
	if !h.Checked.IsZero() {
		st["last_sync"] = h.Checked
	}
	if h.Err != nil {
		st["state"] = "error"
		st["error"] = clip(oneLine(h.Err.Error()), 300)
	}
	return st
}

type readArgs struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	Limit     float64 `json:"limit"`
	WorkoutID string  `json:"workout_id"`
	Days      float64 `json:"days"`
	Query     string  `json:"query"`
}

// fresh syncs unless the last check is recent, and answers the snapshot to read; a failed sync with something read
// before answers that.
func (a *app) fresh(ctx context.Context) (*snapshot, error) {
	err := a.sync(ctx, false)
	s := a.snapshot()
	if s == nil {
		if err == nil {
			err = errors.New("还没有读到 openGym 的数据")
		}
		return nil, err
	}
	return s, nil
}

func (a *app) callTool(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var args readArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", bad("参数不对：" + err.Error())
	}
	switch name {
	case toolSummary, toolWorkouts, toolPlan, toolBodyweight, toolExercises:
	default:
		return "", ext.UnknownMethod("connector.tool " + name)
	}
	s, err := a.fresh(ctx)
	if err != nil {
		return "", err
	}
	if s.Empty {
		return "这个 openGym 账号还没有从手机同步过数据。", nil
	}
	loc := a.zone()
	switch name {
	case toolSummary:
		return a.summaryText(s, loc), nil
	case toolWorkouts:
		if !s.Me.can("workouts", "view") {
			return "API 令牌没有读训练记录的权限：在 openGym 里给令牌的「训练」开「查看」。", nil
		}
		if args.WorkoutID != "" {
			w := s.State.workout(strings.TrimSpace(args.WorkoutID))
			if w == nil {
				return "", bad("没有训练 " + args.WorkoutID + "（id 从 opengym_workouts 的列表拿）")
			}
			return a.workoutText(s.State, *w, loc), nil
		}
		for _, d := range []string{args.From, args.To} {
			if d != "" && !validDate(d) {
				return "", bad("日期要写成 YYYY-MM-DD：" + d)
			}
		}
		limit := 10
		if args.Limit > 0 {
			limit = min(int(args.Limit), 50)
		}
		return a.workoutsText(s.State, args.From, args.To, limit), nil
	case toolPlan:
		if !s.Me.can("plans", "view") {
			return "API 令牌没有读计划的权限：在 openGym 里给令牌的「计划」开「查看」。", nil
		}
		return a.planText(s.State, loc), nil
	case toolBodyweight:
		if !s.Me.can("bodyweight", "view") {
			return "API 令牌没有读体重的权限：在 openGym 里给令牌的「体重」开「查看」。", nil
		}
		days := 90
		if args.Days > 0 {
			days = min(int(args.Days), 3650)
		}
		return a.bodyweightText(s.State, days, loc), nil
	default:
		limit := 20
		if args.Limit > 0 {
			limit = min(int(args.Limit), 50)
		}
		return a.exercisesText(args.Query, limit), nil
	}
}

func (a *app) summaryText(s *snapshot, loc *time.Location) string {
	st, now := s.State, a.now()
	today := dateOf(now, loc)
	var b strings.Builder
	fmt.Fprintf(&b, "openGym（%s）；重量单位 %s；数据 %s 读取\n", firstNonEmpty(s.Me.User.Name, "账号"), st.Unit, s.At.In(loc).Format("2006-01-02 15:04"))
	if s.Me.can("workouts", "view") {
		weeks := weeklyTotals(st, now, loc, 12)
		this, last := weeks[11], weeks[10]
		fmt.Fprintf(&b, "\n本周 %d 次，训练量 %s，%d 分钟；上周 %d 次，%s，%d 分钟\n", this.Sessions, weight(this.Volume, st.Unit), this.Minutes,
			last.Sessions, weight(last.Volume, st.Unit), last.Minutes)
		var parts []string
		for _, w := range weeks {
			parts = append(parts, fmt.Sprintf("%s %s（%d 次）", w.Start[5:], num(w.Volume), w.Sessions))
		}
		b.WriteString("最近 12 周每周训练量（" + st.Unit + "，周一开始）：" + strings.Join(parts, "、") + "\n")
		if n := len(st.Workouts); n > 0 {
			w := st.Workouts[n-1]
			fmt.Fprintf(&b, "最近一次：%s「%s」（id %s）\n", w.D, w.title(st), w.ID)
		}
	} else {
		b.WriteString("\nAPI 令牌没有读训练记录的权限。\n")
	}
	if s.Me.can("plans", "view") {
		if n := st.next(now, loc); n != nil {
			fmt.Fprintf(&b, "下一次：%s（%s）%s", n.Date, dayLabel(n.Date, today), n.Routine.Name)
			if t := byText(n.By); t != "" {
				b.WriteString("（" + t + "）")
			}
			b.WriteString("：" + a.routineLine(n.Routine, st.Unit) + "\n")
		} else {
			b.WriteString("下一次：两周内没有安排训练\n")
		}
	}
	if s.Me.can("bodyweight", "view") {
		if bw, ok := st.latestWeight(); ok {
			fmt.Fprintf(&b, "体重 %s（%s）", weight(bw.W, st.Unit), bw.D)
			from := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -30).Format(time.DateOnly)
			if old, ok := st.weightAround(from); ok && old.D != bw.D {
				fmt.Fprintf(&b, "，30 天前 %s，变化 %s %s", weight(old.W, st.Unit), signed(bw.W-old.W), st.Unit)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (a *app) routineLine(r routine, unit string) string {
	var parts []string
	for _, sl := range r.Ex {
		parts = append(parts, a.exName(sl.ID)+" "+slotText(sl, unit))
	}
	if len(parts) == 0 {
		return "（没有动作）"
	}
	return strings.Join(parts, "；")
}

func (a *app) workoutLine(st gymState, w workout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s %s「%s」（id %s）：", w.D, weekdayName(w.D), w.title(st), w.ID)
	var parts []string
	if m := w.minutes(); m > 0 {
		parts = append(parts, fmt.Sprintf("%d 分钟", m))
	}
	parts = append(parts, fmt.Sprintf("%d 组", w.doneSets()), "训练量 "+weight(w.volume(), st.Unit))
	b.WriteString(strings.Join(parts, "，"))
	if w.hasPR() {
		var names []string
		for _, id := range w.PRs {
			names = append(names, a.exName(id))
		}
		b.WriteString("，PR：" + strings.Join(names, "、"))
	}
	return b.String()
}

func (a *app) workoutsText(st gymState, from, to string, limit int) string {
	var list []workout
	for i := len(st.Workouts) - 1; i >= 0; i-- {
		w := st.Workouts[i]
		if (from != "" && w.D < from) || (to != "" && w.D > to) {
			continue
		}
		list = append(list, w)
	}
	var b strings.Builder
	what := "训练"
	if from != "" || to != "" {
		what = fmt.Sprintf("%s 到 %s 的训练", firstNonEmpty(from, "最早"), firstNonEmpty(to, "今天"))
	}
	fmt.Fprintf(&b, "%s：%d 次", what, len(list))
	if len(list) > limit {
		fmt.Fprintf(&b, "，这里列最近 %d 次", limit)
		list = list[:limit]
	}
	b.WriteString("（重量单位 " + st.Unit + "）\n")
	for _, w := range list {
		b.WriteString(a.workoutLine(st, w) + "\n")
	}
	if len(st.Workouts) == 0 {
		b.WriteString("还没有完成的训练。")
	}
	return strings.TrimSpace(b.String())
}

func (a *app) workoutText(st gymState, w workout, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "「%s」（id %s）%s %s", w.title(st), w.ID, w.D, weekdayName(w.D))
	if c := clockOf(w.Start, loc); c != "" {
		b.WriteString(" " + c)
	}
	if m := w.minutes(); m > 0 {
		fmt.Fprintf(&b, "，%d 分钟", m)
	}
	fmt.Fprintf(&b, "，%d 组，训练量 %s\n", w.doneSets(), weight(w.volume(), st.Unit))
	if w.hasPR() {
		var names []string
		for _, id := range w.PRs {
			names = append(names, a.exName(id))
		}
		b.WriteString("PR（破纪录）：" + strings.Join(names, "、") + "\n")
	}
	if w.Note != "" {
		b.WriteString("备注：" + clip(w.Note, 500) + "\n")
	}
	for _, e := range w.Entries {
		var sets []string
		for _, s := range e.Sets {
			sets = append(sets, setText(s, st.Unit))
		}
		fmt.Fprintf(&b, "\n%s（%s）：%s", a.exName(e.ID), e.ID, strings.Join(sets, "、"))
		if e.Note != "" {
			b.WriteString("；备注：" + clip(e.Note, 200))
		}
	}
	return strings.TrimSpace(b.String())
}

func (a *app) planText(st gymState, loc *time.Location) string {
	now := a.now()
	today := dateOf(now, loc)
	var b strings.Builder
	b.WriteString("未来 7 天（重量单位 " + st.Unit + "）：\n")
	for _, d := range st.planDays(now, loc, 7) {
		fmt.Fprintf(&b, "- %s（%s）", d.Date, weekdayName(d.Date))
		if len(d.IDs) == 0 {
			b.WriteString("休息")
		} else {
			var names []string
			for _, id := range d.IDs {
				names = append(names, st.routineName(id))
			}
			b.WriteString(strings.Join(names, " + "))
		}
		if t := byText(d.By); t != "" {
			b.WriteString("（" + t + "）")
		}
		b.WriteString("\n")
	}
	if n := st.next(now, loc); n != nil {
		fmt.Fprintf(&b, "\n下一次：%s（%s）%s（id %s）\n", n.Date, dayLabel(n.Date, today), n.Routine.Name, n.Routine.ID)
	}
	if cw := st.coachWeek(now, loc); cw != nil {
		fmt.Fprintf(&b, "\n教练周「%s」，%s 开始", firstNonEmpty(cw.Label, "未命名"), cw.StartsOn)
		if cw.Waiting {
			b.WriteString("（还没开始）")
		}
		b.WriteString("：按顺序练，不固定哪天\n")
		state := map[string]string{"done": "做完了", "next": "下一个", "pinned": "排在 ", "later": "之后"}
		for i, it := range cw.Items {
			s := state[it.State]
			if it.State == "pinned" {
				s += it.PinnedTo
			}
			fmt.Fprintf(&b, "%d. %s（id %s）：%s\n", i+1, it.Name, it.ID, s)
		}
	}
	b.WriteString("\n训练计划：\n")
	if len(st.Routines) == 0 {
		b.WriteString("还没有训练计划。\n")
	}
	for _, r := range st.Routines {
		fmt.Fprintf(&b, "- 「%s」（id %s）：%s\n", r.Name, r.ID, a.routineLine(r, st.Unit))
	}
	return strings.TrimSpace(b.String())
}

func (a *app) bodyweightText(st gymState, days int, loc *time.Location) string {
	now := a.now()
	from := now.In(loc).AddDate(0, 0, -days).Format(time.DateOnly)
	var list []weighIn
	for i := len(st.Bodyweight) - 1; i >= 0; i-- {
		if st.Bodyweight[i].D >= from {
			list = append(list, st.Bodyweight[i])
		}
	}
	if len(list) == 0 {
		return fmt.Sprintf("最近 %d 天没有记体重。", days)
	}
	var b strings.Builder
	first, last := list[len(list)-1], list[0]
	fmt.Fprintf(&b, "最近 %d 天记了 %d 次体重：最新 %s（%s），最早 %s（%s），变化 %s %s\n", days, len(list),
		weight(last.W, st.Unit), last.D, weight(first.W, st.Unit), first.D, signed(last.W-first.W), st.Unit)
	for i, w := range list {
		if i >= 60 {
			fmt.Fprintf(&b, "……还有 %d 次\n", len(list)-i)
			break
		}
		fmt.Fprintf(&b, "- %s %s\n", w.D, weight(w.W, st.Unit))
	}
	return strings.TrimSpace(b.String())
}

func (a *app) exercisesText(q string, limit int) string {
	found := a.findExercises(q, limit)
	var b strings.Builder
	if q == "" {
		b.WriteString("用户练过、计划里有和自建的动作（编号 名字）：\n")
	} else {
		fmt.Fprintf(&b, "名字里有「%s」的动作（编号 名字，用过的在前）：\n", q)
	}
	for _, m := range found {
		b.WriteString(m.ID + " " + m.Name)
		switch {
		case m.Custom:
			b.WriteString("（自建）")
		case m.Used:
			b.WriteString("（用过）")
		}
		b.WriteString("\n")
	}
	if len(found) == 0 {
		b.WriteString("没有找到。")
		if len(a.catalog.all()) == 0 {
			b.WriteString("动作库的名字没有下载（设置里「显示动作的中文名」关着或者下载失败），只能按编号和自建动作找。")
		}
	}
	return strings.TrimSpace(b.String())
}
