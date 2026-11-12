import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, crateName, queryText, ReadError, type CrateSummary, type DetailResult, type SearchResult } from "./model.ts";
import { dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, checkSignal, type Crates } from "./service.ts";
export interface PageState { q: string; found: boolean; crate?: CrateSummary }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "results"; result: SearchResult } | { kind: "detail"; result: DetailResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
function summaryState(value: unknown): CrateSummary | undefined {
  if (value == null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value as Record<string, unknown>, name = crateName(raw.name);
  const summary: CrateSummary = { name, version: typeof raw.version === "string" && /^[0-9A-Za-z][0-9A-Za-z.+-]{0,99}$/.test(raw.version) ? raw.version : (() => { throw new ReadError("invalid"); })(), description: clip(typeof raw.description === "string" ? raw.description : "", 500), license: clip(typeof raw.license === "string" ? raw.license : "", 120), url: `https://crates.io/crates/${name}` };
  for (const field of ["repository", "homepage", "documentation"] as const) {
    if (raw[field] == null) continue;
    if (typeof raw[field] !== "string") throw new ReadError("invalid");
    const link = new URL(raw[field]);
    if (link.protocol !== "https:" || link.username || link.password || link.port) throw new ReadError("invalid");
    summary[field] = link.href;
  }
  if (raw.updated != null) {
    if (typeof raw.updated !== "string" || !Number.isFinite(Date.parse(raw.updated))) throw new ReadError("invalid");
    summary.updated = raw.updated;
  }
  for (const field of ["recentDownloads", "totalDownloads"] as const) {
    if (raw[field] == null) continue;
    if (typeof raw[field] !== "number" || !Number.isSafeInteger(raw[field]) || (raw[field] as number) < 0 || (raw[field] as number) > 2_000_000_000_000) throw new ReadError("invalid");
    summary[field] = raw[field] as number;
  }
  return summary;
}
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 10_000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), found: value.found, crate: summaryState(value.crate) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function crateTransition(state: PageState, result: SearchResult, id: string): PageState {
  if (!id.startsWith("crate:")) return state;
  const crate = result.crates.find(candidate => id === `crate:${candidate.name}`);
  return crate ? { q: state.q, found: true, crate } : state;
}
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") { try { values = JSON.parse(values); } catch { return state; } }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (id === "search") {
    try {
      const next = { q: queryText(fields.query), found: true };
      return next.q === state.q && state.found ? { ...next, crate: state.crate } : next;
    } catch { return state; }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, found: true };
  return state;
}
export async function loadPage(service: Crates, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (state.crate) return { kind: "detail", result: await service.crate(state.crate.name, refresh, request.signal) };
    return { kind: "results", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Crates; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "crates", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="crates-search" title={t.search}><Field key="crates-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="crates-submit" title={t.searchAction} action={act("search")} /></Section>;
  const attribution = <Section key="crates-attribution">{text("crates-license", t.attribution, "caption")}</Section>;
  const footer = <Section key="crates-footer"><Button key="crates-refresh" title={t.retry} action={act("refresh")} />{state.crate ? <Button key="crates-back" title={t.back} action={act("back")} /> : null}<Button key="crates-home" title={t.home} action={act("home")} /></Section>;
  const stale = (error?: ReadError) => error ? <Section key="crates-stale">{text("crates-stale-text", errorText(error, props.locale ?? service.locale))}</Section> : null;
  if (loaded.kind === "loading") return <View key="crates" title={t.search} state={encodeState(state)}>{search}<Section key="crates-loading"><Loading key="crates-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="crates" title={t.search} state={encodeState(state)}>{search}<Section key="crates-error">{text("crates-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="crates-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="crates" title={t.search} state={encodeState(state)}>{search}<Section key="crates-welcome">{text("crates-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  if (loaded.kind === "detail") {
    const crate = loaded.result.crate;
    return <View key="crates" title={crate.name} state={encodeState(state)}>{search}
      <Section key="crates-detail" title={crate.name} footer={[fmt(t.license, { license: crate.license }), crate.published ? fmt(t.published, { version: crate.version, date: dateText(crate.published) }) : `v${crate.version}`].join(" · ")}>
        {text("crates-description", crate.description, "body")}
        {crate.recentDownloads !== undefined ? text("crates-recent", fmt(t.recent, { count: number(crate.recentDownloads) })) : null}
        {crate.totalDownloads !== undefined ? text("crates-total", fmt(t.total, { count: number(crate.totalDownloads) })) : null}
        {crate.versionDownloads !== undefined ? text("crates-version-downloads", fmt(t.versionDownloads, { count: number(crate.versionDownloads) })) : null}
        {crate.yanked ? text("crates-yanked", t.yanked, "caption") : null}
      </Section>
      <Section key="crates-install" title={t.install}>{text("crates-install-command", crate.install, "body")}{text("crates-install-hint", t.installHint, "caption")}</Section>
      <Section key="crates-links"><Link key="crates-crate-link" title={t.openCrate} url={crate.url} />{crate.documentation ? <Link key="crates-docs-link" title={t.openDocs} url={crate.documentation} /> : null}{crate.repository ? <Link key="crates-repository-link" title={t.openRepository} url={crate.repository} /> : null}{crate.homepage ? <Link key="crates-homepage-link" title={t.openHomepage} url={crate.homepage} /> : null}</Section>
      {stale(loaded.result.stale)}{footer}{attribution}
    </View>;
  }
  return <View key="crates" title={`${t.title}: ${clip(state.q, 100)}`} state={encodeState(state)}>{search}
    <Section key="crates-result-count">{text("crates-total-results", fmt(t.results, { total: number(loaded.result.total) }), "caption")}</Section>
    {loaded.result.crates.length ? loaded.result.crates.map(crate => <Section key={`crate-${crate.name}`} title={`${crate.name}@${crate.version}`} footer={[fmt(t.license, { license: crate.license }), crate.recentDownloads !== undefined ? fmt(t.recent, { count: number(crate.recentDownloads) }) : "", crate.updated ? dateText(crate.updated) : ""].filter(Boolean).join(" · ")}>
      {text(`description-${crate.name}`, crate.description, "body")}
      <Button key={`open-${crate.name}`} title={t.details} action={act(`crate:${crate.name}`, { ...state, crate }, crate.name)} />
      <Link key={`link-${crate.name}`} title={t.openCrate} url={crate.url} />
    </Section>) : <Section key="crates-empty-results">{text("crates-empty", t.empty)}</Section>}
    {stale(loaded.result.stale)}{footer}{attribution}
  </View>;
}
