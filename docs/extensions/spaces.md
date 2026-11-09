# Spaces

[Documentation](../README.md) · [Source](../../extensions/spaces/)

Spaces keeps structured data whose shape you decide. A space declares its own types; a type declares its fields, its statuses and how it is drawn. Entities and the relations between them are stored in one place, and the page is generated from the definitions, so a type defined a moment ago, from the page or by the assistant in a conversation, has its tab, its view and its form straight away.

The extension starts empty. Nothing is preset: no spaces, no types, no example data. All data is local, in the extension's data directory as `spaces_state.json`; the extension makes no network requests and needs no secrets.

## The ontology

| Concept | Fields |
| --- | --- |
| `Space` | `id`, `name`, `icon` (a view NavIcon, default `folder`), `description`, `types` |
| `TypeDef` | `key` (snake_case), `label`, `view`, `fields`, `statuses` (optional, in order) |
| `FieldDef` | `key` (snake_case), `label`, `kind`, `options` (enum only), `required` |
| `Entity` | `id`, `space`, `type`, `title`, `status`, `values` (field key → value), `created`, `updated` |
| `Relation` | `from`, `type` (a name such as 属于 or 依赖), `to`: a directed edge between two entities of one space |

Types are scoped to their space: the same key can mean different things in two spaces, and relations never cross spaces. Limits: 50 spaces, 8 types a space, 24 fields a type, 8 statuses, 10,000 entities and 20,000 relations.

### Values

A value is held to its field's kind, never read by its look:

| Kind | Accepts | Stored as |
| --- | --- | --- |
| `text` | a string (at most 2,000 characters) | the string: `"007"` stays `"007"` |
| `number` | a number, or a string that parses as one | a number |
| `boolean` | `true` / `false`, or those words as strings | a boolean |
| `date` | `YYYY-MM-DD` | the string |
| `enum` | one of the field's options | the string |
| `url` | an `http` or `https` URL with a host | the string |

An unknown field is refused with the list of the type's fields; a missing required field, or a status the type does not have, is refused. A new entity starts at the type's first status. On a change, values merge into what the entity holds, and `null` or an empty string removes one. When given, `relations` are all of an entity's outgoing edges; leaving them out keeps them. Deleting an entity deletes its edges, and deleting a space deletes everything in it.

### Changing a type

A type's definition is replaced as a whole. Its entities must fit the new one: values of a field left out go with the field, the remaining values are read again by their (possibly new) kind (`"12"` in a text field becomes `12` in a number field), entities take the first status when a type gains statuses and lose theirs when it has none. A change that any entity does not fit (a removed option or status still in use, a new required field, a text that is not a number) is refused as a whole, with the entity named.

## The page

| Contribution | Behavior |
| --- | --- |
| `ui` `spaces` (`nav_page`) | The space's home, entity pages and the forms below. |
| `ui` `spaces-card` (`overview_card`) | Up to four spaces with their number of types and entities. |
| `skills` `spaces` | Tells the assistant to read the schema before writing, and to agree a structure with the user before defining one. |
| `mcp_servers` `spaces` | `spaces mcp` on the assistant's computer. Only the three read tools are in `allow`; the write tools follow the conversation's permission policy. |

The filter switches spaces (and offers ＋ 新建空间). A space's types are its tabs, each with its number of entities, and a last ＋ tab defines a new one. The search looks in the titles and text values of the current type. A type is drawn by its view:

| View | Drawn as | Summary |
| --- | --- | --- |
| `list` | Rows: the title, up to three values, the status | The count per status, when there are two to six |
| `board` | A section per status, in order | none: the sections count |
| `progress` | A row and a progress bar per entity: the first number field over the second (100 when there is one) | The average progress and how many are complete |
| `table` | A table of the title, the status and the first fields; a search turns it into rows that open | As `list` |
| `feed` | Rows newest first, with the first text value and when it was added | As `list` |

Each page shows the 100 most recent entities of a type; the search finds the rest. Empty states say what to do next: with no space, create one or describe to the assistant what to manage; with no type or no entities, the same for a type or an entity.

An entity's page shows its values (short ones as facts in the header, links and longer text below), its outgoing and incoming relations, each opening the other entity, a button that moves it to the next status, edit and delete. 管理关系 lists the outgoing relations, removed by a tap, and adds one to another entity of the space. The entity form is generated from the fields: text, number, a toggle for a boolean, a choice for an enum and for the status.

空间设置 edits the space's name, icon and description, lists its types and deletes the space. A type's form takes its label, view, statuses (separated by commas) and fields written as `key:名称:kind`, separated by `;` or new lines, with `:选项|选项` after an enum and a trailing `!` for a required field:

```text
author:作者:text!; pages:页数:number; shelf:书架:enum:客厅|书房; started:开始:date
```

The page is drawn for apps of UI version 4 and later (tabs, header).

## The MCP server

`spaces mcp` speaks MCP over stdio (JSON-RPC 2.0, one message a line: `initialize`, `ping`, `tools/list`, `tools/call`). `spaces --help` names `mcp`, which the manifest's `requires` checks. Set `PERSONA_SPACES_BIN` to the program's path when `spaces` is not on `PATH` or in `~/bin`.

| Tool | Access | What it does |
| --- | --- | --- |
| `space_list` | read, allowed | The spaces, each with its types (key, label, view, number of entities) and number of relations. |
| `space_schema_get` | read, allowed | A space with its full type definitions, and the field kinds and views there are. |
| `space_query` | read, allowed | Entities of a space by `type`, `id`, `query` (title and text values), `status` and `where` (exact field values, with `type`), at most `limit` (50, up to 200), most recently changed first; each with `out` and `in` edges naming the other end. |
| `space_upsert` | write, asks | Creates a space (optionally with a lowercase `id`) or changes its name, icon or description. |
| `space_schema_upsert` | write, asks | Defines a type or replaces its definition, under the rules above. |
| `space_entity_upsert` | write, asks | Creates an entity (no `id`: `type` and `title`) or changes one, with `values` and `relations`. |
| `space_entity_delete` | write, asks | Deletes an entity and its edges. |

`space_id` can be left out when there is exactly one space. Errors are returned as tool errors in words the assistant can act on.

## Storage

The extension and `spaces mcp` share `spaces_state.json`. Every read loads the file again; every change takes an exclusive lock on `.spaces.lock` in the same directory, loads, applies the change and writes a temporary file that is renamed over the state, so neither process overwrites the other's change. A change that fails saves nothing. An unreadable file is reported and left as it is, never replaced.

The extension checks the file every two seconds and, when another process changed it, asks the apps to draw its pages again, so what the assistant writes appears on the page without a refresh.

`spaces mcp` finds its data in `--data`, else `PERSONA_SPACES_DATA`, else the data directory the hub's extension last wrote to `<user config dir>/persona-spaces/data-dir` on the same computer (best effort: a sandboxed extension may not be allowed to write it), else `<user config dir>/persona-spaces/data`.

## Tests

```sh
go -C extensions/spaces test -race ./...
```

The tests cover the protocol conformance check and the manifest, every field kind's validation, relations and deletion, type changes and the entities they must fit, the store shared by two processes, every page and view checked against the view schema, the forms and their actions, and all seven MCP tools over stdio together with the page that reads what they wrote.
