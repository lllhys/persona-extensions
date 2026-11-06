import { View, Section, Text, Row, Button, Field, Empty, Loading, Metric, Chart, StatGrid, Segmented, Markdown, Progress, callback, useEffect, useState, Renderer } from "@persona/extension/ui";
import type { ActionResult, ChartSeries, Stat, View as NativeView } from "@persona/extension/wire";
import type { ActionOptions, Values, Node } from "@persona/extension/ui";
import { Examples, catalog, catalogIntroduction } from "./examples.tsx";
import { translator, type Translate } from "./locale.ts";
import { ShowcaseStore, displayed, pageSize, itemText, kindChoices, summarize, runShortcut, type PageState } from "./services.ts";

export interface PageInput { state?: PageState; language?: string; uiVersion?: number }
const encode = (state: PageState) => JSON.stringify(state);
const listState = (state: PageState): PageState => ({ ...state, p: "list", id: undefined });
export const days = ["10-30", "10-31", "11-01", "11-02", "11-03", "11-04", "11-05"];
export function usage(t: Translate): ChartSeries {
  return { name: t("usage"), points: [3, 5, 2, 8, 6, 4, 7].map((value, index) => ({ label: days[index], value })) };
}
/** Root/page factory only. The committed manager bridge will own mounting, authorization and native navigation. */
export function createPage(store: ShowcaseStore, input: PageInput = {}): Node {
  return <ShowcaseRoot key="showcase-root" store={store} input={input} />;
}
export function ShowcaseRoot({ store, input }: { store: ShowcaseStore; input: PageInput }) {
  const t = translator(input.language);
  const old = (input.uiVersion ?? 3) < 3;
  const [state, setState] = useState<PageState>(() => input.state ?? {});
  const [, revision] = useState(0);
  useEffect(() => store.subscribe(() => revision(value => value + 1)), [store]);
  const change = (update: Partial<PageState>) => setState(previous => ({ ...previous, ...update }));
  function open(target: PageState, title: string) {
    const push = { state: encode(target), title };
    return callback(() => {
      if (old) { setState(target); return; }
      return { push } satisfies ActionResult;
    }, old ? {} : { push });
  }
  const act = (id: string, options: ActionOptions = {}) => callback((values: Values): ActionResult | void => {
    switch (id) {
      case "home": setState({}); return;
      case "back": setState(listState(state)); return;
      case "toast": return { toast: t("toast") };
      case "delete": return { toast: t("deleted") };
      case "open-url": return { open_url: "https://example.com/" };
      case "submit": return { toast: t("submitted", { values: summarize(values) }) };
      case "range": change({ range: values.range }); return;
      case "answer": change({ answer: values.answer }); return;
      case "search": change({ q: (values.q ?? "").trim(), shown: 0, ...(values.kind !== undefined ? { kind: values.kind } : {}) }); return;
      case "kind": change({ kind: values.kind, shown: 0, ...(values.q !== undefined ? { q: values.q.trim() } : {}) }); return;
      case "more": change({ shown: displayed(state) + pageSize }); return;
      case "ask": store.ask(); return;
      case "save":
        if (!store.save(state.id, values)) return { toast: t("emptyTitle") };
        if (old) { change({ p: "detail" }); return { toast: t("saved") }; }
        return { toast: t("saved"), pop: true };
    }
  }, options);
  const pages = catalog(t);
  const page = pages.find(page => page.id === state.p);
  const item = store.find(state.id);
  const title = (state.p === "detail" || state.p === "edit")
    ? item ? (state.p === "edit" ? t("editTitle", { title: itemText(item, t).title }) : itemText(item, t).title) : t("gone")
    : page?.title ?? t("s170");
  const all = store.matching(state, t);
  const shown = Math.min(displayed(state), all.length);
  const listContext = old ? { q: state.q, kind: state.kind, shown: state.shown } : {};
  const list = state.p === "list";
  return <View key="showcase-view" title={title} state={encode(state)}
    search={list && !old ? { placeholder: t("searchPlaceholder"), value: state.q || undefined, action: act("search") } : undefined}
    filter={list && !old ? { name: "kind", title: t("kind"), value: state.kind || "all", choices: kindChoices(t), action: act("kind") } : undefined}>
    {!page && state.p !== "detail" && state.p !== "edit" && <>
      <Section key="catalog-introduction"><Text key="catalog-intro-text" style="secondary" text={catalogIntroduction(t)} /></Section>
      <Section key="catalog-pages" title={t("components")}>
        {pages.map(page => <Row key={`catalog-${page.id}`} title={page.title} subtitle={page.subtitle} icon={page.icon}
          action={open({ p: page.id }, page.title)} />)}
      </Section>
    </>}
    {page && !["list", "segmented", "invalidate"].includes(page.id) && <Examples key={`examples-${page.id}`} page={page.id} t={t} act={act} />}
    {list && <>
      {old && <Section key="list-tools">
        <Field key="list-q" name="q" label={t("search")} kind="text" value={state.q ?? ""} placeholder={t("searchPlaceholder")} />
        <Field key="list-kind" name="kind" label={t("kind")} kind="choice" value={state.kind || "all"} choices={kindChoices(t)} />
        <Button key="list-search" title={t("search")} action={act("search", { submit: true })} />
      </Section>}
      <Section key="list-items" title={t("count", { total: all.length, shown })}
        more={!old && shown < all.length ? { action: act("more") } : undefined}>
        {all.slice(0, shown).map(item => <Row key={`item-${item.id}`} title={itemText(item, t).title} subtitle={kindChoices(t).find(choice => choice.value === item.kind)?.title ?? item.kind}
          icon={item.kind} badge={item.done ? { text: t("done"), tone: "positive" } : undefined}
          action={open({ p: "detail", id: item.id, ...listContext }, itemText(item, t).title)} />)}
        {all.length === 0 && <Empty key="list-empty" title={t("noMatch", { q: state.q ?? "" })} icon="search" />}
        {old && shown < all.length && <Button key="list-more" title={t("more")} action={act("more")} />}
      </Section>
    </>}
    {(state.p === "detail" || state.p === "edit") && (!item
      ? <Section key="missing"><Empty key="missing-item" title={t("goneDetail")} /></Section>
      : state.p === "detail" ? <Section key="detail">
        <Markdown key="detail-note" text={`**${itemText(item, t).title}**\n\n${itemText(item, t).note}`} />
        <Row key="detail-kind" title={t("kind")} value={kindChoices(t).find(choice => choice.value === item.kind)?.title ?? item.kind} icon={item.kind} />
        <Row key="detail-done" title={t("done")} value={item.done ? t("finished") : t("notDone")} />
        <Button key="detail-edit" title={t("edit")} action={open({ p: "edit", id: item.id, ...listContext }, t("edit"))} />
        {old && <Button key="detail-back" title={t("back")} action={act("back")} />}
      </Section> : <>
        <Section key="edit-fields">
          <Field key="edit-title" name="title" label={t("s024")} kind="text" value={itemText(item, t).title} />
          <Field key="edit-kind" name="kind" label={t("kind")} kind="choice" value={item.kind} choices={kindChoices(t, false)} />
          <Field key="edit-note" name="note" label={t("noteLabel")} kind="text" value={itemText(item, t).note} />
          <Field key="edit-done" name="done" label={t("done")} kind="toggle" value={String(item.done)} />
        </Section>
        <Section key="edit-actions">
          <Button key="edit-save" title={t("save")} action={act("save", { submit: true })} />
          {old && <Button key="edit-back" title={t("back")} action={act("back")} />}
        </Section>
      </>)}
    {state.p === "segmented" && <Section key="segments">
      <Segmented key="range" name="range" value={state.range || "7d"} action={act("range")}
        choices={[{ value: "1d", title: t("today") }, { value: "7d", title: t("7d") }, { value: "30d", title: t("30d") }]} />
      <Chart key="range-chart" kind="bar" series={[{ ...usage(t), points: usage(t).points.slice(state.range === "1d" ? -1 : -7) }]} detail={t("selected", { range: state.range || "7d" })} />
      <Segmented key="answer" name="answer" value={state.answer || "yes"} action={act("answer")}
        choices={[{ value: "yes", title: t("yes") }, { value: "no", title: t("no") }]} />
    </Section>}
    {state.p === "invalidate" && <Section key="invalidate">
      <Text key="ask-intro" style="secondary" text={t("askIntro")} />
      {store.asked === undefined ? <Empty key="not-asked" title={t("notAsked")} icon="bell" />
        : store.came === undefined ? <Loading key="ask-loading" title={t("onWay")} />
        : <Metric key="ask-data" label={t("arrived")} value={new Date(store.came).toISOString().slice(11, 19)}
          detail={t("waited", { seconds: ((store.came - store.asked) / 1000).toFixed(1) })} tone="positive" />}
      <Button key="ask" title={t("ask")} action={act("ask")} />
    </Section>}
    {old && !!state.p && <Section key="catalog-back"><Button key="home" title={t("home")} action={act("home")} /></Section>}
  </View>;
}
export const componentCount = 15;
function stats(t: Translate): Stat[] {
  return [{ label: t("components"), value: String(componentCount) }, { label: t("pages"), value: String(catalog(t).length) }, { label: t("status"), value: t("healthy"), tone: "positive" }];
}
export function createCard(language = "en"): Node {
  const t = translator(language);
  return <View key="overview"><Section key="overview-stats">
    <StatGrid key="overview-grid" stats={stats(t)} /><Chart key="overview-chart" kind="bar" series={[usage(t)]} />
  </Section></View>;
}
export function createWidget(family: string, language = "en"): Node {
  const t = translator(language), n = catalog(t).length;
  return <View key="widget"><Section key="widget-body">
    {family === "accessory_circular" ? <Progress key="widget-progress" label={t("done")} ratio={0.72} />
      : family === "accessory_rectangular" ? <Metric key="widget-metric" label={t("s170")} value={t("pageCount", { n })} />
      : family === "accessory_inline" ? <Text key="widget-inline" text={t("inline", { n })} />
      : family === "small" ? <>
        <Metric key="widget-metric" label={t("pages")} value={String(n)} detail={t("s170")} tone="positive" />
        <Progress key="widget-progress" label={t("done")} ratio={0.72} />
      </> : <>
        <StatGrid key="widget-grid" stats={stats(t)} />
        {family === "large" && <>
          <Chart key="widget-chart" kind="bar" series={[usage(t)]} />
          <Row key="widget-list" title={t("list")} value={t("itemCount")} />
          <Row key="widget-charts" title={t("s149")} value={t("chartCount")} />
        </>}
      </>}
  </Section></View>;
}
export function createShortcut(method: string, contribution: string, values: Values, language = "en") {
  const result = runShortcut(method, contribution, values, language);
  if (!("count" in result)) return result;
  const t = translator(language);
  return { text: result.text, view: snapshot(<View key="shortcut-result"><Section key="shortcut-body">
    <Metric key="shortcut-count" label={t("countTo")} value={String(result.count)} detail={result.detail} />
  </Section></View>) };
}
/** Only system surfaces use complete snapshots. Interactive mounting belongs to the manager, not this helper. */
export function snapshot(root: Node): NativeView {
  const renderer = new Renderer({ authorize: () => false });
  try { return renderer.mount(root).view; } finally { renderer.dispose(); }
}
