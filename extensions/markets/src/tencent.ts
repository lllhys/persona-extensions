import { HTTP, Cache, ReadError, notFound } from "./http.ts";
import { numericJSON, jsonInteger, JSONNumber } from "./integers.ts";
import { typedJSON, type JSONType } from "./typed-json.ts";
import { parseFloat64 } from "./floats.ts";
const candleData: JSONType = { map: { map: "raw" } };
const minuteData: JSONType = { map: { fields: { data: { fields: { date: "string", data: { array: "string" } } } } } };
function parseTencent(text: string, minutes: boolean) {
  return typedJSON(numericJSON(text), { fields: { code: "integer", msg: "string", data: minutes ? minuteData : candleData } });
}
import { emptyQuote, number, normalize, sourceBars, zones, localTime, parts, price, fmtCN, type SourceID, type Quote, type Candle } from "./model.ts";
const record = (value: unknown): value is Record<string, any> => !!value && typeof value === "object" && !Array.isArray(value) && !(value instanceof JSONNumber);
export type MarketID = Exclude<SourceID, "okx">;
export function kindName(s: string) { return s.includes("ETF") ? "ETF" : s === "ZS" ? "index" : /^(GP-FUND|LOF|JJ)/.test(s) ? "fund" : s.startsWith("GP") ? "stock" : ""; }
export const decimals = (m: MarketID, kind: string, last: number) => m === "cn" && ["ETF", "fund"].includes(kind) || m === "hk" && last < 1 ? 3 : m === "us" && last < 1 ? 4 : 2;
export class Tencent {
  readonly usCode: Cache<string>;
  constructor(readonly http: HTTP) { this.usCode = new Cache(http.clock); }
  market(id: MarketID) { return new TxMarket(this, id); }
  async quoteLines(codes: string[], fresh: number, signal?: AbortSignal) { const raw = await this.http.get("https://qt.gtimg.cn/q=" + codes.join(","), fresh, undefined, signal); const out = new Map<string, string[]>(); for (const line of new TextDecoder("gbk").decode(raw).split(";")) { const i = line.indexOf("="), k = line.slice(0, i).trim(); if (i < 0 || !k.startsWith("v_") || k === "v_pv_none_match") continue; out.set(k.slice(2), line.slice(i + 1).trim().replace(/^"|"$/g, "").split("~")); } return out; }
}
export class TxMarket {
  readonly bars: string[];
  constructor(readonly tx: Tencent, readonly id: MarketID) { this.bars = sourceBars[id]; }
  normalize = (s: string) => normalize(this.id, s);
  code(sym: string, bars = false) { if (this.id === "cn") { const [num, ex] = sym.split("."); return ex.toLowerCase() + num; } if (this.id === "hk") return (bars ? "hk" : "r_hk") + sym; return "us" + (bars ? this.tx.usCode.entries.get(sym)?.value ?? sym : sym); }
  url = (sym: string) => "https://gu.qq.com/" + this.code(sym, true);
  parseQuote(sym: string, fields: string[]): Quote | undefined {
    const at = (i: number) => fields[i]?.trim() ?? "", num = (i: number) => number(at(i)); if (fields.length < 50 || !num(3)) return;
    const q: Quote = { ...emptyQuote(sym), name: at(1), last: num(3), base: num(4), high: num(33), low: num(34), period: "day" };
    let kind = "";
    if (this.id === "cn") { kind = at(61); q.currency = at(82); const p = at(35).split("/"); if (p.length === 3) q.turnover = number(p[2]); }
    else if (this.id === "hk") { kind = at(63); q.currency = at(75); q.turnover = num(37); }
    else { kind = sym.startsWith(".") ? "ZS" : at(56); q.currency = at(35); q.turnover = num(37); if (at(2)) this.tx.usCode.put(sym, at(2)); }
    if (kind === "ZS") q.turnover = this.id === "hk" ? q.turnover * 1e4 : this.id === "us" ? 0 : q.turnover;
    q.currency ||= { cn: "CNY", hk: "HKD", us: "USD" }[this.id]; q.kind = kindName(kind); q.at = localTime(at(30), zones[this.id]); q.idle = !!q.at && this.tx.http.clock() - Number(q.at) > 20 * 60_000; q.decimals = decimals(this.id, q.kind, q.last);
    q.facts = this.facts(q, num); return q;
  }
  facts(q: Quote, num: (i: number) => number) { const facts: Quote["facts"] = [], index = q.kind === "index";
    const add = (label: string, value: number, format: (v: number) => string = v => price(q, v)) => { if (value > 0) facts.push({ label, value: format(value) }); }, pct = (v: number) => v.toFixed(2) + "%";
    add("open", num(5)); add("previous", num(4)); add("volume", this.id === "cn" ? num(6) * 100 : this.id === "hk" && index ? 0 : num(6), fmtCN); add("range", num(43), pct);
    if (!index) { add("turnover_rate", num(this.id === "hk" ? 59 : 38), pct); add("pe", num(39), v => v.toFixed(2)); add("cap", num(45) * 1e8, v => fmtCN(v) + " " + q.currency); if (this.id === "cn") { add("limit_up", num(47)); add("limit_down", num(48)); } }
    add("high52", num(this.id === "cn" ? 67 : 48)); add("low52", num(this.id === "cn" ? 68 : 49)); return facts;
  }
  async quotes(syms: string[], fresh: number, signal?: AbortSignal): Promise<Map<string, Quote>> { const out = new Map<string, Quote>(); if (!syms.length) return out; const codes = syms.map(s => this.code(s)), lines = await this.tx.quoteLines(codes, fresh, signal); syms.forEach((s, i) => { const f = lines.get(codes[i]), q = f && this.parseQuote(s, f); if (q) out.set(s, q); }); return out; }
  async quote(sym: string, fresh: number, signal?: AbortSignal) { const q = (await this.quotes([sym], fresh, signal)).get(sym); if (!q) throw notFound(); return q; }
  listed(market: string, type: string) { return (this.id === "cn" ? ["sh", "sz", "bj"].includes(market) : market === this.id) && !!kindName(type); }
  async search(query: string, n: number, fresh: number, signal?: AbortSignal) { if (!query.trim() || n <= 0) return [];
    const body = await this.tx.http.get("https://smartbox.gtimg.cn/s3/?" + new URLSearchParams({ c: "1", q: query.trim(), t: "all", v: "2" }), fresh, undefined, signal); const raw = new TextDecoder().decode(body).trim(), i = raw.indexOf("="); if (i < 0) throw new ReadError("invalid", "Invalid Tencent search"); let v = raw.slice(i + 1).replace(/;$/, ""); try { v = JSON.parse(v); } catch { v = v.replace(/^"|"$/g, ""); } if (typeof v !== "string") throw new ReadError("invalid", "Invalid Tencent search"); if (!v || v === "N") return [];
    const syms: string[] = []; for (const item of v.split("^")) { const f = item.split("~"); if (f.length < 5 || !this.listed(f[0], f[4])) continue; const sym = this.normalize(f[1] + (this.id === "cn" ? "." + f[0] : "")); if (sym && !syms.includes(sym)) syms.push(sym); if (syms.length === n) break; }
    const quotes = await this.quotes(syms, fresh, signal); return syms.flatMap(s => quotes.has(s) ? [quotes.get(s)!] : []);
  }
  private async envelope(url: string, code: string, fresh: number, signal: AbortSignal | undefined, validate: (data: Record<string, any>) => void) {
    return this.tx.http.json(url, fresh, undefined, signal, env => {
      if (!record(env) || env.code != null && jsonInteger(env.code) !== 0n || env.msg != null && typeof env.msg !== "string") throw new ReadError("invalid", "Tencent provider error");
      if (env.data == null || Array.isArray(env.data) && !env.data.length) throw notFound();
      if (!record(env.data)) throw new ReadError("invalid", "Invalid Tencent data");
      for (const data of Object.values(env.data)) {
        if (data !== null && !record(data)) throw new ReadError("invalid", "Invalid Tencent instrument data");
        validate(data ?? {});
      }
      if (!Object.hasOwn(env.data, code)) throw notFound();
    }, text => parseTencent(text, url.includes("/minute/query?")));
  }
  async candles(sym: string, bar: string, n: number, fresh: number, signal?: AbortSignal): Promise<Candle[]> {
    if (!this.bars.includes(bar)) throw new ReadError("invalid", "Unsupported stock period"); if (this.id === "us" && !this.tx.usCode.entries.has(sym)) await this.quote(sym, 3600_000, signal); const code = this.code(sym, true);
    if (bar === "T") return this.minutes(code, fresh, signal);
    const minute = ["15m", "1H"].includes(bar), rowKey = minute ? bar === "15m" ? "m15" : "m60" : bar === "1D" ? "day" : "week";
    const url = minute ? `https://ifzq.gtimg.cn/appstock/app/kline/mkline?param=${code},${rowKey},,${n}` : `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${code},${rowKey},,,${n},qfq`;
    const rowsFor = (d: Record<string, any>) => Object.hasOwn(d, "qfq" + rowKey) ? d["qfq" + rowKey] : d[rowKey];
    const env = await this.envelope(url, code, fresh, signal, d => {
      const rows = rowsFor(d);
      if (rows != null && (!Array.isArray(rows) || rows.some(r => r !== null && !Array.isArray(r)))) throw new ReadError("invalid", "Invalid Tencent candles");
    });
    const rows = rowsFor(env.data[code] ?? {}) ?? [];
    return rows.flatMap((r: unknown) => { if (!Array.isArray(r) || r.length < 6 || r.slice(0, 6).some(x => typeof x !== "string")) return []; const values = r.slice(1, 6).map(x => parseFloat64(x)); if (values.some(x => x === undefined)) return []; let at = localTime(r[0], zones[this.id]); if (!at) return []; if (minute) at -= bar === "15m" ? 900_000 : 3600_000; else if (bar === "1W") { const p = parts(at, zones[this.id]); const date = new Date(Date.UTC(+p.year, +p.month - 1, +p.day)); date.setUTCDate(date.getUTCDate() - ((date.getUTCDay() + 6) % 7)); at = localTime(date.toISOString().slice(0, 10), zones[this.id]); }
      const [open, close, high, low, vol] = values as number[]; return [{ at, open, close, high, low, vol: vol * (this.id === "cn" ? 100 : 1) }]; }).slice(-n);
  }
  async minutes(code: string, fresh: number, signal?: AbortSignal): Promise<Candle[]> {
    const env = await this.envelope("https://web.ifzq.gtimg.cn/appstock/app/minute/query?" + new URLSearchParams({ code }), code, fresh, signal, d => {
      if (d.data == null) return;
      if (!record(d.data) || d.data.date != null && typeof d.data.date !== "string" || d.data.data != null && (!Array.isArray(d.data.data) || d.data.data.some((line: unknown) => line !== null && typeof line !== "string"))) throw new ReadError("invalid", "Invalid minute response");
    });
    const data = env.data[code]?.data; if (!data || typeof data.date !== "string" || !localTime(data.date, zones[this.id])) return [];
    const minutes: { at: number; price: number; cum: number }[] = []; for (const line of data.data ?? []) { if (typeof line !== "string") continue; const f = line.trim().split(/\s+/); if (f.length < 3 || !/^\d{4}$/.test(f[0])) continue; const price = parseFloat64(f[1]), cum = parseFloat64(f[2]); if (price === undefined || cum === undefined || price === 0) continue; const at = localTime(data.date + f[0], zones[this.id]); if (at) minutes.push({ at, price, cum }); }
    const per = Math.ceil(minutes.length / 120), out: Candle[] = []; let prev = 0;
    for (let i = 0; i < minutes.length; i += per) { const g = minutes.slice(i, i + per), last = g.at(-1)!; out.push({ at: g[0].at, open: g[0].price, close: last.price, high: Math.max(...g.map(m => m.price)), low: Math.min(...g.map(m => m.price)), vol: Math.max(last.cum - prev, 0) * (this.id === "cn" ? 100 : 1) }); prev = last.cum; } return out;
  }
}
