import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const sampleShow = {
  wrapperType: "track", kind: "podcast", collectionId: 1860072717, trackId: 1860072717,
  collectionName: "Example Engineering", artistName: "Example Studio", primaryGenreName: "Technology", trackCount: 2,
  collectionViewUrl: "https://podcasts.apple.com/us/podcast/example/id1860072717?uo=4",
  feedUrl: "https://feeds.example.com/show.xml",
};
export const otherShow = { ...sampleShow, collectionId: 42, collectionName: "Another Show", feedUrl: undefined };
export const sampleEpisode = {
  wrapperType: "podcastEpisode", kind: "podcast-episode", collectionId: 1860072717, trackId: 1000792066218,
  trackName: "A useful conversation", releaseDate: "2026-09-28T17:46:26Z", trackTimeMillis: 3538000,
  description: "A synthetic description of an episode.", episodeUrl: "https://media.example.com/audio.mp3",
};
export const searchURL = "/search?term=synthetic&media=podcast&entity=podcast&limit=20";
export const searchToolURL = "/search?term=synthetic&media=podcast&entity=podcast&limit=10";
export const lookupURL = "/lookup?id=1860072717&entity=podcastEpisode&limit=20&sort=recent";
export const lookupToolURL = "/lookup?id=1860072717&entity=podcastEpisode&limit=10&sort=recent";
export const showURL = "/lookup?id=1860072717&entity=podcast&limit=1";
export class Fixture {
  clock = Date.UTC(2026, 10, 12);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(searchURL, { resultCount: 2, results: [sampleShow, otherShow] });
    this.put(searchToolURL, { resultCount: 2, results: [sampleShow, otherShow] });
    this.put(lookupURL, { resultCount: 3, results: [sampleShow, sampleEpisode] });
    this.put(lookupToolURL, { resultCount: 3, results: [sampleShow, sampleEpisode] });
    this.put(showURL, { resultCount: 1, results: [sampleShow] });
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) {
    this.answers.set(path, { body: JSON.stringify(value), ...options });
  }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://itunes.apple.com") throw new Error("unexpected origin");
    const answer = this.answers.get(path);
    if (!answer) return new Response("not found", { status: 404 });
    const signal = init.signal;
    if (answer.delay || signal.aborted) {
      await new Promise<void>((resolve, reject) => {
        const cancel = () => { clearTimeout(timer); this.cancelled++; reject(signal.reason); };
        const timer = setTimeout(resolve, answer.delay ?? 20);
        if (signal.aborted) cancel(); else signal.addEventListener("abort", cancel, { once: true });
      });
    }
    return new Response(answer.body, { status: answer.status ?? 200, headers: { "Content-Type": "application/json" } });
  };
}
export const until = async <T>(read: () => Promise<T>, ready: (value: T) => boolean): Promise<T> => {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) { const value = await read(); if (ready(value)) return value; await new Promise(resolve => setTimeout(resolve, 10)); }
  throw new Error("condition did not settle");
};
export function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  if (!item || !("action" in item) || !item.action) throw new Error("missing action " + key);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
