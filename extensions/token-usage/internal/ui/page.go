// Package ui draws the Token 用量 page and the overview card. The page has three tabs: the overview (totals, the
// change, the daily trend and the top models, machines and launchers), the detail (a filterable table that exports a
// CSV) and the budgets. Everything comes from the local store; the hub is asked only for the gateway account list,
// which is hidden when it cannot be read.
package ui

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/hub"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/usage"
)

const (
	PageUI = "token-usage"
	CardUI = "token-usage-card"

	tabOverview = "overview"
	tabDetail   = "detail"
	tabBudget   = "budget"

	topN = 8
)

// pageState is what a page remembers between draws.
type pageState struct {
	Tab      string `json:"tab,omitempty"`
	Days     int    `json:"days,omitempty"`
	Machine  string `json:"machine,omitempty"`
	Model    string `json:"model,omitempty"`
	Launcher string `json:"launcher,omitempty"`
}

func (p pageState) days() int {
	if p.Days == 7 || p.Days == 30 || p.Days == 90 {
		return p.Days
	}
	return 30
}

// App draws both contributions from one store.
type App struct {
	st  *store.Store
	bf  *budget.File
	hub *hub.Client
	now func() time.Time
	loc func() *time.Location
}

// New returns an app. now and loc are functions so a test can pin them.
func New(st *store.Store, bf *budget.File, h *hub.Client, now func() time.Time, loc func() *time.Location) *App {
	return &App{st: st, bf: bf, hub: h, now: now, loc: loc}
}

// Page draws the navigation page.
func (a *App) Page(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	var ps pageState
	if err := r.DecodeState(&ps); err != nil {
		ps = pageState{}
	}
	if ps.Tab == "" {
		ps.Tab = tabOverview
	}
	var view *wire.View
	err := a.st.View(func(st *store.State) error {
		var err error
		view, err = a.page(ctx, st, ps)
		return err
	})
	return view, err
}

// Act answers a button.
func (a *App) Act(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var ps pageState
	_ = r.DecodeState(&ps)
	switch r.Action {
	case "tab":
		ps.Tab = r.Values["tab"]
	case "range":
		ps.Days, _ = strconv.Atoi(r.Values["days"])
	case "filter":
		ps.Machine = r.Values["machine"]
		ps.Model = r.Values["model"]
		ps.Launcher = r.Values["launcher"]
	case "budget-set":
		if err := a.setBudget(r.Values); err != nil {
			return &wire.ActionResult{Toast: err.Error()}, nil
		}
		return &wire.ActionResult{Toast: "已保存预算"}, nil
	case "budget-delete":
		if err := a.bf.Delete(r.Values["span"], r.Values["model"]); err != nil {
			return &wire.ActionResult{Toast: err.Error()}, nil
		}
		return &wire.ActionResult{Toast: "已删除预算"}, nil
	case "export":
		return a.export(ps)
	}
	return &wire.ActionResult{}, nil
}

func (a *App) page(ctx context.Context, st *store.State, ps pageState) (*wire.View, error) {
	now, loc := a.now(), a.loc()
	v := &wire.View{
		Title: "Token 用量",
		Tabs: &wire.Tabs{
			Name:  "tab",
			Value: ps.Tab,
			Items: []wire.Tab{
				{Value: tabOverview, Title: "总览"},
				{Value: tabDetail, Title: "明细"},
				{Value: tabBudget, Title: "预算"},
			},
			Action: wire.Action{ID: "tab"},
		},
		RefreshAfter: 60,
	}
	switch ps.Tab {
	case tabDetail:
		v.Sections = a.detail(st, ps, now, loc)
	case tabBudget:
		v.Sections = a.budgets(st, now, loc)
	default:
		v.Sections = a.overview(ctx, st, ps, now, loc)
	}
	v.State = ext.EncodeState(ps)
	return v, nil
}

// overview is the first tab: today's totals against the previous period, the daily trend and the rankings.
func (a *App) overview(ctx context.Context, st *store.State, ps pageState, now time.Time, loc *time.Location) []wire.Section {
	days := ps.days()
	r := usage.Summarize(st, now, days, loc, usage.Filter{})
	today := usage.Summarize(st, now, 1, loc, usage.Filter{})
	week := usage.Summarize(st, now, 7, loc, usage.Filter{})
	month := usage.Summarize(st, now, 30, loc, usage.Filter{})

	change := func(label string, s usage.Range) wire.Stat {
		text, tone := formatChange(s.Total.Total(), s.Before.Total())
		return wire.Stat{Label: label, Value: formatTokens(s.Total.Total()), Detail: "较上期 " + text, Tone: tone}
	}
	sections := []wire.Section{{
		Items: []wire.Component{{
			Type: wire.ComponentStatGrid,
			Stats: []wire.Stat{
				change("今天", today),
				change("7 天", week),
				change("30 天", month),
			},
		}},
	}}

	sections = append(sections, wire.Section{
		Title: "按天",
		Items: []wire.Component{
			segmented("range", "days", strconv.Itoa(days), []wire.Choice{
				{Value: "7", Title: "7 天"}, {Value: "30", Title: "30 天"}, {Value: "90", Title: "90 天"},
			}),
			trendChart(r),
		},
	})
	sections = append(sections, ranking("按模型", "模型", r.Models))
	sections = append(sections, ranking("按机器", "机器", named(r.Machines, machineName)))
	sections = append(sections, ranking("按启动器", "启动器", named(r.Launchers, launcherName)))

	if ga, ok := a.hub.GatewayAccounts(ctx); ok {
		sections = append(sections, gateways(ga))
	}
	return sections
}

// named rewrites each share's name.
func named(in []usage.Share, name func(string) string) []usage.Share {
	out := make([]usage.Share, len(in))
	for i, s := range in {
		out[i] = s
		out[i].Name = name(s.Name)
	}
	return out
}

func machineName(name string) string {
	if name == "" {
		return "（未知机器）"
	}
	return name
}

// trendChart is the daily bar chart. A range longer than the chart allows is folded into weeks.
func trendChart(r usage.Range) wire.Component {
	points := make([]wire.ChartPoint, 0, len(r.Trend))
	if len(r.Trend) > 60 {
		var sum int64
		var from string
		flush := func(to string) {
			if from == "" {
				return
			}
			points = append(points, wire.ChartPoint{Label: from[5:] + "~", Value: float64(sum), Display: formatTokens(sum)})
			_ = to
			sum, from = 0, ""
		}
		for i, d := range r.Trend {
			if from == "" {
				from = d.Date
			}
			sum += d.Total()
			if (i+1)%7 == 0 {
				flush(d.Date)
			}
		}
		flush("")
	} else {
		for _, d := range r.Trend {
			points = append(points, wire.ChartPoint{Label: d.Date[5:], Value: float64(d.Total()), Display: formatTokens(d.Total())})
		}
	}
	if len(points) == 0 {
		points = []wire.ChartPoint{{Label: "-", Value: 0, Display: "0"}}
	}
	return wire.Component{Type: wire.ComponentChart, Kind: "bar", Series: []wire.ChartSeries{{Name: "token", Points: points}}}
}

// ranking is the top entries of one dimension, the rest folded into 其他.
func ranking(title, unit string, shares []usage.Share) wire.Section {
	if len(shares) == 0 {
		return wire.Section{Title: title, Items: []wire.Component{{Type: wire.ComponentText, Text: "还没有用量。"}}}
	}
	var total int64
	for _, s := range shares {
		total += s.Total()
	}
	shown := shares
	var rest int64
	if len(shares) > topN {
		shown = shares[:topN]
		for _, s := range shares[topN:] {
			rest += s.Total()
		}
	}
	rows := make([]wire.Component, 0, len(shown)+1)
	for _, s := range shown {
		ratio := 0.0
		if total > 0 {
			ratio = float64(s.Total()) / float64(total)
		}
		rows = append(rows, wire.Component{
			Type: wire.ComponentProgress, Label: s.Name, Detail: formatTokens(s.Total()), Ratio: &ratio,
		})
	}
	if rest > 0 {
		ratio := float64(rest) / float64(total)
		rows = append(rows, wire.Component{Type: wire.ComponentProgress, Label: "其他", Detail: formatTokens(rest), Ratio: &ratio})
	}
	return wire.Section{Title: title, Footer: fmt.Sprintf("共 %d 个%s", len(shares), unit), Items: rows}
}

// detail is the filterable table.
func (a *App) detail(st *store.State, ps pageState, now time.Time, loc *time.Location) []wire.Section {
	models, machines, launchers := usage.Names(st)
	f := usage.Filter{Machine: ps.Machine, Model: ps.Model, Launcher: ps.Launcher}
	r := usage.Summarize(st, now, ps.days(), loc, f)
	rows := make([][]string, 0, len(r.Trend))
	for i := len(r.Trend) - 1; i >= 0; i-- {
		d := r.Trend[i]
		if d.Total() == 0 {
			continue
		}
		rows = append(rows, []string{d.Date, formatTokens(d.Total()), formatTokens(d.Input), formatTokens(d.Output), formatTokens(d.CacheRead), strconv.Itoa(d.Requests)})
	}
	items := []wire.Component{
		segmented("range", "days", strconv.Itoa(ps.days()), []wire.Choice{
			{Value: "7", Title: "7 天"}, {Value: "30", Title: "30 天"}, {Value: "90", Title: "90 天"},
		}),
		choice("filter", "machine", "机器", ps.Machine, machines),
		choice("filter", "model", "模型", ps.Model, models),
		choice("filter", "launcher", "启动器", ps.Launcher, append([]string{""}, launchers...)),
	}
	if len(rows) == 0 {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: "这个范围里没有用量。"})
	} else {
		items = append(items, wire.Component{
			Type: wire.ComponentTable,
			Columns: []wire.TableColumn{
				{Title: "日期"}, {Title: "总量", Align: "end"}, {Title: "输入", Align: "end"},
				{Title: "输出", Align: "end"}, {Title: "缓存读", Align: "end"}, {Title: "请求", Align: "end"},
			},
			Rows: rows,
		})
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: "导出 CSV", Action: &wire.Action{ID: "export"}})
	return []wire.Section{{Items: items}}
}

// choice is a drop-down that filters one dimension. The empty value keeps all of them.
func choice(action, name, title, value string, options []string) wire.Component {
	choices := []wire.Choice{{Value: "", Title: "全部" + title}}
	for _, o := range options {
		if o == "" {
			continue
		}
		choices = append(choices, wire.Choice{Value: o, Title: o})
	}
	return wire.Component{
		Type: wire.ComponentField, Kind: "choice", Name: name, Label: title, Value: value, Choices: choices,
		Action: &wire.Action{ID: action, Submit: true},
	}
}

func segmented(action, name, value string, choices []wire.Choice) wire.Component {
	return wire.Component{Type: wire.ComponentSegmented, Name: name, Value: value, Choices: choices, Action: &wire.Action{ID: action}}
}

// export writes the current detail as CSV and hands it back as a download.
func (a *App) export(ps pageState) (*wire.ActionResult, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"date", "machine", "model", "launcher", "input", "cache_write", "cache_read", "output", "requests"})
	err := a.st.View(func(st *store.State) error {
		now, loc := a.now(), a.loc()
		from := now.In(loc).AddDate(0, 0, -(ps.days() - 1)).Format("2006-01-02")
		to := now.In(loc).Format("2006-01-02")
		f := usage.Filter{Machine: ps.Machine, Model: ps.Model, Launcher: ps.Launcher}
		for _, d := range st.Days {
			if d.Date < from || d.Date > to {
				continue
			}
			for _, b := range d.Buckets {
				if !f.Keeps(d.Machine, b) {
					continue
				}
				_ = w.Write([]string{d.Date, d.Machine, b.Model, b.Launcher,
					strconv.FormatInt(b.Input, 10), strconv.FormatInt(b.CacheWrite, 10),
					strconv.FormatInt(b.CacheRead, 10), strconv.FormatInt(b.Output, 10), strconv.Itoa(b.Requests)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	w.Flush()
	return &wire.ActionResult{Toast: "已生成 CSV，请在明细里查看"}, nil
}

// budgets is the third tab: the budgets and their progress, and the form for a new one.
func (a *App) budgets(st *store.State, now time.Time, loc *time.Location) []wire.Section {
	statuses, err := a.bf.Statuses(st, now, loc)
	items := []wire.Component{}
	if err != nil {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: "预算读不出来：" + err.Error()})
	}
	if len(statuses) == 0 {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: "还没有预算。下面可以加一个。"})
	}
	for _, s := range statuses {
		who := "全部模型"
		if s.Budget.Model != "" {
			who = modelName(s.Budget.Model)
		}
		span := "每日"
		if s.Budget.Span == budget.Weekly {
			span = "每周"
		}
		text, tone := formatRatio(s.Used, s.Budget.Limit)
		ratio := s.Ratio
		if ratio > 1 {
			ratio = 1
		}
		items = append(items, wire.Component{
			Type: wire.ComponentProgress, Label: span + " · " + who, Tone: tone,
			Detail: formatTokens(s.Used) + " / " + formatTokens(s.Budget.Limit) + "（" + text + "）", Ratio: &ratio,
		})
		items = append(items, wire.Component{
			Type: wire.ComponentButton, Title: "删除" + who, Role: "destructive",
			Action: &wire.Action{ID: "budget-delete", Confirm: "删除这个预算？",
				Values: map[string]string{"span": s.Budget.Span, "model": s.Budget.Model}},
		})
	}
	models, _, _ := usage.Names(st)
	modelChoices := []wire.Choice{{Value: "", Title: "全部模型"}}
	for _, m := range models {
		if m != "" {
			modelChoices = append(modelChoices, wire.Choice{Value: m, Title: m})
		}
	}
	form := []wire.Component{
		{Type: wire.ComponentField, Kind: "choice", Name: "model", Label: "范围", Choices: modelChoices},
		{Type: wire.ComponentSegmented, Name: "span", Value: budget.Daily, Choices: []wire.Choice{
			{Value: budget.Daily, Title: "每日"}, {Value: budget.Weekly, Title: "每周"},
		}, Action: &wire.Action{ID: "budget-span"}},
		{Type: wire.ComponentField, Kind: "number", Name: "limit", Label: "上限（token）", Placeholder: "1000000"},
		{Type: wire.ComponentButton, Title: "保存预算", Action: &wire.Action{ID: "budget-set", Submit: true}},
	}
	return []wire.Section{{Title: "当前预算", Items: items}, {Title: "新增或修改", Items: form}}
}

// setBudget reads the form and stores the budget.
func (a *App) setBudget(values map[string]string) error {
	limit, err := strconv.ParseInt(strings.TrimSpace(values["limit"]), 10, 64)
	if err != nil {
		return fmt.Errorf("上限要填一个整数")
	}
	span := values["span"]
	if span == "" {
		span = budget.Daily
	}
	return a.bf.Set(budget.Budget{Model: values["model"], Span: span, Limit: limit})
}

// gateways is the account list, shown only when the hub could provide it.
func gateways(ga hub.GatewayAccounts) wire.Section {
	rows := make([][]string, 0, len(ga.Accounts))
	for _, ac := range ga.Accounts {
		window := "—"
		if len(ac.Windows) > 0 && ac.Windows[0].UsedRatio != nil {
			window = fmt.Sprintf("%.0f%%", *ac.Windows[0].UsedRatio*100)
		}
		rows = append(rows, []string{ac.Provider, ac.Name, ac.Status, window})
	}
	return wire.Section{Title: "网关账号额度", Items: []wire.Component{{
		Type: wire.ComponentTable,
		Columns: []wire.TableColumn{
			{Title: "来源"}, {Title: "账号"}, {Title: "状态"}, {Title: "已用", Align: "end"},
		},
		Rows: rows,
	}}}
}
