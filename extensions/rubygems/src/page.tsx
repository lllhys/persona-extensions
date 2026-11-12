import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, gemName, httpsURL, queryText, ReadError, type DetailResult, type GemSummary, type SearchResult } from "./model.ts";
import { dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, checkSignal, type RubyGems } from "./service.ts";
export interface PageState { q: string; found: boolean; gem?: GemSummary }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "results"; result: SearchResult } | { kind: "detail"; result: DetailResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
function summaryState(value: unknown): GemSummary | undefined {
  if (value == null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value as Record<string, unknown>, name = gemName(raw.name);
  if (typeof raw.version !== "string" || !/^[0-9A-Za-z][0-9A-Za-z.+-]{0,99}$/.test(raw.version) || typeof raw.totalDownloads !== "number" || !Number.isSafeInteger(raw.totalDownloads) || raw.totalDownloads < 0 || raw.totalDownloads > 2_000_000_000_000) throw new ReadError("invalid");
  const summary: GemSummary = { name, version: raw.version, description: clip(typeof raw.description === "string" ? raw.description : "", 500), license: clip(typeof raw.license === "string" ? raw.license : "", 120), url: `https://rubygems.org/gems/${name}`, totalDownloads: raw.totalDownloads };
  for (const field of ["repository", "homepage", "documentation"] as const) {
    if (raw[field] == null) continue;
    const link = httpsURL(raw[field]);
    if (!link) throw new ReadError("invalid");
    summary[field] = link;
  }
  if (raw.versionDownloads != null) {
    if (typeof raw.versionDownloads !== "number" || !Number.isSafeInteger(raw.versionDownloads) || raw.versionDownloads < 0 || raw.versionDownloads > 2_000_000_000_000) throw new ReadError("invalid");
    summary.versionDownloads = raw.versionDownloads;
  }
  return summary;
}
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 10_000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), found: value.found, gem: summaryState(value.gem) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function gemTransition(state: PageState, result: SearchResult, id: string): PageState {
  if (!id.startsWith("gem:")) return state;
  const gem = result.gems.find(candidate => id === `gem:${candidate.name}`);
  return gem ? { q: state.q, found: true, gem } : state;
}
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") { try { values = JSON.parse(values); } catch { return state; } }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (id === "search") {
    try {
      const next = { q: queryText(fields.query), found: true };
      return next.q === state.q && state.found ? { ...next, gem: state.gem } : next;
    } catch { return state; }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, found: true };
  return state;
}
export async function loadPage(service: RubyGems, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (state.gem) return { kind: "detail", result: await service.gem(state.gem.name, refresh, request.signal) };
    return { kind: "results", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: RubyGems; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "rubygems", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="rubygems-search" title={t.search}><Field key="rubygems-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="rubygems-submit" title={t.searchAction} action={act("search")} /></Section>;
  const attribution = <Section key="rubygems-attribution">{text("rubygems-license", t.attribution, "caption")}</Section>;
  const footer = <Section key="rubygems-footer"><Button key="rubygems-refresh" title={t.retry} action={act("refresh")} />{state.gem ? <Button key="rubygems-back" title={t.back} action={act("back")} /> : null}<Button key="rubygems-home" title={t.home} action={act("home")} /></Section>;
  const stale = (error?: ReadError) => error ? <Section key="rubygems-stale">{text("rubygems-stale-text", errorText(error, props.locale ?? service.locale))}</Section> : null;
  if (loaded.kind === "loading") return <View key="rubygems" title={t.search} state={encodeState(state)}>{search}<Section key="rubygems-loading"><Loading key="rubygems-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="rubygems" title={t.search} state={encodeState(state)}>{search}<Section key="rubygems-error">{text("rubygems-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="rubygems-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="rubygems" title={t.search} state={encodeState(state)}>{search}<Section key="rubygems-welcome">{text("rubygems-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  if (loaded.kind === "detail") {
    const gem = loaded.result.gem;
    return <View key="rubygems" title={gem.name} state={encodeState(state)}>{search}
      <Section key="rubygems-detail" title={`${gem.name}@${gem.version}`} footer={[fmt(t.license, { license: gem.license }), gem.published ? dateText(gem.published) : ""].filter(Boolean).join(" · ")}>
        {text("rubygems-description", gem.description, "body")}
        {text("rubygems-total", fmt(t.total, { count: number(gem.totalDownloads) }))}
        {gem.versionDownloads !== undefined ? text("rubygems-version-downloads", fmt(t.versionDownloads, { count: number(gem.versionDownloads) })) : null}
        {gem.yanked ? text("rubygems-yanked", t.yanked, "caption") : null}
      </Section>
      <Section key="rubygems-dependencies" title={fmt(t.dependencies, { count: number(gem.dependencies.length) })}>
        {gem.dependencies.length ? gem.dependencies.slice(0, 30).map(dependency => text(`dependency-${dependency.name}`, `${dependency.name} (${dependency.requirements})`)) : text("rubygems-no-dependencies", t.noDependencies)}
      </Section>
      <Section key="rubygems-install" title={t.install}>{text("rubygems-install-command", gem.install, "body")}{text("rubygems-install-hint", t.installHint, "caption")}</Section>
      <Section key="rubygems-links"><Link key="rubygems-gem-link" title={t.openGem} url={gem.url} />{gem.documentation ? <Link key="rubygems-docs-link" title={t.openDocs} url={gem.documentation} /> : null}{gem.repository ? <Link key="rubygems-repository-link" title={t.openRepository} url={gem.repository} /> : null}{gem.homepage ? <Link key="rubygems-homepage-link" title={t.openHomepage} url={gem.homepage} /> : null}</Section>
      {stale(loaded.result.stale)}{footer}{attribution}
    </View>;
  }
  return <View key="rubygems" title={`${t.title}: ${clip(state.q, 100)}`} state={encodeState(state)}>{search}
    <Section key="rubygems-result-count">{text("rubygems-total-results", fmt(t.results, { total: number(loaded.result.gems.length) }), "caption")}</Section>
    {loaded.result.gems.length ? loaded.result.gems.map(gem => <Section key={`gem-${gem.name}`} title={`${gem.name}@${gem.version}`} footer={[fmt(t.license, { license: gem.license }), fmt(t.total, { count: number(gem.totalDownloads) })].join(" · ")}>
      {text(`description-${gem.name}`, gem.description, "body")}
      <Button key={`open-${gem.name}`} title={t.details} action={act(`gem:${gem.name}`, { ...state, gem }, gem.name)} />
      <Link key={`link-${gem.name}`} title={t.openGem} url={gem.url} />
    </Section>) : <Section key="rubygems-empty-results">{text("rubygems-empty", t.empty)}</Section>}
    {stale(loaded.result.stale)}{footer}{attribution}
  </View>;
}
