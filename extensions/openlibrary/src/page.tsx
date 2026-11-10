import { Button, Field, Link, Loading, Row, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, ReadError, workKey, type Book, type SearchResult, type Work } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, type OpenLibrary } from "./service.ts";
export type ViewKind = "discover" | "search" | "work";
export interface PageState { v: ViewKind; q: string; k: string; b?: Book }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "search"; result: SearchResult } | { kind: "work"; work: Work } | { kind: "error"; error: unknown };
export const initialState: PageState = { v: "discover", q: "", k: "" };
function book(raw: unknown): Book | undefined {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return undefined;
  const b = raw as Record<string, unknown>;
  if (typeof b.key !== "string" || typeof b.title !== "string" || !Array.isArray(b.authors) || b.authors.some(x => typeof x !== "string") ||
    typeof b.url !== "string" || b.url !== `https://openlibrary.org/works/${b.key}` || typeof b.access !== "string" ||
    !["public", "borrowable", "printdisabled", "no_ebook"].includes(b.access)) return undefined;
  if (b.year != null && (typeof b.year !== "number" || !Number.isSafeInteger(b.year) || b.year < 0 || b.year > 2200)) return undefined;
  if (b.editions != null && (typeof b.editions !== "number" || !Number.isSafeInteger(b.editions) || b.editions < 0)) return undefined;
  if (b.readingURL != null && (typeof b.readingURL !== "string" || !/^https:\/\/openlibrary\.org\/books\/ia:[A-Za-z0-9][A-Za-z0-9._%-]{0,150}$/.test(b.readingURL))) return undefined;
  try { workKey(b.key); } catch { return undefined; }
  return { key: b.key, title: clip(b.title, 500), authors: b.authors.slice(0, 8).map(x => clip(x as string, 200)),
    ...(b.year == null ? {} : { year: b.year as number }), ...(b.editions == null ? {} : { editions: b.editions as number }),
    access: b.access as Book["access"], url: b.url, ...(typeof b.readingURL === "string" ? { readingURL: b.readingURL } : {}) };
}
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 8000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value)) return initialState;
    const state: PageState = { v: value.v === "search" || value.v === "work" ? value.v : "discover",
      q: typeof value.q === "string" ? clip(value.q.trim(), 200) : "", k: typeof value.k === "string" && value.k ? workKey(value.k) : "",
      ...(value.b === undefined ? {} : { b: book(value.b) }) };
    if (state.v === "work" && (!state.k || !state.b) || state.v === "search" && !state.q) return initialState;
    return state;
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  const [kind, argument] = id.split(":", 2);
  if (kind === "work" && argument && state.b?.key === argument) return { ...state, v: "work", k: argument };
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") {
    try { values = JSON.parse(values); } catch { return state; }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  const string = (key: string) => typeof fields[key] === "string" ? fields[key] as string : undefined;
  if (kind === "search") {
    const raw = string("query") ?? "";
    const query = clip(raw.trim(), 200);
    return query ? { v: "search", q: query, k: "" } : state;
  }
  if (kind === "back") return state.q ? { v: "search", q: state.q, k: "" } : initialState;
  if (kind === "home") return initialState;
  return state;
}
export async function loadPage(service: OpenLibrary, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "work") {
      if (!state.b) throw new ReadError("invalid");
      return { kind: "work", work: await service.work(state.k, state.b, refresh, request.signal) };
    }
    if (state.v === "search") return { kind: "search", result: await service.search(state.q, 20, refresh, request.signal) };
    return { kind: "empty" };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: OpenLibrary; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "discover", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 8000)} style={style} />;
  const search = <Section key="book-search" title={t.search}><Field key="book-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="book-search-submit" title={t.searchAction} action={act("search")} /></Section>;
  const navigation = <Section key="book-navigation"><Button key="book-back" title={state.q ? t.back : t.backDiscover} action={act("back")} /></Section>;
  if (loaded.kind === "loading") return <View key="openlibrary" title={t.discover} state={encodeState(state)}>{search}{state.v !== "discover" ? navigation : null}<Section key="book-loading"><Loading key="book-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="openlibrary" title={t.discover} state={encodeState(state)}>{search}{state.v !== "discover" ? navigation : null}<Section key="book-error">{text("book-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="book-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (state.v === "work") {
    if (loaded.kind !== "work") throw new ReadError("invalid");
    const work = loaded.work;
    return <View key="openlibrary" title={work.title} state={encodeState(state)}>
      <Section key="work-details" title={work.title} footer={[work.year ? fmt(t.year, { year: work.year }) : t.unknownYear, work.editions ? fmt(t.editions, { count: work.editions }) : "", t[work.access]].filter(Boolean).join(" · ")}>
        {text("work-authors", work.authors.join(" · "), "body")}{text("work-description", work.description || t.noDescription, "body")}
      </Section>
      {work.subjects.length ? <Section key="work-subjects" title={t.subjects}>{text("work-subject-list", work.subjects.join(" · "))}</Section> : null}
      <Section key="work-actions">{work.readingURL ? <Link key="work-read" title={t.read} url={work.readingURL} /> : null}<Link key="work-open" title={t.open} url={work.url} /><Button key="work-back" title={t.back} action={act("back")} /></Section>
      <Section key="work-footer" footer={`${fmt(t.readAt, { at: readTime(work.at, tz, props.locale ?? service.locale) })}${work.stale ? ` · ${errorText(work.stale, props.locale ?? service.locale)}` : ""}`}><Button key="work-refresh" title={t.refresh} action={act("refresh")} /></Section>
      <Section key="work-attribution">{text("work-license", t.attribution, "caption")}</Section>
    </View>;
  }
  const row = (book: Book) => <Row key={`book-${book.key}`} title={book.title} subtitle={[book.authors.join(" · "), book.year ? fmt(t.year, { year: book.year }) : "", t[book.access]].filter(Boolean).join(" · ")}
    action={act(`work:${book.key}`, { v: "work", q: state.q, k: book.key, b: book }, book.title)} />;
  return <View key="openlibrary" title={state.v === "search" ? fmt(t.results, { query: clip(state.q, 100) }) : t.discover} state={encodeState(state)}>{search}
    {state.v === "search" ? <Section key="book-results" title={fmt(t.results, { query: clip(state.q, 100) })}>
      {loaded.kind === "search" && loaded.result.books.length ? loaded.result.books.map(row) : text("book-empty", t.empty)}<Button key="book-home" title={t.backDiscover} action={act("home")} />
    </Section> : <Section key="book-welcome">{text("book-welcome-text", t.searchHint, "body")}</Section>}
    <Section key="book-attribution">{text("book-license", t.attribution, "caption")}</Section>
  </View>;
}
