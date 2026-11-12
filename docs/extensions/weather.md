# Weather

[Documentation](../README.md) · [Source](../../extensions/weather/)

Weather in Persona from [Open-Meteo](https://open-meteo.com)'s public APIs, which are **free and need no key**. It shows the current weather, the next 24 hours as a temperature line, a 7-day forecast and an air-quality card, and gives the assistant tools to read each.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `weather`, placement `nav_page`, icon `gauge` | A "天气" entry on the main navigation, and the same page as `page`. |
| `ui` | `page`, with the compatibility placement `plugin_page` | The weather page, kept as `plugin_page` so older hubs that do not know `nav_page` still show it. |
| `connectors` | `weather`, capabilities `status` and `tools` | `weather_now`, `weather_forecast`, `weather_air` and `weather_pollen`, for the assistant. |

## Data and network

The hub's machine reads Open-Meteo. The phone needs no way to the site.

| Endpoint | Answer |
| --- | --- |
| `https://api.open-meteo.com/v1/forecast` | Current values, the next hours and up to 7 days, for a latitude and longitude. |
| `https://air-quality-api.open-meteo.com/v1/air-quality` | Current `pm2_5`, `pm10` and the European AQI; the separate bounded outlook request adds the same hourly fields plus alder, birch, grass, mugwort, olive and ragweed pollen. |
| `https://geocoding-api.open-meteo.com/v1/search` | A city name to its coordinates and timezone. |

Each request has an 8-second timeout and a 2 MiB answer limit, and answers are cached for 10 minutes per city. A refresh asks again, but not within 30 seconds of the last read. The connector's status is `ok` until a read fails; a missing city name does not change it. A failed read is drawn or answered in words, with a way to retry, never as a temperature of zero.

## Configuration

| Setting | Meaning |
| --- | --- |
| `cities` | The cities to show, each with a `name`, `latitude`, `longitude` and optional `timezone`. The first is the default. The default city is 上海 (31.22, 121.46). An unusable city (missing name or coordinates out of range) is skipped and named on the page. |
| `temperature_unit` | `celsius` (the default) or `fahrenheit`. |
| `wind_unit` | `kmh` (the default) or `ms`. |

A tool call may name a city; a name that is not configured is searched through the geocoding endpoint, and the first usable hit is read.

## The page

The page has, from top to bottom:

- **当前天气**: the city's name, a large temperature with its weather icon and description, and beside it the apparent temperature, humidity, wind direction and speed, and today's high and low.
- **未来 24 小时**: a temperature line, one point an hour, in the chosen unit.
- **7 天预报**: one row a day, with its weekday, icon, high and low and precipitation probability.
- **空气质量**: the European AQI with its level, and `pm2_5` and `pm10`, the fine particles with their own level.
- **花粉与空气质量趋势**: the next six hourly air-quality observations, plus species-specific pollen when the upstream provides it. Pollen is available only in Europe during pollen season; missing values mean unavailable, never zero. These are informational concentrations, not medical or dosage advice.

The page attributes Open-Meteo and the CAMS ENSEMBLE data providers. Pollen units are grains/m³.

With more than one city, a control at the top switches between them. A button at the bottom refreshes; the page also redraws every 10 minutes on its own. What went wrong is drawn with a 重试 button.

## The tools

- `weather_now` — the current temperature, apparent temperature, weather, humidity, wind and today's high and low. Use it when the user asks about the weather now, whether it is cold, or what to wear.
- `weather_forecast` — the next `days` (1–7, default 7): each day's high and low, weather and precipitation probability. Use it when the user asks about coming days or whether to bring an umbrella.
- `weather_air` — the AQI, PM2.5 and PM10, with level descriptions. Use it when the user asks about air quality or going outside.
- `weather_pollen` — the next `hours` (1–24, default 12) of available pollen concentrations and air-quality context. Pollen is Europe-only and seasonal; unavailable data is stated explicitly. It does not provide medical or dosage advice.

All four take an optional `city` name; without one they read the default city.

## Development

```sh
go -C extensions/weather test -race -count=1 -timeout=90s ./...
scripts/package.sh weather
```

The parsing, unit conversion, city configuration, WMO weather-code mapping and tool boundaries are covered by tests that use real Open-Meteo responses captured under [`extensions/weather/testdata`](../../extensions/weather/testdata).
