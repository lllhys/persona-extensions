import { Button, Divider, Link, Loading, Row, Section, Segmented, Text, View, type NativeAction, type CallbackAction, type Node } from "@persona/extension/ui";
import { chunks, clip, filter, isList, lists, oneLine, opening, parseJSON, topicID, topicURL, type List, type Topic } from "./model.ts";
import { date, describe, fmt, meta, textsFor, topicMeta } from "./i18n.ts";
import { postText, plainText } from "./text.ts";
import { bestReplies } from "./topic.ts";
import { LinuxDo, errorText, type Listing, type Thread } from "./service.ts";
export interface PageState { l: List; t: string; a: boolean; p?: number }
const topicsPerPage = 270;
export type Loaded = { kind: "loading" } | { kind: "list"; listing: Listing } | { kind: "topic"; thread: Thread } | { kind: "error"; error: unknown };
export interface PageProps { service: LinuxDo; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; tz?: string; action?: (id: string) => NativeAction | CallbackAction }
export function readState(raw: unknown, service: LinuxDo): PageState {
  let value: Record<string, unknown> = {};
  try { const data = typeof raw === "string" ? parseJSON(raw) : raw; if (data && typeof data === "object" && !Array.isArray(data)) value = data as Record<string, unknown>; } catch { /* Invalid saved state uses defaults. */ }
  let t = "0"; try { t = topicID(value.t); } catch { /* Invalid navigation does not initiate a topic read. */ }
  return { l: isList(value.l) ? value.l : service.config.list, t, a: value.a === true,
    ...(typeof value.p === "number" && Number.isSafeInteger(value.p) && value.p > 0 ? { p: value.p } : {}) };
}
export function transition(state: PageState, action: string, values: Readonly<Record<string, string>> = {}): PageState {
  const [kind, arg] = action.split(":");
  const { p: _page, ...first } = state;
  if (kind === "list" && isList(arg || values.list)) return { ...first, l: (arg || values.list) as List, t: "0" };
  if (kind === "topic") { try { const id = topicID(arg); if (id !== "0") return { ...state, t: id }; } catch { /* Ignore invalid callback values. */ } }
  if (kind === "back") return { ...state, t: "0" };
  if (kind === "all") return { ...first, a: !state.a };
  if (kind === "page" && /^\d+$/u.test(arg ?? "")) {
    const p = Number(arg);
    if (Number.isSafeInteger(p)) return p > 0 ? { ...state, p } : first;
  }
  return state;
}
export async function loadPage(service: LinuxDo, state: PageState, refresh = false, signal?: AbortSignal): Promise<Loaded> {
  try { return state.t !== "0" ? { kind: "topic", thread: await service.thread(state.t, signal) } : { kind: "list", listing: await service.listing(state.l, refresh, signal) }; }
  catch (error) { signal?.throwIfAborted(); return { kind: "error", error }; }
}
/** Exact original opening budget: separate code segments, at most 200 components and 12000 runes. */
export function openingParts(topic: Topic, locale: string): { parts: { code: boolean; text: string }[]; more: boolean } {
  const op = opening(topic), parts: { code: boolean; text: string }[] = []; let left = 12000, room = 200, more = false;
  if (op) for (const segment of postText(op.cooked, textsFor(locale))) {
    if (left <= 0) { more = true; break; }
    const n = Array.from(segment.text).length, cs = chunks(clip(segment.text, left));
    if (cs.length > room) { more = true; break; }
    room -= cs.length; parts.push(...cs.map(text => ({ code: segment.code, text })));
    if (n > left) { more = true; break; } left -= n;
  }
  return { parts, more };
}
/** Native, schema-token-only JSX, no browser page or client network. */
export function page(props: PageProps): Node {
  const { service, state, loaded } = props, locale = props.locale ?? service.locale, uiVersion = props.uiVersion ?? 1, t = textsFor(locale), zone = service.zone(props.tz);
  const act = (id: string) => props.action?.(id) ?? { id };
  const text = (key: string, content: string, style: "caption" | "body" | "secondary" | "code" = "secondary") => <Text key={key} text={clip(content, 1800)} style={style} />;
  const picker = <Section key="picker" title={uiVersion < 2 ? t.pick : undefined}>
    {uiVersion >= 2 ? <Segmented key="list" name="list" value={state.l} choices={Object.keys(lists).map(id => ({ value: id, title: t.short[id as List] }))} action={act("list")} />
      : Object.keys(lists).map(id => <Row key={`list-${id}`} title={t.lists[id as List]} badge={id === state.l ? { text: t.viewing, tone: "positive" } : undefined} action={act(`list:${id}`)} />)}
  </Section>;
  const topicPage = state.t !== "0", encoded = JSON.stringify(state);
  const tail = <Section key="tail" footer={t.textOnly}><Link key="site" title={t.open} url={topicURL(state.t)} /><Button key="back" title={t.back} action={act("back")} /></Section>;
  if (loaded.kind === "loading") return <View key="page" title="LINUX DO" state={encoded}>{!topicPage ? picker : null}<Section key="loading-section"><Loading key="loading" title={t.loading} />{topicPage ? <Button key="back" title={t.back} action={act("back")} /> : null}</Section></View>;
  if (loaded.kind === "error") return <View key="page" title="LINUX DO" state={encoded}>{!topicPage ? picker : null}<Section key="error">{text("failure", errorText(loaded.error, locale))}{!topicPage ? <Button key="retry" title={t.retry} action={act("refresh")} /> : null}</Section>{topicPage ? tail : null}</View>;
  if (topicPage && loaded.kind === "topic") {
    const topic = loaded.thread.value, body = openingParts(topic, locale), replies = bestReplies(topic, 10);
    return <View key="page" title={clip(oneLine(topic.title) || "LINUX DO", 200)} state={encoded}>
      <Section key="opening">{text("meta", topicMeta(t, topic, zone), "caption")}
        {loaded.thread.stale ? text("stale", fmt(t.stale, errorText(loaded.thread.stale, locale))) : null}
        {body.parts.map((part, i) => text(`body-${i}`, part.text, part.code ? "code" : "body"))}{body.more ? text("more", t.more, "caption") : null}
      </Section>
      {replies.length ? <Section key="replies" title={t.replies} footer={fmt(t.repliesFoot, topic.posts.length, replies.length, topic.postsCount)}>
        {replies.map((reply, i) => [i ? <Divider key={`divider-${reply.id}-${reply.number}`} /> : null,
          text(`reply-meta-${reply.id}-${reply.number}`, [`#${reply.number} ${reply.username}`, date(reply.at, zone), BigInt(reply.likes) > 0n ? fmt(t.likes, reply.likes) : ""].filter(Boolean).join(" · "), "caption"),
          text(`reply-${reply.id}-${reply.number}`, clip(plainText(reply.cooked, t), 1200), "body")])}
      </Section> : null}{tail}
    </View>;
  }
  if (loaded.kind !== "list") throw new Error("list page needs a listing");
  const reading = loaded.listing, topics = reading.value, shown = state.a ? topics : filter(topics, service.config), description = describe(t, service.config);
  const title = description ? (state.a ? fmt(t.countAll, t.lists[state.l], topics.length) : fmt(t.countOf, t.lists[state.l], shown.length, topics.length)) : fmt(t.count, t.lists[state.l], topics.length);
  const pageNumber = Math.min(state.p ?? 0, Math.max(0, Math.ceil(shown.length / topicsPerPage) - 1));
  const start = pageNumber * topicsPerPage;
  return <View key="page" title="LINUX DO" state={encoded}>{picker}
    {reading.stale ? <Section key="stale">{text("stale-note", fmt(t.stale, errorText(reading.stale, locale)))}</Section> : null}
    <Section key="topics" title={title} footer={clip(description ? state.a ? t.unfiltered : fmt(t.interests, description) : t.configure, 1800)}>
      {shown.slice(start, start + topicsPerPage).map(topic => <Row key={`topic-${topic.id}`} title={clip(topic.title || topic.id, 300)} subtitle={clip(meta(t, topic, service.now()), 500)} action={act(`topic:${topic.id}`)} />)}
      {!shown.length ? text("empty", topics.length ? t.filteredEmpty : t.empty) : null}
      {pageNumber > 0 ? <Button key="previous-page" title={t.previousPage} action={act(`page:${pageNumber - 1}`)} /> : null}
      {start + topicsPerPage < shown.length ? <Button key="next-page" title={t.nextPage} action={act(`page:${pageNumber + 1}`)} /> : null}
      {description && (state.a || shown.length < topics.length) ? <Row key="all" title={state.a ? t.filterAgain : fmt(t.showAll, topics.length)} action={act("all")} /> : null}
    </Section><Section key="read-time">{text("reading", fmt(t.read, date(reading.at, zone, false)), "caption")}<Button key="refresh" title={t.refresh} action={act("refresh")} /></Section>
  </View>;
}
