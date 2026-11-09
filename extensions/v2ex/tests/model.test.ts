import { test } from "node:test";
import assert from "node:assert/strict";
import { cleanNodes, clip, chunks, configFrom, filter, latestReplies, length, nodePath, parseArray, parseReplies, parseTopics, topicID } from "../src/model.ts";
import { textsFor, time, topicMeta } from "../src/i18n.ts";
import { rawReplies, rawTopic } from "./fixtures.ts";
import { credentialFixtures } from "./credential-fixtures.ts";
import en from "../src/locales/en.json";
import zh from "../src/locales/zh-Hans.json";
import ja from "../src/locales/ja.json";
const bad = (fn: () => unknown) => assert.throws(fn, { code: "bad_answer" });
test("topics project only public fields and preserve every deletion variant", () => {
  for (const deleted of [undefined, 0, null, false, "0", ""]) {
    const topics = parseTopics(JSON.stringify([rawTopic(1, { deleted })]));
    assert.equal(topics.length, 1); assert.equal(topics[0].author, "fixture-author");
    assert.ok(!JSON.stringify(topics).includes("DO-NOT-PROJECT-PROFILE"));
  }
  for (const deleted of [1, true, "deleted", {}, []]) assert.equal(parseTopics(JSON.stringify([rawTopic(1, { deleted })])).length, 0);
  bad(() => parseTopics('[{"id":1,"deleted":1,"title":3}]'));
  for (const body of ["{}", "null", "<html>", "[", '[{"id":"1"}]', '[{"node":[]}]', '[{"replies":1.5}]', '[{"member":{"username":4}}]']) bad(() => parseTopics(body));
  assert.equal(parseTopics("[null]")[0].id, "0");
  assert.equal(parseReplies(JSON.stringify(rawReplies(1)))[0].author, "fixture-user-1");
});
test("int64 tokens, escaping and synthetic markers never round or spoof field types", () => {
  assert.equal(parseTopics('[{"id":9223372036854775807,"title":"9007199254740993"}]')[0].id, "9223372036854775807");
  assert.equal(parseTopics('[{"id":9007199254740993}]')[0].id, "9007199254740993");
  for (const body of ['[{"id":9223372036854775808}]', '[{"id":1e20}]', '[{"id":01}]', '[{"id":{"__source_integer__":0},"unused":9007199254740993}]']) bad(() => parseTopics(body));
  assert.deepEqual(parseArray('["\\u0040v2ex-int:9007199254740993",9007199254740993]'), ["@v2ex-int:9007199254740993", 9007199254740993n]);
  assert.equal(parseTopics('[{"id":1,"title":"\\\" escaped \\u0040v2ex-int:22"}]')[0].title, '" escaped @v2ex-int:22');
});
test("topic IDs accept only the site's links and positive exact signed-int64 IDs", () => {
  for (const value of [123, "00123", " /t/123 ", "v2ex.com/t/123", "https://www.v2ex.com/t/123?p=2#reply", "http://V2EX.com/t/123/2"]) assert.equal(topicID(value), "123");
  assert.equal(topicID("9223372036854775807"), "9223372036854775807");
  for (const value of [0, -1, 1.5, NaN, 9007199254740992, "9223372036854775808", "1e3", true, null, "https://example.com/t/123", "https://v2ex.com.example.com/t/123", credentialFixtures.topic, "//v2ex.com/t/123", "1/2"]) assert.throws(() => topicID(value), { code: "invalid" });
});
test("all configuration keys, exact node filters and independently narrowing keywords survive", () => {
  assert.deepEqual(configFrom({ list: "latest", nodes: ["go"], exclude_nodes: ["Jobs"], keywords: ["Go"], exclude_keywords: ["blocked"], tz: "UTC" }),
    { list: "latest", tz: "UTC", nodes: ["go"], exclude_nodes: ["Jobs"], keywords: ["Go"], exclude_keywords: ["blocked"] });
  for (const cfg of [[], 1, { nodes: [1] }, { keywords: "Go" }, { tz: "Mars/Olympus" }]) assert.throws(() => configFrom(cfg), { code: "invalid" });
  const topics = parseTopics(JSON.stringify([rawTopic(1, { title: "Go database" }), rawTopic(2, { title: "Go UI" }), rawTopic(3, { title: "Python database" }), rawTopic(4, { title: "Go database blocked" })]));
  assert.deepEqual(filter(filter(topics, { keywords: ["go"], exclude_keywords: ["blocked"] }), { keywords: ["database"] }).map(t => t.id), ["1"]);
  assert.equal(filter(topics, { exclude_nodes: [" g "] }).length, 4);
  assert.equal(filter(topics, { exclude_nodes: [" GO "] }).length, 0);
  assert.equal(filter(topics, { keywords: ["  "] }).length, 4);
});
test("nodes are trimmed, deduplicated and bounded before any request", () => {
  const result = cleanNodes([" go ", "go", "python", "n1", "n2", "n3", "n4", "", "../bad", "a".repeat(65)]);
  assert.deepEqual(result.good, ["go", "python", "n1", "n2", "n3"]);
  assert.deepEqual(result.bad, ["../bad", "a".repeat(65)]);
  assert.equal(nodePath("Go_-"), "/topics/show.json?node_name=Go_-");
  assert.throws(() => nodePath("../bad"), { code: "no_node" });
});
test("Unicode output budgets, line breaks and latest reply floors match Go behavior", () => {
  assert.equal(length(clip("😀".repeat(10), 3)), 4);
  assert.deepEqual(chunks("aaa\nbbb\nccc", 5), ["aaa", "bbb", "ccc"]);
  assert.deepEqual(chunks("😀😀😀😀😀😀😀", 3), ["😀😀😀", "😀😀😀", "😀"]);
  assert.deepEqual(chunks("a\nbcdefgh", 4), ["a\nbc", "defg", "h"]);
  assert.deepEqual(chunks("  \n "), []);
  assert.deepEqual(latestReplies(parseReplies(JSON.stringify(rawReplies(25))), 3).map(r => r.floor), [23, 24, 25]);
});
test("three locales have identical nonempty keys and placeholder sets, timezone formatting is explicit", () => {
  for (const locale of [zh, ja]) {
    assert.deepEqual(Object.keys(locale).sort(), Object.keys(en).sort());
    for (const key of Object.keys(en) as (keyof typeof en)[]) {
      assert.ok(locale[key].trim()); assert.deepEqual(locale[key].match(/\{\d+\}/g)?.sort(), en[key].match(/\{\d+\}/g)?.sort());
    }
  }
  assert.equal(textsFor("en-US"), en); assert.equal(textsFor("ja"), ja); assert.equal(textsFor("zh-Hans"), zh);
  assert.equal(time(1700000000000, "Asia/Shanghai"), "11-15 06:13");
  assert.equal(time(1700000000000, "UTC"), "11-14 22:13");
  assert.ok(topicMeta(en, parseTopics(JSON.stringify([rawTopic()]))[0], "UTC").includes("11-14 22:13"));
});
