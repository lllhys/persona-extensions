import { test } from "node:test";
import assert from "node:assert/strict";
import { identifier, limit, parseChats, parseMessages, parseSearch, parseStatus, query, ReadError, errorFrom } from "../src/model.ts";
import { readState, transition } from "../src/page.tsx";
const invalid = (fn: () => unknown) => assert.throws(fn, (error: unknown) => error instanceof ReadError && error.code === "invalid");
test("queries, IDs, limits and page state stay bounded", () => {
  assert.equal(query("  design review  "), "design review");
  assert.equal(identifier("oc_example:abc-1", "chat"), "oc_example:abc-1");
  assert.equal(limit(undefined), 20); assert.equal(limit(50), 50);
  invalid(() => query("")); invalid(() => query("x".repeat(501))); invalid(() => query("a\u0000b"));
  invalid(() => identifier("../credential", "chat")); invalid(() => identifier("oc_a/shell", "chat"));
  invalid(() => limit(0)); invalid(() => limit(51)); invalid(() => limit(1.5)); invalid(() => limit(1n));
  assert.deepEqual(readState("not-json"), { mode: "home" });
  assert.deepEqual(readState(JSON.stringify({ mode: "chat", chat: "../other" })), { mode: "home" });
  assert.deepEqual(transition({ mode: "home" }, "search", { kind: "docs", query: "Roadmap" }), { mode: "search", kind: "docs", q: "Roadmap" });
  assert.deepEqual(transition({ mode: "home" }, "search", { kind: "invalid", query: "Roadmap" }), { mode: "home" });
});
test("host responses require the declared integration and typed results", () => {
  assert.throws(() => parseStatus({ integration: "other", result: {} }), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
  assert.deepEqual(parseStatus({ integration: "lark-cli", result: { logged_in: true, expired: false, user_name: "Example User" } }), { state: "ok", account: "Example User", guide: [] });
  assert.equal(parseStatus({ integration: "lark-cli", result: { logged_in: false, expired: false } }).state, "not_configured");
  assert.equal(parseStatus({ integration: "lark-cli", result: { logged_in: true, expired: true } }).state, "error");
  assert.throws(() => parseChats({ integration: "lark-cli", result: { chats: "not-an-array" } }));
  assert.equal(parseChats({ integration: "lark-cli", result: { chats: [{ chat_id: "oc_example", name: "Example" }] } })[0].id, "oc_example");
});
test("messages preserve bounded text and strip rich content into plain text", () => {
  const messages = parseMessages({ integration: "lark-cli", result: { messages: [
    { message_id: "om_example", chat_id: "oc_example", msg_type: "text", sender: { name: "Example User" }, content: '{"text":"A <b>plain</b> message"}' },
    { message_id: "om_post", chat_id: "oc_example", msg_type: "post", content: { elements: [{ text: "Nested text" }] } },
  ] } });
  assert.equal(messages[0].text, "A <b>plain</b> message"); assert.equal(messages[1].text, "Nested text");
  assert.throws(() => parseMessages({ integration: "lark-cli", result: { messages: [{ message_id: "om_bad", sender: 42, content: null }] } }), (error: unknown) => error instanceof ReadError && error.code === "bad_answer");
});
test("document search accepts only safe public Lark links and removes highlight markup", () => {
  const result = parseSearch({ integration: "lark-cli", result: { data: { results: [
    { title_highlighted: "<em>Design</em>", result_meta: { url: "https://tenant.feishu.cn/docx/example" } },
    { title: "Unsafe", url: "javascript:alert(1)" },
    { title: "Other host", url: "https://attacker.invalid/document" },
  ] } } }, "docs");
  assert.equal(result.kind, "docs");
  if (result.kind !== "docs") return;
  assert.equal(result.items[0].title, "Design"); assert.equal(result.items[0].url, "https://tenant.feishu.cn/docx/example");
  assert.equal(result.items[1].url, ""); assert.equal(result.items[2].url, "");
});
test("host errors become stable public categories without exporting CLI details", () => {
  assert.equal(errorFrom({ code: "unsupported" }).code, "unavailable");
  assert.equal(errorFrom({ code: "permission_denied", message: "host CLI: expired" }).code, "not_logged_in");
  assert.equal(errorFrom({ code: "permission_denied", message: "host CLI: missing_permission" }).code, "permission");
  assert.equal(errorFrom({ code: "rate_limited" }).code, "rate_limited");
  assert.equal(errorFrom({ code: "timeout" }).code, "slow");
  assert.equal(errorFrom(new Error("private command output")).code, "network");
});
