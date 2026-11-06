package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// The page (ui contribution "watchlist", a nav_page): the watchlist with last prices and changes, a search across
// every source, and an instrument opened in the same page: its figures, its price over the chosen bar and its
// volume, and buttons to keep it, drop it or move it. Only Source's methods are used here, whichever market the
// instrument is in. The state it carries is the open instrument (a ref), its bar and the search.

type pageState struct {
	Inst string `json:"i,omitempty"` // a ref ("okx:BTC-USDT"); a bare symbol is the default source's
	Bar  string `json:"b,omitempty"`
	Q    string `json:"q,omitempty"` // the search: the list page shows what matches it instead of the watchlist
}

const (
	candlesShow  = 60 // bars in the charts (a chart takes 120 points a series)
	refreshEvery = 30 // seconds: the apps draw the page again while it is on screen
)

func (p *plugin) readState(raw string) (pageState, *Ref) {
	var st pageState
	_ = json.Unmarshal([]byte(raw), &st)
	var open *Ref
	if r, ok := p.srcs.parse(st.Inst); ok && st.Inst != "" {
		open, st.Inst = &r, r.String()
	} else {
		st.Inst = ""
	}
	if !barKnown(st.Bar) {
		st.Bar = ""
	}
	st.Q = cleanQuery(st.Q)
	return st, open
}

func (st pageState) encode() string {
	b, _ := json.Marshal(st)
	return string(b)
}

// draw is how the asking app draws: the time zone, whether it has UI version 2 (charts, stat grids, segmented) and 3
// (a search box).
type draw struct {
	loc    *time.Location
	v2, v3 bool
}

func (p *plugin) drawFor(r pluginkit.UIRequest) draw {
	d := draw{loc: p.loc, v2: r.Draws(2), v3: r.Draws(3)}
	if d.loc == nil { // the extension's own setting wins; else the user's, as the hub says it
		d.loc = r.Location()
	}
	return d
}

func (p *plugin) uiRender(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var r pluginkit.UIRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	st, open := p.readState(r.State)
	return p.page(ctx, p.drawFor(r), st, open, false), nil
}

func (p *plugin) uiAction(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var r pluginkit.UIRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	st, open := p.readState(r.State)
	refresh, toast := false, ""
	kind, arg, _ := strings.Cut(r.Action, ":") // the rest is a ref, itself "source:symbol"
	ref, refOK := p.srcs.parse(arg)
	switch kind {
	case "open":
		if refOK {
			open = &ref
		}
	case "back":
		open = nil
	case "search":
		q, ok := r.Values["q"] // the search box
		if !ok {
			q = r.Values["inst"] // an app before UI version 3: the field and a button
		}
		st.Q, open = cleanQuery(q), nil
	case "clear":
		st.Q = ""
	case "bar":
		if arg == "" { // the segmented control: the choice is in the values
			arg = r.Values["bar"]
		}
		if barKnown(arg) {
			st.Bar = arg
		}
	case "look", "add":
		if arg == "" { // typed in the add field
			typed := strings.TrimSpace(r.Values["inst"])
			if ref, refOK = p.srcs.parse(typed); !refOK {
				toast = "「" + clip(typed, 30) + "」不像能查的代码：用上面的搜索，或者写成 BTC-USDT 这样"
				break
			}
		} else if !refOK {
			break
		}
		if kind == "look" {
			open = &ref
			break
		}
		if _, err := p.quote(ctx, ref); err != nil {
			toast = ref.Symbol + "：" + err.Error()
			break
		}
		if msg, err := p.addWatch(ref); err != nil {
			toast = err.Error()
		} else {
			toast = msg
		}
	case "remove":
		if refOK {
			if msg, err := p.removeWatch(ref); err != nil {
				toast = err.Error()
			} else {
				toast = msg
			}
		}
	case "up", "down":
		if refOK {
			by := -1
			if kind == "down" {
				by = 1
			}
			if err := p.moveWatch(ref, by); err != nil {
				toast = err.Error()
			}
		}
	case "refresh":
		refresh, toast = true, "已刷新"
	}
	st.Inst = ""
	if open != nil {
		st.Inst = open.String()
	}
	v := p.page(ctx, p.drawFor(r), st, open, refresh)
	return pluginkit.ActionResult{View: &v, Toast: toast}, nil
}

// page is the watchlist (or a search) or the open instrument; what went wrong is drawn, with a way to try again.
func (p *plugin) page(ctx context.Context, d draw, st pageState, open *Ref, refresh bool) pluginkit.View {
	if open != nil {
		return p.instPage(ctx, d, st, *open)
	}
	return p.listPage(ctx, d, st, refresh)
}

func (p *plugin) listPage(ctx context.Context, d draw, st pageState, refresh bool) pluginkit.View {
	v := pluginkit.View{Title: "行情", State: st.encode(), RefreshAfter: refreshEvery}
	if d.v3 {
		v.Search = &pluginkit.Search{Placeholder: "搜代码或名称：BTC、PEPE、ETH-USDC", Value: st.Q, Action: pluginkit.UIAction{ID: "search"}}
	}
	if st.Q != "" {
		v.Sections = append(v.Sections, p.searchSection(ctx, st.Q, refresh))
		if !d.v3 {
			v.Sections = append(v.Sections, p.addSection(false))
		}
		v.Sections = append(v.Sections, p.readAt(d))
		return v
	}
	watch := p.watchlist()
	sec := pluginkit.Section{Title: fmt.Sprintf("自选 · %d", len(watch))}
	if len(watch) == 0 {
		sec.Items = append(sec.Items, pluginkit.Item{Type: "text", Text: "自选是空的：搜一个加进来", Style: "secondary"})
	} else {
		qs, err := p.quotes(ctx, watch, refresh)
		if err != nil {
			v.Sections = append(v.Sections, pluginkit.Section{Title: sec.Title, Items: []pluginkit.Item{
				{Type: "text", Text: err.Error(), Style: "secondary"},
				{Type: "button", Title: "重试", Action: &pluginkit.UIAction{ID: "refresh"}}}})
			v.Sections = append(v.Sections, p.addSection(d.v3))
			return v
		}
		periods := map[string]bool{}
		for _, r := range watch {
			it := pluginkit.Item{Type: "row", Title: p.srcs.label(r), Action: &pluginkit.UIAction{ID: "open:" + r.String()}}
			q, ok := qs[r]
			if !ok {
				it.Subtitle = "现在查不到这个代码的行情"
				it.Badge = &pluginkit.Badge{Text: "无行情", Tone: "caution"}
				sec.Items = append(sec.Items, it)
				continue
			}
			periods[q.Period] = true
			it.Value = fmtPrice(q.Last)
			it.Subtitle = q.Period + " 高 " + fmtPrice(q.High) + " · 低 " + fmtPrice(q.Low)
			if q.Name != "" {
				it.Subtitle = q.Name + " · " + it.Subtitle
			}
			if c, ok := q.Change(); ok {
				it.Badge = &pluginkit.Badge{Text: fmtChange(c), Tone: changeTone(c)}
			}
			sec.Items = append(sec.Items, it)
		}
		sec.Footer = "点一行看 K 线"
		if periods["24h"] {
			sec.Footer = "24h 涨跌按 24 小时前的价格算。" + sec.Footer
		}
	}
	v.Sections = append(v.Sections, sec, p.addSection(d.v3), p.readAt(d))
	return v
}

// searchSection lists what matches q in every source: a row each, opening the instrument (whose page adds it to
// the watchlist).
func (p *plugin) searchSection(ctx context.Context, q string, refresh bool) pluginkit.Section {
	sec := pluginkit.Section{Title: "搜索「" + q + "」"}
	hits, err := p.search(ctx, q, searchMost, refresh)
	if err != nil {
		sec.Items = []pluginkit.Item{{Type: "text", Text: err.Error(), Style: "secondary"},
			{Type: "button", Title: "重试", Action: &pluginkit.UIAction{ID: "refresh"}}}
		return sec
	}
	watch := p.watchlist()
	for _, h := range hits {
		parts := []string{}
		if h.Quote.Name != "" {
			parts = append(parts, h.Quote.Name)
		}
		if h.Quote.Kind != "" {
			parts = append(parts, h.Quote.Kind)
		}
		if h.Quote.Turnover > 0 {
			parts = append(parts, h.Quote.Period+" 成交额 "+fmtCompactFloat(h.Quote.Turnover)+" "+h.Quote.Currency)
		}
		if slices.Contains(watch, h.Ref) {
			parts = append(parts, "已在自选")
		}
		it := pluginkit.Item{Type: "row", Title: p.srcs.label(h.Ref), Subtitle: strings.Join(parts, " · "), Value: fmtPrice(h.Quote.Last),
			Action: &pluginkit.UIAction{ID: "open:" + h.Ref.String()}}
		if c, ok := h.Quote.Change(); ok {
			it.Badge = &pluginkit.Badge{Text: fmtChange(c), Tone: changeTone(c)}
		}
		sec.Items = append(sec.Items, it)
	}
	switch {
	case len(hits) == 0:
		sec.Items = append(sec.Items, pluginkit.Item{Type: "text", Text: "没有和「" + q + "」对得上的：" + p.srcs.markets(), Style: "secondary"})
	case len(hits) == searchMost:
		sec.Footer = fmt.Sprintf("只列最前面的 %d 个，写得再具体些能找到别的。点一行看 K 线、加入自选", searchMost)
	default:
		sec.Footer = "点一行看 K 线、加入自选"
	}
	sec.Items = append(sec.Items, pluginkit.Item{Type: "button", Title: "返回自选", Action: &pluginkit.UIAction{ID: "clear"}})
	return sec
}

// addSection is the way to add by typing: with a search box (UI version 3) a code typed in full; before it, the
// same field also searches.
func (p *plugin) addSection(v3 bool) pluginkit.Section {
	hint := "直接写代码：BTC-USDT、永续合约 BTC-USDT-SWAP，只写 BTC 当成 BTC-USDT"
	if len(p.srcs) > 1 {
		var ids []string
		for _, s := range p.srcs {
			ids = append(ids, s.ID()+":（"+s.Name()+"）")
		}
		hint += "；不写来源是 " + p.srcs[0].Name() + " 的，别的来源在代码前加：" + strings.Join(ids, "、")
	}
	sec := pluginkit.Section{Title: "添加",
		Footer: hint,
		Items: []pluginkit.Item{
			{Type: "field", Name: "inst", Label: "代码", Kind: "text", Placeholder: "BTC-USDT"},
			{Type: "button", Title: "加入自选", Action: &pluginkit.UIAction{ID: "add", Submit: true}}}}
	if v3 {
		sec.Footer = "不确定代码就用上面的搜索。" + sec.Footer
	} else {
		sec.Items = append(sec.Items, pluginkit.Item{Type: "button", Title: "搜索", Action: &pluginkit.UIAction{ID: "search", Submit: true}})
	}
	return sec
}

func (p *plugin) readAt(d draw) pluginkit.Section {
	return pluginkit.Section{Items: []pluginkit.Item{
		{Type: "text", Text: p.sourceNames() + " 公开行情 · 读于 " + time.Now().In(d.loc).Format("15:04:05"), Style: "caption"},
		{Type: "button", Title: "刷新", Action: &pluginkit.UIAction{ID: "refresh"}}}}
}

func (p *plugin) sourceNames() string {
	var names []string
	for _, s := range p.srcs {
		names = append(names, s.Name())
	}
	return strings.Join(names, "、")
}

// cleanQuery is a search as kept: trimmed, at most 30 characters.
func cleanQuery(q string) string {
	return clip(strings.TrimSpace(q), 30)
}

func (p *plugin) instPage(ctx context.Context, d draw, st pageState, r Ref) pluginkit.View {
	back := pluginkit.Item{Type: "button", Title: "返回自选", Action: &pluginkit.UIAction{ID: "back"}}
	if st.Q != "" {
		back.Title = "返回搜索"
	}
	src := p.srcs.get(r.Source)
	bars := src.Bars() // the K line, over a bar this source has
	if !slices.Contains(bars, st.Bar) {
		st.Bar = p.bar(src)
	}
	bar := st.Bar
	v := pluginkit.View{Title: r.Symbol, State: st.encode(), RefreshAfter: refreshEvery}
	q, err := p.quote(ctx, r)
	if err != nil {
		items := []pluginkit.Item{{Type: "text", Text: err.Error(), Style: "secondary"}}
		if p.watching(r) {
			items = append(items, pluginkit.Item{Type: "button", Title: "移出自选", Role: "destructive",
				Action: &pluginkit.UIAction{ID: "remove:" + r.String()}})
		}
		v.Sections = []pluginkit.Section{{Items: append(items, back)}}
		return v
	}
	if q.Name != "" {
		v.Title = r.Symbol + " " + q.Name
	}

	// the period
	last := pluginkit.Stat{Label: "最新价", Value: fmtPrice(q.Last)}
	if c, ok := q.Change(); ok {
		last.Detail, last.Tone = q.Period+" "+fmtChange(c), changeTone(c)
	}
	figures := []pluginkit.Stat{last, {Label: q.Period + " 最高", Value: fmtPrice(q.High)}, {Label: q.Period + " 最低", Value: fmtPrice(q.Low)},
		{Label: q.Period + " 成交额", Value: fmtCompactFloat(q.Turnover) + " " + q.Currency}}
	if d.v2 {
		v.Sections = append(v.Sections, pluginkit.Section{Items: []pluginkit.Item{{Type: "stat_grid", Stats: figures}}})
	} else {
		var items []pluginkit.Item
		for _, s := range figures {
			items = append(items, pluginkit.Item{Type: "metric", Label: s.Label, Value: s.Value, Detail: s.Detail, Tone: s.Tone})
		}
		v.Sections = append(v.Sections, pluginkit.Section{Items: items})
	}

	kline := pluginkit.Section{Title: "K 线 · " + barTitle(bar)}
	if d.v2 && len(bars) >= 2 && len(bars) <= 5 {
		seg := pluginkit.Item{Type: "segmented", Name: "bar", Value: bar, Action: &pluginkit.UIAction{ID: "bar"}}
		for _, b := range allBars {
			if slices.Contains(bars, b.id) {
				seg.Choices = append(seg.Choices, pluginkit.Choice{Value: b.id, Title: b.short})
			}
		}
		kline.Items = append(kline.Items, seg)
	} else if len(bars) >= 2 {
		for _, b := range allBars {
			if !slices.Contains(bars, b.id) {
				continue
			}
			it := pluginkit.Item{Type: "row", Title: b.title, Action: &pluginkit.UIAction{ID: "bar:" + b.id}}
			if b.id == bar {
				it.Badge = &pluginkit.Badge{Text: "正在看", Tone: "positive"}
			}
			kline.Items = append(kline.Items, it)
		}
	}
	cs, err := p.candles(ctx, r, bar, candlesShow)
	switch {
	case err != nil:
		kline.Items = append(kline.Items, pluginkit.Item{Type: "text", Text: err.Error(), Style: "secondary"})
	case len(cs) == 0:
		kline.Items = append(kline.Items, pluginkit.Item{Type: "text", Text: src.Name() + " 没给这个周期的 K 线", Style: "secondary"})
	default:
		kline.Items = append(kline.Items, klineItems(cs, bar, d.loc, d.v2)...)
	}
	v.Sections = append(v.Sections, kline)

	// what to do with it
	acts := pluginkit.Section{}
	watch := p.watchlist()
	if i := slices.Index(watch, r); i >= 0 {
		if i > 0 {
			acts.Items = append(acts.Items, pluginkit.Item{Type: "button", Title: "上移", Action: &pluginkit.UIAction{ID: "up:" + r.String()}})
		}
		if i < len(watch)-1 {
			acts.Items = append(acts.Items, pluginkit.Item{Type: "button", Title: "下移", Action: &pluginkit.UIAction{ID: "down:" + r.String()}})
		}
		acts.Items = append(acts.Items, pluginkit.Item{Type: "button", Title: "移出自选", Role: "destructive",
			Action: &pluginkit.UIAction{ID: "remove:" + r.String(), Confirm: "把 " + r.Symbol + " 移出自选？"}})
	} else {
		acts.Items = append(acts.Items, pluginkit.Item{Type: "button", Title: "加入自选", Action: &pluginkit.UIAction{ID: "add:" + r.String()}})
	}
	if u := src.URL(r.Symbol); strings.HasPrefix(u, "https://") {
		acts.Items = append(acts.Items, pluginkit.Item{Type: "link", Title: "在 " + src.Name() + " 打开", URL: u})
	}
	acts.Items = append(acts.Items, back,
		pluginkit.Item{Type: "text", Text: src.Name() + " 公开行情 · 读于 " + time.Now().In(d.loc).Format("15:04:05"), Style: "caption"})
	v.Sections = append(v.Sections, acts)
	return v
}

// klineItems are the period's figures, its closing prices as a line and its volume as bars; an app before UI
// version 2 gets the figures only (the hub drops charts for it anyway).
func klineItems(cs []Candle, bar string, loc *time.Location, v2 bool) []pluginkit.Item {
	first, last := cs[0], cs[len(cs)-1]
	hi, lo := first.High, first.Low
	for _, c := range cs {
		hi, lo = math.Max(hi, c.High), math.Min(lo, c.Low)
	}
	span := pluginkit.Stat{Label: fmt.Sprintf("%d 根涨跌", len(cs)), Value: "—"}
	if first.Open != 0 {
		ch := (last.Close - first.Open) / first.Open
		span.Value, span.Tone = fmtChange(ch), changeTone(ch)
	}
	stats := []pluginkit.Stat{span, {Label: "区间最高", Value: fmtPrice(hi)}, {Label: "区间最低", Value: fmtPrice(lo)}}
	if !v2 {
		var items []pluginkit.Item
		for _, s := range stats {
			items = append(items, pluginkit.Item{Type: "metric", Label: s.Label, Value: s.Value, Tone: s.Tone})
		}
		return items
	}
	price := pluginkit.ChartSeries{Name: "收盘价"}
	vol := pluginkit.ChartSeries{Name: "成交量"}
	for _, c := range cs {
		label := barLabel(c.At, bar, loc)
		price.Points = append(price.Points, pluginkit.ChartPoint{Label: label, Value: c.Close,
			// the apps take 40 characters here: the open is the previous close, so it is left out
			Display: clip(fmt.Sprintf("收 %s 高 %s 低 %s", fmtPrice(c.Close), fmtPrice(c.High), fmtPrice(c.Low)), 40)})
		vol.Points = append(vol.Points, pluginkit.ChartPoint{Label: label, Value: c.Vol, Display: fmtCompactFloat(c.Vol)})
	}
	return []pluginkit.Item{
		{Type: "stat_grid", Stats: stats},
		{Type: "chart", Kind: "line", Title: "收盘价", Series: []pluginkit.ChartSeries{price}},
		{Type: "chart", Kind: "bar", Title: "成交量", Series: []pluginkit.ChartSeries{vol}},
	}
}

func barLabel(at time.Time, bar string, loc *time.Location) string {
	at = at.In(loc)
	if bar == "1D" || bar == "1W" {
		return at.Format("1/2")
	}
	return at.Format("1/2 15:04")
}

func changeTone(c float64) string {
	switch {
	case c > 0:
		return "positive"
	case c < 0:
		return "danger"
	}
	return "neutral"
}

func fmtChange(c float64) string {
	return fmt.Sprintf("%+.2f%%", c*100)
}

// fmtPrice writes a price with as many decimals as its size needs: 76330.10, 3.5021, 0.00001234; "—" when unknown.
func fmtPrice(f float64) string {
	if f == 0 {
		return "—"
	}
	return fmtFloat(f)
}

func fmtFloat(f float64) string {
	a := math.Abs(f)
	var s string
	switch {
	case a >= 1000:
		s = strconv.FormatFloat(f, 'f', 2, 64)
	case a >= 1:
		s = strconv.FormatFloat(f, 'f', 4, 64)
	case a == 0:
		return "0"
	default:
		s = strconv.FormatFloat(f, 'g', 4, 64)
		if strings.Contains(s, "e") { // 1.234e-05 reads worse than 0.00001234
			s = strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	if strings.Contains(s, ".") && a < 1000 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// fmtCompactFloat writes a volume short: 1.18B, 15.2K.
func fmtCompactFloat(f float64) string {
	a := math.Abs(f)
	for _, u := range []struct {
		n float64
		s string
	}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "K"}} {
		if a >= u.n {
			return strconv.FormatFloat(f/u.n, 'f', 2, 64) + u.s
		}
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}
