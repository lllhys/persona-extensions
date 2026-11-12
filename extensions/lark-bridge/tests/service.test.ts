import { test } from "node:test";
import assert from "node:assert/strict";
import { LarkBridge } from "../src/service.ts";
import { callTool } from "../src/tools.ts";
import { Fixture } from "./fixtures.ts";
test("service uses only typed cli.call reads", async () => {
  const fixture = new Fixture(), service = new LarkBridge(fixture);
  assert.equal((await service.status()).state, "ok");
  assert.equal((await service.chats()).length, 2);
  assert.equal((await service.messages("oc_example_group")).length, 2);
  assert.equal((await service.thread("omt_example_thread")).length, 1);
  assert.equal((await service.search("messages", "example")).kind, "messages");
  const docs = await service.search("docs", "Example");
  assert.equal(docs.kind, "docs");
  assert.ok(fixture.requests.every(request => ["auth_status", "chats", "messages", "search"].includes(String(request.operation))));
  assert.ok(fixture.requests.every(request => !("command" in request) && !("args" in request) && !("profile" in request)));
});
test("assistant tools expose bounded chat, message, people and document reads", async () => {
  const fixture = new Fixture(), service = new LarkBridge(fixture);
  assert.ok((await callTool(service, "larkbridge_chats")).includes("oc_example_group"));
  assert.ok((await callTool(service, "larkbridge_messages", { chat: "oc_example_group" })).includes("Newer example message"));
  assert.ok((await callTool(service, "larkbridge_messages", { thread: "omt_example_thread" })).includes("Newer example message"));
  assert.ok((await callTool(service, "larkbridge_search", { kind: "messages", query: "example" })).includes("Example search result"));
  assert.ok((await callTool(service, "larkbridge_search", { kind: "people", query: "Example" })).includes("Example Contact"));
  assert.ok((await callTool(service, "larkbridge_docs", { query: "Example" })).includes("https://example.feishu.cn/docx/example_token"));
  await assert.rejects(callTool(service, "larkbridge_send", { chat: "oc_example_group", text: "no" }));
  await assert.rejects(callTool(service, "larkbridge_messages", { chat: "oc_example_group", command: "auth" }));
  await assert.rejects(callTool(service, "larkbridge_docs", { query: "" }));
});
test("failed host reads never become successful fabricated results", async () => {
  const fixture = new Fixture(), service = new LarkBridge(fixture);
  fixture.failures.set("chats", { code: "permission_denied", message: "host CLI: missing_permission" });
  await assert.rejects(service.chats(), (error: { code?: string }) => error.code === "permission");
  fixture.failures.set("search:messages:example", { code: "timeout", message: "host CLI: timeout" });
  await assert.rejects(service.search("messages", "example"), (error: { code?: string }) => error.code === "slow");
  fixture.failures.set("auth_status", { code: "unsupported", message: "host CLI: not_installed" });
  assert.equal((await service.status()).state, "error");
});
