import { test } from "node:test";
import assert from "node:assert/strict";
import { componentPage, connect } from "@persona/extension";
import { Button, Loading, Section, Text, View, callback, mount, useEffect, useLoadEffect, useState, type Renderer, type UIEvent } from "@persona/extension/ui";
import { deferred, until } from "./fixtures.ts";

// SDK compatibility fixture only: Sentry still ships no page.
test("official SDK cold snapshot awaits finite load while subscriptions remain visibility-managed", async () => {
  let reads = 0, starts = 0, stops = 0;
  function ColdFixture() {
    const [ready, setReady] = useState(false);
    useLoadEffect(async signal => {
      reads++;
      await new Promise(resolve => setTimeout(resolve, 100));
      if (!signal.aborted) setReady(true);
    }, []);
    useEffect(() => { starts++; return () => { stops++; }; }, []);
    return <View key="root"><Section key="main">{ready ? <Text key="data" text="synthetic data" /> : <Loading key="loading" />}</Section></View>;
  }
  const frames: any[] = [];
  const conn = connect({ id: "synthetic-cold-fixture", version: "0.1.0", ui: { fixture: componentPage(() => <ColdFixture key="fixture" />) } }, line => frames.push(JSON.parse(line)));
  let next = 0;
  const call = async (method: string, params: unknown) => {
    const id = String(++next); conn.line(JSON.stringify({ id, method, params })); await conn.drain(); return frames.find(frame => frame.id === id);
  };
  try {
    await call("initialize", { protocol: 1 });
    const first = (await call("ui.page.mount", { session: "cold", transport: "snapshot", params: { contribution: "fixture" } })).result;
    assert.ok(JSON.stringify(first).includes("synthetic data")); assert.equal(reads, 1);
    await call("ui.page.suspend", { session: "cold" }); assert.equal(stops, 1);
    const resumed = (await call("ui.page.resume", { session: "cold" })).result;
    assert.ok(JSON.stringify(resumed).includes("synthetic data")); assert.equal(reads, 2); assert.equal(starts, 2);
  } finally { await conn.shutdown(); }
});
/** SDK compatibility fixture only: never registered in the Sentry package. */
function interaction(renderer: Renderer, id: string = crypto.randomUUID()): UIEvent {
  const button = renderer.current().view.sections[0].items.find(item => item.key === "press") as { action: { id: string } };
  return { generation: renderer.generation, version: renderer.current().version, key: "press", actionId: button.action.id, eventId: id, values: {} };
}
test("synthetic native JSX snapshot/fullView and replay/stale callbacks", async () => {
  let writes = 0;
  function FixturePage() {
    const [title, setTitle] = useState("fixture");
    return <View key="root"><Section key="main"><Text key="text" text={title} /><Button key="press" title="fixture" action={callback(() => { writes++; setTitle("updated fixture"); })} /></Section></View>;
  }
  const renderer = mount(<FixturePage key="fixture" />, { authorize: () => true });
  try {
    assert.deepEqual(renderer.current().view.sections[0].items[0], { key: "text", type: "text", text: "fixture" });
    const first = interaction(renderer, "write-once"); const accepted = renderer.event(first); await accepted;
    await until(() => JSON.stringify(renderer.current().view).includes("updated fixture"));
    assert.equal(renderer.event(first), accepted); await renderer.event(first); assert.equal(writes, 1);
    await assert.rejects(renderer.event({ ...first, eventId: "stale" }), /stale/);
  } finally { renderer.dispose(); }
});
test("synthetic component fixture cancels work on hiding and restarts effects on resume", async () => {
  const work = deferred<void>(); let active: AbortSignal | undefined, effectSignal: AbortSignal | undefined; let effects = 0;
  function FixturePage() {
    useEffect(signal => { effectSignal = signal; effects++; }, []);
    return <View key="root"><Section key="main"><Button key="press" title="fixture" action={callback(async (_, context) => { active = context.signal; await work.promise; })} /></Section></View>;
  }
  const renderer = mount(<FixturePage key="fixture" />, { authorize: () => true });
  try {
    await until(() => effects === 1);
    const event = interaction(renderer); const pending = renderer.event(event); await until(() => !!active);
    renderer.suspend(); assert.equal(active!.aborted, true); assert.equal(effectSignal!.aborted, true);
    renderer.resume(); await until(() => effects === 2); assert.equal(effectSignal!.aborted, false);
    work.resolve(); await pending;
  } finally { renderer.dispose(); }
});
test("synthetic componentPage uses owner-bound session snapshots; widget keeps old fullView", async () => {
  const fixture = <View key="root"><Section key="main"><Text key="text" text="fixture" /></Section></View>;
  const conn = connect({ id: "synthetic-fixture", version: "0.1.0", ui: { fixture: componentPage(() => fixture) } }, line => frames.push(JSON.parse(line)));
  const frames: any[] = []; let next = 0;
  const call = async (method: string, params: unknown) => {
    const id = String(++next); conn.line(JSON.stringify({ id, method, params })); await conn.drain(); return frames.find(frame => frame.id === id);
  };
  try {
    await call("initialize", { protocol: 1 });
    const mounted = (await call("ui.page.mount", { session: "fixture", params: { contribution: "fixture", ui_version: 5 } })).result;
    assert.equal(mounted.view.sections[0].items[0].text, "fixture");
    const fullView = (await call("ui.render", { contribution: "fixture", placement: "widget", ui_version: 1 })).result;
    assert.deepEqual(fullView.sections, mounted.view.sections);
    assert.equal((await call("ui.render", { contribution: "fixture", placement: "nav_page", ui_version: 5 })).error.code, "unsupported");
    await call("ui.page.suspend", { session: "fixture" }); assert.ok((await call("ui.page.resume", { session: "fixture" })).result);
    await call("ui.page.dispose", { session: "fixture" });
  } finally { await conn.shutdown(); }
});
