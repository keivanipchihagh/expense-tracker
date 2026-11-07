#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

if [ -z "$(go list ./... 2>/dev/null)" ]; then
	echo "No Go packages yet, skipping lint."
	exit 0
fi

go fix ./...
go vet ./...

unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
	echo "Files need gofmt:"
	echo "$unformatted"
	exit 1
fi
echo "Lint/vet completed."
