#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
"$ROOT/scripts/build-backend.sh"
cd "$ROOT"
[ -d node_modules ] || npm install --no-audit --no-fund
npx tauri build "$@"
echo "✓ Приложение: src-tauri/target/release/bundle/"
