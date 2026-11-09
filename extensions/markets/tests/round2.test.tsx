import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "@persona/extension/ui";
import { HTTP, ReadError } from "../src/http.ts";
import { Markets } from "../src/service.ts";
import { root } from "../src/root.tsx";
import { loadPage, page, readPageState, fingerprint } from "../src/page.tsx";
import { int64, numericJSON, jsonInteger } from "../src/integers.ts";
import { stamp, localTime } from "../src/model.ts";
import { displayZone } from "../src/time.ts";
import { parseStateJSON, decodeState } from "../src/state.ts";
import { FakeHub, Fixture, deferred, until, okxTicker } from "./fixtures.ts";
const invalid = (err: unknown) => err instanceof ReadError && err.code === "invalid";
const hits = (fixture: Fixture) => [...fixture.hits.values()].reduce((sum, n) => sum + n, 0);
async function setup() {
  const hub = new FakeHub(), fixture = new Fixture();
  const service = new Markets(hub, {}, { fetch: fixture.fetch, clock: () => fixture.at, interval: () => () => {} });
  await service.initialize(); return { service, fixture, hub };
}
for (const bad of [null, { instId: "ETH-USDT", last: 100 }, { instId: "ETH-USDT", ts: true }, { instId: "ETH-USDT", vol24h: [] }]) test(`OKX rejects wrong-type ticker ${JSON.stringify(bad)} before state/health/cache success and immediately recovers`, async () => {
  const { service, fixture, hub } = await setup();
  try {
    fixture.override = url => url.pathname.endsWith("ticker") ? new Response(JSON.stringify({ code: "0", data: [bad] })) : undefined;
    const before = hub.data;
    await assert.rejects(service.addWatch({ source: "okx", symbol: "ETH-USDT" }), invalid);
    assert.equal(hub.data, before); assert.equal(service.status("en").state, "error"); assert.equal(service.http.cache.entries.size, 0);
    const count = hits(fixture); fixture.override = undefined;
    await service.addWatch({ source: "okx", symbol: "ETH-USDT" }); assert.equal(hits(fixture), count + 1);
    assert.equal(service.status("en").state, "ok");
    // A forced bad refresh must leave the previous valid cached body untouched.
    const good = [...service.http.cache.entries.values()][0].value;
    fixture.override = () => new Response(JSON.stringify({ code: "0", data: [bad] }));
    await assert.rejects(service.quote({ source: "okx", symbol: "ETH-USDT" }, 0), invalid);
    assert.equal([...service.http.cache.entries.values()][0].value, good);
    assert.equal((await service.sources.okx.quote("ETH-USDT", 10_000)).last, 100);
  } finally { service.dispose(); }
});
test("OKX batch/search reject null and wrong-type records even when the broken record is not requested", async () => {
  const { service, fixture } = await setup();
  try {
    fixture.override = () => new Response(JSON.stringify({ code: "0", data: [okxTicker("BTC-USDT"), { instId: "UNRELATED-USDT", last: true }] }));
    await assert.rejects(service.sources.okx.quotes(["BTC-USDT", "ETH-USDT", "A-USDT", "B-USDT", "C-USDT", "D-USDT"], 10_000), invalid);
    await assert.rejects(service.sources.okx.search("BTC", 10, 10_000), invalid);
    assert.equal(service.http.cache.entries.size, 0);
  } finally { service.dispose(); }
});
for (const bar of ["1D", "T"]) test(`Tencent ${bar} validates nested shapes and code lexemes before cache admission`, async () => {
  const { service, fixture } = await setup(); const code = "sh600000";
  const shapes = bar === "T" ? [1, [], { data: 1 }, { data: { date: 123, data: [] } }, { data: { date: "20261106", data: "broken" } }, { data: { date: "20261106", data: [123] } }]
    : [1, [], { qfqday: "broken" }, { qfqday: [1] }, { qfqday: [], day: "ignored" }];
  try {
    // A valid qfq field takes precedence even when the unadjusted field is unusable.
    if (bar === "1D") shapes.pop();
    for (const shape of shapes) {
      fixture.override = () => new Response(JSON.stringify({ code: 0, data: { [code]: shape } }));
      await assert.rejects(service.candles({ source: "cn", symbol: "600000.SH" }, bar, 60), invalid);
      assert.equal(service.status("en").state, "error"); assert.equal(service.http.cache.entries.size, 0);
      const before = hits(fixture); fixture.override = undefined;
      assert.ok((await service.candles({ source: "cn", symbol: "600000.SH" }, bar, 60)).length);
      assert.equal(hits(fixture), before + 1);
      const good = [...service.http.cache.entries.values()][0].value;
      fixture.override = () => new Response(JSON.stringify({ code: 0, data: { [code]: shape } }));
      await assert.rejects(service.sources.cn.candles("600000.SH", bar, 60, 0), invalid);
      assert.equal([...service.http.cache.entries.values()][0].value, good);
      service.http.cache.entries.clear();
    }
    for (const token of ["0.0", "0e0", "9223372036854775808", '{"$marketNumber":"0"}']) {
      fixture.override = () => new Response(`{"code":${token},"data":{"sh600000":{}}}`);
      await assert.rejects(service.sources.cn.candles("600000.SH", bar, 60, 30_000), invalid);
      assert.equal(service.http.cache.entries.size, 0);
    }
  } finally { service.dispose(); }
});
test("raw numeric JSON validates syntax first, retains exact int64 lexemes and cannot mistake user objects for numbers", () => {
  for (const text of ['{"code":01}', '{"code":+0}', '{"code":.0}', '{"code":0,}', '{"code":NaN}', '{"code":0} trailing']) assert.throws(() => numericJSON(text));
  for (const token of ["9223372036854775807", "-9223372036854775808", "9007199254740993"]) assert.equal(jsonInteger((numericJSON(`{"code":${token}}`) as any).code), BigInt(token));
  assert.equal(jsonInteger((numericJSON('{"code":{"$marketNumber":"0"}}') as any).code), undefined);
  assert.equal(jsonInteger((numericJSON('{"code":0,"code":0.0}') as any).code), undefined);
  assert.equal(jsonInteger((numericJSON('{"code":0.0,"code":0}') as any).code), 0n);
  assert.equal(int64("+0001"), 1n); assert.equal(int64("1e3"), undefined); assert.equal(int64("9223372036854775808"), undefined);
});
test("raw state rejects invalid JSON and non-integer decimal lexemes before a converter can normalize them", () => {
  for (const token of ["2.0", "2e0", "9223372036854775808"]) assert.throws(() => parseStateJSON(`[{"decimals":${token}}]`));
  for (const text of ['[{"decimals":+2}]', '[{"decimals":02}]', '[{"decimals":2,}]']) assert.throws(() => parseStateJSON(text));
  const raw = '{"schema_version":1,"watchlist":[],"alerts":[{"id":"fixture-raw","ref":"BTC","kind":"above","value":120,"created":"2026-11-01T00:00:00Z","decimals":2}]}';
  assert.equal(decodeState(Buffer.from(raw).toString("base64")).alerts[0].decimals, 2);
  assert.throws(() => decodeState(Buffer.from(raw.replace('"decimals":2', '"decimals":2.0')).toString("base64")));
});
test("OKX timestamp strings retain full int64 values and skip only Go ParseInt-invalid candle records", async () => {
  const { service, fixture } = await setup();
  try {
    const tokens = ["1e3", "1.0", " 1000", "9223372036854775808", "9223372036854775807", "-9223372036854775808", "9007199254740993", "+0001000"];
    fixture.override = url => new Response(JSON.stringify({ code: "0", data: url.pathname.endsWith("candles") ? tokens.map(at => [at, "1", "2", "1", "2", "3"]) : [{ ...okxTicker("BTC-USDT"), ts: "9223372036854775807" }] }));
    const q = await service.sources.okx.quote("BTC-USDT", 0); assert.equal(q.at, 9223372036854775807n);
    const candles = await service.sources.okx.candles("BTC-USDT", "1H", 60, 0);
    assert.deepEqual(candles.map(c => c.at), [1000, 9007199254740993n, -9223372036854775808n, 9223372036854775807n]);
    const loaded = await loadPage(service, readPageState({ i: "BTC-USDT", b: "1H" }, service), true, false);
    assert.equal(loaded.kind, "instrument"); assert.doesNotThrow(() => fingerprint(loaded));
    const renderer = new Renderer({ authorize: () => true });
    try { renderer.mount(page({ service, state: readPageState({ i: "BTC-USDT", b: "1H" }, service), loaded, tz: "UTC", action: id => ({ id }) })); assert.ok(renderer.current().view.sections.length); }
    finally { renderer.dispose(); }
  } finally { service.dispose(); }
});
test("full-range formatter matches original Go time.UnixMilli/Format oracle including negative years and future DST", () => {
  const cases: Array<[bigint, string, string]> = [
    [9223372036854775807n, "UTC", "292278994-08-17 07:12"],
    [-9223372036854775808n, "UTC", "-292275055-05-16 16:47"],
    [9007199254740993n, "UTC", "287396-10-12 08:59"],
    [9223372036854775807n, "America/New_York", "292278994-08-17 03:12"],
    [-9223372036854775808n, "America/New_York", "-292275055-05-16 11:51"],
    [9223372036854775807n, "Asia/Hong_Kong", "292278994-08-17 15:12"],
    [-9223372036854775808n, "Asia/Hong_Kong", "-292275055-05-17 00:23"],
    [-9223372036854775808n, "Asia/Shanghai", "-292275055-05-17 00:52"],
  ];
  for (const [at, zone, expected] of cases) assert.equal(stamp(at, zone), expected);
  assert.equal(stamp(8640000000000000n, "Asia/Hong_Kong"), "275760-09-13 08:00");
  assert.equal(int64("1000\n"), undefined);
  for (const date of ["20260230", "2026-04-31", "2026-11-08 24:00", "2026-03-08 02:30"]) assert.equal(localTime(date, "America/New_York"), 0);
  assert.ok(localTime("0000-02-29", "UTC")); assert.ok(localTime("0099-01-01", "UTC"));
});
test("Local and missing old-hub timezone use local fallback instead of silently selecting UTC", () => {
  const old = process.env.TZ; process.env.TZ = "America/New_York";
  try { const at = Date.UTC(2026, 10, 8, 12); assert.equal(stamp(at, "Local"), "2026-11-08 07:00"); assert.equal(stamp(at, ""), "2026-11-08 07:00"); assert.equal(displayZone("Local", at), "UTC-05:00"); }
  finally { if (old === undefined) delete process.env.TZ; else process.env.TZ = old; }
});
test("initial load cancellation is silent on suspension and disposal, aborts transport and admits no late cache", async () => {
  for (const dispose of [false, true]) {
    const { service, fixture } = await setup(); const started = deferred<void>(), errors: unknown[] = []; let aborted = false;
    fixture.override = (_url, init) => new Promise((_yes, no) => { started.resolve(); init!.signal!.addEventListener("abort", () => { aborted = true; no(init!.signal!.reason); }, { once: true }); });
    const renderer = new Renderer({ authorize: () => true, onError: error => errors.push(error) });
    try { renderer.mount(root({ service })); await started.promise; if (dispose) renderer.dispose(); else renderer.suspend(); await until(() => aborted, Boolean); await new Promise(yes => setTimeout(yes, 5)); assert.deepEqual(errors, []); assert.equal(service.http.cache.entries.size, 0); }
    finally { renderer.dispose(); service.dispose(); }
  }
});
test("cache body limits apply on direct and queued hits without destroying a good larger response", async () => {
  let calls = 0; const url = "https://www.okx.com/fixture", gate = deferred<void>();
  const http = new HTTP(async () => { calls++; return new Response("12345678"); }, () => 100);
  await http.get(url, 1000, 8); await assert.rejects(http.get(url, 1000, 4), invalid);
  assert.equal(calls, 1); assert.equal((await http.get(url, 1000, 8)).byteLength, 8);
  http.cache.entries.clear();
  const blockers = Array.from({ length: 4 }, () => http.queue.run(() => gate.promise));
  const pending = http.get(url, 1000, 4); http.cache.put(url, new TextEncoder().encode("12345678")); gate.resolve();
  await Promise.all(blockers); await assert.rejects(pending, invalid); assert.equal(calls, 1);
  for (const limit of [-1, NaN, Infinity, 1.5]) await assert.rejects(http.get(url, 1000, limit), invalid);
});
