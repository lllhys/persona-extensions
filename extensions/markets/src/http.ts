import type { FetchAdapter } from "@persona/extension/ui";
export class ReadError extends Error { constructor(readonly code: "not_found" | "rate_limited" | "http" | "invalid" | "timeout", message: string, readonly status?: number) { super(message); } }
export const notFound = () => new ReadError("not_found", "Instrument not found");
const aborted = (signal?: AbortSignal) => { if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError"); };
export const isCancelled = (err: unknown) => err instanceof Error && err.name === "AbortError";
export class Queue {
  active = 0;
  private waiting: Array<() => void> = [];
  constructor(readonly maximum = 4) {}
  async run<T>(fn: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    aborted(signal);
    if (this.active >= this.maximum) await new Promise<void>((resolve, reject) => {
      const start = () => { signal?.removeEventListener("abort", cancel); this.active++; resolve(); };
      const cancel = () => { const i = this.waiting.indexOf(start); if (i >= 0) this.waiting.splice(i, 1); reject(signal?.reason ?? new DOMException("Cancelled", "AbortError")); };
      if (this.waiting.length >= 128) { reject(new ReadError("rate_limited", "Market request queue full")); return; }
      this.waiting.push(start); signal?.addEventListener("abort", cancel, { once: true });
    }); else this.active++;
    try { aborted(signal); return await fn(); } finally { this.active--; this.waiting.shift()?.(); }
  }
}
export class Cache<T> {
  readonly entries = new Map<string, { value: T; at: number }>();
  constructor(readonly clock: () => number, readonly maximum = 100) {}
  get(k: string, fresh: number) { const c = this.entries.get(k); return c && fresh > 0 && this.clock() - c.at < fresh ? c.value : undefined; }
  put(k: string, value: T) { if (!this.entries.has(k) && this.entries.size >= this.maximum) { let oldest = "", at = Infinity; for (const [k, c] of this.entries) if (c.at < at) { oldest = k; at = c.at; } this.entries.delete(oldest); } this.entries.set(k, { value, at: this.clock() }); }
}
export const hosts = ["www.okx.com", "qt.gtimg.cn", "smartbox.gtimg.cn", "web.ifzq.gtimg.cn", "ifzq.gtimg.cn"];
/** The Deno trusted adapter installs this immutable, grant-checked fetch. No guest transport starts here. */
export const trustedFetch: FetchAdapter = (input, init) => globalThis.fetch(input, init);
export class HTTP {
  readonly queue = new Queue(4);
  readonly cache: Cache<Uint8Array>;
  constructor(private fetch: FetchAdapter = trustedFetch, readonly clock = Date.now) { this.cache = new Cache(clock); }
  async get(url: string, fresh: number, limit = 4 * 1024 * 1024, signal?: AbortSignal, validate?: (body: Uint8Array) => void): Promise<Uint8Array> {
    aborted(signal);
    const u = new URL(url); if (u.protocol !== "https:" || !hosts.includes(u.hostname) || u.username || u.password || u.port) throw new ReadError("invalid", "Unapproved market URL");
    if (!Number.isSafeInteger(limit) || limit < 0) throw new ReadError("invalid", "Invalid market response size limit");
    const bounded = (body: Uint8Array) => { if (body.byteLength > limit) throw new ReadError("invalid", "Market response exceeds size limit"); validate?.(body); return body; };
    const hit = this.cache.get(url, fresh); if (hit) return bounded(hit);
    return this.queue.run(async () => {
      aborted(signal); const cached = this.cache.get(url, fresh); if (cached) return bounded(cached);
      const control = new AbortController(); const cancel = () => control.abort(signal?.reason);
      signal?.addEventListener("abort", cancel, { once: true }); const timer = setTimeout(() => control.abort(new ReadError("timeout", "Market request timed out")), 15_000);
      try {
        const response = await this.fetch(url, { method: "GET", headers: { "User-Agent": "persona-markets/0.6.5", Accept: "application/json" }, signal: control.signal, redirect: "error" });
        if (response.status === 429) { await response.body?.cancel(); throw new ReadError("rate_limited", "Too many requests; try later", 429); }
        if (!response.ok) { await response.body?.cancel(); throw new ReadError("http", `Market returned HTTP ${response.status}`, response.status); }
        const reader = response.body?.getReader(); if (!reader) throw new ReadError("invalid", "Empty market response");
        const chunks: Uint8Array[] = []; let size = 0;
        try { for (;;) { aborted(control.signal); const { done, value } = await reader.read(); if (done) break; size += value.byteLength; if (size > limit) throw new ReadError("invalid", "Market response exceeds size limit"); chunks.push(value); } } catch (err) { await reader.cancel().catch(() => {}); throw err; } finally { reader.releaseLock(); }
        const body = new Uint8Array(size); let i = 0; for (const chunk of chunks) { body.set(chunk, i); i += chunk.length; }
        aborted(control.signal); bounded(body); this.cache.put(url, body); return body;
      } catch (err) { if (control.signal.aborted) throw control.signal.reason; if (err instanceof ReadError) throw err; throw new ReadError("http", "Cannot read market response"); }
      finally { clearTimeout(timer); signal?.removeEventListener("abort", cancel); }
    }, signal);
  }
  async json(url: string, fresh: number, limit?: number, signal?: AbortSignal, validate?: (value: any) => void, parse: (text: string) => unknown = JSON.parse): Promise<any> {
    let value: unknown;
    await this.get(url, fresh, limit, signal, body => {
      try { value = parse(new TextDecoder("utf-8", { fatal: true }).decode(body)); validate?.(value); }
      catch (err) { if (err instanceof ReadError) throw err; throw new ReadError("invalid", "Invalid market JSON"); }
    });
    return value;
  }
}
