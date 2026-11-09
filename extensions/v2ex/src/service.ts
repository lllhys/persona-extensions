import type { FetchAdapter } from "@persona/extension/ui";
import { configFrom, cleanNodes, decodeStruct, limits, nodePath, parseArray, parseJSON, parseReplies, parseTopics, ReadError, repliesPath, topicPath,
  validTimezone, type Config, type Decoded, type Integer, type LimitOptions, type Listing, type Reading, type Thread } from "./model.ts";
import { errorText, textsFor } from "./i18n.ts";
export { ReadError, limits } from "./model.ts";
export interface ServiceOptions { now?: () => number; monotonic?: () => number; limits?: LimitOptions; timezone?: string; locale?: string }
function abortError(signal: AbortSignal): Error { return signal.reason?.name === "TimeoutError" || signal.reason instanceof ReadError && signal.reason.code === "slow" ? new ReadError("slow") : new DOMException("Cancelled", "AbortError"); }
export function checkSignal(signal?: AbortSignal) { if (signal?.aborted) throw abortError(signal); }
export function budget(parent: AbortSignal | undefined, milliseconds: number) {
  const controller = new AbortController();
  const cancel = () => controller.abort(parent?.reason ?? new DOMException("Cancelled", "AbortError"));
  if (parent?.aborted) cancel(); else parent?.addEventListener("abort", cancel, { once: true });
  const timer = setTimeout(() => controller.abort(new ReadError("slow")), milliseconds);
  return { signal: controller.signal, dispose: () => { clearTimeout(timer); parent?.removeEventListener("abort", cancel); } };
}
function wait(milliseconds: number, signal?: AbortSignal): Promise<void> {
  checkSignal(signal);
  return new Promise((resolve, reject) => {
    const done = () => { clearTimeout(timer); signal?.removeEventListener("abort", cancel); };
    const cancel = () => { done(); reject(abortError(signal!)); };
    const timer = setTimeout(() => { done(); resolve(); }, Math.max(0, milliseconds));
    signal?.addEventListener("abort", cancel, { once: true });
  });
}
/** A cancellable FIFO gate; cancelled waiters leave the queue immediately. */
class Gate {
  private held = false;
  private queue: { grant: () => void; cancel: () => void }[] = [];
  async acquire(signal?: AbortSignal): Promise<() => void> {
    checkSignal(signal);
    if (this.held) await new Promise<void>((resolve, reject) => {
      const entry = { grant: () => { signal?.removeEventListener("abort", entry.cancel); resolve(); },
        cancel: () => { this.queue = this.queue.filter(item => item !== entry); reject(abortError(signal!)); } };
      this.queue.push(entry); signal?.addEventListener("abort", entry.cancel, { once: true });
    });
    else this.held = true;
    let released = false;
    return () => { if (released) return; released = true; const next = this.queue.shift(); if (next) next.grant(); else this.held = false; };
  }
}
const integerHeader = (value: string | null): bigint | undefined => {
  if (value == null || !/^[+-]?\d+$/.test(value)) return undefined;
  const n = BigInt(value); return n >= -9223372036854775808n && n <= 9223372036854775807n ? n : undefined;
};
const untilReset = (reset: Integer, now: number) => (BigInt.asIntN(64, BigInt(reset) + 62135596800n) - 62135596800n) * 1000n - BigInt(Math.trunc(now));
export function limitPause(headers: Headers, resetAt: Integer, now: number, settings: LimitOptions = {}): number {
  const l = { ...limits, ...settings }, seconds = integerHeader(headers.get("Retry-After"));
  if (seconds !== undefined && seconds > 0n) return Math.max(60_000, Number(seconds > BigInt(Math.floor(l.pauseMost / 1000)) ? BigInt(Math.floor(l.pauseMost / 1000)) : seconds) * 1000);
  const delta = untilReset(resetAt, now);
  if (resetAt > 0 && delta > 0n) return Number(delta > BigInt(l.pauseMost) ? BigInt(l.pauseMost) : delta);
  return l.pause;
}
export class V2EX {
  readonly config: Config;
  readonly nodes: string[];
  readonly badNodes: string[];
  readonly timezone: string;
  readonly locale: string;
  readonly now: () => number;
  readonly monotonic: () => number;
  readonly limits: { [K in keyof typeof limits]: number };
  private readonly gate = new Gate();
  private readonly cache = new Map<string, { body: string; at: number; monotonic: number; bytes: number }>();
  private size = 0;
  private last = -Infinity;
  private paused = 0;
  private lastOK: number | undefined;
  private lastOKMonotonic: number | undefined;
  private lastError: { error: ReadError; at: number } | undefined;
  constructor(private readonly fetch: FetchAdapter, config: unknown = {}, options: ServiceOptions = {}) {
    this.config = configFrom(config); const nodes = cleanNodes(this.config.nodes); this.nodes = nodes.good; this.badNodes = nodes.bad;
    this.timezone = this.config.tz || (options.timezone && validTimezone(options.timezone) ? options.timezone : "Local");
    this.locale = options.locale ?? ""; this.now = options.now ?? Date.now;
    this.monotonic = options.monotonic ?? (() => performance.now()); this.limits = { ...limits, ...options.limits };
  }
  offers(id: unknown): id is "hot" | "latest" | "nodes" { return id === "hot" || id === "latest" || id === "nodes" && this.nodes.length > 0; }
  zoneFor(tz?: string) { return !this.config.tz && tz && validTimezone(tz) ? tz : this.timezone; }
  cacheStats() { return { entries: this.cache.size, bytes: this.size, pausedUntil: this.paused }; }
  private young(path: string, fresh: number): Reading | undefined {
    const c = this.cache.get(path); return c && fresh > 0 && this.monotonic() - c.monotonic < fresh ? { body: c.body, at: c.at, live: false } : undefined;
  }
  private store(path: string, body: string, at: number) {
    const bytes = new TextEncoder().encode(body).length;
    const old = this.cache.get(path); if (old) { this.size -= old.bytes; this.cache.delete(path); }
    // Enforce the byte ceiling even for one response larger than the entire cache.
    if (bytes > this.limits.bytes) return;
    this.cache.set(path, { body, at, monotonic: this.monotonic(), bytes }); this.size += bytes;
    while (this.cache.size > this.limits.entries || this.size > this.limits.bytes) {
      let oldest: string | undefined, at = Infinity;
      for (const [key, value] of this.cache) if (value.monotonic < at) { oldest = key; at = value.monotonic; }
      if (oldest === undefined) break; this.size -= this.cache.get(oldest)!.bytes; this.cache.delete(oldest);
    }
  }
  private pause(until: number) { this.paused = Math.max(this.paused, until); }
  private noteQuota(headers: Headers) {
    if (!["", "MISS", "BYPASS", "DYNAMIC", "EXPIRED"].includes(headers.get("Cf-Cache-Status") ?? "")) return;
    const left = integerHeader(headers.get("X-Rate-Limit-Remaining")), reset = integerHeader(headers.get("X-Rate-Limit-Reset"));
    const now = this.now();
    if (left === undefined || left >= this.limits.quota || reset === undefined) return;
    const delta = untilReset(reset, now);
    if (delta > 0n) this.pause(this.monotonic() + Number(delta > BigInt(this.limits.pauseMost) ? BigInt(this.limits.pauseMost) : delta));
  }
  private judge(response: Response, body: string) {
    let raw: unknown = {};
    if (body.trimStart().startsWith("{")) { try { raw = parseJSON(body); } catch { /* Syntax errors leave Go's entire destination untouched. */ } }
    const refusal = decodeStruct(raw, { message: "string", rate_limit: { used: "integer", quota: "integer", reset_at: "integer" } }).value;
    const q = refusal.rate_limit as Decoded, quota = q.quota as Integer, used = q.used as Integer, spent = quota > 0 && used >= quota;
    if (response.status === 429 || spent || typeof refusal.message === "string" && refusal.message.toLowerCase().includes("rate limit")) {
      this.pause(this.monotonic() + limitPause(response.headers, q.reset_at as Integer, this.now(), this.limits)); throw new ReadError("rate_limited");
    }
    if (body.slice(0, 2048).includes("Just a moment") || response.status === 403) throw new ReadError("challenged");
    if (response.status === 404) throw new ReadError("not_found");
    if (response.status !== 200) throw new ReadError("http", response.status);
    parseArray(body);
  }
  private async body(response: Response, signal: AbortSignal): Promise<string> {
    if (!response.body) return "";
    const reader = response.body.getReader(), decoder = new TextDecoder(), out: string[] = []; let size = 0;
    const cancel = () => { void reader.cancel().catch(() => {}); };
    signal.addEventListener("abort", cancel, { once: true });
    try {
      while (true) {
        checkSignal(signal); const chunk = await reader.read(); checkSignal(signal);
        if (chunk.done) break; size += chunk.value.byteLength;
        if (size > this.limits.response) { await reader.cancel(); throw new ReadError("bad_answer"); }
        out.push(decoder.decode(chunk.value, { stream: true }));
      }
      out.push(decoder.decode()); return out.join("");
    } finally { signal.removeEventListener("abort", cancel); reader.releaseLock(); }
  }
  private async read(path: string, fresh: number, signal?: AbortSignal): Promise<Reading> {
    const release = await this.gate.acquire(signal);
    try {
      checkSignal(signal);
      const cached = this.young(path, fresh); if (cached) return cached;
      checkSignal(signal); if (this.monotonic() < this.paused) throw new ReadError("rate_limited");
      await wait(this.limits.gap - (this.monotonic() - this.last), signal);
      checkSignal(signal); if (this.monotonic() < this.paused) throw new ReadError("rate_limited");
      const request = budget(signal, this.limits.request);
      try {
        // Only known public paths, through the host's trusted, grant-checked adapter.
        const response = await this.fetch(`https://www.v2ex.com/api${path}`, { method: "GET", headers: { "User-Agent": "persona-v2ex/0.1.0", Accept: "application/json" }, signal: request.signal, redirect: "error" });
        const body = await this.body(response, request.signal); checkSignal(request.signal);
        this.noteQuota(response.headers); this.judge(response, body);
        // Validate the projected types before replacing a good cached answer.
        if (path.startsWith("/replies/")) parseReplies(body); else parseTopics(body);
        const at = this.now(); this.store(path, body, at); return { body, at, live: true };
      } catch (error) {
        if (request.signal.aborted) throw abortError(request.signal);
        if (error instanceof ReadError || error instanceof Error && error.name === "AbortError") throw error;
        throw new ReadError("network");
      } finally { this.last = this.monotonic(); request.dispose(); }
    } finally { release(); }
  }
  async get(path: string, fresh: number, signal?: AbortSignal): Promise<Reading> {
    // A cached response can still contribute when a multi-node deadline has elapsed.
    if (signal?.aborted && signal.reason?.name !== "TimeoutError" && !(signal.reason instanceof ReadError && signal.reason.code === "slow")) checkSignal(signal);
    const young = this.young(path, fresh); if (young) return young;
    let rd: Reading;
    try { rd = await this.read(path, fresh, signal); }
    catch (error) {
      if (error instanceof Error && error.name === "AbortError") throw error;
      const failure = error instanceof ReadError ? error : new ReadError("network");
      if (failure.code !== "not_found") this.lastError = { error: failure, at: this.monotonic() };
      const old = this.cache.get(path);
      if (!old || failure.code === "not_found") throw failure;
      return { body: old.body, at: old.at, live: false, stale: failure };
    }
    if (rd.live) { this.lastOK = rd.at; this.lastOKMonotonic = this.monotonic(); }
    return rd;
  }
  status() {
    const t = textsFor(this.locale), failed = this.lastError && (this.lastOKMonotonic === undefined || this.lastError.at > this.lastOKMonotonic);
    const state = this.monotonic() < this.paused ? "rate_limited" : failed && this.lastError!.error.code !== "rate_limited" ? "error" : "ok";
    return { state, account: t.account, ...(this.lastOK === undefined ? {} : { last_sync: new Date(this.lastOK).toISOString() }),
      ...(state === "rate_limited" ? { error: t.rate_limited } : state === "error" ? { error: errorText(this.lastError!.error, this.locale),
        ...(this.lastError!.error.code === "challenged" ? { guide: [t.guide] } : {}) } : {}) };
  }
  private async topics(path: string, fresh: number, signal?: AbortSignal): Promise<Listing> {
    const rd = await this.get(path, fresh, signal); return { topics: parseTopics(rd.body), at: rd.at, stale: rd.stale, missed: [] };
  }
  async node(name: string, fresh = this.limits.list, signal?: AbortSignal): Promise<Listing> {
    try { return await this.topics(nodePath(name), fresh, signal); }
    catch (error) { if (error instanceof ReadError && error.code === "not_found") throw new ReadError("no_node"); throw error; }
  }
  async list(source: string, refresh = false, signal?: AbortSignal): Promise<Listing> {
    const fresh = refresh ? this.limits.refresh : this.limits.list;
    if (source === "nodes") {
      if (!this.nodes.length) return this.list(this.offers(this.config.list) ? this.config.list! : "hot", refresh, signal);
      const out: Listing = { topics: [], at: 0, missed: [] }, seen = new Set<string>(); let hasTime = false;
      for (const name of this.nodes) {
        try {
          const list = await this.node(name, fresh, signal); out.at = hasTime ? Math.min(out.at, list.at) : list.at; hasTime = true; out.stale ??= list.stale;
          for (const topic of list.topics) if (!seen.has(topic.id)) { seen.add(topic.id); out.topics.push(topic); }
        } catch (error) { if (error instanceof Error && error.name === "AbortError") throw error; out.missed.push({ name, error: error instanceof ReadError ? error : new ReadError("network") }); }
      }
      out.topics.sort((a, b) => a.lastTouched < b.lastTouched ? 1 : a.lastTouched > b.lastTouched ? -1 : 0); return out;
    }
    if (source !== "hot" && source !== "latest") throw new ReadError("invalid");
    return this.topics(`/topics/${source}.json`, fresh, signal);
  }
  async thread(id: string, wantReplies = true, refresh = false, signal?: AbortSignal): Promise<Thread> {
    const fresh = refresh ? this.limits.refresh : this.limits.topic;
    let list: Listing;
    try { list = await this.topics(topicPath(id), fresh, signal); }
    catch (error) { if (error instanceof ReadError && error.code === "not_found") throw new ReadError("no_topic"); throw error; }
    if (!list.topics.length) throw new ReadError("no_topic");
    const out: Thread = { topic: list.topics[0], at: list.at, stale: list.stale, replies: [] };
    if (!wantReplies || !out.topic.replies) return out;
    try { const rd = await this.get(repliesPath(id), fresh, signal); out.replies = parseReplies(rd.body); out.at = Math.min(out.at, rd.at); out.stale ??= rd.stale; }
    catch (error) { if (error instanceof Error && error.name === "AbortError") throw error; out.repliesErr = error instanceof ReadError ? error : new ReadError("network"); }
    return out;
  }
}
