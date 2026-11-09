import assert from "node:assert/strict";
import { readFile, copyFile, readdir, mkdtemp } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
const root = fileURLToPath(new URL("../", import.meta.url));
const sealed = await mkdtemp(resolve(root, "build/round3-sealed-"));
await copyFile(resolve(root, "build/markets.mjs"), resolve(sealed, "markets.mjs"));
assert.deepEqual(await readdir(sealed), ["markets.mjs"]);
const bytes = await readFile(resolve(sealed, "markets.mjs"));
assert.ok(!/\b(?:import|require)\s*\(/.test(bytes.toString()));
const bundle = await import(pathToFileURL(resolve(sealed, "markets.mjs")).href);
const hub = {
  data: Buffer.from(JSON.stringify({ schema_version: 1, watchlist: ["okx:BTC-USDT"], alerts: [] })).toString("base64"),
  async call(method, params) {
    if (method === "connector.secrets.load") return { data: this.data };
    if (method === "connector.secrets.save") { this.data = params.data; return {}; }
    throw new Error("Unexpected fixture host call");
  }, notify() {}, invalidate() {},
};
const oldFetch = globalThis.fetch, oldInterval = globalThis.setInterval, oldClear = globalThis.clearInterval;
globalThis.fetch = async input => {
  const url = new URL(input);
  if (url.hostname === "qt.gtimg.cn") return new Response('v_pv_none_match="1";');
  assert.equal(url.hostname, "www.okx.com");
  return new Response(JSON.stringify({ code: "0", data: url.pathname.endsWith("candles")
    ? [["9223372036854775807", "100", "110", "90", "105", "3"], ["-9223372036854775808", "100", "110", "90", "105", "3"]]
    : [{ instId: "BTC-USDT", last: "100", open24h: "90", high24h: "110", low24h: "80", ts: "9223372036854775807" }] }));
};
globalThis.setInterval = () => 123; globalThis.clearInterval = () => {};
try {
  const program = bundle.default;
  assert.equal(program.manifest, undefined); assert.equal(program.fetch, undefined); assert.equal(program.id, "markets"); assert.equal(program.version, "0.6.5");
  await program.init({ config: { tz: "UTC" } }, hub);
  const quote = await program.namespaces.connector("tools.call", { name: "markets_quote", args: { symbols: ["BTC-USDT"] }, locale: "en" });
  assert.match(quote.text, /100/);
  const candles = await program.namespaces.connector("tools.call", { name: "markets_candles", args: { symbol: "BTC-USDT", bar: "1H" }, locale: "en" });
  assert.match(candles.text, /292278994-08-17 07:12/); assert.match(candles.text, /-292275055-05-16 16:47/);
  const service = new bundle.Markets(hub, { tz: "UTC" }, { interval: () => () => {} }); await service.initialize();
  try {
    const view = await bundle.snapshotPage(service).render({ state: JSON.stringify({ i: "BTC-USDT", b: "1H" }), ui_version: 5, locale: "en", tz: "UTC" });
    assert.equal(view.sections.flatMap(section => section.items).filter(item => item.type === "chart").length, 2);
  } finally { service.dispose(); }
  console.log("VERDICT sealed default bundle: PASS one file, no authoring SDK/imports, offline fixture transport, init/tools/extreme-time native snapshot");
} catch (error) {
  console.log("VERDICT sealed default bundle: FAIL"); throw error;
} finally { globalThis.fetch = oldFetch; globalThis.setInterval = oldInterval; globalThis.clearInterval = oldClear; }
