import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { clip, identifier, query, ReadError, type Chat, type Message, type SearchItem, type SearchResult } from "./model.ts";
import type { LarkBridge } from "./service.ts";
export type PageState = { mode: "home" } | { mode: "search"; kind: "messages" | "docs" | "chats" | "people"; q: string } | { mode: "chat"; chat: string; title: string } | { mode: "thread"; thread: string; title: string };
export const initialState: PageState = { mode: "home" };
export type Loaded = { kind: "loading" } | { kind: "home"; chats: Chat[] } | { kind: "search"; result: SearchResult } | { kind: "messages"; messages: Message[]; at: string } | { kind: "error"; error: unknown };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 3000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.mode !== "string") return initialState;
    if (value.mode === "home") return initialState;
    if (value.mode === "search" && ["messages", "docs", "chats", "people"].includes(String(value.kind))) return { mode: "search", kind: value.kind as "messages" | "docs" | "chats" | "people", q: query(value.q) };
    if (value.mode === "chat") return { mode: "chat", chat: identifier(value.chat, "chat"), title: clip(String(value.title ?? "Chat"), 300) };
    if (value.mode === "thread") return { mode: "thread", thread: identifier(value.thread, "thread"), title: clip(String(value.title ?? "Topic"), 300) };
  } catch {}
  return initialState;
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function transition(state: PageState, id: string, values: unknown = {}, locale = "en"): PageState {
  const t = textsFor(locale);
  if (id === "home") return initialState;
  if (id.startsWith("chat:")) {
    try { return { mode: "chat", chat: identifier(id.slice(5), "chat"), title: t.chatTitle }; } catch { return state; }
  }
  if (id.startsWith("thread:")) {
    try { return { mode: "thread", thread: identifier(id.slice(7), "thread"), title: t.topic }; } catch { return state; }
  }
  if (id !== "search" || !values || typeof values !== "object" || Array.isArray(values)) return state;
  const fields = values as Record<string, unknown>;
  const kind = fields.kind;
  if (kind !== "messages" && kind !== "docs" && kind !== "chats" && kind !== "people") return state;
  try { return { mode: "search", kind, q: query(fields.query) }; } catch { return state; }
}
export async function loadPage(service: LarkBridge, state: PageState, signal?: AbortSignal, at = new Date()): Promise<Loaded> {
  try {
    signal?.throwIfAborted();
    if (state.mode === "home") return { kind: "home", chats: await service.chats() };
    if (state.mode === "search") return { kind: "search", result: await service.search(state.kind, state.q) };
    if (state.mode === "chat") return { kind: "messages", messages: await service.messages(state.chat), at: at.toISOString() };
    return { kind: "messages", messages: await service.thread(state.thread), at: at.toISOString() };
  } catch (error) { signal?.throwIfAborted(); return { kind: "error", error }; }
}
export type PageAction = (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction;
interface PageProps { service: LarkBridge; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: PageAction }
export function page(props: PageProps): Node {
  const { state, loaded, locale = "en" } = props, t = textsFor(locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && (props.uiVersion ?? 0) >= 3 ? { id, push: { ui: "larkbridge", state: encodeState(next), ...(title ? { title } : {}) } } : { id: next?.mode === "chat" ? `chat:${next.chat}` : next?.mode === "thread" ? `thread:${next.thread}` : id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 8000)} style={style} />;
  const search = <Section key="larkbridge-search" title={t.search}>
    <Field key="larkbridge-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.mode === "search" ? state.q : ""} />
    <Field key="larkbridge-kind" name="kind" label={t.kind} kind="choice" value={state.mode === "search" ? state.kind : "messages"} choices={[{ value: "messages", title: t.messages }, { value: "docs", title: t.docs }, { value: "chats", title: t.chats }, { value: "people", title: t.people }]} />
    <Button key="larkbridge-submit" title={t.searchAction} action={act("search")} />
  </Section>;
  const home = state.mode !== "home" ? <Section key="larkbridge-home"><Button key="larkbridge-back" title={t.back} action={act("home")} /></Section> : null;
  if (loaded.kind === "loading") return <View key="larkbridge" title={t.title} state={encodeState(state)}>{search}{home}<Section key="larkbridge-loading"><Loading key="larkbridge-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="larkbridge" title={t.title} state={encodeState(state)}>{search}{home}<Section key="larkbridge-error">{text("larkbridge-error-message", errorText(loaded.error, locale))}<Button key="larkbridge-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (loaded.kind === "home") return <View key="larkbridge" title={t.title} state={encodeState(state)}>{search}
    <Section key="larkbridge-welcome">{text("larkbridge-welcome-text", t.welcome, "body")}{text("larkbridge-readonly", t.readonly, "caption")}</Section>
    <Section key="larkbridge-recent" title={t.recent} />
    {loaded.chats.map(chat => <Section key={`chat-${chat.id}`} title={chat.name} footer={chat.type}>{text(`chat-id-${chat.id}`, chat.id)}<Button key={`read-${chat.id}`} title={t.read} action={act("chat", { mode: "chat", chat: chat.id, title: chat.name }, chat.name)} /></Section>)}
    {!loaded.chats.length ? <Section key="larkbridge-empty">{text("larkbridge-empty-text", t.noResults)}</Section> : null}
  </View>;
  if (loaded.kind === "search") {
    const result = loaded.result;
    return <View key="larkbridge" title={state.mode === "search" ? state.q : t.title} state={encodeState(state)}>{search}{home}
      {result.kind === "messages" ? messageSections(result.messages, t, act, text) : result.items.map((item: SearchItem, index: number) => <Section key={`result-${index}-${item.id}`} title={item.title}>{item.detail ? text(`detail-${index}`, item.detail, "body") : null}{item.url ? <Link key={`link-${index}`} title={t.open} url={item.url} /> : null}{result.kind === "chats" ? <Button key={`read-${index}`} title={t.read} action={act("chat", { mode: "chat", chat: item.id, title: item.title }, item.title)} /> : null}</Section>)}
      {result.kind === "messages" && !result.messages.length || result.kind !== "messages" && !result.items.length ? <Section key="larkbridge-no-results">{text("larkbridge-no-results-text", t.noResults)}</Section> : null}
      <Section key="larkbridge-footer"><Button key="larkbridge-refresh" title={t.refresh} action={act("refresh")} />{text("larkbridge-footer-readonly", t.readonly, "caption")}</Section>
    </View>;
  }
  return <View key="larkbridge" title={state.mode === "chat" || state.mode === "thread" ? state.title : t.title} state={encodeState(state)}>{search}{home}
    {messageSections(loaded.messages, t, act, text)}
    {!loaded.messages.length ? <Section key="larkbridge-no-messages">{text("larkbridge-no-messages-text", t.noResults)}</Section> : null}
    <Section key="larkbridge-footer" footer={fmt(t.readAt, { at: readTime(loaded.at, locale) })}><Button key="larkbridge-refresh" title={t.refresh} action={act("refresh")} /></Section>
  </View>;
}
function messageSections(messages: Message[], t: ReturnType<typeof textsFor>, act: PageAction, text: (key: string, value: string, style?: "body" | "caption" | "secondary") => Node): Node[] {
  return messages.map((message, index) => <Section key={`message-${index}-${message.id}`} title={message.sender} footer={readTime(message.at)}>
    {message.chatName ? text(`chat-name-${index}`, message.chatName) : null}
    {text(`body-${index}`, message.text, "body")}
    {text(`id-${index}`, message.id, "caption")}
    {message.thread ? <Button key={`thread-${index}`} title={t.thread} action={act("thread", { mode: "thread", thread: message.thread, title: message.chatName || t.thread }, message.chatName || t.thread)} /> : null}
  </Section>);
}
