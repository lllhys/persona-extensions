import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createHash } from "node:crypto";
import { connect, type Program } from "@persona/extension";
import type { Snapshot } from "@persona/extension/ui";
import { Fixture, interaction, until } from "./fixtures.ts";
async function setup(initialize = true) {
  const bundle = await import(pathToFileURL(resolve("build/currency.mjs")).href);
  const fixture = new Fixture(), frames: any[] = [];
  const p = connect(bundle.createProgram(fixture.fetch, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 } }) as Program, line => frames.push(JSON.parse(line)));
  let id = 0;
  const call = async (method: string, params: unknown = {}) => {
    const request = String(++id); p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    const frame = frames.find(frame => frame.id === request); assert.ok(frame); return frame;
  };
  if (initialize) await call("initialize", { protocol: 1, config: {}, hub: { locale: "en", timezone: "UTC" } });
  return { bundle, fixture, frames, p, call };
}
const body = (snapshot: Snapshot) => JSON.stringify(snapshot.view);
const settled = async (h: Awaited<ReturnType<typeof setup>>, session: string, token: string): Promise<Snapshot> => {
  const frame = await until(() => h.call("ui.page.snapshot", { session }), frame => !!frame.error || JSON.stringify(frame.result).includes(token));
  assert.equal(frame.error, undefined, JSON.stringify({ error: frame.error, snapshot: frame.result, frames: h.frames })); return frame.result;
};

test("source artifact and package have verified identity and no runtime imports", async () => {
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  assert.equal(manifest.id, "currency"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.contributes.ui[0].id, "converter"); assert.equal(manifest.contributes.connectors[0].capabilities.includes("actions"), false);
  assert.deepEqual(manifest.permissions.network, ["api.frankfurter.dev"]);
  assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  const bundle = await import(pathToFileURL(resolve("build/currency.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "currency");
});

test("protocol initialization, localized tools and unknown contributions are SDK-owned", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "currency");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("欧州中央銀行"));
    assert.ok((await h.call("connector.tools.call", { name: "currency_convert", args: { amount: "1", from: "USD\n", to: "CNY" } })).error);
  } finally { h.p.close(); }
});

test("currency and local unit conversion render native results with date and attribution", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `currency-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "converter", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "currency-welcome-text");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "currency-convert", { amount: "100", from: "USD", to: "CNY" }) });
      snapshot = await settled(h, session, "667.03 CNY");
      assert.ok(body(snapshot).includes("2026-11-10")); assert.ok(body(snapshot).includes("European Central Bank"));
      assert.ok(body(snapshot).includes("1 USD = 6.6703 CNY"));
      assert.ok(body(snapshot).includes("https://frankfurter.dev/"));
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, version >= 2 ? "currency-mode" : "mode-units", { tab: "units" }) });
      snapshot = (await h.call("ui.page.snapshot", { session })).result;
      assert.equal(JSON.parse(snapshot.view.state ?? "{}").mode, "units");
      const localEvent = await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "currency-convert", { amount: "100", from: "C", to: "F" }) });
      assert.equal(localEvent.error, undefined, JSON.stringify(localEvent));
      snapshot = await settled(h, session, "212 °F");
      assert.ok(body(snapshot).includes("1 °C = 1.8 °F"));
      assert.ok(body(snapshot).includes("no network request"));
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});

test("shared connector tools and cache, trusted tickets and replay controls", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "converter", locale: "en", ui_version: 5 } });
    const snapshot = await settled(h, "owned", "currency-welcome-text"), event = interaction(snapshot, "currency-convert", { amount: "100", from: "USD", to: "CNY" });
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    if (accepted?.snapshot) {
      const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
      assert.equal(replay.snapshot.version, accepted.snapshot.version);
    } else assert.equal(accepted.status, "accepted");
    assert.equal((await h.call("connector.tools.call", { name: "currency_convert", args: { amount: "1", from: "USD", to: "CNY" } })).result.text.includes("6.6703 CNY"), true);
    assert.equal(h.fixture.hits.size > 0, true);
    await h.call("ui.page.dispose", { session: "owned" });
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new", params: { contribution: "converter", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "new", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});

test("snapshot transport mounts the initial editor without a pending load", async () => {
  const h = await setup();
  try {
    const mounted = (await h.call("ui.page.mount", { session: "snapshot-editor", transport: "snapshot", params: { contribution: "converter", locale: "zh-Hans", ui_version: 5 } })).result;
    assert.ok(JSON.stringify(mounted).includes("换算常用货币"));
    assert.equal(mounted.view.sections.some((section: { key: string }) => section.key === "currency-loading"), false);
  } finally { h.p.close(); }
});

test("invalid state cannot trigger arbitrary requests; pure snapshots are supported", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "bad", params: { contribution: "converter", locale: "zh-Hans", ui_version: 1, state: "not-json" } });
    await settled(h, "bad", "汇率和单位换算");
    const local = new h.bundle.CurrencyService(h.fixture.fetch, {}, { locale: "en", now: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local);
    const view = await pure.render({ contribution: "converter", locale: "en", ui_version: 1, hub: {}, state: h.bundle.encodeState({ mode: "currency", amount: "100", from: "USD", to: "CNY", dimension: "length", converted: true }) });
    assert.ok(JSON.stringify(view).includes("667.03 CNY"));
  } finally { h.p.close(); }
});
