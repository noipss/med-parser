#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/backend" && go run ./cmd/server &
BACK=$!
trap 'kill $BACK 2>/dev/null' EXIT
cd "$ROOT/frontend"
[ -d node_modules ] || npm install --no-audit --no-fund
npm run dev
