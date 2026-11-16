import { ExtensionStorage, storageBytes, storageValue, type Hub } from "@persona/extension";
import { domainName, ReadError } from "./model.ts";
export interface History { schema_version: 1; domains: string[] }
export interface HistoryStorage { load(): Promise<History>; add(domain: string): Promise<void>; clear(): Promise<void> }
export const emptyHistory = (): History => ({ schema_version: 1, domains: [] });
export function decodeHistory(value: string): History {
  if (value.length > 87_384) throw new ReadError("storage");
  let raw: unknown;
  try { raw = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(storageValue(value))); }
  catch { throw new ReadError("storage"); }
  if (!raw || typeof raw !== "object" || Array.isArray(raw) || (raw as { schema_version?: unknown }).schema_version !== 1 || !Array.isArray((raw as { domains?: unknown }).domains)) throw new ReadError("storage");
  const values = (raw as { domains: unknown[] }).domains;
  if (values.length > 20 || new Set(values).size !== values.length) throw new ReadError("storage");
  try { return { schema_version: 1, domains: values.map(value => domainName(value).ascii) }; }
  catch { throw new ReadError("storage"); }
}
export function encodeHistory(history: History): string {
  if (history.schema_version !== 1 || history.domains.length > 20 || new Set(history.domains).size !== history.domains.length) throw new ReadError("storage");
  const value = storageBytes(new TextEncoder().encode(JSON.stringify(history)));
  decodeHistory(value);
  return value;
}
export class DomainHistory implements HistoryStorage {
  private history = emptyHistory();
  private loaded = false;
  private loading: Promise<History> | undefined;
  private revision: string | undefined;
  private serial: Promise<unknown> = Promise.resolve();
  private readonly storage: ExtensionStorage;
  constructor(hub: Pick<Hub, "call">) { this.storage = new ExtensionStorage(hub); }
  snapshot() { return structuredClone(this.history); }
  async load() {
    if (this.loaded) return this.snapshot();
    if (this.loading) return this.loading;
    this.loading = (async () => {
      try {
        const stored = await this.storage.get("history");
        this.history = decodeHistory(stored.data); this.revision = stored.revision;
      } catch (error) {
        if ((error as { code?: string })?.code !== "not_found") { this.loaded = false; throw new ReadError("storage"); }
        this.history = emptyHistory(); this.revision = undefined;
      }
      this.loaded = true;
      return this.snapshot();
    })();
    try { return await this.loading; }
    finally { this.loading = undefined; }
  }
  private mutate(change: (domains: string[]) => string[]): Promise<void> {
    const work = this.serial.then(async () => {
      if (!this.loaded) await this.load();
      for (let attempt = 0; attempt < 2; attempt++) {
        const next = { schema_version: 1 as const, domains: change(this.snapshot().domains) };
        if (next.domains.length > 20) throw new ReadError("storage");
        const { request_id } = await this.storage.request();
        try {
          const result = await this.storage.transaction({ request_id, operations: [{ kind: "put", key: "history", revision: this.revision ?? "", data: encodeHistory(next) }] });
          const item = result.items.find(item => item.key === "history");
          if (!item || item.deleted || typeof item.revision !== "string") throw new ReadError("storage");
          this.history = next; this.revision = item.revision;
          return;
        } catch (error) {
          if ((error as { code?: string })?.code !== "storage_conflict" || attempt === 1) throw error;
          this.loaded = false;
          await this.load();
        }
      }
    });
    this.serial = work.catch(() => {});
    return work;
  }
  add(domain: string) { const normalized = domainName(domain).ascii; return this.mutate(domains => [normalized, ...domains.filter(item => item !== normalized)].slice(0, 20)); }
  clear() { return this.mutate(() => []); }
}
export class MemoryHistory implements HistoryStorage {
  private history = emptyHistory();
  async load() { return structuredClone(this.history); }
  async add(domain: string) { const normalized = domainName(domain).ascii; this.history = { schema_version: 1, domains: [normalized, ...this.history.domains.filter(item => item !== normalized)].slice(0, 20) }; }
  async clear() { this.history = emptyHistory(); }
}
