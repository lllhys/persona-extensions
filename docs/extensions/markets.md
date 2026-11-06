# Markets

[Documentation](../README.md) · [Source](../../extensions/markets/)

Market prices in Persona: a watchlist on the apps' main navigation, a search, an instrument page with its price chart and volume, and assistant tools. Prices come from **sources** behind one interface: OKX's public market API for cryptocurrencies (`okx`, the default) and Tencent's quote endpoints for A-shares (`cn`), Hong Kong (`hk`) and US (`us`) stocks, ETFs and indices. None needs an account. More can be added, see [Adding a source](#adding-a-source).

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `watchlist`, placement `nav_page`, icon `chart` | A "行情" entry on the main navigation. The page lists the watchlist with last price, 24-hour change and range. A search box (UI version 3) finds spot pairs and perpetual swaps by coin or pair, and stocks by code, name or pinyin initials; apps before it search through the add field and a button. Open one for its figures over the period (24 hours, or the trading day), closing prices and volume over the bars its source offers, and buttons to reorder, remove or open it on OKX or 腾讯证券. |
| `connectors` | `markets`, capabilities `status` and `tools` | `markets_quote` (the watchlist or given pairs), `markets_search` (find pairs by coin or pair) and `markets_candles` (OHLCV bars) for the assistant, across every source. |

## Sources and references

Everything outside the sources (the page, the tools, the watchlist) uses only the `Source` interface in [`source.go`](../../extensions/markets/source.go): normalize a typed symbol, search, quotes, one quote, the bars offered, candles, the instrument's website and the hosts reached. Answers come in common shapes: a `Quote` (last, the price the change is against, high, low, turnover, currency, and the period's name such as `24h` or `今日`) and `Candle`s.

An instrument is named by a **ref**, `source:symbol` (`okx:BTC-USDT`). The watchlist file, the page's state, action ids and the tools carry refs. A bare symbol, typed or configured, is the default (first) source's. With more than one source the page labels rows with the source's name, a search asks every source and interleaves their answers (sources rank in their own ways and currencies), and a failing source is skipped while another answers. Each source offers its own subset of the bars `15m`, `1H`, `4H`, `1D`, `1W`; the configured default bar falls back to the source's nearest.

## Data, network and rate limits (OKX)

The hub's machine reads `https://www.okx.com/api/v5/market/…`; the phone needs no access to OKX. Endpoints:

- `/market/tickers?instType=SPOT` (and `SWAP` when the watchlist has a perpetual swap, or for a search): one request for the whole list, cached for 10 seconds.
- `/market/ticker?instId=…`: one instrument, cached for 10 seconds.
- `/market/candles?instId=…&bar=…&limit=…`: the last 60 bars for the page, cached for 30 seconds.

OKX allows about 20 requests per 2 seconds per IP on these endpoints; the page refreshes every 30 seconds while it is on screen. A `429` or OKX error `50011` shows as "rate limited" in the connector status and the page offers a retry.

Instruments are OKX instrument IDs: spot pairs such as `BTC-USDT` or `ETH-USDC`, and perpetual swaps such as `BTC-USDT-SWAP`. A bare coin (`BTC`) means its USDT pair; `/` and `_` are read as `-`. A 24-hour change is against the price 24 hours ago (`open24h`), not a calendar day. OKX's `1D` bars start at 00:00 Hong Kong time.

OKX's search reads the same cached spot and swap ticker lists (no extra requests per keystroke beyond the 10-second cache) and matches case-insensitively: pairs whose base coin is the query first, then ids starting with it, then ids containing it; within each, the highest 24-hour turnover in the quote currency first. It shows at most 30.

## Data, network and limits (Tencent: A-shares, Hong Kong, US)

These are the endpoints Tencent's own stock pages (`gu.qq.com`) read, **not a documented API**: there is no published contract, terms or rate limit, and the formats were read off live answers (samples in [`testdata/tencent`](../../extensions/markets/testdata/tencent/), which the tests run against). Tencent may change them without notice; the sources then fail with "读不懂" until updated. The three markets share one client and cache.

- `qt.gtimg.cn/q=sh600519,r_hk00700,usAAPL`: quotes, every symbol of a market in one request (GBK text, `~`-separated), cached for 10 seconds. Hong Kong is read through `r_` (real time; without it Tencent answers 15 minutes late). US quotes may be delayed; outside US trading hours they are the last close.
- `smartbox.gtimg.cn/s3/`: the search, every market in one answer (shared by the three sources), then one quote request per market for its hits. Warrants, callable bull/bear contracts and off-exchange funds are left out.
- `web.ifzq.gtimg.cn/appstock/app/fqkline/get`: daily and weekly bars, forward-adjusted (`qfq`) where Tencent has them.
- `ifzq.gtimg.cn/appstock/app/kline/mkline`: 15-minute and hourly bars, A-shares only (Tencent answers "param error" for Hong Kong and US codes), so Hong Kong and US offer `1D` and `1W` only.

Symbols, typed after the source (`cn:600519`):

| Source | Symbols | Notes |
| --- | --- | --- |
| `cn` A股 | `600519.SH`, `000001.SZ`, `920000.BJ`; typed also `600519`, `sh600519` | A bare six-digit code is Shanghai when it starts with 6, 5 or 9, Beijing with 4, 8 or 92, else Shenzhen; indices that share a code with a stock need the exchange (`000001.SH` 上证指数). Volumes come in lots of 100 and are given in shares. Currency CNY. |
| `hk` 港股 | `00700`, `HSI`; typed also `700`, `0700.HK` | HKD, or CNY for RMB counters. |
| `us` 美股 | `AAPL`, `BRK.B`, `.DJI`, `.IXIC`, `.INX`; typed also `AAPL.OQ`, `BRK-B` | The exchange suffix Tencent needs for bars is learnt from a quote. Times are New York's. |

The period is the trading day (`当日`): the change is against the previous close, and high, low and turnover are the day's. A minute bar is stamped by Tencent with its end and a weekly bar with the week's last trading day; both are shown from their start (the week's Monday). Times are each market's own (Asia/Shanghai, Asia/Hong_Kong, America/New_York) before the chart's time zone is applied.

## Configuration

[Manifest and settings schema](../../extensions/markets/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `watchlist` | The initial watchlist (default `BTC-USDT`, `ETH-USDT`, `SOL-USDT`); stocks with their source, `cn:600519`, `hk:00700`, `us:AAPL`. Used only until the page first changes it; after that the watchlist lives in the extension's data directory (`watchlist.json`, at most 50 instruments). |
| `bar` | Default bar for the instrument page and `markets_candles`: `15m`, `1H` (default), `4H` or `1D`. |
| `tz` | Time zone for chart labels and tool output; empty uses the app's (the page) or the hub machine's (tools). |

## App view and compatibility

The charts are a line of closing prices and bars of volume; the declarative UI has no candlestick chart, so each point's detail shows close, high and low instead. Apps before UI version 2 get the figures as metrics and the bar choice as rows, without charts.

## Adding a source

1. Write a type implementing `Source` in its own file (`<id>.go`; `tencent.go` is three sources sharing one client), with its own HTTP client, cache and rate limiting; map the provider's errors onto `ErrNotFound` and `ErrRateLimited` (wrapped, so `errors.Is` works). Its `ID` is stored in users' watchlists: choose it once (`us`, `cn`, …) and never change it.
2. List it in `newSources()` in [`plugin.go`](../../extensions/markets/plugin.go). The first one stays the default for bare symbols, so append new sources rather than putting them first.
3. Add its `Hosts()` to the manifest's `permissions.network` and the connector's `hosts`; `TestManifest` fails otherwise. A provider that needs a key gets a manifest secret and reads it in `configure`.
4. Test it against a fake server, as `markets_test.go` does for OKX; `sources_test.go` shows a second, stock-like source mixed with OKX.

## Limits

- The stock sources rely on Tencent's undocumented endpoints (above). A keyed provider (e.g. Alpaca for real-time US quotes and intraday bars) could be added as another source.
- Hong Kong and US have no intraday bars.
- No price alerts or inbox items yet.
