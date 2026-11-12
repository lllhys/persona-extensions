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
  const bundle = await import(pathToFileURL(resolve("build/rubygems.mjs")).href);
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
  assert.equal(frame.error, undefined, JSON.stringify(frame.error)); return frame.result;
};

test("source artifact and package have verified identity and no runtime imports", async () => {
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  assert.equal(manifest.id, "rubygems"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.contributes.ui[0].id, "rubygems"); assert.equal(manifest.contributes.connectors[0].capabilities.includes("actions"), false);
  assert.deepEqual(manifest.permissions.network, ["rubygems.org"]);
  assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  const bundle = await import(pathToFileURL(resolve("build/rubygems.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "rubygems");
});

test("protocol initialization, localized tools and unknown contributions are SDK-owned", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "rubygems");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("検索"));
    assert.ok((await h.call("connector.tools.call", { name: "rubygems_search", args: { query: "bad\n" } })).error);
  } finally { h.p.close(); }
});

test("search and detail navigation retain version, license, docs and install text", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `rubygems-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "rubygems", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "rubygems-welcome-text");
      const button = snapshot.view.sections[0].items.find(item => item.key === "rubygems-submit");
      assert.ok(button && "action" in button && button.action?.submit === true, "search submits current field values");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "rubygems-submit", { query: "rails" }) });
      snapshot = await settled(h, session, "rails@8.1.4.1");
      const navigation = (await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "open-rails") })).result;
      if (navigation.result.push) {
        await h.call("ui.page.mount", { session: session + "-detail", params: { contribution: "rubygems", locale: "en", ui_version: version, state: navigation.result.push.state } });
      }
      snapshot = await settled(h, navigation.result.push ? session + "-detail" : session, "gem install rails -v 8.1.4.1");
      assert.ok(body(snapshot).includes("https://rubygems.org/gems/rails"));
      assert.ok(body(snapshot).includes("https://api.rubyonrails.org/v8.1.4.1/"));
      assert.ok(body(snapshot).includes("MIT"));
      assert.ok(body(snapshot).includes("1,388,621"));
      assert.ok(body(snapshot).includes("actionpack (= 8.1.4.1)"));
      assert.ok(!body(snapshot).includes("attacker.invalid"));
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});

test("shared connector tools, trusted tickets and replay controls", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "rubygems", locale: "en", ui_version: 5 } });
    const snapshot = await settled(h, "owned", "rubygems-welcome-text"), event = interaction(snapshot, "rubygems-submit", { query: "rails" });
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    if (accepted?.snapshot) {
      const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
      assert.equal(replay.snapshot.version, accepted.snapshot.version);
    } else assert.equal(accepted.status, "accepted");
    assert.equal((await h.call("connector.tools.call", { name: "rubygems_search", args: { query: "rails" } })).result.text.includes("rails@8.1.4.1"), true);
    assert.equal(h.fixture.hits.size > 0, true);
    await h.call("ui.page.dispose", { session: "owned" });
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new", params: { contribution: "rubygems", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "new", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});

test("modern navigation mounts the validated selected gem detail", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "search", params: { contribution: "rubygems", locale: "en", ui_version: 5 } });
    const initial = await settled(h, "search", "rubygems-welcome-text");
    await h.call("ui.page.event", { session: "search", ticket: "host-ticket", interaction: interaction(initial, "rubygems-submit", { query: "rails" }) });
    const results = await settled(h, "search", "open-rack");
    const navigation = (await h.call("ui.page.event", { session: "search", ticket: "host-ticket", interaction: interaction(results, "open-rack") })).result;
    assert.equal(navigation.result.push.ui, "rubygems");
    await h.call("ui.page.mount", { session: "detail", params: { contribution: "rubygems", locale: "en", ui_version: 5, state: navigation.result.push.state } });
    const detail = await settled(h, "detail", "rack@3.2.4");
    assert.ok(body(detail).includes("rack@3.2.4"));
    assert.ok(!body(detail).includes("http://github.com"));
  } finally { h.p.close(); }
});

test("snapshot transport mounts the initial editor without a pending load", async () => {
  const h = await setup();
  try {
    const mounted = (await h.call("ui.page.mount", { session: "snapshot-editor", transport: "snapshot", params: { contribution: "rubygems", locale: "zh-Hans", ui_version: 5 } })).result;
    assert.ok(JSON.stringify(mounted).includes("gem 名称或关键词"));
    assert.equal(mounted.view.sections.some((section: { key: string }) => section.key === "rubygems-loading"), false);
  } finally { h.p.close(); }
});

test("invalid state cannot trigger arbitrary requests; pure snapshots are supported", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "bad", params: { contribution: "rubygems", locale: "ja", ui_version: 1, state: "not-json" } });
    await settled(h, "bad", "Ruby gem を検索");
    const local = new h.bundle.RubyGems(h.fixture.fetch, {}, { locale: "en", now: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local);
    const view = await pure.render({ contribution: "rubygems", locale: "en", ui_version: 1, hub: {}, state: h.bundle.encodeState({ q: "rails", found: true }) });
    assert.ok(JSON.stringify(view).includes("rails@8.1.4.1"));
  } finally { h.p.close(); }
});
