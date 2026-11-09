import { HTTP, Cache, ReadError, notFound } from "./http.ts";
import { int64, numericJSON, jsonObject } from "./integers.ts";
import { typedJSON, type JSONType } from "./typed-json.ts";
import { parseFloat64 } from "./floats.ts";
import { milliseconds } from "./time.ts";
import { emptyQuote, number, sourceBars, normalize, type Quote, type Candle } from "./model.ts";
const tickerFields = ["instId", "instType", "last", "open24h", "high24h", "low24h", "vol24h", "volCcy24h", "ts"];
const tickerType: JSONType = { fields: Object.fromEntries(tickerFields.map(field => [field, "string" as const])) };
function validTicker(t: unknown) { if (!jsonObject(t)) throw new ReadError("invalid", "Invalid OKX ticker"); typedJSON(t, tickerType); }
function parseOKX(text: string, candles: boolean) {
  const raw = numericJSON(text);
  if (!jsonObject(raw)) throw new ReadError("invalid", "Invalid OKX response");
  const env = typedJSON(raw, { fields: { code: "string", msg: "string", data: "raw" } });
  // Data is json.RawMessage in the Go envelope: only its final occurrence is
  // decoded as records. Code/msg, and each selected record, retain earlier errors.
  if (env.code === "0") {
    if (!Array.isArray(env.data)) throw new ReadError("invalid", "Invalid OKX data");
    if (!candles && env.data.some((row: unknown) => !jsonObject(row))) throw new ReadError("invalid", "Invalid OKX ticker");
    env.data = typedJSON(env.data, { array: candles ? { array: "string" } : tickerType });
  }
  return env;
}
export const instType = (id: string) => id.endsWith("-SWAP") ? "SWAP" : "SPOT";
export function ticker(t: Record<string, unknown>): Quote { validTicker(t); const id = String(t.instId ?? ""); const q: Quote = { ...emptyQuote(id), kind: instType(id) === "SWAP" ? "swap" : "spot", currency: id.split("-")[1] ?? "", last: number(t.last), base: number(t.open24h), high: number(t.high24h), low: number(t.low24h), turnover: number(t.volCcy24h), period: "24h", at: milliseconds(typeof t.ts === "string" ? int64(t.ts) : undefined) }; if (instType(id) === "SWAP") q.turnover *= q.last; return q; }
export class OKX {
  readonly id = "okx" as const;
  readonly bars = sourceBars.okx;
  readonly missing: Cache<boolean>;
  constructor(readonly http: HTTP) { this.missing = new Cache(http.clock); }
  normalize = (s: string) => normalize(this.id, s);
  url = (s: string) => `https://www.okx.com/trade-${instType(s) === "SWAP" ? "swap" : "spot"}/${s.toLowerCase()}`;
  async get(path: string, fresh: number, signal?: AbortSignal): Promise<any[]> {
    const url = "https://www.okx.com/api/v5/market/" + path;
    const env = await this.http.json(url, fresh, 8 * 1024 * 1024, signal, env => {
      if (env?.code !== "0") {
        if (env?.code === "51001") throw notFound();
        if (env?.code === "50011") throw new ReadError("rate_limited", "OKX rate limit");
        throw new ReadError("invalid", "Invalid OKX response");
      }
      if (!Array.isArray(env.data)) throw new ReadError("invalid", "Invalid OKX data");
      for (const row of env.data) {
        if (!path.startsWith("candles?")) validTicker(row);
        else if (row !== null && (!Array.isArray(row) || row.some(x => x !== null && typeof x !== "string"))) throw new ReadError("invalid", "Invalid OKX candle row");
      }
    }, text => parseOKX(text, path.startsWith("candles?")));
    return env.data;
  }
  async quote(id: string, fresh: number, signal?: AbortSignal) { if (this.missing.get(id, fresh)) throw notFound(); try { const list = await this.get("ticker?" + new URLSearchParams({ instId: id }), fresh, signal); if (!list.length) throw notFound(); return ticker(list[0]); } catch (err) { if (err instanceof ReadError && err.code === "not_found") this.missing.put(id, true); throw err; } }
  async quotes(ids: string[], fresh: number, signal?: AbortSignal): Promise<Map<string, Quote>> {
    const out = new Map<string, Quote>();
    if (ids.length <= 5) {
      const answers = await Promise.allSettled(ids.map(async id => [id, await this.quote(id, fresh, signal)] as const));
      let first: unknown;
      for (const answer of answers) {
        if (answer.status === "fulfilled") out.set(...answer.value);
        else if (!(answer.reason instanceof ReadError && answer.reason.code === "not_found")) first ??= answer.reason;
      }
      if (first) throw first;
    }
    else { const want = new Set(ids); for (const type of ["SPOT", "SWAP"]) if (ids.some(id => instType(id) === type)) for (const t of await this.get("tickers?instType=" + type, fresh, signal)) if (want.has(t.instId)) out.set(t.instId, ticker(t)); }
    return out;
  }
  async search(query: string, n: number, fresh: number, signal?: AbortSignal): Promise<Quote[]> {
    const q = query.trim().toUpperCase().replace(/[/_]/g, "-").replace(/ /g, ""); if (!q || n <= 0) return [];
    const hits: { quote: Quote; rank: number }[] = [];
    for (const type of ["SPOT", "SWAP"]) for (const t of await this.get("tickers?instType=" + type, fresh, signal)) { const id: string = t.instId; if (typeof id !== "string" || type === "SWAP" && !id.endsWith("-SWAP")) continue; const rank = id === q || id.split("-")[0] === q ? 0 : id.startsWith(q) ? 1 : id.includes(q) ? 2 : -1; if (rank >= 0) hits.push({ quote: ticker(t), rank }); }
    return hits.sort((a, b) => a.rank - b.rank || b.quote.turnover - a.quote.turnover).slice(0, n).map(h => h.quote);
  }
  async candles(id: string, bar: string, n: number, fresh: number, signal?: AbortSignal): Promise<Candle[]> {
    if (!this.bars.includes(bar)) throw new ReadError("invalid", "Unsupported OKX period");
    const rows = await this.get("candles?" + new URLSearchParams({ bar, instId: id, limit: String(n) }), fresh, signal);
    return rows.slice().reverse().flatMap(r => {
      if (!Array.isArray(r) || r.length < 6 || typeof r[0] !== "string") return [];
      const exact = int64(r[0]), values = r.slice(1, 6).map(x => typeof x === "string" ? parseFloat64(x) : undefined);
      if (exact === undefined || values.some(x => x === undefined)) return [];
      const [open, high, low, close, vol] = values as number[];
      return [{ at: milliseconds(exact), open, high, low, close, vol }];
    });
  }
}
