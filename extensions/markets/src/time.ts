export type Milliseconds = number | bigint;
/** Stored alerts use RFC3339, not Date.parse's permissive date/calendar normalization. */
export function validRFC3339(value: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match || match[0].length !== value.length) return false;
  const [year, month, date, hour, minute, second] = match.slice(1, 7).map(Number);
  const days = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  const zone = match[7];
  return month >= 1 && month <= 12 && date >= 1 && date <= days[month - 1] && hour <= 23 && minute <= 59 && second <= 59
    && (zone === "Z" || Number(zone.slice(1, 3)) <= 23 && Number(zone.slice(4)) <= 59);
}
export const milliseconds = (n: bigint | undefined): Milliseconds => n === undefined ? 0 : n >= BigInt(Number.MIN_SAFE_INTEGER) && n <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(n) : n;
const day = 86_400_000n;
const floor = (a: bigint, b: bigint) => { const q = a / b; return a % b < 0n ? q - 1n : q; };
function civil(at: bigint) {
  const days = floor(at, day), within = at - days * day, z = days + 719468n;
  const era = floor(z, 146097n), doe = z - era * 146097n;
  const yoe = (doe - doe / 1460n + doe / 36524n - doe / 146096n) / 365n;
  const doy = doe - (365n * yoe + yoe / 4n - yoe / 100n), mp = (5n * doy + 2n) / 153n;
  const month = mp + (mp < 10n ? 3n : -9n), date = doy - (153n * mp + 2n) / 5n + 1n;
  return { year: Number(yoe + era * 400n + (month <= 2n ? 1n : 0n)), month: Number(month), day: Number(date), hour: Number(within / 3600000n), minute: Number(within / 60000n % 60n), second: Number(within / 1000n % 60n) };
}
export function utcDate(y: number, m: number, d: number, h = 0, mi = 0, s = 0) {
  const date = new Date(0); date.setUTCFullYear(y, m - 1, d); date.setUTCHours(h, mi, s, 0); return date.getTime();
}
export const timeZone = (zone?: string) => !zone || zone === "Local" ? undefined : zone;
function calendarMilliseconds(year: number, month: number, date: number, hour: number, minute: number, second: number) {
  const y = BigInt(year - (month <= 2 ? 1 : 0)), era = floor(y, 400n), yoe = y - era * 400n;
  const mp = BigInt(month + (month > 2 ? -3 : 9));
  const doy = (153n * mp + 2n) / 5n + BigInt(date) - 1n;
  const days = era * 146097n + yoe * 365n + yoe / 4n - yoe / 100n + doy - 719468n;
  return days * day + BigInt(hour * 3600 + minute * 60 + second) * 1000n;
}
function offset(at: number, zone?: string) {
  const p = Object.fromEntries(new Intl.DateTimeFormat("en-US", { timeZone: timeZone(zone), era: "short", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).formatToParts(at).map(p => [p.type, p.value]));
  const year = p.era === "BC" ? 1 - Number(p.year) : Number(p.year);
  return Number(calendarMilliseconds(year, +p.month, +p.day, +p.hour, +p.minute, +p.second) - BigInt(Math.floor(at / 1000)) * 1000n);
}
export function displayZone(zone: string | undefined, at: number) {
  if (timeZone(zone)) return zone!;
  const minutes = Math.trunc(offset(at, zone) / 60000), absolute = Math.abs(minutes);
  return `UTC${minutes < 0 ? "-" : "+"}${String(Math.floor(absolute / 60)).padStart(2, "0")}:${String(absolute % 60).padStart(2, "0")}`;
}
/** Full int64 Unix milliseconds; only the zone offset, never the timestamp, uses Intl. */
export function timeParts(at: Milliseconds, zone?: string): Record<string, string> {
  if (typeof at === "number" && (!Number.isFinite(at) || !Number.isInteger(at))) throw new Error("Invalid market timestamp");
  const exact = BigInt(at), utc = civil(exact);
  let delta = 0;
  if (zone !== "UTC" && zone !== "Etc/UTC") {
    // TZif's future rules repeat with the 400-year Gregorian cycle. Before its
    // first transition, use the historical offset rather than a future DST rule.
    const probe = exact >= -8640000000000000n && exact <= 8640000000000000n ? Number(exact)
      : utcDate(utc.year < 0 ? 0 : 2400 + utc.year % 400, utc.month, utc.day, utc.hour, utc.minute, utc.second);
    delta = offset(probe, zone);
  }
  const p = civil(exact + BigInt(delta));
  return Object.fromEntries(Object.entries(p).map(([key, value]) => [key, key === "year" ? (value < 0 ? "-" : "") + String(Math.abs(value)).padStart(4, "0") : String(value).padStart(2, "0")]));
}
