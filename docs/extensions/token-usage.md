# Token usage

[Documentation](../README.md)

`token-usage` summarizes how many tokens each computer runs and keeps 400 days of it. The hub keeps about a week; the extension pulls that and stores the rest itself.

The navigation page has three tabs. 总览 shows today's, the last 7 days' and the last 30 days' totals, each against the period before, a daily chart, and the top models, machines and launchers. 明细 is the same data filterable by machine, model and launcher, with a CSV export. 预算 sets a daily or weekly limit for one model or for all of them. The overview card shows today's total against the 7-day daily average and the busiest model.

## Data

The extension reads only endpoints the hub already has: `GET /api/v1/machines` and `GET /api/v1/machines/{id}/usage` (`machines.read`), and, when the hub offers it, the `usage.gateway_accounts` extension method (`usage_gateways`) for the gateway account quotas. It asks for nothing else and adds nothing to the hub.

A background loop starts with the extension, pulls the last 7 days once and the last 24 hours every 10 minutes after that. Each bucket is one minute of one model and launcher on one machine and replaces the stored copy of that minute, so a pull never double-counts.

## Storage

The history is one JSON file in the extension's data directory, locked with `flock` while it is written and replaced atomically. It is summed per day, machine, model and launcher and pruned to 400 days. The budgets and the record of which alerts were already sent are a second JSON file. A file that is not valid for its format is left untouched.

## Budgets and alerts

Crossing 80% and 100% of a budget each sends one notification through `POST /api/v1/notify` (`notify`), and neither is sent again the same day. A model whose current hour exceeds five times its average for that hour over the previous seven days sends one notification that day as well.

## Assistant

The `token-usage mcp` command speaks the Model Context Protocol on stdio. `usage_summary(range, group_by)`, `usage_top(range, dim, n)` and `budget_status()` only read; `budget_set` and `budget_delete` change a budget. The read-only three are in the manifest's `allow` list, so they run without asking. The bundled skill explains to the assistant when each tool applies. The command is found on `PATH` or in `~/bin`, unless `PERSONA_TOKEN_USAGE_BIN` names it.
