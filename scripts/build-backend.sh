#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "▸ UI (React)"
cd "$ROOT/frontend"
[ -d node_modules ] || npm install --no-audit --no-fund
npm run build
rm -rf "$ROOT/backend/internal/web/dist"
cp -R "$ROOT/frontend/dist" "$ROOT/backend/internal/web/dist"

echo "▸ Go-бэкенд"
cd "$ROOT/backend"
TRIPLE="$(rustc -vV 2>/dev/null | sed -n 's/^host: //p')"
TRIPLE="${TRIPLE:-$(uname -m | sed 's/arm64/aarch64/')-apple-darwin}"
EXT=""; [[ "$TRIPLE" == *windows* ]] && EXT=".exe"
mkdir -p "$ROOT/src-tauri/binaries" "$ROOT/bin"
go build -trimpath -ldflags "-s -w" -o "$ROOT/bin/uzi-backend$EXT" ./cmd/server
cp "$ROOT/bin/uzi-backend$EXT" "$ROOT/src-tauri/binaries/uzi-backend-$TRIPLE$EXT"
echo "✓ bin/uzi-backend$EXT и src-tauri/binaries/uzi-backend-$TRIPLE$EXT"
