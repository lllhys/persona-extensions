import type { ErrorReport } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import { SentryService, type Config } from "../src/service.ts";
export const origin = "https://errors.example.com";
export const syntheticDSN = `${origin.replace("https://", "https://fixture-key@")}\/42`;
export function sample(id = "r1"): ErrorReport {
  return { id, source: "android", kind: "crash", level: "fatal", time: "2026-11-06T09:00:00Z",
    release: "1.4.0", build: "512", os: "android", os_version: "16", device_model: "Synthetic phone",
    message: "IllegalStateException: synthetic failure",
    exceptions: [
      { type: "java.lang.RuntimeException", value: "wrapped", frames: [{ function: "outer", module: "persona.App", in_app: true }] },
      { type: "java.lang.IllegalStateException", value: "synthetic failure", frames: [{ function: "load", module: "persona.Inbox", file: "Inbox.kt", line: 88, column: 4, in_app: true }] },
    ], breadcrumbs: [{ time: "2026-11-06T08:59:59Z", category: "navigation", message: "inbox" }],
    tags: { screen: "inbox", source: "spoofed", kind: "spoofed" }, previous_launch: true, fingerprint: "fp1", count: 3, received_at: "2026-11-06T09:01:00Z" };
}
export function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
export class Fixture {
  now = Date.parse("2026-11-06T09:00:00Z");
  hits: { url: string; body: string; headers: Headers; signal?: AbortSignal | null; redirect?: RequestRedirect }[] = [];
  status = 200;
  headers = new Headers();
  offline = false;
  gate?: ReturnType<typeof deferred<Response>>;
  readonly fetch: FetchAdapter = async (input, init) => {
    const url = String(input);
    if (new URL(url).origin !== origin) throw new Error("fixture denies unknown destination");
    const body = new TextDecoder().decode(init?.body as Uint8Array);
    this.hits.push({ url, body, headers: new Headers(init?.headers), signal: init?.signal, redirect: init?.redirect });
    if (this.offline) throw new Error("https://fixture-key@errors.example.com/42: synthetic network failure");
    return this.gate ? this.gate.promise : new Response("{\"id\":\"synthetic-receipt\"}", { status: this.status, headers: this.headers });
  };
}
export function service(fixture: Fixture, config: Config = {}, overrides: Partial<ConstructorParameters<typeof SentryService>[0]> = {}) {
  const logs: string[] = [];
  const app = new SentryService({ fetch: fixture.fetch, now: () => fixture.now, random: () => 0.25, log: message => logs.push(message), ...overrides });
  app.configure(config, syntheticDSN, "en");
  return { app, logs };
}
export async function until(predicate: () => boolean, timeout = 1000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error("synthetic fixture timed out");
    await new Promise(resolve => setTimeout(resolve, 1));
  }
}
