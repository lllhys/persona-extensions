import type { FetchAdapter } from "@persona/extension/ui";
import { lists, settings, topicID, resolveZone, type Settings, type HotTopic, type Topic } from "./model.ts";
import { parseFeed } from "./feed.ts";
import { parseTopic } from "./topic.ts";
import { Site, ReadError, limits, errorText, type Reading, type SiteOptions } from "./site.ts";
import { textsFor } from "./i18n.ts";
import { InputError } from "./errors.ts";
export { ReadError, limits, errorText } from "./site.ts";
export type Listing = Reading<HotTopic[]>;
export type Thread = Reading<Topic>;
/** One instance per Program; connector, tools and every page share the same queue/cache/status. */
export class LinuxDo {
  readonly config: Settings;
  readonly site: Site;
  readonly now: () => number;
  private lastOK = 0;
  private lastErr?: ReadError;
  private lastErrAt = 0;
  constructor(fetch: FetchAdapter, config: unknown = {}, readonly timezone = "Local", readonly locale = "", options: SiteOptions = {}) {
    this.config = settings(config, timezone); this.site = new Site(fetch, options); this.now = this.site.now;
  }
  private async read<T>(read: () => Promise<Reading<T>>, signal?: AbortSignal): Promise<Reading<T>> {
    try {
      const reading = await read();
      // Even an already-resolved cache hit yields at await. Cancellation in that
      // window must not return data, clear a failure or advance last_sync.
      signal?.throwIfAborted();
      if (reading.stale) { this.lastErr = reading.stale; this.lastErrAt = this.now(); }
      else {
        // Preserve the original normal-hit status semantics: cache hits advance last_sync
        // and clear older failures. Reading.at/live/stale separately identify actual data freshness.
        this.lastOK = this.now(); this.lastErr = undefined;
      }
      return reading;
    } catch (error) {
      signal?.throwIfAborted();
      if (error instanceof ReadError && error.code !== "gone") { this.lastErr = error; this.lastErrAt = this.now(); }
      throw error;
    }
  }
  listing(list: string, refresh = false, signal?: AbortSignal): Promise<Listing> {
    if (!Object.hasOwn(lists, list)) return Promise.reject(new InputError("invalidList"));
    return this.read(() => this.site.get(lists[list as keyof typeof lists], refresh ? 0 : limits.list, parseFeed, signal), signal);
  }
  thread(value: unknown, signal?: AbortSignal): Promise<Thread> {
    const id = topicID(value);
    return this.read(() => this.site.get(`/t/topic/${id}.json`, limits.topic, raw => {
      const topic = parseTopic(raw); if (topic.id === "0" || !topic.posts.length) throw new ReadError("gone");
      return topic;
    }, signal), signal);
  }
  zone(request?: string) { return resolveZone(this.config.tz || request || this.timezone); }
  status(locale = this.locale) {
    const t = textsFor(locale), fail = this.lastErr && this.lastErrAt >= this.lastOK ? this.lastErr : undefined;
    return { state: fail?.code === "rate" ? "rate_limited" : fail ? "error" : "ok", account: t.account,
      ...(this.lastOK ? { last_sync: new Date(this.lastOK).toISOString() } : {}),
      ...(fail ? { error: errorText(fail, locale) } : {}), ...(fail?.code === "challenge" ? { guide: [t.guide] } : {}) };
  }
}
