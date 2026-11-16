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
  const bundle = await import(pathToFileURL(resolve("build/domains.mjs")).href);
  const fixture = new Fixture(), frames: any[] = [];
  const p = connect(bundle.createProgram(fixture.fetch, { now: () => fixture.clock, monotonic: () => fixture.clock, limits: { gap: 0 } }, new bundle.MemoryHistory()) as Program, line => frames.push(JSON.parse(line)));
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
test("source artifact has verified identity and only declared authority", async () => {
  const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
  const bytes = await readFile("build/package/" + manifest.runtime.source.bundle);
  assert.equal(manifest.id, "domains"); assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.sha256, createHash("sha256").update(bytes).digest("hex"));
  assert.deepEqual(manifest.permissions.network, ["data.iana.org", "rdap.verisign.com"]);
  assert.deepEqual(manifest.permissions.storage, { keys: ["history"] });
  assert.equal(manifest.permissions.hub, undefined);
  const bundle = await import(pathToFileURL(resolve("build/domains.mjs")).href);
  assert.equal(bundle.default.fetch, undefined); assert.equal(bundle.default.id, "domains");
});
test("protocol, localized tools and unknown contributions follow the SDK", async () => {
  const h = await setup(false);
  try {
    assert.equal((await h.call("connector.status")).error.code, "not_initialized");
    assert.equal((await h.call("initialize", { protocol: 2 })).error.code, "wrong_protocol");
    assert.equal((await h.call("initialize", { protocol: 1, config: {}, hub: { locale: "ja" } })).result.id, "domains");
    assert.deepEqual((await h.call("ping")).result, {});
    assert.equal((await h.call("connector.status", { contribution: "missing" })).error.code, "no_contribution");
    assert.equal((await h.call("connector.unsupported", {})).error.code, "unknown_method");
    assert.ok((await h.call("connector.tools.list", {})).result.tools[0].description.includes("ドメイン"));
    assert.equal((await h.call("connector.tools.call", { name: "domains_lookup", args: { domain: "https://example.com" } })).error.code, "invalid");
  } finally { h.p.close(); }
});
test("lookup, detail navigation and history work with both native navigation contracts", async () => {
  const h = await setup();
  try {
    for (const version of [1, 5]) {
      const session = `lookup-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "lookup", locale: "en", ui_version: version } });
      let snapshot = await settled(h, session, "domain-welcome-text");
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "domain-submit", { domain: "EXAMPLE.com" }) });
      snapshot = await settled(h, session, "Example Registrar");
      assert.ok(body(snapshot).includes("client transfer prohibited"));
      assert.ok(body(snapshot).includes("https://www.iana.org/assignments/rdap-status-values/rdap-status-values.xhtml"));
      await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "domain-history") });
      snapshot = await settled(h, session, "history-example.com");
      const opened = await h.call("ui.page.event", { session, ticket: "host-ticket", interaction: interaction(snapshot, "history-example.com") });
      if (version >= 3) {
        assert.equal(opened.result.result.push.ui, "lookup");
        await h.call("ui.page.mount", { session: "child", params: { contribution: "lookup", locale: "en", ui_version: version, state: opened.result.result.push.state } });
        assert.ok(body(await settled(h, "child", "Example Registrar")).includes("https://rdap.verisign.com/com/v1/domain/example.com"));
        await h.call("ui.page.dispose", { session: "child" });
      } else assert.ok(body(await settled(h, session, "Example Registrar")).includes("Example Registrar"));
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});
test("history clear, replay and three-language pure snapshots remain safe", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "owned", params: { contribution: "lookup", locale: "en", ui_version: 5 } });
    const welcome = await settled(h, "owned", "domain-welcome-text"), event = interaction(welcome, "domain-submit", { domain: "example.com" });
    assert.ok((await h.call("ui.page.event", { session: "owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: event })).result;
    const replay = (await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: { ...event, eventId: crypto.randomUUID() } })).result;
    assert.ok(replay.snapshot.version >= accepted.snapshot.version);
    await settled(h, "owned", "Example Registrar");
    await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: interaction(await settled(h, "owned", "Example Registrar"), "domain-history") });
    const history = await settled(h, "owned", "history-example.com");
    await h.call("ui.page.event", { session: "owned", ticket: "host-ticket", interaction: interaction(history, "history-clear") });
    await settled(h, "owned", "Recent lookups cleared.");
    assert.equal((await h.call("connector.tools.call", { name: "domains_lookup", args: { domain: "example.com" } })).result.text.includes("Example Registrar"), true);
    const remount: Snapshot = (await h.call("ui.page.mount", { session: "new", params: { contribution: "lookup", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, welcome.generation);
    assert.equal((await h.call("ui.page.event", { session: "new", ticket: "host-ticket", interaction: event })).result.status, "ambiguous");
    const local = new h.bundle.Domains(h.fixture.fetch, {}, { locale: "en", now: () => h.fixture.clock, limits: { gap: 0 } });
    const pure = h.bundle.snapshotPage(local, new h.bundle.MemoryHistory());
    for (const [locale, expected] of [["en", "Domain registration"], ["zh-Hans", "域名注册信息"], ["ja", "ドメイン登録情報"]]) {
      assert.ok(JSON.stringify(await pure.render({ contribution: "lookup", locale, ui_version: 1, hub: {} })).includes(expected));
    }
  } finally { h.p.close(); }
});
