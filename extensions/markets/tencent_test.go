package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// fakeTencent answers like Tencent's endpoints from the samples in testdata/tencent, recorded from the live ones:
// quotes for the codes asked that the sample has, the search for "tengxun", and the bars there are files for.
type fakeTencent struct {
	srv    *httptest.Server
	mu     sync.Mutex
	asked  []string // every request, path and query
	quotes map[string][]byte
}

func newFakeTencent(t *testing.T) *fakeTencent {
	t.Helper()
	f := &fakeTencent{quotes: map[string][]byte{}}
	raw, err := os.ReadFile("testdata/tencent/quotes.gbk")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(raw, []byte(";")) {
		line = bytes.TrimSpace(line)
		if k, _, ok := bytes.Cut(line, []byte("=")); ok {
			f.quotes[strings.TrimPrefix(string(k), "v_")] = append(line, ";\n"...)
		}
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.asked = append(f.asked, r.URL.RequestURI())
		f.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/q="):
			var out []byte
			for _, c := range strings.Split(strings.TrimPrefix(r.URL.Path, "/q="), ",") {
				out = append(out, f.quotes[c]...)
			}
			if len(out) == 0 {
				out = []byte(`v_pv_none_match="1";` + "\n")
			}
			_, _ = w.Write(out)
		case r.URL.Path == "/s3/":
			name := "search-none.txt"
			if r.URL.Query().Get("q") == "tengxun" {
				name = "search-tengxun.txt"
			}
			serveFile(t, w, name)
		case r.URL.Path == "/appstock/app/fqkline/get", r.URL.Path == "/appstock/app/kline/mkline":
			p := strings.Split(r.URL.Query().Get("param"), ",")
			prefix := "kline-"
			if strings.HasSuffix(r.URL.Path, "mkline") {
				prefix = "mkline-"
			}
			name := prefix + p[0] + "-" + p[1] + ".json"
			if _, err := os.Stat(filepath.Join("testdata/tencent", name)); err != nil {
				_, _ = w.Write([]byte(`{"code":-1,"msg":"param error","data":[]}`))
				return
			}
			serveFile(t, w, name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func serveFile(t *testing.T, w http.ResponseWriter, name string) {
	b, err := os.ReadFile(filepath.Join("testdata/tencent", name))
	if err != nil {
		t.Error(err)
	}
	_, _ = w.Write(b)
}

func (f *fakeTencent) hosts() tencentHosts {
	return tencentHosts{quote: f.srv.URL, search: f.srv.URL, kline: f.srv.URL, mkline: f.srv.URL}
}

func (f *fakeTencent) requests(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.asked {
		if strings.HasPrefix(a, prefix) {
			n++
		}
	}
	return n
}

func txSources(t *testing.T) (*fakeTencent, Source, Source, Source) {
	f := newFakeTencent(t)
	ss := tencentSources(f.hosts())
	return f, ss[0], ss[1], ss[2]
}

func TestTencentNormalize(t *testing.T) {
	_, cn, hk, us := txSources(t)
	for _, c := range []struct {
		src  Source
		in   string
		want string // "" when it cannot be one
	}{
		{cn, "600519", "600519.SH"}, {cn, "sh600519", "600519.SH"}, {cn, "600519.sh", "600519.SH"},
		{cn, "000001", "000001.SZ"}, {cn, "000001.SH", "000001.SH"}, {cn, "300750", "300750.SZ"},
		{cn, "920000", "920000.BJ"}, {cn, "430047", "430047.BJ"}, {cn, "510300", "510300.SH"},
		{cn, "SH600519.SZ", ""}, {cn, "60051", ""}, {cn, "AAPL", ""},
		{hk, "700", "00700"}, {hk, "00700", "00700"}, {hk, "hk00700", "00700"}, {hk, "0700.HK", "00700"},
		{hk, "hsi", "HSI"}, {hk, "123456", ""}, {hk, "腾讯", ""},
		{us, "aapl", "AAPL"}, {us, "AAPL.OQ", "AAPL"}, {us, "brk.b.n", "BRK.B"}, {us, "BRK-B", "BRK.B"},
		{us, ".dji", ".DJI"}, {us, "tcehy.ps", "TCEHY"}, {us, "BTC-USDT", ""}, {us, "", ""},
	} {
		got, ok := c.src.Normalize(c.in)
		if c.want == "" && ok || c.want != "" && got != c.want {
			t.Errorf("%s Normalize(%q) = %q %v, want %q", c.src.ID(), c.in, got, ok, c.want)
		}
	}
}

func TestTencentQuotes(t *testing.T) {
	f, cn, hk, us := txSources(t)
	ctx := context.Background()
	qs, err := cn.Quotes(ctx, []string{"600519.SH", "000001.SH", "300750.SZ", "600000.SH"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	m := qs["600519.SH"]
	if m.Name != "贵州茅台" || m.Kind != "股票" || m.Currency != "CNY" || m.Last != 1440.49 || m.Base != 1452 ||
		m.High != 1457.72 || m.Low != 1436 || m.Turnover < 3e9 || m.Turnover > 4e9 || m.Period != "当日" {
		t.Errorf("600519: %+v", m)
	}
	if m.At.Location() != marketInfo[mCN].loc || m.At.Format("2006-01-02 15") != "2026-11-06 11" {
		t.Errorf("600519 at %v", m.At)
	}
	if qs["000001.SH"].Kind != "指数" || qs["000001.SH"].Name != "上证指数" {
		t.Errorf("index: %+v", qs["000001.SH"])
	}
	if _, ok := qs["600000.SH"]; ok || len(qs) != 3 {
		t.Errorf("one the sample lacks is missing: %v", qs)
	}
	if got := f.requests("/q=sh600519,sh000001,sz300750,sh600000"); got != 1 {
		t.Errorf("one request for the batch, got %d: %v", got, f.asked)
	}

	h, err := hk.Quote(ctx, "00700", time.Minute)
	if err != nil || h.Name != "腾讯控股" || h.Currency != "HKD" || h.Kind != "股票" || h.Turnover < 1e9 || h.At.Location() != marketInfo[mHK].loc {
		t.Errorf("00700: %+v %v", h, err)
	}
	if f.requests("/q=r_hk00700") != 1 {
		t.Errorf("Hong Kong in real time (r_): %v", f.asked)
	}
	if x, err := hk.Quote(ctx, "HSI", time.Minute); err != nil || x.Kind != "指数" {
		t.Errorf("HSI: %+v %v", x, err)
	}

	u, err := us.Quotes(ctx, []string{"AAPL", "BRK.B", ".DJI", "ZZZQQQ"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if a := u["AAPL"]; a.Name != "苹果" || a.Currency != "USD" || a.Last != 344.17 || a.Base != 349.51 || a.Kind != "股票" ||
		!a.At.Equal(time.Date(2026, 11, 5, 16, 0, 2, 0, marketInfo[mUS].loc)) {
		t.Errorf("AAPL: %+v", a)
	}
	if u[".DJI"].Kind != "指数" || u["BRK.B"].Name != "伯克希尔B" || len(u) != 3 {
		t.Errorf("us: %+v", u)
	}
	if _, err := us.Quote(ctx, "ZZZQQQ", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if got := us.URL("AAPL"); got != "https://gu.qq.com/usAAPL.OQ" {
		t.Errorf("url %s", got)
	}
	if got := cn.URL("600519.SH"); got != "https://gu.qq.com/sh600519" {
		t.Errorf("url %s", got)
	}
}

func TestTencentSearch(t *testing.T) {
	f, cn, hk, us := txSources(t)
	ctx := context.Background()
	h, err := hk.Search(ctx, "tengxun", 5, time.Minute)
	if err != nil || len(h) != 1 || h[0].Symbol != "00700" || h[0].Name != "腾讯控股" {
		t.Errorf("hk search: %+v %v", h, err) // the others are not in the quote sample
	}
	if _, err := cn.Search(ctx, "tengxun", 5, time.Minute); err != nil {
		t.Error(err)
	}
	if _, err := us.Search(ctx, "tengxun", 5, time.Minute); err != nil {
		t.Error(err)
	}
	if n := f.requests("/s3/"); n != 1 {
		t.Errorf("the three markets share one search answer: %d requests", n)
	}
	// each market asks quotes for its own hits only, warrants (QZ) left out
	for _, want := range []string{"/q=sh000847", "/q=r_hk00700,r_hk80700,r_hk01698", "/q=usTCEHY,usTCTZF,usTME"} {
		if f.requests(want) != 1 {
			t.Errorf("no request %s: %v", want, f.asked)
		}
	}
	if got, err := cn.Search(ctx, "zzzzqqq", 5, time.Minute); err != nil || len(got) != 0 {
		t.Errorf("nothing found: %v %v", got, err)
	}
}

func TestTencentCandles(t *testing.T) {
	_, cn, hk, us := txSources(t)
	ctx := context.Background()
	d, err := cn.Candles(ctx, "600519.SH", "1D", 3, time.Minute)
	if err != nil || len(d) != 3 {
		t.Fatalf("day: %+v %v", d, err)
	}
	// 2026-11-05: open 1446.7, close 1452, high 1466, low 1430, 46712 lots
	if c := d[1]; !c.At.Equal(time.Date(2026, 11, 5, 0, 0, 0, 0, marketInfo[mCN].loc)) || c.Open != 1446.7 || c.Close != 1452 ||
		c.High != 1466 || c.Low != 1430 || c.Vol != 4671200 {
		t.Errorf("day bar: %+v", c)
	}
	h, err := cn.Candles(ctx, "600519.SH", "1H", 3, time.Minute)
	if err != nil || len(h) != 3 {
		t.Fatalf("hour: %+v %v", h, err)
	}
	if !h[0].At.Equal(time.Date(2026, 11, 5, 14, 0, 0, 0, marketInfo[mCN].loc)) || !h[1].At.Equal(time.Date(2026, 11, 6, 9, 30, 0, 0, marketInfo[mCN].loc)) {
		t.Errorf("hourly bars start an hour before Tencent's stamp: %v %v", h[0].At, h[1].At)
	}

	// US bars need the exchange, which a quote tells first
	w, err := us.Candles(ctx, "AAPL", "1W", 3, time.Minute)
	if err != nil || len(w) != 3 {
		t.Fatalf("week: %+v %v", w, err)
	}
	if !w[0].At.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, marketInfo[mUS].loc)) || !w[2].At.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, marketInfo[mUS].loc)) ||
		w[2].Vol != 293234411 {
		t.Errorf("weekly bars start on the Monday: %+v", w)
	}

	if _, err := hk.Candles(ctx, "00700", "15m", 3, time.Minute); err == nil || !strings.Contains(err.Error(), "没有") {
		t.Errorf("Hong Kong has no minute bars: %v", err)
	}
	if _, err := hk.Candles(ctx, "00700", "1D", 3, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Errorf("a code Tencent answers param error for: %v", err)
	}
	if !slices.Equal(cn.Bars(), []string{"15m", "1H", "1D", "1W"}) || !slices.Equal(us.Bars(), []string{"1D", "1W"}) {
		t.Errorf("bars %v %v", cn.Bars(), us.Bars())
	}
}

func TestTencentInPlugin(t *testing.T) {
	ft := newFakeTencent(t)
	fo := newFakeOKX(t)
	p := newPlugin(t.Logf)
	p.srcs = append(sources{newOKX(fo.srv.URL)}, tencentSources(ft.hosts())...)
	if r, ok := p.srcs.parse("cn:600519"); !ok || r.String() != "cn:600519.SH" {
		t.Errorf("parse %v %v", r, ok)
	}
	if got := p.srcs.markets(); got != "加密货币（OKX）、A股、港股、美股" {
		t.Errorf("markets %q", got)
	}
	r := act(t, p, "", "open:cn:600519.SH", nil)
	got := texts(*r.View)
	for _, want := range []string{"当日 成交额=3.", "CNY", "K 线 · 1 小时", "→add:cn:600519.SH", "link 在 腾讯证券 打开"} {
		if !strings.Contains(got, want) {
			t.Errorf("the A-share page has no %q:\n%s", want, got)
		}
	}
	if r.View.Title != "600519.SH 贵州茅台" {
		t.Errorf("title %q", r.View.Title)
	}
	var link string
	for _, s := range r.View.Sections {
		for _, it := range s.Items {
			if it.Type == "link" {
				link = it.URL
			}
		}
	}
	if link != "https://gu.qq.com/sh600519" {
		t.Errorf("link %q", link)
	}
}

func TestLayoutGroups(t *testing.T) {
	ft, fo := newFakeTencent(t), newFakeOKX(t)
	p := newPlugin(t.Logf)
	p.srcs = append(sources{newOKX(fo.srv.URL)}, tencentSources(ft.hosts())...)
	defer func(old func() time.Time) { now = old }(now)
	// 2026-11-06 11:40 in Shanghai: A-shares and Hong Kong are trading, the US closed the day before
	now = func() time.Time { return time.Date(2026, 11, 6, 11, 40, 0, 0, marketInfo[mCN].loc) }
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "watchlist.json"),
		[]byte(`["us:AAPL","okx:BTC-USDT","cn:600519.SH","hk:00700","us:.DJI","cn:000001.SH","okx:ETH-USDT"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var ip pluginkit.InitParams
	ip.Plugin.DataDir = dir
	if err := p.configure(ip); err != nil {
		t.Fatal(err)
	}
	v := render(t, p, "", 5)
	var titles []string
	for _, s := range v.Sections {
		titles = append(titles, s.Title)
	}
	if !slices.Equal(titles[:4], []string{"加密货币 · 2", "A股 · 2", "港股 · 1", "美股 · 2"}) {
		t.Fatalf("groups %q", titles)
	}
	got := texts(v)
	for _, want := range []string{
		"row 贵州茅台 600519.SH · 高 1457.72 · 低 1436.00 1440.49   [-0.79% positive]",
		"row 苹果 AAPL · 高 355.38 · 低 342.06 · 休市 · 截至 11/5 16:00 344.17",
		"## A股 · 2 | 涨跌按上个交易日的收盘价算，时间是交易所当地的",
		"## 美股 · 2 | 涨跌按上个交易日的收盘价算，时间是交易所当地的。点一行看 K 线"} {
		if !strings.Contains(got, want) {
			t.Errorf("the page has no %q:\n%s", want, got)
		}
	}
	if v.Search == nil || v.Search.Placeholder != "搜代码或名称：BTC、茅台、腾讯、AAPL" {
		t.Errorf("search box %+v", v.Search)
	}
	if strings.Contains(got, "腾讯控股 00700 · 高 428.60 · 低 419.60 · 休市") {
		t.Errorf("Hong Kong is trading:\n%s", got)
	}

	// moves stay within the group: 000001.SH up passes over the others to 600519.SH
	act(t, p, v.State, "up:cn:000001.SH", nil)
	if got := refStrings(p.watchlist()); !slices.Equal(got, []string{"us:AAPL", "okx:BTC-USDT", "cn:000001.SH", "hk:00700", "us:.DJI", "cn:600519.SH", "okx:ETH-USDT"}) {
		t.Errorf("moved %v", got)
	}
	r := act(t, p, "", "open:cn:000001.SH", nil)
	if s := texts(*r.View); strings.Contains(s, "→up:") || !strings.Contains(s, "→down:cn:000001.SH") {
		t.Errorf("first of its group: no up, a down:\n%s", s)
	}

	// a source that fails says so in its group; the others draw
	ft.srv.Close()
	got = texts(*act(t, p, "", "refresh", nil).View) // past the cache
	if !strings.Contains(got, "row BTC-USDT") || !strings.Contains(got, "连不上腾讯行情") || !strings.Contains(got, "## 加密货币 · 2") {
		t.Errorf("one source down:\n%s", got)
	}

	// green_up turns the tones round
	ip.Config = []byte(`{"colors": "green_up"}`)
	if err := p.configure(ip); err != nil {
		t.Fatal(err)
	}
	if got := texts(render(t, p, "", 5)); !strings.Contains(got, "[+4.17% positive] →open:okx:BTC-USDT") {
		t.Errorf("green_up:\n%s", got)
	}
}

func TestDecimals(t *testing.T) {
	for _, c := range []struct {
		m    market
		kind string
		last float64
		want string
	}{{mCN, "股票", 1440.5, "1440.50"}, {mCN, "ETF", 4.135, "4.135"}, {mHK, "股票", 423, "423.00"}, {mHK, "股票", 0.315, "0.315"},
		{mUS, "股票", 0.4567, "0.4567"}, {mUS, "指数", 51523.06, "51523.06"}} {
		q := Quote{Decimals: decimals(c.m, c.kind, c.last)}
		if got := q.Price(c.last); got != c.want {
			t.Errorf("%v %s %v: %s, want %s", c.m, c.kind, c.last, got, c.want)
		}
	}
	if got := (Quote{}).Price(3.50210); got != "3.5021" {
		t.Errorf("auto: %s", got)
	}
}
