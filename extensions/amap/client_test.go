package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestTheKeyGoesOnlyToAMap(t *testing.T) {
	f := newFake(t)
	a, _ := testApp(t, f)
	mustCall(t, a, "amap_geocode", `{"address":"天安门"}`)
	if got := f.last("/v3/geocode/geo").Get("key"); got != testKey {
		t.Fatalf("key %q", got)
	}

	// A failed request names neither the URL nor the key.
	f.srv.Close()
	_, err := call(t, a, "amap_geocode", `{"address":"北京市朝阳区阜通东大街6号"}`)
	if err == nil {
		t.Fatal("no error from a closed server")
	}
	for _, leak := range []string{testKey, "key=", "http://", "/v3/"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the error shows %q: %v", leak, err)
		}
	}
	wantIn(t, err.Error(), "连不上高德")
}

func TestAnswersAreReusedAndAskedOnce(t *testing.T) {
	f := newFake(t)
	a, c := testApp(t, f)
	mustCall(t, a, "amap_search", `{"keywords":"北京南站","city":"北京"}`)
	mustCall(t, a, "amap_search", `{"keywords":"北京南站","city":"北京"}`)
	if n := f.count("/v5/place/text"); n != 1 {
		t.Fatalf("searched %d times", n)
	}
	c.advance(searchFor)
	mustCall(t, a, "amap_search", `{"keywords":"北京南站","city":"北京"}`)
	if n := f.count("/v5/place/text"); n != 2 {
		t.Fatalf("a stale answer was reused (%d requests)", n)
	}

	// The same request asked at once by several callers reaches AMap once.
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = a.weather(context.Background(), "110101", false)
		}()
	}
	wg.Wait()
	if n := f.count("/v3/weather/weatherInfo"); n != 1 {
		t.Fatalf("asked the weather %d times", n)
	}
}

func TestRequestsStartAtMostThreeASecond(t *testing.T) {
	f := newFake(t)
	a, c := testApp(t, f)
	for i := range 4 {
		if _, err := a.geocode(context.Background(), "地址"+string(rune('A'+i)), ""); err != nil {
			t.Fatal(err)
		}
	}
	// The clock never moves on its own, so every wait is the pacing's.
	var waited time.Duration
	for _, d := range c.sleeps {
		waited += d
	}
	if want := 3 * (time.Second / 3); waited != want {
		t.Fatalf("waited %v (%v), want %v", waited, c.sleeps, want)
	}

	// A request whose turn is more than 3 seconds away is refused rather than queued.
	a.client.next = c.now().Add(4 * time.Second)
	_, err := a.geocode(context.Background(), "地址Z", "")
	var e *ext.Error
	if !errors.As(err, &e) || e.Code != wire.ConnectorErrorCodeRateLimited {
		t.Fatalf("err %v", err)
	}
}

func TestATooFrequentAnswerIsTriedOnceMore(t *testing.T) {
	f := newFake(t)
	a, c := testApp(t, f)
	f.queue("/v3/weather/weatherInfo", refusal("10004", "ACCESS_TOO_FREQUENT"))
	text := mustCall(t, a, "amap_weather", `{"city":"110101","days":0}`)
	wantIn(t, text, "霾，7°C")
	if n := f.count("/v3/weather/weatherInfo"); n != 2 {
		t.Fatalf("%d requests", n)
	}
	waitedASecond := false
	for _, d := range c.sleeps {
		waitedASecond = waitedASecond || d == time.Second
	}
	if !waitedASecond {
		t.Fatalf("no wait before the retry: %v", c.sleeps)
	}

	f.queue("/v5/place/text", refusal("10021", "CUQPS_HAS_EXCEEDED_THE_LIMIT"), refusal("10021", "CUQPS_HAS_EXCEEDED_THE_LIMIT"))
	_, err := call(t, a, "amap_search", `{"keywords":"火锅"}`)
	var e *ext.Error
	if !errors.As(err, &e) || e.Code != wire.ConnectorErrorCodeRateLimited || !strings.Contains(e.Message, "请求太频繁") {
		t.Fatalf("err %v", err)
	}
	if n := f.count("/v5/place/text"); n != 2 {
		t.Fatalf("%d searches, want the request and one retry", n)
	}
}

func TestAUsedUpAllowancePausesItsGroup(t *testing.T) {
	f := newFake(t)
	a, c := testApp(t, f)
	f.queue("/v5/place/text", refusal("10003", "DAILY_QUERY_OVER_LIMIT"))
	_, err := call(t, a, "amap_search", `{"keywords":"火锅"}`)
	if err == nil {
		t.Fatal("no error")
	}
	wantIn(t, err.Error(), "「搜索」额度用完了", "DAILY_QUERY_OVER_LIMIT", "北京时间明天 0 点恢复", "地址查询和路线规划、天气还能用")

	// The group is not asked again today: the answer is at once, from memory.
	for _, tool := range []string{`{"keywords":"咖啡"}`, `{"keywords":"火锅","city":"北京"}`} {
		if _, err := call(t, a, "amap_search", tool); err == nil || !strings.Contains(err.Error(), "额度用完了") {
			t.Fatalf("err %v", err)
		}
	}
	if _, err := call(t, a, "amap_nearby", `{"latitude":39.9055,"longitude":116.3976}`); err == nil {
		t.Fatal("nearby search ran while search is paused")
	}
	if n := f.count("/v5/place/text") + f.count("/v5/place/around"); n != 1 {
		t.Fatalf("%d searches reached AMap", n)
	}
	// Other groups still work.
	mustCall(t, a, "amap_geocode", `{"address":"天安门"}`)
	st := a.status()
	if st.State != wire.ConnectorStateOk || !strings.Contains(st.Account, "搜索额度已用完，11-16 00:00 恢复") {
		t.Fatalf("status %+v", st)
	}

	// A restart remembers the pause.
	b := newClient(testKey, filepath.Dir(a.client.file), t.Logf)
	tuneFor(f, c)(b)
	b.load()
	if _, err := b.get(context.Background(), groupSearch, "/v5/place/text", url.Values{"keywords": {"x"}}, 0); err == nil {
		t.Fatal("a new client forgot the pause")
	}

	// After midnight in Beijing it asks again.
	c.advance(14 * time.Hour)
	mustCall(t, a, "amap_search", `{"keywords":"火锅"}`)
	if n := f.count("/v5/place/text"); n != 2 {
		t.Fatalf("%d searches", n)
	}
}

func TestAUsedUpPlanPausesUntilNextMonth(t *testing.T) {
	f := newFake(t)
	a, c := testApp(t, f)
	f.queue("/v3/weather/weatherInfo", refusal("40000", "QUOTA_PLAN_RUN_OUT"))
	_, err := call(t, a, "amap_weather", `{"city":"110101"}`)
	if err == nil {
		t.Fatal("no error")
	}
	wantIn(t, err.Error(), "「天气」额度用完了", "下个月 1 日恢复")
	c.advance(10 * 24 * time.Hour)
	if _, err := call(t, a, "amap_weather", `{"city":"110101"}`); err == nil {
		t.Fatal("asked the weather while its plan is used up")
	}
	c.advance(10 * 24 * time.Hour) // December
	mustCall(t, a, "amap_weather", `{"city":"110101"}`)
}

func TestARefusedKeyShowsInTheStatus(t *testing.T) {
	f := newFake(t)
	a, _ := testApp(t, f)
	if st := a.status(); st.State != wire.ConnectorStateOk || !strings.Contains(st.Account, "本月已用：搜索 0 次") {
		t.Fatalf("status %+v", st)
	}
	f.queue("/v3/geocode/geo", refusal("10009", "USERKEY_PLAT_NOMATCH"))
	_, err := call(t, a, "amap_geocode", `{"address":"天安门"}`)
	if err == nil {
		t.Fatal("no error")
	}
	wantIn(t, err.Error(), "不是「Web服务」类型", "设置 › 扩展 › 高德地图")
	st := a.status()
	if st.State != wire.ConnectorStateError || !strings.Contains(st.Error, "Web服务") || st.GuideURL != consoleURL || len(st.Guide) == 0 {
		t.Fatalf("status %+v", st)
	}
	if err := wire.ValidateJSON("ConnectorStatus", mustJSON(t, st)); err != nil {
		t.Fatal(err)
	}
}

func TestRefusalsAboutOneRequestLeaveTheConnectorAlone(t *testing.T) {
	f := newFake(t)
	a, _ := testApp(t, f)
	f.queue("/v5/direction/walking", refusal("20803", "OVER_DIRECTION_RANGE"))
	_, err := call(t, a, "amap_route", `{"mode":"walking","from_latitude":39.9,"from_longitude":116.39,"to_latitude":31.23,"to_longitude":121.47}`)
	if err == nil {
		t.Fatal("no error")
	}
	wantIn(t, err.Error(), "离得太远")
	if st := a.status(); st.State != wire.ConnectorStateOk {
		t.Fatalf("status %+v", st)
	}
}

func TestUsageIsCountedByGroupAndMonth(t *testing.T) {
	f := newFake(t)
	a, c := testApp(t, f)
	mustCall(t, a, "amap_search", `{"keywords":"北京南站"}`)
	mustCall(t, a, "amap_weather", `{"city":"110101"}`) // live and forecast
	mustCall(t, a, "amap_geocode", `{"address":"天安门"}`)
	_, _, u := a.client.state()
	if u.Counts[groupSearch] != 1 || u.Counts[groupWeather] != 2 || u.Counts[groupLBS] != 1 {
		t.Fatalf("counts %v", u.Counts)
	}
	if got := usageLine(u); got != "本月已用：搜索 1 次 · 地址和路线 1 次 · 天气 2 次" {
		t.Fatalf("usage line %q", got)
	}
	raw, err := os.ReadFile(a.client.file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testKey) {
		t.Fatal("usage.json holds the key")
	}
	if info, _ := os.Stat(a.client.file); info.Mode().Perm() != 0o600 {
		t.Fatalf("usage.json mode %v", info.Mode())
	}

	dir := filepath.Dir(a.client.file)
	same := newClient(testKey, dir, t.Logf)
	tuneFor(f, c)(same)
	same.load()
	if _, _, u := same.state(); u.Counts[groupWeather] != 2 {
		t.Fatalf("a restart lost the counts: %v", u.Counts)
	}
	other := newClient(strings.Repeat("f", 32), dir, t.Logf)
	if _, _, u := other.state(); u.Counts[groupWeather] != 0 {
		t.Fatalf("another key kept the counts: %v", u.Counts)
	}
	c.advance(20 * 24 * time.Hour) // December in Beijing
	if _, _, u := a.client.state(); u.Counts[groupWeather] != 0 || u.Month != "2026-12" {
		t.Fatalf("a new month kept the counts: %+v", u)
	}
}

func TestLooseFieldsRead(t *testing.T) {
	var g geocode
	if err := json.Unmarshal([]byte(`{"city":[],"district":["东城区"],"street":{},"adcode":110101,"location":"116.4,39.9"}`), &g); err != nil {
		t.Fatal(err)
	}
	if g.City != "" || g.District != "东城区" || g.AdCode != "110101" {
		t.Fatalf("%+v", g)
	}
	var r regeocode
	if err := json.Unmarshal([]byte(`{"formatted_address":"x","addressComponent":{"streetNumber":[],"businessAreas":[[]],"city":[]},"pois":[],"aois":[[]]}`), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.AddressComponent.V.BusinessAreas) != 0 || r.AddressComponent.V.StreetNumber.V.Street != "" || len(r.Aois) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestEachRefusalIsWorded(t *testing.T) {
	for code, want := range map[string]struct {
		kind  errKind
		words string
	}{
		"10001": {kindKey, "不对或已经过期"},
		"10007": {kindKey, "数字签名"},
		"10005": {kindKey, "IP 白名单"},
		"10003": {kindDaily, "额度用完了"},
		"10044": {kindDaily, "额度用完了"},
		"40000": {kindPlan, "额度用完了"},
		"10004": {kindBusy, "请求太频繁"},
		"10020": {kindBusy, "请求太频繁"},
		"20801": {kindOther, "找不到路"},
		"10002": {kindOther, "没有开通这项服务"},
		"10012": {kindOther, "没有这项服务的权限"},
		"20000": {kindOther, "参数不对（INVALID_PARAMS）"},
		"30001": {kindOther, "高德服务出错了"},
		"99999": {kindOther, "高德没查成"},
	} {
		e := checkAnswer([]byte(refusal(code, map[string]string{"20000": "INVALID_PARAMS"}[code])))
		if e == nil || e.kind() != want.kind || !strings.Contains(e.msg, want.words) {
			t.Errorf("%s: %+v", code, e)
		}
	}
	if e := checkAnswer([]byte(`{"status":"1","info":"OK","infocode":"10000"}`)); e != nil {
		t.Fatalf("success read as %v", e)
	}
	if e := checkAnswer([]byte(`<html>`)); e == nil {
		t.Fatal("HTML read as success")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
