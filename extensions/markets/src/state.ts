import type { Hub } from "@persona/extension";
import { parseRef, readRefs, key, type Alert } from "./model.ts";
import { MutationError } from "./errors.ts";
import { numericJSON } from "./integers.ts";
import { typedJSON, ordinaryJSON, type JSONType } from "./typed-json.ts";
import { validRFC3339 } from "./time.ts";
const alertType: JSONType = { fields: { id: "string", ref: "string", kind: "string", value: "float", created: "time", met: { nullable: "time" }, price: "float", change: "float", name: "string", decimals: "integer" } };
/** Raw state is still available here, unlike SDK-decoded tool arguments. */
export function parseStateJSON(text: string): unknown {
  const value = numericJSON(text);
  if (value === null) return null;
  const type: JSONType = Array.isArray(value) ? { array: value.some(item => item !== null && typeof item === "object") ? alertType : "string" }
    : { fields: { schema_version: "integer", watchlist: { array: "string" }, alerts: { array: alertType } } };
  return ordinaryJSON(typedJSON(value, type));
}
export interface State { schema_version: 1; watchlist: string[]; alerts: Alert[] }
export function validateState(value: unknown): State {
  const s = value as State | undefined;
  if (!s || s.schema_version !== 1 || !Array.isArray(s.watchlist) || s.watchlist.length > 50 || s.watchlist.some(x => typeof x !== "string" || !parseRef(x)) || new Set(s.watchlist).size !== s.watchlist.length || !Array.isArray(s.alerts) || s.alerts.length > 50) throw new Error("Invalid stored market state; original blob retained");
  const ids = new Set<string>();
  for (const a of s.alerts) { if (!a || typeof a.id !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(a.id) || ids.has(a.id) || typeof a.ref !== "string" || !parseRef(a.ref) || !["above", "below", "move"].includes(a.kind) || !Number.isFinite(a.value) || a.value <= 0 || typeof a.created !== "string" || !validRFC3339(a.created) || a.met !== undefined && (typeof a.met !== "string" || !validRFC3339(a.met)) || [a.price, a.change, a.decimals].some(x => x !== undefined && !Number.isFinite(x)) || a.decimals !== undefined && (!Number.isInteger(a.decimals) || a.decimals < 0 || a.decimals > 12) || a.kind === "move" && a.value > 100 || a.name !== undefined && (typeof a.name !== "string" || a.name.length > 512)) throw new Error("Invalid stored alerts; original blob retained"); ids.add(a.id); }
  const normalized = structuredClone(s);
  normalized.watchlist = readRefs(s.watchlist);
  if (normalized.watchlist.length !== s.watchlist.length) throw new Error("Duplicate normalized watchlist references; original blob retained");
  normalized.alerts = normalized.alerts.map(a => ({ ...a, ref: key(parseRef(a.ref)!) }));
  return normalized;
}
export function encodeState(state: State): string { const bytes = new TextEncoder().encode(JSON.stringify(validateState(state))); if (bytes.length > 256 * 1024) throw new Error("Stored market state too large"); let binary = ""; for (const b of bytes) binary += String.fromCharCode(b); return btoa(binary); }
export function decodeState(data: string): State { if (data.length > 350_000) throw new Error("Stored market state too large"); const bytes = Uint8Array.from(atob(data), c => c.charCodeAt(0)); return validateState(parseStateJSON(new TextDecoder("utf-8", { fatal: true }).decode(bytes))); }
/** Pure conversion only. Guest code cannot read old JSON files; a reviewed host/offline importer is required before upgrades. */
export function legacyState(watchlist: unknown, alerts: unknown): State {
  if (watchlist === null) watchlist = [];
  if (alerts === null) alerts = [];
  if (!Array.isArray(watchlist) || watchlist.some(x => typeof x !== "string" || !parseRef(x))) throw new Error("Invalid legacy watchlist; review before import");
  const refs = readRefs(watchlist);
  if (watchlist.length > 50 || refs.length !== new Set(watchlist.map(x => { const r = parseRef(x)!; return r.source + ":" + r.symbol; })).size) throw new Error("Legacy watchlist exceeds 50 entries; review before import");
  return validateState({ schema_version: 1, watchlist: refs, alerts });
}
export class StateStore {
  private state: State = { schema_version: 1, watchlist: [], alerts: [] };
  private serial: Promise<unknown> = Promise.resolve();
  constructor(private hub: Pick<Hub, "call">) {}
  async load(seed: string[]) { const stored = await this.hub.call<{ data?: string }>("connector.secrets.load", { contribution: "markets" }); if (stored.data) this.state = decodeState(stored.data); else { const initial: State = { schema_version: 1, watchlist: readRefs(seed), alerts: [] }; await this.save(initial); this.state = initial; } }
  snapshot(): State { return structuredClone(this.state); }
  private save(s: State) { return this.hub.call("connector.secrets.save", { contribution: "markets", data: encodeState(s) }); }
  transaction<T>(fn: (next: State) => T, keepOnFailure = false): Promise<T> {
    const work = this.serial.then(async () => { const draft = this.snapshot(), result = fn(draft), next = validateState(draft); if (JSON.stringify(next) === JSON.stringify(this.state)) return result; try { await this.save(next); } catch (err) { if (keepOnFailure) this.state = next; throw new MutationError("saveError", "Could not save encrypted market state"); } this.state = next; return result; }); this.serial = work.catch(() => {}); return work;
  }
}
