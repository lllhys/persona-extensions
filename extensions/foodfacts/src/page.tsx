import { Button, Field, Link, Loading, Row, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, queryText, ReadError, type Product, type SearchResult } from "./model.ts";
import { errorText, fmt, textsFor } from "./i18n.ts";
import { budget, checkSignal, type FoodFacts } from "./service.ts";
export interface PageState { q: string; found: boolean }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "result"; result: SearchResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 1000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    const q = queryText(value.q);
    return { q, found: value.found };
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
    try { return { q: queryText(fields.query), found: true }; } catch { return state; }
  }
  if (id === "home") return initialState;
  return state;
}
export async function loadPage(service: FoodFacts, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (/^[0-9]{8,14}$/.test(state.q)) return { kind: "result", result: { products: [await service.product(state.q, refresh, request.signal)], total: 1, at: service.now() } };
    return { kind: "result", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: FoodFacts; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "products", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="foodfacts-search" title={t.search}><Field key="foodfacts-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="foodfacts-submit" title={t.searchAction} action={act("search")} /></Section>;
  const attribution = <Section key="foodfacts-attribution">{text("foodfacts-license", t.attribution, "caption")}</Section>;
  if (loaded.kind === "loading") return <View key="foodfacts" title={t.search} state={encodeState(state)}>{search}<Section key="foodfacts-loading"><Loading key="foodfacts-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="foodfacts" title={t.search} state={encodeState(state)}>{search}<Section key="foodfacts-error">{text("foodfacts-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="foodfacts-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="foodfacts" title={t.search} state={encodeState(state)}>{search}<Section key="foodfacts-welcome">{text("foodfacts-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  const result = loaded.result;
  return <View key="foodfacts" title={fmt(t.results, { query: clip(state.q, 100) })} state={encodeState(state)}>{search}
    {result.products.length ? result.products.map(product => <Section key={`product-${product.code}`} title={product.name} footer={[product.brands, product.quantity, product.nutriscore ? fmt(t.nutriscore, { grade: product.nutriscore.toUpperCase() }) : "", product.nova ? fmt(t.nova, { group: product.nova }) : ""].filter(Boolean).join(" · ") || t.unknown}>
      {text(`ingredients-${product.code}`, `${t.ingredients}: ${product.ingredients}`, "body")}
      {text(`allergens-${product.code}`, `${t.allergens}: ${product.allergens}`, "caption")}
      <Link key={`link-${product.code}`} title={t.openProduct} url={product.url} />
    </Section>) : <Section key="foodfacts-empty-results">{text("foodfacts-empty", t.empty)}</Section>}
    <Section key="foodfacts-footer"><Button key="foodfacts-refresh" title={t.retry} action={act("refresh")} /><Button key="foodfacts-home" title={t.search} action={act("home")} /></Section>
    {result.stale ? <Section key="foodfacts-stale">{text("foodfacts-stale-text", errorText(result.stale, props.locale ?? service.locale))}</Section> : null}
    {attribution}
  </View>;
}
