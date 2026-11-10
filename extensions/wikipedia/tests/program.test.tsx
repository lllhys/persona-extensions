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
  const bundle = await import(pathToFileURL(resolve("build/wikipedia.mjs")).href);
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
  assert.equal(manifest.id, "wikipedia"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.contributes.ui[0].id, "today"); assert.equal(manifest.contributes.connectors[0].capabilities.includes("actions"), false);
  assert.deepEqual(manifest.permissions.network, ["en.wikipedia.org", "zh.wikipedia.org", "ja.wikipedia.org"]);
  assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  const bundle = await import(pathToFileURL(resolve("build/wikipedia.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "wikipedia");
});
test("protocol initialization, localized tools and unknown contributions are SDK-owned", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.ok((await h.call("initialize", { protocol: 1, config: { language: "fr" } })).error);
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "wikipedia");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("今日の"));
    assert.equal((await h.call("connector.tools.call", { name: "wikipedia_search", args: { query: "bad\n" } })).error.code, "invalid");
  } finally { h.p.close(); }
});
test("daily tabs, search and article navigation use native server hooks", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `daily-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "today", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "Featured article");
      assert.equal(snapshot.view.sections[1].items[0].type, version < 2 ? "row" : "segmented");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: version >= 2 ? interaction(snapshot, "tab", { tab: "popular" }) : interaction(snapshot, "tab-popular") });
      snapshot = await settled(h, session, "Popular article");
      assert.ok(!body(snapshot).includes("article-0-Featured_article"));
      const searchButton = snapshot.view.sections[0].items.find(item => item.key === "do-search");
      assert.ok(searchButton && "action" in searchButton && searchButton.action?.submit === true, "search submits the current field values");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "do-search", { query: "synthetic" }) });
      snapshot = await settled(h, session, "Found article");
      const result = await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "article-0-Found_article") });
      assert.equal(result.result.status, "accepted");
      if (version >= 3) {
        assert.equal(result.result.result.push.ui, "today");
        const child = `article-${version}`;
        await h.call("ui.page.mount", { session: child, params: { contribution: "today", locale: "en", ui_version: version, state: result.result.result.push.state } });
        const detail = await settled(h, child, "A found synthetic introduction");
        assert.ok(body(detail).includes("A found synthetic introduction")); assert.ok(!body(detail).includes("attacker.invalid"));
        await h.call("ui.page.dispose", { session: child });
      } else {
        const detail = await settled(h, session, "A found synthetic introduction");
        await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(detail, "back-button") });
        assert.ok(body(await settled(h, session, "Found article")).includes("Found article"));
      }
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});
test("shared connector tools and cache, trusted tickets and replay controls", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "today", locale: "en", ui_version: 5 } });
    const snapshot = await settled(h, "owned", "Featured article"), event = interaction(snapshot, "tab", { tab: "popular" });
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, accepted.snapshot.version);
    assert.equal((await h.call("connector.tools.call", { name: "wikipedia_today", args: {} })).result.text.includes("Featured article"), true);
    assert.equal(h.fixture.hits.get("/api/rest_v1/feed/featured/2026/11/09"), 1);
    await h.call("ui.page.dispose", { session: "owned" });
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new", params: { contribution: "today", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "new", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});
test("invalid state cannot trigger arbitrary requests; pure snapshots are supported", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "bad", params: { contribution: "today", locale: "zh-Hans", ui_version: 1, state: "not-json" } });
    await settled(h, "bad", "今日精选");
    const local = new h.bundle.Wikipedia(h.fixture.fetch, {}, { locale: "en", now: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local);
    const view = await pure.render({ contribution: "today", locale: "en", ui_version: 1, hub: {} });
    assert.ok(JSON.stringify(view).includes("Featured article"));
    const searched = await pure.action({ contribution: "today", locale: "en", ui_version: 1, hub: {}, action: "search", values: JSON.stringify({ query: "synthetic" }) as unknown as Record<string, string> });
    assert.ok(JSON.stringify(searched).includes("Found article"));
  } finally { h.p.close(); }
});
