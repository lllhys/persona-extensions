export { Fragment } from "./jsx-runtime.ts";
export type { JSX } from "./jsx-runtime.ts";
import { createElement, type Element } from "./components.ts";
/** Development transforms carry compiler metadata, which never enters the native View. */
export function jsxDEV(type: Element["type"], props: Record<string, unknown> | null, key?: string, _staticChildren?: boolean, _source?: unknown, _self?: unknown): Element {
  return createElement(type, props, key);
}
