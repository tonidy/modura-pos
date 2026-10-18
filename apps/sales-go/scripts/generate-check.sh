#!/usr/bin/env bash
# Fails if the file `go generate` produces is stale: the oapi-codegen output for the
# current generated/openapi/sales-go.json.
set -euo pipefail

cd "$(dirname "$0")/.."

fresh="$(mktemp)"
trap 'rm -f "$fresh"' EXIT
stale=0

go tool oapi-codegen -config oapi-codegen.yaml -o "$fresh" ../../generated/openapi/sales-go.json
if ! cmp -s "$fresh" internal/api/api.gen.go; then
  echo "apps/sales-go/internal/api/api.gen.go is stale." >&2
  stale=1
fi

if [ "$stale" -ne 0 ]; then
  echo "Run \`pnpm generate\` and commit the result." >&2
  exit 1
fi
echo "Go generated file is up to date."
