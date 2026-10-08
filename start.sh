#!/usr/bin/env bash
# Run shelf-server locally for development. Docker comes later.
set -euo pipefail
cd "$(dirname "$0")"

export SHELF_DATA_DIR="${SHELF_DATA_DIR:-./data}"
export SHELF_LISTEN="${SHELF_LISTEN:-:8080}"
export SHELF_PASSWORD="${SHELF_PASSWORD:-shelf}"   # dev-only default

mkdir -p "$SHELF_DATA_DIR"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
exec go run -ldflags "-X main.version=$VERSION" ./cmd/shelf-server "$@"
