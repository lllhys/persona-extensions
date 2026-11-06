import type { FetchAdapter } from "@persona/extension/ui";
import { feeds, gone, isFeed, normalize, type Config, type Item, type Feed } from "./model.ts";
import { fmt, textsFor } from "./i18n.ts";
const api = "https://hacker-news.firebaseio.com/v0";
export const limits = { response: 2 << 20, cache: 2000, parallel: 8, fresh: 300_000, refresh: 30_000, request: 15_000 } as const;
type FailureCode = "slow" | "bad_answer" | "not_found" | "unreachable" | "http" | "all_missed";
export class ReadError extends Error {
  constructor(readonly code: FailureCode, readonly status = 0) { super(fmt(textsFor("en")[code], status)); }
}
export function errorText(error: unknown, locale: string): string {
  return error instanceof ReadError ? fmt(textsFor(locale)[error.code], error.status) : textsFor(locale).unreachable;
}
interface Reading { value: unknown; at: number; stale?: ReadError }
export interface Listing { stories: Item[]; missed: number; at: number; stale?: ReadError }
export interface Thread { story: Item; comments: Item[]; missed: number }
const aborted = (signal?: AbortSignal) => signal?.throwIfAborted();
const cancelled = (error: unknown) => error instanceof Error && error.name === "AbortError";
function ids(value: unknown): number[] {
  if (!Array.isArray(value) || value.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new ReadError("bad_answer");
  // Duplicate API IDs must not create duplicate native keys.
  return [...new Set(value)] as number[];
}
function item(value: unknown, id: number): Item {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  const data = value as Record<string, unknown>;
  if (data.id !== id) throw new ReadError("bad_answer");
  const string = (key: string) => { if (data[key] === undefined) return ""; if (typeof data[key] !== "string") throw new ReadError("bad_answer"); return data[key] as string; };
  const number = (key: string) => { if (data[key] === undefined) return 0; if (!Number.isSafeInteger(data[key])) throw new ReadError("bad_answer"); return data[key] as number; };
  const boolean = (key: string) => { if (data[key] === undefined) return false; if (typeof data[key] !== "boolean") throw new ReadError("bad_answer"); return data[key] as boolean; };
  return { id, type: string("type"), by: string("by"), time: number("time"), title: string("title"), url: string("url"),
    text: string("text"), score: number("score"), descendants: number("descendants"), kids: data.kids === undefined ? [] : ids(data.kids),
    dead: boolean("dead"), deleted: boolean("deleted") };
}
/** Reads only the declared Firebase destination. The host must provide its sanctioned egress adapter. */
export class HackerNews {
  readonly config: Required<Config>;
  private cache = new Map<string, Reading>();
  private running = 0;
  private queue: (() => void)[] = [];
  private lastOK?: number;
  private lastError?: ReadError;
  constructor(private fetch: FetchAdapter, config: Config = {}, readonly now: () => number = Date.now, private cacheLimit: number = limits.cache) {
    if (typeof fetch !== "function") throw new Error("a sanctioned fetch adapter is required");
    if (!Number.isSafeInteger(cacheLimit) || cacheLimit < 1 || cacheLimit > limits.cache) throw new Error("invalid cache limit");
    this.config = normalize(config);
  }
  status(locale = "") {
    const fail = this.lastError !== undefined;
    return { state: fail ? "error" : "ok", account: textsFor(locale).account,
      ...(this.lastOK !== undefined ? { last_sync: new Date(this.lastOK).toISOString() } : {}),
      ...(fail ? { error: errorText(this.lastError, locale) } : {}) };
  }
  private async acquire(signal: AbortSignal): Promise<() => void> {
    aborted(signal);
    if (this.running >= limits.parallel) await new Promise<void>((resolve, reject) => {
      const ready = () => { signal.removeEventListener("abort", abort); resolve(); };
      const abort = () => { const i = this.queue.indexOf(ready); if (i >= 0) this.queue.splice(i, 1); reject(signal.reason); };
      this.queue.push(ready); signal.addEventListener("abort", abort, { once: true });
    });
    // A released slot is reserved for its queued waiter, even before its continuation runs.
    else this.running++;
    if (signal.aborted) { this.release(); signal.throwIfAborted(); }
    return () => this.release();
  }
  private release() { const next = this.queue.shift(); if (next) next(); else this.running--; }
  private async request(path: string, signal?: AbortSignal): Promise<unknown> {
    const combined = signal ? AbortSignal.any([signal, AbortSignal.timeout(limits.request)]) : AbortSignal.timeout(limits.request);
    let release: (() => void) | undefined;
    try {
      release = await this.acquire(combined);
      const response = await this.fetch(api + path, { method: "GET", redirect: "error", signal: combined, headers: { Accept: "application/json" } });
      if (response.status !== 200) {
        // A tee-backed Response cancellation can wait for another reader. Never block the gate on it.
        void response.body?.cancel().catch(() => {});
        throw response.status === 404 ? new ReadError("not_found") : new ReadError("http", response.status);
      }
      const reader = response.body?.getReader();
      if (!reader) throw new ReadError("bad_answer");
      const parts: Uint8Array[] = []; let size = 0;
      // Host-provided streams may not themselves observe the request signal.
      const cancel = () => { void reader.cancel().catch(() => {}); };
      combined.addEventListener("abort", cancel, { once: true });
      try {
        for (;;) { aborted(combined); const part = await reader.read(); if (part.done) break;
          size += part.value.length; if (size > limits.response) throw new ReadError("bad_answer"); parts.push(part.value); }
      } finally { combined.removeEventListener("abort", cancel); cancel(); reader.releaseLock(); }
      aborted(combined);
      const bytes = new Uint8Array(size); let offset = 0;
      for (const part of parts) { bytes.set(part, offset); offset += part.length; }
      let result: unknown;
      try { result = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)); } catch { throw new ReadError("bad_answer"); }
      if (result === null) throw new ReadError("not_found");
      return result;
    } catch (error) {
      if (signal?.aborted) signal.throwIfAborted();
      if (combined.aborted) throw new ReadError("slow");
      if (error instanceof ReadError) throw error;
      throw new ReadError("unreachable");
    } finally { release?.(); }
  }
  private async read(path: string, refresh: boolean, validate: (value: unknown) => unknown, signal?: AbortSignal): Promise<Reading> {
    aborted(signal);
    const old = this.cache.get(path);
    if (old && this.now() - old.at < (refresh ? limits.refresh : limits.fresh)) return old;
    try {
      const value = validate(await this.request(path, signal)); aborted(signal);
      const reading = { value, at: this.now() };
      this.cache.set(path, reading);
      while (this.cache.size > this.cacheLimit) {
        const first = [...this.cache].reduce((a, b) => a[1].at <= b[1].at ? a : b);
        this.cache.delete(first[0]);
      }
      this.lastOK = reading.at;
      this.lastError = undefined;
      return reading;
    } catch (error) {
      aborted(signal);
      if (cancelled(error)) throw error;
      const failure = error instanceof ReadError ? error : new ReadError("bad_answer");
      if (failure.code === "not_found") this.cache.delete(path);
      else this.lastError = failure;
      if (old && failure.code !== "not_found") return { ...old, stale: failure };
      throw failure;
    }
  }
  async item(id: number, refresh = false, signal?: AbortSignal): Promise<Item> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ReadError("not_found");
    const result = await this.read(`/item/${id}.json`, refresh, value => item(value, id), signal);
    return result.value as Item;
  }
  private async items(ids: number[], refresh: boolean, signal?: AbortSignal): Promise<{ items: Item[]; missed: number }> {
    const result = await Promise.all(ids.map(async id => {
      try { return await this.item(id, refresh, signal); }
      catch (error) { aborted(signal); return error instanceof ReadError && error.code === "not_found" ? null : undefined; }
    }));
    aborted(signal);
    return { items: result.filter((it): it is Item => !!it), missed: result.filter(it => it === undefined).length };
  }
  async stories(feed: Feed, count = 30, refresh = false, signal?: AbortSignal): Promise<Listing> {
    if (!isFeed(feed)) throw new Error("unknown list");
    const rd = await this.read(`/${feeds[feed]}.json`, refresh, ids, signal);
    const wanted = (rd.value as number[]).slice(0, Math.min(30, Math.max(0, Math.trunc(count))));
    const result = await this.items(wanted, refresh, signal);
    if (wanted.length && result.missed === wanted.length) throw new ReadError("all_missed");
    return { stories: result.items.filter(it => !gone(it)), missed: result.missed, at: rd.at, stale: rd.stale };
  }
  async thread(id: number, count = 15, refresh = false, signal?: AbortSignal): Promise<Thread> {
    const story = await this.item(id, refresh, signal);
    if (gone(story)) return { story, comments: [], missed: 0 };
    count = Math.max(0, Math.min(30, Math.trunc(count)));
    const result = await this.items(story.kids.slice(0, count + Math.floor(count / 2)), refresh, signal);
    return { story, comments: result.items.filter(it => !gone(it)).slice(0, count), missed: result.missed };
  }
}
