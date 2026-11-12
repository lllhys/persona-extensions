import type { Snapshot, UIEvent } from "@persona/extension/ui";
import { identifier, limit, type HostCaller } from "../src/model.ts";
export class Fixture implements HostCaller {
  requests: Record<string, unknown>[] = [];
  answers = new Map<string, unknown>();
  failures = new Map<string, { code: string; message: string }>();
  cancelled = 0;
  constructor() {
    this.answers.set("auth_status", { integration: "lark-cli", result: { logged_in: true, expired: false, user_name: "Example User" } });
    this.answers.set("chats", { integration: "lark-cli", result: { chats: [
      { chat_id: "oc_example_group", name: "Example Group", chat_type: "group" },
      { chat_id: "oc_example_dm", name: "Example Contact", chat_type: "p2p" },
    ] } });
    this.answers.set("messages:oc_example_group", { integration: "lark-cli", result: { messages: [
      { message_id: "om_example_older", msg_type: "text", create_time: "2030-01-01T12:00:00Z", sender: { name: "Example Contact" }, content: '{"text":"Earlier example message"}', chat_id: "oc_example_group" },
      { message_id: "om_example_newer", msg_type: "text", create_time: "2030-01-01T12:01:00Z", sender: { name: "Example User" }, content: { text: "Newer example message" }, chat_id: "oc_example_group", thread_id: "omt_example_thread" },
    ] } });
    this.answers.set("messages:thread:omt_example_thread", { integration: "lark-cli", result: { messages: [
      { message_id: "om_example_newer", msg_type: "text", create_time: "2030-01-01T12:01:00Z", sender: { name: "Example User" }, content: { text: "Newer example message" }, chat_id: "oc_example_group", thread_id: "omt_example_thread" },
    ] } });
    this.answers.set("search:messages:example", { integration: "lark-cli", result: { messages: [
      { message_id: "om_example_search", msg_type: "text", create_time: "2030-01-02T09:00:00Z", sender: { name: "Example Contact" }, content: '{"text":"Example search result"}', chat_id: "oc_example_group", chat_name: "Example Group" },
    ] } });
    this.answers.set("search:chats:Example", { integration: "lark-cli", result: { data: { chats: [{ chat_id: "oc_example_group", name: "Example Group" }] } } });
    this.answers.set("search:people:Example", { integration: "lark-cli", result: { data: { users: [{ user_id: "ou_example_contact", name: "Example Contact" }] } } });
    this.answers.set("search:docs:Example", { integration: "lark-cli", result: { data: { results: [{ title_highlighted: "<em>Example</em> design", result_meta: { url: "https://example.feishu.cn/docx/example_token" } }] } } });
  }
  async call<T>(method: string, params: unknown = {}): Promise<T> {
    if (method !== "cli.call") throw new Error("unexpected host method " + method);
    if (!params || typeof params !== "object" || Array.isArray(params)) throw new Error("invalid parameters");
    const value = params as Record<string, unknown>;
    const allowed = ["operation", "query", "kind", "chat_id", "thread", "before", "limit"];
    if (Object.keys(value).some(key => !allowed.includes(key))) throw new Error("arbitrary arguments crossed the boundary");
    const operation = String(value.operation);
    const count = limit(value.limit, operation === "chats" ? 30 : 20);
    let key = operation;
    if (operation === "messages") key += value.thread ? ":thread:" + identifier(value.thread, "thread") : ":" + identifier(value.chat_id, "chat");
    if (operation === "messages" && value.before) key += ":" + identifier(value.before, "before");
    if (operation === "search") key += ":" + String(value.kind) + ":" + String(value.query);
    this.requests.push({ ...value, limit: count });
    const failure = this.failures.get(key);
    if (failure) throw Object.assign(new Error(failure.message), { code: failure.code });
    if (!this.answers.has(key)) throw Object.assign(new Error("host CLI: not_logged_in"), { code: "permission_denied" });
    return this.answers.get(key) as T;
  }
}
export const until = async <T>(read: () => Promise<T>, ready: (value: T) => boolean): Promise<T> => {
  const deadline = Date.now() + 5000;
  let value: T | undefined;
  while (Date.now() < deadline) { value = await read(); if (ready(value)) return value; await new Promise(resolve => setTimeout(resolve, 10)); }
  throw new Error("condition did not settle: " + JSON.stringify(value));
};
export function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  if (!item || !("action" in item) || !item.action) throw new Error("missing action " + key);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
