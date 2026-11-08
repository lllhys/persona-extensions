import { decode, type Segment } from "./model.ts";
import { textsFor, type Texts } from "./i18n.ts";
import { scanTags, stripTags, attributes, nextClose } from "./html.ts";
const dropped = new Set(["svg", "script", "style"]), div = new Set(["div"]), aside = new Set(["aside"]), pre = new Set(["pre"]);
const breaks = new Set(["p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "tr", "td", "th", "details", "summary", "aside", "ul", "ol"]);
function prose(s: string): string {
  const parts: string[] = []; let from = 0;
  for (const tag of scanTags(s)) {
    parts.push(s.slice(from, tag.start));
    if (!tag.close && tag.name === "li") parts.push("\n• ");
    else if (!tag.close && tag.name === "blockquote") parts.push("\n> ");
    else if (tag.name === "br" || tag.close && breaks.has(tag.name) && tag.raw.toLowerCase() === `</${tag.name}>`) parts.push("\n");
    from = tag.end;
  }
  parts.push(s.slice(from));
  return decode(parts.join("")).split("\n").map(line => line.replace(/\s+/gu, " ").trim()).join("\n").replace(/\n{3,}/gu, "\n\n").trim();
}
function dropContent(s: string): string {
  const tags = scanTags(s), ends = nextClose(tags, dropped), divEnds = nextClose(tags, div), parts: string[] = []; let from = 0;
  for (let i = 0; i < tags.length; i++) {
    const tag = tags[i]; if (tag.close) continue;
    const end = dropped.has(tag.name) ? ends[i] : /^<div class="meta">$/iu.test(tag.raw) ? divEnds[i] : -1;
    if (end < 0) continue;
    parts.push(s.slice(from, tag.start)); from = tags[end].end; i = end;
  }
  parts.push(s.slice(from)); return parts.join("");
}
function previews(s: string, t: Texts): string {
  const tags = scanTags(s), ends = nextClose(tags, aside), parts: string[] = []; let from = 0;
  for (let i = 0; i < tags.length; i++) {
    const tag = tags[i]; if (tag.close || tag.name !== "aside" && tag.name !== "img") continue;
    const attrs = attributes(tag), cls = attrs.get("class"), doubleClass = cls?.quote === '"' ? cls.value : "";
    if (tag.name === "aside" && /\bonebox\b/iu.test(doubleClass) && ends[i] >= 0) {
      const src = attrs.get("data-onebox-src"), url = src?.quote === '"' ? src.value : "";
      parts.push(s.slice(from, tag.start), url ? `\n${t.link} ${url}\n` : `\n${t.preview}\n`);
      from = tags[ends[i]].end; i = ends[i];
    } else if (tag.name === "img") {
      parts.push(s.slice(from, tag.start), /\b(?:emoji|avatar)\b/iu.test(doubleClass) ? "" : t.image); from = tag.end;
    }
  }
  parts.push(s.slice(from)); return parts.join("");
}
/** Three bounded forward/indexed passes preserve the original cooked-text pipeline.
 * Each unmatched script/aside/pre open is constant work, never a suffix search. */
export function postText(cooked: string, t: Texts = textsFor()): Segment[] {
  const s = previews(dropContent(cooked), t), tags = scanTags(s), ends = nextClose(tags, pre), out: Segment[] = []; let from = 0;
  const add = (code: boolean, text: string) => { if (text.trim()) out.push({ code, text }); };
  for (let i = 0; i < tags.length; i++) {
    const tag = tags[i], end = ends[i];
    if (tag.close || tag.name !== "pre" || end < 0) continue;
    add(false, prose(s.slice(from, tag.start)));
    add(true, decode(stripTags(s.slice(tag.end, tags[end].start))).replace(/^\n+|\n+$/gu, ""));
    from = tags[end].end; i = end;
  }
  add(false, prose(s.slice(from))); return out;
}
export const plainText = (cooked: string, t?: Texts) => postText(cooked, t).map(s => s.text).join("\n");
