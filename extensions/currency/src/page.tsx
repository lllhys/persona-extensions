import { Button, Field, Link, Loading, Metric, Row, Section, Segmented, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { amount, clip, currency, currencyConversion, dimension, dimensions, formatForDisplay, ReadError, unit, unitConversion, type Conversion, type Dimension, type Rate } from "./model.ts";
import { errorText, fmt, textsFor } from "./i18n.ts";
import { budget, checkSignal, type CurrencyService } from "./service.ts";
export interface PageState { mode: "currency" | "units"; amount: string; from: string; to: string; dimension: Dimension; converted: boolean }
export type Loaded = { kind: "loading" } | { kind: "local"; result: Conversion } | { kind: "currency"; rate: Rate; result: Conversion } | { kind: "error"; error: unknown };
export const initialState: PageState = { mode: "currency", amount: "1", from: "USD", to: "CNY", dimension: "length", converted: false };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 2000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value) || (value.mode !== "currency" && value.mode !== "units") || typeof value.converted !== "boolean") return initialState;
    if (value.mode === "currency") {
      return { mode: "currency", amount: amount(value.amount), from: currency(value.from), to: currency(value.to), dimension: "length", converted: value.converted };
    }
    const d = dimension(value.dimension);
    // Validate both units and their shared dimension before accepting a saved local conversion.
    const parsed = unitConversion(value.amount, value.from, value.to, d);
    return { mode: "units", amount: parsed.amount, from: parsed.from, to: parsed.to, dimension: d, converted: value.converted };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
function fields(values: unknown): Readonly<Record<string, unknown>> {
  if (typeof values === "string") {
    try { values = JSON.parse(values); } catch { return {}; }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return {};
  return values as Readonly<Record<string, unknown>>;
}
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  const f = fields(values);
  if (id.startsWith("mode:")) {
    const mode = id.slice(5) === "units" ? "units" : "currency";
    if (mode === "currency") return { mode, amount: state.amount, from: "USD", to: "CNY", dimension: "length", converted: false };
    return { mode, amount: state.amount, from: "km", to: "mi", dimension: "length", converted: false };
  }
  if (id === "tab") {
    const selected = f.tab === "units" ? "units" : f.tab === "currency" ? "currency" : state.mode;
    return transition(state, `mode:${selected}`);
  }
  if (id === "convert") {
    try {
      if (state.mode === "currency") return { ...state, amount: amount(f.amount), from: currency(f.from), to: currency(f.to), converted: true };
      const converted = unitConversion(f.amount, f.from, f.to);
      return { ...state, amount: converted.amount, from: converted.from, to: converted.to, dimension: unit(converted.to).dimension, converted: true };
    } catch { return state; }
  }
  if (id.startsWith("dimension:")) {
    try {
      const d = dimension(id.slice("dimension:".length)), options = { length: ["km", "mi"], mass: ["kg", "lb"], temperature: ["°C", "°F"], volume: ["L", "US gal"], area: ["m²", "ft²"], speed: ["km/h", "mph"], duration: ["min", "h"], data: ["MiB", "MB"] } as const;
      return { mode: "units", amount: state.amount, from: options[d][0], to: options[d][1], dimension: d, converted: false };
    } catch { return state; }
  }
  return state;
}
export async function loadPage(service: CurrencyService, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.converted) return { kind: "error", error: new ReadError("invalid") };
    if (state.mode === "units") return { kind: "local", result: unitConversion(state.amount, state.from, state.to, state.dimension) };
    const rate = await service.rate(state.from, state.to, refresh, request.signal);
    return { kind: "currency", rate, result: currencyConversion(state.amount, rate) };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: CurrencyService; state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), locale = props.locale ?? service.locale;
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "converter", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 1900)} style={style} />;
  const modes = <Section key="currency-modes">{uiVersion >= 2 ? <Segmented key="currency-mode" name="tab" value={state.mode} choices={[{ value: "currency", title: t.currency }, { value: "units", title: t.units }]} action={act("tab")} />
    : (["currency", "units"] as const).map(mode => <Row key={`mode-${mode}`} title={t[mode]} action={act(`mode:${mode}`)} badge={state.mode === mode ? { text: t.choose, tone: "positive" } : undefined} />)}</Section>;
  const editor = <Section key="currency-editor" title={state.mode === "currency" ? t.currency : t.units}>
    <Field key="currency-amount" name="amount" label={t.amount} kind="text" placeholder="1" value={state.amount} />
    <Field key="currency-from" name="from" label={t.from} kind="text" placeholder={state.mode === "currency" ? t.currencyHint : t.unitHint} value={state.from} />
    <Field key="currency-to" name="to" label={t.to} kind="text" placeholder={state.mode === "currency" ? t.currencyHint : t.unitHint} value={state.to} />
    <Button key="currency-convert" title={t.convert} action={act("convert")} />
  </Section>;
  const attribution = <Section key="currency-attribution">{text("currency-license", state.mode === "currency" ? t.attribution : t.unitAttribution, "caption")}{state.mode === "currency" ? <Link key="currency-source" title={t.source} url="https://frankfurter.dev/" /> : null}</Section>;
  if (!state.converted) return <View key="currency" title={t.title} state={encodeState(state)}>{modes}{editor}<Section key="currency-welcome">{text("currency-welcome-text", t.welcome, "body")}</Section>{attribution}</View>;
  if (loaded.kind === "loading") return <View key="currency" title={t.title} state={encodeState(state)}>{modes}{editor}<Section key="currency-loading"><Loading key="currency-loading-indicator" title={t.convert} /></Section>{attribution}</View>;
  if (loaded.kind === "error") return <View key="currency" title={t.title} state={encodeState(state)}>{modes}{editor}<Section key="currency-error">{text("currency-error-text", errorText(loaded.error, locale))}<Button key="currency-retry" title={t.refresh} action={act("refresh")} /></Section>{attribution}</View>;
  const result = loaded.result;
  return <View key="currency" title={t.title} state={encodeState(state)}>{modes}{editor}
    <Section key="currency-result" title={t.convert}>
      <Metric key="currency-value" label={`${formatForDisplay(result.amount, locale)} ${result.from}`} value={`${formatForDisplay(result.result, locale)} ${result.to}`} />
      {text("currency-rate", loaded.kind === "currency"
        ? fmt(t.rate, { from: result.from, to: result.to, rate: formatForDisplay(loaded.rate.rate, locale) })
        : fmt(t.inverse, { from: result.to, to: result.from, rate: formatForDisplay(result.inverse ?? "1", locale) }), "caption")}
      {loaded.kind === "currency" ? text("currency-date", fmt(t.rateDate, { date: loaded.rate.date }) + (loaded.rate.stale ? ` · ${errorText(loaded.rate.stale, locale)}` : ""), "caption") : text("currency-local", t.local, "caption")}
    </Section>
    {state.mode === "units" ? <Section key="currency-dimensions" title={t.dimension}>{dimensions.map(d => <Button key={`dimension-${d}`} title={t[d]} action={act(`dimension:${d}`)} />)}</Section> : null}
    <Section key="currency-footer"><Button key="currency-refresh" title={t.refresh} action={act("refresh")} /></Section>
    {attribution}
  </View>;
}
