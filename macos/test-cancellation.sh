#!/bin/bash
# Process lifecycle regression checks; only macOS Command Line Tools required.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD="$(mktemp -d "${TMPDIR:-/tmp}/replicant-cancellation.XXXXXX")"
trap 'rm -rf "$BUILD"' EXIT
swiftc -parse-as-library \
  "$ROOT/macos/ReplicantMenu/Sources/ReplicantMenu/EjectGuard.swift" \
  "$ROOT/macos/tests/ActiveScanTests.swift" \
  -o "$BUILD/test-cancellation"
"$BUILD/test-cancellation"
