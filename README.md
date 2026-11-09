# expense-tracker

![GitHub repo size](https://img.shields.io/github/repo-size/keivanipchihagh/expense-tracker)
![GitHub License](https://img.shields.io/github/license/keivanipchihagh/expense-tracker)

A self-hosted expense log you can talk to. Tell an AI agent "12.50 for lunch at Rewe" and it's recorded; browse, filter and analyse it in a small web UI. Single user, one Go binary, SQLite, Docker.

## Features

- **MCP server** for Claude, ChatGPT, Gemini and other agents: log, correct, look up and delete expenses
- **Web UI**: list and filter expenses, quick add, CSV/JSON export, light and dark theme
- **Analysis**: totals against the previous period, category breakdown, spending over time, top merchants
- **Built-in OAuth 2.1** for agents, and a REST API
- A change log of every edit

## Run

```bash
cp .env.template .env   # set AUTH_PASSWORD (openssl rand -base64 24)
docker compose -f deploy/docker-compose.yaml --env-file .env up -d --build
```

Open http://localhost:8000 and sign in with `AUTH_PASSWORD`.

> Stop the container before opening or copying `data/expense.db` from the host; SQLite in WAL mode can corrupt otherwise.

## Connect an agent

Add the MCP server and sign in when the browser opens. With Claude Code:

```bash
claude mcp add --transport http expense-tracker http://localhost:8000/mcp
```

Tools: `create_expense`, `list_expenses`, `get_expense`, `update_expense`, `delete_expense`.

Agents that connect from the internet (claude.ai, ChatGPT, Gemini) need `PUBLIC_URL` set to a public HTTPS address, e.g. a Cloudflare Tunnel.

## API

`/api/v1/expenses` supports create, list (filter by `date_from`, `date_to`, `category`, `merchant`), get, update (`PATCH`, merge patch) and delete. It needs an OAuth bearer token; agents get one automatically, and [MCP Inspector](https://github.com/modelcontextprotocol/inspector) can get one by hand.

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
AUTH_PASSWORD=... go run .
```

Configuration is read from the environment; see [`.env.template`](.env.template).

## License

[MIT](LICENSE)
