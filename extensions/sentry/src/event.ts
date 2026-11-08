import type { ErrorReport } from "@persona/extension";
import { stringifyExact, type Report } from "./params.ts";
import type { DSN } from "./dsn.ts";
const utf8 = new TextEncoder();
export const platforms: Record<string, string> = {
  hub: "go", manager: "go", web: "javascript", android: "java",
  ios: "cocoa", mac: "cocoa", watch: "cocoa", vision: "cocoa",
};
export interface Options { environment: string; client: string }
/** Mirror Go time.Parse(RFC3339Nano), including its accepted single-digit hour,
 * comma fraction and offset boundaries. Check the calendar before constructing
 * a Date: Date.parse normalizes invalid days and rejects valid Go forms. Go's
 * four-digit years fit Date and exact millisecond arithmetic, including year 0.
 * Go UnixMilli floors negative sub-millisecond instants; do not round to zero. */
function breadcrumbMillis(time?: string): number | undefined {
  if (!time) return;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{1,2}):(\d{2}):(\d{2})(?:[.,](\d+))?(Z|([+-])(\d{2}):(\d{2}))$/.exec(time);
  if (!match) return;
  const [, y, m, d, h, minute, second, fraction, zone, sign, oh, om] = match;
  const year = Number(y), month = Number(m), day = Number(d), hour = Number(h);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (month < 1 || month > 12 || day < 1 || day > days[month - 1] || hour > 23 || Number(minute) > 59 || Number(second) > 59) return;
  if (zone !== "Z" && (Number(oh) > 24 || Number(om) > 60)) return;
  const date = new Date(0);
  date.setUTCFullYear(year, month - 1, day);
  date.setUTCHours(hour, Number(minute), Number(second), Number((fraction ?? "").slice(0, 3).padEnd(3, "0")));
  const offset = zone === "Z" ? 0 : (Number(oh) * 60 + Number(om)) * (sign === "-" ? -1 : 1);
  return date.getTime() - offset * 60_000;
}
export async function eventID(report: Pick<ErrorReport, "source" | "id">): Promise<string> {
  const sum = await crypto.subtle.digest("SHA-256", utf8.encode(`${report.source}\0${report.id}`));
  return [...new Uint8Array(sum).slice(0, 16)].map(byte => byte.toString(16).padStart(2, "0")).join("");
}
/** The hub owns scrubbing and deduplication. Whitelist the wire fields; never add
 * user, host, IP, arbitrary extras or SDK IP inference to the scrubbed report. */
export async function event(report: Report, options: Options): Promise<Record<string, any>> {
  const tags = { ...report.tags, source: report.source, kind: report.kind } as Record<string, string>;
  if (report.previous_launch) tags.previous_launch = "true";
  const out: Record<string, any> = {
    event_id: await eventID(report), timestamp: report.time, platform: Object.hasOwn(platforms, report.source) ? platforms[report.source] : "",
    level: report.level, logger: `persona.${report.source}`, environment: options.environment,
    tags, extra: { count: report.count ?? 0, previous_launch: report.previous_launch ?? false, received_at: report.received_at ?? "" },
    sdk: { name: "persona.sentry", version: options.client.replace(/^persona-sentry\//, ""), settings: { infer_ip: "never" } },
  };
  if (report.release) out.release = `persona-${report.source}@${report.release}${report.build ? `+${report.build}` : ""}`;
  if (report.build) out.dist = report.build;
  if (report.fingerprint) out.fingerprint = [report.fingerprint];
  if (report.message) out.message = { formatted: report.message };
  const contexts: Record<string, unknown> = {};
  if (report.os) contexts.os = { name: report.os, version: report.os_version ?? "" };
  if (report.device_model) contexts.device = { model: report.device_model };
  if (Object.keys(contexts).length) out.contexts = contexts;
  if (report.exceptions?.length) out.exception = { values: [...report.exceptions].reverse().map(exception => ({
    type: exception.type, value: exception.value ?? "",
    mechanism: { type: report.kind, handled: report.kind === "exception" || report.kind === "log" },
    ...(exception.frames?.length ? { stacktrace: { frames: exception.frames.map(frame => ({
      function: frame.function ?? "", in_app: frame.in_app ?? false,
      ...(frame.module ? { module: frame.module } : {}), ...(frame.file ? { filename: frame.file } : {}),
      ...((frame.line ?? 0) > 0 ? { lineno: frame.line } : {}), ...((frame.column ?? 0) > 0 ? { colno: frame.column } : {}),
    })) } } : {}),
  })) };
  if (report.breadcrumbs?.length) out.breadcrumbs = { values: report.breadcrumbs.map(crumb => {
    const millis = breadcrumbMillis(crumb.time);
    return { category: crumb.category, message: crumb.message ?? "", ...(crumb.level ? { level: crumb.level } : {}),
      ...(millis !== undefined ? { timestamp: millis / 1000 } : {}) };
  }) };
  return out;
}
export function envelope(dsn: DSN, value: Record<string, any>, now: number): Uint8Array {
  const body = stringifyExact(value);
  const header = JSON.stringify({ event_id: value.event_id, dsn: dsn.safe, sent_at: new Date(now).toISOString().replace(/\.\d{3}Z$/, "Z") });
  const item = JSON.stringify({ type: "event", length: utf8.encode(body).length, content_type: "application/json" });
  return utf8.encode(`${header}\n${item}\n${body}\n`);
}
