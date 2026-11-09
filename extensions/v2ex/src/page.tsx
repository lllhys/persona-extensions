import { Button, Divider, Link, Loading, Renderer, Row, Section, Segmented, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { chunks, clip, decodeStruct, filter, latestReplies, length, parseJSON, ReadError, tidy, topicID, topicURL, type Listing, type Thread, type Topic } from "./model.ts";
import { describe, errorText, fmt, meta, missedText, textsFor, time, title, topicMeta } from "./i18n.ts";
import { budget, checkSignal, type V2EX } from "./service.ts";
export interface PageState { l: "hot" | "latest" | "nodes"; t: string | 0; a: boolean; p?: number }
export type Loaded = { kind: "loading" } | { kind: "list"; listing: Listing } | { kind: "topic"; thread: Thread } | { kind: "error"; error: unknown };
export interface PageProps { service: V2EX; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function encodeState(state: PageState): string {
  // Emit an exact JSON integer even beyond Number's range. Never round-trip it through JSON.parse.
  const token = state.t ? topicID(state.t) : "0";
  return JSON.stringify({ ...state, t: 0 }).replace('"t":0', `"t":${token}`);
}
export function readState(raw: unknown, service: V2EX): PageState {
  let v: ReturnType<typeof decodeStruct>["value"] = {};
  try {
    const parsed = typeof raw === "string" ? parseJSON(raw) : raw;
    const decoded = decodeStruct(parsed, { l: "string", t: "integer", a: "boolean", p: "integer" });
    if (!decoded.errors) v = decoded.value;
  } catch { /* Invalid state falls back as a whole, like Go DecodeState. */ }
  let topic: string | 0 = 0; try { topic = topicID(v.t); } catch { /* No positive topic means a list. */ }
  return { l: service.offers(v.l) ? v.l : service.offers(service.config.list) ? service.config.list : "hot", t: topic, a: v.a === true,
    ...(typeof v.p === "number" && Number.isSafeInteger(v.p) && v.p > 0 ? { p: v.p } : {}) };
}
export function transition(state: PageState, id: string, service: V2EX, values: Readonly<Record<string, string>> = {}): PageState {
  const [kind, arg] = id.split(":");
  const { p: _page, ...first } = state;
  if (kind === "batch" && /^\d+$/.test(arg ?? "") && Number.isSafeInteger(Number(arg))) return { ...state, p: Number(arg) };
  if (kind === "list" && service.offers(arg || values.list)) return { ...first, l: (arg || values.list) as PageState["l"], t: 0 };
  if (kind === "topic") { try { return { ...state, t: topicID(arg) }; } catch { return state; } }
  if (kind === "back") return { ...state, t: 0 };
  if (kind === "all") return { ...first, a: !state.a };
  return state;
}
export async function loadPage(service: V2EX, state: PageState, refresh = false, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try { return state.t ? { kind: "topic", thread: await service.thread(state.t, true, refresh, request.signal) } : { kind: "list", listing: await service.list(state.l, refresh, request.signal) }; }
  catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
/** The same native JSX tree serves owner-bound sessions and stateless full-view snapshots. */
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, locale = props.locale ?? service.locale, t = textsFor(locale), tz = service.zoneFor(props.tz);
  const act = (id: string, next?: PageState, heading?: string) => props.action?.(id, next, heading)
    ?? (next && uiVersion >= 3 ? { id, push: { ui: "topics", state: encodeState(next), ...(heading ? { title: heading } : {}) } } : { id });
  const text = (key: string, content: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(content, 1900)} style={style} />;
  const lists = (["hot", "latest", "nodes"] as const).filter(id => service.offers(id));
  const picker = <Section key="picker" title={uiVersion < 2 ? t.pick : undefined}>
    {uiVersion >= 2 ? <Segmented key="list" name="list" value={state.l} choices={lists.map(id => ({ value: id, title: t[id] }))} action={act("list")} />
      : lists.map(id => <Row key={`list-${id}`} title={t[id]} action={act(`list:${id}`)} badge={id === state.l ? { text: t.viewing, tone: "positive" } : undefined} />)}
  </Section>;
  const badNodes = service.badNodes.length ? <Section key="bad-nodes">{text("bad-nodes-note", fmt(t.badNodes, service.badNodes.join("、")))}</Section> : null;
  const tail = state.t ? <Section key="tail" footer={t.webFoot}>
    <Link key="site" title={t.openSite} url={topicURL(state.t)} /><Button key="refresh" title={t.refresh} action={act("refresh")} />
    {uiVersion < 3 ? <Button key="back" title={t.back} action={act("back")} /> : null}
  </Section> : null;
  if (loaded.kind === "loading") return <View key="page" title="V2EX" state={encodeState(state)}>
    {!state.t ? [picker, badNodes] : null}<Section key="loading-section"><Loading key="loading" title={t.loading} /></Section>{tail}</View>;
  if (loaded.kind === "error") return <View key="page" title="V2EX" state={encodeState(state)}>
    {!state.t ? [picker, badNodes] : null}<Section key="error-section" title={!state.t ? t[state.l] : undefined}>
      {text("error", errorText(loaded.error, locale))}{!state.t ? <Button key="retry" title={t.retry} action={act("refresh")} /> : null}
    </Section>{tail}</View>;
  if (state.t) {
    if (loaded.kind !== "topic") throw new ReadError("invalid");
    const thread = loaded.thread, topic = thread.topic, body = tidy(topic.content), latest = latestReplies(thread.replies, 20);
    return <View key="page" title={clip(title(t, topic), 200)} state={encodeState(state)}>
      <Section key="topic-head">{text("topic-meta", topicMeta(t, topic, tz), "caption")}
        {thread.stale ? text("stale", fmt(t.stale, errorText(thread.stale, locale), time(thread.at, tz))) : null}
        {chunks(clip(body, 12000)).map((part, i) => text(`body-${topic.id}-${i}`, part, "body"))}
        {length(body) > 12000 ? text("body-long", t.bodyLong, "caption") : null}
      </Section>
      {thread.repliesErr ? <Section key="replies" title={t.replies}>{text("replies-error", fmt(t.repliesMissed, errorText(thread.repliesErr, locale)))}</Section>
        : latest.length ? <Section key="replies" title={t.latestReplies} footer={fmt(t.repliesFoot, latest.length, thread.replies.length)}>
          {latest.map(({ reply, floor }, i) => [i ? <Divider key={`divider-${reply.id}-${floor}`} /> : null,
            text(`reply-meta-${reply.id}-${floor}`, `#${floor} ${reply.author} · ${time(BigInt(reply.created) * 1000n, tz)}`, "caption"),
            tidy(reply.content) ? text(`reply-${reply.id}-${floor}`, clip(tidy(reply.content), 1200), "body") : null])}
        </Section> : null}{tail}
    </View>;
  }
  if (loaded.kind !== "list") throw new ReadError("invalid");
  const listing = loaded.listing, description = describe(t, service.config), shown = state.a ? listing.topics : filter(listing.topics, service.config);
  // Duplicate API IDs must not create duplicate native siblings or callback keys.
  const seen = new Set<string>(), unique = shown.filter(topic => !seen.has(topic.id) && !!seen.add(topic.id));
  const heading = description ? state.a ? fmt(t.countAll, t[state.l], listing.topics.length) : fmt(t.countOf, t[state.l], shown.length, listing.topics.length) : fmt(t.count, t[state.l], listing.topics.length);
  const now = service.now();
  const display = new Map(unique.map(topic => [topic.id, { title: clip(title(t, topic), 300), subtitle: clip(meta(t, topic, now), 1900), heading: clip(title(t, topic), 200) }]));
  const buildList = (topics: Topic[], pagination?: { index: number; count: number }) => <View key="page" title="V2EX" state={encodeState(pagination ? { ...state, p: pagination.index } : state)}>{picker}{badNodes}
    {listing.stale || listing.missed.length ? <Section key="notes">
      {listing.stale ? text("stale", fmt(t.stale, errorText(listing.stale, locale), time(listing.at, tz))) : null}
      {listing.missed.length ? text("missed", fmt(t.missed, missedText(listing.missed, locale))) : null}
    </Section> : null}
    <Section key="topics" title={heading} footer={description ? state.a ? t.unfiltered : clip(fmt(t.byInterests, description), 1900) : undefined}>
      {topics.map(topic => <Row key={`topic-${topic.id}`} title={display.get(topic.id)!.title} subtitle={display.get(topic.id)!.subtitle}
        action={BigInt(topic.id) > 0n ? act(`topic:${topic.id}`, { ...state, t: topic.id }, display.get(topic.id)!.heading) : undefined} />)}
      {!shown.length ? text("empty", listing.topics.length && description && !state.a ? t.filteredEmpty : t.nothing) : null}
      {description && (state.a || shown.length < listing.topics.length) ? <Row key="all" title={state.a ? t.filterAgain : fmt(t.showAll, listing.topics.length)} action={act("all")} /> : null}
    </Section>
    {pagination ? <Section key="pagination" title={fmt(t.batchCount, pagination.index + 1, pagination.count)}>
      {pagination.index > 0 ? <Button key="previous-batch" title={t.previousBatch} action={act(`batch:${pagination.index - 1}`)} /> : null}
      {pagination.index + 1 < pagination.count ? <Button key="next-batch" title={t.nextBatch} action={act(`batch:${pagination.index + 1}`)} /> : null}
    </Section> : null}
    <Section key="read-time">{listing.at || state.l !== "nodes" || listing.missed.length < service.nodes.length ? text("read-at", fmt(t.readAt, time(listing.at, tz, false)), "caption") : null}<Button key="refresh" title={t.refresh} action={act("refresh")} /></Section>
  </View>;
  const full = buildList(unique);
  if (fitsNativeFrame(full)) return full;
  // Only genuinely over-budget lists get navigation. Probe a separate pure tree,
  // never the owner-bound renderer (a failed render disposes that session).
  const groups: Topic[][] = [];
  const partition = (topics: Topic[]) => {
    if (fitsNativeFrame(buildList(topics, { index: unique.length, count: unique.length + 2 }))) { groups.push(topics); return; }
    if (topics.length <= 1) throw new Error("one bounded topic cannot fit the native frame");
    const middle = Math.floor(topics.length / 2); partition(topics.slice(0, middle)); partition(topics.slice(middle));
  };
  partition(unique);
  const index = Math.min(state.p ?? 0, groups.length - 1);
  return buildList(groups[index], { index, count: groups.length });
}
function fitsNativeFrame(tree: Node): boolean {
  const probe = new Renderer({ authorize: () => false });
  try {
    const snapshot = probe.mount(tree);
    // Measure the actual envelope at its largest supported revision, not an
    // arbitrary lower byte limit. A resumed session may have more version digits.
    return new TextEncoder().encode(JSON.stringify({ ...snapshot, version: Number.MAX_SAFE_INTEGER })).byteLength <= 1048576;
  } catch (error) {
    if (error instanceof Error && /^UI (frame|node|prop|callback) limit exceeded/.test(error.message)) return false;
    throw error;
  } finally { probe.dispose(); }
}
