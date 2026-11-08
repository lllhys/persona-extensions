import { test } from "node:test";
import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { connect } from "@persona/extension";
import { Fixture, deferred, origin, sample, syntheticDSN, until } from "./fixtures.ts";

async function bundle() { return import(pathToFileURL(resolve(".source-dist/sentry/bundle/main.mjs")).href); }
async function session(runtime: any, locale = "en") {
  const frames: any[] = [], conn = connect(runtime.program, line => frames.push(JSON.parse(line)));
  let next = 0;
  const raw = async (method: string, params: string) => {
    const id = String(++next);
    conn.line(`{"id":"${id}","method":"${method}","params":${params}}`);
    await conn.drain(); return frames.find(frame => frame.id === id);
  };
  const call = (method: string, params: unknown) => raw(method, JSON.stringify(params));
  await call("initialize", { protocol: 1, hub: { locale }, config: {}, secrets: { dsn: syntheticDSN } });
  return { raw, call, close: async () => { await runtime.service.stop(1000); await conn.shutdown(); } };
}

test("round2: actual default Program emits only a neutral stderr receipt, never secrets or report content", async () => {
  const imported = await bundle(), f = new Fixture(), logs: unknown[][] = [];
  const oldFetch = globalThis.fetch, oldError = console.error;
  globalThis.fetch = f.fetch as typeof globalThis.fetch; console.error = (...args) => { logs.push(args); };
  const conn = connect(imported.default, () => {});
  try {
    conn.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1, config: {}, secrets: { dsn: syntheticDSN } } }));
    await conn.drain();
    conn.line(JSON.stringify({ id: "send", method: "error_reports.deliver", params: { contribution: "sentry", reports: [sample()] } }));
    await conn.drain(); await until(() => f.hits.length === 1 && logs.length === 1);
    assert.deepEqual(logs, [["sentry: sent event"]]);
    f.offline = true;
    conn.line(JSON.stringify({ id: "fail", method: "error_reports.deliver", params: { contribution: "sentry", reports: [sample("offline")] } }));
    await conn.drain(); await until(() => f.hits.length === 2);
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.deepEqual(logs, [["sentry: sent event"]]);
  } finally { await conn.shutdown(); globalThis.fetch = oldFetch; console.error = oldError; }
});

test("round2: count rejects visible fractions, wrong types, overflow and unsafe rounded values", async () => {
  const imported = await bundle(), f = new Fixture(), h = await session(imported.createRuntime({ fetch: f.fetch }));
  try {
    for (const token of ["1.0", "1e0", "-1.0", "-1e0", "1.5", "1e100", "1e400", "\"1\"", "true", "{}", "9223372036854775808", "-9223372036854775809"]) {
      const report = JSON.stringify(sample()).replace('"count":3', `"count":${token}`);
      assert.equal((await h.raw("error_reports.deliver", `{"contribution":"sentry","reports":[${report}]}`)).error?.code, "invalid_params", token);
    }
    // These are the exactly representable subset only, NOT full Go int64/lexical parity.
    for (const count of [-Number.MAX_SAFE_INTEGER, -1, 0, 1, Number.MAX_SAFE_INTEGER]) {
      assert.equal((await h.call("error_reports.deliver", { contribution: "sentry", reports: [{ ...sample(String(count)), count }] })).error, undefined);
    }
    assert.equal(await h.raw("error_reports.deliver", '{"contribution":"sentry","reports":[{"count":01}]}'), undefined, "invalid JSON must not be repaired");
    await h.call("error_reports.deliver", { contribution: "sentry", reports: [] });
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal(f.hits.length, 5);
    assert.deepEqual(f.hits.map(hit => JSON.parse(hit.body.trimEnd().split("\n")[2]).extra.count), [-Number.MAX_SAFE_INTEGER, -1, 0, 1, Number.MAX_SAFE_INTEGER]);
  } finally { await h.close(); }
});

test("round2: configuration is snapshotted before asynchronous hashing, including endpoint and environment", async () => {
  const imported = await bundle(), started = deferred<void>(), resume = deferred<void>(), hits: { url: string; environment: string }[] = [];
  const oldDigest = crypto.subtle.digest.bind(crypto.subtle);
  crypto.subtle.digest = (async (...args: Parameters<typeof oldDigest>) => { started.resolve(); await resume.promise; return oldDigest(...args); }) as typeof crypto.subtle.digest;
  const runtime = imported.createRuntime({ fetch: async (input: any, init: RequestInit) => {
    hits.push({ url: String(input), environment: JSON.parse(new TextDecoder().decode(init.body as Uint8Array).trimEnd().split("\n")[2]).environment });
    return new Response("synthetic receipt");
  } });
  try {
    runtime.service.configure({ destination: origin, environment: "A" }, syntheticDSN);
    runtime.service.deliver([sample("a")]); await started.promise;
    runtime.service.configure({ destination: "https://o1.ingest.sentry.io", environment: "B" }, "https://<key>@o1.ingest.sentry.io/7");
    resume.resolve(); assert.equal(await runtime.service.flush(1000), true);
    runtime.service.deliver([sample("b")]); assert.equal(await runtime.service.flush(1000), true);
    assert.deepEqual(hits, [{ url: origin + "/api/42/envelope/", environment: "A" }, { url: "https://o1.ingest.sentry.io/api/7/envelope/", environment: "B" }]);
  } finally { resume.resolve(); crypto.subtle.digest = oldDigest; await runtime.service.stop(1000); }
});

test("round2: source map uses own keys through actual bundle delivery", async () => {
  const imported = await bundle(), f = new Fixture(), runtime = imported.createRuntime({ fetch: f.fetch }), h = await session(runtime);
  try {
    for (const source of ["__proto__", "constructor", "toString", "hasOwnProperty", "unknown"]) {
      assert.equal((await h.call("error_reports.deliver", { contribution: "sentry", reports: [{ ...sample(source), source }] })).error, undefined);
    }
    await runtime.service.flush(1000);
    assert.equal(f.hits.length, 5);
    for (const hit of f.hits) assert.equal(JSON.parse(hit.body.trimEnd().split("\n")[2]).platform, "");
  } finally { await h.close(); }
});

test("round2: breadcrumb calendar rejects invalid normalization; Go year/fraction/offset behavior stays supported", async () => {
  const imported = await bundle();
  const valid: [string, number][] = [
    ["2024-02-29T09:00:00Z", 1709197200], ["2000-02-29T00:00:00Z", 951782400],
    ["0000-01-01T00:00:00Z", -62167219200], ["9999-12-31T23:59:59.999999999Z", 253402300799.999],
    ["1969-12-31T23:59:59.999999999Z", -0.001], ["2026-01-01T1:00:00Z", 1767229200],
    ["2026-01-01T00:00:00,1234Z", 1767225600.123], ["2026-01-01T00:00:00+24:00", 1767139200],
    ["2026-01-01T00:00:00+00:60", 1767222000],
  ];
  const invalid = ["2026-02-30T09:00:00Z", "1900-02-29T00:00:00Z", "2026-04-31T00:00:00Z", "2026-00-01T00:00:00Z", "2026-01-00T00:00:00Z", "2026-01-01T24:00:00Z", "2026-01-01T00:60:00Z", "2026-01-01T00:00:60Z", "2026-01-01T00:00:00+25:00", "2026-01-01T00:00:00+00:61", "2026-01-01 00:00:00Z", "2026-01-01T00:00:00", "10000-01-01T00:00:00Z", "999999999999999999-01-01T00:00:00Z"];
  const report = { ...sample(), breadcrumbs: [...valid.map(([time]) => ({ category: "valid", time })), ...invalid.map(time => ({ category: "invalid", time }))] };
  const value = await imported.event(report, { environment: "production", client: "persona-sentry/0.1.2" });
  assert.deepEqual(value.breadcrumbs.values.slice(0, valid.length).map((crumb: any) => crumb.timestamp), valid.map(([, timestamp]) => timestamp));
  for (const crumb of value.breadcrumbs.values.slice(valid.length)) assert.equal(crumb.timestamp, undefined, crumb.time);
  // The report timestamp is an opaque wire string in Go too; do not silently zero it.
  const huge = "999999999999999999-01-01T00:00:00Z";
  assert.equal((await imported.event({ ...sample(), time: huge }, { environment: "production", client: "persona-sentry/0.1.2" })).timestamp, huge);
});

test("round2: inherited locales fall back to real English protocol errors", async () => {
  const imported = await bundle();
  for (const locale of ["constructor", "__proto__", "toString", "unknown"]) {
    const runtime = imported.createRuntime({ fetch: new Fixture().fetch }), frames: any[] = [], conn = connect(runtime.program, line => frames.push(JSON.parse(line)));
    try {
      conn.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1, hub: { locale }, secrets: { dsn: "invalid" }, config: {} } }));
      await conn.drain(); assert.equal(frames[0].error.code, "invalid_params");
      assert.equal(frames[0].error.message, "The DSN must be https://<public key>@<host>/<project number>.");
    } finally { await runtime.service.stop(1000); await conn.shutdown(); }
  }
});
