import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer, type Snapshot, type UIEvent } from "@persona/extension/ui";
import type { View, UIRequest } from "@persona/extension";
import { Fixture, rss, topic } from "./fixtures.ts";
import { LinuxDo, ReadError, limits } from "../src/service.ts";
import { Site } from "../src/site.ts";
import { parseFeed } from "../src/feed.ts";
import { parseTopic, bestReplies } from "../src/topic.ts";
import { plainText, postText } from "../src/text.ts";
import { clip, chunks, filter, topicID, settings, lists, opening } from "../src/model.ts";
import { date, meta, resources, textsFor, describe } from "../src/i18n.ts";
import { callTool, connector, tools, toolsFor } from "../src/tools.ts";
import { readState, transition, page, loadPage, openingParts } from "../src/page.tsx";
import { root } from "../src/root.tsx";
import { snapshotPage } from "../src/snapshot.ts";
const setup = (config = {}) => { const fixture = new Fixture(); return { fixture, service: new LinuxDo(fixture.fetch, config, "UTC", "en", { now: () => fixture.time, gap: 0 }) }; };
const text = (value: unknown) => JSON.stringify(value);
const draw = async (service: LinuxDo, raw: unknown = {}, locale = "en", uiVersion = 2, tz?: string) => {
  const state = readState(raw, service), loaded = await loadPage(service, state), renderer = new Renderer({ authorize: () => false });
  try { return renderer.mount(page({ service, state, loaded, locale, uiVersion, tz })).view; } finally { renderer.dispose(); }
};
async function until(predicate: () => boolean) { const deadline = Date.now() + 3000; while (!predicate()) { if (Date.now() > deadline) throw new Error("fixture did not settle"); await new Promise(resolve => setTimeout(resolve, 2)); } }
const pause = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(s => s.items).find(it => it.key === key); assert.ok(item && "action" in item && item.action);
  return { generation: snapshot.generation, version: snapshot.version, eventId: crypto.randomUUID(), key, actionId: item.action.id, values };
}
function checkView(view: View) {
  assert.ok((view.state?.length ?? 0) < 16 << 10); let count = 0;
  const bounded = (value: unknown) => { if (typeof value === "string") assert.ok(Array.from(value).length <= 2000); };
  bounded(view.title);
  for (const section of view.sections) { bounded(section.title); bounded(section.footer); for (const item of section.items) { count++; Object.values(item).forEach(bounded); if (item.type === "text") assert.ok(item.text.trim()); } }
  assert.ok(count <= 300, `view has ${count} items`);
}
test("TestParseFeed: RSS roots, namespaces, CDATA, XML/HTML entities, bilingual counts and suffixes", () => {
  const f = new Fixture(), ts = parseFeed(f.bodies.get("/hot.rss") as string);
  assert.equal(ts.length, 5); assert.equal(ts[0].pinned, true); assert.equal(ts[1].pinned, false);
  assert.equal(ts[1].id, "1"); assert.equal(ts[1].author, "fixture-author"); assert.equal(ts[1].category, "Dev"); assert.equal(ts[1].excerpt, "Rust benchmark"); assert.equal(ts[1].posts, 75); assert.equal(ts[1].people, 59);
  assert.equal(meta(textsFor("en"), ts[1], f.time), "Dev · fixture-author · 75 posts · 59 people · 3h ago");
  for (const english of [true, false]) {
    const body = rss([{ id: 42, title: "A & B", author: "fixture <author>", excerpt: "Escaped &amp; &lt;tag&gt;", english }]);
    const item = parseFeed(body)[0]; assert.equal(item.title, "A & B"); assert.equal(item.author, "fixture <author>"); assert.equal(item.excerpt, "Escaped & <tag>"); assert.equal(item.posts, 75);
    const noCounts = body.replace(/75 (?:posts - 59 participants|个帖子 - 59 位参与者)/u, ""); assert.equal(parseFeed(noCounts)[0].excerpt, "Escaped & <tag>");
  }
  assert.equal(Array.from(parseFeed(rss([{ id: 1, excerpt: "😀".repeat(250) }]))[0].excerpt).length, 201);
  for (const raw of ["<html>Just a moment</html>", "<rss><channel><item></channel></rss>", "<rss><channel>&unknown;</channel></rss>", '<!DOCTYPE rss [<!ENTITY x "a">]><rss><channel/></rss>']) assert.throws(() => parseFeed(raw));
  assert.deepEqual(parseFeed(rss([{ id: 1, link: "https://linux.do/t/some-slug/42/7" }, { id: 2, link: "https://linux.do/t/123/4" }, { id: 3, link: "hello" }])).map(t => t.id), ["42", "123"]);
});
test("TestFilter: explicit flags, category substring/case/whitespace, OR/exclusions and narrowing", () => {
  const topics = parseFeed(new Fixture().bodies.get("/hot.rss") as string);
  const ids = (f: object) => filter(topics, f).map(t => t.id);
  for (const [f, expected] of [
    [{}, ["10", "1", "2", "3", "4"]], [{ hide_nsfw: true }, ["10", "1", "3", "4"]], [{ hide_pinned: true }, ["1", "2", "3", "4"]],
    [{ categories: [" dE ", "Resources"], hide_nsfw: true }, ["1", "4"]], [{ exclude_categories: ["new"], hide_nsfw: true }, ["10", "1", "4"]],
    [{ keywords: ["Rust", " AI "] }, ["1", "4"]], [{ exclude_keywords: ["gpu"] }, ["10", "1", "2", "3"]], [{ categories: [" "], keywords: [""] }, ["10", "1", "2", "3", "4"]]
  ] as const) assert.deepEqual(ids(f), expected);
  assert.equal(describe(textsFor("en"), { categories: ["Dev"], hide_nsfw: true }), "only Dev · hide NSFW");
  assert.equal(settings({ list: "bad" }).list, "hot"); assert.equal(settings({ hide_nsfw: false }).hide_nsfw, false); assert.equal(settings({ hide_pinned: false }).hide_pinned, false);
  for (const c of [{ tz: "Invalid/Zone" }, { keywords: [2] }, { hide_nsfw: "false" }, []]) assert.throws(() => settings(c));
});
test("TestPostText: paragraphs/list/quote/table/code, dropped metadata/emoji, onebox once and pictures", () => {
  const cooked = '<h2>Conclusion</h2><p>Faster <strong>30%</strong><img class="emoji" src="https://example.com/emoji"></p><div><img src="https://example.com/image"><div class="meta">image 1920</div></div><ul><li>One</li><li>Two</li></ul><pre><code>  a &lt; b\nx := 1\n</code></pre><aside class="onebox" data-onebox-src="https://example.com/s?q=go&amp;reg=cn&amp;lt=1&amp;param=x"><article>preview</article></aside>';
  const sg = postText(cooked, textsFor("en")); assert.equal(sg.length, 3); assert.equal(sg[1].code, true); assert.equal(sg[1].text, "  a < b\nx := 1");
  for (const expected of ["Conclusion", "Faster 30%", "[Image]", "• One\n• Two"]) assert.ok(sg[0].text.includes(expected));
  assert.ok(!sg[0].text.includes("1920")); assert.equal(sg[2].text, "[Link] https://example.com/s?q=go&reg=cn&lt=1&param=x");
  assert.equal(plainText("<table><tr><th>A</th><td>B</td><td>C</td></tr></table>"), "A\nB\nC");
  assert.equal(plainText('<div><img class="avatar" src="https://example.com/a">op:</div><blockquote><p>quote</p></blockquote><p>response</p>'), "op:\n\n> quote\n\nresponse");
  assert.equal(plainText('<script>bad</script><style>bad</style><svg>bad</svg><p>good</p>'), "good");
  assert.equal(plainText('<aside class="onebox"><p>preview</p></aside>', textsFor("en")), "[Link preview]");
});
test("topic tags/score ties/likes/opening and TestRepliesWithoutTextDoNotBreakThePage", async () => {
  const d = topic(40, "<p>Body</p>", ['<img class="emoji">', "<video><source></video>", "<img src='https://example.com/a'>", "<audio><source></audio>", "<p>Regular reply</p>"]);
  let parsed = parseTopic(JSON.stringify(d)); assert.deepEqual(bestReplies(parsed, 10).map(p => p.number), [6, 4]); assert.equal(opening(parsed)?.likes, 89); assert.deepEqual(parsed.tags, ["synthetic-tag"]);
  d.tags = ["old-tag"]; d.post_stream.posts[3].score = d.post_stream.posts[5].score; d.post_stream.posts[3].actions_summary = [{ id: 2, count: 9 }];
  parsed = parseTopic(JSON.stringify(d)); assert.deepEqual(bestReplies(parsed, 10).map(p => p.number), [4, 6]); assert.equal(parsed.posts[3].likes, 9); assert.deepEqual(parsed.tags, ["old-tag"]);
  d.post_stream.posts[3].hidden = true; d.post_stream.posts[5].post_type = 2; assert.deepEqual(bestReplies(parseTopic(JSON.stringify(d)), 10), []);
  const { fixture, service } = setup(); fixture.bodies.set("/t/topic/40.json", topic(40, "<p>Body</p>", ['<img class="emoji">', "<video></video>", "<img>", "<audio></audio>", "<p>Regular reply</p>"]));
  const view = await draw(service, { t: 40 }); checkView(view); assert.ok(text(view).includes("Regular reply")); assert.ok(!text(view).includes("#2 fixture"));
  assert.ok((await callTool(service, "linuxdo_topic", { topic: 40 })).includes("Regular reply"));
});
test("TestPage/V2/TimeZone: all four lists, empty vs filtered, all/back, defaults and no push", async () => {
  const { fixture, service } = setup({ exclude_categories: ["News"] });
  const view = await draw(service, {}, "en", 1); checkView(view); assert.equal(view.sections[0].items[0].type, "row"); assert.ok(text(view).includes("Hot · 2 / 5 topics"));
  assert.equal((await draw(service)).sections[0].items[0].type, "segmented");
  assert.ok(text(await draw(service, { a: true })).includes("Hot · all 5 topics"));
  const daily = text(await draw(service, { l: "daily" })); assert.ok(daily.includes("No topics match"));
  const weekly = text(await draw(service, { l: "weekly" })); assert.ok(weekly.includes("This list has no topics")); assert.ok(!weekly.includes("No topics match"));
  assert.ok(text(await draw(service, { l: "latest" })).includes("Synthetic Rust topic"));
  const state = readState('{"l":"daily","t":1,"a":true}', service); assert.deepEqual(transition(state, "back"), { l: "daily", t: "0", a: true });
  assert.equal(transition(state, "list", { list: "weekly" }).l, "weekly"); assert.equal(transition(state, "topic:9007199254740993").t, "9007199254740993"); assert.equal(transition(state, "all").a, false);
  assert.equal(readState("invalid", service).l, "hot"); assert.equal(readState({ l: "bad" }, service).l, "hot");
  const tokyo = await draw(service, { t: 1 }, "en", 3, "Asia/Tokyo"); assert.ok(text(tokyo).includes("11-03 18:37")); assert.ok(text(tokyo).includes("Back to list")); assert.ok(!text(tokyo).includes('"push"'));
  const configured = new LinuxDo(fixture.fetch, { tz: "Asia/Shanghai" }, "UTC", "en", { gap: 0 }); assert.ok(text(await draw(configured, { t: 1 }, "en", 2, "Asia/Tokyo")).includes("11-03 17:37"));
  assert.ok(text(await draw(service, { t: 999 })).includes("missing, deleted")); assert.ok(text(await draw(service, { t: 999 })).includes("https://linux.do/t/topic/999"));
  assert.equal(service.status().state, "ok");
});
test("optional tool nulls preserve Go defaults, and descriptions/error messages follow all three locales", async () => {
  for (const locale of ["en", "zh-Hans", "ja"]) {
    const fixture = new Fixture(), service = new LinuxDo(fixture.fetch, {}, "UTC", locale, { gap: 0 });
    assert.equal(await callTool(service, "linuxdo_hot", { list: null, limit: null, filtered: null, keywords: null }), await callTool(service, "linuxdo_hot"));
    assert.equal(await callTool(service, "linuxdo_topic", { topic: 1, replies: null }), await callTool(service, "linuxdo_topic", { topic: 1 }));
    const t = textsFor(locale), handler = connector(service);
    assert.equal(toolsFor(locale)[0].description, t.hotDescription);
    await assert.rejects(async () => handler("tools.call", { name: "linuxdo_hot", args: { list: "invalid" } }), { message: t.invalidList });
    await assert.rejects(async () => handler("tools.call", { name: "linuxdo_topic", args: { topic: "invalid" } }), { message: t.invalidTopic });
    fixture.bodies.set("/top.rss?period=daily", new Response("blocked", { status: 429 }));
    await assert.rejects(async () => handler("tools.call", { name: "linuxdo_hot", args: { list: "daily" } }), { message: t.rate });
  }
});
test("streaming response limit and abort release the queue without waiting for a never-settling cancel promise", async () => {
  let cancelled = 0, count = 0;
  const fetch = async () => {
    count++;
    if (count > 1) return new Response("small");
    return new Response(new ReadableStream<Uint8Array>({
      start(controller) { controller.enqueue(new Uint8Array(limits.response + 1)); },
      cancel() { cancelled++; return new Promise(() => {}); },
    }));
  };
  const site = new Site(fetch, { gap: 0, request: 20 });
  await assert.rejects(site.get("/oversize", 0, value => value), (e: unknown) => e instanceof ReadError && e.code === "bad");
  assert.equal(cancelled, 1); assert.equal((await site.get("/next", 0, value => value)).value, "small");
  let streamCancelled = false;
  const streaming = new Site(async () => new Response(new ReadableStream<Uint8Array>({ cancel() { streamCancelled = true; } })), { gap: 0 });
  const controller = new AbortController(), pending = streaming.get("/blocked", 0, value => value, controller.signal);
  await pause(2); controller.abort(); await assert.rejects(pending, { name: "AbortError" }); assert.ok(streamCancelled);
});
test("TestLongTopic/ManyCodeBlocks/CodeBlockKeepsIndentation/Chunks: native View budgets", async () => {
  const { fixture, service } = setup();
  fixture.bodies.set("/t/topic/7.json", topic(7, ("<p>" + "😀".repeat(700) + "</p>").repeat(40), Array(19).fill("<p>" + "😀".repeat(3000) + "</p>")));
  fixture.bodies.set("/t/topic/8.json", topic(8, "<p>Step</p><pre><code>ls</code></pre>".repeat(200), Array(19).fill("<p>Reply</p>")));
  fixture.bodies.set("/t/topic/9.json", topic(9, "<p>config:</p><pre><code>  server:\n    port: 80\n</code></pre>"));
  for (const id of [7, 8, 9]) { const view = await draw(service, { t: id }); checkView(view); if (id !== 9) { assert.ok(text(view).includes("too long")); assert.ok(text(view).includes("Popular replies")); } }
  const parts = openingParts(parseTopic(JSON.stringify(fixture.bodies.get("/t/topic/7.json"))), "en"); assert.ok(parts.parts.reduce((n, p) => n + Array.from(p.text).length, 0) <= 12001);
  assert.ok(text(await draw(service, { t: 9 })).includes('  server:\\n    port: 80'));
  assert.deepEqual(chunks("  a:\n    b\n  c:\n    d", 12), ["  a:\n    b", "  c:\n    d"]); assert.equal(clip("  a:\n    b", 6), "  a:…"); assert.deepEqual(chunks("\n\n\n", 2), []); assert.equal(chunks("c".repeat(25), 10).length, 3);
  assert.equal(clip("😀".repeat(5), 3), "😀😀😀…"); assert.ok(chunks("😀".repeat(5000)).every(s => Array.from(s).length <= 1800));
});
test("TestSiteFailures/StaleStandIn: challenge, ordinary list 403/404, missing topic, stale/status recovery", async () => {
  const { fixture, service } = setup(); fixture.bodies.set("/hot.rss", new Response("Just a moment", { status: 403 }));
  await assert.rejects(service.listing("hot"), (e: unknown) => e instanceof ReadError && e.code === "challenge"); assert.equal(service.status().state, "error"); assert.ok(service.status().guide);
  fixture.bodies.set("/hot.rss", rss([{ id: 1 }])); fixture.time++; const reading = await service.listing("hot"); const last = service.status().last_sync;
  for (const status of [403, 404]) {
    fixture.time++; fixture.bodies.set("/hot.rss", new Response("blocked", { status }));
    const stale = await service.listing("hot", true); assert.equal(stale.value, reading.value); assert.equal(stale.at, reading.at); assert.equal(stale.stale?.status, status); assert.equal(service.status().state, "error"); assert.equal(service.status().last_sync, last);
    fixture.bodies.set("/latest.rss", new Response("blocked", { status })); await assert.rejects(service.listing("latest"), (e: unknown) => e instanceof ReadError && e.code === "http");
    fixture.bodies.set("/t/topic/999.json", new Response("blocked", { status })); await assert.rejects(service.thread(999), (e: unknown) => e instanceof ReadError && e.code === "gone");
  }
  // Preserve the actual Go normal-cache-hit semantics, distinct from network Reading.at.
  fixture.time++; const cached = await service.listing("hot"); assert.equal(cached.live, false); assert.equal(cached.at, reading.at); assert.equal(service.status().state, "ok"); assert.notEqual(service.status().last_sync, last);
  fixture.bodies.set("/hot.rss", rss([{ id: 1 }])); const refreshed = await service.listing("hot", true); assert.equal(refreshed.live, true);
});
test("TestJustAMomentInContent and challenge prefix boundary", async () => {
  const { fixture, service } = setup(); fixture.bodies.set("/hot.rss", rss([{ id: 1, title: "Just a moment in good content" }])); fixture.bodies.set("/t/topic/1.json", topic(1, "<p>Just a moment in prose</p>"));
  await service.listing("hot"); await service.thread(1); assert.equal(service.status().state, "ok");
  fixture.bodies.set("/latest.rss", new Response("x".repeat(2048) + "Just a moment", { status: 403 })); await assert.rejects(service.listing("latest"), (e: unknown) => e instanceof ReadError && e.code === "http");
});
test("429 Retry-After/default/minimum, globally paused, cached stand-in and status", async () => {
  for (const [header, expected] of [["", limits.pause], ["7", 60000], ["123", 123000], ["Thu, 01 Jan 1970", limits.pause], ["0", limits.pause]] as const) {
    const { fixture, service } = setup(); await service.listing("hot"); fixture.time++;
    fixture.bodies.set("/hot.rss", new Response("slow", { status: 429, headers: { "Retry-After": header } }));
    const stale = await service.listing("hot", true); assert.equal(stale.stale?.code, "rate"); assert.equal(service.site.stats.paused, fixture.time + expected); assert.equal(service.status().state, "rate_limited");
    const before = fixture.calls.length; await assert.rejects(service.listing("daily"), (e: unknown) => e instanceof ReadError && e.code === "rate"); assert.equal(fixture.calls.length, before);
    fixture.time += expected; fixture.bodies.set("/hot.rss", rss()); await service.listing("hot", true); assert.equal(service.status().state, "ok");
  }
});
test("oversized, unreadable or stalled 429 bodies still pause globally and release the serial queue", async () => {
  for (const shape of ["declared", "streamed", "unreadable", "stalled"] as const) {
    let now = 10_000, calls = 0, cancelled = 0;
    const fetch = async () => {
      calls++;
      if (calls > 1) return new Response("recovered");
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          if (shape === "streamed") controller.enqueue(new Uint8Array(limits.response + 1));
          if (shape === "unreadable") controller.error(new Error("synthetic body failure"));
        },
        cancel() { cancelled++; return new Promise(() => {}); },
      });
      return new Response(body, { status: 429, headers: { "Retry-After": "123", ...(shape === "declared" ? { "Content-Length": String(limits.response + 1) } : {}) } });
    };
    const site = new Site(fetch, { gap: 0, now: () => now, request: 20 });
    const rate = (e: unknown) => e instanceof ReadError && e.code === "rate";
    await assert.rejects(site.get("/hot.rss", 0, raw => raw), rate);
    assert.equal(site.stats.paused, now + 123_000);
    await assert.rejects(site.get("/latest.rss", 0, raw => raw), rate); assert.equal(calls, 1);
    if (shape !== "unreadable") assert.equal(cancelled, 1);
    now += 123_000; assert.equal((await site.get("/latest.rss", 0, raw => raw)).value, "recovered"); assert.equal(calls, 2);
  }
  const { fixture, service } = setup(); const good = await service.listing("hot"); fixture.time++;
  fixture.bodies.set("/hot.rss", new Response("body", { status: 429, headers: { "Retry-After": "123", "Content-Length": String(limits.response + 1) } }));
  const stale = await service.listing("hot", true);
  assert.equal(stale.value, good.value); assert.equal(stale.at, good.at); assert.equal(stale.stale?.code, "rate");
  assert.equal(service.status().state, "rate_limited"); assert.equal(service.site.stats.paused, fixture.time + 123_000);
});
test("cache TTL and forced list refresh, topic refresh retains TTL, shared serial queue and coalescing", async () => {
  const { fixture, service } = setup(); fixture.delay = 5;
  await Promise.all([service.listing("hot"), service.listing("hot"), service.thread(1), service.thread(1)]); assert.equal(fixture.peak, 1); assert.equal(fixture.hits.get("/hot.rss"), 1); assert.equal(fixture.hits.get("/t/topic/1.json"), 1);
  await service.listing("hot", true); assert.equal(fixture.hits.get("/hot.rss"), 2);
  const ui = snapshotPage(service); await ui.action!({ state: '{"t":1}', action: "refresh", locale: "en", ui_version: 2 } as UIRequest); assert.equal(fixture.hits.get("/t/topic/1.json"), 1);
  fixture.time += limits.list; await service.listing("hot"); await service.thread(1); assert.equal(fixture.hits.get("/hot.rss"), 3); assert.equal(fixture.hits.get("/t/topic/1.json"), 1);
  fixture.time += limits.topic; await service.thread(1); assert.equal(fixture.hits.get("/t/topic/1.json"), 2);
  assert.ok(fixture.calls.every(c => c.init?.redirect === "error" && c.init?.method === "GET" && c.init.signal));
});
test("requests wait after the previous request completed, and queue/gap waits cancel", async () => {
  const fixture = new Fixture(); fixture.delay = 10;
  const service = new LinuxDo(fixture.fetch, {}, "UTC", "en", { gap: 25 });
  await Promise.all([service.listing("hot"), service.listing("latest")]); assert.equal(fixture.peak, 1); assert.ok(fixture.calls[1].at - fixture.calls[0].at >= 30);
  fixture.delay = 100; const first = service.listing("daily"); const c = new AbortController(), second = service.listing("weekly", false, c.signal); c.abort(); await assert.rejects(second, { name: "AbortError" }); await first;
  assert.equal(fixture.hits.get("/top.rss?period=weekly"), undefined);
  const gapAbort = new AbortController(), next = service.listing("weekly", false, gapAbort.signal); gapAbort.abort(); await assert.rejects(next, { name: "AbortError" }); assert.equal(service.status().state, "ok");
});
test("cancelled live/stale reads never return stale or alter connector state; request timeout cancels fetch", async () => {
  const { fixture, service } = setup(); await service.thread(1); fixture.time += limits.topic; fixture.delay = 100;
  const c = new AbortController(), reading = service.thread(1, c.signal); await until(() => fixture.active > 0); c.abort(); await assert.rejects(reading, { name: "AbortError" }); assert.equal(service.status().state, "ok"); assert.equal(fixture.cancelled, 1);
  const timed = new LinuxDo(fixture.fetch, {}, "UTC", "en", { gap: 0, request: 5 }); await assert.rejects(timed.listing("daily"), (e: unknown) => e instanceof ReadError && e.code === "slow"); assert.equal(fixture.active, 0); assert.equal(timed.status().state, "error");
});
test("cancelled pending cache hits preserve both list and topic connector failures and last_sync", async () => {
  for (const kind of ["list", "topic"] as const) {
    const { fixture, service } = setup();
    const read = (signal?: AbortSignal) => kind === "list" ? service.listing("hot", false, signal) : service.thread(1, signal);
    await read(); fixture.time++;
    fixture.bodies.set("/latest.rss", new Response("temporarily broken", { status: 503 }));
    await assert.rejects(service.listing("latest"), (e: unknown) => e instanceof ReadError && e.status === 503);
    const before = service.status(), requests = fixture.calls.length; fixture.time++;
    const controller = new AbortController(), pending = read(controller.signal); controller.abort();
    await assert.rejects(pending, { name: "AbortError" });
    assert.deepEqual(service.status(), before); assert.equal(fixture.calls.length, requests);
    // A completed, uncancelled hit retains the original Go recovery semantics.
    await read(); assert.equal(service.status().state, "ok"); assert.notEqual(service.status().last_sync, before.last_sync);
  }
  const { fixture, service } = setup(); await service.listing("hot"); fixture.time++;
  fixture.bodies.set("/hot.rss", new Response("temporarily broken", { status: 503 }));
  await service.listing("hot", true); const before = service.status(); fixture.time++;
  const controller = new AbortController(), pending = service.listing("hot", false, controller.signal); controller.abort();
  await assert.rejects(pending, { name: "AbortError" }); assert.deepEqual(service.status(), before);
});
test("cache entry/byte caps, replacement accounting, TTL prune before eviction, retained parsed objects only", async () => {
  const fixture = new Fixture(), site = new Site(fixture.fetch, { now: () => fixture.time, gap: 0, entries: 3, bytes: 12 });
  for (const path of ["/a", "/b", "/c", "/d"]) fixture.bodies.set(path, "xxxx");
  for (const path of ["/a", "/b", "/c", "/d"]) { await site.get(path, limits.list, s => s); fixture.time++; }
  assert.deepEqual(site.stats.entries, 3); assert.equal(site.stats.bytes, 12); await site.get("/a", limits.list, s => s); assert.equal(fixture.hits.get("/a"), 2);
  fixture.bodies.set("/a", "x"); await site.get("/a", 0, s => s); assert.equal(site.stats.bytes, 9);
  const other = new Site(fixture.fetch, { now: () => fixture.time, gap: 0, entries: 2 }); fixture.bodies.set("/expired.rss", "x"); fixture.bodies.set("/t/topic/9.json", "x"); fixture.bodies.set("/fresh.rss", "x");
  await other.get("/expired.rss", 0, s => s); await other.get("/t/topic/9.json", 0, s => s); fixture.time += limits.list + 1; await other.get("/fresh.rss", 0, s => s);
  await other.get("/t/topic/9.json", limits.topic, s => s); assert.equal(fixture.hits.get("/t/topic/9.json"), 1);
  const capped = new Site(fixture.fetch, { now: () => fixture.time++, gap: 0 }); for (let i = 0; i < 210; i++) { fixture.bodies.set(`/x${i}`, "x"); await capped.get(`/x${i}`, 0, s => s); } assert.equal(capped.stats.entries, 200);
});
test("malformed/oversize answers never replace good cached data, missing topic never stands in", async () => {
  const { fixture, service } = setup(); const before = await service.listing("hot");
  for (const response of [new Response("<html>bad</html>"), new Response("x".repeat(limits.response + 1)), new Response("small", { headers: { "Content-Length": String(limits.response + 1) } })]) {
    fixture.time++; fixture.bodies.set("/hot.rss", response); const stale = await service.listing("hot", true); assert.equal(stale.value, before.value); assert.equal(stale.stale?.code, "bad");
  }
  await service.thread(1); fixture.time += limits.topic; fixture.bodies.set("/t/topic/1.json", new Response("missing", { status: 404 })); await assert.rejects(service.thread(1), (e: unknown) => e instanceof ReadError && e.code === "gone");
  fixture.bodies.set("/t/topic/1.json", { id: 0, post_stream: { posts: [] } }); await assert.rejects(service.thread(1), (e: unknown) => e instanceof ReadError && e.code === "gone");
  const invalid = topic(1); invalid.post_stream.posts[0].cooked = 123;
  for (const raw of ["{", "[]", "42", JSON.stringify({ id: 1, post_stream: { posts: {} } }), JSON.stringify(invalid)]) {
    fixture.bodies.set("/t/topic/1.json", raw);
    assert.equal((await service.thread(1)).stale?.code, "bad");
  }
});
test("signed-int64 counters and reply floors retain exact text, ranking and likes beyond JS precision", async () => {
  const doc = topic(); doc.posts_count = "upper"; doc.views = "lower"; doc.like_count = "larger";
  doc.post_stream.posts[0].reaction_users_count = "large";
  doc.post_stream.posts[0].actions_summary[0].count = "larger";
  doc.post_stream.posts[1].post_number = "upper"; doc.post_stream.posts[2].post_number = "previous";
  doc.post_stream.posts[1].score = 2; doc.post_stream.posts[2].score = 2;
  const raw = JSON.stringify(doc).replaceAll('"upper"', "9223372036854775807").replaceAll('"lower"', "-9223372036854775808")
    .replaceAll('"large"', "9007199254740992").replaceAll('"larger"', "9007199254740993").replaceAll('"previous"', "9223372036854775806");
  const parsed = parseTopic(raw);
  assert.equal(parsed.postsCount, "9223372036854775807"); assert.equal(parsed.views, "-9223372036854775808");
  assert.equal(parsed.posts[0].likes, "9007199254740993");
  assert.deepEqual(bestReplies(parsed, 2).map(p => p.number), ["9223372036854775806", "9223372036854775807"]);
  for (const invalid of ["9223372036854775808", "-9223372036854775809"]) assert.throws(() => parseTopic(raw.replace("-9223372036854775808", invalid)));
  const { fixture, service } = setup(); fixture.bodies.set("/t/topic/1.json", raw);
  const view = text(await draw(service, { t: 1 })), tool = await callTool(service, "linuxdo_topic", { topic: 1 });
  for (const value of ["9223372036854775807", "9007199254740993", "#9223372036854775806"]) { assert.ok(view.includes(value)); assert.ok(tool.includes(value)); }
});
test("integer topic fields reject fractional/rounded numeric lexemes without replacing good body or metadata", async () => {
  const paths = [
    ["posts_count"], ["like_count"], ["views"], ["participant_count"],
    ["post_stream", "posts", 0, "post_number"], ["post_stream", "posts", 0, "post_type"],
    ["post_stream", "posts", 1, "reaction_users_count"],
    ["post_stream", "posts", 0, "actions_summary", 0, "id"], ["post_stream", "posts", 0, "actions_summary", 0, "count"],
  ] as const;
  for (const path of paths) {
    const { fixture, service } = setup(); const good = await service.thread(1); fixture.time += limits.topic;
    const invalid = topic(1, "<p>Invalid replacement must not appear</p>");
    let parent: any = invalid;
    for (const key of path.slice(0, -1)) parent = parent[key];
    const field = path.at(-1)!; parent[field] = "numeric-marker";
    for (const token of ["1.5", "1.0", "1e0", "1.0000000000000001", "1e309", '"1"']) {
      const raw = JSON.stringify(invalid).replace('"numeric-marker"', token);
      assert.throws(() => parseTopic(raw), `accepted ${path.join(".")}=${token}`);
      fixture.bodies.set("/t/topic/1.json", raw);
      const reading = await service.thread(1);
      assert.equal(reading.value, good.value); assert.equal(reading.at, good.at); assert.equal(reading.live, false); assert.equal(reading.stale?.code, "bad");
      assert.equal(opening(reading.value)?.cooked, "<p>Synthetic opening</p>"); assert.equal(service.status().state, "error");
      const view = text(await draw(service, { t: 1 })), tool = await callTool(service, "linuxdo_topic", { topic: 1 });
      assert.ok(view.includes("Synthetic opening")); assert.ok(tool.includes("Synthetic opening"));
      assert.ok(!view.includes("Invalid replacement")); assert.ok(!tool.includes("Invalid replacement"));
    }
  }
  const valid = topic(); valid.post_stream.posts[0].score = 1.5;
  assert.equal(parseTopic(JSON.stringify(valid)).posts[0].score, 1.5);
  valid.post_stream.posts[0].post_number = null; valid.posts_count = null;
  assert.equal(parseTopic(JSON.stringify(valid)).posts[0].number, 0); assert.equal(parseTopic(JSON.stringify(valid)).postsCount, 0);
});
test("TestTools/ToolKeywordsNarrow/TopicID: contract, bounds, no extra pagination/network or write grants", async () => {
  const { fixture, service } = setup({ keywords: ["Rust", "GPU"] });
  assert.deepEqual(tools.map(t => t.name), ["linuxdo_hot", "linuxdo_topic"]); assert.equal(tools[1].input_schema.properties.topic!.type, "string");
  const narrowed = await callTool(service, "linuxdo_hot", { keywords: ["gpu"] }); assert.ok(narrowed.includes("id 4")); assert.ok(!narrowed.includes("id 1"));
  assert.ok((await callTool(service, "linuxdo_hot", { filtered: false, keywords: ["gpu"] })).includes("id 4"));
  fixture.bodies.set("/latest.rss", rss(Array.from({ length: 31 }, (_, i) => ({ id: i + 100, title: `Synthetic ${i}` }))));
  for (const limit of [0, 31, -1]) assert.equal((await callTool(service, "linuxdo_hot", { list: "latest", filtered: false, limit })).match(/\(id /gu)?.length, 15);
  assert.equal((await callTool(service, "linuxdo_hot", { list: "latest", filtered: false, limit: 30 })).match(/\(id /gu)?.length, 30);
  for (const id of [1, "1", "https://linux.do/t/topic/1/3", "/t/some-slug/1/7", "/t/1/7"]) { const output = await callTool(service, "linuxdo_topic", { topic: id, replies: 1 }); assert.ok(output.includes("#3 fixture-3")); assert.ok(!output.includes("#2 fixture-2")); }
  assert.ok(!(await callTool(service, "linuxdo_topic", { topic: 1, replies: 0 })).includes("#3"));
  for (const args of [{ list: "monthly" }, { keywords: [1] }, { limit: 1.2 }, { filtered: "false" }]) await assert.rejects(callTool(service, "linuxdo_hot", args));
  await assert.rejects(callTool(service, "linuxdo_topic", { topic: "hello" })); await assert.rejects(callTool(service, "unknown"));
  assert.ok(fixture.calls.every(c => c.url.startsWith("https://linux.do/"))); assert.equal(fixture.hits.get("/t/topic/1.json"), 1); assert.ok(!fixture.calls.some(c => c.url.includes("page=")));
});
test("topic IDs retain int64 text and the conservative unsafe-number guard, do not fetch supplied origin", async () => {
  for (const [input, expected] of [[123, "123"], ["456", "456"], ["/t/123/4", "123"], ["/t/some-slug/42/7", "42"], ["9007199254740993", "9007199254740993"], ["9223372036854775807", "9223372036854775807"]] as const) assert.equal(topicID(input), expected);
  for (const input of ["hello", "12 words", -1, 1.5, {}, "9223372036854775808", 9007199254740992, 9223372036854775808]) assert.throws(() => topicID(input));
  const { fixture, service } = setup();
  assert.deepEqual(readState('{"l":"weekly","t":9007199254740993,"a":true}', service), { l: "weekly", t: "9007199254740993", a: true });
  assert.equal(readState('{"t":9223372036854775807}', service).t, "9223372036854775807");
  await callTool(service, "linuxdo_topic", { topic: "https://example.com/t/topic/1" }); assert.equal(fixture.calls[0].url, "https://linux.do/t/topic/1.json");
  const raw = JSON.stringify(topic()).replace('"id":1,', '"id":9007199254740993,'); assert.equal(parseTopic(raw).id, "9007199254740993");
});
test("tool body/replies/total text budgets preserve rune clipping", async () => {
  const { fixture, service } = setup(); fixture.bodies.set("/t/topic/8.json", topic(8, "<p>" + "😀".repeat(10000) + "</p>", Array(30).fill("<p>" + "😀".repeat(2000) + "</p>")));
  const output = await callTool(service, "linuxdo_topic", { topic: 8, replies: 99 }); assert.ok(Array.from(output).length <= 30001); assert.equal(output.match(/#\d+ fixture/gu)?.length, 20); assert.ok(Array.from(await callTool(service, "linuxdo_topic", { topic: 8, replies: -1 })).length < 8500);
});
test("connector namespaces/config and locale resources mirror en/zh-Hans/ja", async () => {
  const { service } = setup(); const handler = connector(service);
  assert.equal((await handler("tools.list", {}) as { tools: unknown[] }).tools.length, 2); assert.equal((await handler("status", {}) as { state: string }).state, "ok"); assert.ok((await handler("tools.call", { name: "linuxdo_hot" }) as { text: string }).text);
  await assert.rejects(async () => handler("status", { contribution: "other" }), { code: "no_contribution" }); await assert.rejects(async () => handler("inbox.collect", {}), { code: "unknown_method" });
  const keys = Object.keys(resources.en).sort(); for (const resource of Object.values(resources)) { assert.deepEqual(Object.keys(resource).sort(), keys); for (const key of keys) { const value = resource[key as keyof typeof resource], expected = resources.en[key as keyof typeof resources.en]; if (typeof value === "string" && typeof expected === "string") { assert.ok(value); assert.deepEqual((value.match(/\{\d+\}/gu) ?? []).sort(), (expected.match(/\{\d+\}/gu) ?? []).sort()); } } }
  assert.equal(textsFor("en-US"), resources.en); assert.equal(textsFor("ja_JP"), resources.ja); assert.equal(textsFor(""), resources["zh-Hans"]); assert.equal(textsFor("fr"), resources["zh-Hans"]);
  assert.equal(date(Date.parse("2026-11-03T09:37:02Z"), "Asia/Tokyo"), "11-03 18:37");
  for (const locale of ["en", "ja", "zh-Hans"]) checkView(await draw(service, {}, locale));
});
test("server Root loading/live effects/list/all/topic/back/refresh and replay vs stale events", async () => {
  const { fixture, service } = setup(); let snapshot!: Snapshot;
  const renderer = new Renderer({ authorize: () => true, onRender: value => { snapshot = value; } });
  try {
    renderer.mount(root({ service, locale: "en", uiVersion: 2 })); assert.ok(text(snapshot.view).includes("Reading LINUX DO")); await until(() => text(snapshot.view).includes("Hot · 3 / 5"));
    const request = interaction(snapshot, "all"), stale = interaction(snapshot, "all"); await Promise.all([renderer.event(request), renderer.event(request)]); await until(() => text(snapshot.view).includes("Hot · all 5")); await assert.rejects(renderer.event(stale));
    await renderer.event(interaction(snapshot, "topic-1")); await until(() => text(snapshot.view).includes("Popular replies"));
    await renderer.event(interaction(snapshot, "back")); await until(() => text(snapshot.view).includes("Hot · all 5"));
    await renderer.event(interaction(snapshot, "list", { list: "daily" })); await until(() => text(snapshot.view).includes("Daily top · all 1"));
    await renderer.event(interaction(snapshot, "refresh")); await until(() => fixture.hits.get("/top.rss?period=daily") === 2 && text(snapshot.view).includes("Daily top · all 1")); checkView(snapshot.view);
  } finally { renderer.dispose(); }
});
test("suspend/dispose abort loads, resume rechecks shared cache without an unchanged publication", async () => {
  const { fixture, service } = setup(); fixture.delay = 100; let snapshot!: Snapshot;
  const renderer = new Renderer({ authorize: () => true, onRender: value => { snapshot = value; } });
  renderer.mount(root({ service, locale: "en", uiVersion: 2 })); await until(() => fixture.active > 0); renderer.suspend(); await until(() => fixture.active === 0); assert.equal(fixture.cancelled, 1);
  fixture.delay = 0; renderer.resume(); await until(() => text(snapshot.view).includes("topic-1")); const keys = snapshot.view.sections.flatMap(s => s.items).map(it => it.key); assert.equal(new Set(keys).size, keys.length);
  renderer.suspend(); const resumed = renderer.resume(); await pause(10); assert.equal(snapshot.version, resumed.version); await renderer.event(interaction(resumed, "all"));
  renderer.dispose();
  const second = new Renderer({ authorize: () => true }); fixture.delay = 100; second.mount(root({ service, state: { l: "weekly" }, locale: "en" })); await until(() => fixture.active > 0); second.dispose(); await until(() => fixture.active === 0); assert.equal(service.status().state, "ok");
});
test("late old-list effect cannot overwrite newer identity; duplicate topics/replies have stable keys", async () => {
  const { fixture, service } = setup(); fixture.bodies.set("/hot.rss", rss([{ id: 1 }, { id: 1 }])); fixture.delay = 30; let snapshot!: Snapshot;
  const renderer = new Renderer({ authorize: () => true, onRender: value => { snapshot = value; } });
  try {
    renderer.mount(root({ service, locale: "en", uiVersion: 2 })); await until(() => fixture.active > 0);
    await renderer.event(interaction(snapshot, "list", { list: "daily" })); await until(() => text(snapshot.view).includes("Daily top · 1 / 1")); assert.ok(!text(snapshot.view).includes("Hot ·")); assert.ok(fixture.cancelled > 0);
  } finally { renderer.dispose(); }
  fixture.delay = 0; assert.equal(parseFeed(fixture.bodies.get("/hot.rss") as string).length, 1);
});
