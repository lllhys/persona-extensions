package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Places: keyword search and around search (POI 2.0, the search allowance), geocoding and reverse geocoding (the LBS
// allowance), and how the tools write them.

// How long answers are reused.
const (
	geoFor     = 30 * 24 * time.Hour // addresses and coordinates hardly move
	searchFor  = 15 * time.Minute
	liveFor    = 20 * time.Minute
	castFor    = time.Hour
	drivingFor = 2 * time.Minute // live traffic
	routeFor   = 15 * time.Minute
)

// coarseLevels are geocoding matches too rough to stand for a place someone named (a whole city for 国贸).
var coarseLevels = map[string]bool{"国家": true, "省": true, "市": true, "区县": true, "开发区": true, "乡镇": true, "未知": true, "": true}

// errNotFound says AMap found nothing for a name.
type errNotFound struct{ what string }

func (e errNotFound) Error() string {
	return "高德没找到「" + e.what + "」，换个更完整的说法试试"
}

// geocode looks an address up (LBS allowance).
func (a *app) geocode(ctx context.Context, address, city string) ([]geocode, error) {
	q := url.Values{"address": {address}}
	if city != "" {
		q.Set("city", city)
	}
	body, err := a.client.get(ctx, groupLBS, "/v3/geocode/geo", q, geoFor)
	if err != nil {
		return nil, err
	}
	var ans geoAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, errBadAnswer
	}
	return ans.Geocodes, nil
}

// search is a keyword search (search allowance).
func (a *app) search(ctx context.Context, keywords, city string, cityOnly bool, limit int) ([]poi, error) {
	q := url.Values{"keywords": {keywords}, "page_size": {strconv.Itoa(limit)}, "show_fields": {"business"}}
	if city != "" {
		q.Set("region", city)
		if cityOnly {
			q.Set("city_limit", "true")
		}
	}
	body, err := a.client.get(ctx, groupSearch, "/v5/place/text", q, searchFor)
	if err != nil {
		return nil, err
	}
	var ans poiAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, errBadAnswer
	}
	return ans.Pois, nil
}

// place is somewhere a tool resolved: its name, WGS-84 point and, when known, its city code and where AMap put it
// (city and district, for the answer, so a same-named place in another city shows).
type place struct {
	name     string
	at       point
	citycode string
	where    string
}

// addressLike is text written as an address (a number with a road, lane or house word) rather than a place's name.
func addressLike(text string) bool {
	return strings.ContainsAny(text, "0123456789０１２３４５６７８９") && strings.ContainsAny(text, "路街道巷弄号村镇栋幢室")
}

// locate turns a name or an address into a place, preferring city (a name, citycode or adcode; may be empty).
// An address is geocoded (the larger allowance); a place's name is searched for, because geocoding matches names
// poorly (东方明珠 without a city went to a shop of that name in another province). Each falls back on the other,
// and a lookup's answer is kept for a month.
func (a *app) locate(ctx context.Context, text, city string) (place, error) {
	if addressLike(text) {
		pl, rough, gerr := a.locateByGeocode(ctx, text, city)
		if gerr == nil && !rough {
			return pl, nil
		}
		hint := city
		if hint == "" {
			hint = pl.citycode
		}
		if found, serr := a.locateBySearch(ctx, text, hint); serr == nil {
			return found, nil
		}
		if gerr == nil {
			return pl, nil
		}
		return place{}, gerr
	}
	found, serr := a.locateBySearch(ctx, text, city)
	if serr == nil {
		return found, nil
	}
	pl, _, gerr := a.locateByGeocode(ctx, text, city)
	if gerr == nil {
		return pl, nil
	}
	var nf errNotFound
	if errors.As(serr, &nf) {
		return place{}, gerr
	}
	return place{}, serr
}

// locateByGeocode is the first geocoding match; rough says it is no finer than a district.
func (a *app) locateByGeocode(ctx context.Context, text, city string) (place, bool, error) {
	gs, err := a.geocode(ctx, text, city)
	if err != nil {
		return place{}, false, err
	}
	for _, g := range gs {
		if p, ok := parseAMapLocation(g.Location.String()); ok {
			where := g.City.String()
			if where == "" {
				where = g.Province.String()
			}
			if d := g.District.String(); d != "" && d != where {
				where += d
			}
			return place{name: text, at: p, citycode: g.CityCode.String(), where: where}, coarseLevels[g.Level.String()], nil
		}
	}
	return place{}, false, errNotFound{text}
}

// locateBySearch is the first keyword search match, preferring city.
func (a *app) locateBySearch(ctx context.Context, text, city string) (place, error) {
	q := url.Values{"keywords": {text}, "page_size": {"1"}}
	if city != "" {
		q.Set("region", city)
	}
	body, err := a.client.get(ctx, groupSearch, "/v5/place/text", q, geoFor)
	if err != nil {
		return place{}, err
	}
	var ans poiAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return place{}, errBadAnswer
	}
	for _, p := range ans.Pois {
		if at, ok := parseAMapLocation(p.Location.String()); ok {
			where := p.CityName.String()
			if d := p.AdName.String(); d != "" && d != where {
				where += d
			}
			return place{name: p.Name.String(), at: at, citycode: p.CityCode.String(), where: where}, nil
		}
	}
	return place{}, errNotFound{text}
}

// cityCode is the city code of point p (reverse geocoding, LBS allowance), for public transport.
func (a *app) cityCode(ctx context.Context, p point) (string, error) {
	r, err := a.reverse(ctx, point{math.Round(p.Lat*1e4) / 1e4, math.Round(p.Lng*1e4) / 1e4}, false)
	if err != nil {
		return "", err
	}
	if c := r.AddressComponent.V.CityCode.String(); c != "" {
		return c, nil
	}
	return "", errors.New("高德不知道这个点在哪个城市，公交路线算不了")
}

// reverse is reverse geocoding of p; all asks for the nearby places too.
func (a *app) reverse(ctx context.Context, p point, all bool) (regeocode, error) {
	q := url.Values{"location": {amapLocation(p)}}
	if all {
		q.Set("extensions", "all")
		q.Set("radius", "500")
	}
	body, err := a.client.get(ctx, groupLBS, "/v3/geocode/regeo", q, geoFor)
	if err != nil {
		return regeocode{}, err
	}
	var ans regeoAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return regeocode{}, errBadAnswer
	}
	return ans.Regeocode.V, nil
}

func (a *app) toolSearch(ctx context.Context, in args) (string, error) {
	if in.Keywords == "" {
		return "", errors.New("要给 keywords：搜什么")
	}
	pois, err := a.search(ctx, in.Keywords, in.City, in.CityOnly, limitOr(in.Limit, 5, 10))
	if err != nil {
		return "", err
	}
	where := "全国"
	if in.City != "" {
		where = in.City
	}
	if len(pois) == 0 {
		return fmt.Sprintf("高德在%s没搜到「%s」。", where, in.Keywords), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "高德在%s搜「%s」（坐标为 WGS-84 纬度,经度）：", where, in.Keywords)
	for i, p := range pois {
		b.WriteString("\n" + poiLine(i+1, p, nil))
	}
	return b.String(), nil
}

func (a *app) toolNearby(ctx context.Context, in args) (string, error) {
	center, ok, err := pointArg(in.Latitude, in.Longitude, "中心点")
	if err != nil {
		return "", err
	}
	name := ""
	if !ok {
		if in.Place == "" {
			return "", errors.New("要给中心点：latitude/longitude（用户说「附近」时先用 phone_location_current），或 place（地址或地名）")
		}
		pl, err := a.locate(ctx, in.Place, in.City)
		if err != nil {
			return "", err
		}
		center, name = pl.at, in.Place
		if pl.where != "" {
			name += "（" + pl.where + "）"
		}
	} else {
		name = center.String()
	}
	radius := limitOr(in.RadiusMeters, 1000, 50000)
	limit := limitOr(in.Limit, 5, 10)
	q := url.Values{"location": {amapLocation(center)}, "radius": {strconv.Itoa(radius)},
		"page_size": {strconv.Itoa(limit)}, "show_fields": {"business"}}
	if in.Keywords != "" {
		q.Set("keywords", in.Keywords)
	}
	byWeight := in.Sort == "weight"
	if byWeight {
		q.Set("sortrule", "weight")
	} else {
		q.Set("sortrule", "distance")
	}
	body, err := a.client.get(ctx, groupSearch, "/v5/place/around", q, searchFor)
	if err != nil {
		return "", err
	}
	var ans poiAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return "", errBadAnswer
	}
	pois := ans.Pois
	what := in.Keywords
	if what == "" {
		what = "吃饭、购物和生活服务的地方"
	}
	if len(pois) == 0 {
		return fmt.Sprintf("高德在 %s 周围 %s 内没搜到%s。", name, distanceWords(float64(radius)), quoted(what)), nil
	}
	if !byWeight {
		// AMap ignores the distance order for a keyword-only search: order what it gave.
		sort.SliceStable(pois, func(i, j int) bool { return poiDistance(pois[i], center) < poiDistance(pois[j], center) })
	}
	var b strings.Builder
	fmt.Fprintf(&b, "高德在 %s 周围 %s 内找%s（坐标为 WGS-84 纬度,经度）：", name, distanceWords(float64(radius)), quoted(what))
	for i, p := range pois {
		b.WriteString("\n" + poiLine(i+1, p, &center))
	}
	return b.String(), nil
}

func quoted(s string) string {
	if strings.HasSuffix(s, "地方") {
		return s
	}
	return "「" + s + "」"
}

// poiDistance is AMap's distance of p, or the distance from center when AMap gave none.
func poiDistance(p poi, center point) float64 {
	if d, ok := p.Distance.num(); ok {
		return d
	}
	if at, ok := parseAMapLocation(p.Location.String()); ok {
		return metres(center, at)
	}
	return math.Inf(1)
}

// poiLine writes one place: name (category) · distance from center (when given) · address · point · phone ·
// today's hours · rating · cost.
func poiLine(i int, p poi, center *point) string {
	parts := []string{p.Name.String()}
	if c := category(p.Type.String()); c != "" && !strings.Contains(parts[0], c) {
		parts[0] += "（" + c + "）"
	}
	if center != nil {
		if d := poiDistance(p, *center); !math.IsInf(d, 1) {
			parts = append(parts, distanceWords(d))
		}
	}
	if addr := address(p.CityName.String(), p.AdName.String(), p.Address.String()); addr != "" {
		parts = append(parts, addr)
	}
	if at, ok := parseAMapLocation(p.Location.String()); ok {
		parts = append(parts, at.String())
	}
	biz := p.Business.V
	if tel := phones(biz.Tel.String()); tel != "" {
		parts = append(parts, "电话 "+tel)
	}
	if h := biz.OpentimeToday.String(); h != "" {
		parts = append(parts, "今天 "+h)
	}
	if r, ok := biz.Rating.num(); ok && r > 0 {
		parts = append(parts, "评分 "+biz.Rating.String())
	}
	if c, ok := biz.Cost.num(); ok && c > 0 {
		parts = append(parts, "人均 ¥"+strconv.FormatFloat(c, 'f', -1, 64))
	}
	return fmt.Sprintf("%d. %s", i, strings.Join(parts, " · "))
}

// category is the useful part of AMap's "大类;中类;小类" type: the middle one, or the last when the middle only
// repeats the first (风景名胜;风景名胜;国家级景点).
func category(t string) string {
	t, _, _ = strings.Cut(t, "|")
	parts := strings.Split(t, ";")
	switch {
	case len(parts) >= 3 && parts[1] == parts[0]:
		return parts[2]
	case len(parts) >= 2:
		return parts[1]
	}
	return parts[0]
}

// address joins city, district and street address, without repeating what the address already says.
func address(city, district, addr string) string {
	out := ""
	for _, part := range []string{city, district} {
		if part != "" && !strings.Contains(addr, part) && !strings.HasSuffix(out, part) {
			out += part
		}
	}
	return out + addr
}

// phones keeps at most two of AMap's ";"-separated numbers.
func phones(s string) string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" && len(out) < 2 {
			out = append(out, p)
		}
	}
	return strings.Join(out, "、")
}

// distanceWords writes metres as 米 below a kilometre and 公里 with one decimal above.
func distanceWords(m float64) string {
	if m < 1000 {
		return fmt.Sprintf("%.0f 米", m)
	}
	return strconv.FormatFloat(math.Round(m/100)/10, 'f', -1, 64) + " 公里"
}

func (a *app) toolGeocode(ctx context.Context, in args) (string, error) {
	if in.Address == "" {
		return "", errors.New("要给 address：地址")
	}
	gs, err := a.geocode(ctx, in.Address, in.City)
	if err != nil {
		return "", err
	}
	if len(gs) == 0 {
		return fmt.Sprintf("高德没找到「%s」这个地址；店名、景点名用 amap_search 搜。", in.Address), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "「%s」的坐标（WGS-84 纬度,经度）：", in.Address)
	for i, g := range gs {
		if i == 3 {
			break
		}
		at, ok := parseAMapLocation(g.Location.String())
		if !ok {
			continue
		}
		parts := []string{g.FormattedAddress.String(), at.String()}
		if l := g.Level.String(); l != "" {
			parts = append(parts, "精度："+l)
		}
		if c := strings.TrimSpace(g.City.String() + " " + g.District.String()); c != "" {
			parts = append(parts, c)
		}
		if ad := g.AdCode.String(); ad != "" {
			parts = append(parts, "adcode "+ad)
		}
		fmt.Fprintf(&b, "\n%d. %s", i+1, strings.Join(parts, " · "))
	}
	return b.String(), nil
}

func (a *app) toolReverse(ctx context.Context, in args) (string, error) {
	p, ok, err := pointArg(in.Latitude, in.Longitude, "")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("要给 latitude 和 longitude（WGS-84）")
	}
	r, err := a.reverse(ctx, p, true)
	if err != nil {
		return "", err
	}
	addr := r.FormattedAddress.String()
	if addr == "" {
		return fmt.Sprintf("高德不知道 %s 是哪儿（可能在境外或海上）。", p), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s（WGS-84）在：%s", p, addr)
	ac := r.AddressComponent.V
	if sn := ac.StreetNumber.V; sn.Street.String() != "" {
		fmt.Fprintf(&b, "\n门牌：%s%s", sn.Street, sn.Number)
		if d, ok := sn.Distance.num(); ok {
			fmt.Fprintf(&b, "（%s%s）", sn.Direction, distanceWords(d))
		}
	}
	var in2 []string
	for _, aoi := range r.Aois {
		if d, ok := aoi.Distance.num(); ok && d == 0 && len(in2) < 2 {
			in2 = append(in2, aoi.Name.String())
		}
	}
	if len(in2) > 0 {
		b.WriteString("\n在：" + strings.Join(in2, "、") + " 里")
	}
	var areas []string
	for _, ba := range ac.BusinessAreas {
		if n := ba.Name.String(); n != "" && len(areas) < 3 {
			areas = append(areas, n)
		}
	}
	if len(areas) > 0 {
		b.WriteString("\n商圈：" + strings.Join(areas, "、"))
	}
	var near []string
	for _, np := range r.Pois {
		if len(near) == 3 {
			break
		}
		s := np.Name.String()
		var bits []string
		if c := category(np.Type.String()); c != "" {
			bits = append(bits, c)
		}
		if d, ok := np.Distance.num(); ok {
			bits = append(bits, strings.TrimSpace(np.Direction.String()+" "+distanceWords(d)))
		}
		if len(bits) > 0 {
			s += "（" + strings.Join(bits, "，") + "）"
		}
		near = append(near, s)
	}
	if len(near) > 0 {
		b.WriteString("\n附近：" + strings.Join(near, "；"))
	}
	return b.String(), nil
}
