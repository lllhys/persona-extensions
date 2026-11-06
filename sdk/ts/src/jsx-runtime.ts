import { createElement, Fragment, type Element as UIElement, type Node, type FunctionComponent } from "./components.ts";
export { Fragment };
export const jsx = createElement;
export const jsxs = createElement;
export namespace JSX {
  export type Element = UIElement;
  export type ElementType = FunctionComponent | typeof Fragment;
  export interface ElementChildrenAttribute { children: unknown }
  export interface IntrinsicAttributes { key?: string }
  // No DOM/intrinsic names, arbitrary styles or client code.
  export interface IntrinsicElements {}
}
