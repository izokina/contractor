#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

echo "BUILD_STATUS: RUNNING"
if ! go build -o bin/contractor ./cmd/contractor 2>&1 ||
  ! go build -race -o bin/contractor-race ./cmd/contractor 2>&1; then
  echo "BUILD_STATUS: FAILED"
  exit 1
fi
echo "BUILD_STATUS: OK"
echo "---"

status=0
python3 scripts/check-fixtures.py --race-bin bin/contractor-race bin/contractor || status=1
echo "---"
echo "Again, with the merger moving its terms to a temporary file on every add:"
python3 scripts/check-fixtures.py --race-bin bin/contractor-race --args=-merge-memory=1 bin/contractor || status=1
exit "$status"
