import { Button, Field, Image, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, type SearchResult } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, type Commons } from "./service.ts";
export interface PageState { q: string; found: boolean }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "result"; result: SearchResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 2000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.q !== "string" || typeof value.found !== "boolean") return initialState;
    const q = clip(value.q.trim(), 160);
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
    const query = typeof fields.query === "string" ? clip(fields.query.trim(), 160) : "";
    return query ? { q: query, found: true } : state;
  }
  if (id === "home") return initialState;
  return state;
}
export async function loadPage(service: Commons, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    return { kind: "result", result: await service.search(state.q, 20, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Commons; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "search", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 4000)} style={style} />;
  const search = <Section key="commons-search" title={t.search}><Field key="commons-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="commons-submit" title={t.searchAction} action={act("search")} /></Section>;
  if (loaded.kind === "loading") return <View key="commons" title={t.search} state={encodeState(state)}>{search}<Section key="commons-loading"><Loading key="commons-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="commons" title={t.search} state={encodeState(state)}>{search}<Section key="commons-error">{text("commons-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="commons-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (loaded.kind === "empty") return <View key="commons" title={t.search} state={encodeState(state)}>{search}<Section key="commons-welcome">{text("commons-welcome-text", t.welcome, "body")}</Section><Section key="commons-attribution">{text("commons-license", t.attribution, "caption")}</Section></View>;
  const result = loaded.result;
  return <View key="commons" title={fmt(t.results, { query: clip(state.q, 100) })} state={encodeState(state)}>{search}
    {result.images.length ? result.images.map(image => <Section key={`image-card-${image.id}`} title={image.title}>
      {image.thumbnailURL ? <Image key={`picture-${image.id}`} src={{ url: image.thumbnailURL, width: image.width, height: image.height }} alt={clip(image.description || image.title, 199)} caption={clip(image.description || t.noDescription, 1000)} aspect="landscape" /> : null}
      {text(`artist-${image.id}`, `${t.artist}: ${image.artist || t.noDescription}`, "body")}
      {text(`license-${image.id}`, `${t.license}: ${image.license || t.licenseUnknown}`)}
      <Link key={`page-${image.id}`} title={t.open} url={image.pageURL} />
      {image.licenseURL ? <Link key={`license-link-${image.id}`} title={t.licenseLink} url={image.licenseURL} /> : null}
    </Section>) : <Section key="commons-empty">{text("commons-empty-text", t.empty)}</Section>}
    <Section key="commons-footer" footer={`${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` · ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`}><Button key="commons-refresh" title={t.retry} action={act("refresh")} /><Button key="commons-home" title={t.search} action={act("home")} /></Section>
    <Section key="commons-attribution">{text("commons-license", t.attribution, "caption")}</Section>
  </View>;
}
