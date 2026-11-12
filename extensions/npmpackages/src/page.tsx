import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, packageName, queryText, ReadError, type DetailResult, type PackageSummary, type SearchResult } from "./model.ts";
import { bytes, dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, checkSignal, type NpmPackages } from "./service.ts";
export interface PageState { q: string; found: boolean; package?: PackageSummary }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "results"; result: SearchResult } | { kind: "detail"; result: DetailResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
function summaryState(value: unknown): PackageSummary | undefined {
  if (value == null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value as Record<string, unknown>, name = packageName(raw.name);
  const summary: PackageSummary = { name, version: typeof raw.version === "string" && /^[0-9A-Za-z][0-9A-Za-z.+-]{0,99}$/.test(raw.version) ? raw.version : (() => { throw new ReadError("invalid"); })(), description: clip(typeof raw.description === "string" ? raw.description : "", 500), license: clip(typeof raw.license === "string" ? raw.license : "", 120), url: "" };
  const url = new URL(typeof raw.url === "string" ? raw.url : "");
  if (url.protocol !== "https:" || url.hostname !== "www.npmjs.com" || url.username || url.password || url.port || url.pathname !== `/package/${name}`) throw new ReadError("invalid");
  summary.url = url.href;
  for (const field of ["repository", "homepage"] as const) {
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
  for (const field of ["weeklyDownloads", "monthlyDownloads"] as const) {
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
    return { q: queryText(value.q), found: value.found, package: summaryState(value.package) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function packageTransition(state: PageState, result: SearchResult, id: string): PageState {
  if (!id.startsWith("package:")) return state;
  const pkg = result.packages.find(candidate => id === `package:${candidate.name}`);
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
export async function loadPage(service: NpmPackages, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (state.package) return { kind: "detail", result: await service.package(state.package.name, refresh, request.signal) };
    return { kind: "results", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: NpmPackages; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "packages", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="npmpackages-search" title={t.search}><Field key="npmpackages-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="npmpackages-submit" title={t.searchAction} action={act("search")} /></Section>;
  const attribution = <Section key="npmpackages-attribution">{text("npmpackages-license", t.attribution, "caption")}</Section>;
  const footer = <Section key="npmpackages-footer"><Button key="npmpackages-refresh" title={t.retry} action={act("refresh")} />{state.package ? <Button key="npmpackages-back" title={t.back} action={act("back")} /> : null}<Button key="npmpackages-home" title={t.home} action={act("home")} /></Section>;
  const stale = (error?: ReadError) => error ? <Section key="npmpackages-stale">{text("npmpackages-stale-text", errorText(error, props.locale ?? service.locale))}</Section> : null;
  if (loaded.kind === "loading") return <View key="npmpackages" title={t.search} state={encodeState(state)}>{search}<Section key="npmpackages-loading"><Loading key="npmpackages-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="npmpackages" title={t.search} state={encodeState(state)}>{search}<Section key="npmpackages-error">{text("npmpackages-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="npmpackages-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="npmpackages" title={t.search} state={encodeState(state)}>{search}<Section key="npmpackages-welcome">{text("npmpackages-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  if (loaded.kind === "detail") {
    const { package: pkg, downloadsStart, downloadsEnd } = loaded.result;
    const links = <Section key="npmpackages-links"><Link key="npmpackages-npm-link" title={t.openPackage} url={pkg.url} />{pkg.repository ? <Link key="npmpackages-repository-link" title={t.openRepository} url={pkg.repository} /> : null}{pkg.homepage ? <Link key="npmpackages-homepage-link" title={t.openHomepage} url={pkg.homepage} /> : null}</Section>;
    return <View key="npmpackages" title={pkg.name} state={encodeState(state)}>{search}
      <Section key="npmpackages-detail" title={pkg.name} footer={[fmt(t.license, { license: pkg.license }), pkg.published ? fmt(t.published, { version: pkg.version, date: dateText(pkg.published) }) : `v${pkg.version}`].join(" · ")}>
        {text("npmpackages-description", pkg.description, "body")}
        {text("npmpackages-downloads", pkg.weeklyDownloads === undefined ? t.noDownloads : `${fmt(t.weekly, { count: number(pkg.weeklyDownloads) })}${downloadsStart && downloadsEnd ? ` · ${downloadsStart} – ${downloadsEnd}` : ""}`)}
        {text("npmpackages-dependencies", fmt(t.dependencies, { count: pkg.dependencies }))}
        {text("npmpackages-engines", `${t.engines}: ${pkg.engines}`)}
        {pkg.unpackedSize !== undefined ? text("npmpackages-size", fmt(t.size, { count: bytes(pkg.unpackedSize) })) : null}
        {text("npmpackages-types", pkg.types ? t.types : t.typesNone, "caption")}
      </Section>
      <Section key="npmpackages-install" title={t.install}>{text("npmpackages-install-command", pkg.install, "body")}{text("npmpackages-install-hint", t.installHint, "caption")}</Section>
      {links}{stale(loaded.result.stale)}{footer}{attribution}
    </View>;
  }
  return <View key="npmpackages" title={`${t.title}: ${clip(state.q, 100)}`} state={encodeState(state)}>{search}
    <Section key="npmpackages-result-count">{text("npmpackages-total", fmt(t.results, { total: number(loaded.result.total) }), "caption")}</Section>
    {loaded.result.packages.length ? loaded.result.packages.map(pkg => <Section key={`package-${pkg.name}`} title={`${pkg.name}@${pkg.version}`} footer={[fmt(t.license, { license: pkg.license }), pkg.weeklyDownloads !== undefined ? fmt(t.weekly, { count: number(pkg.weeklyDownloads) }) : "", pkg.published ? dateText(pkg.published) : ""].filter(Boolean).join(" · ")}>
      {text(`description-${pkg.name}`, pkg.description, "body")}
      <Button key={`open-${pkg.name}`} title={t.details} action={act(`package:${pkg.name}`, { ...state, package: pkg }, pkg.name)} />
      <Link key={`link-${pkg.name}`} title={t.openPackage} url={pkg.url} />
    </Section>) : <Section key="npmpackages-empty-results">{text("npmpackages-empty", t.empty)}</Section>}
    {stale(loaded.result.stale)}{footer}{attribution}
  </View>;
}
