import { open } from "@persona/extension";
import { Button, Divider, Link, Loading, Row, Section, Segmented, Text, View, type NativeAction, type CallbackAction, type Node } from "@persona/extension/ui";
import { active, chunks, clip, feeds, filter, gone, isFeed, itemURL, oneLine, plain, safeLink, type Feed, type Item } from "./model.ts";
import { ago, describe, fmt, meta, textsFor } from "./i18n.ts";
import { errorText, HackerNews, type Listing, type Thread } from "./service.ts";
export interface PageState { l: Feed; s: number; a: boolean }
export type Loaded = { kind: "list"; listing: Listing } | { kind: "story"; thread: Thread } | { kind: "error"; error: unknown } | { kind: "loading" };
type Action = NativeAction | CallbackAction;
export interface PageProps {
  service: HackerNews; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number;
  action?: (id: string, next?: PageState) => Action;
}
export function readState(raw: unknown, service: HackerNews): PageState {
  let value: Record<string, unknown> = {};
  try { const data = typeof raw === "string" ? JSON.parse(raw) : raw;
    if (data && typeof data === "object" && !Array.isArray(data)) value = data;
  } catch { /* Invalid legacy state uses the configured feed. */ }
  return { l: isFeed(value.l) ? value.l : service.config.list as Feed,
    s: Number.isSafeInteger(value.s) && (value.s as number) > 0 ? value.s as number : 0, a: value.a === true };
}
export async function loadPage(service: HackerNews, state: PageState, refresh = false, signal?: AbortSignal): Promise<Loaded> {
  try { return state.s ? { kind: "story", thread: await service.thread(state.s, 15, refresh, signal) }
    : { kind: "list", listing: await service.stories(state.l, 30, refresh, signal) }; }
  catch (error) { signal?.throwIfAborted(); return { kind: "error", error }; }
}
/** Pure schema-derived JSX factory. Native action IDs preserve the old snapshot path. */
export function page(props: PageProps): Node {
  const { service, state, loaded, locale = "", uiVersion = 3 } = props;
  const t = textsFor(locale), now = service.now();
  const act = (id: string, next?: PageState, title?: string): Action => props.action?.(id, next) ?? (next ? open(id, "", next, title) : { id });
  const bodyText = (key: string, content: string, style: "body" | "secondary" | "caption" = "secondary") => <Text key={key} text={clip(content, 1800)} style={style} />;
  const picker = <Section key="picker" title={uiVersion < 2 ? t.pick : undefined}>
    {uiVersion >= 2 ? <Segmented key="feed" name="list" value={state.l} choices={Object.keys(feeds).map(id => ({ value: id, title: t.lists[id as Feed] }))} action={act("list")} />
      : Object.keys(feeds).map(id => <Row key={`feed-${id}`} title={t.lists[id as Feed]} action={act(`list:${id}`)} badge={id === state.l ? { text: t.viewing, tone: "positive" } : undefined} />)}
  </Section>;
  if (loaded.kind === "loading") return <View key="page" title="Hacker News" state={JSON.stringify(state)}>
    {!state.s ? picker : null}<Section key="loading-section"><Loading key="loading" title={t.loading} />
      {state.s && props.action ? <Button key="back" title={t.back} action={act("back")} /> : null}
    </Section></View>;
  if (state.s) {
    const story = loaded.kind === "story" ? loaded.thread.story : undefined;
    const tail = <Section key="tail" footer={t.textOnly}>
      {story && !gone(story) && safeLink(story) ? <Link key="article" title={t.openArticle} url={safeLink(story)} /> : null}
      <Link key="site" title={t.openSite} url={itemURL(state.s)} />
      <Button key="refresh" title={t.refresh} action={act("refresh")} />
      {uiVersion < 3 || props.action ? <Button key="back" title={t.back} action={act("back")} /> : null}
    </Section>;
    if (loaded.kind === "error") return <View key="page" title="Hacker News" state={JSON.stringify(state)}><Section key="error-section">{bodyText("error", fmt(t.failed, errorText(loaded.error, locale)))}</Section>{tail}</View>;
    if (loaded.kind !== "story" || !story) throw new Error("story page needs a thread");
    if (gone(story)) return <View key="page" title="Hacker News" state={JSON.stringify(state)}><Section key="gone-section">{bodyText("gone", t.gone)}</Section>{tail}</View>;
    const { comments, missed } = loaded.thread;
    return <View key="page" title={clip(story.title, 200)} state={JSON.stringify(state)}>
      <Section key="story-head">{bodyText("story-meta", meta(t, story, now), "caption")}
        {chunks(clip(plain(story.text), 12000)).map((part, i) => bodyText(`body-${i}`, part, "body"))}
      </Section>
      <Section key="comments" title={t.comments} footer={comments.length ? fmt(t.commentsFoot, comments.length, story.descendants) : undefined}>
        {missed ? bodyText("comments-missed", fmt(t.commentsMissed, missed)) : null}
        {comments.map((c, i) => [i ? <Divider key={`divider-${c.id}`} /> : null,
          bodyText(`comment-meta-${c.id}`, `${c.by} · ${ago(t, now - c.time * 1000)}`, "caption"),
          c.text ? bodyText(`comment-${c.id}`, clip(plain(c.text), 1500), "body") : null])}
        {!comments.length && !missed ? bodyText("no-comments", t.noComments) : null}
      </Section>{tail}
    </View>;
  }
  if (loaded.kind === "error") return <View key="page" title="Hacker News" state={JSON.stringify(state)}>{picker}<Section key="error-section" title={t.lists[state.l]}>
    {bodyText("error", fmt(t.failed, errorText(loaded.error, locale)))}<Button key="retry" title={t.retry} action={act("refresh")} />
  </Section></View>;
  if (loaded.kind !== "list") throw new Error("list page needs a listing");
  const l = loaded.listing, f = service.config, description = describe(t, f), shown = state.a ? l.stories : filter(l.stories, f);
  const title = description ? (state.a ? fmt(t.countAll, t.lists[state.l], l.stories.length) : fmt(t.countOf, t.lists[state.l], shown.length, l.stories.length)) : fmt(t.count, t.lists[state.l], l.stories.length);
  return <View key="page" title="Hacker News" state={JSON.stringify(state)}>{picker}
    {l.stale || l.missed ? <Section key="notes">
      {l.stale ? bodyText("stale", fmt(t.stale, errorText(l.stale, locale), ago(t, now - l.at))) : null}
      {l.missed ? bodyText("missed", fmt(t.missed, l.missed)) : null}
    </Section> : null}
    <Section key="stories" title={title} footer={description ? (state.a ? t.unfiltered : clip(fmt(t.byInterests, description), 1800)) : undefined}>
      {shown.map(it => <Row key={`story-${it.id}`} title={clip(it.title, 300)} subtitle={clip(meta(t, it, now), 300)} action={act(`story:${it.id}`, { ...state, s: it.id }, clip(it.title, 200))} />)}
      {!shown.length ? bodyText("empty", t.nothing) : null}
      {active(f) && (state.a || shown.length < l.stories.length) ? <Row key="all" title={state.a ? t.filterAgain : fmt(t.showAll, l.stories.length)} action={act("all")} /> : null}
    </Section>
    <Section key="read-time">{bodyText("read-ago", fmt(t.readAgo, ago(t, now - l.at)), "caption")}<Button key="refresh" title={t.refresh} action={act("refresh")} /></Section>
  </View>;
}
export function transition(state: PageState, id: string, values: Readonly<Record<string, string>> = {}): PageState {
  const [kind, arg] = id.split(":");
  if (kind === "list" && isFeed(arg || values.list)) return { ...state, l: (arg || values.list) as Feed, s: 0 };
  if (kind === "story" && Number.isSafeInteger(Number(arg)) && Number(arg) > 0) return { ...state, s: Number(arg) };
  if (kind === "back") return { ...state, s: 0 };
  if (kind === "all") return { ...state, a: !state.a };
  return state;
}
