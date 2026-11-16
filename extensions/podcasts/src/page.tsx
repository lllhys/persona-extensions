import { Button, Field, Link, Loading, Row, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, parseStoredShow, podcastID, ReadError, type Episode, type EpisodesResult, type SearchResult, type Show } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, type Podcasts } from "./service.ts";
import type { Storage } from "./store.ts";
export type ViewKind = "discover" | "search" | "show" | "library";
export interface PageState { v: ViewKind; q: string; k: number; from?: "library"; b?: Show; message?: string }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "search"; result: SearchResult } | { kind: "show"; result: EpisodesResult } | { kind: "library"; shows: Show[] } | { kind: "error"; error: unknown };
export const initialState: PageState = { v: "discover", q: "", k: 0 };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 8000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value)) return initialState;
    const k = value.k === 0 ? 0 : podcastID(value.k);
    const state: PageState = { v: ["search", "show", "library"].includes(String(value.v)) ? value.v as ViewKind : "discover",
      q: typeof value.q === "string" ? clip(value.q.trim(), 200) : "", k,
      ...(value.from === "library" ? { from: "library" as const } : {}),
      ...(value.b === undefined ? {} : { b: parseStoredShow(value.b) }),
      ...(typeof value.message === "string" && ["saved", "removed"].includes(value.message) ? { message: value.message } : {}) };
    if (state.v === "show" && (!state.k || !state.b) || state.v === "search" && !state.q) return initialState;
    return state;
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  const [kind, argument] = id.split(":", 2);
  if (kind === "show" && argument && state.b?.id === Number(argument)) return { ...state, v: "show", k: state.b.id, message: undefined };
  if (kind === "saved" && argument && state.b?.id === Number(argument)) return { ...state, message: "saved" };
  if (kind === "removed" && argument && state.b?.id === Number(argument)) return { ...state, message: "removed" };
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") {
    try { values = JSON.parse(values); } catch { return state; }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (kind === "search") {
    const raw = typeof fields.query === "string" ? fields.query : "";
    const query = clip(raw.trim(), 200);
    return query ? { v: "search", q: query, k: 0 } : state;
  }
  if (kind === "library") return { ...state, v: "library", k: 0, from: undefined, b: undefined };
  if (kind === "back") return state.from === "library" ? { v: "library", q: "", k: 0 } : state.q ? { v: "search", q: state.q, k: 0 } : initialState;
  if (kind === "home") return initialState;
  return state;
}
export async function loadPage(service: Podcasts, store: Storage, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "show") return { kind: "show", result: await service.episodes(state.k, 20, refresh, request.signal) };
    if (state.v === "search") return { kind: "search", result: await service.search(state.q, 20, refresh, request.signal) };
    if (state.v === "library") return { kind: "library", shows: (await store.load()).shows };
    return { kind: "empty" };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Podcasts; store: Storage; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "discover", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 1800)} style={style} />;
  const search = <Section key="podcast-search" title={t.search}><Field key="podcast-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="podcast-search-submit" title={t.searchAction} action={act("search")} /></Section>;
  const navigation = <Section key="podcast-navigation"><Button key="podcast-home" title={t.discover} action={act("home")} /><Button key="podcast-library" title={t.library} action={act("library")} /></Section>;
  const message = state.message ? <Section key="podcast-message">{text("podcast-message-text", state.message === "saved" ? t.savedMessage : t.removedMessage, "body")}</Section> : null;
  if (loaded.kind === "loading") return <View key="podcasts" title={t.discover} state={encodeState(state)}>{search}{navigation}<Section key="podcast-loading"><Loading key="podcast-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="podcasts" title={t.discover} state={encodeState(state)}>{search}{navigation}<Section key="podcast-error">{text("podcast-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="podcast-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (state.v === "show") {
    if (loaded.kind !== "show") throw new ReadError("invalid");
    const result = loaded.result, show = result.show, saved = props.store.has(show.id);
    return <View key="podcasts" title={show.title} state={encodeState(state)}>
      <Section key="show-details" title={show.title} footer={[show.publisher, show.genre, show.episodes === undefined ? "" : fmt(t.episodeCount, { count: show.episodes })].filter(Boolean).join(" · ")}>
        {text("show-publisher", show.publisher || t.account, "body")}
      </Section>
      <Section key="show-actions"><Button key="show-save" title={saved ? t.saved : t.save} action={act(`save:${show.id}`)} />{saved ? <Button key="show-remove" title={t.remove} action={act(`remove:${show.id}`)} /> : null}<Link key="show-open" title={t.openShow} url={show.url} />{show.feedURL ? <Link key="show-feed" title={t.openFeed} url={show.feedURL} /> : null}<Button key="show-back" title={t.back} action={act("back")} /></Section>
      {message}
      {result.episodes.length ? <Section key="show-episodes" title={t.episodes}>{result.episodes.map((episode: Episode) => <Link key={`episode-${episode.id}`} title={episode.title} url={episode.url} />)}</Section> : <Section key="show-no-episodes">{text("show-no-episodes-text", t.noEpisodes)}</Section>}
      <Section key="show-episode-notes" title={t.episodes}>{result.episodes.map((episode: Episode) => text(`episode-note-${episode.id}`, `${episode.title}\n${[episode.at ? readTime(episode.at, tz, props.locale ?? service.locale) : t.unknownDate, episode.minutes === undefined ? "" : fmt(t.minutes, { count: episode.minutes })].filter(Boolean).join(" · ")}\n${episode.description}`, "body"))}</Section>
      <Section key="show-footer" footer={`${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` · ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`}><Button key="show-refresh" title={t.refresh} action={act("refresh")} /></Section>
      <Section key="show-feed-note">{text("show-feed-note-text", t.feedHint, "caption")}</Section>
      <Section key="show-attribution">{text("show-license", t.attribution, "caption")}</Section>
    </View>;
  }
  if (state.v === "library") {
    if (loaded.kind !== "library") throw new ReadError("invalid");
    return <View key="podcasts" title={t.library} state={encodeState(state)}>{navigation}
      <Section key="library-shows" title={t.library}>{loaded.shows.length ? loaded.shows.map(show => <Row key={`library-${show.id}`} title={show.title} subtitle={[show.publisher, show.genre].filter(Boolean).join(" · ")} action={act(`show:${show.id}`, { v: "show", q: "", k: show.id, from: "library" as const, b: show }, show.title)} />) : text("library-empty", t.libraryEmpty)}</Section>
      <Section key="library-note">{text("library-note-text", t.libraryHint, "caption")}</Section>
    </View>;
  }
  const row = (show: Show) => <Row key={`show-${show.id}`} title={show.title} subtitle={[show.publisher, show.genre, show.episodes === undefined ? "" : fmt(t.episodeCount, { count: show.episodes })].filter(Boolean).join(" · ")}
    action={act(`show:${show.id}`, { v: "show", q: state.q, k: show.id, b: show }, show.title)} />;
  return <View key="podcasts" title={state.v === "search" ? fmt(t.results, { query: clip(state.q, 100) }) : t.discover} state={encodeState(state)}>{search}{navigation}
    {state.v === "search" ? <Section key="podcast-results" title={fmt(t.results, { query: clip(state.q, 100) })}>
      {loaded.kind === "search" && loaded.result.shows.length ? loaded.result.shows.map(row) : text("podcast-empty", t.empty)}
    </Section> : <Section key="podcast-welcome">{text("podcast-welcome-text", t.searchHint, "body")}</Section>}
    <Section key="podcast-attribution">{text("podcast-license", t.attribution, "caption")}</Section>
  </View>;
}
