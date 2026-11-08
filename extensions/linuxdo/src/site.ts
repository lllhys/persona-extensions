import type { FetchAdapter } from "@persona/extension/ui";
import { fmt, textsFor } from "./i18n.ts";
import { InputError } from "./errors.ts";
import { decimalInt64 } from "./model.ts";
export const limits = { gap: 2000, request: 20_000, list: 300_000, topic: 600_000, pause: 600_000, response: 8 << 20, entries: 200, bytes: 32 << 20 } as const;
export type ErrorCode = "rate" | "challenge" | "gone" | "network" | "bad" | "slow" | "http";
export class ReadError extends Error { constructor(readonly code: ErrorCode, readonly status?: number) { super(code === "http" ? `LINUX DO: HTTP ${status}` : textsFor("en")[code]); } }
export const errorText = (error: unknown, locale = "") => {
  const t = textsFor(locale);
  if (error instanceof InputError) return fmt(t[error.code], error.field);
  return error instanceof ReadError ? (error.code === "http" ? fmt(t.http, error.status ?? 0) : t[error.code]) : t.network;
};
export interface Reading<T> { value: T; at: number; live: boolean; stale?: ReadError }
interface Cached { value: unknown; at: number; bytes: number }
export interface SiteOptions { now?: () => number; gap?: number; request?: number; entries?: number; bytes?: number }
/** Abortable waits never retain a waiter or timer after cancellation. */
function wait<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return promise;
  signal.throwIfAborted();
  return new Promise((resolve, reject) => {
    const abort = () => { cleanup(); reject(signal.reason); };
    const cleanup = () => signal.removeEventListener("abort", abort);
    signal.addEventListener("abort", abort, { once: true });
    promise.then(value => { cleanup(); resolve(value); }, error => { cleanup(); reject(error); });
  });
}
function delay(ms: number, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  return new Promise((resolve, reject) => {
    const cleanup = () => { clearTimeout(timer); signal?.removeEventListener("abort", abort); };
    const abort = () => { cleanup(); reject(signal?.reason); };
    const timer = setTimeout(() => { cleanup(); resolve(); }, ms);
    signal?.addEventListener("abort", abort, { once: true });
  });
}
export class Site {
  readonly now: () => number;
  private cache = new Map<string, Cached>();
  private total = 0;
  private gate: Promise<void> = Promise.resolve();
  private last = -Infinity;
  private paused = 0;
  constructor(private fetch: FetchAdapter, private options: SiteOptions = {}) { this.now = options.now ?? Date.now; }
  get stats() { return { entries: this.cache.size, bytes: this.total, paused: this.paused }; }
  private prune() {
    if (this.cache.size <= (this.options.entries ?? limits.entries) && this.total <= (this.options.bytes ?? limits.bytes)) return;
    for (const [key, value] of this.cache) if (this.now() - value.at > (key.startsWith("/t/") ? limits.topic : limits.list)) this.drop(key);
    while (this.cache.size > (this.options.entries ?? limits.entries) || this.total > (this.options.bytes ?? limits.bytes)) {
      let oldest: string | undefined;
      for (const [key, value] of this.cache) if (oldest === undefined || value.at < this.cache.get(oldest)!.at) oldest = key;
      if (oldest === undefined) break;
      this.drop(oldest);
    }
  }
  private drop(key: string) { const old = this.cache.get(key); if (old) this.total -= old.bytes; this.cache.delete(key); }
  async get<T>(path: string, fresh: number, parse: (raw: string) => T, signal?: AbortSignal): Promise<Reading<T>> {
    signal?.throwIfAborted();
    const readCache = (): Reading<T> | undefined => { const c = this.cache.get(path); return c && fresh > 0 && this.now() - c.at < fresh ? { value: c.value as T, at: c.at, live: false } : undefined; };
    const hit = readCache(); if (hit) return hit;
    const previous = this.gate; let release!: () => void;
    const turn = new Promise<void>(resolve => { release = resolve; });
    this.gate = previous.then(() => turn);
    try { await wait(previous, signal); }
    catch (error) { release(); throw error; }
    try {
      signal?.throwIfAborted();
      const queuedHit = readCache(); if (queuedHit) return queuedHit;
      try {
        if (this.now() < this.paused) throw new ReadError("rate");
        const gap = (this.options.gap ?? limits.gap) - (this.now() - this.last);
        if (gap > 0) await delay(gap, signal);
        signal?.throwIfAborted();
        const timeout = AbortSignal.timeout(this.options.request ?? limits.request);
        const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
        let bytes: Uint8Array;
        try {
          const res = await this.fetch(`https://linux.do${path}`, { method: "GET", redirect: "error", headers: { "User-Agent": "persona-linuxdo/0.1.14 (RSS reader)" }, signal: combined });
          // Rate-limit headers apply even when the error body is oversized,
          // unreadable or stalled. Do not drain that body or hold the queue for it.
          if (res.status === 429) {
            const retry = res.headers.get("Retry-After") ?? "";
            let pause: number = limits.pause;
            try {
              const seconds = decimalInt64(retry);
              if (seconds > 0n) {
                // Preserve Go's signed time.Duration multiplication, including its
                // wraparound/minimum behavior; out-of-int64 headers use the default.
                const ns = BigInt.asIntN(64, seconds * 1_000_000_000n);
                pause = Number(ns > 60_000_000_000n ? ns : 60_000_000_000n) / 1_000_000;
              }
            } catch { /* Invalid Retry-After keeps the original default. */ }
            this.paused = this.now() + pause;
            void res.body?.cancel().catch(() => {});
            throw new ReadError("rate");
          }
          const topicGone = (res.status === 403 || res.status === 404) && path.startsWith("/t/");
          // Match Go's bounded 8 MiB error-body read, including errors before the
          // cap. Truncation, not a bad-data error, preserves gone/challenge status.
          bytes = await this.body(res, combined, topicGone);
          const prefix = new TextDecoder().decode(bytes.subarray(0, 2048));
          if (res.status !== 200 && prefix.includes("Just a moment")) throw new ReadError("challenge");
          if ((res.status === 403 || res.status === 404) && path.startsWith("/t/")) throw new ReadError("gone");
          if (res.status !== 200) throw new ReadError("http", res.status);
          combined.throwIfAborted();
        } catch (error) {
          signal?.throwIfAborted();
          if (timeout.aborted) throw new ReadError("slow");
          throw error instanceof ReadError ? error : new ReadError("network");
        } finally { this.last = this.now(); }
        let value: T;
        try { value = parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)); }
        catch (error) { throw error instanceof ReadError ? error : new ReadError("bad"); }
        const at = this.now(); this.drop(path);
        // Keep parsed data only; raw response bytes are not retained alongside it.
        this.cache.set(path, { value, at, bytes: bytes.byteLength }); this.total += bytes.byteLength; this.prune();
        return { value, at, live: true };
      } catch (error) {
        signal?.throwIfAborted();
        const failure = error instanceof ReadError ? error : new ReadError("network");
        const old = this.cache.get(path);
        if (old && failure.code !== "gone") return { value: old.value as T, at: old.at, live: false, stale: failure };
        throw failure;
      }
    } finally { release(); }
  }
  private async body(response: Response, signal: AbortSignal, truncate = false): Promise<Uint8Array> {
    const declared = Number(response.headers.get("Content-Length"));
    if (!truncate && Number.isFinite(declared) && declared > limits.response) { void response.body?.cancel().catch(() => {}); throw new ReadError("bad"); }
    const reader = response.body?.getReader(); if (!reader) return new Uint8Array();
    const parts: Uint8Array[] = []; let size = 0;
    try {
      while (true) {
        const result = await wait(reader.read(), signal); if (result.done) break;
        const part = truncate ? result.value.subarray(0, limits.response - size) : result.value;
        size += part.byteLength; if (size > limits.response) throw new ReadError("bad"); parts.push(part);
        if (truncate && size === limits.response) break;
      }
      const body = new Uint8Array(size); let offset = 0; for (const part of parts) { body.set(part, offset); offset += part.byteLength; } return body;
    } finally {
      // Cancellation starts immediately, but a tee/clone may never settle its
      // cancel promise until its other branch drains. Do not hold the queue for it.
      void reader.cancel().catch(() => {}); reader.releaseLock();
    }
  }
}
