# expense-tracker

Self-hosted expense log. Single user, SQLite, one Go binary, runs in Docker.

## Contents

- [Run](#run)
- [UI](#ui)
- [MCP](#mcp)
- [Authentication](#authentication)
- [Tables](#tables)
  - [Expense](#expense)
  - [Log](#log)
- [API](#api)
- [Development](#development)
- [License](#license)

## Run

```bash
cp .env.template .env   # set AUTH_PASSWORD (openssl rand -base64 24)
docker compose -f deploy/docker-compose.yaml --env-file .env up -d --build
```

The database is stored in `data/expense.db`. Stop the container before opening or copying it (see [Log](#log)).

## UI

Open `PUBLIC_URL` (by default http://localhost:8000) and sign in with `AUTH_PASSWORD`.

- List expenses for a date range, with presets for this month, last month, this year and all time
- Totals per currency, with a breakdown by category
- Add, edit and delete expenses; time and time zone default to your browser's
- **Apps** page: see which apps (Claude, ChatGPT, …) are signed in through OAuth and revoke their access

The UI is server-rendered (Go templates and [HTMX](https://htmx.org), bundled in the binary). Signing in sets an `HttpOnly`, `SameSite=Strict` session cookie valid for 30 days (named with the `__Host-` prefix when `PUBLIC_URL` is HTTPS). The cookie only opens the UI; the JSON API always needs a bearer token.

## MCP

An MCP server runs at `/mcp` (Streamable HTTP), so AI agents can log, correct and look up expenses for you. It uses the same validation and change log as the API.

| Tool | Does |
|---|---|
| `create_expense` | Record an expense |
| `list_expenses` | List expenses for a date range (`date_from`, `date_to`, both optional) |
| `get_expense` | Get one expense by id |
| `update_expense` | Change the fields sent; `null` clears `merchant` or `note` |
| `delete_expense` | Delete an expense permanently |

Clients log in with OAuth (see [Authentication](#authentication)): add the server and sign in with `AUTH_PASSWORD` when the browser opens. For example, with Claude Code:

```bash
claude mcp add --transport http expense-tracker http://localhost:8000/mcp
```

ChatGPT, claude.ai and Gemini connect from the internet, so they need `PUBLIC_URL` set to a public HTTPS address that reaches the server.

## Authentication

Every `/api/` endpoint and `/mcp` requires `Authorization: Bearer <access token>`. Tokens come from a built-in OAuth 2.1 server, the flow MCP clients such as Claude, ChatGPT and Gemini use:

1. The client calls the API, gets `401`, and finds the OAuth endpoints through `/.well-known/oauth-protected-resource`.
2. It registers itself (`POST /oauth/register`) and opens `/oauth/authorize` in your browser.
3. You sign in with `AUTH_PASSWORD`. The page shows which app is asking and where it will redirect.
4. The client exchanges the one-time code for an access token (valid 1 hour) and a refresh token (30 days, single use).

PKCE (S256) is required. Tokens and codes are stored only as hashes. After 10 wrong passwords in 15 minutes (on this page or the UI sign-in), both are locked for the rest of that window; tokens and sessions already issued keep working. Registration is open, as MCP clients expect, but capped at 20 new clients per hour, and clients that never sign in are deleted after a day. Revoke an app on the **Apps** page.

Clients connecting from the internet (ChatGPT, claude.ai, Gemini) need `PUBLIC_URL` set to an HTTPS address that reaches the server, e.g. a Cloudflare Tunnel.

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/oauth-protected-resource` | Which authorization server protects this API (RFC 9728) |
| `GET /.well-known/oauth-authorization-server` | OAuth endpoints and capabilities (RFC 8414) |
| `POST /oauth/register` | Dynamic client registration (RFC 7591) |
| `GET/POST /oauth/authorize` | Login page |
| `POST /oauth/token` | `authorization_code` and `refresh_token` grants |

## Tables

### Expense

One row per expense, in the `expenses` table.

| Field | Required | Notes |
|---|---|---|
| `id` | server | UUIDv7 |
| `amount` | yes | Decimal string or number, > 0, at most 2 decimal places, e.g. `"12.50"` |
| `currency` | yes | `USD` or `EUR` |
| `occurred_at` | yes | RFC 3339 **with offset**, e.g. `2026-11-07T13:20:00+01:00`; the offset is kept |
| `category` | yes | `food`, `groceries`, `transport`, `housing`, `utilities`, `health`, `entertainment`, `shopping`, `travel`, `subscriptions`, `education`, `gifts`, `other` |
| `merchant` | no | Up to 200 characters |
| `note` | no | Up to 2000 characters, only what the user adds |
| `created_at` | server | UTC |
| `updated_at` | server | UTC |

### Log

One row per change to an expense, in the `logs` table. A log row is written in the same transaction as the change, so the two can't disagree. There is no API for logs; read them from the database.

| Field | Notes |
|---|---|
| `id` | Auto-increment |
| `expense_id` | The changed expense; kept after the expense is deleted |
| `action` | `create`, `update` or `delete` |
| `before` | JSON snapshot of the expense before the change; null on create |
| `after` | JSON snapshot of the expense after the change; null on delete |
| `created_at` | UTC |

```bash
docker compose -f deploy/docker-compose.yaml --env-file .env stop
sqlite3 data/expense.db "SELECT action, before, after FROM logs WHERE expense_id = '<id>' ORDER BY id"
```

> **Stop the container before opening `data/expense.db` from the host.** SQLite runs in WAL mode, which coordinates readers and writers through shared memory and only works when all of them run on the same machine. On Docker Desktop the container runs in a VM, so a host `sqlite3` and the running container can corrupt the database. The same goes for copying the file as a backup.

## API

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Service status and version |
| POST | `/api/v1/expenses` | Create an expense |
| GET | `/api/v1/expenses?date_from=&date_to=` | List expenses by local date (`YYYY-MM-DD`, inclusive), oldest first |
| GET | `/api/v1/expenses/{id}` | Get an expense |
| PATCH | `/api/v1/expenses/{id}` | Update the fields sent; `null` clears an optional field |
| DELETE | `/api/v1/expenses/{id}` | Delete an expense permanently |

```bash
curl -X POST localhost:8000/api/v1/expenses -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"amount":"12.50","currency":"EUR","occurred_at":"2026-11-07T13:20:00+01:00","category":"food","merchant":"Rewe"}'
```

Errors look like `{"error":{"code":"validation_failed","message":"...","fields":{"amount":"must be greater than zero"}}}`.

## Development

Requires Go 1.27+ and Docker.

```bash
make lint     # go fix, go vet, gofmt check
make test     # all tests with the race detector; coverage in .coverage/
make mocks    # regenerate *_mock.go after changing a repository interface
AUTH_PASSWORD=... go run .   # run locally; uses data/expense.db and port 8000
```

Configuration is read from environment variables (see `.env.template`): `AUTH_PASSWORD` (required), `PUBLIC_URL`, `LOG_LEVEL`, `HTTP_PORT`, `HTTP_REQUEST_TIMEOUT`, `HTTP_MAX_BODY_BYTES`, `SQLITE_PATH`.

To get a token by hand, run the OAuth flow with [MCP Inspector](https://github.com/modelcontextprotocol/inspector) or follow the steps in `tests/api_test.go`.

## License

[MIT](LICENSE)
