package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Weather (/v3/weather/weatherInfo, the weather allowance): the live report and AMap's forecast of today and the next
// three days, for a Chinese city or district by its adcode. A name is turned into its adcode by geocoding (the LBS
// allowance, cached for a month), so a question costs one weather request, or two with a forecast.

var adcodeRe = regexp.MustCompile(`^\d{6}$`)

var weekdays = map[string]string{"1": "周一", "2": "周二", "3": "周三", "4": "周四", "5": "周五", "6": "周六", "7": "周日"}

// adcode finds a city's adcode: as given, or by geocoding its name.
func (a *app) adcode(ctx context.Context, city string) (string, error) {
	if adcodeRe.MatchString(city) {
		return city, nil
	}
	gs, err := a.geocode(ctx, city, "")
	if err != nil {
		return "", err
	}
	for _, g := range gs {
		if ad := g.AdCode.String(); adcodeRe.MatchString(ad) {
			return ad, nil
		}
	}
	return "", errNotFound{city}
}

func (a *app) weather(ctx context.Context, adcode string, forecastToo bool) (weatherAnswer, error) {
	ext, ttl := "base", liveFor
	if forecastToo {
		ext, ttl = "all", castFor
	}
	body, err := a.client.get(ctx, groupWeather, "/v3/weather/weatherInfo", url.Values{"city": {adcode}, "extensions": {ext}}, ttl)
	if err != nil {
		return weatherAnswer{}, err
	}
	var ans weatherAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return weatherAnswer{}, errBadAnswer
	}
	return ans, nil
}

func (a *app) toolWeather(ctx context.Context, in args) (string, error) {
	if in.City == "" {
		return "", errors.New("要给 city：城市或区县名。用户没说哪儿时，问用户，或用 list_places 看「家」在哪个城市")
	}
	days := 3
	if in.Days != nil {
		days = max(0, min(*in.Days, 4))
	}
	ad, err := a.adcode(ctx, in.City)
	if err != nil {
		return "", err
	}
	now, err := a.weather(ctx, ad, false)
	if err != nil {
		return "", err
	}
	var casts weatherAnswer
	if days > 0 {
		if casts, err = a.weather(ctx, ad, true); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	name := in.City
	if len(now.Lives) > 0 {
		name = placeName(now.Lives[0].Province.String(), now.Lives[0].City.String())
	} else if len(casts.Forecasts) > 0 {
		name = placeName(casts.Forecasts[0].Province.String(), casts.Forecasts[0].City.String())
	}
	b.WriteString(name + " 天气（中国气象局，经高德）：")
	if len(now.Lives) > 0 {
		l := now.Lives[0]
		parts := []string{l.Weather.String()}
		if t := l.Temperature.String(); t != "" {
			parts = append(parts, t+"°C")
		}
		if h := l.Humidity.String(); h != "" {
			parts = append(parts, "湿度 "+h+"%")
		}
		if w := wind(l.WindDirection.String(), l.WindPower.String()); w != "" {
			parts = append(parts, w)
		}
		fmt.Fprintf(&b, "\n实况（%s 发布）：%s", reportTime(l.ReportTime.String()), strings.Join(parts, "，"))
	}
	if days > 0 && len(casts.Forecasts) > 0 {
		cs := casts.Forecasts[0].Casts
		if len(cs) > days {
			cs = cs[:days]
		}
		for _, c := range cs {
			when := c.Date.String()
			if t, err := time.Parse("2006-01-02", when); err == nil {
				when = t.Format("01-02")
			}
			sky := "白天" + c.DayWeather.String() + "，夜间" + c.NightWeather.String()
			if c.DayWeather == c.NightWeather {
				sky = c.DayWeather.String()
			}
			w := wind(c.DayWind.String(), c.DayPower.String())
			if c.NightWind != c.DayWind || c.NightPower != c.DayPower {
				w = "白天" + w + "，夜间" + wind(c.NightWind.String(), c.NightPower.String())
			}
			fmt.Fprintf(&b, "\n- %s %s：%s，%s~%s°C，%s", when, weekdays[c.Week.String()], sky, c.NightTemp, c.DayTemp, w)
		}
	}
	return b.String(), nil
}

// placeName is province and city without repeating the province (北京 东城区 → 北京东城区, 北京 北京市 → 北京市).
func placeName(province, city string) string {
	switch {
	case city == "":
		return province
	case province == "" || strings.HasPrefix(city, province) || strings.HasPrefix(province, strings.TrimSuffix(city, "市")):
		return city
	}
	return province + city
}

// wind writes a direction and AMap's force (in 级) as 东北风 ≤3 级.
func wind(dir, power string) string {
	switch dir {
	case "":
	case "无风向", "旋转不定":
		dir = "风向不定"
	default:
		if !strings.HasSuffix(dir, "风") {
			dir += "风"
		}
	}
	if power != "" {
		power += " 级"
	}
	return strings.TrimSpace(dir + " " + power)
}

// reportTime writes "2026-11-15 18:39:39" as 11-15 18:39.
func reportTime(s string) string {
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.Format("01-02 15:04")
	}
	return s
}
