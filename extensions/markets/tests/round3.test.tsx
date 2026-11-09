import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, mkdtemp, writeFile, rm } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "typescript";
import { parseFloat64, floatResult } from "../src/floats.ts";
import { numericJSON, jsonInteger } from "../src/integers.ts";
import { typedJSON } from "../src/typed-json.ts";
import { parseStateJSON, decodeState, legacyState, encodeState } from "../src/state.ts";
import { FakeHub, Fixture } from "./fixtures.ts";
const extension = resolve(import.meta.dirname, "../..");
async function setup() {
  // Exercise the actual default artifact's service, not a source-only substitute.
  const bundle = await import(pathToFileURL(resolve(extension, "build/markets.mjs")).href);
  const fixture = new Fixture(), hub = new FakeHub();
  const service = new bundle.Markets(hub, {}, { fetch: fixture.fetch, clock: () => fixture.at, interval: () => () => {}, id: () => "fixture-round3" });
  await service.initialize(); return { bundle, fixture, hub, service };
}
const rejected = (error: any) => error.code === "invalid";
for (const raw of [
  '{"code":0.0,"code":0,"data":{"sh600000":{"qfqday":[]}}}',
  '{"code":0e0,"CODE":0,"data":{"sh600000":{"qfqday":[]}}}',
  '{"code":0,"msg":123,"MSG":"ok","data":{"sh600000":{"qfqday":[]}}}',
  '{"code":0,"data":{"sh600000":1,"sh600000":{"qfqday":[]}}}',
]) test(`Tencent rejects every earlier consumed type/lexeme error: ${raw}`, async () => {
  const { service, fixture } = await setup();
  try {
    fixture.override = () => new Response(raw);
    await assert.rejects(service.candles({ source: "cn", symbol: "600000.SH" }, "1D", 60), rejected);
    assert.equal(service.status("en").state, "error"); assert.equal(service.http.cache.entries.size, 0);
    fixture.override = undefined;
    assert.ok((await service.candles({ source: "cn", symbol: "600000.SH" }, "1D", 60)).length);
    assert.equal(service.status("en").state, "ok");
  } finally { service.dispose(); }
});
test("typed JSON retains valid duplicates, struct merges, map replacement and Go ASCII simple-fold matching", () => {
  const type = { fields: { code: "integer" as const, data: { fields: { date: "string" as const, name: "string" as const } }, key: "string" as const, symbols: "string" as const } };
  const decoded = typedJSON(numericJSON('{"code":1,"CODE":0,"data":{"date":"first"},"DATA":{"name":"second"},"Key":"kelvin","ſymbols":"long-s"}'), type);
  assert.equal(jsonInteger(decoded.code), 0n); assert.equal(decoded.data.date, "first"); assert.equal(decoded.data.name, "second"); assert.equal(decoded.key, "kelvin"); assert.equal(decoded.symbols, "long-s");
  assert.throws(() => typedJSON(numericJSON('{"code":0.0,"CODE":0}'), type));
  const map = typedJSON(numericJSON('{"data":{"x":{"date":"first"}},"DATA":{"y":{"date":"second"},"x":{"name":"replaced"}}}'), { fields: { data: { map: type.fields.data } } });
  assert.equal(map.data.x.date, undefined); assert.equal(map.data.x.name, "replaced"); assert.equal(map.data.y.date, "second");
});
for (const [field, bad, good] of [
  ["id", 123, "fixture-state"], ["ref", false, "BTC"], ["kind", [], "above"], ["value", "120", 120],
  ["created", "2026-02-30T00:00:00Z", "2026-02-01T00:00:00Z"], ["met", 123, "2026-02-01T00:00:00Z"],
  ["price", "100", 100], ["change", true, 0.1], ["name", 123, "Synthetic"], ["decimals", 2.5, 2],
] as const) test(`raw state validates every ${field} occurrence before normalization`, () => {
  const duplicate = `[{${JSON.stringify(field)}:${JSON.stringify(bad)},${JSON.stringify(field.toUpperCase())}:${JSON.stringify(good)}}]`;
  assert.throws(() => parseStateJSON(duplicate));
  assert.throws(() => decodeState(Buffer.from(`{"schema_version":1,"watchlist":[],"alerts":${duplicate}}`).toString("base64")));
});
test("bundled Tencent accepts valid duplicate codes and actual minute struct merges with case variants", async () => {
  const { service, fixture } = await setup();
  try {
    fixture.override = () => new Response('{"code":1,"CODE":0,"data":{"sh600000":{"data":{"date":"20261106"},"DATA":{"data":["0930 100 10"]}}}}');
    const rows = await service.sources.cn.candles("600000.SH", "T", 60, 0);
    assert.equal(rows.length, 1); assert.equal(rows[0].open, 100);
  } finally { service.dispose(); }
});
test("raw state met pointer null clears a previous timestamp while string null keeps its prior value", () => {
  const decoded = parseStateJSON('[{"met":"2026-02-01T00:00:00Z","MET":null,"name":"Synthetic","NAME":null}]') as any[];
  assert.equal(decoded[0].met, undefined); assert.equal(decoded[0].name, "Synthetic");
});
test("raw state rejects invalid integer lexemes in both duplicate orders and repeated state containers", () => {
  for (const token of ["2.0", "2e0", "9223372036854775808"]) {
    for (const pair of [`"decimals":${token},"decimals":2`, `"decimals":2,"decimals":${token}`]) assert.throws(() => parseStateJSON(`[{${pair}}]`));
    assert.throws(() => parseStateJSON(`{"schema_version":1,"alerts":[{"decimals":${token}}],"ALERTS":[]}`));
  }
  assert.throws(() => parseStateJSON('{"schema_version":1.0,"SCHEMA_VERSION":1}'));
  assert.throws(() => parseStateJSON('{"watchlist":[12],"WATCHLIST":[]}'));
  assert.equal(parseStateJSON("null"), null);
});
for (const date of ["2026-02-30T00:00:00Z", "2026-04-31T00:00:00Z", "2026-02-01", "2026-02-01 00:00:00Z", "2026-02-01T24:00:00Z", "2026-02-01T00:00:60Z", "2026-02-01T00:00:00Z\n"]) test(`stored RFC3339 calendar rejects ${JSON.stringify(date)}`, () => {
  const alert = { id: "fixture-date", ref: "BTC", kind: "above", value: 120, created: date };
  assert.throws(() => legacyState(["BTC"], [alert]));
  assert.throws(() => legacyState(["BTC"], [{ ...alert, created: "2026-02-01T00:00:00Z", met: date }]));
  assert.throws(() => parseStateJSON(JSON.stringify([alert])));
});
test("stored RFC3339 keeps legal leap days, fractional seconds, UTC offsets and epoch zero", () => {
  for (const created of ["0000-02-29T00:00:00Z", "2000-02-29T23:59:59.123456789Z", "1970-01-01T00:00:00Z", "2026-02-01T00:00:00+08:00", "2026-02-01T00:00:00-05:00"]) {
    const state = legacyState(["BTC"], [{ id: "fixture-date", ref: "BTC", kind: "above", value: 120, created }]);
    assert.equal(decodeState(encodeState(state)).alerts[0].created, created);
  }
});
const floats: Array<[string, number | undefined]> = [
  ["0x80", undefined], ["0b10000000", undefined], ["0o200", undefined], ["0x1p7", 128], ["1_28", 128],
  ["+0X_1.8p+7", 192], [".5", .5], ["1.", 1], ["1e+0_2", 100], ["128 ", undefined], ["1__28", undefined],
  ["0x1p-1074", Number.MIN_VALUE], ["0x1p-1075", 0], ["0x1.0000000000001p-1075", Number.MIN_VALUE],
  ["0x1.fffffffffffffp1023", Number.MAX_VALUE], ["0x1.fffffffffffff8p1023", undefined],
  ["0x1.00000000000008p0", 1], ["0x1.0000000000000801p0", 1 + Number.EPSILON],
  ["0x0p999999999999999999999", 0], ["0x1p-9999999999999999999", 0], ["1e999999", undefined], ["-0x0p0", -0],
];
for (const [raw, expected] of floats) test(`Go float grammar and IEEE rounding: ${raw}`, () => { assert.ok(Object.is(parseFloat64(raw), expected)); });
test("Go float specials are recognized without accepting signed NaN or whitespace", () => {
  assert.ok(Number.isNaN(parseFloat64("nAn"))); assert.equal(parseFloat64("+Inf"), Infinity); assert.equal(parseFloat64("-INFINITY"), -Infinity);
  assert.equal(parseFloat64("+NaN"), undefined); for (const whitespace of ["\n", "\r", "\r\n", " ", " ", " "]) assert.equal(parseFloat64("Inf" + whitespace), undefined); assert.equal(floatResult("1e999").value, Infinity);
});
test("bundled alert and OKX/Tencent candles preserve Go float grammar instead of JS numeric prefixes", async () => {
  const { service, fixture } = await setup();
  try {
    for (const raw of ["0x80", "0b10000000", "0o200"]) await assert.rejects(service.addAlert({ source: "okx", symbol: "BTC-USDT" }, "above", raw));
    for (const raw of ["0x1p7", "1_28"]) {
      const alert = await service.addAlert({ source: "okx", symbol: "BTC-USDT" }, "above", raw); assert.equal(alert.value, 128); await service.removeAlert(alert.id);
    }
    const tokens = ["0x80", "0b10000000", "0o200", "0x1p7", "1_28"];
    fixture.override = url => new Response(JSON.stringify(url.hostname === "www.okx.com" ? { code: "0", data: tokens.map(raw => ["1000", raw, "150", "90", "120", "3"]) }
      : { code: 0, data: { sh600000: { qfqday: tokens.map(raw => ["2026-02-01", raw, "120", "150", "90", "3"]) } } }));
    assert.deepEqual((await service.sources.okx.candles("BTC-USDT", "1H", 60, 0)).map((row: any) => row.open), [128, 128]);
    assert.deepEqual((await service.sources.cn.candles("600000.SH", "1D", 60, 0)).map((row: any) => row.open), [128, 128]);
  } finally { service.dispose(); }
});
for (const raw of [
  '{"code":"0","msg":123,"data":[{"instId":"ETH-USDT","last":"100"}]}',
  '{"code":"0","msg":123,"MSG":"ok","data":[{"instId":"ETH-USDT","last":"100"}]}',
  '{"code":0,"CODE":"0","data":[{"instId":"ETH-USDT","last":"100"}]}',
  '{"code":"0","data":[{"instId":"ETH-USDT","last":100,"last":"100"}]}',
  '{"code":"0","data":[{"instId":"ETH-USDT","LAST":100,"last":"100"}]}',
]) test(`OKX validates envelope and duplicate fields before state/cache success: ${raw}`, async () => {
  const { service, fixture, hub } = await setup();
  try {
    const before = hub.data; fixture.override = () => new Response(raw);
    await assert.rejects(service.addWatch({ source: "okx", symbol: "ETH-USDT" }), rejected);
    assert.equal(hub.data, before); assert.equal(service.status("en").state, "error"); assert.equal(service.http.cache.entries.size, 0);
    fixture.override = undefined;
    await service.addWatch({ source: "okx", symbol: "ETH-USDT" }); assert.equal(service.status("en").state, "ok");
    const good = [...service.http.cache.entries.values()][0].value; fixture.override = () => new Response(raw);
    await assert.rejects(service.quote({ source: "okx", symbol: "ETH-USDT" }, 0), rejected);
    assert.equal([...service.http.cache.entries.values()][0].value, good);
  } finally { service.dispose(); }
});
test("OKX validates every known ticker field, including unrelated records, duplicate orders and case variants", async () => {
  const { service, fixture } = await setup();
  try {
    for (const field of ["instId", "instType", "last", "open24h", "high24h", "low24h", "vol24h", "volCcy24h", "ts"]) for (const bad of [123, false, [], {}]) {
      fixture.override = () => new Response(`{"code":"0","data":[{"instId":"BTC-USDT","last":"100"},{"${field}":${JSON.stringify(bad)},"${field.toUpperCase()}":"100"}]}`);
      await assert.rejects(service.sources.okx.search("BTC", 30, 0), rejected); assert.equal(service.http.cache.entries.size, 0);
    }
    fixture.override = () => new Response('{"CODE":"0","MSG":null,"data":123,"DATA":[{"INSTID":"BTC-USDT","LAST":"101","last":null,"TS":"0","unknown":123}]}');
    const q = await service.sources.okx.quote("BTC-USDT", 0); assert.equal(q.last, 101); assert.equal(q.at, 0);
    fixture.override = () => new Response('{"code":"0","data":[["0","1","2","1","2","3"]]}');
    assert.equal((await service.sources.okx.candles("BTC-USDT", "1H", 60, 0))[0].at, 0);
  } finally { service.dispose(); }
});
test("local authoring parser checks parseDiagnostics and Node syntax, with a genuine malformed-source negative control", async () => {
  const dir = await mkdtemp(resolve(extension, "build/parser-control-"));
  try {
    const source = await readFile(resolve(extension, "build/markets.mjs"), "utf8");
    const parse = (text: string) => (ts.createSourceFile("artifact.mjs", text, ts.ScriptTarget.ES2022, true, ts.ScriptKind.JS) as ts.SourceFile & { parseDiagnostics: readonly ts.Diagnostic[] }).parseDiagnostics;
    assert.equal(parse(source).length, 0); assert.ok(parse("export default (").length > 0);
    for (const [text, accepted] of [[source, true], ["export default (", false]] as const) {
      const file = resolve(dir, "control.mjs"); await writeFile(file, text);
      const checked = spawnSync("node", ["--check", file], { cwd: dir, encoding: "utf8", timeout: 10_000 });
      assert.equal(checked.status === 0, accepted); if (!accepted) assert.match(checked.stderr, /SyntaxError/);
    }
  } finally { await rm(dir, { recursive: true, force: true }); }
});
