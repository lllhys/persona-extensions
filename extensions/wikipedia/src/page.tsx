import { Button, Field, Link, Loading, Row, Section, Segmented, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, dateAt, titleKey, type Article, type Feed } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, ReadError, type Wikipedia } from "./service.ts";
export type Tab = "featured" | "popular" | "history";
export type ViewKind = "today" | "search" | "article";
export interface PageState { v: ViewKind; t: Tab; a: string; q: string; d: string }
export type Loaded = { kind: "loading" } | { kind: "today"; feed: Feed } | { kind: "search"; articles: Article[] } | { kind: "article"; article: Article } | { kind: "error"; error: unknown };
export const initialState: PageState = { v: "today", t: "featured", a: "", q: "", d: "" };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 4000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value)) return initialState;
    const state: PageState = {
      v: value.v === "search" || value.v === "article" ? value.v : "today",
      t: value.t === "popular" || value.t === "history" ? value.t : "featured",
      a: typeof value.a === "string" && value.a ? titleKey(value.a) : "",
      q: typeof value.q === "string" ? clip(value.q.trim(), 200) : "",
      d: typeof value.d === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value.d) ? value.d : "",
    };
    if (state.v === "article" && !state.a || state.v === "search" && !state.q) state.v = "today";
    return state;
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  const [kind, argument] = id.split(":", 2);
  // Legacy full-view actions carry a JSON-encoded record. Reject other shapes before
  // reading fields; a string itself is never a search query or an outbound target.
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") {
    try { values = JSON.parse(values); } catch { return state; }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  const string = (key: string) => typeof fields[key] === "string" ? fields[key] as string : undefined;
  try {
    if (kind === "tab" && (argument === "featured" || argument === "popular" || argument === "history")) return { ...state, t: argument };
    if (kind === "tab" && (string("tab") === "featured" || string("tab") === "popular" || string("tab") === "history")) return { ...state, t: string("tab")! as Tab };
    if (kind === "search") {
      const query = clip((string("query") ?? "").trim(), 200);
      return query ? { ...state, v: "search", q: query, a: "" } : state;
    }
    if (kind === "article" && argument) return { ...state, v: "article", a: titleKey(argument) };
    if (kind === "back") return { ...state, v: state.q ? "search" : "today", a: "" };
    if (kind === "home") return { ...initialState, d: state.d };
  } catch { /* Invalid external state never selects a new outbound target. */ }
  return state;
}
export async function loadPage(service: Wikipedia, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "article") return { kind: "article", article: await service.article(state.a, refresh, request.signal) };
    if (state.v === "search") return { kind: "search", articles: await service.search(state.q, 30, request.signal) };
    return { kind: "today", feed: await service.today(state.d || dateAt(service.now()), refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Wikipedia; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? service.timezone;
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "today", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 1900)} style={style} />;
  const search = <Section key="search" title={t.search}><Field key="query-field" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="do-search" title={t.searchAction} action={act("search")} /></Section>;
  const tabs = <Section key="tabs">{uiVersion >= 2 ? <Segmented key="tab" name="tab" value={state.t} choices={[{ value: "featured", title: t.featuredShort }, { value: "popular", title: t.popularShort }, { value: "history", title: t.historyShort }]} action={act("tab")} />
    : (["featured", "popular", "history"] as const).map(tab => <Row key={`tab-${tab}`} title={t[tab]} action={act("tab:" + tab)} badge={state.t === tab ? { text: t.read, tone: "positive" } : undefined} />)}</Section>;
  const back = <Section key="navigation"><Button key="back-button" title={state.q ? t.back : t.backToday} action={act("back")} /></Section>;
  if (loaded.kind === "loading") return <View key="wikipedia" title={t.today} state={encodeState(state)}>{search}{state.v === "today" ? tabs : back}<Section key="loading"><Loading key="loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="wikipedia" title={t.today} state={encodeState(state)}>{search}{state.v === "today" ? tabs : back}<Section key="error">{text("message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (state.v === "article") {
    if (loaded.kind !== "article") throw new ReadError("invalid");
    const article = loaded.article;
    return <View key="wikipedia" title={article.heading} state={encodeState(state)}>
      <Section key="article-summary">{article.description ? text("description", article.description, "caption") : null}{article.extract ? text("extract", article.extract, "body") : text("no-summary", t.noFeatured)}</Section>
      <Section key="article-actions" footer={t.summaryHint}><Link key="open" title={t.open} url={article.url} /><Button key="back-button" title={state.q ? t.back : t.backToday} action={act("back")} /></Section>
      <Section key="attribution">{text("license", t.attribution, "caption")}</Section>
    </View>;
  }
  const articleRow = (article: Article, index: number) => <Row key={`article-${index}-${article.title}`} title={article.heading} subtitle={[article.description, article.views !== undefined ? fmt(t.views, { views: article.views.toLocaleString("en-US") }) : ""].filter(Boolean).join(" · ")} action={act(`article:${article.title}`, { ...state, v: "article", a: article.title }, article.heading)} />;
  if (state.v === "search") {
    if (loaded.kind !== "search") throw new ReadError("invalid");
    return <View key="wikipedia" title={fmt(t.results, { query: clip(state.q, 100) })} state={encodeState(state)}>{search}<Section key="results" title={fmt(t.results, { query: clip(state.q, 100) })}>{loaded.articles.length ? loaded.articles.map(articleRow) : text("empty", t.empty)}<Button key="home" title={t.backToday} action={act("home")} /></Section></View>;
  }
  if (loaded.kind !== "today") throw new ReadError("invalid");
  const feed = loaded.feed;
  const footer = `${fmt(t.readAt, { at: readTime(feed.at, tz, props.locale ?? service.locale) })}${feed.stale ? ` · ${errorText(feed.stale, props.locale ?? service.locale)}` : ""}`;
  return <View key="wikipedia" title={t.today} state={encodeState(state)}>{search}{tabs}
    {state.t === "featured" ? <Section key="featured" title={t.featured}>{feed.featured ? articleRow(feed.featured, 0) : text("no-featured", t.noFeatured)}</Section> : null}
    {state.t === "popular" ? <Section key="popular" title={t.popular}>{feed.popular.length ? feed.popular.slice(0, 20).map(articleRow) : text("empty", t.empty)}</Section> : null}
    {state.t === "history" ? <Section key="history" title={t.history}>{feed.events.length ? feed.events.slice(0, 12).map((event, i) => [
      text(`event-${i}-${event.year}`, `${event.year} · ${event.text}`, "body"),
      event.pages[0] ? articleRow(event.pages[0], i) : null,
    ]) : text("empty", t.empty)}</Section> : null}
    <Section key="footer" footer={footer}><Button key="refresh" title={t.refresh} action={act("refresh")} /></Section>
    <Section key="attribution">{text("license", t.attribution, "caption")}</Section>
  </View>;
}
