import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, type SearchResult } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, type OpenAlex } from "./service.ts";
export interface PageState { q: string; found: boolean }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "result"; result: SearchResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 2000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.q !== "string" || typeof value.found !== "boolean") return initialState;
    const q = clip(value.q.trim(), 200);
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
  if (id === "search") {
    const query = typeof fields.query === "string" ? clip(fields.query.trim(), 200) : "";
    return query ? { q: query, found: true } : state;
  }
  if (id === "home") return initialState;
  return state;
}
export async function loadPage(service: OpenAlex, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    return { kind: "result", result: await service.search(state.q, 20, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: OpenAlex; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "research", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 4000)} style={style} />;
  const search = <Section key="openalex-search" title={t.search}><Field key="openalex-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="openalex-submit" title={t.searchAction} action={act("search")} /></Section>;
  if (loaded.kind === "loading") return <View key="openalex" title={t.search} state={encodeState(state)}>{search}<Section key="openalex-loading"><Loading key="openalex-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="openalex" title={t.search} state={encodeState(state)}>{search}<Section key="openalex-error">{text("openalex-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="openalex-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (loaded.kind === "empty") return <View key="openalex" title={t.search} state={encodeState(state)}>{search}<Section key="openalex-welcome">{text("openalex-welcome-text", t.welcome, "body")}</Section><Section key="openalex-attribution">{text("openalex-license", t.attribution, "caption")}</Section></View>;
  const result = loaded.result;
  return <View key="openalex" title={fmt(t.results, { query: clip(state.q, 100) })} state={encodeState(state)}>{search}
    {result.works.length ? result.works.map(work => <Section key={`work-${work.id}`} title={work.title} footer={[work.source, work.year ? fmt(t.year, { year: work.year }) : "", fmt(t.citations, { count: work.citations.toLocaleString("en-US") }), work.openAccess ? t.openAccess : t.closedAccess].filter(Boolean).join(" · ")}>
      {work.license ? text(`license-${work.id}`, work.license, "caption") : null}
      {work.oaURL ? <Link key={`fulltext-${work.id}`} title={t.openFulltext} url={work.oaURL} /> : null}
      <Link key={`work-link-${work.id}`} title={t.openWork} url={work.id} />
      {work.doi ? <Link key={`doi-${work.id}`} title={t.openDOI} url={work.doi} /> : null}
    </Section>) : <Section key="openalex-empty-results">{text("openalex-empty", t.empty)}</Section>}
    <Section key="openalex-footer" footer={`${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` · ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`}><Button key="openalex-refresh" title={t.retry} action={act("refresh")} /><Button key="openalex-home" title={t.search} action={act("home")} /></Section>
    <Section key="openalex-attribution">{text("openalex-license", t.attribution, "caption")}</Section>
  </View>;
}
