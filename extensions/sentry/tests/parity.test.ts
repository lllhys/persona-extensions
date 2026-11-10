import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { parseDSN } from "../src/dsn.ts";
import { eventID, event, envelope, platforms } from "../src/event.ts";
import { locales } from "../src/i18n.ts";
import { limitedUntil, send, bounds } from "../src/transport.ts";
import { SentryService } from "../src/service.ts";
import { sample, service, Fixture, deferred, until, origin, syntheticDSN } from "./fixtures.ts";
const opts = { environment: "staging", client: "persona-sentry/0.1.2" };
test("Go DSN cases, path prefix, project uint64, no credential in public errors", () => {
  const dsn = parseDSN(" https://<key>@o1.ingest.sentry.io/42/ ");
  assert.equal(dsn.endpoint, "https://o1.ingest.sentry.io/api/42/envelope/");
  assert.equal(dsn.project, "42"); assert.equal(dsn.publicKey, "<key>");
  assert.ok(!dsn.safe.includes("fixture-key"));
  assert.equal(parseDSN("https://fixture-key@errors.example.com/sentry/7").endpoint, origin + "/sentry/api/7/envelope/");
  for (const bad of ["", "http://fixture-key@errors.example.com/3", origin + "/1", "https://fixture-key@errors.example.com/x", "ftp://fixture-key@errors.example.com/3", "https://fixture-key@errors.example.com/18446744073709551616", "https://fixture%0akey@errors.example.com/3"]) {
    assert.throws(() => parseDSN(bad), error => !String(error).includes("fixture-key"));
  }
  assert.equal(parseDSN("http://fixture-key@errors.example.com:9000/3", true).endpoint, "http://errors.example.com:9000/api/3/envelope/");
  assert.equal(parseDSN("https://fixture-key@errors.example.com/18446744073709551615").project, "18446744073709551615");
});
test("original DSN/config needs no extra destination setting; enforcement belongs to the broker", async () => {
  const app = new SentryService({ fetch: new Fixture().fetch });
  app.configure({}, syntheticDSN); assert.equal(app.state().phase, "running");
  await app.stop();
});
test("Go event parity: deterministic ID, cause order, frames, release, tags, contexts, breadcrumbs", async () => {
  const r = sample();
  const e = await event(r, opts);
  assert.equal(e.event_id, createHash("sha256").update("android\0r1").digest("hex").slice(0, 32));
  assert.equal(e.event_id, await eventID(r)); assert.equal(e.platform, "java"); assert.equal(e.level, "fatal");
  assert.equal(e.release, "persona-android@1.4.0+512"); assert.equal(e.dist, "512"); assert.equal(e.environment, "staging");
  assert.deepEqual(e.fingerprint, ["fp1"]); assert.deepEqual(e.extra, { count: 3, previous_launch: true, received_at: r.received_at });
  assert.deepEqual(e.tags, { screen: "inbox", source: "android", kind: "crash", previous_launch: "true" });
  assert.equal(e.exception.values[0].type, "java.lang.IllegalStateException");
  assert.deepEqual(e.exception.values[0].stacktrace.frames[0], { function: "load", module: "persona.Inbox", filename: "Inbox.kt", lineno: 88, colno: 4, in_app: true });
  assert.equal(e.exception.values[0].mechanism.handled, false);
  assert.equal(e.breadcrumbs.values[0].timestamp, Date.parse(r.breadcrumbs![0].time!) / 1000);
  assert.deepEqual(e.contexts, { os: { name: "android", version: "16" }, device: { model: "Synthetic phone" } });
  assert.equal(r.exceptions![0].type, "java.lang.RuntimeException", "input was not reversed in place");
});
test("all platforms infer no IP; omission/default fields and handled exceptions", async () => {
  for (const [source, platform] of Object.entries(platforms)) {
    const r = { ...sample(), source } as ReturnType<typeof sample>;
    const e = await event(r, opts);
    assert.equal(e.platform, platform); assert.equal(e.sdk.settings.infer_ip, "never");
    assert.ok(!("user" in e)); assert.ok(!("server_name" in e));
  }
  const r = { ...sample(), source: "hub", kind: "exception", release: "", build: "", fingerprint: "", message: "", os: "", device_model: "", exceptions: [{ type: "SyntheticError" }], breadcrumbs: [{ category: "fixture", time: "invalid" }] } as ReturnType<typeof sample>;
  const e = await event(r, opts);
  for (const field of ["release", "dist", "fingerprint", "message", "contexts"]) assert.equal(e[field], undefined);
  assert.equal(e.exception.values[0].mechanism.handled, true); assert.equal(e.exception.values[0].stacktrace, undefined);
  assert.equal(e.breadcrumbs.values[0].timestamp, undefined);
});
test("hub-scrubbed data stays scrubbed; arbitrary incoming identity fields are not forwarded", async () => {
  const r = { ...sample(), message: "token=[redacted] account=[redacted]", user: { email: "me@example.com" }, server_name: "synthetic-host", ip_address: "192.0.2.1" };
  const raw = JSON.stringify(await event(r, opts));
  assert.ok(raw.includes("[redacted]"));
  for (const forbidden of ["me@example.com", "synthetic-host", "192.0.2.1", "user", "server_name"]) assert.ok(!raw.includes(forbidden));
});
test("Unicode envelope has exactly three UTF-8 lines and a credential-free header", async () => {
  const r = { ...sample(), message: "Synthetic Unicode: 日本語 中文 🧪" };
  const dsn = parseDSN(syntheticDSN);
  const bytes = envelope(dsn, await event(r, opts), Date.parse(r.time));
  const lines = new TextDecoder().decode(bytes).trimEnd().split("\n");
  assert.equal(lines.length, 3);
  const header = JSON.parse(lines[0]), item = JSON.parse(lines[1]);
  assert.equal(header.event_id, await eventID(r)); assert.equal(header.dsn, origin + "/42");
  assert.equal(header.sent_at, r.time); assert.equal(item.length, Buffer.byteLength(lines[2]));
  assert.equal(item.type, "event"); assert.equal(item.content_type, "application/json");
  assert.ok(!lines[0].includes("fixture-key"));
});
test("rate limits longest error/default/all category and Retry-After fallback/date", () => {
  const now = Date.parse(sample().time);
  const limit = (status: number, values: Record<string, string>) => limitedUntil(status, new Headers(values), now);
  assert.equal(limit(200, { "X-Sentry-Rate-Limits": "60:transaction:key,120:error;default:organization,90::key" }), now + 120_000);
  assert.equal(limit(200, { "X-Sentry-Rate-Limits": "60:transaction:key,80:errors:key" }), undefined);
  assert.equal(limit(200, { "X-Sentry-Rate-Limits": "45:default:key" }), now + 45_000);
  assert.equal(limit(200, { "X-Sentry-Rate-Limits": "5" }), now + 5000);
  assert.equal(limit(429, { "Retry-After": "30" }), now + 30_000);
  for (const retry of ["0", "bad", "-1"]) assert.equal(limit(429, { "Retry-After": retry }), now + 60_000);
  assert.equal(limit(429, { "Retry-After": new Date(now + 90_000).toUTCString() }), now + 60_000);
  assert.equal(limit(429, { "X-Sentry-Rate-Limits": "not-a-number:error", "Retry-After": "20" }), now + 20_000);
});
test("signed Go duration overflow and Retry-After integer rules are preserved at millisecond precision", () => {
  const now = Date.parse(sample().time);
  assert.equal(limitedUntil(200, new Headers({ "X-Sentry-Rate-Limits": "10000000000:error" }), now), now - 8446744073710);
  assert.equal(limitedUntil(429, new Headers({ "Retry-After": "10000000000" }), now), now - 8446744073710);
  assert.equal(limitedUntil(429, new Headers({ "Retry-After": "+2" }), now), now + 2000);
  assert.equal(limitedUntil(200, new Headers({ "X-Sentry-Rate-Limits": "9223372036854775808:error" }), now), undefined);
  assert.equal(limitedUntil(429, new Headers({ "Retry-After": "9223372036854775808" }), now), now + 60000);
});
test("forward uses auth/content type, configured endpoint, no redirects and safe logs", async () => {
  const fixture = new Fixture(), { app, logs } = service(fixture, { environment: "staging", include_log_lines: false });
  app.deliver([sample(), { ...sample("log"), kind: "log" }]);
  assert.equal(await app.flush(1000), true); assert.equal(fixture.hits.length, 1);
  assert.equal(fixture.hits[0].url, origin + "/api/42/envelope/"); assert.equal(fixture.hits[0].redirect, "error");
  assert.equal(fixture.hits[0].headers.get("Content-Type"), "application/x-sentry-envelope");
  assert.equal(fixture.hits[0].headers.get("X-Sentry-Auth"), "Sentry sentry_version=7, sentry_client=persona-sentry/0.1.2, sentry_key=fixture-key");
  assert.ok(fixture.hits[0].body.includes('"environment":"staging"'));
  assert.equal(app.state().sent, 1); assert.equal(app.state().dropped, 0);
  assert.ok(!logs.join().includes("fixture-key")); await app.stop();
});
test("missing DSN never starts, all samples 0/1/fraction and clamping, invalid config", async () => {
  const fixture = new Fixture(), noDSN = new SentryService({ fetch: fixture.fetch });
  noDSN.configure({}, ""); noDSN.deliver([sample()]); await noDSN.flush();
  assert.equal(fixture.hits.length, 0); assert.equal(noDSN.state().phase, "unconfigured"); await noDSN.stop();
  for (const [rate, random, sent] of [[0, 0, 0], [1, 0.999, 1], [0.5, 0.25, 1], [0.5, 0.5, 0], [-1, 0, 0], [2, 0.99, 1]]) {
    const f = new Fixture(), { app } = service(f, { sample_rate: rate }, { random: () => random });
    app.deliver([sample()]); await app.flush(); assert.equal(f.hits.length, sent); await app.stop();
  }
  for (const cfg of [[], { sample_rate: "1" }, { allow_http: "yes" }, { include_log_lines: 1 }]) {
    const app = new SentryService({ fetch: fixture.fetch }); assert.throws(() => app.configure(cfg, syntheticDSN)); await app.stop();
  }
});
test("queue most 200 plus in-flight; overflow counted; minute reset at exact boundary", async () => {
  const fixture = new Fixture(); fixture.gate = deferred<Response>();
  const { app } = service(fixture);
  app.deliver([sample("first")]); await until(() => fixture.hits.length === 1);
  app.deliver(Array.from({ length: 205 }, (_, i) => sample(String(i))));
  assert.equal(app.state().queued, bounds.queue); assert.equal(app.state().dropped, 5);
  fixture.gate.resolve(new Response("ok")); fixture.gate = undefined;
  assert.equal(await app.flush(1000), true); assert.equal(app.state().sent, 60); assert.equal(app.state().dropped, 146);
  fixture.now += 59_999; app.deliver([sample("still-limited")]); await app.flush(); assert.equal(app.state().sent, 60);
  fixture.now++; app.deliver([sample("new-minute")]); await app.flush(); assert.equal(app.state().sent, 61); await app.stop();
});
test("Sentry 429 pauses and drops, resumes at boundary; successful limit header also drops", async () => {
  const f = new Fixture(); f.status = 429; f.headers.set("Retry-After", "30"); const { app } = service(f);
  app.deliver([sample(), sample("r2")]); await app.flush();
  assert.equal(f.hits.length, 1); assert.equal(app.state().dropped, 2); assert.equal(app.state().pausedUntil, f.now + 30_000);
  f.now += 30_000; f.status = 200; f.headers = new Headers({ "X-Sentry-Rate-Limits": "120:error:key" });
  app.deliver([sample("r3")]); await app.flush(); assert.equal(app.state().dropped, 3); assert.equal(app.state().sent, 0);
  f.now += 120_000; f.headers = new Headers(); app.deliver([sample("r4")]); await app.flush(); assert.equal(app.state().sent, 1); await app.stop();
});
test("offline and non-2xx are dropped without retries or secret-bearing error logs", async () => {
  for (const status of [400, 401, 500, 302]) {
    const f = new Fixture(); f.status = status; const { app, logs } = service(f);
    app.deliver([sample()]); await app.flush(); assert.equal(app.state().dropped, 1); assert.equal(app.state().sent, 0);
    assert.equal(f.hits.length, 1); assert.equal(logs.length, 0); await app.stop();
  }
  const f = new Fixture(); f.offline = true; const { app, logs } = service(f); app.deliver([sample()]); await app.flush();
  assert.equal(app.state().dropped, 1); assert.equal(f.hits.length, 1); assert.equal(logs.length, 0); await app.stop();
});
test("flush/stop cancellation are bounded and idempotent; no late callback changes counts", async () => {
  const f = new Fixture(); f.gate = deferred<Response>(); const { app } = service(f, {}, { timeout: 200 });
  app.deliver([sample(), sample("queued")]); await until(() => f.hits.length === 1);
  assert.equal(await app.flush(5), false); assert.equal(f.hits[0].signal?.aborted, false);
  const stopped = app.stop(0); assert.equal(app.stop(0), stopped); app.deliver([sample("after-stop")]);
  assert.equal(await stopped, false); assert.equal(app.state().phase, "stopped"); assert.equal(app.state().dropped, 2);
  assert.equal(f.hits[0].signal?.aborted, true);
  f.gate.resolve(new Response("late receipt")); await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(app.state().sent, 0); assert.equal(app.state().dropped, 2); assert.equal(f.hits.length, 1);
});
test("graceful stop drains accepted queue; external signal cancels immediately", async () => {
  const f = new Fixture(), { app } = service(f); app.deliver([sample()]); assert.equal(await app.stop(1000), true);
  assert.equal(app.state().sent, 1); assert.equal(app.state().phase, "stopped");
  const g = new Fixture(); g.gate = deferred<Response>(); const cancel = new AbortController(); const stopped = service(g, {}, { signal: cancel.signal }).app;
  stopped.deliver([sample()]); await until(() => g.hits.length === 1); cancel.abort();
  await until(() => stopped.state().phase === "stopped"); assert.equal(stopped.state().dropped, 1); assert.equal(g.hits[0].signal?.aborted, true);
});
test("deadline bounds ignored fetch abort; oversized envelope never fetches", async () => {
  const f = new Fixture(); f.gate = deferred<Response>(); const { app } = service(f, {}, { timeout: 5 });
  app.deliver([sample()]); assert.equal(await app.flush(1000), true); assert.equal(app.state().dropped, 1); await app.stop();
  const result = await send(f.fetch, parseDSN(syntheticDSN), opts.client, new Uint8Array(bounds.body + 1), f.now, new AbortController().signal);
  assert.equal(result.status, "failed"); assert.equal(f.hits.length, 1);
});
test("response drain stops after 4 KiB and cancels stream; stalled body meets deadline", async () => {
  let pulls = 0, cancelled = false;
  const fetch = async () => new Response(new ReadableStream<Uint8Array>({ pull(controller) { pulls++; controller.enqueue(new Uint8Array(4096)); }, cancel() { cancelled = true; } }));
  const bytes = envelope(parseDSN(syntheticDSN), await event(sample(), opts), Date.now());
  assert.equal((await send(fetch, parseDSN(syntheticDSN), opts.client, bytes, Date.now(), new AbortController().signal)).status, "sent");
  assert.equal(cancelled, true); assert.ok(pulls <= 2);
  const stalled = async () => new Response(new ReadableStream<Uint8Array>({}));
  assert.equal((await send(stalled, parseDSN(syntheticDSN), opts.client, bytes, Date.now(), new AbortController().signal, 5)).status, "failed");
  const broken = async () => new Response(new ReadableStream<Uint8Array>({ start(controller) { controller.error(new Error("synthetic response text failure")); } }));
  assert.equal((await send(broken, parseDSN(syntheticDSN), opts.client, bytes, Date.now(), new AbortController().signal)).status, "sent");
});
test("HTTP DSN needs only the original explicit allow_http; grants remain a broker gate", async () => {
  const f = new Fixture(); const app = new SentryService({ fetch: f.fetch });
  const dsn = "http://fixture-key@errors.example.com:9000/42";
  assert.throws(() => app.configure({}, dsn));
  app.configure({ allow_http: true }, dsn); await app.stop();
});
test("all locale resources have identical nonempty keys", () => {
  const keys = Object.keys(locales.en).sort();
  for (const resource of Object.values(locales)) { assert.deepEqual(Object.keys(resource).sort(), keys); assert.ok(Object.values(resource).every(value => value.trim())); }
});
