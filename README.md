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

## Development

```bash
make lint
make test
```

## License

[MIT](LICENSE)
