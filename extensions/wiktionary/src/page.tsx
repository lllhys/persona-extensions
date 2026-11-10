import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, ReadError, word, type Entry } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, type Wiktionary } from "./service.ts";
export interface PageState { q: string; found: boolean }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "entry"; entry: Entry } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 1000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.q !== "string" || typeof value.found !== "boolean") return initialState;
    const q = clip(value.q.trim(), 120);
    return q ? { q, found: value.found } : initialState;
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") {
    try { values = JSON.parse(values); } catch { return state; }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (id === "lookup") {
    const query = typeof fields.word === "string" ? clip(fields.word.trim(), 120) : "";
    try { return query ? { q: word(query), found: true } : state; } catch { return state; }
  }
  if (id === "home") return initialState;
  return state;
}
export async function loadPage(service: Wiktionary, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    return { kind: "entry", entry: await service.lookup(state.q, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Wiktionary; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "lookup", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 4000)} style={style} />;
  const search = <Section key="dictionary-search" title={t.lookup}><Field key="dictionary-word" name="word" label={t.lookup} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="dictionary-submit" title={t.searchAction} action={act("lookup")} /></Section>;
  const home = <Section key="dictionary-home"><Button key="dictionary-home-button" title={t.back} action={act("home")} /></Section>;
  if (loaded.kind === "loading") return <View key="wiktionary" title={t.lookup} state={encodeState(state)}>{search}{state.found ? home : null}<Section key="dictionary-loading"><Loading key="dictionary-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="wiktionary" title={t.lookup} state={encodeState(state)}>{search}{state.found ? home : null}<Section key="dictionary-error">{text("dictionary-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="dictionary-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (loaded.kind === "empty") return <View key="wiktionary" title={t.lookup} state={encodeState(state)}>{search}<Section key="dictionary-welcome">{text("dictionary-welcome-text", t.welcome, "body")}</Section><Section key="dictionary-attribution">{text("dictionary-license", t.attribution, "caption")}</Section></View>;
  const entry = loaded.entry;
  return <View key="wiktionary" title={entry.word} state={encodeState(state)}>{search}
    {entry.groups.map((group, index) => <Section key={`sense-${index}-${group.partOfSpeech}`} title={[group.partOfSpeech, group.language].filter(Boolean).join(" · ")}>
      {group.definitions.map((definition, i) => [
        text(`definition-${index}-${i}`, `${i + 1}. ${definition.text}`, "body"),
        ...definition.examples.map((example, j) => text(`example-${index}-${i}-${j}`, `${t.examples}: ${example.text}`, "caption")),
      ])}
    </Section>)}
    <Section key="dictionary-actions"><Link key="dictionary-open" title={t.open} url={entry.url} /><Button key="dictionary-back-button" title={t.back} action={act("home")} /></Section>
    <Section key="dictionary-footer" footer={`${fmt(t.readAt, { at: readTime(entry.at, tz, props.locale ?? service.locale) })}${entry.stale ? ` · ${errorText(entry.stale, props.locale ?? service.locale)}` : ""}`}><Button key="dictionary-refresh" title={t.refresh} action={act("refresh")} /></Section>
    <Section key="dictionary-attribution">{text("dictionary-license", t.attribution, "caption")}</Section>
  </View>;
}
