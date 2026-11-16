# AMap (高德地图)

[Documentation](../README.md) · [Source](../../extensions/amap/)

Places, routes and weather in China for the Persona assistant, from the [AMap Web Service API](https://lbs.amap.com/api/webservice/summary) with the user's own key. Design: [#590](https://github.com/nwverse/persona-extensions/issues/590).

Who it serves: everyday assistant users. Nothing new appears in the apps' UI. The user fills in a key once in 设置 › 扩展 › 高德地图, and the assistant gets six read-only tools.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `amap`, capabilities `status` and `tools` | `amap_search`, `amap_nearby`, `amap_geocode`, `amap_reverse_geocode`, `amap_route` and `amap_weather`, all read-only, with no confirmation card. |
| `skills` | `amap` | When to use which tool, getting the user's location or saved places first, and how to treat the quota. |

## The key

| Secret | Meaning |
| --- | --- |
| `api_key` | An AMap key of the **Web服务** (Web Service) platform: at lbs.amap.com, 控制台 › 应用管理 › 我的应用, create an application, then 添加 Key with 服务平台 Web服务. Keys of the JS API or mobile SDK platforms are refused (`10009`). Digital signatures are not supported; an IP allowlist must include the hub machine's public address. The settings show the last four characters. |

The key is sent only to `restapi.amap.com`, in the query string AMap requires. It never appears in configuration, logs, errors, status or tool output: a failed request is worded without its URL, and the key is replaced in any text that could carry it.

**Checked before it is saved** (`settings_check`): one geocoding request with the new key (a public landmark). AMap refusing the key keeps the old settings and says why in words. A used-up allowance or a busy answer still means AMap knows the key, so it is saved. The first key of a new install cannot be checked, because the extension does not run until it has one. The program therefore tries the key once when it starts, and a refused key shows in the connector status straight away.

There is no other configuration.

## The tools

Every coordinate the tools take or give is **WGS-84 `纬度,经度`**, the system of Persona's phone location (`phone_location_current`), Apple Maps tools (`phone_maps_*`) and saved places (`list_places`, `save_place`), so the assistant passes points between them unchanged.

| Tool | Arguments | Answer, one line each |
| --- | --- | --- |
| `amap_search` | `keywords`; `city` (a preference, not a limit); `city_only`; `limit` 1–10, default 5 | Name (category), address, point, phone, today's opening hours, rating, cost per person in ¥. |
| `amap_nearby` | `latitude` + `longitude`, or `place` (looked up first, preferring `city`); `keywords`; `radius_meters` 1–50000, default 1000; `sort` `distance` (default) or `weight`; `limit` | As above with the distance in metres, nearest first. Without keywords AMap's default categories: food, shopping, daily services. |
| `amap_geocode` | `address`; `city` | Up to three matches: formatted address, point, match level (门址, 兴趣点, 区县…), city, district, adcode. |
| `amap_reverse_geocode` | `latitude`, `longitude` | Address, street number with direction and distance, the areas the point is inside, business areas, three nearby places. |
| `amap_route` | `mode` `driving` (default), `walking`, `cycling`, `ebike` or `transit`; each end as `*_latitude` + `*_longitude` or a name or address (`from`, `to`); `city` (where to look the names up); `prefer` (driving: `recommended`, `fastest`, `avoid_congestion`, `avoid_tolls`, `avoid_highways`); `depart_at` (transit, Beijing time `2026-11-16 08:30`); `alternatives` 1–3; `steps` | Distance in km and time in minutes. Driving adds traffic lights, tolls, a restriction that cannot be avoided, the three main roads and AMap's taxi fare estimate. Transit gives each plan's time, fare, total walking and legs: `步行 880 米 → 地铁2号线内环 前门→宣武门（2 站） → …`. `steps` adds up to 15 turn-by-turn instructions. |
| `amap_weather` | `city` (name or adcode); `days` 0–4, default 3 | The live report (weather, °C, humidity, wind direction and force in 级, report time) and each day's day and night weather, low~high °C and wind. Data from the China Meteorological Administration through AMap. |

A place given by name (a route's end, or the centre of a nearby search) is looked up in one of two ways:

- An **address**, meaning text with a number and a road, lane or house word (`阜通东大街6号`), is geocoded, which spends the larger allowance. A match no finer than a district (国家, 省, 市, 区县, 开发区, 乡镇, 未知) is searched for instead.
- A **name** (`东方明珠`, `人民广场`) costs one keyword search. Geocoding matches bare names poorly: without a city, 东方明珠 went to a shop of that name in another province.

Each falls back on the other, and a lookup is cached for a month. Names repeat across cities, so a route looks a name up in the `city` given, else in the other end's city: a point's city comes from reverse geocoding, and for two names the second is looked up in the first one's city. The answer shows each looked-up end with its city and district (`东方明珠（上海市浦东新区）`), so a wrong match is visible. Transit needs each end's city code, which comes from the same lookups. A route between two points less than 20 m apart is not asked for.

When the user says "here" or "near me", the skill has the assistant call `phone_location_current` first. For "home" or "work" it calls `list_places` first. The extension itself never reads the phone or the saved places. It plans routes but does not open navigation; `phone_maps_navigate` does.

### Coordinates

AMap reads and writes GCJ-02 inside China. The program converts every point at AMap's edge: WGS-84 → GCJ-02 with the published formula, and GCJ-02 → WGS-84 by iterating that formula (well under a metre off). This spends no quota; AMap's own conversion endpoint converts only one way. Checked against AMap on 2026-11-15: its points in mainland China, Hong Kong and Taiwan are all GCJ-02. Converted public landmarks land 14–47 m from AMap's point, unconverted ones 500–640 m away. So the shift applies to all three, within a coarse outline that cuts out the neighbouring countries. Outside China points pass through unchanged.

## Quota and pacing

AMap's free allowances are monthly and shared by everything in a group. For a verified individual developer, from 2025-05-20: 150,000 a month for 基础 LBS (geocoding, reverse geocoding, routes), 5,000 for 基础搜索 (keyword and around search) and 5,000 for weather. Unverified accounts get less, and the per-second limit is shown only in the AMap console.

- **Cache** (memory only): geocoding and reverse geocoding 30 days (at most 2000 answers in all); searches 15 minutes; weather 20 minutes live, 1 hour forecast; driving 2 minutes (live traffic); other routes 15 minutes. The same request asked while one is on its way waits for that one.
- **Pacing**: requests start at most three a second. One that would wait more than 3 seconds for its turn is refused as `rate_limited`.
- **Too frequent** (`10004`, `10014`–`10016`, `10019`–`10021`): tried once more after a second, then `rate_limited`.
- **Allowance used up**: `10003` and `10044` (and the overseas `10029`, `10045`) pause that group until midnight Beijing time; `40000` and `40003` until the 1st of next month. While paused, the group's tools answer at once without asking AMap, saying which tools still work. The pause survives a restart (`usage.json`).
- **Counts**: every request that reached AMap is counted per group and month (Beijing time) in `usage.json` in the extension's data directory (0600), with a 32-bit fingerprint of the key so a new key starts from zero. The connector status shows them: `本月已用：搜索 12 次 · 地址和路线 30 次 · 天气 3 次`, and any paused group with when it resumes.

## Status

| State | When |
| --- | --- |
| `ok` | Normally, with this month's counts as the account line and the last successful request. A paused group or a failed request (network, AMap busy, a route AMap cannot plan) leaves it `ok`: the other tools still work, and the hub offers tools only while a connector is `ok`. |
| `error` | AMap refused the key itself (`10001`, `10005`–`10010`, `10013`, `10026`, `10041`, `40002`): what it means, steps to fix it and the console link. Saving a new key restarts the program. A service the key lacks (`10002`, `10012`) is said by that tool only, since it may concern one service. |
| `not_configured` | No key (the hub normally shows the extension as needing setup instead). |

## Privacy

Search words, addresses and coordinates, including the user's current position when the assistant passes it on, go from the hub machine to `restapi.amap.com`. Nothing else is sent, and `permissions.network` lists only that host. The program keeps answers in memory only; on disk it keeps the counts and pauses in `usage.json`, never a query or a point. Logs carry error codes and counts only.

## Development

```sh
go -C extensions/amap test -race -count=1 -timeout=90s ./...
scripts/package.sh amap
```

The tests run against a fake AMap (`fake_test.go`) answering scrubbed recordings of public landmarks (fake phone numbers) under [`extensions/amap/testdata`](../../extensions/amap/testdata). They need no network or key. They cover the coordinate conversion against AMap's own points, the key staying out of every error, the cache, pacing, retry, allowance pauses and counts, each tool's request and answer, the settings check and the protocol.
