import test from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { Renderer } from "@persona/extension/ui";
import { createRoot as createPage } from "./ui.tsx";
import { ShowcaseStore } from "./services.ts";
import { defaultMedia } from "./media.ts";

for (const language of ["en", "zh-Hans", "ja"]) test(`row media keeps exact HTTPS native values in ${language}`, () => {
  const store = new ShowcaseStore(), renderer = new Renderer({ authorize: () => false });
  try {
    const view = renderer.mount(createPage(store, { state: { p: "row" }, language })).view;
    const rows = view.sections.flatMap(section => section.items).filter(item => item.type === "row");
    const row = (key: string) => rows.find(item => item.key === key)!;
    assert.equal(row("row-avatar-url").avatar!.url, defaultMedia.avatar);
    assert.equal(row("row-thumb-square").thumbnail!.url, defaultMedia.square);
    assert.equal(row("row-thumb-square").thumbnail!.aspect, "square");
    assert.equal(row("row-thumb-wide").thumbnail!.url, defaultMedia.wide);
    assert.equal(row("row-thumb-wide").thumbnail!.aspect, "wide");
    assert.equal(row("row-thumb-broken").thumbnail!.url, defaultMedia.broken);
    for (const value of Object.values(defaultMedia)) {
      const url = new URL(value);
      assert.equal(url.protocol, "https:");
      assert.equal(url.username + url.password, "");
    }
    assert.ok(!JSON.stringify(view).includes("data:image/"));
  } finally { renderer.dispose(); store.dispose(); }
});

test("page factories preserve explicitly supplied HTTPS media without an invented hub API", () => {
  const media = { avatar: "https://example.com/media/a.png", square: "https://example.com/media/s.png", wide: "https://example.com/media/w.png", broken: "https://example.com/media/missing.png" };
  const store = new ShowcaseStore(), renderer = new Renderer({ authorize: () => false });
  try {
    const view = renderer.mount(createPage(store, { state: { p: "row" }, media })).view;
    const rows = view.sections.flatMap(section => section.items).filter(item => item.type === "row");
    assert.deepEqual(rows.filter(row => row.avatar?.url || row.thumbnail?.url).map(row => row.avatar?.url ?? row.thumbnail?.url), Object.values(media));
  } finally { renderer.dispose(); store.dispose(); }
});

test("repository-owned media files contain real synthetic PNGs and matching aspect ratios", () => {
  for (const [name, width, height] of [["avatar", 96, 96], ["square", 160, 160], ["wide", 320, 180]] as const) {
    const bytes = readFileSync(new URL(`../assets/${name}.png`, import.meta.url));
    assert.deepEqual(bytes.subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
    assert.equal(bytes.readUInt32BE(16), width);
    assert.equal(bytes.readUInt32BE(20), height);
  }
  assert.equal(existsSync(new URL("../assets/intentionally-missing.png", import.meta.url)), false);
});
