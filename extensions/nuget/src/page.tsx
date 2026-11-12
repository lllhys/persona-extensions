import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, packageID, queryText, ReadError, type DetailResult, type PackageSummary, type SearchResult } from "./model.ts";
import { dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, checkSignal, type Nuget } from "./service.ts";
export interface PageState { q: string; found: boolean; package?: PackageSummary }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "results"; result: SearchResult } | { kind: "detail"; result: DetailResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
function summaryState(value: unknown): PackageSummary | undefined {
  if (value == null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value as Record<string, unknown>, id = packageID(raw.id);
  const summary: PackageSummary = { id, version: typeof raw.version === "string" && /^[0-9A-Za-z][0-9A-Za-z.+-]{0,99}$/.test(raw.version) ? raw.version : (() => { throw new ReadError("invalid"); })(), description: clip(typeof raw.description === "string" ? raw.description : "", 500), license: clip(typeof raw.license === "string" ? raw.license : "", 120), url: `https://www.nuget.org/packages/${id}/${raw.version}` };
  for (const field of ["projectURL", "licenseURL"] as const) {
    if (raw[field] == null) continue;
    if (typeof raw[field] !== "string") throw new ReadError("invalid");
    const link = new URL(raw[field]);
    if (link.protocol !== "https:" || link.username || link.password || link.port) throw new ReadError("invalid");
    summary[field] = link.href;
  }
  if (raw.published != null) {
    if (typeof raw.published !== "string" || !Number.isFinite(Date.parse(raw.published))) throw new ReadError("invalid");
    summary.published = raw.published;
  }
  if (raw.totalDownloads != null) {
    if (typeof raw.totalDownloads !== "number" || !Number.isSafeInteger(raw.totalDownloads) || raw.totalDownloads < 0 || raw.totalDownloads > 20_000_000_000_000) throw new ReadError("invalid");
    summary.totalDownloads = raw.totalDownloads;
  }
  return summary;
}
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 10_000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), found: value.found, package: summaryState(value.package) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function packageTransition(state: PageState, result: SearchResult, id: string): PageState {
  if (!id.startsWith("package:")) return state;
  const pkg = result.packages.find(candidate => id === `package:${candidate.id}`);
  return pkg ? { q: state.q, found: true, package: pkg } : state;
}
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") { try { values = JSON.parse(values); } catch { return state; } }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (id === "search") {
    try {
      const next = { q: queryText(fields.query), found: true };
      return next.q === state.q && state.found ? { ...next, package: state.package } : next;
    } catch { return state; }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, found: true };
  return state;
}
export async function loadPage(service: Nuget, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (state.package) return { kind: "detail", result: await service.package(state.package.id, refresh, request.signal) };
    return { kind: "results", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Nuget; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "nuget", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="nuget-search" title={t.search}><Field key="nuget-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="nuget-submit" title={t.searchAction} action={act("search")} /></Section>;
  const attribution = <Section key="nuget-attribution">{text("nuget-license", t.attribution, "caption")}</Section>;
  const footer = <Section key="nuget-footer"><Button key="nuget-refresh" title={t.retry} action={act("refresh")} />{state.package ? <Button key="nuget-back" title={t.back} action={act("back")} /> : null}<Button key="nuget-home" title={t.home} action={act("home")} /></Section>;
  const stale = (error?: ReadError) => error ? <Section key="nuget-stale">{text("nuget-stale-text", errorText(error, props.locale ?? service.locale))}</Section> : null;
  if (loaded.kind === "loading") return <View key="nuget" title={t.search} state={encodeState(state)}>{search}<Section key="nuget-loading"><Loading key="nuget-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="nuget" title={t.search} state={encodeState(state)}>{search}<Section key="nuget-error">{text("nuget-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="nuget-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="nuget" title={t.search} state={encodeState(state)}>{search}<Section key="nuget-welcome">{text("nuget-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  if (loaded.kind === "detail") {
    const pkg = loaded.result.package;
    return <View key="nuget" title={pkg.id} state={encodeState(state)}>{search}
      <Section key="nuget-detail" title={pkg.id} footer={[fmt(t.license, { license: pkg.license }), pkg.published ? fmt(t.published, { version: pkg.version, date: dateText(pkg.published) }) : `v${pkg.version}`].join(" · ")}>
        {text("nuget-description", pkg.description, "body")}
        {pkg.totalDownloads !== undefined ? text("nuget-downloads", fmt(t.downloads, { count: number(pkg.totalDownloads) })) : null}
        {text("nuget-dependencies", fmt(t.dependencies, { count: pkg.dependencies }))}
        {text("nuget-frameworks", `${t.frameworks}: ${pkg.frameworks.join(" · ")}`)}
        {!pkg.listed ? text("nuget-unlisted", t.listed, "caption") : null}
      </Section>
      <Section key="nuget-install" title={t.install}>{text("nuget-install-command", pkg.install, "body")}{text("nuget-install-hint", t.installHint, "caption")}</Section>
      <Section key="nuget-links"><Link key="nuget-package-link" title={t.openPackage} url={pkg.url} />{pkg.projectURL ? <Link key="nuget-project-link" title={t.openProject} url={pkg.projectURL} /> : null}{pkg.licenseURL ? <Link key="nuget-license-link" title={t.openLicense} url={pkg.licenseURL} /> : null}</Section>
      {stale(loaded.result.stale)}{footer}{attribution}
    </View>;
  }
  return <View key="nuget" title={`${t.title}: ${clip(state.q, 100)}`} state={encodeState(state)}>{search}
    <Section key="nuget-result-count">{text("nuget-total-results", fmt(t.results, { total: number(loaded.result.total) }), "caption")}</Section>
    {loaded.result.packages.length ? loaded.result.packages.map(pkg => <Section key={`package-${pkg.id}`} title={`${pkg.id}@${pkg.version}`} footer={[fmt(t.license, { license: pkg.license }), pkg.totalDownloads !== undefined ? fmt(t.downloads, { count: number(pkg.totalDownloads) }) : "", pkg.published ? dateText(pkg.published) : ""].filter(Boolean).join(" · ")}>
      {text(`description-${pkg.id}`, pkg.description, "body")}
      <Button key={`open-${pkg.id}`} title={t.details} action={act(`package:${pkg.id}`, { ...state, package: pkg }, pkg.id)} />
      <Link key={`link-${pkg.id}`} title={t.openPackage} url={pkg.url} />
    </Section>) : <Section key="nuget-empty-results">{text("nuget-empty", t.empty)}</Section>}
    {stale(loaded.result.stale)}{footer}{attribution}
  </View>;
}
