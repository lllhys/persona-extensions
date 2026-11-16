import { test } from "node:test";
import assert from "node:assert/strict";
import { canonicalEpisodeURL, lookupPath, parseEpisodes, parseSearch, parseShow, podcastID, publicURL, ReadError, searchPath } from "../src/model.ts";
import { decodeList, encodeList, MemoryStore } from "../src/store.ts";
import { sampleEpisode, sampleShow } from "./fixtures.ts";
import { encodeState, page, readState, transition } from "../src/page.tsx";
import { Podcasts } from "../src/service.ts";
import { Renderer } from "@persona/extension/ui";

test("search and lookup paths validate identifiers and bounds", () => {
  assert.equal(podcastID(1860072717), 1860072717);
  assert.equal(podcastID("1860072717"), 1860072717);
  for (const id of [0, -1, 1.2, "https://attacker.invalid", "1&limit=200", 2_000_000_000_001, undefined]) assert.throws(() => podcastID(id), ReadError);
  for (const query of ["", " ", "x".repeat(201), "bad\n"]) assert.throws(() => searchPath(query), ReadError);
  assert.equal(searchPath("A show", 2), "/search?term=A+show&media=podcast&entity=podcast&limit=2");
  assert.throws(() => searchPath("valid", 31), ReadError);
  assert.equal(lookupPath(42, 3), "/lookup?id=42&entity=podcastEpisode&limit=3&sort=recent");
});

test("only canonical Apple links and HTTPS publisher feeds survive parsing", () => {
  const show = parseShow(sampleShow);
  assert.equal(show.url, "https://podcasts.apple.com/us/podcast/id1860072717");
  assert.equal(show.feedURL, "https://feeds.example.com/show.xml");
  assert.equal(canonicalEpisodeURL(show.id, sampleEpisode.trackId), "https://podcasts.apple.com/us/podcast/id1860072717?i=1000792066218");
  assert.throws(() => parseShow({ ...sampleShow, collectionViewUrl: "https://attacker.invalid/phishing" }), ReadError);
  assert.throws(() => parseShow({ ...sampleShow, feedUrl: "http://feeds.example.com/show.xml" }), ReadError);
  assert.throws(() => parseShow({ ...sampleShow, feedUrl: "https://user:password@feeds.example.com/show.xml" }), ReadError);
  assert.equal(publicURL(undefined, "feed"), undefined);
});

test("parent and episode records are strictly separated and bounded", () => {
  const search = parseSearch({ resultCount: 2, results: [sampleShow, sampleShow] }, 1000);
  assert.equal(search.shows.length, 1);
  const detail = parseEpisodes({ resultCount: 2, results: [sampleShow, sampleEpisode] }, sampleShow.collectionId, 1000);
  assert.equal(detail.episodes.length, 1);
  assert.equal(detail.episodes[0].minutes, 59);
  assert.equal(detail.episodes[0].description, "A synthetic description of an episode.");
  assert.ok(!("episodeUrl" in detail.episodes[0]));
  assert.throws(() => parseEpisodes({ resultCount: 2, results: [sampleShow, { ...sampleEpisode, kind: "podcastEpisode" }] }, sampleShow.collectionId, 0), ReadError);
  assert.throws(() => parseEpisodes({ resultCount: 1, results: [sampleEpisode] }, 42, 0), ReadError);
  assert.throws(() => parseEpisodes({ resultCount: 2, results: [sampleShow, { ...sampleEpisode, collectionId: 42 }] }, sampleShow.collectionId, 0), ReadError);
  // Date.parse would roll this into October; an episode date must be a real calendar day.
  assert.throws(() => parseEpisodes({ resultCount: 2, results: [sampleShow, { ...sampleEpisode, releaseDate: "2026-02-30T00:00:00Z" }] }, sampleShow.collectionId, 0), ReadError);
  for (const raw of [null, [], {}, { resultCount: -1, results: [] }, { resultCount: 1 }, { resultCount: 1, results: [null] }]) assert.throws(() => parseSearch(raw, 0), ReadError);
});

test("native episode descriptions respect the SDK's 2000-character text bound", () => {
  const show = parseShow(sampleShow), result = parseEpisodes({ resultCount: 2, results: [sampleShow, { ...sampleEpisode, description: "long ".repeat(1600) }] }, show.id, 1000);
  const state = { v: "show" as const, q: "engineering", k: show.id, b: show };
  const renderer = new Renderer({ authorize: () => false });
  try {
    const snapshot = renderer.mount(page({ service: new Podcasts(async () => new Response()), store: new MemoryStore(), state, loaded: { kind: "show", result } }));
    for (const section of snapshot.view.sections) for (const item of section.items) {
      if (item.type === "text") assert.ok(Array.from(item.text).length <= 2000);
      if (item.type === "row") assert.ok(Array.from(item.subtitle ?? "").length <= 2000);
    }
  } finally { renderer.dispose(); }
});
test("listening-list persistence validates untrusted data and does not manufacture writes", async () => {
  const show = parseShow(sampleShow), store = new MemoryStore();
  await store.save(show);
  assert.equal(store.has(show.id), true);
  const list = decodeList(encodeList(await store.load()));
  assert.deepEqual(list.shows, [show]);
  await store.save(otherShow());
  assert.equal((await store.load()).shows.length, 2);
  await store.remove(show.id);
  assert.equal(store.has(show.id), false);
  assert.throws(() => decodeList("not-base64"), ReadError);
  assert.throws(() => decodeList(encodeList({ schema_version: 1, shows: [] }) + "x"), ReadError);
});
function otherShow() { return parseShow({ ...sampleShow, collectionId: 42, collectionName: "Another Show", feedUrl: undefined }); }

test("navigation retains only validated show metadata", () => {
  const show = parseShow(sampleShow);
  const searched = transition(readState(undefined), "search", '{"query":"engineering"}');
  const opened = transition({ ...searched, b: show }, `show:${show.id}`);
  assert.equal(opened.v, "show");
  const roundTrip = readState(encodeState(opened));
  assert.equal(roundTrip.b?.url, show.url);
  assert.equal(transition(roundTrip, "back").v, "search");
  const fromLibrary = transition(readState(undefined), "library");
  const libraryShow = { ...fromLibrary, b: show, v: "show" as const, k: show.id, from: "library" as const };
  assert.equal(transition(libraryShow, "back").v, "library");
  assert.equal(transition(readState(encodeState(libraryShow)), "back").v, "library");
  assert.equal(transition(roundTrip, "library").v, "library");
  for (const values of ["show", "not-json", "null", "[]", 1, { query: ["bad"] }]) assert.equal(transition(readState(undefined), "search", values).v, "discover");
  assert.equal(readState(JSON.stringify({ ...opened, b: { ...show, url: "https://attacker.invalid" } })).v, "discover");
  assert.equal(readState(JSON.stringify({ ...opened, b: { ...show, feedURL: "http://attacker.invalid" } })).v, "discover");
});
