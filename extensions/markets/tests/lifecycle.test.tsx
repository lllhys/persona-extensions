import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "@persona/extension/ui";
import { fingerprint, loading } from "../src/page.tsx";
import { root } from "../src/root.tsx";
import { emptyQuote, type SourceID } from "../src/model.ts";
import { ReadError } from "../src/http.ts";
import { Markets } from "../src/service.ts";
import { FakeHub, Fixture, until } from "./fixtures.ts";

test("quote/error fingerprints ignore asynchronous Map insertion order without masking data changes", () => {
  const a = { ...loading(), kind: "list" as const,
    quotes: new Map([["okx:BTC-USDT", emptyQuote("BTC-USDT")], ["cn:600000.SH", emptyQuote("600000.SH")]]),
    errors: new Map<SourceID, unknown>([["hk", new ReadError("rate_limited", "Synthetic rate error", 429)], ["us", new ReadError("invalid", "Synthetic provider error", 500)]]) };
  const b = { ...a, quotes: new Map([...a.quotes].reverse()), errors: new Map([...a.errors].reverse()), at: a.at + 1000 };
  const order = [...b.quotes.keys()];
  assert.equal(fingerprint(a), fingerprint(b));
  assert.deepEqual([...b.quotes.keys()], order, "Fingerprint must not reorder a shared cache");
  b.quotes.set("okx:BTC-USDT", { ...emptyQuote("BTC-USDT"), last: 101 });
  assert.notEqual(fingerprint(a), fingerprint(b));
  b.quotes = new Map(a.quotes);
  b.errors.set("us", new ReadError("invalid", "Synthetic provider error", 502));
  assert.notEqual(fingerprint(a), fingerprint(b));
});

test("native resume consumes exactly one revision when quote completion order changes, retaining fresh callbacks", async () => {
  const fixture = new Fixture(), service = new Markets(new FakeHub(), {}, { fetch: fixture.fetch, clock: () => fixture.at, interval: () => () => {} });
  await service.initialize();
  const read = service.quotes.bind(service);
  let reversed = false;
  service.quotes = async (...args) => {
    const data = await read(...args);
    // Model equal reads assembled by independently completing provider/cache tasks.
    const entries = [...data.quotes];
    if (reversed) entries.reverse();
    const quotes = new Map<string, typeof entries[number][1]>();
    for (const entry of entries) await Promise.resolve().then(() => quotes.set(...entry));
    return { quotes, errors: new Map(reversed ? [...data.errors].reverse() : data.errors) };
  };
  const errors: unknown[] = [], renderer = new Renderer({ authorize: () => true, onError: error => errors.push(error) });
  try {
    renderer.mount(root({ service, locale: "en" }));
    await renderer.settle(AbortSignal.timeout(3000), 3000);
    const mounted = renderer.current();
    assert.ok(!JSON.stringify(mounted.view).includes('"loading"'));
    const old = mounted.view.sections.flatMap(s => s.items).find(i => i.key === "quote-okx:BTC-USDT") as any;
    renderer.suspend(); reversed = true;
    const resumed = renderer.resume();
    // This is the native Renderer revision, not the manager's coalesced wire version.
    assert.equal(resumed.version, mounted.version + 1);
    assert.equal(resumed.generation, mounted.generation);
    await renderer.settle(AbortSignal.timeout(3000), 3000);
    assert.equal(renderer.current().version, resumed.version, "Equal cache content must not create a second resume render");
    await assert.rejects(renderer.event({ generation: mounted.generation, version: mounted.version, key: old.key, actionId: old.action.id, eventId: "fixture-stale-resume", values: {} }));
    const fresh = resumed.view.sections.flatMap(s => s.items).find(i => i.key === old.key) as any;
    await renderer.event({ generation: resumed.generation, version: resumed.version, key: fresh.key, actionId: fresh.action.id, eventId: "fixture-fresh-resume", values: {} });
    await renderer.settle(AbortSignal.timeout(3000), 3000);
    await until(() => renderer.current(), s => JSON.stringify(s.view).includes("section-candles"));
    assert.deepEqual(errors, []);
  } finally { renderer.dispose(); service.dispose(); }
});
