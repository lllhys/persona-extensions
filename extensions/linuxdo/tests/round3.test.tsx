import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { Renderer } from "@persona/extension/ui";
import { Fixture, topic } from "./fixtures.ts";
import { LinuxDo, limits } from "../src/service.ts";
import { bestReplies, parseTopic } from "../src/topic.ts";
import { topicTime } from "../src/time.ts";
import { date, textsFor, topicMeta } from "../src/i18n.ts";
import { loadPage, page, readState } from "../src/page.tsx";
import { callTool } from "../src/tools.ts";

const setup = () => { const fixture = new Fixture(); return { fixture, service: new LinuxDo(fixture.fetch, {}, "UTC", "en", { gap: 0, now: () => fixture.time }) }; };

test("round3 reply dedup is linear after ranking for 8k/16k/32k legal unique replies", { timeout: 10_000 }, () => {
  for (const count of [8000, 16000, 32000]) {
    const raw = JSON.stringify({ id: 1, post_stream: { posts: Array.from({ length: count }, (_, i) => ({ id: i + 1, post_number: i + 2, post_type: 1, cooked: "x" })) } });
    assert.ok(Buffer.byteLength(raw) < limits.response);
    const parsed = parseTopic(raw); let reads = 0;
    for (const post of parsed.posts) {
      const id = post.id;
      Object.defineProperty(post, "id", { get: () => { reads++; return id; } });
    }
    const start = performance.now(), replies = bestReplies(parsed, 10), elapsed = performance.now() - start;
    // Count work, not a fragile wall-clock threshold: the former findIndex
    // performs approximately count^2/2 ID reads even when only 10 are requested.
    assert.ok(reads <= count * 3, `dedup rescanned IDs: ${reads} for ${count}`);
    assert.deepEqual(replies.map(p => p.number), Array.from({ length: 10 }, (_, i) => i + 2));
    console.log(`round3 ranking: count=${count} bytes=${Buffer.byteLength(raw)} idReads=${reads} elapsed=${elapsed.toFixed(1)}ms`);
  }
});

test("round3 dedup retains the first ranked exact ID/floor pair and stable ties", () => {
  const parsed = parseTopic(JSON.stringify(topic(1, "Opening", ["Low", "High"]))), a = parsed.posts[1], b = parsed.posts[2];
  const higher = { ...a, score: 30, cooked: "Highest duplicate" };
  parsed.posts = [a, b, higher, { ...b }, { ...a, number: 4 }, { ...a, number: "2" }];
  const actual = bestReplies(parsed, 20);
  assert.deepEqual(actual.map(p => [p.cooked, p.number]), [["Highest duplicate", 2], ["High", 3], ["Low", "2"], ["Low", 4]]);
  assert.deepEqual(bestReplies(parsed, 2), actual.slice(0, 2));
  assert.deepEqual(bestReplies(parsed, 0), []);
});

test("round3 epoch zero is a valid topic/post timestamp, not missing metadata", async () => {
  const { fixture, service } = setup(), doc = topic();
  doc.created_at = "1970-01-01T00:00:00Z";
  for (const post of doc.post_stream.posts) post.created_at = doc.created_at;
  fixture.bodies.set("/t/topic/1.json", doc);
  const parsed = parseTopic(JSON.stringify(doc));
  assert.equal(parsed.at, 0); assert.equal(parsed.posts[1].at, 0);
  assert.equal(topicTime(null), null); assert.equal(topicTime(undefined), null);
  assert.equal(date(null, "UTC"), ""); assert.equal(date(0, "UTC"), "01-01 00:00");
  assert.equal(date(0, "Asia/Tokyo"), "01-01 09:00");
  assert.ok(topicMeta(textsFor("en"), parsed, "UTC").includes("01-01 00:00"));
  const renderer = new Renderer({ authorize: () => false });
  try {
    const state = readState('{"t":1}', service), loaded = await loadPage(service, state);
    const view = renderer.mount(page({ service, state, loaded, locale: "en" })).view;
    const captions = view.sections.flatMap(s => s.items).filter(i => i.type === "text" && i.style === "caption");
    assert.equal(captions.length, 3);
    for (const caption of captions) assert.ok(caption.type === "text" && caption.text.includes("01-01 00:00"));
  } finally { renderer.dispose(); }
  const tool = await callTool(service, "linuxdo_topic", { topic: 1 });
  assert.equal(tool.split("01-01 00:00").length - 1, 3);
  // The final Program factory and default bundle use the same timestamp path.
  const bundle = await import(pathToFileURL(resolve("build/linuxdo.mjs")).href);
  const bundled = new bundle.LinuxDo(fixture.fetch, {}, "UTC", "en", { gap: 0 });
  const rendered = await bundle.snapshotPage(bundled).render({ state: '{"t":1}', locale: "en", tz: "UTC" });
  assert.equal(JSON.stringify(rendered).split("01-01 00:00").length - 1, 3);
});

test("round3 page omits nonpositive likes while the original tool keeps signed values", async () => {
  for (const likes of [-1, "-9007199254740993", 0, 1, "9007199254740993"]) {
    const { fixture, service } = setup(), doc = topic(1, "Opening", ["Reply"]);
    doc.post_stream.posts[1].reaction_users_count = likes;
    // Exact large values are raw numeric tokens, not JSON strings.
    fixture.bodies.set("/t/topic/1.json", JSON.stringify(doc).replace(/"reaction_users_count":"(-?\d+)"/u, '"reaction_users_count":$1'));
    const renderer = new Renderer({ authorize: () => false });
    try {
      const state = readState('{"t":1}', service), loaded = await loadPage(service, state);
      const view = renderer.mount(page({ service, state, loaded, locale: "en" })).view;
      const caption = view.sections.flatMap(s => s.items).find(i => i.key === "reply-meta-101-2");
      assert.ok(caption?.type === "text");
      assert.equal(caption.text.includes(" likes"), BigInt(likes) > 0n);
      if (BigInt(likes) > 0n) assert.ok(caption.text.includes(`${likes} likes`));
    } finally { renderer.dispose(); }
    const tool = await callTool(service, "linuxdo_topic", { topic: 1 });
    assert.ok(tool.includes(`${likes} likes`));
  }
});

test("round3 local source profile rejects recovery AST syntax errors with Node --check negative controls", async () => {
  const { verifySourceProfile } = await import(pathToFileURL(resolve("scripts/source-profile.mjs")).href);
  await mkdir("build/tests", { recursive: true });
  const dir = await mkdtemp(resolve("build/tests/syntax-"));
  try {
    const manifest = JSON.parse(await readFile("build/package/persona-extension.json", "utf8"));
    manifest.runtime.source.entry = "entry.ts"; manifest.runtime.source.bundle = "invalid.mjs";
    const path = resolve(dir, "invalid.mjs");
    await writeFile(resolve(dir, "entry.ts"), "export default {};\n");
    for (const code of ["export default {};\nconst x = ;\n", "export default {};\nfunction broken( {\n", 'export default {};\nconst x = "unterminated;\n', "export default {};\nreturn;\n", "export default {};\nbreak;\n", "export default {};\nconst x = 1; const x = 2;\n", "export default {};\nwith ({}) {}\n", 'export default {};\nthrow new Error("not executed"); while (true) {}\n', "export default {};\n"]) {
      const valid = code === "export default {};\n" || code.includes("not executed"), bytes = Buffer.from(code);
      manifest.runtime.source.sha256 = createHash("sha256").update(bytes).digest("hex");
      await writeFile(path, bytes); await writeFile(resolve(dir, "persona-extension.json"), JSON.stringify(manifest));
      // Use actual Node even when the source suite is driven by Bun.
      const check = spawnSync("node", ["--check", path], { encoding: "utf8", timeout: 10_000 });
      if (check.error) throw check.error;
      if (valid) {
        assert.equal(check.status, 0);
        const result = await verifySourceProfile(dir);
        assert.equal(result.checker.code, "STATIC_OK"); assert.equal(result.checker.program, "not-probed");
      }
      else {
        assert.equal(check.status, 1); assert.match(check.stderr, /SyntaxError/u);
        await assert.rejects(verifySourceProfile(dir), { name: "SyntaxError", message: "source bundle has parse diagnostics" });
      }
    }
  } finally { await rm(dir, { recursive: true, force: true }); }
});
