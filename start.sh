#!/usr/bin/env bash
# Run replicant-server locally for development. Docker comes later.
set -euo pipefail
cd "$(dirname "$0")"

export REPLICANT_DATA_DIR="${REPLICANT_DATA_DIR:-./data}"
export REPLICANT_LISTEN="${REPLICANT_LISTEN:-:8080}"
export REPLICANT_PASSWORD="${REPLICANT_PASSWORD:-replicant}"   # dev-only default

mkdir -p "$REPLICANT_DATA_DIR"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
exec go run -ldflags "-X main.version=$VERSION" ./cmd/replicant-server "$@"
