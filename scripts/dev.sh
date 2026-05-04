#!/usr/bin/env sh
set -eu

GO="${GO:-go}"
SERVER_PKG="${SERVER_PKG:-./cmd/server}"
BINARY="${BINARY:-bin/omp-session-viewer-server}"
ADDR="${ADDR:-127.0.0.1:8080}"
OMP_ROOT="${OMP_ROOT:-~/.omp/agent}"
FRONTEND_ORIGIN="${FRONTEND_ORIGIN:-http://localhost:5173}"

mkdir -p "$(dirname "$BINARY")"
"$GO" build -o "$BINARY" "$SERVER_PKG"

trap 'exit 0' INT TERM
"$BINARY" -addr "$ADDR" -omp-root "$OMP_ROOT" -frontend-origin "$FRONTEND_ORIGIN"
