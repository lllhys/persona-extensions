import { ExtError, noContribution, unknownMethod } from "@persona/extension";
import type { Values } from "@persona/extension/ui";
import { translator, type Translate } from "./locale.ts";

export interface Item { id: string; title?: string; kind: string; note?: string; done: boolean }
export interface PageState { p?: string; q?: string; kind?: string; shown?: number; id?: string; range?: string; answer?: string }
export interface Clock {
  now(): number;
  later(fn: () => void, milliseconds: number): unknown;
  cancel(handle: unknown): void;
}
const clock: Clock = {
  now: () => Date.now(), later: (fn, ms) => setTimeout(fn, ms),
  cancel: (id) => clearTimeout(id as ReturnType<typeof setTimeout>),
};
export const pageSize = 20;
export const kinds = ["all", "doc", "chat", "mail"] as const;
export const displayed = (state: PageState) => state.shown && state.shown > 0 ? state.shown : pageSize;
export function itemText(item: Item, t: Translate) {
  return { ...item, title: item.title ?? t("item", { n: item.id.padStart(2, "0") }), note: item.note ?? t("note", { n: item.id }) };
}
export function kindChoices(t: Translate, all = true) {
  return kinds.slice(all ? 0 : 1).map(value => ({ value, title: t(value) }));
}
export function summarize(values: Values): string {
  return Object.keys(values).sort().map(key => `${key}=${key === "secret" && values[key] ? "••••" : values[key]}`).join("，");
}
/** Synthetic service state is shared by pages; localized defaults are resolved per request. */
export class ShowcaseStore {
  private listeners = new Set<(change: "items" | "data") => void>();
  private timer: unknown;
  private generation = 0;
  private stopped = false;
  asked?: number;
  came?: number;
  readonly items: Item[] = Array.from({ length: 60 }, (_, index) => ({
    id: String(index + 1), kind: kinds[1 + index % 3], done: (index + 1) % 4 === 0,
  }));
  constructor(private readonly time: Clock = clock, readonly delay = 3000) {}
  subscribe(listener: (change: "items" | "data") => void) {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }
  private changed(change: "items" | "data") { for (const listener of this.listeners) listener(change); }
  find(id?: string) { return this.items.find(item => item.id === id); }
  matching(state: PageState, t: Translate) {
    return this.items.filter(item => (!state.kind || state.kind === "all" || item.kind === state.kind)
      && (!state.q || itemText(item, t).title.includes(state.q)));
  }
  save(id: string | undefined, values: Values): boolean {
    const title = values.title?.trim();
    if (!title) return false;
    const item = this.find(id);
    if (!item) return false;
    item.title = title;
    if (values.kind) item.kind = values.kind;
    item.note = values.note ?? "";
    item.done = values.done === "true";
    this.changed("items");
    return true;
  }
  /** A superseded ask cannot deliver or notify, even if its canceled callback was already queued. */
  ask() {
    if (this.stopped) return;
    if (this.timer !== undefined) this.time.cancel(this.timer);
    const generation = ++this.generation;
    this.asked = this.time.now();
    this.came = undefined;
    this.changed("data");
    this.timer = this.time.later(() => {
      if (this.stopped || generation !== this.generation) return;
      this.timer = undefined;
      this.came = this.time.now();
      this.changed("data");
    }, this.delay);
  }
  dispose() {
    this.stopped = true;
    ++this.generation;
    if (this.timer !== undefined) this.time.cancel(this.timer);
    this.timer = undefined;
    this.listeners.clear();
  }
}
/** Strict shortcut semantics, transport-independent and usable by the official process Program. */
export function runShortcut(method: string, contribution: string, values: Values, language = "en") {
  const t = translator(language);
  if (method !== "run") throw unknownMethod(`shortcut.${method}`);
  if (contribution === "showcase-echo") return { text: t("echo", { text: values.text ?? "" }) };
  if (contribution !== "showcase-count") throw noContribution(contribution);
  const input = (values.n ?? "").trim();
  const n = Number(input);
  if (!/^[+-]?\d+$/.test(input) || !Number.isSafeInteger(n) || n < 0 || n > 1000) {
    throw new ExtError(t("countInvalid"), "invalid");
  }
  const unit = values.unit === "apple" || values.unit === "star" ? values.unit : "unit";
  return {
    text: t("counted", { n, unit: t(unit) }) + (values.loud === "true" ? "！" : ""),
    count: n, detail: t(unit === "unit" ? "unitDetail" : unit === "apple" ? "appleDetail" : "starDetail"),
  };
}
