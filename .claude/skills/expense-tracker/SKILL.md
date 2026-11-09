---
name: expense-tracker
description: Log, correct, look up and delete the user's expenses through the expense-tracker MCP server, and set up or troubleshoot that server's connection. Use when the user mentions spending money, a purchase, a bill, a receipt, "how much did I spend", or asks to fix or remove an expense.
---

# expense-tracker

## Connect

The server exposes MCP at `<PUBLIC_URL>/mcp` (local default `http://localhost:8000/mcp`). Auth is OAuth; there are no static API keys.

```bash
claude mcp add --transport http expense-tracker http://localhost:8000/mcp   # add once
claude mcp list                                                           # check status
claude mcp remove expense-tracker                                         # remove
```

- On first use the client opens a browser; the user signs in with the server's `AUTH_PASSWORD`. Never ask the user to paste the password or a token into the chat.
- `401` or "needs authentication": run `/mcp` in Claude Code and re-authenticate the server.
- Connection refused: the container is down. Start it from the repo root with `docker compose -f deploy/docker-compose.yaml --env-file .env up -d`.
- Remote clients (claude.ai, ChatGPT, Gemini) need `PUBLIC_URL` to be a public HTTPS URL.

## Tools

| Tool | Use |
|---|---|
| `create_expense` | Record an expense |
| `list_expenses` | `date_from` / `date_to` (YYYY-MM-DD, local, inclusive), `category`, `merchant` (part of the name); all optional |
| `get_expense` | One expense by `id` |
| `update_expense` | `id` + only the fields that change; `null` clears `merchant` or `note` |
| `delete_expense` | Permanent; confirm with the user unless they clearly asked |

## Filling in an expense

- `amount`: string, positive, at most 2 decimals, e.g. `"12.50"`. Ask if missing; never guess.
- There is no currency field. Amounts are plain numbers, and the user picks one display currency in the web UI. If they mention a different currency than usual, ask before logging.
- `occurred_at`: RFC 3339 **with the user's UTC offset**, e.g. `2026-11-07T13:20:00+01:00`. "Now" or no time given means the current local time. "Yesterday at lunch" means yesterday around 12:30. Never send a time without an offset.
- `category`: one of `food` (eating out), `groceries`, `transport`, `housing`, `utilities`, `health`, `entertainment`, `shopping`, `travel`, `subscriptions`, `education`, `gifts`, `other`.
- `merchant`: the store or vendor if named, e.g. "Rewe".
- `note`: only extra words the user actually said ("split with Alex"). Don't summarise, don't invent.

## Common tasks

- **Log**: one `create_expense` per expense. Confirm briefly afterwards, e.g. "Logged 12.50 groceries at Rewe today".
- **Fix**: find the expense with `list_expenses` for its date, then `update_expense` with only the changed fields. If several match, ask which one.
- **Delete**: same lookup, confirm, then `delete_expense`.
- **How much did I spend**: `list_expenses` for the range, then sum `amount` yourself with exact decimal arithmetic. Filter by `category` or `merchant` when the question names one. Group by `category` when asked for a breakdown.

## Errors

A failed tool call returns `isError` with readable text. For `validation failed`, every invalid field is listed: fix all of them and retry once. If it still fails, tell the user what's wrong. `expense not found` means the id is stale, so list again.
