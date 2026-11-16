// Package budget keeps the token budgets and says which of them a day's or a week's usage has crossed. A budget is a
// daily or a weekly limit, for one model or for everything (model ""). The alerts already sent are remembered, so the
// same threshold on the same day is reported once.
package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

const (
	fileName = "budgets.json"
	format   = 1

	Daily  = "day"
	Weekly = "week"

	// WarnAt is the first threshold, LimitAt the second. Each is alerted once per day.
	WarnAt  = 0.8
	LimitAt = 1.0

	// SpikeTimes is how many times a model's hour must exceed its mean of the same hour over the past week to be a
	// spike, and SpikeMin is the least it must be on its own, so a quiet hour cannot alert.
	SpikeTimes = 5
	SpikeMin   = 1000
)

// Budget is one limit. Model "" is the global limit.
type Budget struct {
	Model string `json:"model,omitempty"`
	Span  string `json:"span"` // day or week
	Limit int64  `json:"limit"`
}

// ID is the budget's identity: one per model and span.
func (b Budget) ID() string {
	return b.Span + ":" + b.Model
}

// Alert is one threshold already reported, so it is not reported again the same day.
type Alert struct {
	Key  string `json:"key"`
	Date string `json:"date"`
}

// State is the whole file.
type State struct {
	Format  int      `json:"format"`
	Budgets []Budget `json:"budgets"`
	Sent    []Alert  `json:"sent"`
}

// File is the budgets file in dir, or memory when dir is "".
type File struct {
	dir string
	mem *State
}

// Open returns the budgets in dir; dir "" keeps them in memory.
func Open(dir string) *File { return &File{dir: dir} }

// Set adds or replaces the budget for its model and span.
func (f *File) Set(b Budget) error {
	if b.Span != Daily && b.Span != Weekly {
		return errors.New("预算的周期只能是 day 或 week")
	}
	if b.Limit <= 0 {
		return errors.New("预算上限必须大于 0")
	}
	return f.update(func(st *State) {
		for i := range st.Budgets {
			if st.Budgets[i].ID() == b.ID() {
				st.Budgets[i] = b
				return
			}
		}
		st.Budgets = append(st.Budgets, b)
	})
}

// Delete removes the budget for a model and span. Model "" is the global one.
func (f *File) Delete(span, model string) error {
	return f.update(func(st *State) {
		out := st.Budgets[:0]
		for _, b := range st.Budgets {
			if b.Span != span || b.Model != model {
				out = append(out, b)
			}
		}
		st.Budgets = out
	})
}

// List returns the budgets.
func (f *File) List() ([]Budget, error) {
	st, err := f.load()
	if err != nil {
		return nil, err
	}
	return append([]Budget{}, st.Budgets...), nil
}

// Status is one budget against the usage so far.
type Status struct {
	Budget Budget  `json:"budget"`
	Used   int64   `json:"used"`
	Ratio  float64 `json:"ratio"`
}

// Statuses compares every budget with the usage in st. today is the current time; loc decides the day and the week
// (a week starts on Monday).
func (f *File) Statuses(st *store.State, today time.Time, loc *time.Location) ([]Status, error) {
	budgets, err := f.List()
	if err != nil {
		return nil, err
	}
	out := make([]Status, len(budgets))
	for i, b := range budgets {
		used := usedOf(st, b, today, loc)
		out[i] = Status{Budget: b, Used: used, Ratio: float64(used) / float64(b.Limit)}
	}
	return out, nil
}

// Notice is one alert to send.
type Notice struct {
	Key   string // remembered, so the same one is not sent again the same day
	Title string
	Body  string
}

// Alerts returns the alerts that should be sent now and remembers them, so a later call the same day returns none of
// them. A budget alerts at 80% and at 100%, each once a day. A model whose current hour is over five times its mean
// for that hour across the past seven days alerts once.
func (f *File) Alerts(st *store.State, now time.Time, loc *time.Location) ([]Notice, error) {
	stt, err := f.load()
	if err != nil {
		return nil, err
	}
	today := now.In(loc).Format("2006-01-02")
	sent := map[string]bool{}
	for _, a := range stt.Sent {
		if a.Date == today {
			sent[a.Key] = true
		}
	}
	var fresh []Notice
	for _, b := range stt.Budgets {
		used := usedOf(st, b, now, loc)
		ratio := float64(used) / float64(b.Limit)
		for _, at := range []struct {
			ratio float64
			word  string
		}{{LimitAt, "已用完"}, {WarnAt, "已用了八成"}} {
			if ratio < at.ratio {
				continue
			}
			key := fmt.Sprintf("budget:%s:%.0f", b.ID(), at.ratio*100)
			if sent[key] {
				break
			}
			fresh = append(fresh, Notice{Key: key, Title: budgetTitle(b, at.word), Body: budgetBody(b, used)})
			break // the higher threshold wins; the lower one stays unsent and can still fire on a later, smaller day
		}
	}
	fresh = append(fresh, spikes(st, now, loc, sent)...)
	if len(fresh) == 0 {
		return nil, nil
	}
	err = f.update(func(s *State) {
		kept := s.Sent[:0]
		for _, a := range s.Sent {
			if a.Date == today {
				kept = append(kept, a)
			}
		}
		for _, n := range fresh {
			kept = append(kept, Alert{Key: n.Key, Date: today})
		}
		s.Sent = kept
	})
	return fresh, err
}

// spikes reports a model whose current hour exceeds SpikeTimes times its mean of the same hour over the past seven
// days. sent holds the keys already reported today.
func spikes(st *store.State, now time.Time, loc *time.Location, sent map[string]bool) []Notice {
	hour := now.In(loc).Hour()
	today := now.In(loc).Format("2006-01-02")
	from := now.In(loc).AddDate(0, 0, -7).Format("2006-01-02")
	type acc struct {
		sum  int64
		days int
	}
	past := map[string]*acc{}
	current := map[string]int64{}
	for _, d := range st.Days {
		for _, b := range d.Buckets {
			at := time.Unix(b.Minute, 0).In(loc)
			if at.Hour() != hour {
				continue
			}
			day := at.Format("2006-01-02")
			if day == today {
				current[b.Model] += b.Total()
			} else if day >= from && day < today {
				a := past[b.Model]
				if a == nil {
					a = &acc{}
					past[b.Model] = a
				}
				a.sum += b.Total()
				a.days++
			}
		}
	}
	var out []Notice
	for model, now := range current {
		a := past[model]
		if a == nil || a.days == 0 {
			continue
		}
		mean := float64(a.sum) / float64(a.days)
		if mean <= 0 || float64(now) < SpikeTimes*mean || now < SpikeMin {
			continue
		}
		key := "spike:" + model
		if sent[key] {
			continue
		}
		out = append(out, Notice{
			Key:   key,
			Title: fmt.Sprintf("模型 %s 本小时用量异常", modelName(model)),
			Body:  fmt.Sprintf("本小时 %s，是过去 7 天同一小时均值的 %.0f 倍。", comma(now), float64(now)/mean),
		})
	}
	return out
}

// usedOf sums the tokens a budget counts: one model or every model, for today or for the week so far.
func usedOf(st *store.State, b Budget, now time.Time, loc *time.Location) int64 {
	today := now.In(loc).Format("2006-01-02")
	from := today
	if b.Span == Weekly {
		d := now.In(loc)
		wd := int(d.Weekday())
		if wd == 0 {
			wd = 7
		}
		from = d.AddDate(0, 0, -(wd - 1)).Format("2006-01-02")
	}
	var sum int64
	for _, d := range st.Days {
		if d.Date < from || d.Date > today {
			continue
		}
		for _, bk := range d.Buckets {
			if b.Model == "" || b.Model == bk.Model {
				sum += bk.Total()
			}
		}
	}
	return sum
}

func budgetTitle(b Budget, word string) string {
	span := "今日"
	if b.Span == Weekly {
		span = "本周"
	}
	who := "全部模型"
	if b.Model != "" {
		who = "模型 " + modelName(b.Model)
	}
	return fmt.Sprintf("%s%s的预算%s", span, who, word)
}

func budgetBody(b Budget, used int64) string {
	word := "今日"
	if b.Span == Weekly {
		word = "本周"
	}
	return fmt.Sprintf("%s已用 %s / %s token。", word, comma(used), comma(b.Limit))
}

// modelName is a model with no name shown as such, rather than as an empty string.
func modelName(model string) string {
	if model == "" {
		return "（未命名）"
	}
	return model
}

// comma groups a number in thousands, for the notification text.
func comma(n int64) string {
	if n < 0 {
		return "-" + comma(-n)
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// update reads the state, changes it and writes it back.
func (f *File) update(fn func(*State)) error {
	st, err := f.load()
	if err != nil {
		return err
	}
	fn(st)
	return f.save(st)
}

func (f *File) load() (*State, error) {
	if f.dir == "" {
		if f.mem == nil {
			return &State{Format: format}, nil
		}
		return f.mem, nil
	}
	raw, err := os.ReadFile(f.path())
	if errors.Is(err, os.ErrNotExist) {
		return &State{Format: format}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("预算文件 %s 不能解析，已拒绝读取：%w", f.path(), err)
	}
	if st.Format != format {
		return nil, fmt.Errorf("预算文件 %s 的格式是 %d，只能读取格式 %d", f.path(), st.Format, format)
	}
	return &st, nil
}

func (f *File) save(st *State) error {
	st.Format = format
	if st.Budgets == nil {
		st.Budgets = []Budget{}
	}
	if st.Sent == nil {
		st.Sent = []Alert{}
	}
	if f.dir == "" {
		f.mem = st
		return nil
	}
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.path(), append(out, '\n'), 0o600)
}

func (f *File) path() string { return filepath.Join(f.dir, fileName) }
