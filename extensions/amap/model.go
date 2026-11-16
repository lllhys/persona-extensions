package main

import (
	"bytes"
	"encoding/json"
)

// The parts of AMap's answers the tools read. v3 endpoints (geocoding, weather) send an empty array for a missing
// text or object, sometimes nested ("businessAreas": [[]]): str, maybe and list take those as empty.

// maybe is an object that may arrive as [] (or anything else that is not an object) when AMap has none.
type maybe[T any] struct{ V T }

func (m *maybe[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	return json.Unmarshal(b, &m.V)
}

// list is an array of objects; elements that are not objects ([] for none) are left out.
type list[T any] []T

func (l *list[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	*l = nil
	if len(b) == 0 || b[0] != '[' {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, r := range raw {
		r = bytes.TrimSpace(r)
		if len(r) == 0 || r[0] != '{' {
			continue
		}
		var v T
		if err := json.Unmarshal(r, &v); err != nil {
			return err
		}
		*l = append(*l, v)
	}
	return nil
}

// POI search 2.0 (/v5/place/text, /v5/place/around).
type poiAnswer struct {
	Pois list[poi] `json:"pois"`
}

type poi struct {
	ID       str             `json:"id"`
	Name     str             `json:"name"`
	Location str             `json:"location"`
	Type     str             `json:"type"`
	PName    str             `json:"pname"`
	CityName str             `json:"cityname"`
	AdName   str             `json:"adname"`
	Address  str             `json:"address"`
	Distance str             `json:"distance"`
	CityCode str             `json:"citycode"`
	AdCode   str             `json:"adcode"`
	Business maybe[business] `json:"business"`
}

type business struct {
	Tel           str `json:"tel"`
	OpentimeToday str `json:"opentime_today"`
	Rating        str `json:"rating"`
	Cost          str `json:"cost"`
	BusinessArea  str `json:"business_area"`
}

// Geocoding (/v3/geocode/geo).
type geoAnswer struct {
	Geocodes list[geocode] `json:"geocodes"`
}

type geocode struct {
	FormattedAddress str `json:"formatted_address"`
	Province         str `json:"province"`
	City             str `json:"city"`
	CityCode         str `json:"citycode"`
	District         str `json:"district"`
	AdCode           str `json:"adcode"`
	Location         str `json:"location"`
	Level            str `json:"level"`
}

// Reverse geocoding (/v3/geocode/regeo).
type regeoAnswer struct {
	Regeocode maybe[regeocode] `json:"regeocode"`
}

type regeocode struct {
	FormattedAddress str `json:"formatted_address"`
	AddressComponent maybe[struct {
		Province      str                      `json:"province"`
		City          str                      `json:"city"`
		CityCode      str                      `json:"citycode"`
		District      str                      `json:"district"`
		AdCode        str                      `json:"adcode"`
		Township      str                      `json:"township"`
		StreetNumber  maybe[streetNumber]      `json:"streetNumber"`
		BusinessAreas list[struct{ Name str }] `json:"businessAreas"`
	}] `json:"addressComponent"`
	Pois list[nearbyPlace] `json:"pois"`
	Aois list[nearbyPlace] `json:"aois"`
}

type streetNumber struct {
	Street    str `json:"street"`
	Number    str `json:"number"`
	Direction str `json:"direction"`
	Distance  str `json:"distance"`
}

type nearbyPlace struct {
	Name      str `json:"name"`
	Type      str `json:"type"`
	Distance  str `json:"distance"`
	Direction str `json:"direction"`
}

// Route planning 2.0 (/v5/direction/...).
type cost struct {
	Duration      str `json:"duration"`
	Tolls         str `json:"tolls"`
	TrafficLights str `json:"traffic_lights"`
	Taxi          str `json:"taxi"`
	TaxiFee       str `json:"taxi_fee"`
	TransitFee    str `json:"transit_fee"`
}

type pathAnswer struct {
	Route maybe[struct {
		TaxiCost str        `json:"taxi_cost"`
		Paths    list[path] `json:"paths"`
	}] `json:"route"`
}

type path struct {
	Distance    str         `json:"distance"`
	Duration    str         `json:"duration"` // cycling and e-bike give it here, not under cost`
	Restriction str         `json:"restriction"`
	Cost        maybe[cost] `json:"cost"`
	Steps       list[step]  `json:"steps"`
}

type step struct {
	Instruction  str `json:"instruction"`
	RoadName     str `json:"road_name"`
	StepDistance str `json:"step_distance"`
}

type transitAnswer struct {
	Route maybe[struct {
		Cost     maybe[cost]   `json:"cost"`
		Transits list[transit] `json:"transits"`
	}] `json:"route"`
}

type transit struct {
	Cost            maybe[cost]   `json:"cost"`
	Distance        str           `json:"distance"`
	WalkingDistance str           `json:"walking_distance"`
	NightFlag       str           `json:"nightflag"`
	Segments        list[segment] `json:"segments"`
}

type segment struct {
	Walking maybe[struct {
		Distance str `json:"distance"`
	}] `json:"walking"`
	Bus maybe[struct {
		Buslines list[busline] `json:"buslines"`
	}] `json:"bus"`
	Railway maybe[railway] `json:"railway"`
	Taxi    maybe[struct {
		Price    str `json:"price"`
		Distance str `json:"distance"`
	}] `json:"taxi"`
}

type busline struct {
	Name          str                       `json:"name"`
	Type          str                       `json:"type"`
	DepartureStop maybe[struct{ Name str }] `json:"departure_stop"`
	ArrivalStop   maybe[struct{ Name str }] `json:"arrival_stop"`
	ViaNum        str                       `json:"via_num"`
}

type railway struct {
	Name          str `json:"name"`
	Trip          str `json:"trip"`
	DepartureStop maybe[struct {
		Name str `json:"name"`
		Time str `json:"time"`
	}] `json:"departure_stop"`
	ArrivalStop maybe[struct {
		Name str `json:"name"`
		Time str `json:"time"`
	}] `json:"arrival_stop"`
}

// Weather (/v3/weather/weatherInfo).
type weatherAnswer struct {
	Lives     list[live]     `json:"lives"`
	Forecasts list[forecast] `json:"forecasts"`
}

type live struct {
	Province      str `json:"province"`
	City          str `json:"city"`
	Weather       str `json:"weather"`
	Temperature   str `json:"temperature"`
	WindDirection str `json:"winddirection"`
	WindPower     str `json:"windpower"`
	Humidity      str `json:"humidity"`
	ReportTime    str `json:"reporttime"`
}

type forecast struct {
	Province   str        `json:"province"`
	City       str        `json:"city"`
	ReportTime str        `json:"reporttime"`
	Casts      list[cast] `json:"casts"`
}

type cast struct {
	Date         str `json:"date"`
	Week         str `json:"week"`
	DayWeather   str `json:"dayweather"`
	NightWeather str `json:"nightweather"`
	DayTemp      str `json:"daytemp"`
	NightTemp    str `json:"nighttemp"`
	DayWind      str `json:"daywind"`
	NightWind    str `json:"nightwind"`
	DayPower     str `json:"daypower"`
	NightPower   str `json:"nightpower"`
}
