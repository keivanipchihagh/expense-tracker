#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
coverage_dir="$root/.coverage"
coverage_file="$coverage_dir/coverage.out"

cd "$root"

if [ -z "$(go list ./... 2>/dev/null)" ]; then
	echo "No Go packages yet, skipping tests."
	exit 0
fi

mkdir -p "$coverage_dir"
go test -race -coverprofile="$coverage_file" -covermode=atomic ./...
echo "Tests completed."
