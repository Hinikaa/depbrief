#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"
echo "$ depbrief diff --no-changelog internal/lockfile/testdata/npm/package-lock.json internal/lockfile/testdata/npm/package-lock.new.json"
go run ./cmd/depbrief diff --no-changelog \
  internal/lockfile/testdata/npm/package-lock.json \
  internal/lockfile/testdata/npm/package-lock.new.json
