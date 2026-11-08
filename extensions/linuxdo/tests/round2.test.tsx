import { test } from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "@persona/extension/ui";
import { connect } from "@persona/extension";
import { Fixture, rss, topic } from "./fixtures.ts";
import { LinuxDo, limits } from "../src/service.ts";
import { topicID, parseJSON, settings } from "../src/model.ts";
import { readState, loadPage, page } from "../src/page.tsx";
import { plainText } from "../src/text.ts";
import { parseTopic } from "../src/topic.ts";
import { parseFeed } from "../src/feed.ts";
import { topicTime } from "../src/time.ts";
import { date } from "../src/i18n.ts";
import { callTool } from "../src/tools.ts";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const setup = () => { const fixture = new Fixture(); return { fixture, service: new LinuxDo(fixture.fetch, {}, "UTC", "en", { gap: 0, now: () => fixture.time }) }; };
test("round2 URL and cooked HTML counterexamples have bounded synchronous work", { timeout: 10_000 }, () => {
  for (const kb of [64, 256, 512]) {
    const start = performance.now();
    assert.throws(() => topicID("/t/" + "x".repeat(kb * 1024)));
    assert.equal(topicID(("/t/no-id".repeat(kb * 128)) + "/t/topic/42/7"), "42");
    for (const tag of ["<script>", "<style>", "<svg>", '<aside class="onebox">', "<pre>", '<img class="', "<"]) {
      const s = tag.repeat(Math.ceil(kb * 1024 / tag.length));
      plainText(s);
    }
    const elapsed = performance.now() - start;
    console.log(`round2 bounded parsing: ${kb}KiB per shape, ${elapsed.toFixed(1)}ms`);
    assert.ok(elapsed < 1800, `unbounded parsing at ${kb}KiB: ${elapsed}ms`);
  }
  for (const [input, id] of [["/t/123/4", "123"], ["/t/123slug/4", "4"], ["/t/123slug/no-id", "123"], ["/t/no-id/t/topic/42", "42"]]) assert.equal(topicID(input), id);
  assert.equal(plainText("<script>unclosed text"), "unclosed text");
  assert.equal(plainText("<pre><pre>  code</pre>tail"), "  code\ntail");
  assert.equal(plainText("<script>hidden</style><p>kept</p>"), "kept");
  assert.equal(plainText("<script>unclosed</script extra><p>kept</p>"), "unclosedkept");
  const noCounts = rss([{ id: 1, excerpt: "9".repeat(256 * 1024) }]);
  const countStart = performance.now(); assert.equal(parseFeed(noCounts)[0].posts, 75);
  assert.ok(performance.now() - countStart < 750, "RSS count suffixes rescan digit runs");
});

test("round2 raw saved state rejects decimal/exponent lexemes and invalid JSON without shrinking int64", () => {
  const { service } = setup();
  for (const token of ["1.0000000000000001", "1e0", "1.0", "1.5", "9223372036854775808", "-1", "null", "true", "{}", "[]"]) assert.equal(readState(`{"l":"weekly","t":${token}}`, service).t, "0", token);
  for (const token of ["1", "9007199254740993", "9223372036854775807"]) assert.equal(readState(`{"t":${token}}`, service).t, token);
  for (const raw of ['{"t":01}', '{"t":+1}', '{"t":1.}', '{"t":1e}', '{"t":NaN}', '{"t":Infinity}', '{"t":1,}', '{"t":1} trailing']) {
    assert.throws(() => parseJSON(raw), raw); assert.equal(readState(raw, service).t, "0");
    assert.throws(() => parseTopic(raw), raw);
  }
});

test("round2 invalid topic/post calendars preserve good body, timestamp, stale status, page and tool", async () => {
  for (const field of ["topic", "post"]) {
    const { fixture, service } = setup(), good = await service.thread(1), last = service.status().last_sync;
    fixture.time += limits.topic;
    for (const invalid of ["not-a-date", "2026-02-30T09:37:00Z", "2026-11-03", "2026-11-03T24:00:00Z", "2026-11-03T09:37:60Z", "2026-11-03T09:37:00+00:60", "1900-02-29T00:00:00Z", "999999999999999999999-01-01T00:00:00Z", "", 1e300]) {
      const doc = topic(1, "<p>Invalid replacement</p>");
      (field === "topic" ? doc : doc.post_stream.posts[0]).created_at = invalid;
      fixture.bodies.set("/t/topic/1.json", doc);
      const read = await service.thread(1);
      assert.equal(read.value, good.value); assert.equal(read.at, good.at); assert.equal(read.stale?.code, "bad");
      assert.equal(service.status().state, "error"); assert.equal(service.status().last_sync, last);
      const renderer = new Renderer({ authorize: () => false });
      try {
        const state = readState('{"t":1}', service), loaded = await loadPage(service, state);
        const body = JSON.stringify(renderer.mount(page({ service, state, loaded, locale: "en" })).view);
        assert.ok(body.includes("Synthetic opening")); assert.ok(body.includes("stale")); assert.ok(!body.includes("Invalid replacement"));
      } finally { renderer.dispose(); }
      const tool = await callTool(service, "linuxdo_topic", { topic: 1 }); assert.ok(tool.includes("Synthetic opening")); assert.ok(!tool.includes("Invalid replacement"));
    }
  }
});

test("round2 original four-digit year formatter range stays supported, not zeroed or recursively crashed", () => {
  for (const [raw, expected] of [["0000-02-29T09:37:00Z", "02-29 09:37"], ["0099-11-03T09:37:00Z", "11-03 09:37"], ["9999-12-31T23:59:59.999999999Z", "12-31 23:59"], ["2000-02-29T09:37:00.123456789Z", "02-29 09:37"]]) {
    const doc = topic(); doc.created_at = raw; const parsed = parseTopic(JSON.stringify(doc));
    assert.equal(date(parsed.at, "UTC"), expected); assert.notEqual(parsed.at, 0);
  }
  assert.equal(date(topicTime("2026-11-03T09:37:00+09:00"), "Asia/Tokyo"), "11-03 09:37");
  assert.throws(() => topicTime("10000-01-01T00:00:00Z"));
  for (const at of [1e300, Infinity, NaN]) assert.throws(() => date(at, "UTC"), /formatter range/u);
});

test("round2 Local and old-hub missing timezone use local fallback through bundled initialization", async () => {
  const oldTZ = process.env.TZ; process.env.TZ = "Asia/Tokyo";
  try {
    assert.equal(settings({ tz: "Local" }).tz, "Local");
    const fixture = new Fixture();
    for (const config of [{}, { tz: "Local" }]) {
      const service = new LinuxDo(fixture.fetch, config, "Local", "en", { gap: 0 });
      assert.equal(service.zone(), "Asia/Tokyo"); assert.equal(date(topicTime("2026-11-03T09:37:00Z"), service.zone()), "11-03 18:37");
      assert.equal(service.zone("Asia/Shanghai"), "tz" in config ? "Asia/Tokyo" : "Asia/Shanghai");
      const mod = await import(pathToFileURL(resolve("build/linuxdo.mjs")).href), frames: any[] = [];
      const p = connect(mod.createProgram(fixture.fetch, { gap: 0 }), line => frames.push(JSON.parse(line)));
      try {
        p.line(JSON.stringify({ id: "init", method: "initialize", params: { protocol: 1, config, hub: { locale: "en" } } })); await p.drain(); assert.ok(frames[0].result);
        p.line(JSON.stringify({ id: "topic", method: "connector.tools.call", params: { name: "linuxdo_topic", args: { topic: 1 } } })); await p.drain();
        assert.ok(frames.find(f => f.id === "topic").result.text.includes("11-03 18:37"));
      } finally { p.close(); }
    }
  } finally { if (oldTZ === undefined) delete process.env.TZ; else process.env.TZ = oldTZ; }
});

test("round2 huge Retry-After defaults and in-int64 duration wrapping match original Go", async () => {
  for (const [retry, pause] of [["1" + "0".repeat(308), limits.pause], ["9223372036854775808", limits.pause], ["9223372036854775807", 60_000], ["9223372036", 9_223_372_036_000], ["9223372037", 60_000], ["+123", 123_000], ["-1", limits.pause]] as const) {
    const { fixture, service } = setup(); fixture.bodies.set("/hot.rss", new Response("rate", { status: 429, headers: { "Retry-After": retry } }));
    await assert.rejects(service.listing("hot")); assert.equal(service.site.stats.paused, fixture.time + pause); assert.ok(Number.isFinite(service.site.stats.paused));
    fixture.time += pause; fixture.bodies.set("/latest.rss", rss([{ id: 1 }])); assert.equal((await service.listing("latest")).live, true);
  }
});

test("round2 RSS posts/people retain int64 text including the original Atoi overflow saturation", async () => {
  for (const english of [true, false]) {
    for (const [token, expected] of [["9007199254740993", "9007199254740993"], ["9223372036854775807", "9223372036854775807"], ["9223372036854775808", "9223372036854775807"], ["9".repeat(4096), "9223372036854775807"]]) {
      const raw = rss([{ id: 1, english }]).replace("75 ", `${token} `).replace("59 ", `${token} `);
      const parsed = parseFeed(raw)[0]; assert.equal(parsed.posts, expected); assert.equal(parsed.people, expected);
      const { fixture, service } = setup(); fixture.bodies.set("/hot.rss", raw); assert.ok((await callTool(service, "linuxdo_hot")).includes(expected));
    }
  }
  const bad = rss([{ id: 1 }]).replace("03 Nov 2026", "30 Feb 2026"); assert.equal(parseFeed(bad)[0].at, 0);
});

test("round2 SDK-visible tool type/int64 range validation is not a safe-integer subset", async () => {
  const { fixture, service } = setup();
  assert.ok((await callTool(service, "linuxdo_topic", { topic: 1, replies: 9007199254740992 })).includes("High score"));
  assert.ok((await callTool(service, "linuxdo_hot", { limit: -9223372036854775808 })).includes("Synthetic Rust"));
  for (const value of ["1", true, {}, 1.5, 9223372036854775808, -9223372036854778000, Infinity, NaN]) {
    const before = fixture.calls.length;
    await assert.rejects(callTool(service, "linuxdo_topic", { topic: 1, replies: value })); assert.equal(fixture.calls.length, before);
  }
});
