import { ExtError, noContribution, unknownMethod, withExactParams, type Handler } from "@persona/extension";
import { identifier, limit, query, ReadError, type Message, type SearchResult } from "./model.ts";
import { errorText, textsFor } from "./i18n.ts";
import type { LarkBridge } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale), count = { type: "integer", minimum: 1, maximum: 50, description: t.limit };
  return [
    { name: "larkbridge_chats", description: t.toolChats, input_schema: { type: "object", additionalProperties: false, properties: { limit: count } } },
    { name: "larkbridge_messages", description: t.toolMessages, input_schema: { type: "object", additionalProperties: false, properties: { chat: { type: "string", description: t.chat }, thread: { type: "string", description: t.threadArg }, before: { type: "string", description: t.before }, limit: count } } },
    { name: "larkbridge_search", description: t.toolSearch, input_schema: { type: "object", required: ["query", "kind"], additionalProperties: false, properties: { query: { type: "string", description: t.query }, kind: { type: "string", enum: ["messages", "chats", "people"], description: t.kindArg } } } },
    { name: "larkbridge_docs", description: t.toolDocs, input_schema: { type: "object", required: ["query"], additionalProperties: false, properties: { query: { type: "string", description: t.query } } } },
  ];
}
export const tools = toolsFor();
const messageText = (message: Message) => `${message.chatName ? message.chatName + "\n" : ""}${message.sender} · ${message.at}\n${message.text}\nMessage: ${message.id}${message.chat ? `\nChat: ${message.chat}` : ""}${message.thread ? `\nTopic: ${message.thread}` : ""}`;
function searchText(result: SearchResult): string {
  if (result.kind === "messages") return result.messages.map(messageText).join("\n\n---\n\n") || "No results.";
  return result.items.map(item => `${item.title}\n${item.detail}${item.url ? `\n${item.url}` : ""}`).join("\n\n---\n\n") || "No results.";
}
function argsFrom(raw: unknown): Record<string, unknown> {
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const args = raw as Record<string, unknown>;
  for (const key of Object.keys(args)) if (!["chat", "thread", "before", "limit", "query", "kind"].includes(key)) throw new ReadError("invalid");
  return args;
}
export async function callTool(service: LarkBridge, name: string, raw: unknown = {}): Promise<string> {
  const args = argsFrom(raw);
  if (name === "larkbridge_chats" || name === "larkbridge_messages" || name === "larkbridge_search" || name === "larkbridge_docs") {
    if (name === "larkbridge_chats" && Object.keys(args).some(key => key !== "limit")) throw new ReadError("invalid");
    if (name === "larkbridge_messages" && Object.keys(args).some(key => !["chat", "thread", "before", "limit"].includes(key))) throw new ReadError("invalid");
    if (name === "larkbridge_search" && Object.keys(args).some(key => !["query", "kind"].includes(key))) throw new ReadError("invalid");
    if (name === "larkbridge_docs" && Object.keys(args).some(key => key !== "query")) throw new ReadError("invalid");
  } else throw unknownMethod(`connector.tools.${name}`);
  if (name === "larkbridge_chats") return (await service.chats()).map(chat => `${chat.name}\nChat: ${chat.id}${chat.type ? `\nType: ${chat.type}` : ""}`).join("\n\n") || "No chats.";
  if (name === "larkbridge_messages") {
    const thread = args.thread == null || args.thread === "" ? "" : identifier(args.thread, "thread");
    const count = limit(args.limit);
    const rows = thread ? await service.thread(thread, count) : await service.messages(identifier(args.chat, "chat"), args.before == null || args.before === "" ? "" : identifier(args.before, "before"), count);
    return rows.map(messageText).join("\n\n---\n\n") || "No messages.";
  }
  if (name === "larkbridge_docs") return searchText(await service.search("docs", query(args.query)));
  if (name === "larkbridge_search") {
    if (args.kind !== "messages" && args.kind !== "chats" && args.kind !== "people") throw new ReadError("invalid");
    return searchText(await service.search(args.kind, query(args.query)));
  }
  throw unknownMethod(`connector.tools.${name}`);
}
export function connector(getService: () => LarkBridge, getLocale: () => string = () => "en"): Handler { return withExactParams(async (method, params: unknown) => {
  const service = getService(), locale = getLocale();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), locale), "invalid");
  if (p.contribution && p.contribution !== "larkbridge") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
