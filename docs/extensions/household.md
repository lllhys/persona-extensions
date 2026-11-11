# Household

The household extension (`household`, 家庭管家) keeps a home's stock, shopping list, chores and meal plan, and tells you when something runs low, expires or falls due. It is built on the ontology kernel of [spaces](spaces.md): every record is a kernel entity in one space with six types, and the household rules are a thin layer above it.

| Extension point | What it adds |
| --- | --- |
| `contributes.ui` `nav_page` `household` | The 家庭管家 page: 总览, 库存, 采购, 家务 and 餐食 tabs, with a badge counting what needs attention |
| `contributes.ui` `overview_card` `household-card` | A card on the overview with the counts and the most urgent entries |
| `contributes.mcp_servers` `household` | Nine tools for the assistant (`household mcp`) |
| `contributes.skills` `household` | Tells the assistant when and how to use the tools |
| `permissions.hub` `notify` | The daily reminder as a push notification |
| `permissions.hub` `todos.write` | Reminders as to-dos (optional, chosen in the settings) |

## What it keeps

One space, `household`, with these types. Names, stages and options are stored values and stay in Chinese.

| Type | Key | Stages | Properties |
| --- | --- | --- | --- |
| 位置 | `place` | none | `note` text; `items` rollup: count of 物品 stored here |
| 物品 | `item` | none | `category` enum (食材, 调味, 日用, 清洁, 药品, 母婴, 其他); `place` relation to 位置 (backlink 存放物品); `qty` number; `unit` text; `min` number, the low-stock threshold; `expires` date; `daily` number, the average use per day; `counted` date, when `qty` was last true; `lead` number, days of warning; `refill` number, how much to buy; `note` text |
| 采购 | `buy` | 待买 (todo) → 已买 (done); 不买 (dropped) | `item` relation to 物品 (backlink 采购记录); `amount` number; `unit` text; `category` enum, as on 物品; `reason` enum (低库存, 已用完, 已过期, 用量预警, 手动); `note` text |
| 家务 | `chore` | none | `every` number, days between two rounds; `last` date; `next` date; `who` text; `place` relation to 位置 (backlink 家务); `minutes` number; `note` text |
| 菜谱 | `recipe` | none | `needs` relation to many 物品 (backlink 可做的菜); `minutes` number; `url` url; `note` text |
| 餐食 | `meal` | 计划 (todo) → 已做 (done); 取消 (dropped) | `date` date; `slot` enum (早餐, 午餐, 晚餐, 加餐); `recipe` relation to 菜谱 (backlink 排期); `note` text |

The purchase amount is called `amount`, not `qty`, on purpose: deriving a purchase from an item copies the properties both types declare (`unit`, `category`), and the stock quantity must not become the amount to buy.

## Rules

All rules are computed from the records when they are read; nothing derived is stored. "Today" is the date in the hub's time zone.

**Estimated quantity.** An item with `daily` set is assumed to be used up at that rate since `counted` (or, without it, since the record last changed): `est = max(0, qty − daily × days)`. Without `daily`, `est = qty`. An item without `qty` is not tracked by quantity and only its expiry date counts.

**Findings.** Each item gets at most one finding, the most severe that applies:

| Finding | When | Purchase reason |
| --- | --- | --- |
| `expired` 已过期 | `expires` is before today | 已过期 |
| `out` 已用完 | `qty` is set and `est` is 0 | 已用完 |
| `low` 低库存 | `min` is set and `est ≤ min` | 低库存 |
| `runout` 用量预警 | `daily > 0` and `est / daily ≤ lead` | 用量预警 |
| `expiring` 即将过期 | `expires` is within `lead` days from today | none: use it first |

`lead` defaults to 7 days for 母婴 items and 3 days for everything else. `runout` is what warns about baby supplies before they are gone: give a pack of diapers its count, the daily use and the day you counted, and it is flagged a week before the estimate reaches zero.

**Shopping list.** 一键补货 creates one 待买 purchase for every item with an `expired`, `out`, `low` or `runout` finding that has no 待买 purchase yet, in one transaction. Each is derived from its item (the kernel's derive: same title, `unit` and `category` copied, origin recorded; the item's note is not carried over) with `item` pointing back, `amount` = the item's `refill` (1 when unset) and the reason from the table. Running it again creates nothing new.

**Buying.** Marking a purchase 已买 through the page or the `household_purchase` tool stocks the item: `amount` is added to its estimated quantity and `counted` becomes today. When the item's `expires` date is already past, the old batch is taken as thrown away: the quantity becomes `amount` alone and the date is cleared (enter the new one when you put the batch away). An item without `qty` stays untracked; only a past date is cleared. 不买 only closes the purchase.

**Chores.** A chore is due when `next` is today or earlier. 完成 sets `last` to today and `next` to today + `every` (rounded up to whole days, at least one); a chore without `every` is a one-off and has its `next` cleared. A new chore without `next` is due today.

**Meals.** Suggestions rank recipes by the ingredients at hand. An ingredient counts as at hand when the item is not expired and is either untracked or has `est > 0`. Recipes whose ingredients are all at hand come first, then by how many of their ingredients are `expiring` (use those first), then by the share at hand. Each suggestion lists what is missing. Planning a meal creates a 计划 entry for a date and slot; marking it 已做 does not change the stock, because recipes do not record amounts. Adjust items with 用掉 on the stock tab or with the `household_use` tool.

## Reminders

The extension starts with the hub (`activation_events: ["on_startup"]`) and checks the rules every ten minutes and whenever the records change.

| Extension point | Used for |
| --- | --- |
| Badge on the navigation entry (`ui.badge`) | The number of findings that need a purchase plus the chores due; `danger` when something is expired or used up, otherwise `caution`; cleared at zero |
| `POST /api/v1/notify` (scope `notify`) | One notification per day, at or after the configured time, when anything needs attention: the title has the count, the body lists expired, used-up, low, soon-used-up and soon-expiring items and the chores due. It carries the collapse id `household-<date>` and opens the 家庭管家 page |
| `POST` / `PATCH /api/v1/todos` (scope `todos.write`) | When the settings choose to-dos: one to-do per item that needs a purchase and per chore due, with today as its due date. An item keeps its one to-do while its finding gets worse. When the item is stocked or the chore done, the to-do is marked done; so are the open ones when to-dos are switched off. At most 20 to-dos are created in one round |

Which to-dos and notifications were already sent is recorded in `remind.json` in the extension's data directory, so a restart repeats nothing. A hub that is unreachable or an assistant that is offline (`503`) is retried in the next round; one failing call does not hold back the others.

Settings (`configuration`):

| Key | Default | Meaning |
| --- | --- | --- |
| `remind` | `notify` | `notify`, `todos`, `both` or `off` |
| `remind_time` | `08:30` | The time of day (hub time zone) from which the daily reminder is sent |

## Assistant tools

`household mcp` serves these tools over stdio. The read-only ones are listed in the manifest's `allow`.

| Tool | Read-only | What it does |
| --- | --- | --- |
| `household_status` | yes | Findings, purchases still open, chores due, and the meals planned for today and tomorrow |
| `household_list` | yes | The records of one type (`place`, `item`, `buy`, `chore`, `recipe`, `meal`), optionally only those whose title contains a text; items come with their estimate and finding |
| `household_meal_suggest` | yes | Recipes ranked by the ingredients at hand, with what is missing and what to use first |
| `household_save` | no | Creates or changes one record of any type; the kernel checks the values against the type |
| `household_use` | no | Takes an amount off an item's estimated quantity and sets `counted` to today |
| `household_restock` | no | Creates the purchases described under "Shopping list" |
| `household_purchase` | no | Marks purchases 已买 (stocking the items) or 不买 |
| `household_chore_done` | no | Completes a chore and schedules the next round |
| `household_remove` | no | Deletes a record; relations pointing to it are removed |

Meals are planned with `household_save` (type `meal`, with `date`, `slot` and `recipe`).

## Data and location

Records are kept in the extension's own data directory, in `spaces.json`, the kernel's file format, written by the same store as spaces (lock file, temporary file and rename). `remind.json` sits beside it.

`household mcp`, started by the assistant, finds the directory in this order: `--data`; the `PERSONA_HOUSEHOLD_DATA` environment variable; the directory the running extension recorded in `persona-household/data-dir` under the user's configuration directory, when that directory exists; `persona-household/data` under the user's configuration directory.

The running extension also records the hub's time zone beside the records, so `household mcp` counts days the same way as the page; without that record it uses the zone of the machine.

The assistant's command is `household`, looked up on `PATH` and then in `~/bin`; `PERSONA_HOUSEHOLD_BIN` names the binary directly, and `off` leaves the server out. See "Making the assistant's `spaces` command findable" in [spaces.md](spaces.md): the steps are the same with the other name.

## Design notes

**An extension of its own, on the shared kernel.** A template pack inside spaces can describe the six types but not the rules: the kernel's derived properties are aggregates over relations and cannot express "estimated quantity at or below the threshold", decay by daily use, an idempotent restock, or a reminder that runs in the background. Those need code, and they do not belong in the general kernel. So household is a separate extension that uses the kernel as a library and adds a domain layer.

**How the kernel is shared.** Go does not allow `extensions/household` to import `extensions/spaces/internal/...`. The kernel and its file store therefore move, unchanged, into the shared module that the repository already has for code used by more than one extension:

- `extensions/spaces/internal/kernel` → `pluginkit/spaces/kernel`
- `extensions/spaces/internal/store` → `pluginkit/spaces/store`

Both extensions reach them through their existing kind of `replace` directive for `pluginkit`. The move changes import paths only; spaces keeps its behaviour, its tests and its version. The kernel stays free of dependencies (its purity test moves with it). The alternatives were rejected: a copy of the kernel in household would drift; a public facade inside the spaces module would make one extension a library of another and tie their releases together; a new top-level module would need a change to the layout rules for no gain.

**Its own data file, not the spaces file.** An extension installed on the hub may run in a sandbox that can write only its own data directory, so household cannot open the file of spaces. It keeps a kernel base of its own with the single space `household`. The format is the same, so a later hub-provided store (the kernel's `Storage` interface) or an import into spaces can bring the two together without converting anything. Until then the household records do not show up on the 空间 page.

**The schema belongs to the extension.** On every start, and before every change, the extension makes sure the space and its six types exist and match this document (missing types are created first without their relations, then every type that differs is written in full, the same two-pass order templates use). Reading never writes: on a new data directory the read-only tools answer with nothing. There is no tool that changes the types; a file edited by hand so that a type differs is brought back to this schema, and values of properties the schema does not have are dropped (a record that loses a value counts as changed then, so an item without `counted` restarts its estimate). The one exception: when a record holds a value this schema does not accept (an option added by hand, say), that type is left as it was found, so the extension keeps working.

### Packages

| Package | Role |
| --- | --- |
| `internal/model` | The schema (space id, type and property keys, stage and option names), `Ensure`, fictional sample records, and `Load`, which reads a space into typed records |
| `internal/rules` | Pure functions over the typed records: estimate, findings, restock suggestions, chores due, meal ideas, summary |
| `internal/ops` | The transactions: save, use, restock, purchase, chore done, remove, load samples. Each works on a `*kernel.Base` inside one `Update` |
| `internal/remind` | The hub client (`notify`, to-dos), the daily plan and its `remind.json` state |
| `internal/ui` | The page and the overview card |
| `internal/mcp` | The stdio MCP server and the nine tools |
| `internal/datadir` | Where the data directory is, and recording it for `household mcp` |

## Limits

- One household per hub.
- The kernel's limits apply: 5,000 records in the space, 24 properties per type.
- Quantities are estimates when `daily` is set; count again and save to correct them.
- Recipes list ingredients without amounts, so cooking a meal does not reduce stock.
- Changing a purchase's stage with `household_save` does not stock the item; use `household_purchase`.
- Reminders need the hub's `notify` or `todos.write` permission; without them only the badge and the page show what is due.

## Development

```sh
go -C pluginkit test -race ./spaces/...
go -C extensions/household test -race ./...
scripts/package.sh household
```

All sample records in the tests and behind 载入示例 are made up.
