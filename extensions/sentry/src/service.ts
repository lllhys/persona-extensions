import { JSONIntegerError } from "@persona/extension";
import { decodeConfig, type Report } from "./params.ts";
import type { FetchAdapter } from "@persona/extension/ui";
import { parseDSN, type DSN } from "./dsn.ts";
import { event, envelope, type Options } from "./event.ts";
import { publicError } from "./i18n.ts";
import { bounds, send } from "./transport.ts";
export interface Config { environment?: string; sample_rate?: number | null; include_log_lines?: boolean | null; allow_http?: boolean }
export interface Dependencies {
  fetch: FetchAdapter;
  now?: () => number;
  random?: () => number;
  log?: (message: string) => void;
  signal?: AbortSignal;
  timeout?: number;
}
export interface State { phase: "unconfigured" | "running" | "stopping" | "stopped"; queued: number; inFlight: boolean; sent: number; dropped: number; pausedUntil: number }
/** One in-memory service owned by the Program, independent of UI visibility.
 * Failed/offline events are dropped, not retried or persisted, matching Go.
 * flush/stop are local controls for embedding/tests, not invented host RPCs. */
export class SentryService {
  private dsn?: DSN;
  private options: Options;
  private sample = 1;
  private logs = true;
  private queue: Report[] = [];
  private running?: Promise<void>;
  private generation = 0;
  private processing = false;
  private minute?: number;
  private inMinute = 0;
  private sent = 0;
  private dropped = 0;
  private pausedUntil = 0;
  private phase: State["phase"] = "unconfigured";
  private cancellation = new AbortController();
  private stopWork?: Promise<boolean>;
  private readonly now: () => number;
  private readonly random: () => number;
  private readonly onAbort = () => {
    // External cancellation supersedes an already-running graceful drain.
    this.cancelPending();
    void this.stop(0).catch(() => {});
  };
  constructor(private deps: Dependencies, version = "0.1.2") {
    this.now = deps.now ?? Date.now;
    this.random = deps.random ?? Math.random;
    this.options = { environment: "production", client: `persona-sentry/${version}` };
    deps.signal?.addEventListener("abort", this.onAbort, { once: true });
    if (deps.signal?.aborted) this.onAbort();
  }
  configure(config: unknown, rawDSN: string, locale?: string): void {
    if (this.phase === "stopping" || this.phase === "stopped") throw publicError("invalidConfig", locale);
    let cfg: Config;
    try { cfg = decodeConfig(config) as Config; }
    catch (error) {
      if (error instanceof JSONIntegerError) throw publicError("invalidConfig", locale);
      throw error;
    }
    if (!rawDSN) return;
    const dsn = parseDSN(rawDSN, cfg.allow_http, locale);
    this.dsn = dsn;
    this.options = { ...this.options, environment: cfg.environment || "production" };
    this.sample = Math.min(1, Math.max(0, cfg.sample_rate ?? 1));
    this.logs = cfg.include_log_lines ?? true;
    this.phase = "running";
    // Deliberately do not log destination, keys, credentials, event content or native errors.
  }
  deliver(reports: Report[]): void {
    if (this.phase !== "running" || !this.dsn) return;
    for (const report of reports) {
      if (report.kind === "log" && !this.logs || this.sample < 1 && !(this.random() < this.sample)) continue;
      if (this.queue.length >= bounds.queue) { this.dropped++; continue; }
      this.queue.push(structuredClone(report));
    }
    this.kick();
  }
  state(): State { return { phase: this.phase, queued: this.queue.length, inFlight: !!this.running, sent: this.sent, dropped: this.dropped, pausedUntil: this.pausedUntil }; }
  /** A timeout does not cancel delivery; stop performs cancellation explicitly. */
  async flush(timeout: number = bounds.timeout): Promise<boolean> {
    const deadline = Date.now() + Math.max(0, timeout);
    while (this.running || this.queue.length) {
      this.kick();
      const remaining = deadline - Date.now();
      if (remaining <= 0) return false;
      let timer: ReturnType<typeof setTimeout> | undefined;
      let interrupted!: () => void;
      const signal = this.cancellation.signal;
      try {
        const finished = await Promise.race([this.running!.then(() => true), new Promise<boolean>(resolve => {
          interrupted = () => resolve(false);
          signal.addEventListener("abort", interrupted, { once: true });
          if (signal.aborted) interrupted();
          timer = setTimeout(interrupted, remaining);
        })]);
        if (!finished) return false;
      } finally {
        clearTimeout(timer);
        signal.removeEventListener("abort", interrupted);
      }
    }
    return true;
  }
  stop(timeout: number = bounds.timeout): Promise<boolean> {
    if (this.stopWork) return this.stopWork;
    this.phase = "stopping";
    this.stopWork = (async () => {
      const drained = await this.flush(timeout) && !this.cancellation.signal.aborted;
      if (!drained) this.cancelPending();
      this.phase = "stopped";
      this.dsn = undefined;
      this.deps.signal?.removeEventListener("abort", this.onAbort);
      return drained;
    })();
    return this.stopWork;
  }
  private cancelPending(): void {
    if (this.cancellation.signal.aborted) return;
    // WebCrypto cannot cancel a digest. Retire its generation instead of waiting
    // past stop's deadline, accounting accepted work once before detaching it.
    this.generation++;
    this.dropped += this.queue.length + (this.processing ? 1 : 0);
    this.queue = [];
    this.processing = false;
    this.running = undefined;
    this.cancellation.abort();
  }
  private kick(): void {
    if (this.running || !this.queue.length || this.phase === "stopped" || this.cancellation.signal.aborted) return;
    const generation = this.generation;
    const current = () => generation === this.generation;
    this.running = Promise.resolve().then(async () => {
      while (current() && this.queue.length && !this.cancellation.signal.aborted) {
        const report = this.queue.shift()!;
        const now = this.now();
        if (this.minute === undefined || now - this.minute >= 60_000) { this.minute = now; this.inMinute = 0; }
        if (this.inMinute >= bounds.perMinute || now < this.pausedUntil) { this.dropped++; continue; }
        this.inMinute++;
        this.processing = true;
        try {
          // Go snapshots these before conversion. Reconfiguration during the
          // asynchronous digest must not mix one event with another destination.
          const dsn = this.dsn!, options = { ...this.options };
          const value = await event(report, options);
          if (!current()) return;
          const result = await send(this.deps.fetch, dsn, options.client, envelope(dsn, value, this.now()),
            this.now(), this.cancellation.signal, this.deps.timeout);
          if (!current()) return;
          this.processing = false;
          if (result.status === "sent") { this.sent++; this.log("sentry: sent event"); }
          else { this.dropped++; if (result.status === "limited") this.pausedUntil = result.until; }
        } catch { if (current()) this.dropped++; }
        finally { if (current()) this.processing = false; }
      }
    }).finally(() => {
      // A detached digest must not clear or kick another processing generation.
      if (current()) { this.running = undefined; this.kick(); }
    }).catch(() => {});
  }
  private log(message: string): void { try { this.deps.log?.(message); } catch {} }
}
