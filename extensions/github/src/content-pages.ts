export interface ContentPage { text: string; startLine: number }
const budget = 100000;
/** Replace the visible window rather than appending beyond the existing body/frame budget. */
export function contentPages(text: string, linesPerPage = 2000): ContentPage[] {
  const pages: ContentPage[] = []; let current = "", size = 0, count = 0, line = 1, startLine = 1;
  const flush = () => { if (current) pages.push({ text: current, startLine }); current = ""; size = 0; count = 0; startLine = line; };
  for (const row of text.match(/[^\n]*\n|[^\n]+$/g) ?? []) {
    const encoded = new TextEncoder().encode(row);
    if (current && (count >= linesPerPage || size + encoded.length > budget)) flush();
    let offset = 0;
    while (offset < encoded.length) {
      let end = Math.min(encoded.length, offset + budget - size);
      while (end < encoded.length && (encoded[end]! & 0xc0) === 0x80) end--;
      current += new TextDecoder().decode(encoded.subarray(offset, end)); size += end - offset; offset = end;
      if (offset < encoded.length) flush();
    }
    count++; if (row.endsWith("\n")) line++;
  }
  flush(); return pages.length ? pages : [{ text: "", startLine: 1 }];
}
export const pageIndex = (requested: number | undefined, length: number, fallback = 0): number => Math.max(0, Math.min(requested ?? fallback, Math.max(0, length - 1)));
export function unstamped(log: string): string { return log.replace(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z ?/gm, ""); }
/** Match Go ErrorLines: ordered, adjacent-deduplicated, last twenty recognized errors. */
export function errorLines(log: string): string {
  const errors: string[] = [];
  for (const raw of unstamped(log).split("\n")) {
    let line = raw.trim();
    if (line.startsWith("##[error]")) line = line.slice(9).trim();
    else if (!/^(?:error|fatal)/i.test(line) && !line.startsWith("--- FAIL") && !line.startsWith("FAIL") && !/panic:|: error/i.test(line) && !line.includes("✗")) continue;
    if (line && errors.at(-1) !== line) errors.push(line);
  }
  return errors.slice(-20).join("\n");
}
/** Go LogAround selects up to 150 lines ending at the last marker, otherwise the tail. */
export function logPages(log: string): { pages: ContentPage[]; focus: number } {
  const lines = unstamped(log).replace(/\n+$/, "").split("\n");
  let end = lines.length;
  for (let index = lines.length - 1; index >= 0; index--) if (lines[index]!.trim().startsWith("##[error]")) { end = index + 1; break; }
  const first = Math.max(0, end - 150), pages: ContentPage[] = [];
  const append = (from: number, to: number) => {
    if (from === to) return;
    for (const page of contentPages(lines.slice(from, to).join("\n"), 150)) pages.push({ text: page.text.replace(/\n$/, ""), startLine: from + page.startLine });
  };
  append(0, first); append(first, end); const focus = Math.max(0, pages.length - 1); append(end, lines.length);
  return { pages, focus };
}
