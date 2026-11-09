import type { Hub } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import { encodeState, type State } from "../src/state.ts";
export function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason?: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
export const initial: State = { schema_version: 1, watchlist: ["okx:BTC-USDT", "cn:600000.SH", "hk:00123", "us:TEST"], alerts: [] };
export class FakeHub {
  data = encodeState(initial);
  failLoad = false;
  failSave = false;
  saves = 0;
  notifications: Array<{ event: string; params: unknown }> = [];
  invalidations: string[] = [];
  call: Hub["call"] = async <T>(method: string, params: any): Promise<T> => {
    if (params.contribution !== "markets") throw new Error("Fixture rejected foreign contribution");
    if (method === "connector.secrets.load") { if (this.failLoad) throw new Error("Synthetic load outage"); return { data: this.data } as T; }
    if (method === "connector.secrets.save") { this.saves++; if (this.failSave) throw new Error("Synthetic storage outage"); this.data = params.data; return {} as T; }
    throw new Error("Fixture rejected unknown host API");
  };
  notify(event: string, params: unknown) { this.notifications.push({ event, params }); }
  invalidate(id = "") { this.invalidations.push(id); }
}
export function fields(source: "cn" | "hk" | "us", name = "Synthetic instrument", last = 100) {
  const f = Array<string>(90).fill("");
  Object.entries({ 1: name, 2: source === "us" ? "TEST.OQ" : "600000", 3: String(last), 4: "90", 5: "92", 6: "1200", 30: source === "cn" ? "20261106150405" : source === "hk" ? "2026/11/06 15:04:05" : "2026-11-06 15:04:05", 33: "110", 34: "85", 37: "24000", 38: "3.21", 39: "12", 43: "4.3", 45: "1.25", 47: "120", 48: "130", 49: "65", 56: "GP", 59: "6.7", 61: "GP-A", 63: "GP", 67: "150", 68: "70" }).forEach(([i, value]) => { f[Number(i)] = value; });
  f[35] = source === "cn" ? "100/1200/24000" : source === "us" ? "USD" : "";
  f[source === "cn" ? 82 : 75] = source === "cn" ? "CNY" : "HKD";
  return f;
}
export const okxTicker = (id: string, last = 100) => ({ instId: id, last: String(last), open24h: "90", high24h: "110", low24h: "85", volCcy24h: "25000", ts: "1793977200000" });
const json = (value: unknown) => new Response(JSON.stringify(value), { headers: { "content-type": "application/json" } });
export class Fixture {
  at = Date.UTC(2026, 10, 8, 12);
  last = 100;
  hits = new Map<string, number>();
  failures = new Map<string, number>();
  override?: (url: URL, init?: RequestInit) => Response | Promise<Response> | undefined;
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(String(input)), path = url.hostname + url.pathname;
    this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (init?.signal?.aborted) throw init.signal.reason;
    const custom = this.override?.(url, init); if (custom) return custom;
    const failed = this.failures.get(url.hostname); if (failed) return new Response("Synthetic failure", { status: failed });
    if (url.hostname === "www.okx.com") {
      if (url.pathname.endsWith("/ticker")) { const id = url.searchParams.get("instId")!; return json(id.startsWith("MISSING") ? { code: "51001", data: [] } : { code: "0", data: [okxTicker(id, this.last)] }); }
      if (url.pathname.endsWith("/tickers")) return json({ code: "0", data: (url.searchParams.get("instType") === "SWAP" ? ["BTC-USDT-SWAP", "ETH-USDT-SWAP"] : ["BTC-USDT", "ETH-USDT", "SOL-USDT", "PEPE-USDT", "TINY-USDT", "X-USDT"]).map(id => okxTicker(id, this.last)) });
      if (url.pathname.endsWith("/candles")) return json({ code: "0", data: [["1793977200000", "92", "110", "85", "100", "300"], ["1793973600000", "90", "105", "84", "92", "100"]] });
    }
    if (url.hostname === "qt.gtimg.cn") {
      const codes = url.pathname.slice(3).split(",");
      return new Response(codes.map(code => `v_${code}="${fields(code.startsWith("us") ? "us" : code.startsWith("r_hk") ? "hk" : "cn", code.includes("000001") || code.includes("HSI") || code.includes("IXIC") ? "Synthetic benchmark" : "Synthetic instrument", this.last).join("~")}";`).join("\n"));
    }
    if (url.hostname === "smartbox.gtimg.cn") return new Response('v_hint="sh~600000~Synthetic instrument~~GP-A^hk~00123~Synthetic instrument~~GP^us~TEST~Synthetic instrument~~GP^hk~09999~Synthetic warrant~~WR^sh~600000~Synthetic duplicate~~GP-A";');
    if (url.pathname.includes("minute/query")) {
      const code = url.searchParams.get("code")!;
      return json({ code: 0, data: { [code]: { data: { date: "20261106", data: ["0930 100 10", "0931 101 15", "0932 99 14"] } } } });
    }
    if (url.pathname.includes("kline")) {
      const [code, bar] = url.searchParams.get("param")!.split(",");
      const time = bar.startsWith("m") ? "202611061000" : "2026-11-06";
      return json({ code: 0, data: { [code]: { ["qfq" + bar]: [[time, "90", "100", "110", "85", "1200"]] } } });
    }
    throw new Error("Unexpected synthetic market endpoint");
  };
}
export async function until<T>(read: () => T | Promise<T>, ready: (value: T) => boolean, ms = 3000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) { const value = await read(); if (ready(value)) return value; await new Promise(resolve => setTimeout(resolve, 2)); }
  throw new Error("Synthetic page did not settle before timeout");
}
