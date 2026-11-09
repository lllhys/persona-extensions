import type { Hub } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import { HTTP, ReadError, isCancelled, trustedFetch } from "./http.ts";
import { OKX } from "./okx.ts";
import { Tencent, type TxMarket } from "./tencent.ts";
import { translator } from "./locale.ts";
import { MutationError } from "./errors.ts";
import { parseFloat64 } from "./floats.ts";
import { StateStore } from "./state.ts";
import { parseRef, key, sourceIDs, sourceBars, meets, change, price, fmtChange, stamp, type Ref, type Quote, type Config, type Alert, type SourceID } from "./model.ts";
export interface ServiceOptions { fetch?: FetchAdapter; clock?: () => number; id?: () => string; interval?: (fn: () => void, ms: number) => () => void }
export class Markets {
  readonly http: HTTP;
  readonly sources: Record<SourceID, OKX | TxMarket>;
  readonly store: StateStore;
  readonly cfg: Config;
  private lastOK = 0;
  private lastErr?: unknown;
  private lastErrAt = 0;
  private stopLoop?: () => void;
  private loopController?: AbortController;
  private alertCheck?: Promise<void>;
  private listeners = new Set<() => void>();
  private closed = false;
  private id: () => string;
  private interval: (fn: () => void, ms: number) => () => void;
  readonly clock: () => number;
  constructor(readonly hub: Pick<Hub, "call" | "notify" | "invalidate">, config: Config = {}, options: ServiceOptions = {}) {
    if (!config || typeof config !== "object" || Array.isArray(config) || config.watchlist !== undefined && (!Array.isArray(config.watchlist) || config.watchlist.some(x => typeof x !== "string")) || [config.bar, config.colors, config.tz, config.triage_rules].some(x => x !== undefined && typeof x !== "string") || config.overview_card !== undefined && typeof config.overview_card !== "boolean") throw new Error("Invalid markets configuration");
    this.cfg = { ...config, watchlist: config.watchlist?.slice() }; if (config.tz && config.tz !== "Local") new Intl.DateTimeFormat("en", { timeZone: config.tz });
    this.clock = options.clock ?? Date.now; this.http = new HTTP(options.fetch ?? trustedFetch, this.clock); const tx = new Tencent(this.http); this.sources = { okx: new OKX(this.http), cn: tx.market("cn"), hk: tx.market("hk"), us: tx.market("us") }; this.store = new StateStore(hub);
    this.id = options.id ?? (() => Array.from(crypto.getRandomValues(new Uint8Array(6)), n => n.toString(16).padStart(2, "0")).join(""));
    this.interval = options.interval ?? ((fn, ms) => { const timer = setInterval(fn, ms); return () => clearInterval(timer); });
  }
  async initialize() { await this.store.load(this.cfg.watchlist?.length ? this.cfg.watchlist : ["BTC-USDT", "ETH-USDT", "SOL-USDT"]); if (!this.closed && !this.stopLoop) { this.loopController = new AbortController(); this.stopLoop = this.interval(() => { void this.checkAlerts(30_000, this.loopController?.signal).catch(err => this.note(err)); }, 60_000); } }
  dispose() { this.closed = true; this.stopLoop?.(); this.stopLoop = undefined; this.loopController?.abort(); this.listeners.clear(); }
  subscribe(fn: () => void) { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; }
  changed() { if (this.closed) return; this.hub.invalidate("watchlist"); this.hub.invalidate("card"); for (const fn of this.listeners) fn(); }
  watchlist() { return this.store.snapshot().watchlist; }
  alerts() { return this.store.snapshot().alerts; }
  defaultBar(id: SourceID) { return [this.cfg.bar, "1H", "1D", sourceBars[id][0]].find(b => b && sourceBars[id].includes(b))!; }
  note(err?: unknown) { if (!err) { this.lastOK = this.clock(); this.lastErr = undefined; } else if (!isCancelled(err) && !(err instanceof ReadError && err.code === "not_found")) { this.lastErr = err; this.lastErrAt = this.clock(); } }
  status(locale = "zh-Hans") { const t = translator(locale), failed = !!this.lastErr && this.lastErrAt >= this.lastOK; return { state: failed ? this.lastErr instanceof ReadError && this.lastErr.code === "rate_limited" ? "rate_limited" : "error" : "ok", account: t("publicAccount"), ...(this.lastOK ? { last_sync: new Date(this.lastOK).toISOString() } : {}), ...(failed ? { error: t(this.lastErr instanceof ReadError && this.lastErr.code === "rate_limited" ? "rateError" : "error") } : {}) }; }
  async quote(ref: Ref, fresh = 10_000, signal?: AbortSignal) { try { const q = await this.sources[ref.source].quote(ref.symbol, fresh, signal); this.note(); return q; } catch (err) { this.note(err); throw err; } }
  async quotes(refs: string[], fresh = 10_000, partial = false, signal?: AbortSignal) {
    const quotes = new Map<string, Quote>(), errors = new Map<SourceID, unknown>();
    for (const id of sourceIDs) { const group = refs.map(parseRef).filter((r): r is Ref => r?.source === id); if (!group.length) continue; try { const got = await this.sources[id].quotes([...new Set(group.map(r => r.symbol))], fresh, signal); this.note(); for (const [symbol, q] of got) quotes.set(key({ source: id, symbol }), q); } catch (err) { this.note(err); if (isCancelled(err) || signal?.aborted || !partial) throw err; errors.set(id, err); } }
    return { quotes, errors };
  }
  async search(q: string, n = 30, fresh = 10_000, signal?: AbortSignal) { const lists: { ref: Ref; quote: Quote }[][] = []; let first: unknown;
    for (const id of sourceIDs) { try { const got = await this.sources[id].search(q, n, fresh, signal); this.note(); lists.push(got.map(quote => ({ ref: { source: id, symbol: quote.symbol }, quote }))); } catch (err) { this.note(err); if (signal?.aborted || isCancelled(err)) throw err; first ??= err; } }
    if (!lists.length && first) throw first; const out: { ref: Ref; quote: Quote }[] = [];
    for (let i = 0; out.length < n; i++) { let more = false; for (const list of lists) if (list[i] && out.length < n) { out.push(list[i]); more = true; } if (!more) break; } return out;
  }
  async candles(r: Ref, bar: string, n: number, signal?: AbortSignal) { try { const out = await this.sources[r.source].candles(r.symbol, bar, n, 30_000, signal); this.note(); return out; } catch (err) { this.note(err); throw err; } }
  async addWatch(r: Ref, signal?: AbortSignal) { await this.quote(r, 10_000, signal); if (signal?.aborted) throw signal.reason; await this.store.transaction(next => { const ref = key(r); if (next.watchlist.includes(ref)) return; if (next.watchlist.length >= 50) throw new MutationError("watchLimit", "Watchlist limit: 50"); next.watchlist.push(ref); }); this.changed(); }
  async removeWatch(r: Ref) { await this.store.transaction(next => { next.watchlist = next.watchlist.filter(s => s !== key(r)); }); this.changed(); }
  async moveWatch(r: Ref, by: -1 | 1 | "top") { await this.store.transaction(next => { const w = next.watchlist, i = w.indexOf(key(r)); if (i < 0) return; if (by === "top") { const at = w.flatMap((s, i) => parseRef(s)?.source === r.source ? [i] : []), group = [key(r), ...at.map(i => w[i]).filter(s => s !== key(r))]; at.forEach((i, j) => { w[i] = group[j]; }); }
    else for (let j = i + by; j >= 0 && j < w.length; j += by) if (parseRef(w[j])?.source === r.source) { [w[i], w[j]] = [w[j], w[i]]; break; } }); this.changed(); }
  async addAlert(r: Ref, kind: string, raw: string, signal?: AbortSignal) { const value = typeof raw === "string" ? parseFloat64(raw.trim().replace(/%$/, "")) ?? NaN : NaN; if (!["above", "below", "move"].includes(kind) || !Number.isFinite(value) || value <= 0 || kind === "move" && value > 100) throw new MutationError("invalid", "Invalid alert target"); const quote = await this.quote(r, 10_000, signal), alert: Alert = { id: this.id(), ref: key(r), kind: kind as Alert["kind"], value, created: new Date(this.clock()).toISOString(), name: quote.name, decimals: quote.decimals }; if (meets(alert, quote)) throw new MutationError("alertMet", "Alert target already met"); if (signal?.aborted) throw signal.reason;
    await this.store.transaction(next => { if (next.alerts.length >= 50) throw new MutationError("alertLimit", "Alert limit: 50"); if (next.alerts.some(a => !a.met && a.ref === alert.ref && a.kind === kind && a.value === value)) throw new MutationError("duplicateAlert", "Duplicate pending alert"); next.alerts.push(alert); }); this.changed(); return alert;
  }
  async removeAlert(id: string) { await this.store.transaction(next => { next.alerts = next.alerts.filter(a => a.id !== id); }); this.changed(); }
  checkAlerts(fresh = 30_000, signal?: AbortSignal): Promise<void> { return this.alertCheck ??= this.performAlertCheck(fresh, signal).finally(() => { this.alertCheck = undefined; }); }
  private async performAlertCheck(fresh: number, signal?: AbortSignal) { const pending = this.alerts().filter(a => !a.met); if (!pending.length) return; const { quotes } = await this.quotes([...new Set(pending.map(a => a.ref))], fresh, true, signal); if (signal?.aborted) throw signal.reason; let met = false;
    try { await this.store.transaction(next => { for (const a of next.alerts) { const q = quotes.get(a.ref); if (a.met || !q || !meets(a, q)) continue; a.met = new Date(this.clock()).toISOString(); a.price = q.last; a.change = change(q) ?? 0; a.name = q.name; met = true; } }, true); }
    catch (err) { this.note(err); /* Go parity: keep triggered alerts in memory, wake now; a restart may repeat after a failed save. */ }
    if (met && !this.closed) { this.hub.notify("connector.changed", { contribution: "markets", scope: "inbox" }); this.changed(); }
  }
  inbox(locale = "zh-Hans") { const t = translator(locale), zone = this.cfg.tz || "Local", rules = Array.from((this.cfg.triage_rules ?? "").replace(/\\n/g, "\n").trim()).slice(0, 4000).join("");
    return { things: this.alerts().filter(a => a.met && this.clock() - Date.parse(a.met) < 7 * 86400_000).map(a => { const r = parseRef(a.ref)!, q = { decimals: a.decimals ?? 0 }, name = [a.name, r.symbol].filter(Boolean).join(" "), what = `${t(a.kind)} ${a.kind === "move" ? a.value + "%" : price(q, a.value)}`;
      return { thread: "alert:" + a.id, kind: "price_alert", label: t("priceAlert"), title: name + " " + what, preview: `${name} ${price(q, a.price ?? 0)}, ${fmtChange(a.change ?? 0)}; ${stamp(Date.parse(a.created), zone, "short")}`, sender: t("markets"), at: a.met, last: a.met, tier: "action", why: t("alertWhy"), tags: [{ key: "symbol", value: a.ref, name }], extra: { rule_tier: "action" }, ...(rules ? { rules } : {}), url: this.sources[r.source].url(r.symbol) };
    }) };
  }
}
