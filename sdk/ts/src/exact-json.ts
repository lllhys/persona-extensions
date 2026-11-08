const EXT_ERROR = Symbol.for("@persona/extension.ExtError");

/** An int64 field rejected by Go-style lexical or type rules, not by JSON Schema.
 * Uses the SDK's public error brand across separately bundled SDK copies. */
export class JSONIntegerError extends TypeError {
  readonly [EXT_ERROR] = true;
  readonly code = "invalid_params";
}

const MAX_EXACT_LENGTH = 8 << 20;
const VALIDATED_JSON = Symbol("validated JSON slice");
const whitespace = (c: string) => c === " " || c === "\t" || c === "\r" || c === "\n";

function skipSpace(text: string, at: number): number {
  while (whitespace(text[at])) at++;
  return at;
}

// Input is already JSON.parse-validated. Scan strings and nested containers without
// a recursive reviver or building a second object graph for unknown fields.
function stringEnd(text: string, at: number): number {
  for (at++; at < text.length; at++) {
    if (text[at] === "\\") at++;
    else if (text[at] === '"') return at + 1;
  }
  throw new SyntaxError("unterminated JSON string");
}

function valueEnd(text: string, at: number): number {
  if (text[at] === '"') return stringEnd(text, at);
  if (text[at] === "{" || text[at] === "[") {
    let depth = 0;
    for (; at < text.length; at++) {
      const c = text[at];
      if (c === '"') at = stringEnd(text, at) - 1;
      else if (c === "{" || c === "[") depth++;
      else if ((c === "}" || c === "]") && --depth === 0) return at + 1;
    }
    throw new SyntaxError("unterminated JSON container");
  }
  while (at < text.length && !whitespace(text[at]) && !",]}".includes(text[at])) at++;
  return at;
}

// Go's folded matching for ASCII struct field names also accepts these two
// Unicode SimpleFold aliases. Do not use full Unicode uppercasing (ß is not SS).
function foldASCII(name: string): string {
  return name.replace(/[a-zſK]/g, c => c === "K" ? "K" : c === "ſ" ? "S" : c.toUpperCase());
}

/** A validated slice of the original JSON text. Numbers are never reconstructed from Number.
 * Object member lookup follows json.RawMessage map semantics: the last decoded key wins,
 * including null. Use int64Fields for Go struct duplicate/null/error behavior instead. */
export class ExactJSON {
  private constructor(readonly raw: string, validated: symbol) {
    if (validated !== VALIDATED_JSON) throw new TypeError("use ExactJSON.parse to validate JSON");
    Object.freeze(this);
  }

  /** Validates JSON syntax, keeping its original tokens. The opt-in view is limited to 8 MiB
   * of UTF-16 text; transport byte/resource limits remain the host's responsibility. */
  static parse(raw: string): ExactJSON {
    if (typeof raw !== "string") throw new TypeError("exact JSON input must be text");
    if (raw.length > MAX_EXACT_LENGTH) throw new RangeError("exact JSON exceeds 8 MiB of text");
    JSON.parse(raw);
    return new ExactJSON(raw.trim(), VALIDATED_JSON);
  }

  private *entries(): Generator<[string, ExactJSON]> {
    if (this.raw === "null") return;
    if (this.raw[0] !== "{") throw new JSONIntegerError("expected a JSON object or null");
    let at = skipSpace(this.raw, 1);
    while (this.raw[at] !== "}") {
      const end = stringEnd(this.raw, at);
      const key = JSON.parse(this.raw.slice(at, end)) as string;
      at = skipSpace(this.raw, skipSpace(this.raw, end) + 1);
      const value = valueEnd(this.raw, at);
      yield [key, new ExactJSON(this.raw.slice(at, value), VALIDATED_JSON)];
      at = skipSpace(this.raw, value);
      if (this.raw[at] === ",") at = skipSpace(this.raw, at + 1);
    }
  }

  /** The final occurrence of an exact, JSON-decoded key; null/nonexistent objects have no members. */
  member(name: string): ExactJSON | undefined {
    let found: ExactJSON | undefined;
    for (const [key, value] of this.entries()) if (key === name) found = value;
    return found;
  }

  /** Ordered raw struct-field occurrences for an extension's custom decoder, including
   * mixed string/integer IDs. The decoder must preserve its own null/type/error rules. */
  *fieldValues(name: string): Generator<ExactJSON> {
    if (!name || /[^\x00-\x7f]/.test(name)) throw new TypeError("field name must be ASCII");
    const folded = foldASCII(name);
    for (const [key, value] of this.entries()) if (foldASCII(key) === folded) yield value;
  }

  /** Last json.RawMessage struct field, including null, with Go matching for an ASCII
   * JSON field name. Unlike member(), this also matches case-folded aliases. This does
   * not merge repeated nested typed structs; decode the selected RawMessage separately. */
  field(name: string): ExactJSON | undefined {
    let found: ExactJSON | undefined;
    for (const value of this.fieldValues(name)) found = value;
    return found;
  }

  /** Decode a Go int64 token: only -?(0|[1-9][0-9]*) within signed 64 bits.
   * null leaves an existing scalar unchanged; on a fresh field it is zero. */
  int64(previous = 0n): bigint {
    if (this.raw === "null") return previous;
    if (!/^-?(?:0|[1-9][0-9]*)$/.test(this.raw)) throw new JSONIntegerError("expected a lexical JSON int64");
    // Bound conversion before BigInt allocation, even for a very long valid JSON integer.
    const negative = this.raw[0] === "-";
    const digits = negative ? this.raw.slice(1) : this.raw;
    const limit = negative ? "9223372036854775808" : "9223372036854775807";
    if (digits.length > limit.length || (digits.length === limit.length && digits > limit)) {
      throw new JSONIntegerError("JSON integer is outside signed int64");
    }
    return BigInt(this.raw);
  }

  /** Decode a fresh Go struct of int64 fields (ASCII JSON field names, no embedding).
   * Missing/null fields are zero; duplicates are read in order, null does not overwrite,
   * and a wrong type/decimal/exponent/overflow is an error even if a later value is valid.
   * Unknown fields are ignored. Matching prefers exact names, then Go's ASCII folded names.
   * This is not JSON Schema's mathematical integer validation. */
  int64Fields<const K extends string>(names: readonly K[]): Record<K, bigint> {
    const values = Object.create(null) as Record<K, bigint>;
    const exact = new Map<string, K>(), folded = new Map<string, K>();
    for (const name of names) {
      if (!name || /[^\x00-\x7f]/.test(name) || exact.has(name) || folded.has(foldASCII(name))) {
        throw new TypeError("int64 field names must be distinct ASCII names with distinct folds");
      }
      exact.set(name, name);
      folded.set(foldASCII(name), name);
      values[name] = 0n;
    }
    for (const [key, value] of this.entries()) {
      const name = exact.get(key) ?? folded.get(foldASCII(key));
      if (name !== undefined) values[name] = value.int64(values[name]);
    }
    return values;
  }
}
