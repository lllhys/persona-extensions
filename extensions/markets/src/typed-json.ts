import { JSONNumber, jsonFields, jsonInteger, jsonObject } from "./integers.ts";
import { validRFC3339 } from "./time.ts";
export type JSONType = "string" | "integer" | "float" | "time" | "raw" | { fields: Record<string, JSONType> } | { array: JSONType } | { map: JSONType } | { nullable: JSONType };
/** These schemas have ASCII field names. Go's simple-fold equivalents also include long s and Kelvin K. */
const fold = (s: string) => s.replace(/[a-zſK]/g, c => c === "ſ" ? "S" : c === "K" ? "K" : c.toUpperCase());
/** Local provider/state decoding, not an SDK tool-wire workaround. Check every consumed occurrence before success. */
export function typedJSON(value: unknown, type: JSONType, previous?: any): any {
  if (type === "raw") return value;
  if (typeof type === "object" && "nullable" in type) return value === null ? undefined : typedJSON(value, type.nullable, previous);
  if (value === null) {
    if (typeof type === "object" && ("map" in type || "array" in type)) return null;
    return previous ?? (type === "string" ? "" : type === "integer" || type === "float" ? new JSONNumber("0") : undefined);
  }
  if (type === "string") { if (typeof value !== "string") throw new Error("Invalid market string field"); return value; }
  if (type === "integer") { if (jsonInteger(value) === undefined) throw new Error("Invalid market integer field"); return value; }
  if (type === "float") { if (!(value instanceof JSONNumber) || !Number.isFinite(Number(value.lexeme))) throw new Error("Invalid market floating-point field"); return value; }
  if (type === "time") { if (typeof value !== "string" || !validRFC3339(value)) throw new Error("Invalid stored RFC3339 time"); return value; }
  if ("array" in type) {
    if (!Array.isArray(value)) throw new Error("Invalid market array field");
    return value.map(item => typedJSON(item, type.array));
  }
  if (!jsonObject(value)) throw new Error("Invalid market object field");
  const result = previous ?? Object.create(null);
  for (const [name, item] of jsonFields(value)) {
    if ("map" in type) {
      // Go map elements are decoded into a fresh zero value, not the previous element.
      result[name] = typedJSON(item, type.map);
    } else {
      const field = Object.hasOwn(type.fields, name) ? name : Object.keys(type.fields).find(key => fold(key) === fold(name));
      if (field !== undefined) result[field] = typedJSON(item, type.fields[field], result[field]);
    }
  }
  return result;
}
export function ordinaryJSON(value: unknown): any {
  if (value instanceof JSONNumber) return Number(value.lexeme);
  if (Array.isArray(value)) return value.map(ordinaryJSON);
  if (jsonObject(value)) return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined).map(([key, item]) => [key, ordinaryJSON(item)]));
  return value;
}
