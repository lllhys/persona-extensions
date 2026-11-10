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
  const bundle = await import(pathToFileURL(resolve("build/openlibrary.mjs")).href);
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
  assert.equal(manifest.id, "openlibrary"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.equal(manifest.contributes.ui[0].id, "discover"); assert.equal(manifest.contributes.connectors[0].capabilities.includes("actions"), false);
  assert.deepEqual(manifest.permissions.network, ["openlibrary.org"]);
  assert.ok(bytes.length > 0 && bytes.length < (8 << 20));
  const bundle = await import(pathToFileURL(resolve("build/openlibrary.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "openlibrary");
});

test("protocol initialization, localized tools and unknown contributions are SDK-owned", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "openlibrary");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("検索"));
    assert.equal((await h.call("connector.tools.call", { name: "openlibrary_search", args: { query: "bad\n" } })).error.code, "invalid");
  } finally { h.p.close(); }
});

test("book search, detail navigation and public links use native server hooks", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `discover-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "discover", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "book-welcome-text");
      const searchButton = snapshot.view.sections[0].items.find(item => item.key === "book-search-submit");
      assert.ok(searchButton && "action" in searchButton && searchButton.action?.submit === true, "search submits the current field values");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "book-search-submit", { query: "synthetic" }) });
      snapshot = await settled(h, session, "Left Hand of Darkness");
      const result = await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "book-OL59800W") });
      assert.equal(result.result.status, "accepted");
      if (version >= 3) {
        assert.equal(result.result.result.push.ui, "discover");
        const child = `work-${version}`;
        await h.call("ui.page.mount", { session: child, params: { contribution: "discover", locale: "en", ui_version: version, state: result.result.result.push.state } });
        const detail = await settled(h, child, "A synthetic description of a novel.");
        assert.ok(body(detail).includes("https://openlibrary.org/works/OL59800W"));
        assert.ok(!body(detail).includes("https://attacker.invalid"));
        assert.ok(!body(detail).includes("Open public edition"));
        await h.call("ui.page.dispose", { session: child });
      } else {
        const detail = await settled(h, session, "A synthetic description of a novel.");
        await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(detail, "work-back") });
        assert.ok(body(await settled(h, session, "Pride and Prejudice")).includes("Pride and Prejudice"));
      }
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});

test("shared connector tools and cache, trusted tickets and replay controls", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "discover", locale: "en", ui_version: 5 } });
    const snapshot = await settled(h, "owned", "book-welcome-text"), event = interaction(snapshot, "book-search-submit", { query: "synthetic" });
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, accepted.snapshot.version);
    assert.equal((await h.call("connector.tools.call", { name: "openlibrary_search", args: { query: "synthetic" } })).result.text.includes("Left Hand of Darkness"), true);
    assert.equal(h.fixture.hits.get("/search.json?q=synthetic&limit=20&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia"), 1);
    await h.call("ui.page.dispose", { session: "owned" });
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new", params: { contribution: "discover", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "new", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});

test("invalid state cannot trigger arbitrary requests; pure snapshots are supported", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "bad", params: { contribution: "discover", locale: "zh-Hans", ui_version: 1, state: "not-json" } });
    await settled(h, "bad", "发现图书");
    const local = new h.bundle.OpenLibrary(h.fixture.fetch, {}, { locale: "en", now: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local);
    const view = await pure.render({ contribution: "discover", locale: "en", ui_version: 1, hub: {} });
    assert.ok(JSON.stringify(view).includes("Discover books"));
    const searched = await pure.render({ contribution: "discover", locale: "en", ui_version: 1, hub: {}, state: h.bundle.encodeState(h.bundle.transition(h.bundle.readState(undefined), "search", { query: "synthetic" })) });
    assert.ok(JSON.stringify(searched).includes("Left Hand of Darkness"));
  } finally { h.p.close(); }
});
