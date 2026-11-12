import { Button, Field, Link, Loading, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, queryText, ReadError, repositoryName, type DetailResult, type ImageSummary, type SearchResult } from "./model.ts";
import { dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, checkSignal, type DockerHub } from "./service.ts";
export interface PageState { q: string; found: boolean; image?: ImageSummary }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "results"; result: SearchResult } | { kind: "detail"; result: DetailResult } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", found: false };
function summaryState(value: unknown): ImageSummary | undefined {
  if (value == null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value as Record<string, unknown>;
  if (typeof raw.description !== "string" || typeof raw.official !== "boolean" || typeof raw.pulls !== "number" || !Number.isSafeInteger(raw.pulls) || raw.pulls < 0 || raw.pulls > 100_000_000_000_000 || typeof raw.stars !== "number" || !Number.isSafeInteger(raw.stars) || raw.stars < 0 || raw.stars > 100_000_000_000_000) throw new ReadError("invalid");
  return { name: repositoryName(raw.name), description: clip(raw.description, 500), official: raw.official, pulls: raw.pulls, stars: raw.stars, url: `https://hub.docker.com/r/${repositoryName(raw.name)}` };
}
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 10_000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), found: value.found, image: summaryState(value.image) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function imageTransition(state: PageState, result: SearchResult, id: string): PageState {
  if (!id.startsWith("image:")) return state;
  const image = result.images.find(candidate => id === `image:${candidate.name}`);
  return image ? { q: state.q, found: true, image } : state;
}
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") { try { values = JSON.parse(values); } catch { return state; } }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (id === "search") {
    try {
      const next = { q: queryText(fields.query), found: true };
      return next.q === state.q && state.found ? { ...next, image: state.image } : next;
    } catch { return state; }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, found: true };
  return state;
}
export async function loadPage(service: DockerHub, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (state.image) return { kind: "detail", result: await service.repository(state.image.name, refresh, request.signal) };
    return { kind: "results", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: DockerHub; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "dockerhub", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="dockerhub-search" title={t.search}><Field key="dockerhub-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} /><Button key="dockerhub-submit" title={t.searchAction} action={act("search")} /></Section>;
  const attribution = <Section key="dockerhub-attribution">{text("dockerhub-license", t.attribution, "caption")}</Section>;
  const footer = <Section key="dockerhub-footer"><Button key="dockerhub-refresh" title={t.retry} action={act("refresh")} />{state.image ? <Button key="dockerhub-back" title={t.back} action={act("back")} /> : null}<Button key="dockerhub-home" title={t.home} action={act("home")} /></Section>;
  const stale = (error?: ReadError) => error ? <Section key="dockerhub-stale">{text("dockerhub-stale-text", errorText(error, props.locale ?? service.locale))}</Section> : null;
  if (loaded.kind === "loading") return <View key="dockerhub" title={t.search} state={encodeState(state)}>{search}<Section key="dockerhub-loading"><Loading key="dockerhub-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="dockerhub" title={t.search} state={encodeState(state)}>{search}<Section key="dockerhub-error">{text("dockerhub-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="dockerhub-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="dockerhub" title={t.search} state={encodeState(state)}>{search}<Section key="dockerhub-welcome">{text("dockerhub-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  if (loaded.kind === "detail") {
    const image = loaded.result.image;
    return <View key="dockerhub" title={image.name} state={encodeState(state)}>{search}
      <Section key="dockerhub-detail" title={image.name} footer={[image.official ? t.official : fmt(t.namespace, { namespace: image.namespace }), dateText(image.updated)].join(" · ")}>
        {text("dockerhub-description", image.description, "body")}
        {text("dockerhub-total", fmt(t.total, { count: number(image.pulls) }))}
        {text("dockerhub-stars", fmt(t.stars, { count: number(image.stars) }))}
      </Section>
      <Section key="dockerhub-install" title={t.install}>{text("dockerhub-install-command", image.pull, "body")}{text("dockerhub-install-hint", t.installHint, "caption")}</Section>
      <Section key="dockerhub-tags" title={fmt(t.tags, { count: number(image.tags.length) })} />
      {image.tags.map(tag => <Section key={`tag-${tag.name}`} title={tag.name} footer={dateText(tag.updated)}>
        {text(`digest-${tag.name}`, tag.digest)}
        {text(`platforms-${tag.name}`, tag.platforms.join(" · ") || t.noPlatforms)}
        <Link key={`tag-link-${tag.name}`} title={t.openTag} url={`${image.url}/tags?name=${encodeURIComponent(tag.name)}`} />
      </Section>)}
      <Section key="dockerhub-links"><Link key="dockerhub-image-link" title={t.openImage} url={image.url} /></Section>
      {stale(loaded.result.stale)}{footer}{attribution}
    </View>;
  }
  return <View key="dockerhub" title={`${t.title}: ${clip(state.q, 100)}`} state={encodeState(state)}>{search}
    <Section key="dockerhub-result-count">{text("dockerhub-total-results", fmt(t.results, { total: number(loaded.result.total) }), "caption")}</Section>
    {loaded.result.images.length ? loaded.result.images.map(image => <Section key={`image-${image.name}`} title={image.name} footer={[image.official ? t.official : t.community, fmt(t.total, { count: number(image.pulls) })].join(" · ")}>
      {text(`description-${image.name}`, image.description, "body")}
      <Button key={`open-${image.name}`} title={t.details} action={act(`image:${image.name}`, { ...state, image }, image.name)} />
      <Link key={`link-${image.name}`} title={t.openImage} url={image.url} />
    </Section>) : <Section key="dockerhub-empty-results">{text("dockerhub-empty", t.empty)}</Section>}
    {stale(loaded.result.stale)}{footer}{attribution}
  </View>;
}
