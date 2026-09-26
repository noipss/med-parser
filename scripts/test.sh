#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/backend" && go vet ./... && go test -race ./...
cd "$ROOT/frontend" && npm run build
echo "✓ Все проверки пройдены"
