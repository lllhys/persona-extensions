import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const response = {
  query: { pages: {
    6926930: { pageid: 6926930, ns: 6, title: "File:Tour Eiffel Wikimedia Commons.jpg", imageinfo: [{
      url: "https://upload.wikimedia.org/wikipedia/commons/a/a8/Tour_Eiffel_Wikimedia_Commons.jpg?utm_source=commons.wikimedia.org",
      descriptionurl: "https://commons.wikimedia.org/wiki/File:Tour_Eiffel_Wikimedia_Commons.jpg",
      thumburl: "https://upload.wikimedia.org/wikipedia/commons/thumb/a/a8/Tour_Eiffel_Wikimedia_Commons.jpg/640px-Tour_Eiffel_Wikimedia_Commons.jpg",
      thumbwidth: 640, thumbheight: 427,
      extmetadata: {
        ImageDescription: { value: "The <b>Eiffel Tower</b> in Paris." },
        Artist: { value: "Example Photographer" },
        LicenseShortName: { value: "Public domain" },
        LicenseUrl: { value: "https://creativecommons.org/publicdomain/mark/1.0/" },
      },
    }] },
  } },
};
export class Fixture {
  clock = Date.UTC(2026, 10, 10);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put("/w/api.php?action=query&generator=search&gsrsearch=File%3AEiffel+Tower&gsrnamespace=6&gsrlimit=20&prop=imageinfo&iiprop=url%7Cextmetadata%7Csize&iiurlwidth=640&format=json", response);
    this.put("/w/api.php?action=query&generator=search&gsrsearch=File%3AEiffel+Tower&gsrnamespace=6&gsrlimit=10&prop=imageinfo&iiprop=url%7Cextmetadata%7Csize&iiurlwidth=640&format=json", response);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.answers.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://commons.wikimedia.org") throw new Error("unexpected origin");
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
