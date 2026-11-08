/** Calendar checks precede Date conversion; Date.parse alone normalizes invalid days. */
function calendar(year: number, month: number, day: number, hour: number, minute: number, second: number): boolean {
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1] && hour <= 23 && minute <= 59 && second <= 59;
}
function instant(year: number, month: number, day: number, hour: number, minute: number, second: number, ms: number, offset: number): number {
  // Date.UTC treats years 00..99 as 1900..1999. Set the full year explicitly.
  const d = new Date(0); d.setUTCFullYear(year, month - 1, day); d.setUTCHours(hour, minute, second, ms);
  const at = d.getTime() - offset * 60_000;
  if (!Number.isFinite(at)) throw new Error("unsupported topic time range");
  return at;
}
/** Go time.Time JSON uses RFC3339, including arbitrary fractional precision.
 * Its four-digit year range (0000..9999) fits Date and Intl without truncation. */
export function topicTime(value: unknown): number | null {
  // Absence is distinct from the valid Unix epoch (zero milliseconds).
  if (value === undefined || value === null) return null;
  if (typeof value !== "string") throw new Error("invalid topic time type");
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:[.,](\d+))?(Z|[+-]\d{2}:\d{2})$/u.exec(value);
  if (!m) throw new Error("invalid topic time");
  const [year, month, day, hour, minute, second] = m.slice(1, 7).map(Number);
  if (!calendar(year, month, day, hour, minute, second)) throw new Error("invalid topic calendar");
  const zone = m[8], oh = zone === "Z" ? 0 : Number(zone.slice(1, 3)), om = zone === "Z" ? 0 : Number(zone.slice(4));
  if (oh > 23 || om > 59) throw new Error("invalid topic offset");
  const offset = (zone[0] === "-" ? -1 : 1) * (oh * 60 + om);
  const fraction = (m[7] ?? "").slice(0, 9); // Go keeps at most nanosecond precision.
  const at = instant(year, month, day, hour, minute, second, Number(fraction.slice(0, 3).padEnd(3, "0")), offset);
  // IsZero compares the complete instant, not its millisecond projection. A 1 ns
  // offset from year 0001 must still render even though Date rounds it to this ms.
  return at === -62135596800000 && !/[1-9]/u.test(fraction) ? null : at;
}
const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
/** RSS retains Go's best-effort RFC1123Z behavior: malformed dates have no metadata. */
export function feedTime(value: string): number {
  const m = /^(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), (\d{2}) ([A-Z][a-z]{2}) (\d{4}) (\d{2}):(\d{2}):(\d{2}) ([+-])(\d{2})(\d{2})$/u.exec(value.trim());
  if (!m) return 0;
  const day = Number(m[1]), month = months.indexOf(m[2]) + 1, year = Number(m[3]), hour = Number(m[4]), minute = Number(m[5]), second = Number(m[6]);
  if (!calendar(year, month, day, hour, minute, second) || Number(m[8]) > 23 || Number(m[9]) > 59) return 0;
  return instant(year, month, day, hour, minute, second, 0, (m[7] === "-" ? -1 : 1) * (Number(m[8]) * 60 + Number(m[9])));
}
