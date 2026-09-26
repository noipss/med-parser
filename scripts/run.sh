#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[ -x "$ROOT/bin/uzi-backend" ] || "$ROOT/scripts/build-backend.sh"
( sleep 1.5; open "http://127.0.0.1:8765" 2>/dev/null || xdg-open "http://127.0.0.1:8765" 2>/dev/null || true ) &
exec "$ROOT/bin/uzi-backend" "$@"
