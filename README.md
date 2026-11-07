# expense-tracker

Self-hosted expense log. Single user, SQLite, one Go binary, runs in Docker.

## Run

```bash
cp .env.template .env
docker compose -f deploy/docker-compose.yaml --env-file .env up -d --build
```

The database is stored in `data/expense.db`.

## Expense

| Field | Required | Notes |
|---|---|---|
| `amount` | yes | Decimal string or number, > 0, at most 2 decimal places, e.g. `"12.50"` |
| `currency` | yes | `USD` or `EUR` |
| `occurred_at` | yes | RFC 3339 **with offset**, e.g. `2026-11-07T13:20:00+01:00` |
| `category` | yes | `food`, `groceries`, `transport`, `housing`, `utilities`, `health`, `entertainment`, `shopping`, `travel`, `subscriptions`, `education`, `gifts`, `other` |
| `merchant` | no | Up to 200 characters |
| `note` | no | Up to 2000 characters, only what the user adds |

`id`, `created_at` and `updated_at` are set by the server. Every change is recorded in the `logs` table with before/after snapshots.

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
curl -X POST localhost:8000/api/v1/expenses -H "Content-Type: application/json" \
  -d '{"amount":"12.50","currency":"EUR","occurred_at":"2026-11-07T13:20:00+01:00","category":"food","merchant":"Rewe"}'
```

Errors look like `{"error":{"code":"validation_failed","message":"...","fields":{"amount":"must be greater than zero"}}}`.

## Development

Requires Go 1.27+ and Docker.

```bash
make lint     # go fix, go vet, gofmt check
make test     # all tests with the race detector; coverage in .coverage/
make mocks    # regenerate *_mock.go after changing a repository interface
go run .      # run locally; uses data/expense.db and port 8000
```

Configuration is read from environment variables (see `.env.template`): `LOG_LEVEL`, `HTTP_PORT`, `HTTP_REQUEST_TIMEOUT`, `HTTP_MAX_BODY_BYTES`, `SQLITE_PATH`.

## License

[MIT](LICENSE)
