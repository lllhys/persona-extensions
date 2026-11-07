import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

// The SDK under test is actual TypeScript-emitted ESM, with no Bun loader or globals.
const ui = await import(pathToFileURL(process.argv[2]));
assert.equal(typeof globalThis.Bun, "undefined");
const { View, Section, Text, Button, createElement: h, callback, mount, useState, useEffect, useFetch, useInterval } = ui;
let cleanup = 0, stopped = 0, activeSignal;
const timers = { setInterval: fn => fn, clearInterval: () => { stopped++; } };
const settle = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
function Page() {
  const [n, set] = useState(0);
  const result = useFetch("https://example.com/blocked");
  useEffect(signal => { activeSignal = signal; return () => { cleanup++; }; }, []);
  useInterval(() => {}, 100);
  return h(View, { key: "root", children: h(Section, { key: "main", children: [
    h(Text, { key: "value", text: String(n) }),
    h(Text, { key: "network", text: result.status }),
    h(Button, { key: "press", title: "fixture", action: callback(() => set(n + 1)) }),
  ] }) });
}
const session = mount(h(Page, { key: "page" }), { authorize: () => true, timers });
await settle();
assert.equal(session.current().view.sections[0].items[1].text, "error");
const snapshot = session.current(), id = snapshot.view.sections[0].items[2].action.id;
const event = { generation: snapshot.generation, version: snapshot.version, eventId: "one", key: "press", actionId: id, values: {} };
const accepted = session.event(event); await accepted; await settle();
assert.equal(session.current().view.sections[0].items[0].text, "1");
assert.equal(session.current().view.sections[0].items[2].action.id, id);
assert.equal(session.event(event), accepted);
session.suspend(); assert.equal(activeSignal.aborted, true); assert.equal(cleanup, 1); assert.equal(stopped, 1);
session.resume(); session.dispose(); assert.equal(cleanup, 2); assert.equal(stopped, 2);
for (const Tiles of [ui.Grid, ui.Carousel]) {
  const tiles = value => h(View, { key: "root", children: h(Section, { key: "main", children: h(Tiles, { key: "tiles", tiles: value }) }) });
  assert.throws(() => mount(tiles(Array(33)), { authorize: () => true, limits: { nodes: 16 } }), /node limit/);
  assert.throws(() => mount(tiles(Array(65)), { authorize: () => true, limits: { props: 32 } }), /prop limit/);
  assert.throws(() => mount(tiles(Array(1)), { authorize: () => true }), /UI key/);
  const sparse = [{ key: "tile", title: "fixture" }, ,];
  sparse[Symbol.iterator] = function* () { yield sparse[0]; };
  assert.throws(() => mount(tiles(sparse), { authorize: () => true }), /UI key/);
}
const pending = [];
function FetchPage({ url, resource }) {
  const result = useFetch(url, { dependencies: [resource] });
  return h(View, { key: "root", title: JSON.stringify(result) });
}
const fetching = mount(h(FetchPage, { key: "page", url: "https://example.com/a", resource: 1 }), {
  authorize: () => true,
  fetch: (_url, { signal }) => new Promise(resolve => { pending.push({ resolve, signal }); }),
});
assert.deepEqual(JSON.parse(fetching.current().view.title), { status: "loading" });
pending[0].resolve(Response.json("a")); await settle();
assert.deepEqual(JSON.parse(fetching.current().view.title), { status: "success", data: "a" });
assert.deepEqual(JSON.parse(fetching.render(h(FetchPage, { key: "page", url: "https://example.com/b", resource: 1 })).view.title), { status: "loading" });
assert.deepEqual(JSON.parse(fetching.render(h(FetchPage, { key: "page", url: "https://example.com/b", resource: 2 })).view.title), { status: "loading" });
assert.equal(pending[1].signal.aborted, true);
pending[1].resolve(Response.json("late")); pending[2].resolve(Response.json("current")); await settle();
assert.deepEqual(JSON.parse(fetching.current().view.title), { status: "success", data: "current" });
assert.deepEqual(JSON.parse(fetching.render(h(FetchPage, { key: "page", url: null, resource: 2 })).view.title), { status: "idle" });
fetching.dispose();
const cloning = mount(h(FetchPage, { key: "page", url: "https://example.com/a", resource: 1 }), {
  authorize: () => true, fetch: async () => Response.json("cloned"),
  state: { create(initial) { let value = structuredClone(initial); return { get: () => structuredClone(value), set: next => { value = structuredClone(next); } }; } },
});
await settle(); assert.deepEqual(JSON.parse(cloning.current().view.title), { status: "success", data: "cloned" }); cloning.dispose();
const { connect } = await import(new URL("./index.js", pathToFileURL(process.argv[2])));
const output = [], events = [];
const conn = connect({ id: "example", version: "1", namespaces: { thing: (method, params) => { events.push([method, params]); } } }, line => output.push(JSON.parse(line)));
for (const frame of [null, [], true, { event: 1 }, { method: {} }]) assert.doesNotThrow(() => conn.line(JSON.stringify(frame)));
conn.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1 } })); await conn.drain();
conn.line(JSON.stringify({ event: "thing.changed", params: { n: 1 } })); await settle(); assert.deepEqual(events, [["changed", { n: 1 }]]);
const reply = conn.hub.call("thing.read"), replyId = output.at(-1).id;
let answered = false; reply.then(() => { answered = true; });
for (const frame of [{ id: replyId }, { id: replyId, error: null }, { id: replyId, error: { message: 1 } }]) conn.line(JSON.stringify(frame));
await settle(); assert.equal(answered, false);
conn.line(JSON.stringify({ id: replyId, result: { value: "current" } })); assert.deepEqual(await reply, { value: "current" });
conn.line(JSON.stringify({ id: "later", method: "ping" })); await conn.drain(); assert.deepEqual(output.at(-1), { id: "later", result: {} }); conn.close();
console.log("PASS: TypeScript-emitted ESM hooks, fetch identity/adapters, sparse tile bounds, protocol dispatch, callbacks, replay and cleanup in Node without Bun globals");
