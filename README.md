# expense-tracker

![GitHub repo size](https://img.shields.io/github/repo-size/keivanipchihagh/expense-tracker)
![GitHub License](https://img.shields.io/github/license/keivanipchihagh/expense-tracker)

A self-hosted expense log you can talk to. Tell an AI agent "12.50 for lunch at Rewe" and it's recorded; browse, filter and analyse it in a small web UI. Accounts for several people, one Go binary, SQLite, Docker.

## Features

- **MCP server** for Claude, ChatGPT, Gemini and other agents: log, correct, look up and delete expenses
- **Accounts**: sign up with email and password; each person sees only their own expenses, and can export all their data or delete their account
- **Web UI**: list and filter expenses, quick add, your own categories, CSV/JSON export, light and dark theme
- **Type, say or snap**: a floating bar on the expense list takes "12.50 lunch at Rewe yesterday" typed or spoken, or a receipt photo, and fills in only what it's sure of for you to finish
- **Receipt scanning**: snap or upload a receipt and a vision model on [OpenRouter](https://openrouter.ai) fills in the expense for you to check, and the photo is kept with it (set `OPENROUTER_API_KEY`; the default model is free)
- **Analysis**: totals against the previous period, category breakdown, spending over time, top merchants
- **Built-in OAuth 2.1** for agents, and a REST API
- A change log of every edit

## Run

```bash
cp .env.template .env
docker compose -f deploy/docker-compose.yaml --env-file .env up -d --build
```

Open http://localhost:8000 and create an account. Once everyone who needs one has signed up, set `SIGNUP_ENABLED=false`.

> Stop the container before opening or copying `data/expense.db` from the host; SQLite in WAL mode can corrupt otherwise.

## Connect an agent

Add the MCP server and sign in when the browser opens. With Claude Code:

```bash
claude mcp add --transport http expense-tracker http://localhost:8000/mcp
```

Tools: `create_expense`, `list_expenses`, `get_expense`, `update_expense`, `delete_expense`.

Agents that connect from the internet (claude.ai, ChatGPT, Gemini) need `PUBLIC_URL` set to a public HTTPS address, e.g. a Cloudflare Tunnel.

## API

`/api/v1/expenses` supports create, list (filter by `date_from`, `date_to`, `category`, `merchant`), get, update (`PATCH`, merge patch) and delete; `GET /api/v1/expenses/{id}/receipt` returns a kept receipt photo. `/api/v1/me` reads, updates (`PATCH`) and deletes (`DELETE`, with your password) your account; `GET /api/v1/me/export` returns all your data and `PUT /api/v1/me/password` changes the password. `/api/v1/categories` lists, adds (`POST`) and removes (`DELETE /api/v1/categories/{name}`, only when no expense uses it) your categories. `POST /api/v1/users` signs up. Everything except sign-up needs an OAuth bearer token; agents get one automatically, and [MCP Inspector](https://github.com/modelcontextprotocol/inspector) can get one by hand.

```bash
curl -X POST localhost:8000/api/v1/expenses -H "Authorization: Bearer $TOKEN" \
  -d '{"amount":"12.50","occurred_at":"2026-11-07T13:20:00+01:00","category":"food","merchant":"Rewe"}'
```

## Development

Requires Go 1.27+.

```bash
make lint    # go fix, go vet, gofmt
make test    # tests with the race detector
make mocks   # regenerate mocks after changing a repository interface
go run .
```

Configuration is read from the environment; see [`.env.template`](.env.template).

## License

[MIT](LICENSE)
