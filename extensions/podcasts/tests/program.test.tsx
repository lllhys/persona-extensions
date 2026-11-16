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
  const bundle = await import(pathToFileURL(resolve("build/podcasts.mjs")).href);
  const fixture = new Fixture(), frames: any[] = [];
  const p = connect(bundle.createProgram(fixture.fetch, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 } }, new bundle.MemoryStore()) as Program, line => frames.push(JSON.parse(line)));
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
  assert.equal(manifest.id, "podcasts"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.contributes.ui[0].id, "discover"); assert.equal(manifest.contributes.connectors[0].capabilities.includes("actions"), false);
  assert.deepEqual(manifest.permissions.network, ["itunes.apple.com"]);
  assert.deepEqual(manifest.permissions.storage, { keys: ["subscriptions"] });
  assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  const bundle = await import(pathToFileURL(resolve("build/podcasts.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "podcasts");
});

test("protocol initialization, localized tools and unknown contributions are SDK-owned", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "podcasts");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("検索"));
    assert.equal((await h.call("connector.tools.call", { name: "podcasts_search", args: { query: "bad\n" } })).error.code, "invalid");
    assert.equal((await h.call("connector.tools.call", { name: "podcasts_episodes", args: { show: 1, limit: 31 } })).error.code, "invalid");
  } finally { h.p.close(); }
});

test("show search, episode links, navigation and local saving use native server hooks", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `discover-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "discover", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "podcast-welcome-text");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "podcast-search-submit", { query: "synthetic" }) });
      snapshot = await settled(h, session, "Example Engineering");
      const result = await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "show-1860072717") });
      assert.equal(result.result.status, "accepted");
      if (version >= 3) {
        assert.equal(result.result.result.push.ui, "discover");
        const child = `show-${version}`;
        await h.call("ui.page.mount", { session: child, params: { contribution: "discover", locale: "en", ui_version: version, state: result.result.result.push.state } });
        let detail = await settled(h, child, "A useful conversation");
        assert.ok(body(detail).includes("https://podcasts.apple.com/us/podcast/id1860072717?i=1000792066218"));
        assert.ok(!body(detail).includes("https://attacker.invalid"));
        assert.ok(!body(detail).includes("https://media.example.com/audio.mp3"));
        await h.call("ui.page.event", { session: child, ticket: "host-ticket", interaction: interaction(detail, "show-save") });
        detail = await settled(h, child, "Saved to your listening list.");
        assert.ok(body(detail).includes("Remove from listening list"));
        const back = await h.call("ui.page.event", { session: child, ticket: "host-ticket", interaction: interaction(detail, "show-back") });
        assert.equal(back.result.result.pop, true);
        await h.call("ui.page.dispose", { session: child });
      } else {
        let detail = await settled(h, session, "A useful conversation");
        await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(detail, "show-save") });
        detail = await settled(h, session, "Saved to your listening list.");
        await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(detail, "show-back") });
        assert.ok(body(await settled(h, session, "Another Show")).includes("Another Show"));
      }
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});

test("listening list persists within the injected local store", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "discover", locale: "en", ui_version: 5 } });
    const welcome = await settled(h, "owned", "podcast-welcome-text"), event = interaction(welcome, "podcast-search-submit", { query: "synthetic" });
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, accepted.snapshot.version);
    const search = await settled(h, "owned", "Example Engineering");
    const opened = await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: interaction(search, "show-1860072717") });
    const childState = opened.result.result.push.state;
    await h.call("ui.page.mount", { session: "child", params: { contribution: "discover", locale: "en", ui_version: 5, state: childState } });
    const detail = await settled(h, "child", "A useful conversation");
    await h.call("ui.page.event", { session: "child", ticket: "host-ticket", interaction: interaction(detail, "show-save") });
    await settled(h, "child", "Saved to your listening list.");
    await h.call("ui.page.mount", { session: "library", params: { contribution: "discover", locale: "en", ui_version: 5, state: JSON.stringify({ v: "library", q: "", k: 0 }) } });
    assert.ok(body(await settled(h, "library", "Example Engineering")).includes("Your list stays in this Persona installation"));
    assert.equal((await h.call("connector.tools.call", { name: "podcasts_search", args: { query: "synthetic" } })).result.text.includes("Example Engineering"), true);
    assert.equal(h.fixture.hits.get("/search?term=synthetic&media=podcast&entity=podcast&limit=20"), 1);
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new", params: { contribution: "discover", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, welcome.generation);
    assert.equal((await h.call("ui.page.event", { session: "new", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});

test("invalid state and three-language pure snapshots are supported", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "bad", params: { contribution: "discover", locale: "zh-Hans", ui_version: 1, state: "not-json" } });
    await settled(h, "bad", "发现播客");
    const store = new h.bundle.MemoryStore(), local = new h.bundle.Podcasts(h.fixture.fetch, {}, { locale: "en", now: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local, store);
    for (const [locale, expected] of [["en", "Discover podcasts"], ["zh-Hans", "发现播客"], ["ja", "ポッドキャストを探す"]]) {
      const view = await pure.render({ contribution: "discover", locale, ui_version: 1, hub: {} });
      assert.ok(JSON.stringify(view).includes(expected));
    }
    const searched = await pure.render({ contribution: "discover", locale: "en", ui_version: 1, hub: {}, state: h.bundle.encodeState(h.bundle.transition(h.bundle.readState(undefined), "search", { query: "synthetic" })) });
    assert.ok(JSON.stringify(searched).includes("Example Engineering"));
  } finally { h.p.close(); }
});
