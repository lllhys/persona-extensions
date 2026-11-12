import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, writeFile, mkdtemp, stat, rm, mkdir, symlink } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { connect, type Program } from "@persona/extension";
import type { Snapshot, UIEvent } from "@persona/extension/ui";
import { Fixture, FakeHub, until } from "./fixtures.ts";
const extension = resolve(import.meta.dirname, "../..");
async function setup(config = {}) {
  const bundle = await import(pathToFileURL(resolve(extension, "build/markets.mjs")).href);
  const fixture = new Fixture(), secrets = new FakeHub(), frames: any[] = [];
  const program = bundle.createProgram({ fetch: fixture.fetch, clock: () => fixture.at, interval: () => () => {}, id: () => "fixture-alert" }) as Program;
  const p = connect(program, line => {
    const frame = JSON.parse(line); frames.push(frame);
    if (frame.method?.startsWith("connector.secrets.")) void secrets.call(frame.method, frame.params).then(result => p.line(JSON.stringify({ id: frame.id, result })), () => p.line(JSON.stringify({ id: frame.id, error: { message: "Synthetic storage outage" } })));
  });
  let id = 0;
  const call = async (method: string, params: unknown = {}) => {
    const request = `fixture-${++id}`; p.line(JSON.stringify({ id: request, method, params })); await p.drain();
    return frames.find(frame => frame.id === request);
  };
  const callRaw = async (method: string, params: string) => {
    const request = `fixture-${++id}`; p.line(`{"id":${JSON.stringify(request)},"method":${JSON.stringify(method)},"params":${params}}`); await p.drain();
    return frames.find(frame => frame.id === request);
  };
  assert.equal((await call("initialize", { protocol: 1, config })).result.id, "markets");
  return { fixture, secrets, frames, p, call, callRaw };
}
function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key) as any;
  assert.ok(item?.action?.id, `Missing native callback ${key}`);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
const settled = (h: Awaited<ReturnType<typeof setup>>, session: string, text = "Synthetic instrument") => until(() => h.call("ui.page.snapshot", { session }), frame => JSON.stringify(frame.result?.view).includes(text)).then(frame => frame.result as Snapshot);

test("source artifact is import-free default Program with original IDs, config, permissions and version", async () => {
  const original = JSON.parse(await readFile(resolve(extension, "persona-extension.json"), "utf8"));
  const manifest = JSON.parse(await readFile(resolve(extension, "build/package/persona-extension.json"), "utf8"));
  const bytes = await readFile(resolve(extension, "build/package", manifest.runtime.source.bundle));
  assert.equal(createHash("sha256").update(bytes).digest("hex"), manifest.runtime.source.sha256);
  if (original.runtime.mode === "deno") assert.equal(original.runtime.source.sha256, manifest.runtime.source.sha256); // Standalone authoring archive.
  else { assert.ok(original.runtime.command); assert.equal(original.runtime.mode, undefined); }
  assert.equal(manifest.version, original.version);
  assert.equal(manifest.runtime.mode, "deno"); assert.equal(manifest.runtime.command, undefined);
  assert.equal(manifest.runtime.source.entry, "extensions/markets/src/index.ts");
  assert.equal(manifest.runtime.source.bundle, "build/markets.mjs");
  assert.ok((await readFile(resolve(extension, "build/package", manifest.runtime.source.entry))).length > 0);
  assert.deepEqual(manifest.contributes, original.contributes); assert.deepEqual(manifest.configuration, original.configuration); assert.deepEqual(manifest.permissions, original.permissions);
  assert.deepEqual(manifest.contributes.ui.map((ui: any) => ui.id), ["watchlist", "card"]);
  const lock = JSON.parse(await readFile(resolve(extension, "package-lock.json"), "utf8"));
  for (const [path, entry] of Object.entries(lock.packages) as Array<[string, any]>) if (entry.resolved) {
    if (path === "node_modules/@persona/extension" && entry.link === true) assert.equal(entry.resolved, "../../sdk/ts");
    else {
      const url = new URL(entry.resolved);
      assert.equal(url.protocol, "https:"); assert.equal(url.host, "registry.npmjs.org");
      assert.equal(url.username, ""); assert.equal(url.password, ""); assert.equal(url.search, ""); assert.equal(url.hash, "");
      assert.ok(entry.integrity?.startsWith("sha512-"), "Public package integrity is required");
    }
  }
  const program = (await import(pathToFileURL(resolve(extension, "build/markets.mjs")).href)).default;
  assert.equal(program.id, "markets"); assert.equal(program.fetch, undefined); assert.equal(typeof program.namespaces.connector, "function");
  assert.ok(bytes.length < 8 * 1024 * 1024); assert.ok(!/\b(?:import|require)\s*\(/.test(bytes.toString()));
});
test("source packaging refuses unapproved resolved origins, credentials and alternate SDK links before producing an artifact", async () => {
  const dir = await mkdtemp(resolve(extension, "build/lock-negative-"));
  try {
    await mkdir(resolve(dir, "scripts"));
    await writeFile(resolve(dir, "scripts/build-source.mjs"), await readFile(resolve(extension, "scripts/build-source.mjs")));
    await writeFile(resolve(dir, "scripts/archive.mjs"), await readFile(resolve(extension, "scripts/archive.mjs")));
    await writeFile(resolve(dir, "persona-extension.json"), await readFile(resolve(extension, "persona-extension.json")));
    await symlink(resolve(extension, "node_modules"), resolve(dir, "node_modules"), "dir");
    const lock = JSON.parse(await readFile(resolve(extension, "package-lock.json"), "utf8"));
    const credentialSource = new URL("https://registry.npmjs.org/package.tgz");
    credentialSource.username = "fixture";
    assert.equal(credentialSource.origin, "https://registry.npmjs.org");
    assert.equal(credentialSource.username, "fixture");
    for (const bad of ["https://example.com/package.tgz", credentialSource.href, "https://registry.npmjs.org/package.tgz?token=fixture", "../../other-sdk"]) {
      const variant = structuredClone(lock);
      const entry = bad.startsWith("../") ? variant.packages["node_modules/@persona/extension"] : variant.packages["node_modules/typescript"];
      entry.resolved = bad;
      await writeFile(resolve(dir, "package-lock.json"), JSON.stringify(variant));
      const result = spawnSync(process.execPath, [resolve(dir, "scripts/build-source.mjs")], { cwd: dir, encoding: "utf8", timeout: 10_000 });
      assert.notEqual(result.status, 0);
      assert.ok(!result.stderr.includes(bad), "Failure must not print a rejected registry URL");
      await assert.rejects(stat(resolve(dir, "build/markets-source.tar.gz")), { code: "ENOENT" });
    }
  } finally { await rm(dir, { recursive: true, force: true }); }
});
test("synthetic credential fixtures retain exactly the existing approved-origin boundaries", () => {
  const registry = new URL("https://registry.npmjs.org/package.tgz"); registry.username = "fixture";
  const provider = new URL("https://www.okx.com/x"); provider.username = "user";
  const query = new URL("https://registry.npmjs.org/package.tgz"); query.searchParams.set("token", "fixture");
  const probe = `import importlib.util, json, pathlib, sys
path = pathlib.Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("fixture_policy", path)
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)
actual = policy.synthetic_credential_fixtures()
assert actual == set(json.load(sys.stdin)) and len(actual) == 3
print("fixtures: PASS (three unchanged boundaries)")
`;
  const result = spawnSync("python3", ["-I", "-B", "-c", probe, resolve(extension, "scripts/verify-source.py")], {
    input: JSON.stringify([registry.href, provider.href, query.href]), encoding: "utf8", timeout: 5000,
  });
  assert.equal(result.status, 0);
  assert.match(result.stdout, /fixtures: PASS/);
  assert.equal(result.stderr, "");
});
test("privacy verifier treats grep PCRE compilation exit 2 as failure, never PASS", async () => {
  const dir = await mkdtemp(resolve(extension, "build/pcre-negative-"));
  try {
    const terms = resolve(dir, "terms.txt"); await writeFile(terms, "[\n");
    const result = spawnSync("python3", ["-I", resolve(extension, "scripts/verify-source.py"), "--terms", terms], { cwd: extension, encoding: "utf8", timeout: 30_000 });
    assert.equal(result.status, 1); assert.match(result.stdout, /FAIL invalid PCRE rule 1/); assert.ok(!result.stdout.includes("["));
  } finally { await rm(dir, { recursive: true, force: true }); }
});
test("bundled page respects UI1/3/5 branches and inline open/back preserves parent search/sort/filter", async () => {
  const h = await setup();
  try {
    for (const version of [1, 3, 5]) {
      const session = `fixture-version-${version}`;
      await h.call("ui.page.mount", { session, params: { contribution: "watchlist", ui_version: version, locale: "en", state: JSON.stringify({ s: "down", m: "cn" }) } });
      let snapshot = await settled(h, session);
      assert.equal(snapshot.view.refresh_after, undefined);
      assert.equal(!!snapshot.view.search, version >= 3);
      assert.equal((await h.call("ui.page.event", { session, ticket: "fixture-host-ticket", interaction: interaction(snapshot, "quote-cn:600000.SH") })).result.status, "accepted");
      snapshot = await settled(h, session, "section-candles");
      assert.equal(snapshot.view.sections.flatMap(s => s.items).some(i => i.type === "chart"), version >= 2);
      assert.equal(JSON.parse(snapshot.view.state!).s, "down"); assert.equal(JSON.parse(snapshot.view.state!).m, "cn");
      const back = await h.call("ui.page.event", { session, ticket: "fixture-host-ticket", interaction: interaction(snapshot, "back") });
      assert.equal(back.result.status, "accepted"); snapshot = await settled(h, session);
      assert.equal(JSON.parse(snapshot.view.state!).i, ""); assert.equal(JSON.parse(snapshot.view.state!).s, "down");
      await h.call("ui.page.dispose", { session });
    }
  } finally { h.p.close(); }
});
test("separately bundled Program shares caches across connector tools, native pages and original card snapshots", async () => {
  const h = await setup();
  try {
    assert.deepEqual((await h.call("ui.page.supported")).result, { contributions: ["watchlist"] });
    assert.equal((await h.call("ui.render", { contribution: "watchlist" })).error.code, "unsupported");
    assert.equal((await h.call("ui.action", { contribution: "watchlist", action: "refresh" })).error.code, "unsupported");
    await h.call("ui.page.mount", { session: "fixture-shared", params: { contribution: "watchlist", locale: "en", ui_version: 5 } });
    await settled(h, "fixture-shared");
    const before = h.fixture.hits.get("www.okx.com/api/v5/market/ticker");
    const tool = await h.call("connector.tools.call", { contribution: "markets", name: "markets_quote", args: { symbols: ["BTC-USDT"] }, locale: "en" });
    assert.match(tool.result.text, /okx:BTC-USDT/);
    assert.equal(h.fixture.hits.get("www.okx.com/api/v5/market/ticker"), before);
    const card = await h.call("ui.render", { contribution: "card", locale: "en", ui_version: 5 }); assert.ok(card.result.sections.length > 0);
    assert.ok(!card.result.sections.flatMap((s: any) => s.items).some((i: any) => i.action));
    assert.equal((await h.call("connector.status", { contribution: "markets", locale: "en" })).result.state, "ok");
    assert.equal((await h.call("connector.tools.list", { contribution: "markets" })).result.tools.length, 3);
    assert.equal((await h.call("connector.status", { contribution: "foreign" })).error.code, "no_contribution");
  } finally { h.p.close(); }
});
test("saved callback requires host ticket, replays once, rejects stale revisions and separates remount generations", async () => {
  const h = await setup();
  try {
    await h.call("ui.page.mount", { session: "fixture-owned", params: { contribution: "watchlist", locale: "en", ui_version: 5 } });
    let snapshot = await settled(h, "fixture-owned");
    const event = interaction(snapshot, "quote-okx:BTC-USDT");
    assert.ok((await h.call("ui.page.event", { session: "fixture-owned", interaction: event })).error);
    const accepted = (await h.call("ui.page.event", { session: "fixture-owned", ticket: "fixture-host-ticket", interaction: event })).result;
    assert.equal(accepted.status, "accepted");
    snapshot = await settled(h, "fixture-owned", "section-candles");
    const replay = (await h.call("ui.page.event", { session: "fixture-owned", ticket: "fixture-host-ticket", interaction: event })).result;
    assert.equal(replay.snapshot.version, snapshot.version);
    assert.deepEqual(replay.result, accepted.result);
    assert.equal((await h.call("ui.page.event", { session: "fixture-owned", ticket: "fixture-host-ticket", interaction: { ...event, eventId: crypto.randomUUID() } })).result.status, "ambiguous");
    await h.call("ui.page.suspend", { session: "fixture-owned" }); const resumed = (await h.call("ui.page.resume", { session: "fixture-owned" })).result;
    await new Promise(resolve => setTimeout(resolve, 10)); const unchanged = (await h.call("ui.page.snapshot", { session: "fixture-owned" })).result;
    assert.equal(resumed.version, unchanged.version); assert.equal(snapshot.generation, unchanged.generation);
    await h.call("ui.page.dispose", { session: "fixture-owned" });
    const remount = (await h.call("ui.page.mount", { session: "fixture-new", params: { contribution: "watchlist", locale: "en", ui_version: 5 } })).result;
    assert.notEqual(remount.generation, snapshot.generation);
    assert.equal((await h.call("ui.page.event", { session: "fixture-new", ticket: "fixture-host-ticket", interaction: event })).result.status, "ambiguous");
  } finally { h.p.close(); }
});
test("offline legacy converter preserves original files, persists all alert fields and refuses overwrites", async () => {
  const dir = await mkdtemp(resolve(extension, "build/conversion-fixture-"));
  try {
    const watch = resolve(dir, "watchlist.json"), alerts = resolve(dir, "alerts.json"), output = resolve(dir, "new-sealed.json");
    const oldAlert = { id: "fixture-legacy", ref: "okx:BTC-USDT", kind: "above", value: 120, created: "2026-11-01T00:00:00Z", met: "2026-11-06T00:00:00Z", price: 125, change: .1, decimals: 2, name: "Synthetic instrument" };
    await writeFile(watch, '["BTC-USDT","cn:600000.SH"]'); await writeFile(alerts, JSON.stringify([oldAlert]));
    const originals = await Promise.all([readFile(watch), readFile(alerts)]);
    const args = [resolve(extension, "scripts/convert-state.mjs"), watch, alerts, output];
    const converted = spawnSync(process.execPath, args, { cwd: extension, encoding: "utf8", timeout: 10_000 }); assert.equal(converted.status, 0, converted.stderr);
    const value = JSON.parse(await readFile(output, "utf8")), state = JSON.parse(Buffer.from(value.data, "base64").toString());
    assert.deepEqual(state.alerts, [oldAlert]); assert.deepEqual(state.watchlist, ["okx:BTC-USDT", "cn:600000.SH"]); assert.equal((await stat(output)).mode & 0o777, 0o600);
    assert.notEqual(spawnSync(process.execPath, args, { cwd: extension, encoding: "utf8", timeout: 10_000 }).status, 0);
    assert.deepEqual(await Promise.all([readFile(watch), readFile(alerts)]), originals);
  } finally { await rm(dir, { recursive: true, force: true }); }
});
test("bundled Program validates every shared argument before tool dispatch without safe-integer narrowing", async () => {
  const h = await setup();
  try {
    for (const name of ["markets_quote", "markets_search", "markets_candles"]) for (const invalid of [{ limit: "1" }, { limit: true }, { limit: 1.5 }, { query: 123 }, { bar: [] }, { symbol: false }, { symbols: [12] }, { limit: 1e30 }]) {
      const result = await h.call("connector.tools.call", { name, args: { query: "BTC", symbol: "BTC", ...invalid } });
      assert.equal(result.error?.code, "invalid_params");
    }
    for (const limit of [null, 0, -1, 31, 9007199254740992, 9223372036854774784]) {
      const result = await h.call("connector.tools.call", { name: "markets_search", args: { query: "BTC", limit } });
      assert.ok(result.result?.text, "Representable int64 values use Go's out-of-UI-range fallback");
    }
    assert.equal((await h.call("manifest")).error?.code, "unknown_method");
  } finally { h.p.close(); }
});
for (const token of ["1.0", "1e0", "-1.0", "-1e0", "0.0", "0e0", "1.5", "-1.5", "9223372036854775808", "-9223372036854775809"]) test(`SDK exact wire rejects Go-invalid raw limit ${token}`, async () => {
  const h = await setup();
  try { const result = await h.callRaw("connector.tools.call", `{"name":"markets_search","args":{"query":"BTC","limit":${token}}}`); assert.equal(result.error?.code, "invalid_params", "SDK JSON decoding erased the integer lexical boundary"); }
  finally { h.p.close(); }
});
test("SDK exact wire preserves full signed int64 defaults, folded fields and ordered nulls", async () => {
  const h = await setup();
  try {
    const fallback = (await h.callRaw("connector.tools.call", '{"name":"markets_search","args":{"query":"BTC","limit":10}}')).result;
    for (const token of ["9223372036854775807", "-9223372036854775808", "9007199254740993", "-9007199254740993", "-0"]) {
      const result = await h.callRaw("connector.tools.call", `{"name":"markets_search","args":{"query":"BTC","limit":${token}}}`);
      assert.deepEqual(result.result, fallback, "Legal int64 tokens must retain the original default clamp");
    }
    for (const raw of [
      '{"NAME":"markets_search","name":null,"ARGS":{"QUERY":"BTC","LIMIT":1,"limit":null}}',
      '{"name":"markets_search","args":false,"ARGS":{"query":"BTC","limit":1}}',
      '{"name":"markets_search","args":{"query":"ETH","QUERY":"BTC","Query":null,"limit":1}}',
      '{"name":"markets_quote","args":{"ſymbols":["BTC-USDT",null],"SYMBOL":null,"LIMIT":null}}',
      '{"name":"markets_quote","args":{"symbols":["ETH-USDT"],"SYMBOLS":null}}',
    ]) assert.ok((await h.callRaw("connector.tools.call", raw)).result?.text);
    for (const raw of [
      '{"name":123,"NAME":"markets_quote","args":{}}',
      '{"name":"markets_quote","args":{"symbols":[12],"SYMBOLS":["BTC-USDT"]}}',
      '{"name":"markets_quote","args":{"ſymbols":false,"symbols":[]}}',
      '{"name":"markets_quote","args":{"symbol":false,"SYMBOL":"BTC"}}',
      '{"name":"markets_quote","args":{"query":123,"QUERY":null}}',
      '{"name":"markets_quote","args":{"bar":{},"BAR":"1H"}}',
      '{"name":"markets_quote","args":{"limit":1.0,"LIMIT":1}}',
      '{"name":"markets_quote","args":{"limit":1,"LIMIT":-1e0}}',
      '{"name":"markets_quote","args":[]}',
      '{"name":"markets_quote","args":{},"ARGS":false}',
    ]) assert.equal((await h.callRaw("connector.tools.call", raw)).error?.code, "invalid_params");
  } finally { h.p.close(); }
});

test("background alerts use encrypted state and Inbox without requiring a mounted page", async () => {
  const fixture = new Fixture(), secrets = new FakeHub(), frames: any[] = []; let tick!: () => void;
  const bundle = await import(pathToFileURL(resolve(extension, "build/markets.mjs")).href);
  const state = JSON.parse(Buffer.from(secrets.data, "base64").toString()); state.alerts = [{ id: "fixture-background", ref: "okx:BTC-USDT", kind: "above", value: 120, created: new Date(fixture.at).toISOString() }]; secrets.data = Buffer.from(JSON.stringify(state)).toString("base64");
  fixture.last = 125;
  const p = connect(bundle.createProgram({ fetch: fixture.fetch, clock: () => fixture.at, interval: (fn: () => void) => { tick = fn; return () => {}; } }) as Program, line => {
    const frame = JSON.parse(line); frames.push(frame);
    if (frame.method?.startsWith("connector.secrets.")) void secrets.call(frame.method, frame.params).then(result => p.line(JSON.stringify({ id: frame.id, result })));
  });
  try {
    p.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1 } })); await p.drain(); tick();
    await until(() => frames, frames => frames.some(f => f.event === "connector.changed"));
    assert.deepEqual(frames.find(f => f.event === "connector.changed").params, { contribution: "markets", scope: "inbox" });
    p.line(JSON.stringify({ id: "inbox", method: "connector.inbox.collect", params: { contribution: "markets", locale: "en" } })); await p.drain();
    assert.equal(frames.find(f => f.id === "inbox").result.things.length, 1);
    const saved = JSON.parse(Buffer.from(secrets.data, "base64").toString()); assert.ok(saved.alerts[0].met);
    tick(); await new Promise(resolve => setTimeout(resolve, 10)); assert.equal(frames.filter(f => f.event === "connector.changed").length, 1);
  } finally { p.close(); }
});

test("bundled Program cold snapshot transport settles before immediate HTTP-boundary suspend and navigation", async () => {
  const h = await setup(), delayed = new Fixture(); let reads = 0;
  h.fixture.override = (url, init) => new Promise((yes, no) => {
    reads++;
    const aborted = () => { clearTimeout(timer); no(init!.signal!.reason); };
    const timer = setTimeout(() => {
      init!.signal!.removeEventListener("abort", aborted);
      void delayed.fetch(url.href, { ...init, signal: init!.signal! }).then(yes, no);
    }, 40);
    init!.signal!.addEventListener("abort", aborted, { once: true });
  });
  try {
    // This is the real top-level host transport, independent of ui_version.
    const mounted = await h.call("ui.page.mount", { session: "fixture-cold-http", transport: "snapshot", params: { contribution: "watchlist", locale: "en" } });
    assert.ok(reads > 0); assert.match(JSON.stringify(mounted.result.view), /Synthetic instrument/);
    assert.ok(!JSON.stringify(mounted.result.view).includes('"loading"'));
    await h.call("ui.page.suspend", { session: "fixture-cold-http" });
    const resumed = await h.call("ui.page.resume", { session: "fixture-cold-http" });
    assert.ok(!JSON.stringify(resumed.result.view).includes('"loading"'));
    assert.deepEqual(resumed.result.view, mounted.result.view);
    assert.equal(resumed.result.generation, mounted.result.generation);
    assert.equal(resumed.result.version, mounted.result.version + 1, "Native resume always renews its callback revision");
    // The native SDK revision is distinct from the manager's coalesced wire
    // version. Equal finite loads must not invalidate this returned revision later.
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal((await h.call("ui.page.snapshot", { session: "fixture-cold-http" })).result.version, resumed.result.version);
    const opened = await h.call("ui.page.event", { session: "fixture-cold-http", ticket: "fixture-host-ticket", interaction: interaction(resumed.result, "quote-okx:BTC-USDT") });
    assert.equal(opened.result.status, "accepted");
    assert.match(JSON.stringify(opened.result.snapshot.view), /section-candles/);
    assert.ok(!JSON.stringify(opened.result.snapshot.view).includes('"loading"'));
    await h.call("ui.page.suspend", { session: "fixture-cold-http" });
    const before = reads;
    await new Promise(resolve => setTimeout(resolve, 75));
    assert.equal(reads, before, "Hidden subscriptions and intervals must not start more reads");
    const snapshot = await h.call("ui.page.snapshot", { session: "fixture-cold-http" });
    assert.match(JSON.stringify(snapshot.result.view), /section-candles/);
    assert.ok(!JSON.stringify(snapshot.result.view).includes('"loading"'));
  } finally { h.p.close(); }
});
