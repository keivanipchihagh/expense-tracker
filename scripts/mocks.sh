#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Remove all existing mock files so deleted interfaces don't leave stale mocks
find . -name "*_mock.go" -type f -not -path "./.git/*" -delete

# Regenerate from the //go:generate directives (mockgen is pinned in go.mod)
go generate ./...
echo "Mock files generated."
