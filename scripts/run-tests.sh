#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

echo "BUILD_STATUS: RUNNING"
if ! GOEXPERIMENT=jsonv2 go build -o bin/ ./cmd/contractor 2>&1; then
  echo "BUILD_STATUS: FAILED"
  exit 1
fi
echo "BUILD_STATUS: OK"
echo "---"

exec python3 scripts/check-fixtures.py
