package remind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// Modes of Config.Mode. Any other value, including "off", sends nothing.
const (
	modeNotify = "notify"
	modeTodos  = "todos"
	modeBoth   = "both"
)

const (
	defaultAt = "08:30"
	maxTodos  = 20   // to-dos created in one run
	maxTitle  = 120  // runes
	maxBody   = 1000 // runes
	ellipsis  = "…"
)

// Config is the reminder's settings. Mode is notify, todos, both or off ("" means notify). At is the time of day,
// "HH:MM", from which the daily notification is sent ("" means 08:30).
type Config struct{ Mode, At string }

// Alert is something that needs a purchase or a chore that is due. Key identifies it across runs.
type Alert struct{ Key, Title, Notes string }

// Alerts returns one alert per buyable finding and per chore due, in rules order.
func Alerts(h *model.Home, today model.Day) []Alert {
	var as []Alert
	for _, f := range rules.Findings(h, today) {
		if !f.Kind.Buyable() {
			continue
		}
		as = append(as, Alert{
			Key:   "item:" + f.Item.ID,
			Title: "补货：" + f.Item.Title + "（" + f.Kind.Label() + "）",
			Notes: "",
		})
	}
	for _, d := range rules.ChoresDue(h, today) {
		as = append(as, Alert{
			Key:   fmt.Sprintf("chore:%s:%s", d.Chore.ID, d.Chore.Next),
			Title: "家务：" + d.Chore.Title,
			Notes: "到期日 " + string(d.Chore.Next),
		})
	}
	return as
}

// Digest is the daily notification. ok is false when nothing needs attention: no findings at all and no chore due.
// The title counts those items; the body has one line per kind that has items, and one for the chores due.
func Digest(h *model.Home, today model.Day) (title, body string, ok bool) {
	fs := rules.Findings(h, today)
	due := rules.ChoresDue(h, today)
	n := len(fs) + len(due)
	if n == 0 {
		return "", "", false
	}

	byKind := map[rules.Kind][]string{}
	for _, f := range fs {
		byKind[f.Kind] = append(byKind[f.Kind], f.Item.Title)
	}
	var lines []string
	for _, k := range []rules.Kind{rules.Expired, rules.Out, rules.Low, rules.Runout, rules.Expiring} {
		if len(byKind[k]) > 0 {
			lines = append(lines, k.Label()+"："+strings.Join(byKind[k], "、"))
		}
	}
	if len(due) > 0 {
		var names []string
		for _, d := range due {
			names = append(names, d.Chore.Title)
		}
		lines = append(lines, "到期家务："+strings.Join(names, "、"))
	}

	title = truncate(fmt.Sprintf("家庭管家：%d 项需要处理", n), maxTitle)
	body = truncate(strings.Join(lines, "\n"), maxBody)
	return title, body, true
}

// truncate cuts s to at most n runes; a cut string ends with an ellipsis.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + ellipsis
}

// Reminder runs the plan and keeps its state in Path (remind.json), so that a restart repeats nothing.
type Reminder struct {
	Hub    API
	Path   string
	Config Config
}

// state is what the reminder has already done: the day of the last notification, and the to-do id of each alert
// key that has one.
type state struct {
	Notified string            `json:"notified,omitempty"`
	Todos    map[string]string `json:"todos,omitempty"`
}

// Run does what is due at now: the daily notification and the to-dos. It is called on every tick. A failing call
// does not hold back the others: the errors are joined and returned, the state reached so far is saved, and what
// failed is tried again on the next call.
func (r *Reminder) Run(ctx context.Context, h *model.Home, now time.Time, loc *time.Location) error {
	if r.Hub == nil {
		return nil
	}
	mode := r.Config.Mode
	if mode == "" {
		mode = modeNotify
	}
	notifyOn := (mode == modeNotify || mode == modeBoth) && r.Hub.Can(ScopeNotify)
	// With the todos.write scope, the to-dos are also checked when the mode no longer creates them, so that the
	// ones already created are marked done.
	todosScope := r.Hub.Can(ScopeTodos)
	createTodos := (mode == modeTodos || mode == modeBoth) && todosScope
	if !notifyOn && !todosScope {
		return nil
	}
	if r.Path == "" {
		return errors.New("没有提醒状态文件的位置")
	}
	if loc == nil {
		loc = time.Local
	}
	today := model.DayOf(now, loc)

	st := readState(r.Path)
	if !notifyOn && !createTodos && len(st.Todos) == 0 {
		return nil
	}
	err := r.step(ctx, &st, h, now, loc, today, notifyOn, todosScope, createTodos)
	if werr := writeState(r.Path, st); werr != nil {
		err = errors.Join(err, werr)
	}
	return err
}

// step does the notification and the to-dos. A failure in one does not stop the other; both errors are returned.
func (r *Reminder) step(ctx context.Context, st *state, h *model.Home, now time.Time, loc *time.Location,
	today model.Day, notifyOn, todosScope, createTodos bool) error {
	var errN, errT error
	if notifyOn {
		errN = r.notify(ctx, st, h, now, loc, today)
	}
	if todosScope {
		errT = r.todos(ctx, st, h, today, createTodos)
	}
	return errors.Join(errN, errT)
}

// notify sends the daily notification when it is due: from the configured time on, once a day, and only when
// something needs attention.
func (r *Reminder) notify(ctx context.Context, st *state, h *model.Home, now time.Time, loc *time.Location,
	today model.Day) error {
	clock, err := parseAt(r.Config.At)
	if err != nil {
		return err
	}
	if st.Notified == string(today) {
		return nil
	}
	local := now.In(loc)
	if local.Hour()*60+local.Minute() < clock {
		return nil
	}
	title, body, ok := Digest(h, today)
	if !ok {
		return nil
	}
	if err := r.Hub.Notify(ctx, Notice{Title: title, Body: body, ID: "household-" + string(today), UI: "household"}); err != nil {
		return err
	}
	st.Notified = string(today)
	return nil
}

// todos creates a to-do for each alert that has none, when create is set (at most maxTodos attempts per run), and
// marks done the to-dos whose alert is gone, or all of them when create is not set. A failed call is recorded and
// the rest go on; the failed to-do is tried again on the next run.
func (r *Reminder) todos(ctx context.Context, st *state, h *model.Home, today model.Day, create bool) error {
	if st.Todos == nil {
		st.Todos = map[string]string{}
	}
	var alerts []Alert
	if create {
		alerts = Alerts(h, today)
	}
	live := make(map[string]bool, len(alerts))
	var errs []error
	attempts := 0
	for _, a := range alerts {
		live[a.Key] = true
		if _, ok := st.Todos[a.Key]; ok || attempts == maxTodos {
			continue
		}
		attempts++
		id, err := r.Hub.CreateTodo(ctx, Todo{Title: a.Title, Notes: a.Notes, DueDate: string(today)})
		if err != nil {
			errs = append(errs, fmt.Errorf("创建待办 %q：%w", a.Title, err))
			continue
		}
		st.Todos[a.Key] = id
	}
	for _, k := range slices.Sorted(maps.Keys(st.Todos)) {
		if live[k] {
			continue
		}
		if err := r.Hub.DoneTodo(ctx, st.Todos[k]); err != nil {
			errs = append(errs, fmt.Errorf("完成待办 %s：%w", st.Todos[k], err))
			continue
		}
		delete(st.Todos, k)
	}
	return errors.Join(errs...)
}

// parseAt reads "HH:MM" and returns the minutes after midnight. "" is 08:30.
func parseAt(s string) (int, error) {
	if s == "" {
		s = defaultAt
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("提醒时间 %q 不是 HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// readState reads the state file. A missing, unreadable or corrupt file is an empty state.
func readState(path string) state {
	var st state
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &st) != nil {
		return state{}
	}
	for k, id := range st.Todos {
		if id == "" {
			delete(st.Todos, k)
		}
	}
	return st
}

// writeState writes the state file atomically: a temporary file in the same directory, renamed over the old one.
func writeState(path string, st state) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".remind-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(raw)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
