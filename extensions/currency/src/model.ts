export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 256 << 10, fresh: 6 * 60 * 60_000, cache: 32, cacheBytes: 256 << 10, gap: 200 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "unsupported" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
function text(value: unknown, most: number): string {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const result = oneLine(value);
  if (!result || Array.from(result).length > most) throw new ReadError("invalid");
  return result;
}
export const currencies = ["AUD", "BRL", "CAD", "CHF", "CNY", "CZK", "DKK", "EUR", "GBP", "HKD", "HUF", "IDR", "ILS", "INR", "ISK", "JPY", "KRW", "MXN", "MYR", "NOK", "NZD", "PHP", "PLN", "RON", "SEK", "SGD", "THB", "TRY", "USD", "ZAR"] as const;
export type Currency = typeof currencies[number];
export function currency(value: unknown): Currency {
  const code = text(value, 3).toUpperCase();
  if (!(currencies as readonly string[]).includes(code)) throw new ReadError("unsupported");
  return code as Currency;
}
export function ratesPath(base: Currency, quote: Currency): string {
  return `/v2/providers/ecb/rates?${new URLSearchParams({ base: base.toLowerCase(), quotes: quote.toLowerCase() })}`;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
export interface Rate { base: Currency; quote: Currency; rate: string; date: string; at: number; stale?: ReadError }
function decimal(value: unknown, name: "rate" | "amount"): string {
  if (typeof value === "number" && (!Number.isFinite(value) || !/^[0-9]+(?:\.[0-9]+)?$/.test(String(value)))) throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  if (typeof value !== "string" && typeof value !== "number") throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  const raw = typeof value === "string" ? value : String(value);
  if (/^[\t\n\v\f\r ]/.test(raw) || /[\t\n\v\f\r ]$/.test(raw) || /[\u0000-\u001f\u007f]/u.test(raw)) throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  if (!/^[0-9]{1,18}(?:\.[0-9]{1,18})?$/.test(raw)) throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  const [whole, fraction = ""] = raw.split(".");
  const normalized = `${whole.replace(/^0+(?=\d)/, "")}${fraction ? `.${fraction}` : ""}`;
  if (name === "rate" && normalized === "0") throw new ReadError("bad_answer");
  return normalized;
}
export function amount(value: unknown): string { return decimal(value, "amount"); }
export function parseRate(raw: unknown, base: Currency, quote: Currency, at: number): Rate {
  const rows = Array.isArray(raw) ? raw : (() => { throw new ReadError("bad_answer"); })();
  if (rows.length !== 1) throw new ReadError("bad_answer");
  const row = object(rows[0]);
  if (row.base?.toString().toUpperCase() !== base || row.quote?.toString().toUpperCase() !== quote) throw new ReadError("bad_answer");
  if (typeof row.date !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(row.date) || Number.isNaN(Date.parse(row.date + "T00:00:00Z")) || new Date(row.date + "T00:00:00Z").toISOString().slice(0, 10) !== row.date) throw new ReadError("bad_answer");
  return { base, quote, rate: decimal(row.rate, "rate"), date: row.date, at };
}
function decimalParts(raw: string): { coefficient: bigint; scale: number } {
  const [whole, fraction = ""] = raw.split(".");
  return { coefficient: BigInt(whole + fraction), scale: fraction.length };
}
export function multiply(a: string, b: string): string {
  const x = decimalParts(a), y = decimalParts(b), negative = x.coefficient < 0n !== (y.coefficient < 0n);
  const abs = (n: bigint) => n < 0n ? -n : n;
  const digits = (abs(x.coefficient) * abs(y.coefficient)).toString().padStart(x.scale + y.scale + 1, "0");
  const split = digits.length - x.scale - y.scale;
  const fraction = digits.slice(split).replace(/0+$/, "");
  return (negative ? "-" : "") + digits.slice(0, split).replace(/^0+(?=\d)/, "") + (fraction ? `.${fraction}` : "");
}
export function roundedNumber(value: string, maximumDigits = 6): string {
  const parts = decimalParts(value);
  return rationalText(reduce(parts.coefficient, 10n ** BigInt(parts.scale)), maximumDigits);
}
export function formatForDisplay(value: string, locale = "en"): string {
  const rounded = roundedNumber(value, 6);
  const [whole, fraction = ""] = rounded.split(".");
  if (whole.replace("-", "").length > 15) return rounded;
  return new Intl.NumberFormat(locale, { maximumFractionDigits: Math.min(6, fraction.length) }).format(Number(rounded));
}
export interface Conversion { amount: string; from: string; to: string; result: string; inverse?: string }
export function currencyConversion(value: unknown, rate: Rate): Conversion {
  return { amount: amount(value), from: rate.base, to: rate.quote, result: multiply(amount(value), rate.rate) };
}
export type Dimension = "length" | "mass" | "temperature" | "volume" | "area" | "speed" | "duration" | "data";
export const dimensions: Dimension[] = ["length", "mass", "temperature", "volume", "area", "speed", "duration", "data"];
interface Unit { dimension: Dimension; symbol: string; numerator: string; denominator: string; aliases: string[] }
const exact = (scale: string) => ({ numerator: scale, denominator: "1" });
const fraction = (numerator: string, denominator: string) => ({ numerator, denominator });
const units: Unit[] = [
  { dimension: "length", symbol: "mm", ...fraction("1", "1000"), aliases: ["millimeter", "millimeters", "毫米"] },
  { dimension: "length", symbol: "cm", ...fraction("1", "100"), aliases: ["centimeter", "centimeters", "厘米"] },
  { dimension: "length", symbol: "m", ...exact("1"), aliases: ["meter", "meters", "metre", "米"] },
  { dimension: "length", symbol: "km", ...exact("1000"), aliases: ["kilometer", "kilometers", "千米", "公里"] },
  { dimension: "length", symbol: "in", ...fraction("254", "10000"), aliases: ["inch", "inches", "英寸"] },
  { dimension: "length", symbol: "ft", ...fraction("3048", "10000"), aliases: ["foot", "feet", "英尺"] },
  { dimension: "length", symbol: "yd", ...fraction("9144", "10000"), aliases: ["yard", "yards", "码"] },
  { dimension: "length", symbol: "mi", ...fraction("1609344", "1000"), aliases: ["mile", "miles", "英里"] },
  { dimension: "mass", symbol: "mg", ...fraction("1", "1000000"), aliases: ["milligram", "milligrams", "毫克"] },
  { dimension: "mass", symbol: "g", ...fraction("1", "1000"), aliases: ["gram", "grams", "克"] },
  { dimension: "mass", symbol: "kg", ...exact("1"), aliases: ["kilogram", "kilograms", "千克", "公斤"] },
  { dimension: "mass", symbol: "t", ...exact("1000"), aliases: ["tonne", "tonnes", "metric ton", "吨"] },
  { dimension: "mass", symbol: "oz", ...fraction("28349523125", "1000000000000"), aliases: ["ounce", "ounces", "盎司"] },
  { dimension: "mass", symbol: "lb", ...fraction("45359237", "100000000"), aliases: ["lbs", "pound", "pounds", "磅"] },
  { dimension: "temperature", symbol: "°C", ...exact("1"), aliases: ["c", "celsius", "centigrade", "摄氏度"] },
  { dimension: "temperature", symbol: "°F", ...fraction("5", "9"), aliases: ["f", "fahrenheit", "华氏度"] },
  { dimension: "temperature", symbol: "K", ...exact("1"), aliases: ["k", "kelvin", "开尔文"] },
  { dimension: "volume", symbol: "mL", ...fraction("1", "1000"), aliases: ["ml", "milliliter", "milliliters", "毫升"] },
  { dimension: "volume", symbol: "L", ...exact("1"), aliases: ["l", "liter", "liters", "litre", "升"] },
  { dimension: "volume", symbol: "m³", ...exact("1000"), aliases: ["m3", "cubic meter", "cubic meters", "立方米"] },
  { dimension: "volume", symbol: "US fl oz", ...fraction("295735295625", "10000000000000"), aliases: ["floz", "fluid ounce", "fluid ounces", "液量盎司"] },
  { dimension: "volume", symbol: "US cup", ...fraction("2365882365", "10000000000"), aliases: ["cup", "cups", "美制杯"] },
  { dimension: "volume", symbol: "US gal", ...fraction("3785411784", "1000000000"), aliases: ["gal", "gallon", "gallons", "加仑", "美制加仑"] },
  { dimension: "area", symbol: "m²", ...exact("1"), aliases: ["m2", "square meter", "square meters", "平方米"] },
  { dimension: "area", symbol: "km²", ...exact("1000000"), aliases: ["km2", "square kilometer", "square kilometers", "平方千米"] },
  { dimension: "area", symbol: "ft²", ...fraction("9290304", "100000000"), aliases: ["ft2", "sqft", "square foot", "square feet", "平方英尺"] },
  { dimension: "area", symbol: "acre", ...fraction("40468564224", "10000000"), aliases: ["acres", "英亩"] },
  { dimension: "area", symbol: "ha", ...exact("10000"), aliases: ["hectare", "hectares", "公顷"] },
  { dimension: "speed", symbol: "m/s", ...exact("1"), aliases: ["mps", "meter per second", "meters per second", "米每秒"] },
  { dimension: "speed", symbol: "km/h", ...fraction("5", "18"), aliases: ["kmh", "kph", "公里每小时", "千米每小时"] },
  { dimension: "speed", symbol: "mph", ...fraction("1397", "3125"), aliases: ["miles per hour", "英里每小时"] },
  { dimension: "speed", symbol: "kn", ...fraction("463", "900"), aliases: ["knot", "knots", "节"] },
  { dimension: "duration", symbol: "s", ...exact("1"), aliases: ["sec", "second", "seconds", "秒"] },
  { dimension: "duration", symbol: "min", ...exact("60"), aliases: ["minute", "minutes", "分钟"] },
  { dimension: "duration", symbol: "h", ...exact("3600"), aliases: ["hr", "hour", "hours", "小时"] },
  { dimension: "duration", symbol: "d", ...exact("86400"), aliases: ["day", "days", "天", "日"] },
  { dimension: "duration", symbol: "week", ...exact("604800"), aliases: ["weeks", "周", "星期"] },
  { dimension: "data", symbol: "B", ...exact("1"), aliases: ["byte", "bytes", "字节"] },
  { dimension: "data", symbol: "KB", ...exact("1000"), aliases: ["kilobyte", "kilobytes", "千字节"] },
  { dimension: "data", symbol: "MB", ...exact("1000000"), aliases: ["megabyte", "megabytes", "兆字节"] },
  { dimension: "data", symbol: "GB", ...exact("1000000000"), aliases: ["gigabyte", "gigabytes", "十亿字节"] },
  { dimension: "data", symbol: "TB", ...exact("1000000000000"), aliases: ["terabyte", "terabytes"] },
  { dimension: "data", symbol: "KiB", ...exact("1024"), aliases: ["kibibyte", "kibibytes"] },
  { dimension: "data", symbol: "MiB", ...exact("1048576"), aliases: ["mebibyte", "mebibytes"] },
  { dimension: "data", symbol: "GiB", ...exact("1073741824"), aliases: ["gibibyte", "gibibytes"] },
  { dimension: "data", symbol: "TiB", ...exact("1099511627776"), aliases: ["tebibyte", "tebibytes"] },
];
export function unit(value: unknown, dimension?: Dimension): Unit {
  const query = text(value, 40), folded = query.toLocaleLowerCase("en").replace(/[\s_°]/gu, "");
  const found = units.filter(unit => [unit.symbol, ...unit.aliases].some(alias => alias.toLocaleLowerCase("en").replace(/[\s_°]/gu, "") === folded));
  if (!found.length || found.some(item => item.dimension !== found[0].dimension) || (dimension && found[0].dimension !== dimension)) throw new ReadError("unsupported");
  return found[0];
}
export function dimension(value: unknown): Dimension {
  const query = text(value, 20).toLowerCase() as Dimension;
  if (!dimensions.includes(query)) throw new ReadError("unsupported");
  return query;
}
export function availableUnits(value: Dimension): string[] { return units.filter(unit => unit.dimension === value).map(unit => unit.symbol); }
export function unitConversion(value: unknown, from: unknown, to: unknown, wanted?: Dimension): Conversion {
  const input = amount(value), a = unit(from, wanted), b = unit(to, a.dimension);
  if (a.dimension !== b.dimension) throw new ReadError("unsupported");
  // A linear unit's numerator and denominator scale the base unit. Affine
  // temperature uses explicit kelvin formulas below, so its offset is never
  // mistaken for a base-unit displacement.
  if (a.dimension === "temperature") {
    const celsius = a.symbol === "°F" ? rationalTimes(rationalPlus(inputRational(input), "-32", "1"), "5", "9")
      : a.symbol === "K" ? rationalPlus(inputRational(input), "-27315", "100") : inputRational(input);
    const result = b.symbol === "°F" ? rationalPlus(rationalTimes(celsius, "9", "5"), "32", "1")
      : b.symbol === "K" ? rationalPlus(celsius, "27315", "100") : celsius;
    return { amount: input, from: a.symbol, to: b.symbol, result: rationalText(result), inverse: rationalText(rationalTimes(rationalTimes(inputRational("1"), a.numerator, a.denominator), b.denominator, b.numerator)) };
  }
  const absolute = rationalTimes(inputRational(input), a.numerator, a.denominator);
  const result = rationalTimes(absolute, b.denominator, b.numerator);
  const inverse = rationalTimes({ n: BigInt(a.numerator), d: BigInt(a.denominator) }, b.denominator, b.numerator);
  return { amount: input, from: a.symbol, to: b.symbol, result: rationalText(result), inverse: rationalText(inverse) };
}
interface Rational { n: bigint; d: bigint }
function rationalTimes(value: string | Rational, numerator: string, denominator: string): Rational {
  let n: bigint, d: bigint;
  if (typeof value === "string") {
    const input = decimalParts(value); n = input.coefficient; d = 10n ** BigInt(input.scale);
  } else { n = value.n; d = value.d; }
  return reduce(n * BigInt(numerator), d * BigInt(denominator));
}
function rationalPlus(value: Rational, numerator: string, denominator: string): Rational {
  return reduce(value.n * BigInt(denominator) + BigInt(numerator) * value.d, value.d * BigInt(denominator));
}
function inputRational(value: string): Rational {
  const parts = decimalParts(value);
  return { n: parts.coefficient, d: 10n ** BigInt(parts.scale) };
}
function reduce(n: bigint, d: bigint): Rational {
  if (d <= 0n) throw new ReadError("bad_answer");
  let a = n < 0n ? -n : n, b = d;
  while (b) [a, b] = [b, a % b];
  const divisor = a || 1n;
  return { n: n / divisor, d: d / divisor };
}
function rationalText(value: Rational, maximumDigits = 18): string {
  const negative = value.n < 0n, numerator = negative ? -value.n : value.n;
  const whole = numerator / value.d;
  let remainder = numerator % value.d, digits = "";
  const recurring = value.d / (2n ** countFactor(value.d, 2n)) / (5n ** countFactor(value.d, 5n)) !== 1n;
  for (let i = 0; i < maximumDigits && remainder; i++) {
    remainder *= 10n; const digit = remainder / value.d;
    digits += digit.toString(); remainder %= value.d;
    if (recurring && i === maximumDigits - 1 && remainder * 2n >= value.d) {
      const rounded = (BigInt(digits) + 1n).toString().padStart(maximumDigits, "0");
      return (negative ? "-" : "") + formatRounded(whole, rounded, maximumDigits);
    }
  }
  digits = digits.replace(/0+$/, "");
  return (negative && (whole || digits) ? "-" : "") + whole + (digits ? `.${digits}` : "");
}
function formatRounded(whole: bigint, digits: string, maximumDigits: number): string {
  if (digits.length > maximumDigits) return (whole + 1n).toString();
  const trimmed = digits.replace(/0+$/, "");
  return whole.toString() + (trimmed ? `.${trimmed}` : "");
}
function countFactor(value: bigint, factor: bigint): bigint {
  let count = 0n, rest = value;
  while (rest % factor === 0n) { rest /= factor; count++; }
  return count;
}
