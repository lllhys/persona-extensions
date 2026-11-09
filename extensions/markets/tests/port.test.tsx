import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer, type Snapshot } from "@persona/extension/ui";
import { Markets } from "../src/service.ts";
import { HTTP, Queue, Cache, ReadError } from "../src/http.ts";
import { OKX, ticker } from "../src/okx.ts";
import { Tencent, decimals } from "../src/tencent.ts";
import { parseRef, normalize, key, readRefs, fmtFloat, price, priceLike, fmtChange, compact, fmtCN, localTime, stamp, tone, meets, near, emptyQuote, type Alert } from "../src/model.ts";
import { StateStore, encodeState, decodeState, legacyState } from "../src/state.ts";
import { callTool, connector, tools } from "../src/tools.ts";
import { snapshotPage } from "../src/snapshot.ts";
import { root } from "../src/root.tsx";
import { loadPage, readPageState, transition, sortByChange } from "../src/page.tsx";
import { locales } from "../src/locale.ts";
import { FakeHub, Fixture, deferred, fields, initial, until } from "./fixtures.ts";
async function setup(config = {}) {
  const hub = new FakeHub(), fixture = new Fixture();
  const service = new Markets(hub, config, { fetch: fixture.fetch, clock: () => fixture.at, interval: () => () => {}, id: () => "fixture-alert" });
  await service.initialize(); return { service, hub, fixture };
}
const alert = (value = 120): Alert => ({ id: "fixture-alert", ref: "okx:BTC-USDT", kind: "above", value, created: new Date(Date.UTC(2026, 10, 1)).toISOString() });
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);

test("source refs preserve normalization and reject unknown prefixes, delivery futures and ambiguous stock codes", () => {
  const cases: Array<[string, string | undefined]> = [["btc", "okx:BTC-USDT"], [" eth / usdc ", "okx:ETH-USDC"], ["BTC_USDT_SWAP", "okx:BTC-USDT-SWAP"], ["cn:600000", "cn:600000.SH"], ["cn:000001", "cn:000001.SZ"], ["cn:920001", "cn:920001.BJ"], ["cn:sh000001", "cn:000001.SH"], ["hk:HK123.HK", "hk:00123"], ["hk:HSI", "hk:HSI"], ["us:BRK-B", "us:BRK.B"], ["us:TEST.OQ", "us:TEST"], ["us:.IXIC", "us:.IXIC"], ["private:ABC", undefined], ["cn:SH000001.SZ", undefined], ["BTC-USDT-202612", undefined], ["us:.OQ", "us:.OQ"]];
  for (const [typed, expected] of cases) assert.equal(parseRef(typed) && key(parseRef(typed)!), expected, typed);
  assert.deepEqual(readRefs(["btc", "okx:BTC-USDT", "bogus:abc"]), ["okx:BTC-USDT"]);
  assert.equal(normalize("us", "BRK.B"), "BRK.B");
});
test("price precision, changes, compact values and configured red/green conventions match Go", () => {
  for (const [n, expected] of [[0, "0"], [76330.1, "76330.10"], [3.5021, "3.5021"], [.0000123456, "0.00001235"], [-.0000123456, "-0.00001235"]] as const) assert.equal(fmtFloat(n), expected);
  assert.equal(price({ decimals: 2 }, 10), "10.00"); assert.equal(price({ decimals: 0 }, 0), "—");
  assert.equal(priceLike({ ...emptyQuote("x"), decimals: 3 }, .1, 1), "0.100");
  assert.equal(fmtChange(.03), "+3.00%"); assert.equal(compact(1180000000), "1.18B"); assert.equal(fmtCN(1803000000), "18.03亿");
  assert.equal(tone({}, .01), "danger"); assert.equal(tone({ colors: "green_up" }, .01), "positive"); assert.equal(tone({}, 0), "neutral");
});
test("exchange wall clocks, minute starts and DST use market timezones, not the guest timezone", () => {
  assert.equal(stamp(localTime("202611061000", "Asia/Shanghai"), "UTC"), "2026-11-06 02:00");
  assert.equal(stamp(localTime("2026-07-06 10:00:00", "America/New_York"), "UTC"), "2026-07-06 14:00");
  assert.equal(stamp(localTime("2026-11-06 10:00:00", "America/New_York"), "UTC"), "2026-11-06 15:00");
  assert.equal(localTime("202602300930", "UTC"), 0);
});
test("bounded fetch queue stays at four, cancels waiters without consuming a slot and progresses fairly", async () => {
  const queue = new Queue(4), controls = Array.from({ length: 8 }, () => deferred<void>()), started: number[] = [];
  const abort = new AbortController();
  const work = controls.map((c, i) => queue.run(async () => { started.push(i); await c.promise; }, i === 4 ? abort.signal : undefined));
  const cancelled = assert.rejects(work[4], { name: "AbortError" }); abort.abort(); await cancelled;
  assert.equal(queue.active, 4); assert.deepEqual(started, [0, 1, 2, 3]);
  controls[0].resolve(); await work[0]; await until(() => started.length, n => n === 5); assert.deepEqual(started, [0, 1, 2, 3, 5]);
  controls.forEach(c => c.resolve()); await Promise.allSettled(work); assert.equal(queue.active, 0);
});
test("HTTP allows only declared HTTPS hosts and does not cache errors, partial or over-budget bodies", async () => {
  let calls = 0;
  const http = new HTTP(async () => { calls++; return new Response("12345"); });
  const credentialSource = new URL("https://www.okx.com/x");
  credentialSource.username = "user";
  assert.equal(credentialSource.origin, "https://www.okx.com");
  assert.equal(credentialSource.username, "user");
  for (const url of ["http://www.okx.com/x", "https://www.okx.com:443/x", "https://example.com/x", credentialSource.href]) {
    if (url.includes(":443")) continue; // URL normalizes the default TLS port.
    await assert.rejects(http.get(url, 10), ReadError);
  }
  assert.equal(calls, 0); await assert.rejects(http.get("https://www.okx.com/x", 10, 4), /size limit/); assert.equal(http.cache.entries.size, 0);
  await http.get("https://www.okx.com/x", 10, 5); await http.get("https://www.okx.com/x", 10, 5); assert.equal(calls, 2);
  const abort = new AbortController(); abort.abort(); await assert.rejects(http.get("https://www.okx.com/x", 10, 5, abort.signal), { name: "AbortError" });
  const rate = new HTTP(async () => new Response("", { status: 429 })); await assert.rejects(rate.get("https://www.okx.com/x", 10), (err: ReadError) => err.code === "rate_limited"); assert.equal(rate.cache.entries.size, 0);
  const invalid = new HTTP(async () => new Response("not json")); await assert.rejects(invalid.json("https://www.okx.com/x", 10)); assert.equal(invalid.cache.entries.size, 0);
});
test("HTTP cancellation reaches active transport and cannot publish a late response to cache", async () => {
  const abort = new AbortController(); const started = deferred<void>();
  const http = new HTTP(async (_url, init) => { started.resolve(); await new Promise<void>((_yes, no) => init?.signal?.addEventListener("abort", () => no(init.signal!.reason), { once: true })); return new Response("late"); });
  const work = http.get("https://www.okx.com/x", 10, undefined, abort.signal); await started.promise;
  const rejected = assert.rejects(work, { name: "AbortError" }); abort.abort(); await rejected;
  assert.equal(http.queue.active, 0); assert.equal(http.cache.entries.size, 0);
});
test("cache eviction is bounded and expiration and force-refresh are independent", () => {
  let at = 100; const cache = new Cache<number>(() => at, 2); cache.put("a", 1); at++; cache.put("b", 2); at++; cache.put("c", 3);
  assert.equal(cache.entries.size, 2); assert.equal(cache.get("a", 10), undefined); assert.equal(cache.get("b", 10), 2); assert.equal(cache.get("b", 0), undefined);
  at += 10; assert.equal(cache.get("b", 10), undefined);
});
test("OKX few/many paths, negative cache, swap turnover and oldest-first malformed candle filtering", async () => {
  const fixture = new Fixture(), source = new OKX(new HTTP(fixture.fetch, () => fixture.at));
  await source.quotes(["BTC-USDT", "ETH-USDT", "SOL-USDT", "PEPE-USDT", "TINY-USDT"], 10_000);
  assert.equal(fixture.hits.get("www.okx.com/api/v5/market/ticker"), 5);
  await source.quotes(["BTC-USDT", "ETH-USDT", "SOL-USDT", "PEPE-USDT", "TINY-USDT", "X-USDT"], 10_000);
  assert.equal(fixture.hits.get("www.okx.com/api/v5/market/tickers"), 1);
  await assert.rejects(source.quote("MISSING-USDT", 10_000), (err: ReadError) => err.code === "not_found"); const count = fixture.hits.get("www.okx.com/api/v5/market/ticker");
  await assert.rejects(source.quote("MISSING-USDT", 10_000)); assert.equal(fixture.hits.get("www.okx.com/api/v5/market/ticker"), count);
  assert.equal(ticker({ instId: "BTC-USDT-SWAP", last: "100", volCcy24h: "200" }).turnover, 20_000);
  const cs = await source.candles("BTC-USDT", "1H", 60, 30_000); assert.ok(cs[0].at < cs[1].at);
  fixture.override = url => url.pathname.endsWith("/candles") ? new Response(JSON.stringify({ code: "0", data: [["bad"], ["1793977200000", "1", "2", "0.5", "1.5", "5"], ["1e3", "2", "3", "4", "5", "6"]] })) : undefined;
  assert.equal((await source.candles("BTC-USDT", "1H", 60, 0)).length, 1);
  // Wrong JSON cell types fail the Go [][]string decoder; they are not skipped like invalid string values.
  fixture.override = () => new Response('{"code":"0","data":[[1,"2","3","4","5","6"]]}');
  await assert.rejects(source.candles("BTC-USDT", "1H", 60, 0), (err: ReadError) => err.code === "invalid");
  await assert.rejects(source.candles("BTC-USDT", "T", 60, 0));
});
test("OKX API rate errors are not cached as successful JSON and recover on the next read", async () => {
  const fixture = new Fixture(); fixture.override = () => new Response('{"code":"50011","data":[]}');
  const source = new OKX(new HTTP(fixture.fetch)); await assert.rejects(source.quote("BTC-USDT", 10_000), (err: ReadError) => err.code === "rate_limited");
  fixture.override = undefined; assert.equal((await source.quote("BTC-USDT", 10_000)).last, 100);
  assert.equal(fixture.hits.get("www.okx.com/api/v5/market/ticker"), 2);
});
test("Tencent batch formats, index turnover, facts, decimal ticks and US exchange code reuse", async () => {
  const fixture = new Fixture(), tx = new Tencent(new HTTP(fixture.fetch, () => fixture.at));
  const cn = tx.market("cn"), hk = tx.market("hk"), us = tx.market("us");
  assert.equal((await cn.quotes(["600000.SH", "000001.SH"], 10_000)).size, 2); assert.equal(fixture.hits.get("qt.gtimg.cn/q=sh600000,sh000001"), 1);
  const f = fields("hk"); f[63] = "ZS"; assert.equal(hk.parseQuote("HSI", f)!.turnover, 240_000_000); assert.ok(!hk.parseQuote("HSI", f)!.facts.some(f => f.label === "pe" || f.label === "volume"));
  assert.equal(us.parseQuote(".IXIC", fields("us"))!.turnover, 0);
  const q = cn.parseQuote("600000.SH", fields("cn"))!; assert.equal(q.facts.find(f => f.label === "volume")!.value, "12.00万"); assert.ok(q.idle); assert.equal(q.currency, "CNY");
  assert.equal(decimals("cn", "ETF", 2), 3); assert.equal(decimals("hk", "stock", .5), 3); assert.equal(decimals("us", "stock", .5), 4);
  await us.candles("TEST", "1D", 24, 30_000); assert.equal(us.code("TEST", true), "usTEST.OQ"); assert.equal(us.code("TEST"), "usTEST");
  await assert.rejects(hk.candles("00123", "1H", 24, 0));
});
test("Tencent search shares one response across markets, filters derivatives, deduplicates and reads GBK names", async () => {
  const fixture = new Fixture(), tx = new Tencent(new HTTP(fixture.fetch));
  for (const id of ["cn", "hk", "us"] as const) assert.equal((await tx.market(id).search("Synthetic", 30, 10_000)).length, 1);
  assert.equal(fixture.hits.get("smartbox.gtimg.cn/s3/"), 1);
  const ascii = new TextEncoder().encode(`v_sh600000="${fields("cn", "NAME").join("~")}";`);
  const index = new TextDecoder().decode(ascii).indexOf("NAME"), bytes = ascii.slice(); bytes.set([0xb2, 0xe2, 0xca, 0xd4], index);
  fixture.override = () => new Response(bytes);
  const quote = await tx.market("cn").quote("600000.SH", 0); assert.equal(quote.name, "测试");
});
test("Tencent weekly/minute bar starts, share volumes and intraday cumulative decreases", async () => {
  const fixture = new Fixture(), tx = new Tencent(new HTTP(fixture.fetch));
  const cs = await tx.market("cn").candles("600000.SH", "15m", 60, 0); assert.equal(stamp(cs[0].at, "Asia/Shanghai"), "2026-11-06 09:45"); assert.equal(cs[0].vol, 120_000);
  const ws = await tx.market("us").candles("TEST", "1W", 60, 0); assert.equal(stamp(ws[0].at, "America/New_York"), "2026-11-02 00:00");
  const ms = await tx.market("cn").candles("600000.SH", "T", 60, 0); assert.deepEqual(ms.map(c => c.vol), [1000, 500, 0]); assert.equal(ms[2].close, 99);
});
test("sealed persistence round-trips Unicode, rejects corrupt blobs and retains original state on load outage", async () => {
  const hub = new FakeHub(), state = { ...initial, alerts: [{ ...alert(), name: "Synthetic 测试 instrument" }] };
  const encoded = encodeState(state); assert.deepEqual(decodeState(encoded), state); assert.ok(!encoded.includes("Synthetic"));
  const store = new StateStore(hub); hub.failLoad = true; await assert.rejects(store.load(["ETH-USDT"])); assert.equal(hub.saves, 0);
  hub.failLoad = false; hub.data = "corrupt"; await assert.rejects(store.load(["ETH-USDT"])); assert.equal(hub.data, "corrupt"); assert.equal(hub.saves, 0);
  assert.throws(() => decodeState(btoa('{"schema_version":99}'))); assert.throws(() => encodeState({ ...initial, alerts: [alert(), alert()] }));
  assert.deepEqual(legacyState(["btc", "okx:BTC-USDT"], null), { schema_version: 1, watchlist: ["okx:BTC-USDT"], alerts: [] });
});
test("empty secret seeds once, existing empty watchlist overrides config and asynchronous mutations serialize", async () => {
  const hub = new FakeHub(); hub.data = ""; const store = new StateStore(hub); await store.load(["btc", "ETH-USDT"]); assert.equal(hub.saves, 1);
  const gate = deferred<void>(); const first = store.transaction(next => { next.watchlist.push("okx:SOL-USDT"); });
  const second = store.transaction(next => { next.watchlist = next.watchlist.filter(ref => !ref.includes("BTC")); }); await Promise.all([first, second]);
  assert.deepEqual(store.snapshot().watchlist, ["okx:ETH-USDT", "okx:SOL-USDT"]);
  hub.failSave = true; const before = store.snapshot(); await assert.rejects(store.transaction(next => { next.watchlist = []; })); assert.deepEqual(store.snapshot(), before);
  hub.failSave = false; hub.data = encodeState({ ...initial, watchlist: [] }); await store.load(["btc"]); assert.deepEqual(store.snapshot().watchlist, []); gate.resolve();
});
test("watchlist reordering stays within its source slots and failed saves do not remove instruments", async () => {
  const { service, hub } = await setup(); await service.addWatch(parseRef("ETH-USDT")!); await service.addWatch(parseRef("SOL-USDT")!);
  await service.moveWatch(parseRef("SOL-USDT")!, "top"); assert.deepEqual(service.watchlist().filter(ref => ref.startsWith("okx:")), ["okx:SOL-USDT", "okx:BTC-USDT", "okx:ETH-USDT"]);
  assert.equal(service.watchlist()[1], "cn:600000.SH"); await service.moveWatch(parseRef("SOL-USDT")!, 1); assert.equal(service.watchlist()[0], "okx:BTC-USDT");
  hub.failSave = true; const before = service.watchlist(); await assert.rejects(service.removeWatch(parseRef("BTC")!)); assert.deepEqual(service.watchlist(), before);
});
test("50-entry watchlist and alert quotas, duplicate targets and already-met alerts are enforced", async () => {
  const { service, hub } = await setup(); await service.store.transaction(next => { next.watchlist = Array.from({ length: 50 }, (_, i) => `okx:C${i}-USDT`); });
  await assert.rejects(service.addWatch(parseRef("ETH")!), /limit/); assert.equal(service.watchlist().length, 50);
  await assert.rejects(service.addAlert(parseRef("BTC")!, "above", "50"), /already met/); await assert.rejects(service.addAlert(parseRef("BTC")!, "move", "101%"));
  await service.addAlert(parseRef("BTC")!, "above", "120"); await assert.rejects(service.addAlert(parseRef("BTC")!, "above", "120"), /Duplicate/);
  await service.store.transaction(next => { next.alerts = Array.from({ length: 50 }, (_, i) => ({ ...alert(120 + i), id: `fixture-${i}` })); });
  await assert.rejects(service.addAlert(parseRef("BTC")!, "below", "50"), /limit/); assert.equal(decodeState(hub.data).alerts.length, 50);
});
test("alerts trigger once, wake Inbox, remain triggered on save failure and expire after seven days", async () => {
  const { service, hub, fixture } = await setup({ triage_rules: "Synthetic rule\\nSecond line", tz: "Asia/Shanghai" });
  await service.addAlert(parseRef("BTC")!, "above", "120"); fixture.last = 125; fixture.at += 60_000;
  hub.failSave = true; await service.checkAlerts(0); assert.ok(service.alerts()[0].met); assert.equal(hub.notifications.filter(n => n.event === "connector.changed").length, 1);
  assert.equal(service.inbox("en").things[0].rules, "Synthetic rule\nSecond line"); assert.equal(service.inbox("en").things[0].kind, "price_alert");
  await service.checkAlerts(0); assert.equal(hub.notifications.length, 1); fixture.at += 7 * 86400_000; assert.deepEqual(service.inbox().things, []);
});
test("alert predicates preserve missing base handling, absolute negative moves and near-target thresholds", () => {
  const q = { ...emptyQuote("BTC-USDT"), last: 98, base: 100 };
  assert.ok(meets({ ...alert(2), kind: "move" }, q)); assert.ok(near({ ...alert(2.5), kind: "move" }, q));
  assert.ok(near({ ...alert(98.9), kind: "above" }, q)); assert.ok(!meets({ ...alert(), kind: "move" }, { ...q, base: 0 })); assert.ok(!near(alert(), { ...q, last: 0 }));
});
test("connector tools retain all three schemas, strict arguments, clamps, timezone and partial source failures", async () => {
  const { service, fixture } = await setup({ tz: "Asia/Shanghai" });
  assert.deepEqual(tools("en").map(t => t.name), ["markets_quote", "markets_search", "markets_candles"]);
  const quote = await callTool(service, "markets_quote", { symbols: ["BTC", "unknown:code"] }, "en"); assert.match(quote.text, /okx:BTC-USDT/); assert.match(quote.text, /Invalid code/);
  assert.match((await callTool(service, "markets_search", { query: "Synthetic", limit: 1000 }, "en")).text, /Synthetic instrument/);
  assert.match((await callTool(service, "markets_candles", { symbol: "BTC", bar: "1H" }, "en")).text, /Asia\/Shanghai/);
  await assert.rejects(callTool(service, "markets_candles", { symbol: "hk:123", bar: "1H" })); await assert.rejects(callTool(service, "markets_quote", { symbols: [1] }));
  await assert.rejects(connector(service)("status", { contribution: "foreign" }), (err: any) => err.code === "no_contribution");
  fixture.failures.set("www.okx.com", 429); fixture.at += 60_000;
  const partial = await service.quotes(service.watchlist(), 0, true); assert.ok(partial.errors.has("okx")); assert.ok(partial.quotes.has("cn:600000.SH"));
  await assert.rejects(service.quotes(service.watchlist(), 0, false));
});
test("native snapshots keep original navigation ID's inline detail/back and old UI1/3/5 branches", async () => {
  const { service } = await setup();
  for (const version of [1, 3, 5]) {
    const req: any = { state: "{}", contribution: "watchlist", ui_version: version, locale: "en", tz: "UTC" };
    const view = await snapshotPage(service).render(req); assert.equal(!!view.search, version >= 3);
    const result = await snapshotPage(service).action!({ ...req, action: "open:cn:600000.SH" });
    assert.ok(result?.view); assert.equal(!!result.view.sections.flatMap(s => s.items).find(i => i.type === "chart"), version >= 2);
    assert.ok(!result.push && !result.pop); const detailState = result.view.state;
    const back = await snapshotPage(service).action!({ ...req, state: detailState, action: "back" }); assert.equal(JSON.parse(back!.view!.state!).i, "");
  }
  const card = await snapshotPage(service, true).render({ ui_version: 5, locale: "en", state: "" } as any); assert.ok(!card.sections.flatMap(s => s.items).some(i => "action" in i && i.action));
  const disabled = await setup({ overview_card: false }); await assert.rejects(async () => snapshotPage(disabled.service, true).render({} as any), (err: any) => err.code === "unsupported");
});
test("page state, source filtering, descending sort and back retain search and parent ordering", async () => {
  const { service } = await setup(); const state = readPageState({ q: "  synthetic  ", s: "down", m: "cn", o: true }, service);
  const detail = await transition(service, state, "open:cn:600000"); assert.equal(detail.state.q, "synthetic"); assert.equal(detail.state.s, "down");
  const back = await transition(service, detail.state, "back"); assert.equal(back.state.q, "synthetic"); assert.equal(back.state.m, "cn"); assert.equal(back.state.i, "");
  const filtered = await snapshotPage(service).render({ state: JSON.stringify({ m: "cn" }), ui_version: 5, locale: "en" } as any);
  assert.ok(filtered.sections.some(s => s.title?.startsWith("A-shares"))); assert.ok(!filtered.sections.some(s => s.title?.startsWith("US ·")));
  const quotes = new Map([["up", { ...emptyQuote("up"), last: 110, base: 100 }], ["down", { ...emptyQuote("down"), last: 90, base: 100 }]]);
  assert.deepEqual(sortByChange(["missing", "up", "down"], quotes, true), ["down", "up", "missing"]);
});
test("root keeps cached effects stable across suspend/resume and cancels superseded reads", async () => {
  const { service, fixture } = await setup(); const errors: unknown[] = [];
  const renderer = new Renderer({ authorize: () => true, onError: error => errors.push(error) });
  renderer.mount(root({ service, locale: "en", uiVersion: 5 }));
  const settled = await until(() => renderer.current(), s => body(s).includes("Synthetic instrument"));
  renderer.suspend(); renderer.resume(); const resumed = renderer.current(); await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(renderer.current().version, resumed.version); assert.equal(settled.generation, resumed.generation);
  const row = resumed.view.sections.flatMap(s => s.items).find(i => i.key === "quote-okx:BTC-USDT") as any;
  await renderer.event({ generation: resumed.generation, version: resumed.version, key: row.key, actionId: row.action.id, eventId: "fixture-open", values: {} });
  await until(() => renderer.current(), s => s.view.sections.flatMap(s => s.items).some(i => i.type === "chart"));
  renderer.dispose(); assert.deepEqual(errors, []); assert.ok(fixture.hits.size > 0);
});
test("root defers its own write invalidation until alert form completion and returns a replayable toast", async () => {
  const { service } = await setup();
  const renderer = new Renderer({ authorize: () => true });
  try {
    renderer.mount(root({ service, locale: "en", uiVersion: 5, state: JSON.stringify({ i: "okx:BTC-USDT", f: true }) }));
    const loaded = await until(() => renderer.current(), s => s.view.sections.flatMap(s => s.items).some(i => i.key === "alert:okx:BTC-USDT"));
    const button = loaded.view.sections.flatMap(s => s.items).find(i => i.key === "alert:okx:BTC-USDT") as any;
    const event = { generation: loaded.generation, version: loaded.version, key: button.key, actionId: button.action.id, eventId: "fixture-set-alert", values: { alert_kind: "above", alert_value: "120" } };
    const result: any = await renderer.event(event);
    assert.equal(result.toast, "Alert set");
    await until(() => renderer.current(), s => JSON.parse(s.view.state!).f === false && s.view.sections.flatMap(s => s.items).some(i => i.key === "form"));
    assert.equal(service.alerts().length, 1); assert.deepEqual(await renderer.event(event), result); assert.equal(service.alerts().length, 1);
  } finally { renderer.dispose(); }
});
test("disposing a mounted root aborts in-flight UI reads and does not publish stale data", async () => {
  const { service, fixture } = await setup(); const started = deferred<void>(); let cancelled = false;
  fixture.override = (_url, init) => {
    started.resolve();
    return new Promise((_yes, no) => init?.signal?.addEventListener("abort", () => { cancelled = true; no(init.signal!.reason); }, { once: true }));
  };
  const renderer = new Renderer({ authorize: () => true }); renderer.mount(root({ service, locale: "en", uiVersion: 5 }));
  await started.promise; renderer.dispose(); await until(() => cancelled, Boolean); assert.equal(service.http.cache.entries.size, 0);
});
test("maximum watchlist plus alerts and multiple failing source groups retain bounded valid native snapshots", async () => {
  const { service, fixture } = await setup();
  await service.store.transaction(next => { next.watchlist = Array.from({ length: 50 }, (_, i) => `okx:C${i}-USDT`); next.alerts = Array.from({ length: 50 }, (_, i) => ({ ...alert(120 + i), id: `fixture-${i}` })); });
  const view = await snapshotPage(service).render({ state: "{}", ui_version: 5, locale: "en" } as any);
  assert.ok(view.sections.flatMap(s => s.items).length <= 300);
  fixture.failures.set("www.okx.com", 429); fixture.failures.set("qt.gtimg.cn", 500);
  await service.store.transaction(next => { next.watchlist = initial.watchlist.slice(); }); fixture.at += 60_000;
  const failed = await snapshotPage(service).render({ state: "{}", ui_version: 5, locale: "en" } as any);
  assert.ok(failed.sections.flatMap(s => s.items).filter(i => i.type === "button").length >= 4);
});
test("exact search stays in its original leading section and failed searches can return to watchlist", async () => {
  const { service, fixture } = await setup();
  fixture.override = url => url.pathname.endsWith("/tickers") || url.hostname === "smartbox.gtimg.cn" ? new Response("Synthetic search failure", { status: 500 }) : undefined;
  const exact = await snapshotPage(service).render({ state: JSON.stringify({ q: "cn:600000.SH" }), ui_version: 5, locale: "en" } as any);
  assert.equal(exact.sections[0].key, "section-exact"); assert.equal(exact.sections[0].title, "Code cn:600000.SH");
  const failed = await snapshotPage(service).render({ state: JSON.stringify({ q: "unknown:code" }), ui_version: 5, locale: "en" } as any);
  const exit = failed.sections.flatMap(s => s.items).find(i => i.key === "clear") as any; assert.equal(exit.action.id, "clear");
  const back = await snapshotPage(service).action!({ state: failed.state, action: "clear", ui_version: 5, locale: "en" } as any); assert.equal(JSON.parse(back!.view!.state!).q, "");
});
test("closed stock watchlists use idle refresh even when an undisplayed crypto benchmark is active", async () => {
  const { service } = await setup(); await service.store.transaction(next => { next.watchlist = ["cn:600000.SH"]; });
  const view = await snapshotPage(service).render({ state: "{}", ui_version: 5, locale: "en" } as any); assert.equal(view.refresh_after, 300);
});
test("all locale resources have identical keys and data fingerprints ignore read timestamps", async () => {
  const expected = Object.keys(locales.en).sort(); for (const dict of Object.values(locales)) { assert.deepEqual(Object.keys(dict).sort(), expected); assert.ok(Object.values(dict).every(value => value.length > 0)); }
  const { service } = await setup(); assert.equal((await loadPage(service, readPageState({}, service), false, false)).kind, "list");
});
