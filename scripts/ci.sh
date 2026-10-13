#!/usr/bin/env bash
# The same bounded backend gates run locally and in GitHub Actions.
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
mode="${1:-core}"
resume=""
if (($# == 3)) && [[ "$mode" == core && "$2" == --resume ]]; then
  resume="$3"
elif (($# > 1)); then
  echo "Usage: $0 [core|postgres] [--resume trusted-local-core-directory]" >&2
  exit 2
fi
if [[ "$mode" != core && "$mode" != postgres ]]; then
  echo 'Mode must be core or postgres.' >&2
  exit 2
fi
if [[ -n "$resume" && ( -n "${CI:-}" || -n "${GITHUB_ACTIONS:-}" ) ]]; then
  echo 'CI requires fresh evidence; local checkpoint resumption is not allowed.' >&2
  exit 2
fi

command -v go >/dev/null
command -v python3 >/dev/null
# Do not silently download a different compiler or inherit test-selection flags.
export GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOWORK=off
# Make/shell launcher metadata is not a test input. Normalize it before both
# test execution and checkpoint identity so make -> direct-script resume works.
unset MAKEFLAGS MFLAGS MAKELEVEL SHLVL OLDPWD
export PWD="$root"
required_go="$(awk '$1 == "go" {version="go" $2} $1 == "toolchain" {toolchain=$2} END {print toolchain ? toolchain : version}' go.mod)"
actual_go="$(go env GOVERSION)"
if [[ "$actual_go" != "$required_go" ]]; then
  echo "Install $required_go from the official Go distribution (found $actual_go)." >&2
  exit 2
fi
if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
  echo 'These gates require CGO_ENABLED=1 and a C compiler (SQLite and race tests).' >&2
  exit 2
fi

jobs="${CI_GO_PARALLELISM:-2}"
parallel="${CI_TEST_PARALLELISM:-4}"
for value in "$jobs" "$parallel"; do
  if ! [[ "$value" =~ ^[1-9][0-9]*$ ]] || ((value > 32)); then
    echo 'CI_GO_PARALLELISM and CI_TEST_PARALLELISM must be integers from 1 to 32.' >&2
    exit 2
  fi
done
export GOMAXPROCS="${GOMAXPROCS:-$parallel}"
packages=(./cmd/... ./internal/... ./pkg/... ./test/...)
test_flags=(-count=1 -p "$jobs" -parallel "$parallel" -timeout 20m)

if [[ "$mode" == postgres ]]; then
  # Refuse inherited services/connection options; the acknowledged DSN is the
  # sole database target. The helper never prints credentials.
  unset PGSERVICE PGSERVICEFILE PGHOSTADDR PGOPTIONS PGDATABASE PGUSER PGPASSWORD PGHOST PGPORT
  python3 scripts/ci/check-postgres.py
else
  # The core suite must not consume a database from the caller's shell.
  unset DB_DSN PAYMENT_TEST_POSTGRES_DSN CHANNEL_TEST_POSTGRES_DSN
  unset AUTOMATION_HISTORY_TEST_POSTGRES_DSN EX_CHAT_AUTH_POSTGRES_TESTS EX_CHAT_AUT_POSTGRES_TESTS
  unset SAML_TEST_POSTGRES_DSN
fi
unset EX_CHAT_MODULE_ACCEPTANCE

mkdir -p tmp
if [[ -n "$resume" ]]; then
  results="$(cd -- "$resume" && pwd)"
  if [[ "$(dirname -- "$results")" != "$root/tmp" ]]; then
    echo 'Resume only your own evidence directory immediately under repository tmp/.' >&2
    exit 2
  fi
  echo '==> Explicit trusted local resumption; completed batches will be revalidated.'
else
  results="$(mktemp -d "$root/tmp/ci-${mode}.XXXXXX")"
fi
runner=(python3 scripts/ci/runner.py "$mode" "$results" --jobs "$jobs" --parallel "$parallel")
if [[ -n "$resume" ]]; then runner+=(--resume); fi
echo "==> $mode gate; toolchain $actual_go; evidence: $results"
printf '==> Controls: GOTOOLCHAIN=%s GOFLAGS=%s CGO_ENABLED=%s GOMAXPROCS=%s CI_GO_PARALLELISM=%s CI_TEST_PARALLELISM=%s test-timeout=20m\n' \
  "$GOTOOLCHAIN" "$GOFLAGS" "$(go env CGO_ENABLED)" "$GOMAXPROCS" "$jobs" "$parallel"
echo '==> Local gate execution only; this output does not establish a hosted GitHub Actions run.'

if [[ "$mode" == core ]]; then
  python3 -m unittest discover -s scripts/ci -p 'test_*.py'
  go mod verify
  go vet -p "$jobs" "${packages[@]}"
  for target in server module-server module-migrate; do
    go build -p "$jobs" -trimpath -buildvcs=false -o "$results/$target" "./cmd/$target"
  done
  "${runner[@]}"
  echo '==> Core backend components passed. Native obligations require the separate PostgreSQL gate. Module isolation remains staged ARCH-08 evidence only.'
else
  export EX_CHAT_AUTH_POSTGRES_TESTS=1 EX_CHAT_AUT_POSTGRES_TESTS=1
  export PAYMENT_TEST_POSTGRES_DSN="$DB_DSN" CHANNEL_TEST_POSTGRES_DSN="$DB_DSN"
  export AUTOMATION_HISTORY_TEST_POSTGRES_DSN="$DB_DSN"
  export SAML_TEST_POSTGRES_DSN="$DB_DSN"
  echo '==> PostgreSQL opt-ins: auth, automation, payment, channel, history, SAML and DB_DSN-based tests; connection credentials are not printed.'
  "${runner[@]}"
  echo '==> Selected real-PostgreSQL regressions and Redis PING passed; no provider or load acceptance is implied.'
fi
