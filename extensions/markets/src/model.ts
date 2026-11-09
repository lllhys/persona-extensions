import { floatResult } from "./floats.ts";
import { translator } from "./locale.ts";
import { timeParts, utcDate, type Milliseconds } from "./time.ts";
export type SourceID = "okx" | "cn" | "hk" | "us";
export interface Ref { source: SourceID; symbol: string }
export interface Fact { label: string; value: string }
export interface Quote { symbol: string; name: string; kind: string; currency: string; last: number; base: number; high: number; low: number; turnover: number; period: string; at: Milliseconds; decimals: number; idle: boolean; facts: Fact[] }
export interface Candle { at: Milliseconds; open: number; high: number; low: number; close: number; vol: number }
export interface Config { watchlist?: string[]; bar?: string; tz?: string; colors?: string; overview_card?: boolean; triage_rules?: string }
export interface Alert { id: string; ref: string; kind: "above" | "below" | "move"; value: number; created: string; met?: string; price?: number; change?: number; name?: string; decimals?: number }
export const sourceIDs: SourceID[] = ["okx", "cn", "hk", "us"];
export const bars = ["T", "15m", "1H", "4H", "1D", "1W"];
export const sourceBars: Record<SourceID, string[]> = { okx: bars.slice(1), cn: ["T", "15m", "1H", "1D", "1W"], hk: ["T", "1D", "1W"], us: ["T", "1D", "1W"] };
export const zones = { okx: "Asia/Hong_Kong", cn: "Asia/Shanghai", hk: "Asia/Hong_Kong", us: "America/New_York" };
export const benchmarks = ["okx:BTC-USDT", "cn:000001.SH", "hk:HSI", "us:.IXIC"];
export const key = (r: Ref) => `${r.source}:${r.symbol}`;
export const number = (s: unknown) => typeof s === "string" ? floatResult(s).value : 0;
export const emptyQuote = (symbol: string): Quote => ({ symbol, name: "", kind: "", currency: "", last: 0, base: 0, high: 0, low: 0, turnover: 0, period: "", at: 0, decimals: 0, idle: false, facts: [] });
export function normalize(source: SourceID, typed: string): string | undefined {
  let s = typed.trim().toUpperCase();
  if (source === "okx") { s = s.replace(/[/_]/g, "-").replace(/ /g, ""); if (s && !s.includes("-")) s += "-USDT"; return /^[A-Z0-9]{1,20}-[A-Z0-9]{1,20}(-SWAP)?$/.test(s) ? s : undefined; }
  if (source === "cn") { const m = /^(?:(SH|SZ|BJ))?(\d{6})(?:\.(SH|SZ|BJ))?$/.exec(s); if (!m || m[1] && m[3]) return; const ex = m[1] || m[3] || (/^(92|4|8)/.test(m[2]) ? "BJ" : /^[659]/.test(m[2]) ? "SH" : "SZ"); return m[2] + "." + ex; }
  if (source === "hk") { s = s.replace(/\.HK$/, "").replace(/^HK(?=\d)/, ""); return /^\d{1,5}$/.test(s) ? s.padStart(5, "0") : /^[A-Z]{2,10}$/.test(s) ? s : undefined; }
  for (const suffix of [".OQ", ".N", ".AM", ".PS"]) if (s.endsWith(suffix) && s.slice(0, -suffix.length) && s.slice(0, -suffix.length) !== ".") { s = s.slice(0, -suffix.length); break; }
  s = s.replace(/(.+)-([A-Z])$/, "$1.$2");
  return /^\.?[A-Z][A-Z0-9]{0,9}(\.[A-Z])?$/.test(s) ? s : undefined;
}
export function parseRef(typed: string): Ref | undefined { const s = typed.trim(), i = s.indexOf(":"), id = i < 0 ? "okx" : s.slice(0, i).trim().toLowerCase(); if (!sourceIDs.includes(id as SourceID)) return; const symbol = normalize(id as SourceID, i < 0 ? s : s.slice(i + 1)); return symbol ? { source: id as SourceID, symbol } : undefined; }
export function readRefs(list: string[]): string[] { return [...new Set(list.map(parseRef).filter((r): r is Ref => !!r).map(key))].slice(0, 50); }
export const change = (q?: Quote) => q?.last && q.base ? (q.last - q.base) / q.base : undefined;
export const meets = (a: Alert, q: Quote) => q.last !== 0 && (a.kind === "above" ? q.last >= a.value : a.kind === "below" ? q.last <= a.value : change(q) !== undefined && Math.abs(change(q)!) * 100 >= a.value);
export const near = (a: Alert, q: Quote) => !a.met && !!q.last && !meets(a, q) && (a.kind === "move" ? change(q) !== undefined && Math.abs(change(q)!) * 100 >= a.value * .8 : Math.abs(a.value - q.last) <= a.value * .01);
export function clip(s: string, n: number) { const chars = Array.from(s); return chars.length > n ? chars.slice(0, n - 1).join("") + "…" : s; }
export const cleanQuery = (s: string) => clip(s.trim(), 30);
export function fmtFloat(f: number): string { const a = Math.abs(f); if (!a) return "0"; if (a >= 1000) return f.toFixed(2); let s = a >= 1 ? f.toFixed(4) : f.toPrecision(4); if (s.includes("e")) { const [mantissa, exponent] = s.split("e"); const digits = mantissa.replace("-", "").replace(".", ""); const point = 1 + Number(exponent); s = (f < 0 ? "-" : "") + (point <= 0 ? "0." + "0".repeat(-point) + digits : digits.slice(0, point) + "." + digits.slice(point)); } return s.replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, ""); }
export const price = (q: Pick<Quote, "decimals">, f: number) => !f ? "—" : q.decimals > 0 ? f.toFixed(q.decimals) : fmtFloat(f);
export const priceLike = (q: Quote, f: number, like: number) => f.toFixed(q.decimals || (fmtFloat(like).split(".")[1]?.length ?? 0));
export const fmtChange = (f: number) => `${f >= 0 ? "+" : ""}${(f * 100).toFixed(2)}%`;
export function compact(f: number) { for (const [n, s] of [[1e12, "T"], [1e9, "B"], [1e6, "M"], [1e3, "K"]] as const) if (Math.abs(f) >= n) return (f / n).toFixed(2) + s; return f.toFixed(2); }
export function fmtCN(f: number, locale = "zh-Hans") { const t = translator(locale); for (const [n, s] of [[1e12, "unitTrillion"], [1e8, "unitHundredMillion"], [1e4, "unitTenThousand"]] as const) if (f >= n) return (f / n).toFixed(2) + t(s); return f.toFixed(0); }
export const tone = (cfg: Config, c: number): "neutral" | "danger" | "positive" => c === 0 ? "neutral" : (c > 0) !== (cfg.colors === "green_up") ? "danger" : "positive";
export const parts = timeParts;
export function localTime(s: string, zone: string): number {
  const m = /^(\d{4})[-/]?(\d{2})[-/]?(\d{2})(?:[ T]?(\d{2}):?(\d{2})(?::?(\d{2}))?)?$/.exec(s); if (!m || m[0].length !== s.length) return 0;
  const [y, mo, d, h, mi, sec] = m.slice(1).map(x => Number(x || 0)); const wanted = utcDate(y, mo, d, h, mi, sec);
  if (mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59) return 0;
  let at = wanted;
  for (let i = 0; i < 3; i++) { const p = parts(at, zone); const wall = utcDate(+p.year, +p.month, +p.day, +p.hour, +p.minute, +p.second); at += wanted - wall; }
  const p = parts(at, zone); return +p.year === y && +p.month === mo && +p.day === d && +p.hour === h && +p.minute === mi && +p.second === sec ? at : 0;
}
export function stamp(at: Milliseconds, zone: string, style: "full" | "short" | "time" | "day" = "full") { const p = parts(at, zone); return style === "time" ? `${p.hour}:${p.minute}` : style === "day" ? `${+p.month}/${+p.day}` : style === "short" ? `${+p.month}/${+p.day} ${p.hour}:${p.minute}` : `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}`; }
