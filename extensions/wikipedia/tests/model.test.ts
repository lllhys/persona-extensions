import { test } from "node:test";
import { readState, transition } from "../src/page.tsx";
test("legacy JSON callback values are decoded before searching or navigation", () => {
  const initial = readState(undefined);
  assert.equal(transition(initial, "search", '{"query":"Earth"}').q, "Earth");
  assert.equal(transition(initial, "search", '{"query":"Earth"}').v, "search");
  assert.equal(transition(initial, "tab", '{"tab":"popular"}').t, "popular");
  assert.equal(transition(initial, "search", '{"query":2}').v, "today");
  for (const values of ["Earth", "not-json", "null", "[]", 1, { query: ["Earth"] }]) assert.equal(transition(initial, "search", values).v, "today");
  const search = transition(initial, "search", { query: "Earth" });
  assert.equal(readState(JSON.stringify(search)).v, "search");
});

import assert from "node:assert/strict";
import { articleURL, configFrom, dateAt, feedPath, languageFor, parseArticle, parseFeed, parseSearch, plain, ReadError, searchPath, titleKey } from "../src/model.ts";
import { syntheticArticle, feed } from "./fixtures.ts";
test("configuration and language selection are bounded", () => {
  assert.deepEqual(configFrom(undefined), {}); assert.deepEqual(configFrom({ language: "auto" }), { language: "auto" });
  assert.equal(languageFor({}, "ja-JP"), "ja"); assert.equal(languageFor({}, "zh-Hans"), "zh"); assert.equal(languageFor({}, "en-US"), "en");
  assert.equal(languageFor({ language: "en" }, "ja"), "en");
  for (const value of [{ language: "fr" }, { language: 1 }, [], "en"]) assert.throws(() => configFrom(value), ReadError);
});
test("feed parser retains summaries and generates trusted canonical links", () => {
  const result = parseFeed(feed, "en", "2026-11-09", 1000);
  assert.equal(result.featured?.url, "https://en.wikipedia.org/wiki/Featured_article");
  assert.equal(result.popular[0].views, 1234); assert.equal(result.events[0].pages[0].title, "Historical_article");
  assert.ok(!result.featured!.url.includes("attacker"));
  assert.equal(parseSearch({ pages: [{ key: "Found_article", excerpt: "A <span class='x'>match</span> &amp; more &#039;quoted&#039;" }] }, "en")[0].extract, "A match & more 'quoted'");
});
test("HTML snippets stay plain data with bounded entities", () => {
  assert.equal(plain(" <b>Hello</b>&nbsp; world &unknown; &#x1F600; "), "Hello world &unknown; 😀");
  assert.equal(plain("unterminated <tag"), "unterminated");
  assert.equal(plain("&#0; &#xD800;"), "� �");
});
test("titles cannot select another URL authority", () => {
  assert.equal(titleKey("Hello world"), "Hello_world");
  assert.equal(articleURL("en", "Hello world"), "https://en.wikipedia.org/wiki/Hello_world");
  for (const title of ["a#fragment", "a?query", "a<b", "x".repeat(256)]) assert.throws(() => titleKey(title), ReadError);
  assert.equal(titleKey("A normal title"), "A_normal_title");
  assert.equal(articleURL("en", "https://evil.invalid"), "https://en.wikipedia.org/wiki/https%3A//evil.invalid");
});
test("public request paths reject malformed dates, queries and service responses", () => {
  assert.equal(feedPath("2026-11-09"), "/api/rest_v1/feed/featured/2026/11/09");
  for (const date of ["2026-02-30", "2020-13-01", "2015-01-01", "2026-1-01", "2026-01-01T00:00:00Z"]) assert.throws(() => feedPath(date), ReadError);
  assert.equal(dateAt(Date.UTC(2026, 10, 9)), "2026-11-09");
  assert.equal(searchPath("a title", 30), "/w/rest.php/v1/search/page?q=a+title&limit=30");
  for (const query of ["", " ", "x".repeat(201), "bad\n"]) assert.throws(() => searchPath(query), ReadError);
  for (const value of [null, [], {}, { mostread: {} }, { mostread: { articles: [] }, onthisday: null }, { mostread: { articles: [null] }, onthisday: [] }]) assert.throws(() => parseFeed(value, "en", "2026-11-09", 0), ReadError);
  assert.throws(() => parseArticle(syntheticArticle("Bad", { titles: { canonical: 1 } }), "en"), ReadError);
  assert.throws(() => parseFeed({ ...feed, mostread: { articles: [syntheticArticle(), syntheticArticle()] } }, "en", "2026-11-09", 0), ReadError);
});
