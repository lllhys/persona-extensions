import { ExtensionStorage, storageBytes, storageValue, type Hub, type StorageTransactionParams } from "@persona/extension";
import { parseStoredShow, ReadError, type Show } from "./model.ts";
export interface ListeningList { schema_version: 1; shows: Show[] }
export interface Storage {
  load(): Promise<ListeningList>;
  has(id: number): boolean;
  save(show: Show): Promise<void>;
  remove(id: number): Promise<void>;
}
export const emptyList = (): ListeningList => ({ schema_version: 1, shows: [] });
export function decodeList(value: string): ListeningList {
  if (value.length > 87_384) throw new ReadError("storage");
  let raw: unknown;
  try { raw = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(storageValue(value))); }
  catch { throw new ReadError("storage"); }
  if (!raw || typeof raw !== "object" || Array.isArray(raw) || (raw as { schema_version?: unknown }).schema_version !== 1 || !Array.isArray((raw as { shows?: unknown }).shows)) throw new ReadError("storage");
  const values = (raw as { shows: unknown[] }).shows;
  if (values.length > 50) throw new ReadError("storage");
  const shows = values.map(parseStoredShow);
  if (shows.some(show => !show) || new Set(shows.map(show => show!.id)).size !== shows.length) throw new ReadError("storage");
  return { schema_version: 1, shows: shows as Show[] };
}
export function encodeList(list: ListeningList): string {
  if (list.schema_version !== 1 || list.shows.length > 50 || new Set(list.shows.map(show => show.id)).size !== list.shows.length) throw new ReadError("storage");
  const value = storageBytes(new TextEncoder().encode(JSON.stringify(list)));
  decodeList(value);
  return value;
}
/** Local-only list. No credential, hub token or other extension is involved. */
export class ListeningStore implements Storage {
  private list = emptyList();
  private loaded = false;
  private loading: Promise<ListeningList> | undefined;
  private revision: string | undefined;
  private serial: Promise<unknown> = Promise.resolve();
  private pending: { request: StorageTransactionParams; at: number; change: (list: ListeningList) => Show[] } | undefined;
  private readonly storage: ExtensionStorage;
  constructor(hub: Pick<Hub, "call">, private readonly now: () => number = Date.now) { this.storage = new ExtensionStorage(hub); }
  snapshot() { return structuredClone(this.list); }
  has(id: number) { return this.list.shows.some(show => show.id === id); }
  async load() {
    if (this.loaded) return this.snapshot();
    if (this.loading) return this.loading;
    this.loading = (async () => {
      try {
        const stored = await this.storage.get("subscriptions");
        this.list = decodeList(stored.data); this.revision = stored.revision;
      } catch (error) {
        if ((error as { code?: string })?.code !== "not_found") { this.loaded = false; throw new ReadError("storage"); }
        this.list = emptyList(); this.revision = undefined;
      }
      this.loaded = true;
      return this.snapshot();
    })();
    try { return await this.loading; }
    finally { this.loading = undefined; }
  }
  private mutate(change: (list: ListeningList) => Show[]): Promise<void> {
    const work = this.serial.then(async () => {
      if (!this.loaded) await this.load();
      for (let attempt = 0; attempt < 3; attempt++) {
        if (this.pending) {
          const pending = this.pending;
          // A lost reply may follow a committed write. Only the identical
          // request/payload may recover its receipt; never invent a new write.
          if (this.now() - pending.at > 5 * 60_000) {
            this.pending = undefined; this.loaded = false;
            await this.load();
            continue;
          }
          try {
            const result = await this.storage.transaction(pending.request);
            const item = result.items.find(item => item.key === "subscriptions");
            if (!item || item.deleted || typeof item.revision !== "string") throw new ReadError("storage");
            this.pending = undefined;
            this.list = decodeList(pending.request.operations[0].data ?? ""); this.revision = item.revision;
          } catch (error) {
            if (["storage_unknown", "storage_timeout", "storage_canceled"].includes((error as { code?: string })?.code ?? "")) throw new ReadError("storage");
            if ((error as { code?: string })?.code === "storage_expired") {
              this.pending = undefined; this.loaded = false; await this.load(); continue;
            }
            this.pending = undefined;
            if ((error as { code?: string })?.code !== "storage_conflict") throw error;
            this.loaded = false; await this.load();
          }
          continue;
        }
        const next = { schema_version: 1 as const, shows: change(this.snapshot()) };
        if (next.shows.length > 50) throw new ReadError("storage");
        const { request_id } = await this.storage.request();
        const request = { request_id, operations: [{ kind: "put", key: "subscriptions", revision: this.revision ?? "", data: encodeList(next) }] };
        try {
          const result = await this.storage.transaction(request);
          const item = result.items.find(item => item.key === "subscriptions");
          if (!item || item.deleted || typeof item.revision !== "string") throw new ReadError("storage");
          this.list = next; this.revision = item.revision;
          return;
        } catch (error) {
          const code = (error as { code?: string })?.code;
          if (["storage_unknown", "storage_timeout", "storage_canceled"].includes(code ?? "")) {
            this.pending = { request, at: this.now(), change };
            throw new ReadError("storage");
          }
          if (code !== "storage_conflict" || attempt === 2) throw error;
          this.loaded = false;
          await this.load();
        }
      }
      throw new ReadError("storage");
    });
    this.serial = work.catch(() => {});
    return work;
  }
  save(show: Show) { return this.mutate(list => [show, ...list.shows.filter(item => item.id !== show.id)]); }
  remove(id: number) { return this.mutate(list => list.shows.filter(show => show.id !== id)); }
}
export class MemoryStore implements Storage {
  private list = emptyList();
  async load() { return this.snapshot(); }
  private snapshot() { return structuredClone(this.list); }
  has(id: number) { return this.list.shows.some(show => show.id === id); }
  async save(show: Show) { this.list = { schema_version: 1, shows: [show, ...this.list.shows.filter(item => item.id !== show.id)] }; }
  async remove(id: number) { this.list = { schema_version: 1, shows: this.list.shows.filter(show => show.id !== id) }; }
}
