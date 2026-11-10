import { View, Section, Text, Row, Button, Metric, Chart, StatGrid, Segmented, Field, Link, Loading, Empty, type Node, type NativeAction, type CallbackAction } from "@persona/extension/ui";
import { unsupported } from "@persona/extension";
import type { ActionOptions, Values } from "@persona/extension/ui";
import type { Quote, Ref, Candle, SourceID, Alert } from "./model.ts";
import { bars, sourceIDs, sourceBars, benchmarks, parseRef, key, cleanQuery, change, price, priceLike, fmtChange, fmtCN, compact, clip, stamp, tone, near, zones } from "./model.ts";
import { translator, localizeValue } from "./locale.ts";
import { ReadError, isCancelled } from "./http.ts";
import { MutationError } from "./errors.ts";
import type { Markets } from "./service.ts";
export interface PageState { i: string; b: string; q: string; s: string; m: string; o: boolean; f: boolean }
export function readPageState(raw: unknown, service: Markets): PageState { let value: any = raw; if (typeof raw === "string") { try { value = JSON.parse(raw); } catch { value = {}; } } if (!value || typeof value !== "object" || Array.isArray(value)) value = {}; const r = typeof value.i === "string" ? parseRef(value.i) : undefined; const b = typeof value.b === "string" && bars.includes(value.b) ? value.b : ""; return { i: r ? key(r) : "", b: r ? sourceBars[r.source].includes(b) ? b : service.defaultBar(r.source) : b, q: typeof value.q === "string" ? cleanQuery(value.q) : "", s: ["up", "down"].includes(value.s) ? value.s : "", m: sourceIDs.includes(value.m) ? value.m : "", o: value.o === true, f: value.f === true }; }
export interface Loaded { kind: "loading" | "list" | "search" | "instrument" | "card" | "error"; quotes: Map<string, Quote>; errors: Map<SourceID, unknown>; hits: { ref: Ref; quote: Quote }[]; exact?: { ref: Ref; quote: Quote }; candles: Candle[]; candleError?: unknown; error?: unknown; at: number }
export const loading = (): Loaded => ({ kind: "loading", quotes: new Map(), errors: new Map(), hits: [], candles: [], at: 0 });
export async function loadPage(service: Markets, state: PageState, refresh: boolean, card: boolean, uiVersion = 5, signal?: AbortSignal): Promise<Loaded> {
  if (card && service.cfg.overview_card === false) throw unsupported(translator()("cardOff"));
  const loaded = loading(); loaded.at = service.clock(); const fresh = refresh ? 0 : 10_000;
  try {
    if (card) { loaded.kind = "card"; const result = await service.quotes([...benchmarks, ...service.watchlist()], fresh, true, signal); loaded.quotes = result.quotes; loaded.errors = result.errors; }
    else if (state.i) { loaded.kind = "instrument"; const ref = parseRef(state.i)!; loaded.quotes.set(state.i, await service.quote(ref, fresh, signal)); try { loaded.candles = await service.candles(ref, state.b, 60, signal); } catch (err) { if (signal?.aborted || isCancelled(err)) throw err; loaded.candleError = err; } }
    else if (state.q) { loaded.kind = "search"; let searchError: unknown; try { loaded.hits = await service.search(state.q, 30, fresh, signal); } catch (err) { if (signal?.aborted || isCancelled(err)) throw err; searchError = err; }
      const exact = uiVersion >= 3 && parseRef(state.q); if (exact && !loaded.hits.some(h => key(h.ref) === key(exact))) { try { const quote = await service.quote(exact, fresh, signal); loaded.exact = { ref: exact, quote }; } catch (err) { if (signal?.aborted || isCancelled(err)) throw err; } }
      if (searchError && !loaded.hits.length && !loaded.exact) throw searchError;
    } else { loaded.kind = "list"; const refs = [...service.watchlist(), ...(uiVersion >= 2 ? benchmarks : [])], result = await service.quotes(refs, fresh, true, signal); loaded.quotes = result.quotes; loaded.errors = result.errors; }
  } catch (error) { if (signal?.aborted || isCancelled(error)) throw error; loaded.kind = "error"; loaded.error = error; } return loaded;
}
/** Quote/error maps describe keyed data, not provider completion order. Never mutate
 * the shared maps, or invalidate a freshly returned resume callback for equal data. */
export function fingerprint(loaded: Loaded) {
  const byKey = ([a]: [string, unknown], [b]: [string, unknown]) => a < b ? -1 : a > b ? 1 : 0;
  return JSON.stringify({
    kind: loaded.kind,
    quotes: [...loaded.quotes].sort(byKey),
    errors: [...loaded.errors].sort(byKey).map(([k, v]) => [k, v instanceof ReadError ? [v.code, v.status] : "error"]),
    hits: loaded.hits, exact: loaded.exact, candles: loaded.candles,
    candleError: loaded.candleError instanceof ReadError ? loaded.candleError.code : !!loaded.candleError,
    error: loaded.error instanceof ReadError ? loaded.error.code : !!loaded.error,
  }, (_key, value) => typeof value === "bigint" ? value.toString() : value);
}
export async function transition(service: Markets, old: PageState, id: string, values: Values = {}, signal?: AbortSignal): Promise<{ state: PageState; refresh: boolean; toast?: string; symbol?: string }> {
  const state = { ...old }; const i = id.indexOf(":"), action = i < 0 ? id : id.slice(0, i), arg = i < 0 ? "" : id.slice(i + 1), ref = parseRef(arg || values.inst || ""); let toast: string | undefined;
  switch (action) {
    case "open": if (ref) { state.i = key(ref); state.b = sourceBars[ref.source].includes(state.b) ? state.b : service.defaultBar(ref.source); state.f = false; } break;
    case "look": if (!ref) throw new MutationError("invalid", "Invalid code"); state.i = key(ref); state.b = service.defaultBar(ref.source); break;
    case "back": state.i = ""; state.f = false; break;
    case "opts": state.o = !state.o; break;
    case "form": state.f = !state.f; break;
    case "search": state.q = cleanQuery(values.q ?? values.inst ?? ""); state.i = ""; break;
    case "clear": state.q = ""; break;
    case "sort": state.s = ["up", "down"].includes(arg || values.sort) ? arg || values.sort : ""; state.o = false; break;
    case "mkt": state.m = sourceIDs.includes((arg || values.mkt) as SourceID) ? arg || values.mkt : ""; state.o = false; break;
    case "bar": if (bars.includes(arg || values.bar)) state.b = arg || values.bar; break;
    case "add": if (!ref) throw new MutationError("invalid", "Invalid code"); await service.addWatch(ref, signal); toast = "added"; if (state.q && state.i === key(ref)) state.i = ""; break;
    case "remove": if (ref) { await service.removeWatch(ref); toast = "removed"; } break;
    case "up": case "down": case "top": if (ref) await service.moveWatch(ref, action === "top" ? "top" : action === "up" ? -1 : 1); break;
    case "alert": if (ref) { await service.addAlert(ref, values.alert_kind, values.alert_value, signal); state.f = false; toast = "alertAdded"; } break;
    case "unalert": await service.removeAlert(arg); toast = "alertRemoved"; break;
    case "refresh": toast = "refreshed"; break;
  }
  return { state: readPageState(state, service), refresh: action === "refresh", toast, ...(ref ? { symbol: ref.symbol } : {}) };
}
export function sortByChange(refs: string[], quotes: Map<string, Quote>, down = false) { return refs.slice().sort((a, b) => { const ca = change(quotes.get(a)), cb = change(quotes.get(b)); if (ca === undefined || cb === undefined) return ca === cb ? 0 : ca === undefined ? 1 : -1; return down ? ca - cb : cb - ca; }); }
export function matchRank(query: string, source: Markets["sources"][SourceID], ref: Ref, q: Quote) { const name = q.name.toLowerCase(), typed = query.trim().toLowerCase(); return source.normalize(query) === ref.symbol || name && name === typed ? 0 : name && name.startsWith(typed) ? 1 : !name && /\p{Script=Han}/u.test(query) ? 3 : 2; }
export function refreshDelay(service: Markets, state: PageState, loaded: Loaded, card = false): number | null {
  if (loaded.kind === "loading") return null;
  if (loaded.kind === "error" || loaded.kind === "search") return 30_000;
  if (loaded.kind === "instrument") return loaded.quotes.get(state.i)?.idle ? 300_000 : 30_000;
  const watch = service.watchlist();
  const active = card ? !loaded.quotes.size || [...loaded.quotes.values()].some(q => !q.idle)
    : loaded.errors.size > 0 || !watch.length || watch.some(ref => { const q = loaded.quotes.get(ref); return !!q && !q.idle; });
  return active ? card ? 60_000 : 30_000 : 300_000;
}
export interface PageProps { service: Markets; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; tz?: string; card?: boolean; action: (id: string, options?: ActionOptions) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state: st, loaded: l, action: a, card = false } = props, t = translator(props.locale), v = props.uiVersion ?? 5, zone = service.cfg.tz || props.tz || "Local", watch = service.watchlist(), alerts = service.alerts();
  const b = (id: string, title: string, options: ActionOptions & { role?: "destructive" } = {}, uiKey = id) => { const { role, ...metadata } = options; return <Button key={uiKey} title={title} action={a(id, metadata)} role={role} />; };
  const textError = (err?: unknown) => err instanceof ReadError ? err.code === "not_found" ? t("missing") : err.code === "rate_limited" ? t("retry") + " · " + t("error") : t("error") : t("error");
  const alertWhat = (alert: Alert, q?: Quote) => `${t(alert.kind)} ${alert.kind === "move" ? alert.value + "%" : price({ decimals: Math.max(q?.decimals ?? 0, alert.decimals ?? 0) }, alert.value)}`;
  const row = (ref: string, q?: Quote, search = false, action = true, summary = false) => { const r = parseRef(ref)!; const c = change(q); const mark = alerts.find(a => a.ref === ref && a.met) ?? alerts.find(a => a.ref === ref && q && near(a, q)); const subtitle = q ? [mark ? `${t(mark.met ? "met" : "near")}: ${alertWhat(mark, q)}` : "", q.name ? r.symbol : "", search ? t(q.kind) : q.last && q.base ? `${q.last - q.base > 0 ? "+" : ""}${priceLike(q, q.last - q.base, q.last)}` : "", q.turnover > 0 ? `${t("turnover")} ${fmtCN(q.turnover, props.locale)} ${q.currency}` : "", search && watch.includes(ref) ? t("watching") : "", q.idle ? t("idle", { at: stamp(q.at, zones[r.source], "short") }) : ""].filter(Boolean).join(" · ") : t("missing");
    return <Row key={(summary ? "mover-" : "quote-") + ref} title={q?.name || r.symbol} subtitle={summary ? q?.name ? r.symbol : undefined : subtitle} value={q ? price(q, q.last) : undefined} badge={c !== undefined ? { text: fmtChange(c) + (mark?.met ? " · " + t("met") : ""), tone: tone(service.cfg, c) } : !q ? { text: t("noQuote"), tone: "caution" } : undefined} action={action ? a("open:" + ref) : undefined} />;
  };
  const stats = (qs: Map<string, Quote>) => benchmarks.flatMap(ref => { const q = qs.get(ref); if (!q) return []; const c = change(q); return [{ label: q.name || parseRef(ref)!.symbol.split("-")[0], value: price(q, q.last), detail: c === undefined ? undefined : fmtChange(c), tone: c === undefined ? undefined : tone(service.cfg, c) }]; });
  const alertRows = (list: Alert[], detail: boolean) => list.map(alert => { const q = l.quotes.get(alert.ref), title = alertWhat(alert, q); return <Row key={alert.id} title={detail ? title : (alert.name || parseRef(alert.ref)!.symbol) + " " + title} subtitle={alert.met ? `${t("met")} · ${stamp(Date.parse(alert.met), zone, "short")} · ${price({ decimals: alert.decimals ?? 0 }, alert.price ?? 0)}` : t("waiting")} badge={alert.met ? { text: t("met"), tone: "caution" } : undefined} action={a((detail ? "unalert:" + alert.id : "open:" + alert.ref), detail ? { confirm: t("alertConfirm") } : {})} />; });
  const readAt = (hint = false) => <Section key="section-read-at">{hint && <Text key="period" text={t("periodHint")} style="caption" />}<Text key="at" text={t("readAt", { at: stamp(l.at || service.clock(), zone, "time") })} style="caption" />{!card && b("refresh", t("refresh"))}</Section>;
  const addFields = <Section key="section-add" title={t("add")} footer={t("addHint")}><Field key="inst" name="inst" label={t("code")} kind="text" placeholder="BTC-USDT" />{b("add", t("add"), { submit: true })}{v < 3 && b("search", t("search"), { submit: true })}</Section>;
  if (l.kind === "loading") return <View key="markets-view" title={card ? undefined : t("markets")} state={JSON.stringify(st)}><Section key="section-loading"><Loading key="loading" title={t("loading")} /></Section></View>;
  if (l.kind === "error") return <View key="markets-view" title={t("markets")} state={JSON.stringify(st)}><Section key="section-error"><Text key="message" text={textError(l.error)} style="secondary" />{!!st.i && watch.includes(st.i) && b("remove:" + st.i, t("remove"), { role: "destructive", confirm: t("removeConfirm", { symbol: parseRef(st.i)!.symbol }) })}{b("refresh", t("retry"))}{b(st.i ? "back" : "clear", t(st.i && st.q ? "backSearch" : "clear"))}</Section></View>;
  if (card) { const moves = watch.filter(ref => change(l.quotes.get(ref)) !== undefined).sort((a, b) => Math.abs(change(l.quotes.get(b))!) - Math.abs(change(l.quotes.get(a))!)).slice(0, 5); return <View key="markets-view">
    {!!stats(l.quotes).length && <Section key="section-benchmarks"><StatGrid key="stats" stats={stats(l.quotes)} /></Section>}
    <Section key="section-movers" title={moves.length ? t("cardMovers") : undefined}>{moves.map(ref => row(ref, l.quotes.get(ref), false, false, true))}{!moves.length && <Text key="empty" text={!watch.length ? t("empty") : l.errors.size ? t("error") : ""} style="secondary" />}</Section>
  </View>; }
  if (st.i) {
    const ref = parseRef(st.i)!, q = l.quotes.get(st.i)!, cs = l.candles, first = cs[0], last = cs.at(-1), c = change(q), details = [{ label: `${t(q.period)} ${t("high")}`, value: price(q, q.high) }, { label: `${t(q.period)} ${t("low")}`, value: price(q, q.low) }, ...(q.turnover > 0 ? [{ label: `${t(q.period)} ${t("turnover")}`, value: compact(q.turnover) + " " + q.currency }] : []), ...q.facts.map(f => ({ label: t(f.label), value: localizeValue(f.value, props.locale) }))], span = first?.open ? (last!.close - first.open) / first.open : undefined;
    const rangeStats = cs.length ? [{ label: st.b === "T" ? t("sinceOpen") : t("span", { n: cs.length }), value: span === undefined ? "—" : fmtChange(span), tone: span === undefined ? undefined : tone(service.cfg, span) }, { label: t("spanHigh"), value: price(q, Math.max(...cs.map(c => c.high))) }, { label: t("spanLow"), value: price(q, Math.min(...cs.map(c => c.low))) }] : [];
    const group = watch.filter(s => parseRef(s)?.source === ref.source), i = group.indexOf(st.i);
    return <View key="markets-view" title={[ref.symbol, q.name].filter(Boolean).join(" ")} state={JSON.stringify(st)}>
      <Section key="section-price">{b("back", t(st.q ? "backSearch" : "clear"))}<Metric key="last" label={q.idle ? t("idle", { at: stamp(q.at, zones[ref.source], "short") }) : t("latest")} value={price(q, q.last)} detail={c === undefined ? undefined : t(q.period) + " " + fmtChange(c)} tone={c === undefined ? undefined : tone(service.cfg, c)} /></Section>
      <Section key="section-candles" title={t("kline") + " · " + t(st.b)}>
        {v >= 2 ? <Segmented key="bar" name="bar" value={st.b} choices={sourceBars[ref.source].map(b => ({ value: b, title: t(b) }))} action={a("bar")} /> : sourceBars[ref.source].map(bar => <Row key={bar} title={t(bar)} badge={bar === st.b ? { text: t("look"), tone: "positive" } : undefined} action={a("bar:" + bar)} />)}
        {l.candleError ? <Text key="error" text={textError(l.candleError)} style="secondary" /> : !cs.length ? <Text key="empty" text={t(st.b === "T" ? "noMinutes" : "noCandles")} style="secondary" /> : <>
          {v >= 2 && <><Chart key="close" kind="line" title={t("close")} series={[{ name: t("close"), points: cs.map(c => ({ label: stamp(c.at, zone, st.b === "T" ? "time" : ["1D", "1W"].includes(st.b) ? "day" : "short"), value: c.close, display: clip(`${t("close")} ${price(q, c.close)} ${t("high")} ${price(q, c.high)} ${t("low")} ${price(q, c.low)}`, 40) })) }]} /><Chart key="volume" kind="bar" title={t("volume")} series={[{ name: t("volume"), points: cs.map(c => ({ label: stamp(c.at, zone, st.b === "T" ? "time" : ["1D", "1W"].includes(st.b) ? "day" : "short"), value: c.vol, display: compact(c.vol) })) }]} /></>}
          {v >= 2 ? <StatGrid key="range" stats={rangeStats} /> : rangeStats.map(s => <Metric key={s.label} {...s} />)}
        </>}
      </Section>
      <Section key="section-details" title={t("details")}>{v >= 2 ? Array.from({ length: Math.ceil(details.length / 6) }, (_, i) => <StatGrid key={"figures-" + i} stats={details.slice(i * 6, i * 6 + 6)} />) : details.map(s => <Metric key={s.label} {...s} />)}</Section>
      <Section key="section-alerts" title={t("alerts")} footer={t("alertsHint")}>{alertRows(alerts.filter(a => a.ref === st.i), true)}{st.f ? <><Field key="kind" name="alert_kind" label={t("alertKind")} kind="choice" value="above" choices={["above", "below", "move"].map(value => ({ value, title: t(value) }))} /><Field key="value" name="alert_value" label={t("alertValue")} kind="number" placeholder={price(q, q.last)} />{b("alert:" + st.i, t("alert"), { submit: true })}{b("form", t("cancel"))}</> : b("form", t("newAlert"))}</Section>
      <Section key="section-watch" title={t("watch")}>{i >= 0 ? <>{i > 1 && b("top:" + st.i, t("top"))}{i > 0 && b("up:" + st.i, t("up"))}{i < group.length - 1 && b("down:" + st.i, t("down"))}{b("remove:" + st.i, t("remove"), { role: "destructive", confirm: t("removeConfirm", { symbol: ref.symbol }) })}</> : b("add:" + st.i, t("add"))}<Link key="site" title={t("site", { site: ref.source === "okx" ? "OKX" : t("tencent") })} url={service.sources[ref.source].url(ref.symbol)} /><Text key="read" text={t("readAt", { at: stamp(l.at, zone, "time") })} style="caption" /></Section>
    </View>;
  }
  if (st.q) { const groups = sourceIDs.map(id => ({ id, hits: l.hits.filter(h => h.ref.source === id) })).filter(g => g.hits.length).sort((a, b) => Math.min(...a.hits.map(h => matchRank(st.q, service.sources[a.id], h.ref, h.quote))) - Math.min(...b.hits.map(h => matchRank(st.q, service.sources[b.id], h.ref, h.quote))));
    return <View key="markets-view" title={t("markets")} state={JSON.stringify(st)} search={v >= 3 ? { placeholder: t("searchHint"), value: st.q, action: a("search") } : undefined}>
      {l.exact && <Section key="section-exact" title={t("code") + " " + key(l.exact.ref)}>{row(key(l.exact.ref), l.exact.quote, true)}{!watch.includes(key(l.exact.ref)) && b("add:" + key(l.exact.ref), t("add") + " · " + clip(l.exact.quote.name || l.exact.ref.symbol, 20))}</Section>}
      {!l.hits.length && !l.exact && <Section key="section-empty" title={t("searchTitle", { q: st.q })}>{v >= 3 ? <Empty key="none" title={t("none")} icon="search" /> : <Text key="none" text={t("none")} style="secondary" />}</Section>}
      {groups.map(g => <Section key={g.id} title={`${t("searchTitle", { q: st.q })} · ${t(g.id)} · ${g.hits.length}`} footer={l.hits.length >= 30 ? t("moreHits") : undefined}>{g.hits.flatMap(h => [row(key(h.ref), h.quote, true), !watch.includes(key(h.ref)) ? b("add:" + key(h.ref), t("add") + " · " + (h.quote.name || h.ref.symbol)) : null])}</Section>)}
      <Section key="section-back">{b("clear", t("clear"))}</Section>{v < 3 && addFields}{readAt()}
    </View>;
  }
  const present = sourceIDs.filter(id => watch.some(ref => parseRef(ref)?.source === id)), movers = watch.filter(s => change(l.quotes.get(s)) !== undefined).sort((a, b) => Math.abs(change(l.quotes.get(b))!) - Math.abs(change(l.quotes.get(a))!));
  return <View key="markets-view" title={t("markets")} state={JSON.stringify(st)} search={v >= 3 ? { placeholder: t("searchHint"), value: st.q, action: a("search") } : undefined}>
    {v >= 2 && stats(l.quotes).length > 1 && <Section key="section-benchmarks" title={t("benchmarks")}><StatGrid key="stats" stats={stats(l.quotes)} /></Section>}
    {!watch.length && <Section key="section-empty" title={t("watch") + " · 0"}>{v >= 3 ? <Empty key="empty" title={t("empty")} icon="search" /> : <Text key="empty" text={t("empty")} style="secondary" />}</Section>}
    {movers.length > 3 && <Section key="section-movers" title={t("movers")}>{sortByChange(movers.slice(0, 3), l.quotes).map(ref => row(ref, l.quotes.get(ref), false, true, true))}</Section>}
    {v >= 2 && watch.length > 1 && <Section key="section-order" title={t("sort")}><Segmented key="sort" name="sort" value={st.s || "order"} choices={[{ value: "order", title: t("order") }, { value: "up", title: t("upSort") }, { value: "down", title: t("downSort") }]} action={a("sort")} />{present.length > 1 && <Segmented key="market" name="mkt" value={st.m || "all"} choices={[{ value: "all", title: t("all") }, ...present.map(value => ({ value, title: t(value) }))]} action={a("mkt")} />}</Section>}
    {present.filter(id => !st.m || !present.includes(st.m as SourceID) || id === st.m).map(id => { const refs = watch.filter(s => parseRef(s)?.source === id); return <Section key={id} title={`${t(id)} · ${refs.length}`}>{l.errors.has(id) ? <><Text key={"error-" + id} text={textError(l.errors.get(id))} style="secondary" />{b("refresh", t("retry"), {}, "retry-" + id)}</> : (st.s ? sortByChange(refs, l.quotes, st.s === "down") : refs).map(ref => row(ref, l.quotes.get(ref)))}</Section>; })}
    {!!alerts.length && <Section key="section-alerts" title={`${t("alerts")} · ${alerts.length}`}>{alertRows(alerts, false)}</Section>}{v < 3 && addFields}{readAt(true)}
  </View>;
}
