#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

if [ -z "$(go list ./... 2>/dev/null)" ]; then
	echo "No Go packages yet, skipping lint."
	exit 0
fi

go fix ./...
go vet ./...
echo "Lint/vet completed."
