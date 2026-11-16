import { Button, Field, Link, Loading, Row, Section, Text, View, type CallbackAction, type NativeAction, type Node } from "@persona/extension/ui";
import { clip, domainName, ReadError, statusURL, type Registration } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, checkSignal, type Domains } from "./service.ts";
import type { HistoryStorage } from "./store.ts";
export type ViewKind = "lookup" | "result" | "history";
export interface PageState { v: ViewKind; q: string; message?: string }
export type Loaded = { kind: "loading" } | { kind: "empty" } | { kind: "result"; result: Registration } | { kind: "history"; domains: string[] } | { kind: "error"; error: unknown };
export const initialState: PageState = { v: "lookup", q: "" };
export function readState(raw: unknown): PageState {
  if (typeof raw !== "string" || raw.length > 2000) return initialState;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (!value || Array.isArray(value)) return initialState;
    const q = typeof value.q === "string" ? clip(value.q.trim(), 253) : "";
    const v = ["result", "history"].includes(String(value.v)) ? value.v as ViewKind : "lookup";
    if (v === "result") domainName(q);
    return { v, q, ...(value.message === "cleared" ? { message: "cleared" } : {}) };
  } catch { return initialState; }
}
export const encodeState = (state: PageState) => JSON.stringify(state);
export function transition(state: PageState, id: string, values: unknown = {}): PageState {
  if (id === "lookup") return { v: "lookup", q: "" };
  if (id === "history") return { v: "history", q: "" };
  if (id === "cleared") return { v: "history", q: "", message: "cleared" };
  if (id === "back") return { v: "lookup", q: state.q };
  if (id.startsWith("domain:")) {
    try { return { v: "result", q: domainName(id.slice(7)).ascii }; } catch { return state; }
  }
  if (id === "search") {
    let fields: unknown = values;
    if (typeof values === "string") { try { fields = JSON.parse(values); } catch { return state; } }
    if (!fields || typeof fields !== "object" || Array.isArray(fields) || typeof (fields as { domain?: unknown }).domain !== "string") return state;
    try { return { v: "result", q: domainName((fields as { domain: string }).domain).ascii }; } catch { return state; }
  }
  return state;
}
export async function loadPage(service: Domains, history: HistoryStorage, state: PageState, refresh: boolean, signal?: AbortSignal): Promise<Loaded> {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "result") {
      const result = await service.lookup(state.q, refresh, request.signal);
      try { await history.add(result.domain.ascii); } catch { /* Read-only results do not depend on history. */ }
      return { kind: "result", result };
    }
    if (state.v === "history") return { kind: "history", domains: (await history.load()).domains };
    return { kind: "empty" };
  } catch (error) { checkSignal(signal); return { kind: "error", error }; }
  finally { request.dispose(); }
}
export interface PageProps { service: Domains; history: HistoryStorage; state: PageState; loaded: Loaded; locale?: string; tz?: string; uiVersion?: number; action?: (id: string, next?: PageState, title?: string) => NativeAction | CallbackAction }
export function page(props: PageProps): Node {
  const { service, state, loaded } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id: string, next?: PageState, title?: string) => props.action?.(id, next, title) ?? (next && (props.uiVersion ?? 0) >= 3 ? { id, push: { ui: "lookup", state: encodeState(next), ...(title ? { title } : {}) } } : { id });
  const text = (key: string, value: string, style: "body" | "caption" | "secondary" = "secondary") => <Text key={key} text={clip(value, 1800)} style={style} />;
  const form = <Section key="domain-form" title={t.search}><Field key="domain-input" name="domain" label={t.search} kind="text" placeholder={t.hint} value={state.q} /><Button key="domain-submit" title={t.action} action={act("search")} /></Section>;
  const nav = <Section key="domain-navigation"><Button key="domain-home" title={t.lookup} action={act("lookup")} /><Button key="domain-history" title={t.history} action={act("history")} /></Section>;
  if (loaded.kind === "loading") return <View key="domains" title={t.lookup} state={encodeState(state)}>{form}{nav}<Section key="domain-loading"><Loading key="domain-loading-indicator" title={t.loading} /></Section></View>;
  if (loaded.kind === "error") return <View key="domains" title={t.lookup} state={encodeState(state)}>{form}{nav}<Section key="domain-error">{text("domain-error-message", errorText(loaded.error, props.locale ?? service.locale))}<Button key="domain-retry" title={t.retry} action={act("refresh")} /></Section></View>;
  if (state.v === "result") {
    if (loaded.kind !== "result") throw new ReadError("invalid");
    const result = loaded.result;
    return <View key="domains" title={result.domain.unicode} state={encodeState(state)}>{form}{nav}
      <Section key="registration" title={fmt(t.result, { domain: result.domain.unicode })}>
        {text("registered-name", `${t.registeredName}: ${result.name}`, "body")}
        {result.registrar ? text("registrar", `${t.registrar}: ${result.registrar}`, "body") : null}
      </Section>
      <Section key="registration-status" title={t.statuses}>{text("registration-status-values", result.statuses.join(" · ") || t.noStatus, "body")}<Link key="status-glossary" title={t.statusGlossary} url={statusURL} /></Section>
      <Section key="registration-events" title={t.events}>{result.events.length ? result.events.map((event, index) => <Row key={`event-${index}`} title={event.action} subtitle={readTime(event.at, tz, props.locale ?? service.locale)} />) : text("no-events", t.noEvents)}</Section>
      <Section key="registration-source" title={t.source}>{text("source-name", result.source, "body")}<Link key="registration-open" title={t.openRecord} url={result.url} /><Button key="registration-back" title={t.lookup} action={act("back")} /></Section>
      <Section key="registration-footer" footer={`${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` · ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`}><Button key="registration-refresh" title={t.refresh} action={act("refresh")} /></Section>
      <Section key="registration-notes">{text("status-note", t.statusHint, "caption")}{text("registration-attribution", t.attribution, "caption")}</Section>
    </View>;
  }
  if (state.v === "history") {
    if (loaded.kind !== "history") throw new ReadError("invalid");
    return <View key="domains" title={t.history} state={encodeState(state)}>{nav}
      {state.message === "cleared" ? <Section key="history-cleared">{text("history-cleared-text", t.cleared, "body")}</Section> : null}
      <Section key="history-list" title={t.history}>{loaded.domains.length ? loaded.domains.map(domain => <Row key={`history-${domain}`} title={domain} action={act(`domain:${domain}`, { v: "result", q: domain }, domain)} />) : text("history-empty", t.historyEmpty)}{loaded.domains.length ? <Button key="history-clear" title={t.clearHistory} action={act("clear")} /> : null}</Section>
      <Section key="history-note">{text("history-note-text", t.historyHint, "caption")}</Section>
    </View>;
  }
  return <View key="domains" title={t.lookup} state={encodeState(state)}>{form}{nav}<Section key="domain-welcome">{text("domain-welcome-text", t.attribution, "body")}</Section></View>;
}
