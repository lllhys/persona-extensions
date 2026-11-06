package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Long-term statistics (homeassistant_statistics): the recorder's hourly statistics, which it keeps long after the
// states are purged, summed up by hour, day, week or month (WebSocket recorder/statistics_during_period). And energy
// (homeassistant_energy): the sensors of Home Assistant's energy dashboard (energy/get_prefs), totalled the same way.

// StatRow is one period of a statistic.
type StatRow struct {
	Start  flexTime `json:"start"`
	Mean   *float64 `json:"mean"`
	Min    *float64 `json:"min"`
	Max    *float64 `json:"max"`
	Change *float64 `json:"change"`
	Sum    *float64 `json:"sum"`
}

// StatMeta is a statistic's metadata.
type StatMeta struct {
	ID      string `json:"statistic_id"`
	Name    string `json:"name"`
	HasMean bool   `json:"has_mean"`
	// MeanType: newer Home Assistant has mean_type (0 none, 1 arithmetic, 2 circular) instead of has_mean.
	MeanType int    `json:"mean_type"`
	HasSum   bool   `json:"has_sum"`
	Unit     string `json:"statistics_unit_of_measurement"`
	Unit2    string `json:"unit_of_measurement"`
}

func (m StatMeta) unit() string {
	if m.Unit != "" {
		return m.Unit
	}
	return m.Unit2
}

func (m StatMeta) mean() bool { return m.HasMean || m.MeanType > 0 }

var periods = map[string]string{"hour": "小时", "day": "天", "week": "周", "month": "月"}

// Statistics reads ids' statistics from start to end by period (types: what to read).
func (h *ha) Statistics(ctx context.Context, ids []string, start, end time.Time, period string, types []string) (map[string][]StatRow, error) {
	out := map[string][]StatRow{}
	err := h.WS(ctx, "recorder/statistics_during_period", map[string]any{
		"start_time": start.UTC().Format(time.RFC3339), "end_time": end.UTC().Format(time.RFC3339),
		"statistic_ids": ids, "period": period, "types": types,
	}, &out)
	return out, err
}

// StatMetas reads ids' metadata.
func (h *ha) StatMetas(ctx context.Context, ids []string) (map[string]StatMeta, error) {
	var list []StatMeta
	if err := h.WS(ctx, "recorder/get_statistics_metadata", map[string]any{"statistic_ids": ids}, &list); err != nil {
		return nil, err
	}
	out := map[string]StatMeta{}
	for _, m := range list {
		out[m.ID] = m
	}
	return out, nil
}

// periodLabel writes a period's start for people.
func periodLabel(t time.Time, period string) string {
	switch period {
	case "hour":
		return t.Format("01-02 15:00")
	case "week":
		return t.Format("01-02") + " 起的一周"
	case "month":
		return t.Format("2006-01")
	}
	return t.Format("01-02") + " " + weekdays[t.Weekday()]
}

// statArgs are the tools' common args.
type statArgs struct {
	IDs    []string
	Period string
	Days   int
	Start  string
	End    string
}

func (s *statArgs) clean() error {
	if s.Period = strings.TrimSpace(s.Period); s.Period == "" {
		s.Period = "day"
	}
	if _, ok := periods[s.Period]; !ok {
		return fmt.Errorf("period 要是 hour、day、week 或 month")
	}
	if s.Days <= 0 {
		s.Days = map[string]int{"hour": 2, "day": 30, "week": 90, "month": 365}[s.Period]
	}
	if s.Days > 730 {
		s.Days = 730
	}
	if s.Period == "hour" && s.Days > 14 {
		return fmt.Errorf("按小时最多 14 天；更长的用 day、week 或 month")
	}
	return nil
}

func (a *app) toolStatistics(ctx context.Context, args statArgs) (string, error) {
	if err := args.clean(); err != nil {
		return "", err
	}
	if len(args.IDs) == 0 || len(args.IDs) > 5 {
		return "", fmt.Errorf("entity_id 要有 1 到 5 个（传感器的实体 id，或统计 id）")
	}
	loc := a.loc()
	from, to, err := span(loc, args.Start, args.End, args.Days*24, 0, 731*24*time.Hour)
	if err != nil {
		return "", err
	}
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	for _, e := range args.IDs {
		if hm.Hidden[e] && hm.AreaOf[e] == nil {
			return "", errNoEntity
		}
	}
	h, _, _ := a.client()
	metas, err := h.StatMetas(ctx, args.IDs)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, sid := range args.IDs {
		m, ok := metas[sid]
		name := sid
		if s, ok := hm.ByID[sid]; ok {
			name = s.Name()
		} else if m.Name != "" {
			name = m.Name
		}
		if !ok {
			fmt.Fprintf(&b, "## %s（%s）\nHome Assistant 没有它的长期统计：只有带 state_class 的传感器（measurement、total、total_increasing）才有；可以用 %s_history 看最近 7 天\n\n", name, sid, id)
			continue
		}
		types := []string{"mean", "min", "max"}
		if m.HasSum {
			types = []string{"change"}
		}
		rows, err := h.Statistics(ctx, []string{sid}, from, to, args.Period, types)
		if err != nil {
			return "", err
		}
		b.WriteString(statBlock(name, sid, m, rows[sid], args.Period, from, to, loc))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// statBlock writes one statistic: a line per period and the totals.
func statBlock(name, sid string, m StatMeta, rows []StatRow, period string, from, to time.Time, loc *time.Location) string {
	var b strings.Builder
	unit := m.unit()
	fmt.Fprintf(&b, "## %s（%s），%s 到 %s，按%s\n", name, sid, from.In(loc).Format("2006-01-02"), to.In(loc).Format("2006-01-02"), periods[period])
	if len(rows) == 0 {
		b.WriteString("这段时间没有统计\n")
		return b.String()
	}
	if m.HasSum {
		total, hi, hiAt := 0.0, 0.0, ""
		for _, r := range rows {
			v := 0.0
			if r.Change != nil {
				v = *r.Change
			}
			total += v
			label := periodLabel(r.Start.In(loc), period)
			if hiAt == "" || v > hi {
				hi, hiAt = v, label
			}
			fmt.Fprintf(&b, "%s：%s %s\n", label, tidy(round2(v)), unit)
		}
		fmt.Fprintf(&b, "合计 %s %s，平均每%s %s %s，最多的是 %s（%s %s）\n", tidy(round2(total)), unit, periods[period],
			tidy(round2(total/float64(len(rows)))), unit, hiAt, tidy(round2(hi)), unit)
		return b.String()
	}
	var lo, hi, sum float64
	n, seenLo, seenHi := 0, false, false
	for _, r := range rows {
		label := periodLabel(r.Start.In(loc), period)
		parts := []string{}
		if r.Mean != nil {
			parts = append(parts, "平均 "+tidy(round2(*r.Mean)))
			sum += *r.Mean
			n++
		}
		if r.Min != nil {
			parts = append(parts, "最低 "+tidy(round2(*r.Min)))
			if !seenLo || *r.Min < lo {
				lo, seenLo = *r.Min, true
			}
		}
		if r.Max != nil {
			parts = append(parts, "最高 "+tidy(round2(*r.Max)))
			if !seenHi || *r.Max > hi {
				hi, seenHi = *r.Max, true
			}
		}
		fmt.Fprintf(&b, "%s：%s %s\n", label, strings.Join(parts, "，"), unit)
	}
	if n > 0 {
		fmt.Fprintf(&b, "整段：平均 %s，最低 %s，最高 %s %s\n", tidy(round2(sum/float64(n))), tidy(round2(lo)), tidy(round2(hi)), unit)
	}
	return b.String()
}

func round2(f float64) float64 {
	if f < 0 {
		return -round2(-f)
	}
	return float64(int64(f*100+0.5)) / 100
}

// EnergyPrefs is the energy dashboard's settings, the fields read here.
type EnergyPrefs struct {
	Sources []struct {
		Type     string `json:"type"` // grid, solar, battery, gas, water
		FlowFrom []struct {
			Stat string `json:"stat_energy_from"`
			Cost string `json:"stat_cost"`
		} `json:"flow_from"`
		FlowTo []struct {
			Stat string `json:"stat_energy_to"`
		} `json:"flow_to"`
		From string `json:"stat_energy_from"`
		To   string `json:"stat_energy_to"`
		Cost string `json:"stat_cost"`
	} `json:"energy_sources"`
	Devices []struct {
		Stat string `json:"stat_consumption"`
		Name string `json:"name"`
	} `json:"device_consumption"`
}

// energyStat is one statistic of the energy settings and what it is.
type energyStat struct {
	ID, What string
	Device   bool
}

func (p EnergyPrefs) stats() []energyStat {
	var out []energyStat
	add := func(id, what string, dev bool) {
		if id != "" {
			out = append(out, energyStat{id, what, dev})
		}
	}
	names := map[string]string{"grid": "电网", "solar": "光伏", "battery": "电池", "gas": "燃气", "water": "用水"}
	for _, s := range p.Sources {
		n := names[s.Type]
		if n == "" {
			n = s.Type
		}
		switch s.Type {
		case "grid":
			for _, f := range s.FlowFrom {
				add(f.Stat, "从电网用电", false)
				add(f.Cost, "电费", false)
			}
			for _, f := range s.FlowTo {
				add(f.Stat, "送回电网", false)
			}
			add(s.From, "从电网用电", false)
			add(s.To, "送回电网", false)
		case "solar":
			add(s.From, "光伏发电", false)
		case "battery":
			add(s.From, "电池放电", false)
			add(s.To, "电池充电", false)
		default:
			add(s.From, n, false)
		}
		if s.Type != "grid" {
			add(s.Cost, n+"费用", false)
		}
	}
	for _, d := range p.Devices {
		add(d.Stat, d.Name, true)
	}
	return out
}

func (a *app) toolEnergy(ctx context.Context, args statArgs) (string, error) {
	if err := args.clean(); err != nil {
		return "", err
	}
	loc := a.loc()
	from, to, err := span(loc, args.Start, args.End, args.Days*24, 0, 731*24*time.Hour)
	if err != nil {
		return "", err
	}
	h, _, _ := a.client()
	var prefs EnergyPrefs
	if err := h.WS(ctx, "energy/get_prefs", nil, &prefs); err != nil {
		if strings.Contains(err.Error(), "not_found") {
			return "Home Assistant 里还没配置能源（设置 › 仪表盘 › 能源）。没配置时可以用 " + id + "_statistics 看单个电量传感器（state_class 是 total_increasing 的）的统计。", nil
		}
		return "", err
	}
	stats := prefs.stats()
	if len(stats) == 0 {
		return "Home Assistant 的能源设置里没有传感器（设置 › 仪表盘 › 能源）。可以用 " + id + "_statistics 看单个电量传感器的统计。", nil
	}
	var ids []string
	for _, s := range stats {
		ids = append(ids, s.ID)
	}
	metas, err := h.StatMetas(ctx, ids)
	if err != nil {
		return "", err
	}
	rows, err := h.Statistics(ctx, ids, from, to, args.Period, []string{"change"})
	if err != nil {
		return "", err
	}
	hm, _ := a.home(ctx, statesFor)
	name := func(s energyStat) string {
		if s.What != "" {
			return s.What
		}
		if hm != nil {
			if st, ok := hm.ByID[s.ID]; ok {
				return st.Name()
			}
		}
		if m, ok := metas[s.ID]; ok && m.Name != "" {
			return m.Name
		}
		return s.ID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "能源，%s 到 %s，按%s（时区 %s）：\n", from.In(loc).Format("2006-01-02"), to.In(loc).Format("2006-01-02"), periods[args.Period], loc)
	type total struct {
		s   energyStat
		sum float64
	}
	var devs []total
	b.WriteString("## 合计\n")
	for _, s := range stats {
		t := 0.0
		for _, r := range rows[s.ID] {
			if r.Change != nil {
				t += *r.Change
			}
		}
		if s.Device {
			devs = append(devs, total{s, t})
			continue
		}
		fmt.Fprintf(&b, "- %s（%s）：%s %s\n", name(s), s.ID, tidy(round2(t)), metas[s.ID].unit())
	}
	if len(devs) > 0 {
		sort.SliceStable(devs, func(i, j int) bool { return devs[i].sum > devs[j].sum })
		b.WriteString("## 各设备\n")
		for _, d := range devs {
			fmt.Fprintf(&b, "- %s（%s）：%s %s\n", name(d.s), d.s.ID, tidy(round2(d.sum)), metas[d.s.ID].unit())
		}
	}
	// the periods of the first source (the grid's use, usually)
	main := stats[0]
	if len(rows[main.ID]) > 1 {
		fmt.Fprintf(&b, "## 每%s的%s\n", periods[args.Period], name(main))
		for _, r := range rows[main.ID] {
			v := 0.0
			if r.Change != nil {
				v = *r.Change
			}
			fmt.Fprintf(&b, "%s：%s %s\n", periodLabel(r.Start.In(loc), args.Period), tidy(round2(v)), metas[main.ID].unit())
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
