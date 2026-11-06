package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The whole home's logbook (homeassistant_logbook): what happened in a stretch of time, everywhere or in one area,
// through Home Assistant's /api/logbook. Kept short: an entity that changes all the time is one line with its count,
// hidden areas are left out, times are the user's.

// LogEntry is one logbook entry as the API answers it.
type LogEntry struct {
	When          flexTime `json:"when"`
	Name          string   `json:"name"`
	Message       string   `json:"message"`
	State         string   `json:"state"`
	EntityID      string   `json:"entity_id"`
	Domain        string   `json:"domain"`
	ContextEntity string   `json:"context_entity_id"`
	ContextName   string   `json:"context_name"`
	ContextUser   string   `json:"context_user_id"`
	ContextDomain string   `json:"context_domain"`
	ContextSvc    string   `json:"context_service"`
	ContextEvent  string   `json:"context_event_type"`
}

// flexTime reads a time as Home Assistant writes it: RFC 3339, or seconds / milliseconds since the epoch.
type flexTime struct{ time.Time }

func (t *flexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f > 1e12 { // milliseconds
			f /= 1000
		}
		sec := int64(f)
		t.Time = time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999-07:00", "2006-01-02 15:04:05.999999-07:00"} {
		if v, err := time.Parse(layout, s); err == nil {
			t.Time = v
			return nil
		}
	}
	return fmt.Errorf("not a time: %s", s)
}

// Logbook reads the entries from start to end (entity: only its).
func (h *ha) Logbook(ctx context.Context, start, end time.Time, entity string) ([]LogEntry, error) {
	q := url.Values{"end_time": {end.UTC().Format(time.RFC3339)}}
	if entity != "" {
		q.Set("entity", entity)
	}
	var out []LogEntry
	path := "/api/logbook/" + url.PathEscape(start.UTC().Format(time.RFC3339)) + "?" + q.Encode()
	if err := h.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.Before(out[j].When.Time) })
	return out, nil
}

// span reads a stretch of time from a tool's args: start and end (RFC 3339, or "2006-01-02 15:04" / "2006-01-02"
// in the user's time zone), or the last hours. most is the longest allowed.
func span(loc *time.Location, start, end string, hours int, defHours int, most time.Duration) (time.Time, time.Time, error) {
	now := time.Now()
	parse := func(s string) (time.Time, error) {
		s = strings.TrimSpace(s)
		for _, l := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(l, s, loc); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("「%s」不是时间：写成 2026-01-02 15:04 或 2026-01-02", clip(s, 40))
	}
	to := now
	if end != "" {
		t, err := parse(end)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}
	var from time.Time
	switch {
	case start != "":
		t, err := parse(start)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	case hours > 0:
		from = to.Add(-time.Duration(hours) * time.Hour)
	default:
		from = to.Add(-time.Duration(defHours) * time.Hour)
	}
	switch {
	case !from.Before(to):
		return time.Time{}, time.Time{}, fmt.Errorf("开始要早于结束")
	case to.Sub(from) > most:
		return time.Time{}, time.Time{}, fmt.Errorf("最多查 %d 天", int(most.Hours()/24))
	}
	return from, to, nil
}

// noisyMost is how many entries one entity has in the logbook before they become one line.
const (
	noisyMost   = 8
	logbookMost = 150
)

var weekdays = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

func (a *app) toolLogbook(ctx context.Context, areaQ, entity, start, end string, hours int) (string, error) {
	loc := a.loc()
	from, to, err := span(loc, start, end, hours, 24, 31*24*time.Hour)
	if err != nil {
		return "", err
	}
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	var in map[string]bool
	where := "全屋"
	if areaQ != "" {
		ar := hm.area(areaQ)
		if ar == nil {
			return "", fmt.Errorf("没有区域「%s」；有：%s", clip(areaQ, 30), strings.Join(areaNames(hm.Areas), "、"))
		}
		in = map[string]bool{}
		for _, e := range ar.Entities {
			in[e] = true
		}
		where = ar.Name
	}
	if entity = strings.TrimSpace(entity); entity != "" {
		if !entityRE.MatchString(entity) {
			return "", fmt.Errorf("「%s」不是实体 id", clip(entity, 60))
		}
		if hm.Hidden[entity] && hm.AreaOf[entity] == nil {
			return "", errNoEntity
		}
		where = entity
		if s, ok := hm.ByID[entity]; ok {
			where = s.Name()
		}
	}
	h, _, _ := a.client()
	entries, err := h.Logbook(ctx, from, to, entity)
	if err != nil {
		return "", err
	}
	var kept []LogEntry
	count := map[string]int{}
	for _, e := range entries {
		switch {
		case e.EntityID != "" && hm.Hidden[e.EntityID] && hm.AreaOf[e.EntityID] == nil:
			continue
		case in != nil && !in[e.EntityID]:
			continue
		}
		kept = append(kept, e)
		if e.EntityID != "" {
			count[e.EntityID]++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s 到 %s 的日志（%s）", where, from.In(loc).Format("01-02 15:04"), to.In(loc).Format("01-02 15:04"), loc.String())
	if len(kept) == 0 {
		b.WriteString("：这段时间没有记录")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "，%d 条", len(kept))
	var noisy []string
	for e, n := range count {
		if n > noisyMost {
			noisy = append(noisy, e)
		}
	}
	sort.Strings(noisy)
	if len(noisy) > 0 {
		b.WriteString("；变化太频繁的合成了一行")
	}
	b.WriteString("：\n")
	shown := map[string]bool{}
	var lines []string
	day := ""
	for _, e := range kept {
		t := e.When.In(loc)
		if d := t.Format("01-02") + " " + weekdays[t.Weekday()]; d != day {
			day = d
			lines = append(lines, "## "+d)
		}
		if count[e.EntityID] > noisyMost {
			if shown[e.EntityID] {
				continue
			}
			shown[e.EntityID] = true
			last := e.When.Time
			for _, x := range kept {
				if x.EntityID == e.EntityID {
					last = x.When.Time
				}
			}
			lines = append(lines, fmt.Sprintf("%s %s 变化了 %d 次（到 %s，最后是%s）", t.Format("15:04"), logName(hm, e),
				count[e.EntityID], last.In(loc).Format("01-02 15:04"), orDash(lastState(hm, kept, e.EntityID))))
			continue
		}
		lines = append(lines, t.Format("15:04")+" "+logLine(hm, e))
	}
	if len(lines) > logbookMost {
		fmt.Fprintf(&b, "（太多了，只列最后 %d 行；缩短时间或加 area / entity_id）\n", logbookMost)
		lines = lines[len(lines)-logbookMost:]
	}
	b.WriteString(strings.Join(lines, "\n"))
	return b.String(), nil
}

func logName(hm *home, e LogEntry) string {
	if s, ok := hm.ByID[e.EntityID]; ok {
		return s.Name()
	}
	if e.Name != "" {
		return e.Name
	}
	return orDash(e.EntityID)
}

func lastState(hm *home, kept []LogEntry, entity string) string {
	for i := len(kept) - 1; i >= 0; i-- {
		if kept[i].EntityID == entity {
			return entryState(hm, kept[i])
		}
	}
	return ""
}

func entryState(hm *home, e LogEntry) string {
	if e.State == "" {
		return e.Message
	}
	s := hm.ByID[e.EntityID]
	s.EntityID, s.State = e.EntityID, e.State
	if t := stateText(s); t != "" {
		return t
	}
	return e.State
}

// logLine is one entry: who, what, and what made it happen.
func logLine(hm *home, e LogEntry) string {
	line := logName(hm, e)
	if st := entryState(hm, e); st != "" {
		if e.State != "" {
			line += " → " + st
		} else {
			line += " " + st
		}
	}
	if n := hm.areaName(e.EntityID); n != "" {
		line += "（" + n + "）"
	}
	switch {
	case strings.HasPrefix(e.ContextEntity, "automation.") || strings.HasPrefix(e.ContextEntity, "script."):
		by := e.ContextName
		if s, ok := hm.ByID[e.ContextEntity]; ok {
			by = s.Name()
		}
		line += "，由" + domainName(strings.SplitN(e.ContextEntity, ".", 2)[0]) + "「" + orDash(by) + "」"
	case e.ContextUser != "" && e.ContextDomain != "":
		line += "，有人调用了 " + e.ContextDomain + "." + e.ContextSvc
	case e.ContextUser != "":
		line += "，有人操作"
	}
	return line
}
