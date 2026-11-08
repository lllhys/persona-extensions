import { SaxesParser } from "saxes";
import { clip, decode, oneLine, topicID, topicURL, decimalInt64, type HotTopic, type Integer } from "./model.ts";
import { stripTags } from "./html.ts";
import { feedTime } from "./time.ts";
// strconv.Atoi on a 64-bit host returns the saturated boundary on range error.
const count = (token = "0"): Integer => {
  let value: bigint; try { value = decimalInt64(token); } catch { value = 9223372036854775807n; }
  return Number.isSafeInteger(Number(value)) ? Number(value) : value.toString();
};
/** Disjoint digit runs plus sticky suffix checks avoid a regex retry at every digit. */
function countsIn(s: string): { index: number; posts: string; people: string } | undefined {
  const postsSuffix = /\s*(?:个帖子|posts?)\s*-\s*/iyu, peopleSuffix = /\s*(?:位参与者|participants?)/iyu;
  const digit = (i: number) => s.charCodeAt(i) >= 48 && s.charCodeAt(i) <= 57;
  for (let i = 0; i < s.length; ) {
    if (!digit(i)) { i++; continue; }
    const start = i; while (i < s.length && digit(i)) i++;
    const firstEnd = i; postsSuffix.lastIndex = i;
    const suffix = postsSuffix.exec(s); if (!suffix) continue;
    const peopleStart = postsSuffix.lastIndex; let peopleEnd = peopleStart;
    while (peopleEnd < s.length && digit(peopleEnd)) peopleEnd++;
    if (peopleEnd === peopleStart) continue;
    peopleSuffix.lastIndex = peopleEnd;
    if (peopleSuffix.exec(s)) return { index: start, posts: s.slice(start, firstEnd), people: s.slice(peopleStart, peopleEnd) };
  }
  return undefined;
}
/** RSS is required: the site's JSON listings challenge server connections. XML entities and CDATA remain distinct. */
export function parseFeed(raw: string): HotTopic[] {
  const parser = new SaxesParser({ xmlns: false });
  const stack: string[] = []; const out: HotTopic[] = [];
  let item: Record<string, string> | undefined, root = "", channel = false;
  parser.on("doctype", () => { throw new Error("RSS document types are not supported"); });
  parser.on("opentag", tag => {
    const name = tag.name.split(":").at(-1)!;
    if (!stack.length) root = name;
    stack.push(name);
    if (stack.length > 64) throw new Error("RSS nesting limit");
    if (stack.join("/") === "rss/channel") channel = true;
    if (stack.join("/") === "rss/channel/item") item = {};
  });
  const text = (value: string) => {
    if (item && stack.length === 4) { const field = stack[3]; item[field] = (item[field] ?? "") + value; }
  };
  parser.on("text", text); parser.on("cdata", text);
  parser.on("closetag", () => {
    if (stack.join("/") === "rss/channel/item" && item) {
      let id: string;
      try { id = topicID(item.link); } catch { stack.pop(); item = undefined; return; }
      let desc = oneLine(decode(stripTags(item.description ?? "", " ")));
      const counts = countsIn(desc);
      if (counts) desc = desc.slice(0, counts.index);
      desc = desc.trim().replace(/(?:阅读完整话题|Read full topic)$/u, "").trim();
      out.push({ id, url: topicURL(id), title: oneLine(decode(item.title ?? "")), author: (item.creator ?? "").trim(), category: decode(item.category ?? "").trim(), excerpt: clip(desc, 200), posts: count(counts?.posts), people: count(counts?.people), at: feedTime(item.pubDate ?? ""), pinned: (item.topicPinned ?? "").trim().toLowerCase() === "yes" });
      item = undefined;
    }
    stack.pop();
  });
  parser.write(raw).close();
  if (root !== "rss" || !channel) throw new Error("not an RSS channel");
  // Stable keys also require deduplication if an upstream feed repeats a topic.
  const seen = new Set<string>();
  return out.filter(t => { if (seen.has(t.id)) return false; seen.add(t.id); return true; });
}
