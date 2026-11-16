import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const bootstrap = { version: "1.0", publication: "2026-10-28T22:00:04Z", services: [[["com", "net"], ["https://rdap.verisign.com/com/v1/", "https://rdap.verisign.com/net/v1/"]]] };
export const registration = {
  objectClassName: "domain", ldhName: "EXAMPLE.COM", status: ["client transfer prohibited", "client update prohibited"],
  events: [{ eventAction: "registration", eventDate: "1995-08-14T04:00:00Z" }, { eventAction: "expiration", eventDate: "2027-08-13T04:00:00Z" }],
  entities: [{ roles: ["registrar"], vcardArray: ["vcard", [["version", {}, "text", "4.0"], ["fn", {}, "text", "Example Registrar"]]] }],
  links: [{ rel: "self", href: "https://rdap.verisign.com/com/v1/domain/example.com", type: "application/rdap+json" }],
};
export const registryURL = "https://rdap.verisign.com/com/v1/domain/example.com";
export const bootstrapURL = "https://data.iana.org/rdap/dns.json";
export class Fixture {
  clock = Date.UTC(2026, 10, 12);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() { this.put(bootstrapURL, bootstrap); this.put(registryURL, registration); }
  put(url: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.answers.set(url, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input);
    this.requests.push({ url, init }); this.hits.set(url.toString(), (this.hits.get(url.toString()) ?? 0) + 1);
    if (!["https://data.iana.org", "https://rdap.verisign.com"].includes(url.origin)) throw new Error("unexpected origin");
    const answer = this.answers.get(url.toString());
    if (!answer) return new Response("not found", { status: 404 });
    const signal = init.signal;
    if (answer.delay || signal.aborted) {
      await new Promise<void>((resolve, reject) => {
        const cancel = () => { clearTimeout(timer); this.cancelled++; reject(signal.reason); };
        const timer = setTimeout(resolve, answer.delay ?? 20);
        if (signal.aborted) cancel(); else signal.addEventListener("abort", cancel, { once: true });
      });
    }
    return new Response(answer.body, { status: answer.status ?? 200, headers: { "Content-Type": "application/rdap+json" } });
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
