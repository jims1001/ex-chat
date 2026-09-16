#!/usr/bin/env bash
set -euo pipefail

# Directory isolation: test only Go packages, avoiding recursive scanning into frontend node_modules
echo "==> Running backend test suite with directory isolation..."
go test ./cmd/... ./internal/... ./pkg/... ./test/... -count=1 "$@"
echo "==> All backend tests passed successfully!"
