#!/usr/bin/env bash
set -euo pipefail

duration="${SOAK_DURATION:-10m}"
case "$duration" in
  *s) seconds="${duration%s}" ;;
  *m) seconds=$(( ${duration%m} * 60 )) ;;
  *h) seconds=$(( ${duration%h} * 3600 )) ;;
  *) echo "SOAK_DURATION must end in s, m, or h" >&2; exit 2 ;;
esac

if ! [[ "$seconds" =~ ^[0-9]+$ ]] || (( seconds < 1 )); then
  echo "SOAK_DURATION must be a positive whole number" >&2
  exit 2
fi

deadline=$(( $(date +%s) + seconds ))
iteration=0
while (( $(date +%s) < deadline )); do
  iteration=$((iteration + 1))
  echo "soak iteration ${iteration}"
  go test ./cmd/... ./internal/... ./pkg/... ./test/... -count=1
done

echo "soak test completed: ${iteration} iteration(s) in ${duration}"
