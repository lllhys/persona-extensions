package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
)

// The connector's tools, for the assistant. Descriptions and answers are in Chinese like the service; every point
// in and out is WGS-84 "纬度,经度".

// schema is a JSON Schema object for a tool's input.
type schema = map[string]any

func prop(typ, desc string) schema { return schema{"type": typ, "description": desc} }

func intProp(desc string, lo, hi int) schema {
	return schema{"type": "integer", "description": desc, "minimum": lo, "maximum": hi}
}

func enumProp(desc string, values ...string) schema {
	return schema{"type": "string", "description": desc, "enum": values}
}

func latProp(what string) schema {
	return schema{"type": "number", "description": what + "纬度（WGS-84，和手机定位、常用地点相同）", "minimum": -90, "maximum": 90}
}

func lngProp(what string) schema {
	return schema{"type": "number", "description": what + "经度（WGS-84）", "minimum": -180, "maximum": 180}
}

func object(required []string, props schema) json.RawMessage {
	s := schema{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	b, _ := json.Marshal(s)
	return b
}

type toolSpec struct {
	name, description string
	input             json.RawMessage
}

var tools = []toolSpec{
	{"amap_search",
		"用高德地图按名字或类别搜中国境内的地点（店铺、景点、车站、小区、公司……）：名称、类别、地址、坐标、电话、今天的营业时间、评分、人均。找「附近的」用 amap_nearby。坐标是 WGS-84「纬度,经度」，可以直接交给 amap_route、amap_nearby 或 phone_maps_*。搜索额度少，不要换着说法反复搜。",
		object([]string{"keywords"}, schema{
			"keywords":  prop("string", "搜什么：地点名、品牌或类别，如「北京南站」「星巴克」「火锅」"),
			"city":      prop("string", "在哪个城市搜，如「上海」；只是优先这个城市，不写就搜全国"),
			"city_only": prop("boolean", "true 时只要 city 里的结果"),
			"limit":     intProp("最多几条，默认 5", 1, 10),
		})},
	{"amap_nearby",
		"用高德地图搜一个点周围的地点，由近到远：名称、类别、距离（米）、地址、坐标、电话、营业时间、评分、人均。用户说「附近」「我这儿」时先用 phone_location_current 拿当前位置再传坐标；说「家附近」「公司附近」时先用 list_places。",
		object(nil, schema{
			"latitude":      latProp("中心点"),
			"longitude":     lngProp("中心点"),
			"place":         prop("string", "没有坐标时，中心点的地址或地名，如「北京市朝阳区三里屯」"),
			"city":          prop("string", "place 在哪个城市，如「上海」；同名地点多时写上"),
			"keywords":      prop("string", "找什么，如「咖啡」「停车场」「药店」；不写就是餐饮、购物和生活服务"),
			"radius_meters": intProp("搜索半径（米），默认 1000", 1, 50000),
			"sort":          enumProp("distance（默认）由近到远；weight 按高德的综合排序", "distance", "weight"),
			"limit":         intProp("最多几条，默认 5", 1, 10),
		})},
	{"amap_geocode",
		"用高德地图把中国境内的地址换成坐标（WGS-84「纬度,经度」），并给出规范地址、所在城市和区、匹配到的精度。找店名、景点名用 amap_search 更准。",
		object([]string{"address"}, schema{
			"address": prop("string", "地址，越完整越准，如「北京市朝阳区阜通东大街6号」"),
			"city":    prop("string", "在哪个城市，如「北京」；不写就查全国"),
		})},
	{"amap_reverse_geocode",
		"用高德地图查一个坐标（WGS-84）在哪儿：地址、街道门牌、所在的小区或商圈、附近的几个地点（距离、方向）。",
		object([]string{"latitude", "longitude"}, schema{
			"latitude":  latProp(""),
			"longitude": lngProp(""),
		})},
	{"amap_route",
		"用高德地图规划中国境内的路线：驾车（默认）、步行、骑行、电动车、公交地铁。给距离（公里）和时长；驾车另给过路费、红绿灯、主要道路和打车约多少钱；公交给每个方案坐哪条线、几站、票价、步行多远。起终点给坐标（WGS-84）最准，也可以给地址或地名。用户说「从这儿」「我现在」时先用 phone_location_current，说「家」「公司」时先用 list_places 拿坐标。只算路线，不打开导航（导航用 phone_maps_navigate）。",
		object(nil, schema{
			"mode":           enumProp("出行方式：driving 驾车（默认），walking 步行，cycling 骑自行车，ebike 电动车，transit 公交地铁", "driving", "walking", "cycling", "ebike", "transit"),
			"from":           prop("string", "起点的地址或地名，如「北京南站」；给了坐标时只当作起点的名字"),
			"from_latitude":  latProp("起点"),
			"from_longitude": lngProp("起点"),
			"to":             prop("string", "终点的地址或地名；给了坐标时只当作终点的名字"),
			"to_latitude":    latProp("终点"),
			"to_longitude":   lngProp("终点"),
			"city":           prop("string", "用名字查起终点时优先在哪个城市找，如「上海」；不写就按另一端所在的城市找"),
			"prefer":         enumProp("驾车偏好：recommended 高德推荐（默认），fastest 最快，avoid_congestion 躲避拥堵，avoid_tolls 不走收费路，avoid_highways 不走高速", "recommended", "fastest", "avoid_congestion", "avoid_tolls", "avoid_highways"),
			"depart_at":      prop("string", "公交的出发时间，北京时间，如「2026-11-16 08:30」；不写就是现在"),
			"alternatives":   intProp("给几个方案，默认 1", 1, 3),
			"steps":          prop("boolean", "true 时附上逐段指引（驾车、步行、骑行、电动车）"),
		})},
	{"amap_weather",
		"用高德地图查中国城市（含区县）的天气，数据来自中国气象局：实况的天气、温度、湿度、风，以及从今天起几天的白天和夜间天气、最高最低温。中国城市优先用这个；境外城市用别的天气工具。",
		object([]string{"city"}, schema{
			"city": prop("string", "城市或区县名，如「杭州」「北京市朝阳区」，或 6 位行政区编码（adcode）"),
			"days": intProp("预报几天（含今天），默认 3；0 只看实况", 0, 4),
		})},
}

func toolList() []wire.ConnectorTool {
	out := make([]wire.ConnectorTool, len(tools))
	for i, t := range tools {
		out[i] = wire.ConnectorTool{Name: t.name, Description: t.description, InputSchema: t.input}
	}
	return out
}

// args are every tool's arguments; each tool reads its own.
type args struct {
	Keywords      string   `json:"keywords"`
	City          string   `json:"city"`
	CityOnly      bool     `json:"city_only"`
	Limit         int      `json:"limit"`
	Latitude      *float64 `json:"latitude"`
	Longitude     *float64 `json:"longitude"`
	Place         string   `json:"place"`
	RadiusMeters  int      `json:"radius_meters"`
	Sort          string   `json:"sort"`
	Address       string   `json:"address"`
	Mode          string   `json:"mode"`
	From          string   `json:"from"`
	FromLatitude  *float64 `json:"from_latitude"`
	FromLongitude *float64 `json:"from_longitude"`
	To            string   `json:"to"`
	ToLatitude    *float64 `json:"to_latitude"`
	ToLongitude   *float64 `json:"to_longitude"`
	Prefer        string   `json:"prefer"`
	DepartAt      string   `json:"depart_at"`
	Alternatives  int      `json:"alternatives"`
	Steps         bool     `json:"steps"`
	Days          *int     `json:"days"`
}

// limitOr is n clamped to 1..most, def when n is not set.
func limitOr(n, def, most int) int {
	if n <= 0 {
		return def
	}
	return min(n, most)
}

// pointArg reads a latitude and longitude pair: ok false when neither is given, an error when only one is or it is
// out of range.
func pointArg(lat, lng *float64, what string) (point, bool, error) {
	if lat == nil && lng == nil {
		return point{}, false, nil
	}
	if lat == nil || lng == nil {
		return point{}, false, fmt.Errorf("%s的纬度和经度要一起给", what)
	}
	p := point{*lat, *lng}
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lng) || math.Abs(p.Lat) > 90 || math.Abs(p.Lng) > 180 {
		return point{}, false, fmt.Errorf("%s的坐标不对：纬度在 -90 到 90 之间，经度在 -180 到 180 之间", what)
	}
	return p, true, nil
}

func (a *app) callTool(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var in args
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("参数不对：%v", err)
		}
	}
	trim := func(s *string) { *s = strings.TrimSpace(*s) }
	for _, s := range []*string{&in.Keywords, &in.City, &in.Place, &in.Sort, &in.Address, &in.Mode, &in.From, &in.To, &in.Prefer, &in.DepartAt} {
		trim(s)
	}
	switch name {
	case "amap_search":
		return a.toolSearch(ctx, in)
	case "amap_nearby":
		return a.toolNearby(ctx, in)
	case "amap_geocode":
		return a.toolGeocode(ctx, in)
	case "amap_reverse_geocode":
		return a.toolReverse(ctx, in)
	case "amap_route":
		return a.toolRoute(ctx, in)
	case "amap_weather":
		return a.toolWeather(ctx, in)
	}
	return "", fmt.Errorf("没有工具 %s", name)
}
