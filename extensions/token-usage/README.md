# Token usage

A navigation page and an overview card for how many tokens each of your computers runs, kept for 400 days — longer than the hub, which holds about a week.

The page has three tabs:

- **总览** — today's, 7-day and 30-day totals, each against the period before it; a daily bar chart for 7, 30 or 90 days; the top models, machines and launchers, with the rest folded into 其他.
- **明细** — the same ranges filtered by machine, model or launcher, one row per day, with a CSV export.
- **预算** — a daily or weekly limit, for one model or for everything, with the share used so far.

The overview card shows today's total against the 7-day daily average and the model that used the most today.

Numbers are written in K and M. The page redraws itself once a minute and is readable at a phone's width.

## Where the numbers come from

Only the hub's existing endpoints, nothing added for this extension:

- `GET /api/v1/machines` for the computers, and `GET /api/v1/machines/{id}/usage?range=1h|24h|7d` for each one's usage per minute, model and launcher (the `machines.read` scope).
- `usage.gateway_accounts` for the account-pool gateways' quota, shown on the overview when the hub provides it and hidden when it does not (the `usage_gateways` permission).

On startup the program pulls the last 7 days once, then the last 24 hours every 10 minutes. A bucket is one minute of one model and launcher on one machine and carries that minute's whole total, so pulling it again replaces it rather than counting it twice.

## What is stored

One JSON file in the extension's data directory, guarded by an exclusive `flock`, summed per day, machine, model and launcher and kept for 400 days. The budgets live in a second file in the same directory. Both are refused rather than overwritten when they are not valid JSON of the expected format.

## Budgets and alerts

A budget alerts through `POST /api/v1/notify` (the `notify` scope) at 80% and at 100%, each once per day. A model whose current hour is more than five times its average for that same hour over the past seven days alerts once that day too. Alerts already sent are remembered, so the same one is not sent again the same day.

## The assistant

`token-usage mcp` serves five tools on stdio: `usage_summary`, `usage_top` and `budget_status` are read-only and run without asking; `budget_set` and `budget_delete` change a budget. The skill `skills/token-usage` tells the assistant when to use them. The server needs the `token-usage` binary on `PATH` or in `~/bin`, or `PERSONA_TOKEN_USAGE_BIN` pointing at it.
