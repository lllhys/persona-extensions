# Spaces

[Documentation](../README.md) · [Source](../../extensions/spaces/)

Spaces manages any kind of thing with **types**, **properties** and **relations**. You define the structure: a type has its own properties, an optional lifecycle, a card that summarises each record, and views. The navigation page, the overview card, boards, progress bars, rollups and the assistant's tools are all generated from that structure, so changing the structure changes every screen that uses it. Four templates give a starting point: project and goals, reading and knowledge, inventory, and a blank space.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `spaces`, placement `nav_page`, icon `list` | The "空间" entry on the main navigation: the welcome page, space overviews, type tabs, record pages and the schema editor. |
| `ui` | `spaces-card`, placement `overview_card`, opens `spaces` | An overview card with the totals across spaces and the records in progress. |
| `skills` | `spaces`, directory `skills/spaces` | The assistant's skill: read the structure first, offer a template, agree on types before defining them, then use the tools. |
| `mcp_servers` | `spaces`, command `spaces`, arguments `mcp`, requires `mcp`, allow `schema` and `query` | The seven assistant tools. The two read-only tools run without asking; the others follow the conversation's permission mode. |

## Concepts

- **Space**: a container with a name, an optional icon and a short description. Types, records and relations stay inside one space. Up to 50 spaces.
- **Type**: a kind of record, such as 目标 (goal) or 任务 (task). A space has at most 7 types, so the tabs fit one row. A type has up to 24 properties, an optional lifecycle, a card and up to 5 views.
- **Record**: one entity of a type, with a title, a status when its type has a lifecycle, property values, and creation and update times. A space holds up to 5 000 records.
- **Property**: a named field of a type. Its key is fixed when it is created; its name is only a label and can change at any time. The kind of a property cannot change after creation.

Keys are lowercase letters, digits and underscores, at most 32 characters, and unique in their type. Titles are at most 200 characters and text values at most 2 000.

## Property kinds

| Kind | Name in the UI | Input | Stores | Options | Shown as |
| --- | --- | --- | --- | --- | --- |
| `text` | 文本 | single line | text | — | the text itself |
| `number` | 数字 | number | a finite number | an optional unit, such as 页 or 小时 | the number and its unit |
| `boolean` | 是否 | switch | yes or no | — | 是 or 否 |
| `date` | 日期 | `YYYY-MM-DD` | a calendar day | — | the day |
| `enum` | 选项 | choice | one of the listed options | 1 to 50 distinct labels | the label |
| `url` | 链接 | `https://` address | an absolute http or https address | — | the host and path |
| `relation` | 关联 | pick a record | references to records of one target type | the target type, whether it takes many, and the name shown on the target's backlinks | the target titles |
| `rollup` | 汇总 | computed, never typed | nothing; computed from related records | the source (own relation or a backlink), the aggregate and, for some aggregates, a numeric property | the computed number, with its unit or as a percentage |

A value is either correct for its kind or absent; an empty value is always allowed. A rollup is never stored and cannot be typed in.

## Status flow

A type with a lifecycle defines an ordered list of stages (2 to 8). Each stage has a phase: `todo`, `doing`, `done` or `dropped`.

- The **main line** is the stages that are not dropped, in order. Their phases never go backwards, so a record moves from `todo` to `doing` to `done`.
- One step at a time: a record on the main line can move to the stage before or after it, or to any dropped stage. A record in a dropped stage can return to any main-line stage. Nothing else is allowed; a jump is refused with a sentence that lists the allowed targets.
- A new record starts at the first stage, unless it is created in another stage: recording something that is already true is not a transition.
- The phase drives the rest: a completion percentage counts the records in phase `done` among those with a lifecycle that are not dropped, and boards, tones and the overview use the phase as well.

A type without a lifecycle has no status.

## Relations, backlinks and provenance

A relation is the only kind of link. A record stores the ids of the records it points to, in the relation's property. The reverse direction is not stored: every record shows the records that point to it as **backlinks**, labelled with the relation's backlink name (for example, a task shows on its goal under "任务").

- A relation targets one type of the same space, and may point to the same type (a task blocked by another task). A record cannot point to itself.
- Deleting a record removes its id from every relation that holds it.
- **Provenance** is a built-in relation: a record created by derive points to its source ("源自"), and the source lists it as "衍生". Deleting the source leaves that link in place but unresolved; ids are never reused, so the link can never point at another record.

## Built-in fields

Every type has these fields without declaring them, so filters, sorts, groups, cards and tables treat them like other properties: `title`, `status`, `phase`, `type`, `origin`, `created` and `updated`. Property keys cannot take these names.

## Rollups

A rollup reads related records and computes a value. It has two forms:

- **Own relation**: the records that this record points to through one of its relations (for example, the notes a topic cites).
- **Backlinks**: the records of another type whose relation points to this record (for example, the tasks of a goal).

| Aggregate | Result |
| --- | --- |
| `count` | the number of records |
| `sum`, `avg`, `min`, `max` | over a numeric property of those records; unset values are skipped; undefined when nothing is set |
| `percent_done` | the share of records in phase `done` among those with a lifecycle that are not dropped; undefined when there are none |

A rollup cannot read another rollup. Queries use the same aggregates over any set of records.

## Derive

Derive creates a record of any type in the same space from an existing one. The title is taken from the source unless given. Every property with a value that both types declare is copied when the new type accepts the value: a number 12 becomes the text "12", and an enum is copied when its option exists. Anything that does not fit is left unset. The new record starts at the first stage of its type and records its source as provenance. The source is not changed.

The answer to the assistant lists the properties that were carried over.

## Views

Each type has up to 5 views. The first is the default. A view is a kind plus a lens: conditions, up to 3 sort keys, a group field and up to 4 aggregates. Every view kind works on any type; a view without a suitable field falls back to a sensible default.

| Kind | Shows |
| --- | --- |
| `list` | rows, grouped when the view has a group field |
| `board` | one column per stage (or per enum option, or a single column when neither exists) |
| `table` | a table of up to 8 columns and 50 rows a page, with the aggregates as a caption; a row index below it opens the records |
| `progress` | each record with its progress bar, from its card's progress or from its position in the lifecycle |
| `feed` | records newest first, one section per day in the viewer's time zone |

Above every view are the totals: the aggregates of the lens, or the count and the per-phase counts.

Every record appears as the same row everywhere: a title, a subtitle (the card's lead text or its pills), a status badge toned by phase, and the type's icon where types are mixed.

## Templates

Templates create a space with its types and, optionally, example records. They are built only from the operations the structure allows, so a template cannot hold anything the rules would refuse.

| Key | Name | Contents |
| --- | --- | --- |
| `project` | 项目与目标 | ideas, goals, tasks and notes, with derive, backlink rollups and five view kinds. |
| `reading` | 阅读与知识 | books, notes, clippings and topics, with rollups in both directions. |
| `inventory` | 清单与物品 | places, items, wishes and kits, with a wish that becomes an item by derive. |
| `blank` | 空白空间 | no types; start with 新建类型. |

The example records are invented and use only `example.com` addresses. Dates in examples are relative to the day they are created.

## Assistant tools

The assistant reaches the spaces through the `spaces` MCP server. The tools are listed here as the assistant sees them.

| Tool | Access | What it does |
| --- | --- | --- |
| `schema` | read, runs without asking | Lists spaces, templates and the vocabulary; or, for one space, its types with a JSON Schema of the values each type accepts. |
| `query` | read, runs without asking | Records of a type, or across types with built-in fields only; filters, sorts, groups, aggregates and paging. A record id reads that one record. |
| `space_write` | write | Creates a space, optionally from a template and with its examples, or changes a space's name, icon or description. |
| `type_write` | write | Adds a type or replaces it as a whole. A replacement migrates existing records and is refused as a whole when one does not fit. |
| `entity_write` | write | Creates a record or changes it. Properties are merged; `null` clears one; the status moves one step at a time. |
| `entity_derive` | write | Derives a record of another type. The source is unchanged. |
| `delete` | write, destructive | Deletes a record, a type with its records, or a space with all its content. It cannot be undone. |

The assistant's skill asks it to read `schema` first, to offer a template for a new space, and to agree on the types with the user before it defines them.

## Data and location

Records are kept in one file, `spaces.json`, in the extension's data directory, with a lock file beside it. Every change is written to a temporary file and then renamed over the old one, so a reader sees either the old or the new contents. A file that cannot be read, has another format or breaks the rules is refused and is never overwritten; its path is shown in the error.

Storage is kept behind a narrow interface, so the same structure can later be provided by the hub.

The program finds the data directory in this order:

1. `--data` on the command line of `spaces mcp`;
2. the `PERSONA_SPACES_DATA` environment variable;
3. the directory the running extension recorded in `persona-spaces/data-dir` under the user's configuration directory, when that directory exists;
4. `persona-spaces/data` under the user's configuration directory.

The running extension records its directory when it starts, so `spaces mcp` started by the assistant reads the same file.

### Making the assistant's `spaces` command findable

The extension package ships `bin/spaces-<platform>` inside the extension directory. Persona starts the assistant's MCP server with the command `spaces`, looked up on `PATH` and then in `~/bin`, and Persona adds nothing to its environment. Either:

- link the binary for your computer into `~/bin` under the name `spaces`, or into a directory on `PATH`; or
- set `PERSONA_SPACES_BIN` to the full path of the binary in the environment Persona runs in. Set it to `off` to leave the server out on that computer.

Without the binary the rest of the extension still works; only the assistant's tools are missing.

## Limits

| Item | Limit |
| --- | --- |
| Spaces | 50 |
| Types per space | 7 |
| Properties per type | 24 |
| Stages per lifecycle | 8 |
| Options per enum | 50 |
| Views per type | 5 |
| Records per space | 5 000 |
| Targets per relation value | 100 |
| Title / text | 200 / 2 000 characters |

## Development

```sh
go -C extensions/spaces test -race -count=1 ./...
scripts/package.sh spaces
```
