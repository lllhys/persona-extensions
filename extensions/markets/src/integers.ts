import { ExtError } from "@persona/extension";
const minimum = -(1n << 63n), maximum = (1n << 63n) - 1n;
export function int64(value: string): bigint | undefined {
  if (value.trim() !== value || !/^[+-]?\d+$/.test(value)) return;
  // Bound conversion work even for a response-sized string of leading zeroes.
  const digits = value.replace(/^[+-]?0*/, "");
  if (digits.length > 19) return;
  const n = BigInt((value.startsWith("-") ? "-" : "") + (digits || "0"));
  return n >= minimum && n <= maximum ? n : undefined;
}
/** Direct callers retain validation; wire callers supply SDK-validated exact bigint. */
export function toolInteger(value: unknown): number | bigint {
  if (value === undefined || value === null) return 0;
  if (typeof value === "bigint") {
    if (value < minimum || value > maximum) throw new ExtError("Invalid market tool arguments", "invalid_params");
    return value;
  }
  if (typeof value !== "number" || !Number.isInteger(value) || !Number.isFinite(value) || BigInt(value) < minimum || BigInt(value) > maximum) throw new ExtError("Invalid market tool arguments", "invalid_params");
  return value;
}
/** Non-enumerable occurrence history is retained until the typed decoder has checked it. */
const occurrences = Symbol("market JSON fields");
export function jsonFields(value: Record<string, unknown>): Array<[string, unknown]> {
  return (value as any)[occurrences] ?? Object.entries(value);
}
export function jsonObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value) && !(value instanceof JSONNumber);
}
/** Validate syntax before retaining numeric tokens; never repair malformed JSON. */
export class JSONNumber { constructor(readonly lexeme: string) {} }
export function numericJSON(text: string): unknown {
  JSON.parse(text);
  let at = 0;
  const whitespace = () => { while (/[\t\r\n ]/.test(text[at] ?? "!") ) at++; };
  const string = () => {
    const start = at++;
    while (text[at] !== '"') { if (text[at] === "\\") at++; at++; }
    at++;
    return JSON.parse(text.slice(start, at)) as string;
  };
  const value = (depth: number): unknown => {
    if (depth > 128) throw new Error("Market JSON nesting exceeds limit");
    whitespace();
    if (text[at] === '"') return string();
    if (text[at] === "{" || text[at] === "[") {
      const object = text[at++] === "{", end = object ? "}" : "]";
      const result: any = object ? Object.create(null) : [];
      const fields: Array<[string, unknown]> = [];
      if (object) Object.defineProperty(result, occurrences, { value: fields });
      whitespace();
      while (text[at] !== end) {
        if (object) { const key = string(); whitespace(); at++; const item = value(depth + 1); fields.push([key, item]); result[key] = item; }
        else result.push(value(depth + 1));
        whitespace(); if (text[at] !== ",") break; at++; whitespace();
      }
      at++; return result;
    }
    const start = at;
    while (at < text.length && !/[\t\r\n ,}\]]/.test(text[at])) at++;
    const token = text.slice(start, at);
    return /^(?:true|false|null)$/.test(token) ? JSON.parse(token) : new JSONNumber(token);
  };
  return value(0);
}
export function jsonInteger(value: unknown): bigint | undefined {
  return value instanceof JSONNumber && /^-?\d+$/.test(value.lexeme) ? int64(value.lexeme) : undefined;
}
