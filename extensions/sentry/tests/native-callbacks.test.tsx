import { test } from "node:test";
import assert from "node:assert/strict";
import { componentPage, connect } from "@persona/extension";
import { Button, Section, Text, View, callback, useLoadEffect, useState } from "@persona/extension/ui";

// Native SDK contract fixtures, never a Sentry contribution or a consent claim.
test("saved native callback requires a host ticket; navigation finite-load settles; replay retains its saved result", async () => {
  let writes = 0, loads = 0;
  function Fixture() {
    const [page, navigate] = useState(0), [title, setTitle] = useState("loading");
    useLoadEffect(async signal => {
      loads++;
      await new Promise(resolve => setTimeout(resolve, 25));
      if (!signal.aborted) setTitle(`synthetic page ${page}`);
    }, [page]);
    return <View key="root"><Section key="main"><Text key="data" text={title} /><Button key="save" title="Save fixture" action={callback(() => { writes++; navigate(n => n + 1); return { toast: "Synthetic saved receipt" }; })} /></Section></View>;
  }
  const frames: any[] = [], conn = connect({ id: "synthetic-native-fixture", version: "1", ui: { fixture: componentPage(() => <Fixture key="fixture" />) } }, line => frames.push(JSON.parse(line)));
  let next = 0;
  const call = async (method: string, params: unknown) => {
    const id = String(++next); conn.line(JSON.stringify({ id, method, params })); await conn.drain(); return frames.find(frame => frame.id === id);
  };
  try {
    await call("initialize", { protocol: 1 });
    const mounted = (await call("ui.page.mount", { session: "owned", transport: "snapshot", params: { contribution: "fixture" } })).result;
    assert.equal(mounted.view.sections[0].items[0].text, "synthetic page 0");
    const button = mounted.view.sections[0].items[1];
    const interaction = { generation: mounted.generation, version: mounted.version, key: "save", actionId: button.action.id, eventId: "synthetic-save", values: {} };
    assert.ok((await call("ui.page.event", { session: "owned", interaction })).error);
    assert.equal(writes, 0);
    const saved = (await call("ui.page.event", { session: "owned", ticket: "synthetic-host-issued-ticket", interaction })).result;
    assert.equal(saved.status, "accepted"); assert.deepEqual(saved.result, { toast: "Synthetic saved receipt" });
    assert.equal(saved.snapshot.view.sections[0].items[0].text, "synthetic page 1"); assert.equal(loads, 2);
    const replay = (await call("ui.page.event", { session: "owned", ticket: "synthetic-replay-ticket", interaction })).result;
    assert.deepEqual(replay.result, saved.result); assert.equal(writes, 1);
    const stale = (await call("ui.page.event", { session: "owned", ticket: "synthetic-stale-ticket", interaction: { ...interaction, eventId: "new-stale-event" } })).result;
    assert.equal(stale.status, "ambiguous"); assert.equal(writes, 1);
    await call("ui.page.suspend", { session: "owned" });
    assert.ok((await call("ui.page.resume", { session: "owned" })).result);
    await call("ui.page.dispose", { session: "owned" });
    assert.ok((await call("ui.page.snapshot", { session: "owned" })).error);
  } finally { await conn.shutdown(); }
});
