export interface Tag { start: number; end: number; raw: string; name: string; close: boolean }
/** Match the original <[^>]*> boundaries in one forward pass, even for malformed HTML. */
export function scanTags(s: string): Tag[] {
  const out: Tag[] = [];
  for (let from = 0; ; ) {
    const start = s.indexOf("<", from); if (start < 0) break;
    const end = s.indexOf(">", start + 1); if (end < 0) break;
    const raw = s.slice(start, end + 1), m = /^<(\/?)([a-z][a-z0-9]*)(?=[^a-z0-9_]|$)/iu.exec(raw);
    out.push({ start, end: end + 1, raw, name: m?.[2].toLowerCase() ?? "", close: m?.[1] === "/" });
    from = end + 1;
  }
  return out;
}
export function stripTags(s: string, replacement = ""): string {
  const parts: string[] = []; let from = 0;
  for (const tag of scanTags(s)) { parts.push(s.slice(from, tag.start), replacement); from = tag.end; }
  parts.push(s.slice(from)); return parts.join("");
}
/** Parse attributes once, rather than searching every suffix of an unclosed quote. */
export function attributes(tag: Tag): Map<string, { value: string; quote: string }> {
  const attrs = new Map<string, { value: string; quote: string }>(), s = tag.raw;
  let i = 1 + (tag.close ? 1 : 0) + tag.name.length;
  while (i < s.length - 1) {
    while (i < s.length - 1 && /[\s/]/u.test(s[i])) i++;
    const start = i;
    while (i < s.length - 1 && !/[\s=/>]/u.test(s[i])) i++;
    if (start === i) { i++; continue; }
    const name = s.slice(start, i).toLowerCase();
    while (i < s.length - 1 && /\s/u.test(s[i])) i++;
    let value = "", quote = "";
    if (s[i] === "=") {
      i++; while (i < s.length - 1 && /\s/u.test(s[i])) i++;
      if (s[i] === '"' || s[i] === "'") {
        quote = s[i++]; const begin = i;
        while (i < s.length - 1 && s[i] !== quote) i++;
        if (s[i] !== quote) break;
        value = s.slice(begin, i++);
      } else {
        const begin = i; while (i < s.length - 1 && !/[\s>]/u.test(s[i])) i++;
        value = s.slice(begin, i);
      }
    }
    if (!attrs.has(name)) attrs.set(name, { value, quote });
  }
  return attrs;
}
/** Pre-index the nearest closing tag; unmatched opens never rescan their suffix. */
export function nextClose(tags: Tag[], names: ReadonlySet<string>): number[] {
  const out = new Array<number>(tags.length); let next = -1;
  for (let i = tags.length - 1; i >= 0; i--) {
    out[i] = next;
    if (tags[i].close && names.has(tags[i].name) && tags[i].raw.toLowerCase() === `</${tags[i].name}>`) next = i;
  }
  return out;
}
