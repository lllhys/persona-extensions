export const clip = (value: string, most: number) => Array.from(value).length <= most ? value : Array.from(value).slice(0, most).join("").trimEnd() + "…";
export const oneLine = (value: string) => value.replace(/[\u0000-\u001f\u007f]+/gu, " ").replace(/\s+/gu, " ").trim();
export type ReadCode = "invalid" | "unavailable" | "not_logged_in" | "permission" | "rate_limited" | "slow" | "bad_answer" | "not_found" | "network";
export class ReadError extends Error {
  constructor(readonly code: ReadCode) { super(code); }
}
export interface HostCaller { call<T = unknown>(method: string, params?: unknown): Promise<T> }
export interface Chat { id: string; name: string; type: string }
export interface Message { id: string; chat: string; chatName: string; sender: string; text: string; at: string; type: string; thread: string }
export interface SearchItem { id: string; title: string; detail: string; url: string }
export type SearchResult = { kind: "messages"; messages: Message[] } | { kind: "docs" | "chats" | "people"; items: SearchItem[]; raw: string };
export function query(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text = value.trim();
  if (!text || text.length > 500 || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/u.test(text)) throw new ReadError("invalid");
  return text;
}
export function identifier(value: unknown, kind: "chat" | "thread" | "before"): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text = value.trim();
  const maximum = kind === "before" ? 500 : 240;
  if (!text || text.length > maximum || !/^[A-Za-z0-9_.:-]+$/u.test(text)) throw new ReadError("invalid");
  return text;
}
export function limit(value: unknown, fallback = 20): number {
  if (value == null) return fallback;
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 50) throw new ReadError("invalid");
  return value;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function text(value: unknown, fallback = "", most = 4000): string {
  if (value == null) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return clip(value, most);
}
function bool(value: unknown): boolean { if (typeof value !== "boolean") throw new ReadError("bad_answer"); return value; }
function hostResult(raw: unknown): unknown {
  const answer = object(raw);
  if (answer.integration !== "lark-cli") throw new ReadError("bad_answer");
  return answer.result;
}
function content(value: unknown): string {
  if (value == null) return "[message]";
  let data: unknown = value;
  if (typeof value === "string") {
    if (value.length > 100_000) return clip(value, 8000);
    try { data = JSON.parse(value); } catch { return clip(value, 8000); }
  }
  if (!data || typeof data !== "object" || Array.isArray(data)) return "[message]";
  const item = data as Record<string, unknown>;
  if (typeof item.text === "string") return clip(item.text, 8000);
  if (!Array.isArray(item.content) && !Array.isArray(item.elements) && !Array.isArray(item.title) && !Array.isArray(item.body)) return "[rich message]";
  const collect = (node: unknown, depth: number): string[] => {
    if (depth > 5) return [];
    if (typeof node === "string") return [node];
    if (Array.isArray(node)) return node.slice(0, 80).flatMap(child => collect(child, depth + 1));
    if (!node || typeof node !== "object") return [];
    const value = node as Record<string, unknown>;
    if (typeof value.text === "string") return [value.text];
    return Object.values(value).slice(0, 40).flatMap(child => collect(child, depth + 1));
  };
  const result = collect(data, 0).join(" ").replace(/\s+/gu, " ").trim();
  return clip(result || "[rich message]", 8000);
}
export function parseStatus(raw: unknown): { state: string; account: string; guide: string[] } {
  const value = object(hostResult(raw));
  const loggedIn = bool(value.logged_in), expired = bool(value.expired);
  const name = clip(text(value.user_name, "lark-cli", 200), 200);
  if (!loggedIn) return { state: "not_configured", account: "", guide: ["Use the existing host-managed lark-cli sign-in. This extension cannot start another login."] };
  if (expired) return { state: "error", account: name, guide: ["The existing lark-cli authorization has expired. Renew it through the host's Lark settings."] };
  return { state: "ok", account: name, guide: [] };
}
export function parseChats(raw: unknown): Chat[] {
  const result = object(hostResult(raw));
  if (!Array.isArray(result.chats) || result.chats.length > 50) throw new ReadError("bad_answer");
  return result.chats.map(item => {
    const value = object(item);
    return { id: identifier(value.chat_id, "chat"), name: clip(oneLine(text(value.name, "Chat")), 300), type: clip(text(value.chat_type), 40) };
  });
}
export function parseMessages(raw: unknown): Message[] {
  const result = object(hostResult(raw));
  if (!Array.isArray(result.messages) || result.messages.length > 50) throw new ReadError("bad_answer");
  return result.messages.map(item => {
    const value = object(item), sender = value.sender == null ? {} : object(value.sender);
    return {
      id: identifier(value.message_id, "thread"), chat: typeof value.chat_id === "string" && value.chat_id ? identifier(value.chat_id, "chat") : "",
      chatName: clip(oneLine(text(value.chat_name)), 300), sender: clip(oneLine(text(sender.name, "Unknown sender")), 200),
      text: content(value.content), at: clip(text(value.create_time), 80), type: clip(text(value.msg_type), 80),
      thread: typeof value.thread_id === "string" && value.thread_id ? identifier(value.thread_id, "thread") : "",
    };
  });
}
function publicURL(value: unknown): string {
  if (typeof value !== "string" || value.length > 2000) return "";
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password && /(^|\.)(?:feishu\.cn|larksuite\.com)$/i.test(url.hostname) ? url.href : "";
  } catch { return ""; }
}
function rawItems(data: unknown): unknown[] {
  if (Array.isArray(data)) return data.slice(0, 50);
  const value = object(data);
  for (const key of ["results", "items", "chats", "users", "docs"]) if (Array.isArray(value[key])) return (value[key] as unknown[]).slice(0, 50);
  return [];
}
export function parseSearch(raw: unknown, kind: "messages" | "docs" | "chats" | "people"): SearchResult {
  const result = hostResult(raw);
  if (kind === "messages") return { kind, messages: parseMessages({ integration: "lark-cli", result }) };
  const data = object(result).data;
  const items = rawItems(data).map((rawItem, index): SearchItem => {
    const value = object(rawItem), meta = value.result_meta == null ? {} : object(value.result_meta);
    const title = text(value.title_highlighted ?? value.title ?? value.name ?? value.display_name, "Result").replace(/<\/?(?:em|b)>/gi, "");
    const detail = text(value.description ?? value.summary ?? value.chat_id ?? value.user_id ?? value.open_id ?? value.token, "");
    const url = publicURL(value.url ?? value.canonical_url ?? meta.url);
    return { id: text(value.chat_id ?? value.user_id ?? value.open_id ?? value.token, `result-${index}`, 240), title: clip(oneLine(title), 400), detail: clip(oneLine(detail), 600), url };
  });
  return { kind, items, raw: clip(JSON.stringify(data), 100_000) };
}
export function errorFrom(raw: unknown): ReadError {
  const code = errorCode(raw), message = errorMessage(raw);
  if (code === "invalid" || code === "invalid_params") return new ReadError("invalid");
  if (code === "rate_limited") return new ReadError("rate_limited");
  if (code === "timeout") return new ReadError("slow");
  if (code === "unsupported" || code === "unavailable") return new ReadError("unavailable");
  if (code === "permission_denied") return new ReadError(message.includes("not_logged_in") || message.includes("expired") ? "not_logged_in" : "permission");
  return new ReadError("network");
}
function errorCode(raw: unknown): unknown {
  return raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as { code?: unknown }).code : undefined;
}
export function errorMessage(raw: unknown): string {
  return raw instanceof Error ? raw.message : raw && typeof raw === "object" && !Array.isArray(raw) && typeof (raw as { message?: unknown }).message === "string" ? (raw as { message: string }).message : "";
}
