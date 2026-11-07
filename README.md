# expense-tracker

Self-hosted expense log with user-defined fields. Single user, SQLite, one Go binary, runs in Docker.

## Run

```bash
cp .env.template .env
docker compose -f deploy/docker-compose.yaml --env-file .env up -d --build
```

## API

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | none | Service status and version |
| POST | `/api/v1/expenses` | none (for now) | Create an expense |
| GET | `/api/v1/expenses?date_from=&date_to=` | none (for now) | List expenses, oldest first |
| GET | `/api/v1/expenses/{id}` | none (for now) | Get an expense |
| PATCH | `/api/v1/expenses/{id}` | none (for now) | Update an expense (`null` clears a field) |
| DELETE | `/api/v1/expenses/{id}` | none (for now) | Delete an expense |

> The expense endpoints are stubs: they validate the request shape and return empty data.

## Development

```bash
make lint
make test
```

## License

[MIT](LICENSE)
