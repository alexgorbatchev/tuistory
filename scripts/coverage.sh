#!/usr/bin/env bash
set -euo pipefail

cd -- "$(dirname -- "$0")/.."
mkdir -p .tmp/test-tmp
export TMPDIR="$PWD/.tmp/test-tmp"

# Fresh native coverage from this run only; subprocesses use the same mode.
rm -rf .tmp/coverage-unit .tmp/coverage-merged
mkdir -p .tmp/coverage-unit .tmp/coverage-merged
go test -race -v -count=1 -covermode=atomic -coverpkg=./... ./... -args "-test.gocoverdir=$PWD/.tmp/coverage-unit"
go tool covdata merge -pcombine -i=.tmp/coverage-unit,.tmp/e2e-coverage -o=.tmp/coverage-merged
go tool covdata textfmt -i=.tmp/coverage-merged -o=.tmp/coverage.out
go tool cover -func=.tmp/coverage.out
awk 'NR > 1 { total += $2; if ($3 > 0) covered += $2 }
END {
    if (total == 0) { print "No coverage statements recorded"; exit 1 }
    percent = 100 * covered / total
    printf "Behavioral statement coverage: %.2f%% (minimum 90%%)\n", percent
    exit (percent < 90)
}' .tmp/coverage.out
