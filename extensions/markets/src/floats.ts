/** strconv.ParseFloat(s, 64): decimal/hex floats, Go digit separators, specials and range errors. */
export function floatResult(raw: string): { value: number; valid: boolean } {
  const fail = { value: 0, valid: false };
  const special = /^[+-]?(?:inf|infinity)$/i.exec(raw);
  if (special && special[0].length === raw.length) return { value: raw.startsWith("-") ? -Infinity : Infinity, valid: true };
  if (/^nan$/i.test(raw) && raw.length === 3) return { value: NaN, valid: true };
  const hex = /^[+-]?0[xX]/.test(raw), digit = hex ? /[0-9a-fA-F]/ : /[0-9]/;
  for (let i = 0; i < raw.length; i++) if (raw[i] === "_") {
    const prefix = hex && i === (raw[0] === "+" || raw[0] === "-" ? 3 : 2);
    if ((!prefix && !digit.test(raw[i - 1] ?? "")) || !digit.test(raw[i + 1] ?? "")) return fail;
  }
  const text = raw.replace(/_/g, "");
  if (!hex) {
    const match = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.exec(text);
    if (!match || match[0].length !== text.length) return fail;
    const value = Number(text); return { value, valid: Number.isFinite(value) };
  }
  const match = /^([+-]?)0[xX]([0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)[pP]([+-]?\d+)$/.exec(text);
  if (!match || match[0].length !== text.length) return fail;
  const negative = match[1] === "-", fraction = match[2].split(".")[1]?.length ?? 0;
  const digits = match[2].replace(".", "").replace(/^0+/, "");
  if (!digits) return { value: negative ? -0 : 0, valid: true };
  // Keep 64 significant bits and sticky discarded digits, as Go does. Conversion
  // work and BigInt shifts stay bounded even for response-sized mantissas/exponents.
  const kept = digits.slice(0, 16), sticky = /[1-9a-fA-F]/.test(digits.slice(16));
  const mantissa = BigInt("0x" + kept), bits = mantissa.toString(2).length;
  const scale = Number(match[3]) - fraction * 4 + Math.max(digits.length - 16, 0) * 4;
  const exponent = bits - 1 + scale;
  if (exponent > 1023) return { value: negative ? -Infinity : Infinity, valid: false };
  if (exponent < -1075) return { value: negative ? -0 : 0, valid: true };
  const quantum = Math.max(exponent - 52, -1074), shift = quantum - scale;
  let rounded = mantissa;
  if (shift > 0) {
    const count = BigInt(shift), half = 1n << (count - 1n), rest = mantissa & ((1n << count) - 1n);
    rounded >>= count;
    if (rest > half || rest === half && (sticky || (rounded & 1n) !== 0n)) rounded++;
  } else rounded <<= BigInt(-shift);
  const value = (negative ? -1 : 1) * Number(rounded) * 2 ** quantum;
  return { value, valid: Number.isFinite(value) };
}
export const parseFloat64 = (raw: string): number | undefined => { const result = floatResult(raw); return result.valid ? result.value : undefined; };
