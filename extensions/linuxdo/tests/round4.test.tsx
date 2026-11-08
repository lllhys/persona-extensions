import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "@persona/extension/ui";
import { Fixture, rss, topic } from "./fixtures.ts";
import { LinuxDo, limits } from "../src/service.ts";
import { loadPage, page, readState, transition } from "../src/page.tsx";
import { parseTopic } from "../src/topic.ts";
import { topicTime } from "../src/time.ts";
import { date } from "../src/i18n.ts";
import { callTool } from "../src/tools.ts";

const setup = () => { const fixture = new Fixture(); return { fixture, service: new LinuxDo(fixture.fetch, {}, "UTC", "en", { gap: 0, now: () => fixture.time }) }; };

test("round4 missing/private oversized topic stays gone, retains cache and connector health", async () => {
  for (const status of [403, 404]) for (const declared of [false, true]) {
    const { fixture, service } = setup(), good = await service.thread(1), health = service.status();
    fixture.time += limits.topic;
    fixture.bodies.set("/t/topic/1.json", new Response("missing" + "x".repeat(limits.response), {
      status, headers: declared ? { "Content-Length": String(limits.response + 7) } : {},
    }));
    await assert.rejects(service.thread(1), { code: "gone" });
    assert.deepEqual(service.status(), health);
    assert.equal(service.site.stats.entries, 1);
    // The original cache remains reusable; gone never marks it as a stale success.
    fixture.time = good.at;
    assert.equal((await service.thread(1)).value, good.value);
    fixture.time += limits.topic;
    fixture.bodies.set("/t/topic/1.json", new Response("Just a moment" + "x".repeat(limits.response), { status }));
    const challenged = await service.thread(1);
    assert.equal(challenged.value, good.value); assert.equal(challenged.stale?.code, "challenge");
    assert.equal(service.status().state, "error");
  }
});

test("round4 topic error-body read failure after the challenge prefix is still a network failure", async () => {
  const fixture = new Fixture(); let fail = false;
  const service = new LinuxDo(async (input, init) => {
    if (!fail) return fixture.fetch(input, init);
    let reads = 0;
    return new Response(new ReadableStream<Uint8Array>({ pull(controller) {
      if (!reads++) controller.enqueue(new TextEncoder().encode("x".repeat(4096)));
      else controller.error(new Error("synthetic error before the Go body cap"));
    } }), { status: 403 });
  }, {}, "UTC", "en", { gap: 0, now: () => fixture.time });
  const good = await service.thread(1); fixture.time += limits.topic; fail = true;
  const failed = await service.thread(1);
  assert.equal(failed.value, good.value); assert.equal(failed.stale?.code, "network");
  assert.equal(service.status().state, "error");
});

test("round4 Go zero time is absent but a one-nanosecond instant remains visible", async () => {
  for (const [raw, missing] of [
    ["0001-01-01T00:00:00Z", true], ["0001-01-01T00:00:00.000000000Z", true],
    ["0001-01-01T01:00:00+01:00", true], ["0001-01-01T00:00:00.000000001Z", false],
    ["0001-01-01T00:00:00.0000000001Z", true], ["1970-01-01T00:00:00Z", false],
  ] as const) {
    assert.equal(topicTime(raw) === null, missing, raw);
    assert.equal(date(topicTime(raw), "UTC"), missing ? "" : "01-01 00:00");
    const { fixture, service } = setup(), doc = topic(); doc.created_at = raw;
    for (const post of doc.post_stream.posts) post.created_at = raw;
    fixture.bodies.set("/t/topic/1.json", doc);
    assert.equal(parseTopic(JSON.stringify(doc)).at === null, missing);
    const state = readState('{"t":1}', service), loaded = await loadPage(service, state);
    const renderer = new Renderer({ authorize: () => false });
    try { assert.equal(JSON.stringify(renderer.mount(page({ service, state, loaded })).view).includes("01-01 00:00"), !missing); }
    finally { renderer.dispose(); }
    assert.equal((await callTool(service, "linuxdo_topic", { topic: 1 })).includes("01-01 00:00"), !missing);
  }
});

test("round4 every RSS row is reachable with bounded native pages and preserved back state", async () => {
  for (const count of [271, 541, 810]) for (const uiVersion of [1, 2, 3, 5]) {
    const { fixture, service } = setup();
    fixture.bodies.set("/hot.rss", rss(Array.from({ length: count }, (_, i) => ({ id: i + 1 }))));
    let state = readState(undefined, service); const seen: string[] = [];
    for (let offset = 0; offset < count; offset += 270) {
      const renderer = new Renderer({ authorize: () => false });
      try {
        const loaded = await loadPage(service, state), view = renderer.mount(page({ service, state, loaded, uiVersion })).view;
        const items = view.sections.flatMap(section => section.items);
        assert.ok(items.length <= 280); // Existing native frame budget, not a raised cap.
        seen.push(...items.flatMap(item => item.key?.startsWith("topic-") ? [item.key] : []));
        assert.equal(items.some(item => item.key === "previous-page"), offset > 0);
        assert.equal(items.some(item => item.key === "next-page"), offset + 270 < count);
        const saved = readState(view.state, service); assert.deepEqual(saved, state);
        const topicState = transition(state, `topic:${offset + 1}`);
        assert.deepEqual(transition(topicState, "back"), state);
        state = transition(state, `page:${Math.floor(offset / 270) + 1}`);
      } finally { renderer.dispose(); }
    }
    assert.deepEqual(seen, Array.from({ length: count }, (_, i) => `topic-${i + 1}`));
    assert.equal(fixture.hits.get("/hot.rss"), 1);
    assert.equal(transition(state, "all").p, undefined);
    assert.equal(transition(state, "list:latest").p, undefined);
  }
});
