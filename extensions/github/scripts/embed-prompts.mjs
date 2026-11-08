import { readFile, writeFile } from "node:fs/promises";
const root = new URL("../", import.meta.url);
const names = ["github_search", "github_read", "github_diff", "github_checks", "github_comment", "github_review"];
const tools = [];
for (const name of names) {
  const text = await readFile(new URL(`internal/github/prompts/${name}.md`, root), "utf8");
  const schema = /```json\s*([\s\S]*?)```/.exec(text);
  if (!schema) throw new Error(`missing original schema for ${name}`);
  tools.push({ name, description: text.slice(0, schema.index).trim(), input_schema: JSON.parse(schema[1]) });
}
await writeFile(new URL("src/tools.json", root), JSON.stringify(tools, null, 2) + "\n");
console.log("PASS: six original prompt descriptions and schemas embedded");
