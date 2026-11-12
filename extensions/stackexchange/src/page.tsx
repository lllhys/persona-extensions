import { Button, Field, Link, Loading, Markdown, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, oneLine, queryText, questionID, ReadError, selectedAnswer, siteID, sites, siteIDs, tagText, type Question, type SearchResult, type Site } from "./model.ts";
import { errorText, fmt, textsFor } from "./i18n.ts";
import { budget, checkSignal, type StackExchange } from "./service.ts";
export interface PageState { q: string; site: Site; tag: string; found: boolean; question?: Question }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "results"; result: SearchResult } | { kind: "answer"; question: Question; body: string; accepted: boolean; id: number; url: string; author: string; license: string; stale?: ReadError; quotaRemaining?: number } | { kind: "no-answer" } | { kind: "error"; error: unknown };
export const initialState: PageState = { q: "", site: "stackoverflow", tag: "", found: false };
function questionState(value: unknown, site: Site): Question | undefined {
  if (value == null) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value as Record<string, unknown>, id = questionID(raw.id);
  const candidate: Question = { id, title: queryText(raw.title), url: "", score: 0, answerCount: 0, tags: [], license: "CC BY-SA 4.0", author: "" };
  const url = new URL(candidate.url = typeof raw.url === "string" ? raw.url : "");
  if (url.protocol !== "https:" || url.hostname !== sites[site].host || url.username || url.password || url.port || url.pathname !== `/questions/${id}` && !url.pathname.startsWith(`/questions/${id}/`)) throw new ReadError("invalid");
  candidate.url = url.href;
  if (!Number.isSafeInteger(raw.score) || !Number.isSafeInteger(raw.answerCount) || (raw.answerCount as number) < 0 || (raw.score as number) < -1_000_000 || (raw.score as number) > 1_000_000 || (raw.answerCount as number) > 1_000_000) throw new ReadError("invalid");
  candidate.score = raw.score as number; candidate.answerCount = raw.answerCount as number;
  candidate.tags = Array.isArray(raw.tags) ? raw.tags.slice(0, 10).map(tagText) : [];
  if (new Set(candidate.tags).size !== candidate.tags.length) throw new ReadError("invalid");
  if (typeof raw.license !== "string" || !/^CC BY-SA (?:2\.5|3\.0|4\.0)$/.test(raw.license)) throw new ReadError("invalid");
  candidate.license = raw.license;
  candidate.author = clip(oneLine(typeof raw.author === "string" ? raw.author : ""), 200);
  if (!candidate.author) throw new ReadError("invalid");
  if (raw.authorURL != null) {
    if (typeof raw.authorURL !== "string") throw new ReadError("invalid");
    const link = new URL(raw.authorURL);
    if (link.protocol !== "https:" || link.hostname !== sites[site].host || !/^\/users\/[1-9][0-9]*(?:\/|$)/.test(link.pathname) || link.username || link.password || link.port) throw new ReadError("invalid");
    candidate.authorURL = link.href;
  }
  if (raw.acceptedAnswerID != null) candidate.acceptedAnswerID = questionID(raw.acceptedAnswerID);
  return candidate;
}
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 16_000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), site: siteID(value.site), tag: tagText(value.tag), found: value.found, question: questionState(value.question, siteID(value.site)) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function questionTransition(state: PageState, results: SearchResult, id: string): PageState {
  if (!id.startsWith("question:")) return state;
  let idNumber: number;
  try { idNumber = questionID(id.slice("question:".length)); } catch { return state; }
  const question = results.questions.find(candidate => candidate.id === idNumber);
  return question ? { q: state.q, site: state.site, tag: state.tag, found: true, question } : state;
}
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  let fields: Readonly<Record<string, unknown>>;
  if (typeof values === "string") { try { values = JSON.parse(values); } catch { return state; } }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields = values as Readonly<Record<string, unknown>>;
  if (id === "search") {
    try {
      const next = { q: queryText(fields.query), site: siteID(fields.site ?? state.site), tag: tagText(fields.tag ?? ""), found: true };
      return next.q === state.q && next.site === state.site && next.tag === state.tag && state.found ? { ...next, question: state.question } : next;
    } catch { return state; }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, site: state.site, tag: state.tag, found: true };
  return state;
}
export async function loadPage(service: StackExchange, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (!state.question) return { kind: "results", result: await service.search(state.q, state.site, state.tag, 12, refresh, request.signal) };
    if (state.question.answerCount === 0) return { kind: "no-answer" };
    const result = await service.answers(state.question.id, state.site, refresh, request.signal), answer = selectedAnswer(result.answers);
    return { kind: "answer", question: state.question, body: answer.body, accepted: answer.accepted, id: answer.id, url: answer.url, author: answer.author, license: answer.license, stale: result.stale, quotaRemaining: result.quotaRemaining };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: StackExchange; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), siteName = sites[state.site].name;
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "answers", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value || t.unknown, 2800)} style={style} />;
  const search = <Section key="stackexchange-search" title={t.search}>
    <Field key="stackexchange-query" name="query" label={t.search} kind="text" placeholder={t.searchHint} value={state.q} />
    <Field key="stackexchange-site" name="site" label={t.site} kind="choice" value={state.site} choices={siteIDs.map(id => ({ value: id, title: sites[id].name }))} />
    <Field key="stackexchange-tag" name="tag" label={t.tag} kind="text" placeholder={t.tagHint} value={state.tag} />
    <Button key="stackexchange-submit" title={t.searchAction} action={act("search")} />
  </Section>;
  const attribution = <Section key="stackexchange-attribution">{text("stackexchange-license", t.attribution, "caption")}</Section>;
  if (loaded.kind === "loading") return <View key="stackexchange" title={t.search} state={encodeState(state)}>{search}<Section key="stackexchange-loading"><Loading key="stackexchange-loading-indicator" title={t.loading} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="stackexchange" title={t.search} state={encodeState(state)}>{search}<Section key="stackexchange-error">{text("stackexchange-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="stackexchange-retry" title={t.retry} action={act("refresh")} /></Section>{attribution}</View>;
  if (loaded.kind === "empty") return <View key="stackexchange" title={t.search} state={encodeState(state)}>{search}<Section key="stackexchange-welcome">{text("stackexchange-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  const footer = <Section key="stackexchange-footer"><Button key="stackexchange-refresh" title={t.retry} action={act("refresh")} />{state.question ? <Button key="stackexchange-back" title={t.back} action={act("back")} /> : null}<Button key="stackexchange-home" title={t.home} action={act("home")} /></Section>;
  if (loaded.kind === "no-answer") return <View key="stackexchange" title={state.question?.title ?? t.title} state={encodeState(state)}>{search}<Section key="stackexchange-no-answer">{text("stackexchange-no-answer-text", t.noAnswer, "body")}{state.question ? <Link key="stackexchange-question-link" title={fmt(t.openQuestion, { site: siteName })} url={state.question.url} /> : null}</Section>{footer}{attribution}</View>;
  if (loaded.kind === "answer") return <View key="stackexchange" title={loaded.question.title} state={encodeState(state)}>
    <Section key="stackexchange-answer-heading" title={loaded.accepted ? t.accepted : t.highest} footer={[fmt(t.score, { count: loaded.question.score }), fmt(t.answers, { count: loaded.question.answerCount })].join(" · ")}>
      <Markdown key="stackexchange-answer-body" text={loaded.body} />
      {text("stackexchange-answer-author", fmt(t.by, { author: loaded.author, license: loaded.license }), "caption")}
      <Link key="stackexchange-answer-link" title={fmt(t.openAnswer, { site: siteName })} url={loaded.url} />
    </Section>
    <Section key="stackexchange-question">{text("stackexchange-question-title", loaded.question.title, "body")}<Link key="stackexchange-question-link" title={fmt(t.openQuestion, { site: siteName })} url={loaded.question.url} />{text("stackexchange-question-tags", loaded.question.tags.map(tag => "#" + tag).join(" · "), "caption")}</Section>
    {loaded.stale ? <Section key="stackexchange-stale">{text("stackexchange-stale-text", errorText(loaded.stale, props.locale ?? service.locale))}</Section> : null}
    {loaded.quotaRemaining !== undefined ? <Section key="stackexchange-quota">{text("stackexchange-quota-text", fmt(t.quota, { count: loaded.quotaRemaining }), "caption")}</Section> : null}
    {footer}{attribution}
  </View>;
  return <View key="stackexchange" title={`${t.title}: ${clip(state.q, 100)}`} state={encodeState(state)}>{search}
    {loaded.result.questions.length ? loaded.result.questions.map(question => <Section key={`question-${question.id}`} title={question.title} footer={[fmt(t.score, { count: question.score }), fmt(t.answers, { count: question.answerCount }), question.tags.map(tag => "#" + tag).join(" · ")].filter(Boolean).join(" · ")}>
      <Button key={`open-${question.id}`} title={t.readAnswer} action={act(`question:${question.id}`, { ...state, question }, question.title)} />
      <Link key={`link-${question.id}`} title={fmt(t.openQuestion, { site: siteName })} url={question.url} />
    </Section>) : <Section key="stackexchange-empty-results">{text("stackexchange-empty", t.empty)}</Section>}
    {loaded.result.stale ? <Section key="stackexchange-stale">{text("stackexchange-stale-text", errorText(loaded.result.stale, props.locale ?? service.locale))}</Section> : null}
    {loaded.result.quotaRemaining !== undefined ? <Section key="stackexchange-quota">{text("stackexchange-quota-text", fmt(t.quota, { count: loaded.result.quotaRemaining }), "caption")}</Section> : null}
    {footer}{attribution}
  </View>;
}
