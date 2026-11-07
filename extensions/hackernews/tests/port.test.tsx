import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { Renderer, type Snapshot, type UIEvent } from "@persona/extension/ui";
import { connect, type UIRequest, type View } from "@persona/extension";
import { Fixture, epoch } from "./fixtures.ts";
import { HackerNews, ReadError, limits } from "../src/service.ts";
import { callTool, connector, tools } from "../src/tools.ts";
import { active, chunks, clip, feeds, filter, gone, itemID, plain, safeLink } from "../src/model.ts";
import { ago, resources, textsFor } from "../src/i18n.ts";
import { page, loadPage, readState, transition } from "../src/page.tsx";
import { root } from "../src/root.tsx";
import { snapshotPage } from "../src/snapshot.ts";
const setup = (config = {}) => { const fixture = new Fixture(); return { fixture, service: new HackerNews(fixture.fetch, config, () => fixture.time) }; };
const text = (view: View) => JSON.stringify(view);
const draw = async (service: HackerNews, state: unknown = {}, locale = "en", uiVersion = 3) => {
  const parsed = readState(state, service), loaded = await loadPage(service, parsed);
  const renderer = new Renderer({ authorize: () => false });
  try { return renderer.mount(page({ service, state: parsed, loaded, locale, uiVersion })).view; } finally { renderer.dispose(); }
};
async function until(predicate: () => boolean) {
  const deadline = Date.now() + 2500;
  while (!predicate()) { if (Date.now() > deadline) throw new Error("fixture render did not settle"); await new Promise(resolve => setTimeout(resolve, 1)); }
}
function event(snapshot: Snapshot, key: string, values: Record<string, string> = {}, eventId = crypto.randomUUID()): UIEvent {
  const node = snapshot.view.sections.flatMap(s => s.items).find(it => it.key === key);
  assert.ok(node && "action" in node && node.action);
  return { generation: snapshot.generation, version: snapshot.version, eventId, key, actionId: node.action.id, values };
}
test("all five feeds, ranked order, deleted/dead/missing, bounded list", async () => {
  const { fixture, service } = setup();
  for (const feed of Object.keys(feeds) as (keyof typeof feeds)[]) {
    const listing = await service.stories(feed, 300);
    assert.deepEqual(listing.stories.map(it => it.id), [1, 2, 3]); assert.equal(listing.missed, 0);
  }
  fixture.bodies.set("/newstories.json", [999, 4]); fixture.time += limits.fresh;
  assert.equal((await service.stories("new")).stories.length, 0);
});
test("normal cache and refresh floor preserve request counts", async () => {
  const { fixture, service } = setup();
  await service.stories("top"); await service.stories("top"); await service.stories("top", 30, true);
  assert.equal(fixture.hits.get("/topstories.json"), 1); assert.equal(fixture.hits.get("/item/1.json"), 1);
  fixture.time += 31_000; await service.stories("top", 30, true); assert.equal(fixture.hits.get("/topstories.json"), 2);
});
test("stale list, error/retry, status recovery and no account", async () => {
  const { fixture, service } = setup(); await service.stories("top");
  fixture.time += 3_600_000; for (const id of [1, 2, 3, 4]) await service.item(id); fixture.time++;
  fixture.bodies.set("/topstories.json", new Response("down", { status: 500 }));
  let output = text(await draw(service));
  assert.ok(output.includes("This read failed: Hacker News answered HTTP 500. Showing what was read 1h ago"));
  assert.ok(output.includes("Synthetic Rust article")); assert.equal(service.status("en").state, "error");
  fixture.bodies.set("/newstories.json", new Response("<html>"));
  output = text(await draw(service, { l: "new" })); assert.ok(output.includes("Could not read")); assert.ok(output.includes("Retry"));
  fixture.bodies.set("/topstories.json", [1]); fixture.time++;
  await service.stories("top", 30, true); assert.equal(service.status("en").state, "ok"); assert.ok(service.status().last_sync);
  assert.equal(textsFor("en").account, service.status("en").account);
});
test("a stale story, comment or list item carries the note and its read time", async () => {
  const { fixture, service } = setup(); await service.thread(1);
  fixture.time += 3 * 3_600_000;
  for (const id of [1, 11, 12, 13]) fixture.bodies.set(`/item/${id}.json`, new Response("down", { status: 500 }));
  await service.stories("top");
  const story = text(await draw(service, { s: 1 }));
  assert.ok(story.includes("This read failed: Hacker News answered HTTP 500. Showing what was read 3h ago")); assert.ok(story.includes("Synthetic comment"));
  const tool = await callTool(service, "hn_item", { item: 1 });
  assert.ok(tool.includes("(This read failed: Hacker News answered HTTP 500. This is what was read 3h ago.)")); assert.ok(tool.includes("- fixture-b ("));
  // The ids were read just now, story 1 is the older copy: the list is as old as its oldest story.
  const list = text(await draw(service));
  assert.ok(list.includes("Showing what was read 3h ago")); assert.ok(list.includes("Read 3h ago")); assert.ok(list.includes("Synthetic Rust article"));
  assert.equal(service.status("en").state, "error");
});
test("filter matches title/domain, exclusions and score; show-all", async () => {
  const { service } = setup({ keywords: ["RUST", " EXAMPLE.NET "], exclude_keywords: ["ask hn"], min_score: 10 });
  const listing = await service.stories("top"); assert.deepEqual(filter(listing.stories, service.config).map(it => it.id), [1]);
  assert.ok(active(service.config)); assert.ok(text(await draw(service)).includes("Top · 1 of 3"));
  const all = text(await draw(service, { a: true })); assert.ok(all.includes("Top · all 3")); assert.ok(all.includes("Filter by my interests"));
  assert.deepEqual(filter(listing.stories, { keywords: ["EXAMPLE.NET"], exclude_keywords: [], min_score: 0 }).map(it => it.id), [3]);
});
test("empty, gone and missing pages remain distinct", async () => {
  const { fixture, service } = setup(); fixture.bodies.set("/topstories.json", []);
  assert.ok(text(await draw(service)).includes("Nothing to show here"));
  assert.ok(text(await draw(service, { s: 4 })).includes("deleted or hidden"));
  assert.ok(text(await draw(service, { s: 999 })).includes("has no such item"));
});
test("old APP fitted snapshots, native push/back, all locales", async () => {
  const { service } = setup();
  const old = await draw(service, {}, "", 1); assert.equal(old.sections[0].items[0].type, "row"); assert.ok(text(old).includes("正在看"));
  const modern = await draw(service); assert.equal(modern.sections[0].items[0].type, "segmented");
  const row = modern.sections.flatMap(s => s.items).find(it => it.key === "story-1");
  assert.ok(row?.type === "row" && row.action?.push?.state); assert.equal(JSON.parse(row.action.push.state).s, 1);
  assert.ok(text(await draw(service, { s: 1 }, "en", 2)).includes("Back to list"));
  assert.ok(!text(await draw(service, { s: 1 }, "en", 3)).includes("Back to list"));
  assert.ok(text(await draw(service, {}, "ja-JP")).includes("トップ · 3 件"));
  assert.ok(text(await draw(service, { s: 2 }, "zh-Hans")).includes("Synthetic / text"));
});
test("story HTML text, ranked top-level comments and safe in-app links", async () => {
  const { service } = setup(); const output = text(await draw(service, { s: 1 }));
  for (const expected of ["fixture-b · 10m ago", "First 2 top-level comments of 3", "https://example.org/full", "Open article", "https://news.ycombinator.com/item?id=1"]) assert.ok(output.includes(expected));
  const item = await service.item(1);
  for (const url of ["http://example.com", "javascript:alert(1)", "file:///tmp/a", "https://me:secret@example.com"]) assert.equal(safeLink({ ...item, url }), "");
  assert.ok(!text(await draw(service, { s: 3 })).includes("Open article"));
});
test("native push retains the bounded story title and hidden stories expose no article link", async () => {
  const { fixture, service } = setup();
  fixture.item({ id: 1, type: "story", title: "😀".repeat(300), url: "https://example.com/article" });
  const view = await snapshotPage(service).render({ locale: "en", ui_version: 3 } as UIRequest);
  const row = view.sections.flatMap(section => section.items).find(it => it.key === "story-1");
  assert.ok(row?.type === "row" && row.action?.push);
  assert.equal(row.action.push.title, clip("😀".repeat(300), 200));
  for (const flag of ["dead", "deleted"]) {
    fixture.item({ id: 9, type: "story", title: "Hidden fixture article", url: "https://example.com/hidden", [flag]: true });
    const hidden = await draw(new HackerNews(fixture.fetch), { s: 9 });
    assert.ok(text(hidden).includes("deleted or hidden"));
    assert.ok(!text(hidden).includes("Open article"));
    assert.ok(text(hidden).includes("https://news.ycombinator.com/item?id=9"));
  }
});
test("bounded story/comments/text, fetch parallelism and no nested comments", async () => {
  const { fixture, service } = setup(); fixture.delay = 2;
  const kids = Array.from({ length: 40 }, (_, i) => i + 100);
  fixture.item({ id: 5, title: "T".repeat(500), text: "word ".repeat(5000), kids, descendants: 40 });
  for (const id of kids) fixture.item({ id, text: "word ".repeat(5000), by: "fixture", kids: [500] });
  const view = await draw(service, { s: 5 });
  assert.equal(Array.from(view.title ?? "").length, 200); assert.ok(text(view).includes("First 15 top-level comments of 40"));
  assert.equal(fixture.hits.get("/item/122.json"), undefined); assert.equal(fixture.hits.get("/item/500.json"), undefined);
  assert.ok(fixture.peak <= 8 && fixture.peak > 1);
  for (const component of view.sections.flatMap(s => s.items)) if (component.type === "text") assert.ok(Array.from(component.text).length <= 1800);
});
test("bounded cache evicts oldest, failed items/partial versus total failure", async () => {
  const fixture = new Fixture(), service = new HackerNews(fixture.fetch, {}, () => fixture.time++, 3);
  for (const id of [1, 2, 3, 4, 1]) await service.item(id);
  assert.equal(fixture.hits.get("/item/1.json"), 2);
  const other = setup(); other.fixture.bodies.set("/item/1.json", new Response("fail", { status: 503 }));
  assert.equal((await other.service.stories("top")).missed, 1);
  other.fixture.bodies.set("/newstories.json", [90]); other.fixture.bodies.set("/item/90.json", new Response("fail", { status: 503 }));
  await assert.rejects(other.service.stories("new"), (error: unknown) => error instanceof ReadError && error.code === "all_missed");
});
test("malformed, wrong-ID, oversize, null, redirect/network errors", async () => {
  for (const response of [new Response("{") , new Response(JSON.stringify({ id: 2 })), new Response("x".repeat(limits.response + 1))]) {
    const service = new HackerNews(async () => response);
    await assert.rejects(service.item(1), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  }
  await assert.rejects(new HackerNews(async () => new Response("null")).item(1), /no such item/);
  await assert.rejects(new HackerNews(async () => { throw new Error("private transport detail"); }).item(1), /cannot reach/i);
  const { fixture, service } = setup(); fixture.bodies.set("/topstories.json", [1, 1, 2]);
  assert.deepEqual((await service.stories("top")).stories.map(it => it.id), [1, 2]);
});
test("cancellation never returns stale data or changes connector error", async () => {
  const { fixture, service } = setup(); await service.item(1); fixture.time += limits.fresh;
  fixture.delay = 100; const abort = new AbortController();
  const promise = service.item(1, false, abort.signal); abort.abort(); await assert.rejects(promise, { name: "AbortError" });
  assert.equal(service.status().state, "ok");
  const before = fixture.hits.size; await assert.rejects(service.item(2, false, abort.signal), { name: "AbortError" }); assert.equal(fixture.hits.size, before);
});
test("assistant tools preserve filters, IDs/links, metadata and trust warning", async () => {
  const { service } = setup({ min_score: 10 });
  let out = await callTool(service, "hn_stories"); assert.ok(out.includes("3 stories, 2 after filtering by at least 10 points")); assert.ok(!out.includes("Synthetic old site"));
  out = await callTool(service, "hn_stories", { filtered: false, keywords: ["old"], limit: 1 }); assert.ok(out.includes("Synthetic old site"));
  for (const item of [1, "1", "https://news.ycombinator.com/item?id=1", "news.ycombinator.com/item?id=1#11"]) {
    out = await callTool(service, "hn_item", { item, comments: 1 });
    assert.ok(out.includes('"Synthetic Rust article" https://news.ycombinator.com/item?id=1'));
    assert.ok(out.includes("Top 1 top-level comments (3 comments in all):")); assert.ok(out.includes("fixture-b (10m ago, id 11)"));
  }
  for (const t of tools) assert.ok(t.description.includes("not the user's instruction"));
  for (const item of ["https://example.com/item?id=1", "abc", -3, {}, "9007199254740993"]) await assert.rejects(callTool(service, "hn_item", { item }));
  await assert.rejects(callTool(service, "hn_stories", { list: "jobs" })); await assert.rejects(callTool(service, "hn_stories", { keywords: [3] }));
  await assert.rejects(callTool(service, "hn_item", { item: 4 }), /deleted/); await assert.rejects(callTool(service, "nope"));
});
test("tool bodies and total answers stay bounded", async () => {
  const { fixture, service } = setup();
  fixture.item({ id: 8, title: "title", text: "字".repeat(10000), kids: [80], descendants: 1 });
  fixture.item({ id: 80, text: "字".repeat(10000) });
  assert.ok(Array.from(await callTool(service, "hn_item", { item: 8 })).length <= 30_000);
  assert.ok(Array.from(await callTool(service, "hn_item", { item: 8, comments: 0 })).length < 6500);
});
test("connector namespace and config fallback", async () => {
  const { service } = setup({ list: "jobs", min_score: -5, keywords: [" go "] });
  assert.equal(service.config.list, "top"); assert.equal(service.config.min_score, 0); assert.deepEqual(service.config.keywords, ["go"]);
  const handler = connector(service); assert.equal((await handler("tools.list", {}) as { tools: unknown[] }).tools.length, 2);
  assert.equal((await handler("status", {}) as { state: string }).state, "ok");
  await assert.rejects(Promise.resolve().then(() => handler("status", { contribution: "other" })), { code: "no_contribution" });
  await assert.rejects(Promise.resolve().then(() => handler("sync", {})), { code: "unknown_method" });
  assert.ok((await handler("tools.call", { name: "hn_stories", args: {} }) as { text: string }).text);
});
test("locale resource parity and fallback, Unicode text helpers, state transitions", () => {
  const keys = Object.keys(resources.en).sort();
  for (const resource of Object.values(resources)) {
    assert.deepEqual(Object.keys(resource).sort(), keys);
    for (const [key, value] of Object.entries(resource)) if (typeof value === "string") { assert.ok(value); assert.equal((value.match(/%[sd]/g) ?? []).join(), (resources.en[key as keyof typeof resources.en] as string).match?.(/%[sd]/g)?.join() ?? ""); }
  }
  for (const locale of ["", "fr", "zh-Hans"]) assert.equal(textsFor(locale), resources["zh-Hans"]);
  assert.equal(textsFor("en_US"), resources.en); assert.equal(ago(resources.en, 10_800_000), "3h ago");
  assert.equal(plain("a<p>b &amp; &#x27;q&#x27;"), "a\n\nb & 'q'"); assert.equal(plain("<pre><code>  a\n  b\n</code></pre>"), "  a\n  b");
  assert.equal(clip("😀".repeat(5), 3), "😀😀…"); assert.ok(chunks("😀".repeat(5000)).every(c => Array.from(c).length <= 1800));
  assert.deepEqual(chunks("  a:\n    b\n  c:\n    d", 12), ["  a:\n    b", "  c:\n    d"]); assert.equal(clip("  a:\n    b", 6), "  a:…");
  const { service } = setup({ list: "ask" }); assert.equal(readState('{"l":"jobs"}', service).l, "ask");
  const state = readState(null, service); assert.equal(transition(state, "list", { list: "show" }).l, "show");
  assert.equal(transition(state, "story:2").s, 2); assert.equal(transition({ ...state, s: 2 }, "back").s, 0); assert.equal(transition(state, "all").a, true);
});
test("legacy snapshot service uses actual SDK Page contract", async () => {
  const { service } = setup(); const ui = snapshotPage(service);
  const req = { locale: "en", ui_version: 2 } as UIRequest;
  const result = await ui.action!({ ...req, action: "story:1" }); assert.ok(result?.view && text(result.view).includes("Back to list"));
  const view = await ui.render(req); assert.ok(text(view).includes("Synthetic Rust article"));
});
test("server hooks: loading, list change, filter toggle, story/back, refresh, replay and stale events", async () => {
  const { fixture, service } = setup({ min_score: 10 });
  let snapshot!: Snapshot;
  const renderer = new Renderer({ authorize: () => true, onRender: value => { snapshot = value; } });
  renderer.mount(root({ service, locale: "en" })); assert.ok(text(snapshot.view).includes("Reading Hacker News"));
  await until(() => text(snapshot.view).includes("Top · 2 of 3"));
  const stale = event(snapshot, "all"), request = event(snapshot, "all");
  await Promise.all([renderer.event(request), renderer.event(request)]);
  await until(() => text(snapshot.view).includes("Top · all 3"));
  await assert.rejects(renderer.event(stale));
  await renderer.event(event(snapshot, "story-1")); await until(() => text(snapshot.view).includes("First 2 top-level comments"));
  await renderer.event(event(snapshot, "back")); await until(() => text(snapshot.view).includes("Top · all 3"));
  await renderer.event(event(snapshot, "feed", { list: "show" })); await until(() => text(snapshot.view).includes("Show HN · all 3"));
  fixture.time += limits.refresh + 1; await renderer.event(event(snapshot, "refresh"));
  await until(() => text(snapshot.view).includes("Show HN · all 3")); assert.equal(fixture.hits.get("/showstories.json"), 2);
  renderer.dispose(); await assert.rejects(renderer.event(event(snapshot, "refresh")));
});
test("unmount and reconnect do not publish late effects; new generation rejects old callbacks", async () => {
  const { fixture, service } = setup(); fixture.delay = 30;
  let count = 0; const renderer = new Renderer({ authorize: () => true, onRender: () => count++ });
  renderer.mount(root({ service, locale: "en" })); await Promise.resolve(); renderer.dispose();
  await new Promise(resolve => setTimeout(resolve, 40)); assert.equal(count, 1);
  let snapshot!: Snapshot;
  const resumed = new Renderer({ authorize: () => true, onRender: s => { snapshot = s; } });
  resumed.mount(root({ service, locale: "en" })); await until(() => text(snapshot.view).includes("Top · 3"));
  const old = event(snapshot, "refresh"); resumed.suspend(); resumed.resume();
  await until(() => text(snapshot.view).includes("Top · 3"));
  // Reconnect retains this renderer generation but invalidates the old version.
  await assert.rejects(resumed.event(old)); resumed.dispose();
  const fresh = new Renderer({ authorize: () => true }); fresh.mount(root({ service, locale: "en" }));
  await assert.rejects(fresh.event(old)); fresh.dispose();
});
test("gone resources never use stale fallback; item validation cannot poison a good cache", async () => {
  const { fixture, service } = setup(); await service.item(1); fixture.time += limits.fresh;
  fixture.bodies.set("/item/1.json", { id: 999 });
  assert.equal((await service.item(1)).id, 1);
  fixture.bodies.delete("/item/1.json");
  await assert.rejects(service.item(1), (error: unknown) => error instanceof ReadError && error.code === "not_found");
});
test("rapid feed selection ignores late effects and denied callbacks cannot change state", async () => {
  const { fixture, service } = setup();
  let snapshot!: Snapshot, allow = true;
  const renderer = new Renderer({ authorize: () => allow, onRender: value => { snapshot = value; } });
  renderer.mount(root({ service, locale: "en" })); await until(() => text(snapshot.view).includes("Top · 3"));
  const top = snapshot;
  allow = false; await assert.rejects(renderer.event(event(top, "feed", { list: "ask" })));
  assert.ok(text(snapshot.view).includes("Top · 3")); allow = true;
  fixture.delay = 20;
  await renderer.event(event(snapshot, "feed", { list: "new" }));
  await until(() => JSON.parse(snapshot.view.state ?? "{}").l === "new" && text(snapshot.view).includes("Reading Hacker News"));
  await renderer.event(event(snapshot, "feed", { list: "show" }));
  await until(() => text(snapshot.view).includes("Show HN · 3"));
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.ok(text(snapshot.view).includes("Show HN · 3"));
  assert.ok(!text(snapshot.view).includes("New · 3"));
  renderer.dispose();
});
test("legacy snapshot downgrade works through the process SDK", async () => {
  const { service } = setup();
  const answers = new Map<string, { error?: unknown; result: any }>();
  const connection = connect({ id: "hackernews", version: "0.1.0", ui: { stories: snapshotPage(service) } }, line => {
    const frame = JSON.parse(line); answers.set(frame.id, frame);
  });
  let next = 0;
  const call = async (method: string, params: unknown) => {
    const id = String(++next);
    connection.line(JSON.stringify({ id, method, params })); await connection.drain();
    const answer = answers.get(id); assert.ok(answer); assert.ifError(answer.error); return answer.result;
  };
  try {
    await call("initialize", { protocol: 1, hub: { build: 0, extension_points: {} }, granted: {} });
    for (const ui_version of [1, 2]) {
      const context = { contribution: "stories", locale: "en", ui_version };
      const list: View = await call("ui.render", context);
      const story = list.sections.flatMap(section => section.items).find(it => it.key === "story-1");
      assert.ok(story?.type === "row" && story.action); assert.match(story.action.id, /^ext\.push\./);
      const detail = await call("ui.action", { ...context, action: story.action.id });
      assert.ok(text(detail.view).includes("Back to list"));
      const back = await call("ui.action", { ...context, action: "back", state: detail.view.state });
      assert.ok(text(back.view).includes("Synthetic Rust article"));
    }
  } finally { connection.close(); }
});
test("snapshot render deadlines retain localized error and navigation controls", async () => {
  const timeout = AbortSignal.timeout;
  try {
    AbortSignal.timeout = () => timeout(5);
    const { fixture, service } = setup(); fixture.delay = 50;
    for (const locale of ["en", "zh-Hans", "ja"]) {
      const view = await snapshotPage(service).render({ locale, ui_version: 2 } as UIRequest);
      assert.ok(text(view).includes(textsFor(locale).slow)); assert.ok(text(view).includes(textsFor(locale).retry));
      const detail = await snapshotPage(service).render({ locale, ui_version: 2, state: '{"s":1}' } as UIRequest);
      assert.ok(text(detail).includes(textsFor(locale).slow)); assert.ok(text(detail).includes(textsFor(locale).back));
    }
  } finally { AbortSignal.timeout = timeout; }
});
test("canceling stalled adapter streams releases all request slots", async () => {
  let started = 0, canceled = 0, stall = true;
  const service = new HackerNews(async () => stall ? new Response(new ReadableStream<Uint8Array>({
    start() { started++; }, pull() {}, cancel() { canceled++; }
  })) : Response.json({ id: 9 }));
  const controller = new AbortController();
  const pending = assert.rejects(Promise.all(Array.from({ length: limits.parallel }, (_, i) => service.item(i + 1, false, controller.signal))), { name: "AbortError" });
  await until(() => started === limits.parallel); await Promise.resolve(); controller.abort(); await pending;
  await until(() => canceled === limits.parallel);
  stall = false; assert.equal((await service.item(9)).id, 9); assert.equal(service.status().state, "ok");
});
test("refreshing a missing item invalidates its younger normal-cache copy", async () => {
  for (const missing of [null, new Response("missing", { status: 404 })]) {
    const { fixture, service } = setup(); await service.item(1); fixture.time += limits.refresh + 1;
    fixture.bodies.set("/item/1.json", missing);
    const notFound = (error: unknown) => error instanceof ReadError && error.code === "not_found";
    await assert.rejects(service.item(1, true), notFound); await assert.rejects(service.item(1), notFound);
    assert.equal(fixture.hits.get("/item/1.json"), 3); assert.equal(service.status().state, "ok");
  }
});
test("connector status follows completion order even within one clock millisecond", async () => {
  const { fixture, service } = setup(); await service.item(1);
  const lastSync = service.status().last_sync;
  fixture.bodies.set("/item/2.json", new Response("failure", { status: 500 }));
  await assert.rejects(service.item(2));
  assert.equal(service.status("en").state, "error"); assert.ok(service.status("en").error?.includes("HTTP 500"));
  await service.item(1); assert.equal(service.status().state, "error"); assert.equal(service.status().last_sync, lastSync);
  await service.item(3); assert.equal(service.status().state, "ok"); assert.equal(service.status().last_sync, lastSync);
});
test("refresh is scoped to its action, not later feed navigation", async () => {
  const { fixture, service } = setup(); await service.stories("top"); await service.stories("new");
  fixture.time += limits.refresh + 1;
  let snapshot!: Snapshot;
  const renderer = new Renderer({ authorize: () => true, onRender: value => { snapshot = value; } });
  try {
    renderer.mount(root({ service, locale: "en" })); await until(() => text(snapshot.view).includes("Top · 3"));
    await renderer.event(event(snapshot, "refresh")); await until(() => text(snapshot.view).includes("Top · 3"));
    assert.equal(fixture.hits.get("/topstories.json"), 2);
    await renderer.event(event(snapshot, "feed", { list: "new" })); await until(() => text(snapshot.view).includes("New · 3"));
    assert.equal(fixture.hits.get("/newstories.json"), 1);
  } finally { renderer.dispose(); }
});
test("service replacement publishes loading rather than the previous service's data", async () => {
  const first = setup(), second = setup();
  second.fixture.item({ id: 1, type: "story", title: "Replacement fixture article" }); second.fixture.delay = 10;
  let snapshot!: Snapshot;
  const renderer = new Renderer({ authorize: () => true, onRender: value => { snapshot = value; } });
  try {
    renderer.mount(root({ service: first.service, locale: "en" })); await until(() => text(snapshot.view).includes("Synthetic Rust article"));
    renderer.render(root({ service: second.service, locale: "en" }));
    assert.ok(text(snapshot.view).includes("Reading Hacker News")); assert.ok(!text(snapshot.view).includes("Synthetic Rust article"));
    await until(() => text(snapshot.view).includes("Replacement fixture article"));
  } finally { renderer.dispose(); }
});
test("manifest stays transitional, unchanged version and read-only egress", async () => {
  const manifest = JSON.parse(await readFile("persona-extension.json", "utf8"));
  assert.equal(manifest.version, "0.1.2"); assert.deepEqual(manifest.permissions.network, ["hacker-news.firebaseio.com"]);
  assert.ok(manifest.runtime.command); assert.equal(manifest.runtime.source, undefined);
});
