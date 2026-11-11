import type { FetchAdapter } from "@persona/extension/ui";
import { limits, lookupPath, parseProduct, parseSearch, ReadError, searchPath, type LimitOptions, type Product, type SearchResult } from "./model.ts";
import { textsFor } from "./i18n.ts";
export { limits, ReadError } from "./model.ts";
export interface ServiceOptions { now?: () => number; monotonic?: () => number; limits?: LimitOptions; locale?: string }
function cancellation(signal: AbortSignal): Error {
  return signal.reason instanceof ReadError ? signal.reason : new DOMException("Cancelled", "AbortError");
}
export function checkSignal(signal?: AbortSignal) { if (signal?.aborted) throw cancellation(signal); }
export function budget(parent: AbortSignal | undefined, milliseconds: number) {
  const controller = new AbortController();
  const cancel = () => controller.abort(parent?.reason ?? new DOMException("Cancelled", "AbortError"));
  if (parent?.aborted) cancel(); else parent?.addEventListener("abort", cancel, { once: true });
  const timer = setTimeout(() => controller.abort(new ReadError("slow")), milliseconds);
  return { signal: controller.signal, dispose: () => { clearTimeout(timer); parent?.removeEventListener("abort", cancel); } };
}
export class FoodFacts {
  readonly locale: string;
  readonly now: () => number;
  readonly monotonic: () => number;
  readonly limits: { [K in keyof typeof limits]: number };
  private readonly cache = new Map<string, { raw: unknown; at: number; used: number; bytes: number }>();
  private bytes = 0;
  private lastRequest = -Infinity;
  private queue: Promise<void> = Promise.resolve();
  private lastOK: number | undefined;
  private lastError: { code: ReadError["code"]; at: number } | undefined;
  constructor(private readonly fetch: FetchAdapter, _config: unknown = {}, options: ServiceOptions = {}) {
    this.locale = options.locale ?? "en";
    this.now = options.now ?? Date.now; this.monotonic = options.monotonic ?? (() => performance.now()); this.limits = { ...limits, ...options.limits };
  }
  private cached(path: string, fresh: number): { raw: unknown; at: number } | undefined {
    const item = this.cache.get(path);
    return item && this.monotonic() - item.used < fresh ? { raw: item.raw, at: item.at } : undefined;
  }
  private save(path: string, raw: unknown, at: number) {
    const bytes = new TextEncoder().encode(JSON.stringify(raw)).byteLength;
    if (bytes > this.limits.cacheBytes) return;
    const old = this.cache.get(path); if (old) this.bytes -= old.bytes;
    this.cache.set(path, { raw, at, used: this.monotonic(), bytes }); this.bytes += bytes;
    while (this.cache.size > this.limits.cache || this.bytes > this.limits.cacheBytes) {
      const oldest = this.cache.keys().next().value as string | undefined;
      if (oldest === undefined) break;
      this.bytes -= this.cache.get(oldest)!.bytes; this.cache.delete(oldest);
    }
  }
  private async response(path: string, signal: AbortSignal): Promise<unknown> {
    let response: Response;
    try {
      response = await this.fetch(`https://world.openfoodfacts.org${path}`, {
        method: "GET", headers: { Accept: "application/json", "User-Agent": "persona-foodfacts/0.1.0" }, signal, redirect: "error",
      });
    } catch (error) { checkSignal(signal); throw error instanceof ReadError ? error : new ReadError("network"); }
    if (!response.body) throw new ReadError("bad_answer");
    const reader = response.body.getReader(), chunks: Uint8Array[] = [];
    let length = 0;
    const cancel = () => { void reader.cancel().catch(() => {}); };
    signal.addEventListener("abort", cancel, { once: true });
    try {
      while (true) {
        checkSignal(signal);
        const chunk = await reader.read(); checkSignal(signal);
        if (chunk.done) break;
        length += chunk.value.byteLength;
        if (length > this.limits.response) { await reader.cancel(); throw new ReadError("bad_answer"); }
        chunks.push(chunk.value);
      }
    } finally { signal.removeEventListener("abort", cancel); reader.releaseLock(); }
    const data = new Uint8Array(length); let offset = 0;
    for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.byteLength; }
    let text: string;
    try { text = new TextDecoder("utf-8", { fatal: true }).decode(data); } catch { throw new ReadError("bad_answer"); }
    if (response.status === 401 || response.status === 403 || response.status === 429) throw new ReadError("rate_limited");
    if (response.status === 404) throw new ReadError("not_found");
    if (response.status !== 200) throw new ReadError("http", response.status);
    try { return JSON.parse(text); } catch { throw new ReadError("bad_answer"); }
  }
  private async read(path: string, fresh: number, signal?: AbortSignal): Promise<{ raw: unknown; at: number; stale?: ReadError }> {
    checkSignal(signal);
    const cached = this.cached(path, fresh); if (cached) return cached;
    const run = this.queue.then(async () => {
      const again = this.cached(path, fresh); if (again) return again;
      const pause = Math.max(0, this.limits.gap - (this.monotonic() - this.lastRequest));
      if (pause) await new Promise<void>((resolve, reject) => {
        const cancel = () => { clearTimeout(timer); reject(cancellation(signal!)); };
        const timer = setTimeout(() => { signal?.removeEventListener("abort", cancel); resolve(); }, pause);
        if (signal?.aborted) cancel(); else signal?.addEventListener("abort", cancel, { once: true });
      });
      checkSignal(signal);
      const request = budget(signal, this.limits.request);
      try {
        const raw = await this.response(path, request.signal);
        const at = this.now(); this.save(path, raw, at); this.lastOK = at; this.lastError = undefined;
        return { raw, at };
      } catch (error) {
        if (request.signal.aborted) throw error instanceof Error && error.name === "AbortError" ? error : new DOMException("Cancelled", "AbortError");
        const failure = error instanceof ReadError ? error : error instanceof Error && error.name === "AbortError" ? error : new ReadError("network");
        if (failure instanceof ReadError) this.lastError = { code: failure.code, at: this.monotonic() };
        throw failure;
      } finally { this.lastRequest = this.monotonic(); request.dispose(); }
    });
    this.queue = run.then(() => {}, () => {});
    try { return await run; }
    catch (error) {
      if (error instanceof Error && error.name === "AbortError") throw error;
      const old = this.cached(path, Infinity);
      if (old && (!(error instanceof ReadError) || error.code !== "not_found")) return { ...old, stale: error instanceof ReadError ? error : new ReadError("network") };
      throw error;
    }
  }
  status() {
    const t = textsFor(this.locale);
    return { state: this.lastError ? this.lastError.code === "rate_limited" ? "rate_limited" : "error" : "ok", account: t.account,
      ...(this.lastOK === undefined ? {} : { last_sync: new Date(this.lastOK).toISOString() }),
      ...(this.lastError ? { error: t[this.lastError.code] } : {}) };
  }
  async search(query: string, limit = 12, refresh = false, signal?: AbortSignal): Promise<SearchResult> {
    const rd = await this.read(searchPath(query, limit), refresh ? 0 : this.limits.fresh, signal);
    return { ...parseSearch(rd.raw, rd.at), stale: rd.stale };
  }
  async product(code: string, refresh = false, signal?: AbortSignal): Promise<Product> {
    const rd = await this.read(lookupPath(code), refresh ? 0 : this.limits.fresh, signal);
    return parseProduct(rd.raw, code, rd.at);
  }
  cacheStats() { return { entries: this.cache.size, bytes: this.bytes }; }
}
