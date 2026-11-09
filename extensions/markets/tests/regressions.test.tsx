import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer, type TimerAdapter, type Snapshot } from "@persona/extension/ui";
import { Markets } from "../src/service.ts";
import { ReadError } from "../src/http.ts";
import { MutationError } from "../src/errors.ts";
import { root } from "../src/root.tsx";
import { snapshotPage } from "../src/snapshot.ts";
import { loadPage, readPageState, refreshDelay } from "../src/page.tsx";
import { encodeState, decodeState, legacyState } from "../src/state.ts";
import { FakeHub, Fixture, deferred, initial, until } from "./fixtures.ts";

class Clock implements TimerAdapter {
  at = 0;
  next = 0;
  tasks = new Map<number, { fn: () => void; ms: number; due: number }>();
  setInterval(fn: () => void, ms: number) { const id = ++this.next; this.tasks.set(id, { fn, ms, due: this.at + ms }); return id; }
  clearInterval(handle: unknown) { this.tasks.delete(handle as number); }
  advance(ms: number) {
    const end = this.at + ms;
    for (;;) {
      const due = [...this.tasks.values()].filter(t => t.due <= end).sort((a, b) => a.due - b.due)[0];
      if (!due) break;
      this.at = due.due; due.due += due.ms; due.fn();
    }
    this.at = end;
  }
}
async function setup() {
  const hub = new FakeHub(), fixture = new Fixture();
  const service = new Markets(hub, {}, { fetch: fixture.fetch, clock: () => fixture.at, interval: () => () => {}, id: () => "fixture-alert" });
  await service.initialize();
  return { hub, fixture, service };
}
const text = (s: Snapshot) => JSON.stringify(s.view);
async function press(renderer: Renderer, key: string, values: Record<string, string> = {}) {
  const snapshot = renderer.current(), item = snapshot.view.sections.flatMap(s => s.items).find(i => i.key === key) as any;
  assert.ok(item?.action?.id, `Missing callback ${key}`);
  return renderer.event({ generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values }) as Promise<any>;
}

test("legacy bare alert refs normalize during conversion and stored-state load, then trigger and wake Inbox", async () => {
  const { service, fixture, hub } = await setup();
  try {
    const old = { id: "fixture-legacy", ref: "BTC-USDT", kind: "above", value: 120, created: new Date(fixture.at).toISOString(), decimals: 2 };
    const converted = legacyState(["BTC-USDT"], [old]);
    assert.equal(converted.alerts[0].ref, "okx:BTC-USDT");
    assert.deepEqual(old.ref, "BTC-USDT");
    // Exercise raw pre-normalization state as well as the converter's encoded result.
    hub.data = Buffer.from(JSON.stringify({ ...initial, alerts: [old] })).toString("base64");
    await service.store.load([]);
    assert.equal(service.alerts()[0].ref, "okx:BTC-USDT");
    fixture.last = 125; await service.checkAlerts(0);
    assert.equal(service.alerts()[0].price, 125);
    assert.equal(decodeState(hub.data).alerts[0].ref, "okx:BTC-USDT");
    assert.deepEqual(hub.notifications, [{ event: "connector.changed", params: { contribution: "markets", scope: "inbox" } }]);
    assert.equal(service.inbox("en").things.length, 1);
    await service.checkAlerts(0); assert.equal(hub.notifications.length, 1);
    assert.throws(() => encodeState({ ...initial, watchlist: ["BTC-USDT", "okx:BTC-USDT"] }), /Duplicate normalized/);
  } finally { service.dispose(); }
});
for (const bar of ["1D", "T"]) test(`Tencent ${bar} checks provider code before missing data, does not cache failure and recovers immediately`, async () => {
  const { service, fixture } = await setup();
  const source = service.sources.cn;
  try {
    for (const data of [{}, []]) {
      fixture.override = url => url.pathname.includes("kline") || url.pathname.includes("minute/query") ? new Response(JSON.stringify({ code: 1, msg: "Synthetic temporary failure", data })) : undefined;
      await assert.rejects(service.candles({ source: "cn", symbol: "600000.SH" }, bar, 60), (err: ReadError) => err.code === "invalid");
      assert.equal(service.status("en").state, "error");
      assert.equal(service.http.cache.entries.size, 0);
      const before = [...fixture.hits.values()].reduce((a, b) => a + b, 0);
      fixture.override = undefined;
      assert.ok((await source.candles("600000.SH", bar, 60, 30_000)).length > 0);
      assert.equal([...fixture.hits.values()].reduce((a, b) => a + b, 0), before + 1);
      service.http.cache.entries.clear();
    }
    fixture.override = () => new Response('{"code":0,"data":{}}');
    await assert.rejects(source.candles("600000.SH", bar, 60, 30_000), (err: ReadError) => err.code === "not_found");
    assert.equal(service.http.cache.entries.size, 0);
    fixture.override = () => new Response("null");
    await assert.rejects(source.candles("600000.SH", bar, 60, 30_000), (err: ReadError) => err.code === "invalid");
    assert.equal(service.http.cache.entries.size, 0);
  } finally { service.dispose(); }
});

test("visible native list polls at 30 seconds, updates quotes and strips legacy refresh hints; hidden or disposed roots never poll", async () => {
  const { service, fixture } = await setup(), clock = new Clock(), errors: unknown[] = [];
  const renderer = new Renderer({ authorize: () => true, timers: clock, onError: e => errors.push(e) });
  try {
    renderer.mount(root({ service, locale: "en", uiVersion: 5 }));
    await until(() => renderer.current(), s => text(s).includes("Synthetic instrument"));
    assert.equal(renderer.current().view.refresh_after, undefined);
    assert.deepEqual([...clock.tasks.values()].map(t => t.ms), [30_000]);
    const before = [...fixture.hits.values()].reduce((a, b) => a + b, 0);
    fixture.last = 125; fixture.at += 31_000; clock.advance(31_000);
    await until(() => renderer.current(), s => text(s).includes('"value":"125.00"'));
    assert.ok([...fixture.hits.values()].reduce((a, b) => a + b, 0) > before);
    const stale = [...clock.tasks.values()][0].fn;
    renderer.suspend(); assert.equal(clock.tasks.size, 0);
    const hiddenHits = [...fixture.hits.values()].reduce((a, b) => a + b, 0);
    stale(); clock.advance(60_000); await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal([...fixture.hits.values()].reduce((a, b) => a + b, 0), hiddenHits);
    renderer.resume(); await until(() => clock.tasks.size, n => n === 1);
    renderer.dispose(); assert.equal(clock.tasks.size, 0); stale(); clock.advance(60_000);
    assert.deepEqual(errors, []);
  } finally { renderer.dispose(); service.dispose(); }
});
test("visible poll has an overlap guard and suspension aborts active reads without caching late quotes", async () => {
  const { service, fixture } = await setup(), clock = new Clock(), started = deferred<void>(), errors: unknown[] = []; let cancelled = false;
  const renderer = new Renderer({ authorize: () => true, timers: clock, onError: e => errors.push(e) });
  try {
    renderer.mount(root({ service, locale: "en", uiVersion: 5 }));
    await until(() => renderer.current(), s => text(s).includes("Synthetic instrument"));
    service.http.cache.entries.clear(); const before = fixture.hits.get("www.okx.com/api/v5/market/ticker")!;
    fixture.override = (_url, init) => new Promise((_yes, no) => {
      started.resolve(); init!.signal!.addEventListener("abort", () => { cancelled = true; no(init!.signal!.reason); }, { once: true });
    });
    fixture.at += 30_000; clock.advance(30_000); await started.promise;
    fixture.at += 90_000; clock.advance(90_000); await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(fixture.hits.get("www.okx.com/api/v5/market/ticker"), before + 1);
    renderer.suspend(); await until(() => cancelled, Boolean);
    assert.equal(service.http.cache.entries.size, 0); assert.equal(clock.tasks.size, 0);
    await new Promise(resolve => setTimeout(resolve, 5)); assert.deepEqual(errors, []);
  } finally { renderer.dispose(); service.dispose(); }
});
test("idle watchlists and instruments poll at five minutes without unrelated crypto benchmarks accelerating them", async () => {
  const { service } = await setup(), clock = new Clock();
  const renderer = new Renderer({ authorize: () => true, timers: clock });
  try {
    await service.store.transaction(next => { next.watchlist = ["cn:600000.SH"]; });
    renderer.mount(root({ service, locale: "en", uiVersion: 5 }));
    await until(() => renderer.current(), s => text(s).includes("Synthetic instrument"));
    assert.deepEqual([...clock.tasks.values()].map(t => t.ms), [300_000]);
    await press(renderer, "quote-cn:600000.SH");
    await until(() => renderer.current(), s => text(s).includes("section-candles"));
    assert.deepEqual([...clock.tasks.values()].map(t => t.ms), [300_000]);
    assert.equal(renderer.current().view.refresh_after, undefined);
    const state = readPageState({}, service), card = await loadPage(service, state, false, true);
    assert.equal(refreshDelay(service, state, card, true), 60_000);
    const snapshot = await snapshotPage(service).render({ locale: "en", ui_version: 5 } as any);
    assert.equal(snapshot.refresh_after, 300);
  } finally { renderer.dispose(); service.dispose(); }
});
test("native search, error and instrument branches have no legacy refresh field", async () => {
  const { service, fixture } = await setup(), clock = new Clock();
  const renderer = new Renderer({ authorize: () => true, timers: clock });
  try {
    renderer.mount(root({ service, locale: "en", state: { q: "Synthetic" } }));
    await until(() => renderer.current(), s => text(s).includes("searchTitle") || text(s).includes("Search: Synthetic"));
    assert.equal(renderer.current().view.refresh_after, undefined);
    assert.deepEqual([...clock.tasks.values()].map(t => t.ms), [30_000]);
    renderer.dispose();
    fixture.failures.set("www.okx.com", 500); service.http.cache.entries.clear();
    const failed = new Renderer({ authorize: () => true, timers: clock });
    try {
      failed.mount(root({ service, locale: "en", state: { i: "okx:BTC-USDT" } }));
      await until(() => failed.current(), s => text(s).includes("section-error"));
      assert.equal(failed.current().view.refresh_after, undefined);
      assert.deepEqual([...clock.tasks.values()].map(t => t.ms), [30_000]);
    } finally { failed.dispose(); }
  } finally { renderer.dispose(); service.dispose(); }
});

test("valid add with failed encrypted save shows saveError, rolls back and can retry via the same native callback", async () => {
  const { service, hub } = await setup(), clock = new Clock();
  const renderer = new Renderer({ authorize: () => true, timers: clock });
  try {
    renderer.mount(root({ service, locale: "en", state: { i: "okx:ETH-USDT" } }));
    await until(() => renderer.current(), s => text(s).includes("add:okx:ETH-USDT"));
    const before = service.watchlist(); hub.failSave = true;
    assert.equal((await press(renderer, "add:okx:ETH-USDT")).toast, "Could not save the change");
    assert.deepEqual(service.watchlist(), before);
    hub.failSave = false;
    assert.equal((await press(renderer, "add:okx:ETH-USDT")).toast, "Added ETH-USDT");
    assert.ok(service.watchlist().includes("okx:ETH-USDT"));
  } finally { renderer.dispose(); service.dispose(); }
});
test("typed UI1 watchlist mutations keep the normalized instrument name in native and legacy toasts", async () => {
  const { service } = await setup(), clock = new Clock();
  const renderer = new Renderer({ authorize: () => true, timers: clock });
  try {
    renderer.mount(root({ service, locale: "en", uiVersion: 1 }));
    await until(() => renderer.current(), s => text(s).includes("Synthetic instrument"));
    assert.equal((await press(renderer, "add", { inst: "eth" })).toast, "Added ETH-USDT");
    const removed = await snapshotPage(service).action!({ action: "remove:okx:ETH-USDT", locale: "en" } as any);
    assert.equal(removed!.toast, "Removed ETH-USDT");
  } finally { renderer.dispose(); service.dispose(); }
});
test("native and legacy actions distinguish invalid targets, already-met targets, duplicates, quotas and save failures", async () => {
  const { service, hub, fixture } = await setup(), clock = new Clock();
  const renderer = new Renderer({ authorize: () => true, timers: clock });
  const alert = { id: "fixture-other", ref: "okx:BTC-USDT", kind: "above" as const, value: 120, created: new Date(fixture.at).toISOString() };
  try {
    renderer.mount(root({ service, locale: "en", state: { i: "okx:BTC-USDT", f: true } }));
    await until(() => renderer.current(), s => text(s).includes("alert:okx:BTC-USDT"));
    assert.equal((await press(renderer, "alert:okx:BTC-USDT", { alert_kind: "above", alert_value: "bad" })).toast, "Invalid code or alert target");
    assert.equal((await press(renderer, "alert:okx:BTC-USDT", { alert_kind: "above", alert_value: "50" })).toast, "The alert target is already met");
    hub.failSave = true;
    assert.equal((await press(renderer, "alert:okx:BTC-USDT", { alert_kind: "above", alert_value: "120" })).toast, "Could not save the change");
    assert.equal(service.alerts().length, 0); hub.failSave = false;
    await service.store.transaction(next => { next.alerts = [alert]; });
    assert.equal((await press(renderer, "alert:okx:BTC-USDT", { alert_kind: "above", alert_value: "120" })).toast, "This pending alert already exists");
    await service.store.transaction(next => { next.alerts = Array.from({ length: 50 }, (_, i) => ({ ...alert, id: `fixture-${i}`, value: 120 + i })); });
    assert.equal((await press(renderer, "alert:okx:BTC-USDT", { alert_kind: "below", alert_value: "50" })).toast, "Alert limit reached (50)");
    const legacy = await snapshotPage(service).action!({ state: JSON.stringify({ i: "okx:BTC-USDT", f: true }), action: "alert:okx:BTC-USDT", values: { alert_kind: "above", alert_value: "120" }, locale: "en" } as any);
    assert.equal(legacy!.toast, "Alert limit reached (50)");
    await service.store.transaction(next => { next.watchlist = Array.from({ length: 50 }, (_, i) => `okx:C${i}-USDT`); });
    const limit = await snapshotPage(service).action!({ action: "add:okx:ETH-USDT", locale: "en" } as any);
    assert.equal(limit!.toast, "Watchlist limit reached (50)");
    hub.failSave = true;
    await assert.rejects(service.removeAlert("fixture-0"), (err: MutationError) => err.code === "saveError");
  } finally { renderer.dispose(); service.dispose(); }
});
