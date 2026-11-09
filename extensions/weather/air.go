package main

import (
	"encoding/json"
	"fmt"
)

// The air quality (air-quality-api.open-meteo.com/v1/air-quality). The European AQI runs 0 to >100; the pollutants are
// in µg/m³.

// air is the current air quality the page and the air tool read.
type air struct {
	Timezone string     `json:"timezone"`
	Current  airCurrent `json:"current"`
}

type airCurrent struct {
	Time        string   `json:"time"`
	PM25        *float64 `json:"pm2_5"`
	PM10        *float64 `json:"pm10"`
	EuropeanAQI *float64 `json:"european_aqi"`
}

// parseAir reads an answer.
func parseAir(raw []byte) (air, error) {
	var a air
	if err := json.Unmarshal(raw, &a); err != nil {
		return air{}, fmt.Errorf("%w：%v", errBadAnswer, err)
	}
	return a, nil
}

// aqiLevel names the European AQI bands (0-20 good … >100 extremely poor).
func aqiLevel(aqi float64) string {
	switch {
	case aqi < 20:
		return "优"
	case aqi < 40:
		return "良"
	case aqi < 60:
		return "一般"
	case aqi < 80:
		return "较差"
	case aqi < 100:
		return "很差"
	}
	return "极差"
}

// pm25Level names the PM2.5 concentration bands in µg/m³ (the Chinese standard, familiar to the audience).
func pm25Level(pm float64) string {
	switch {
	case pm < 35:
		return "优"
	case pm < 75:
		return "良"
	case pm < 115:
		return "轻度污染"
	case pm < 150:
		return "中度污染"
	case pm < 250:
		return "重度污染"
	}
	return "严重污染"
}
