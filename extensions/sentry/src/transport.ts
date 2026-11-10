import type { FetchAdapter } from "@persona/extension/ui";
import type { DSN } from "./dsn.ts";
export const bounds = { queue: 200, perMinute: 60, body: 8 << 20, response: 4 << 10, timeout: 20_000 } as const;
export type SendResult = { status: "sent" } | { status: "limited"; until: number } | { status: "failed" | "cancelled" };
function seconds(raw: string): bigint | undefined {
  if (!/^[+-]?\d+$/.test(raw)) return;
  const digits = raw.replace(/^[+-]?0*/, "");
  if (digits.length > 19) return;
  const value = BigInt((raw.startsWith("-") ? "-" : "") + (digits || "0"));
  if (value < -9223372036854775808n || value > 9223372036854775807n) return;
  return value;
}
// Go converts signed int seconds to time.Duration, whose nanoseconds wrap at 64 bits.
const after = (now: number, value: bigint) => {
  const duration = BigInt.asIntN(64, value * 1_000_000_000n);
  const milliseconds = duration / 1_000_000n - (duration < 0n && duration % 1_000_000n !== 0n ? 1n : 0n);
  return Math.floor(now) + Number(milliseconds);
};
/** The longest error/default/all-category limit wins, including on successful responses. */
export function limitedUntil(status: number, headers: Headers, now: number): number | undefined {
  let until: number | undefined;
  for (const part of (headers.get("X-Sentry-Rate-Limits") ?? "").split(",")) {
    const [raw, categories = ""] = part.trim().split(":");
    const value = seconds(raw);
    if (value === undefined) continue;
    if (!categories || categories.split(";").some(cat => cat === "error" || cat === "default")) {
      const time = after(now, value);
      if (time > (until ?? -62135596800000)) until = time; // Go time.Time's zero baseline.
    }
  }
  if (until !== undefined) return until;
  if (status !== 429) return undefined;
  const value = seconds(headers.get("Retry-After") ?? "");
  // Go accepts integer seconds only; invalid/nonpositive values default to 60.
  return after(now, value !== undefined && value > 0n ? value : 60n);
}
/** A deadline covers both fetch and the bounded response drain, even if an adapter
 * ignores AbortSignal. Redirects cannot forward the DSN key to another origin. */
export async function send(fetch: FetchAdapter, dsn: DSN, client: string, bytes: Uint8Array,
  now: number, signal: AbortSignal, timeout: number = bounds.timeout): Promise<SendResult> {
  if (signal.aborted) return { status: "cancelled" };
  if (bytes.byteLength > bounds.body) return { status: "failed" };
  const controller = new AbortController();
  let finish!: (result: SendResult) => void;
  const interrupted = new Promise<SendResult>(resolve => { finish = resolve; });
  const cancel = () => { controller.abort(); finish({ status: "cancelled" }); };
  signal.addEventListener("abort", cancel, { once: true });
  const timer = setTimeout(() => { controller.abort(); finish({ status: "failed" }); }, timeout);
  const work = (async (): Promise<SendResult> => {
    try {
      const response = await fetch(dsn.endpoint, { method: "POST", redirect: "error", signal: controller.signal,
        headers: { "Content-Type": "application/x-sentry-envelope", "X-Sentry-Auth": `Sentry sentry_version=7, sentry_client=${client}, sentry_key=${dsn.publicKey}` },
        body: bytes as BodyInit });
      if (controller.signal.aborted) { void response.body?.cancel().catch(() => {}); return { status: "cancelled" }; }
      const reader = response.body?.getReader();
      if (reader) {
        const abortRead = () => { void reader.cancel().catch(() => {}); };
        controller.signal.addEventListener("abort", abortRead, { once: true });
        try {
          let count = 0;
          while (count < bounds.response && !controller.signal.aborted) {
            const chunk = await reader.read();
            if (chunk.done) break;
            count += chunk.value.byteLength;
          }
        } catch {
          // Like Go's ignored io.Copy result, response text is not the delivery
          // receipt; the status/limit headers remain authoritative.
        } finally {
          controller.signal.removeEventListener("abort", abortRead);
          void reader.cancel().catch(() => {});
        }
      }
      if (controller.signal.aborted) return { status: "cancelled" };
      const until = limitedUntil(response.status, response.headers, now);
      if (until !== undefined) return { status: "limited", until };
      return { status: response.status >= 200 && response.status < 300 ? "sent" : "failed" };
    } catch { return { status: controller.signal.aborted && signal.aborted ? "cancelled" : "failed" }; }
  })();
  try { return await Promise.race([work, interrupted]); }
  finally { clearTimeout(timer); signal.removeEventListener("abort", cancel); }
}
