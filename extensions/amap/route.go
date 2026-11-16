package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Route planning 2.0 (the LBS allowance): driving, walking, cycling, e-bike and public transport, written as distance
// and time, then what matters for the mode (tolls, lights, main roads and the taxi estimate for driving; the lines,
// stops, fare and walking for transport).

// stepsMost is how many turn-by-turn steps a route lists at most.
const stepsMost = 15

// driveStrategies are AMap's driving strategies for the tool's preferences.
var driveStrategies = map[string]string{
	"recommended":      "32",
	"fastest":          "38",
	"avoid_congestion": "33",
	"avoid_tolls":      "1",
	"avoid_highways":   "35",
}

var preferWords = map[string]string{
	"recommended":      "高德推荐",
	"fastest":          "最快",
	"avoid_congestion": "躲避拥堵",
	"avoid_tolls":      "不走收费路",
	"avoid_highways":   "不走高速",
}

var modeWords = map[string]string{"driving": "驾车", "walking": "步行", "cycling": "骑行", "ebike": "电动车", "transit": "公交地铁"}

var modePaths = map[string]string{
	"driving": "/v5/direction/driving",
	"walking": "/v5/direction/walking",
	"cycling": "/v5/direction/bicycling",
	"ebike":   "/v5/direction/electrobike",
	"transit": "/v5/direction/transit/integrated",
}

// end is one end of a route: the point given, or the name looked up preferring city.
func (a *app) end(ctx context.Context, text string, p point, given bool, city, which string) (place, error) {
	if given {
		name := text
		if name == "" {
			name = which
		}
		return place{name: name, at: p}, nil
	}
	pl, err := a.locate(ctx, text, city)
	if err != nil {
		return place{}, err
	}
	pl.name = text
	return pl, nil
}

// label is a place as the answer names it: what the user called it, and where AMap put it when it was looked up.
func (p place) label() string {
	if p.where == "" || strings.Contains(p.name, p.where) {
		return p.name
	}
	return p.name + "（" + p.where + "）"
}

func (a *app) toolRoute(ctx context.Context, in args) (string, error) {
	mode := in.Mode
	if mode == "" {
		mode = "driving"
	}
	if modePaths[mode] == "" {
		return "", fmt.Errorf("不认识出行方式 %q：driving、walking、cycling、ebike 或 transit", mode)
	}
	prefer := in.Prefer
	if prefer == "" {
		prefer = "recommended"
	}
	if driveStrategies[prefer] == "" {
		return "", fmt.Errorf("不认识驾车偏好 %q", prefer)
	}
	var date, clock string
	if mode == "transit" && in.DepartAt != "" {
		t, err := time.ParseInLocation("2006-01-02 15:04", in.DepartAt, beijing)
		if err != nil {
			return "", errors.New("depart_at 要写成「2026-11-16 08:30」这样（北京时间）")
		}
		date, clock = t.Format("2006-01-02"), fmt.Sprintf("%d-%02d", t.Hour(), t.Minute())
	}
	fromAt, fromGiven, err := pointArg(in.FromLatitude, in.FromLongitude, "起点")
	if err != nil {
		return "", err
	}
	toAt, toGiven, err := pointArg(in.ToLatitude, in.ToLongitude, "终点")
	if err != nil {
		return "", err
	}
	for _, e := range []struct {
		given      bool
		text, what string
		param      string
	}{{fromGiven, in.From, "起点", "from"}, {toGiven, in.To, "终点", "to"}} {
		if !e.given && e.text == "" {
			return "", fmt.Errorf("要给%s：坐标（%s_latitude、%s_longitude），或地址、地名（%s）。用户说「从这儿」「我这儿」时先用 phone_location_current，说「家」「公司」时先用 list_places", e.what, e.param, e.param, e.param)
		}
	}
	// A name is looked up in the city given, else in the city of the other end: most routes stay in one city, and
	// names repeat across cities (人民广场, 东方明珠).
	city, knownCity := in.City, ""
	if fromGiven != toGiven {
		known := fromAt
		if toGiven {
			known = toAt
		}
		if c, err := a.cityCode(ctx, known); err == nil {
			knownCity = c
			if city == "" {
				city = c
			}
		}
	}
	from, err := a.end(ctx, in.From, fromAt, fromGiven, city, "起点")
	if err != nil {
		return "", err
	}
	if fromGiven {
		from.citycode = knownCity
	} else if city == "" {
		city = from.citycode
	}
	to, err := a.end(ctx, in.To, toAt, toGiven, city, "终点")
	if err != nil {
		return "", err
	}
	if toGiven {
		to.citycode = knownCity
	}
	if metres(from.at, to.at) < 20 {
		return fmt.Sprintf("%s和%s几乎在同一个地方，不用规划路线。", from.label(), to.label()), nil
	}
	alts := limitOr(in.Alternatives, 1, 3)
	q := url.Values{"origin": {amapLocation(from.at)}, "destination": {amapLocation(to.at)}, "show_fields": {"cost"}}
	ttl := routeFor
	switch mode {
	case "driving":
		q.Set("strategy", driveStrategies[prefer])
		ttl = drivingFor
	case "walking", "cycling", "ebike":
		if alts > 1 {
			q.Set("alternative_route", strconv.Itoa(alts))
		}
	case "transit":
		if from.citycode == "" {
			if from.citycode, err = a.cityCode(ctx, from.at); err != nil {
				return "", err
			}
		}
		if to.citycode == "" {
			if to.citycode, err = a.cityCode(ctx, to.at); err != nil {
				return "", err
			}
		}
		q.Set("city1", from.citycode)
		q.Set("city2", to.citycode)
		q.Set("AlternativeRoute", strconv.Itoa(alts))
		if date != "" {
			q.Set("date", date)
			q.Set("time", clock)
		}
	}
	body, err := a.client.get(ctx, groupLBS, modePaths[mode], q, ttl)
	if err != nil {
		return "", err
	}
	head := fmt.Sprintf("%s %s → %s", modeWords[mode], from.label(), to.label())
	if mode == "driving" {
		head += "（" + preferWords[prefer] + "）"
	}
	if mode == "transit" {
		return transitText(body, head, in.DepartAt, alts)
	}
	return pathText(body, head, mode, alts, in.Steps)
}

// pathText writes a driving, walking, cycling or e-bike answer.
func pathText(body []byte, head, mode string, alts int, withSteps bool) (string, error) {
	var ans pathAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return "", errBadAnswer
	}
	route := ans.Route.V
	if len(route.Paths) == 0 {
		return head + "：高德没算出路线。", nil
	}
	var b strings.Builder
	b.WriteString(head + "：")
	for i, p := range route.Paths {
		if i == alts {
			break
		}
		if alts > 1 && len(route.Paths) > 1 {
			fmt.Fprintf(&b, "\n方案 %d：", i+1)
		} else {
			b.WriteString("\n")
		}
		var parts []string
		if d, ok := p.Distance.num(); ok {
			parts = append(parts, distanceWords(d))
		}
		c := p.Cost.V
		if s, ok := c.Duration.num(); ok {
			parts = append(parts, "约 "+durationWords(s))
		} else if s, ok := p.Duration.num(); ok {
			parts = append(parts, "约 "+durationWords(s))
		}
		if mode == "driving" {
			if n, ok := c.TrafficLights.num(); ok && n > 0 {
				parts = append(parts, fmt.Sprintf("红绿灯 %.0f 个", n))
			}
			if t, ok := c.Tolls.num(); ok && t > 0 {
				parts = append(parts, "过路费约 ¥"+money(t))
			} else if ok {
				parts = append(parts, "无过路费")
			}
			if p.Restriction.String() == "1" {
				parts = append(parts, "有避不开的限行")
			}
		}
		if t, ok := c.Taxi.num(); ok && t > 0 {
			parts = append(parts, "打车约 ¥"+money(t))
		}
		b.WriteString(strings.Join(parts, "，"))
		if roads := mainRoads(p.Steps); mode == "driving" && len(roads) > 0 {
			b.WriteString("；主要走 " + strings.Join(roads, "、"))
		}
		if withSteps {
			for j, s := range p.Steps {
				if j == stepsMost {
					fmt.Fprintf(&b, "\n  （后面还有 %d 段）", len(p.Steps)-stepsMost)
					break
				}
				fmt.Fprintf(&b, "\n  %d. %s", j+1, s.Instruction)
			}
		}
	}
	if t, ok := route.TaxiCost.num(); ok && t > 0 && mode == "driving" {
		b.WriteString("\n打车约 ¥" + money(t) + "（高德估算）")
	}
	return b.String(), nil
}

// mainRoads are the three named roads a route spends longest on, in the order it meets them.
func mainRoads(steps []step) []string {
	length := map[string]float64{}
	var order []string
	for _, s := range steps {
		name := s.RoadName.String()
		if name == "" {
			continue
		}
		if _, ok := length[name]; !ok {
			order = append(order, name)
		}
		d, _ := s.StepDistance.num()
		length[name] += d
	}
	if len(order) <= 3 {
		return order
	}
	top := append([]string(nil), order...)
	for i := range top {
		for j := i + 1; j < len(top); j++ {
			if length[top[j]] > length[top[i]] {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	keep := map[string]bool{top[0]: true, top[1]: true, top[2]: true}
	var out []string
	for _, name := range order {
		if keep[name] {
			out = append(out, name)
		}
	}
	return out
}

// transitText writes a public transport answer: each plan as its time, fare and walking, then its legs.
func transitText(body []byte, head, departAt string, alts int) (string, error) {
	var ans transitAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return "", errBadAnswer
	}
	route := ans.Route.V
	if departAt != "" {
		head += "（" + departAt + " 出发）"
	}
	if len(route.Transits) == 0 {
		return head + "：高德没找到公交地铁方案，可以试试驾车或步行。", nil
	}
	var b strings.Builder
	b.WriteString(head + "：")
	for i, t := range route.Transits {
		if i == alts {
			break
		}
		var parts []string
		if s, ok := t.Cost.V.Duration.num(); ok {
			parts = append(parts, "约 "+durationWords(s))
		}
		if f, ok := t.Cost.V.TransitFee.num(); ok && f > 0 {
			parts = append(parts, "票价 ¥"+money(f))
		}
		if w, ok := t.WalkingDistance.num(); ok && w > 0 {
			parts = append(parts, "步行共 "+distanceWords(w))
		}
		if t.NightFlag.String() == "1" {
			parts = append(parts, "夜班车")
		}
		fmt.Fprintf(&b, "\n方案 %d：%s\n  %s", i+1, strings.Join(parts, "，"), strings.Join(legs(t.Segments), " → "))
	}
	if f, ok := route.Cost.V.TaxiFee.num(); ok && f > 0 {
		b.WriteString("\n打车约 ¥" + money(f) + "（高德估算）")
	}
	return b.String(), nil
}

// lineName writes a line with the way it runs: AMap names it "line(start--end)", so 地铁4号线大兴线(安河桥北--天宫院) is
// 地铁4号线大兴线（往天宫院）; an end may hold parentheses of its own (唯亭(苏州新区火车站)), and a loop keeps only its name.
func lineName(s string) string {
	if !strings.HasSuffix(s, ")") && !strings.HasSuffix(s, "）") {
		return s
	}
	for i, r := range s {
		if r != '(' && r != '（' {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimSuffix(s[i+len(string(r)):], ")"), "）")
		start, end, ok := strings.Cut(inner, "--")
		if !ok {
			continue
		}
		name := strings.TrimSpace(s[:i])
		if start == end || end == "" {
			return name
		}
		return name + "（往" + end + "）"
	}
	return s
}

// legs writes a plan's segments in order: the walk to a stop, then the ride.
func legs(segs []segment) []string {
	var out []string
	for _, s := range segs {
		if d, ok := s.Walking.V.Distance.num(); ok && d > 0 {
			out = append(out, "步行 "+distanceWords(d))
		}
		if lines := s.Bus.V.Buslines; len(lines) > 0 {
			l := lines[0]
			name := lineName(l.Name.String())
			if !strings.HasSuffix(name, "）") {
				name += " "
			}
			leg := fmt.Sprintf("%s%s→%s", name, l.DepartureStop.V.Name, l.ArrivalStop.V.Name)
			if n, ok := l.ViaNum.num(); ok {
				leg += fmt.Sprintf("（%.0f 站）", n+1)
			}
			var also []string
			for _, other := range lines[1:] {
				if len(also) < 2 {
					also = append(also, lineName(other.Name.String()))
				}
			}
			if len(also) > 0 {
				leg += "（也可坐 " + strings.Join(also, "、") + "）"
			}
			out = append(out, leg)
		}
		if r := s.Railway.V; r.Name.String() != "" || r.Trip.String() != "" {
			name := r.Trip.String()
			if name == "" {
				name = r.Name.String()
			}
			leg := fmt.Sprintf("火车 %s %s", name, r.DepartureStop.V.Name)
			if t := clockWords(r.DepartureStop.V.Time.String()); t != "" {
				leg += " " + t
			}
			leg += "→" + r.ArrivalStop.V.Name.String()
			if t := clockWords(r.ArrivalStop.V.Time.String()); t != "" {
				leg += " " + t
			}
			out = append(out, leg)
		}
		if d, ok := s.Taxi.V.Distance.num(); ok && d > 0 {
			leg := "打车 " + distanceWords(d)
			if p, ok := s.Taxi.V.Price.num(); ok && p > 0 {
				leg += "（约 ¥" + money(p) + "）"
			}
			out = append(out, leg)
		}
	}
	return out
}

// clockWords writes AMap's "0800" or "08:00" as 08:00.
func clockWords(s string) string {
	s = strings.ReplaceAll(s, ":", "")
	if len(s) != 4 {
		return ""
	}
	if _, err := strconv.Atoi(s); err != nil {
		return ""
	}
	return s[:2] + ":" + s[2:]
}

// durationWords writes seconds as minutes, or hours and minutes.
func durationWords(sec float64) string {
	m := int(math.Round(sec / 60))
	switch {
	case m < 1:
		return "1 分钟"
	case m < 60:
		return fmt.Sprintf("%d 分钟", m)
	case m%60 == 0:
		return fmt.Sprintf("%d 小时", m/60)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", m/60, m%60)
}

// money writes yuan without trailing zeros.
func money(f float64) string { return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64) }
