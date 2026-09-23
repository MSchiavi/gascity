#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/test-failure-summary.sh
source "$script_dir/lib/test-failure-summary.sh"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
first="$scratch/first.log"
second="$scratch/second.log"
for (( i = 0; i < 300; i++ )); do printf 'setup line %s\n' "$i"; done >"$first"
printf '%s\n' '--- FAIL: TestFirstBuriedFailure (1.00s)' >>"$first"
printf '\0%s\n' '--- FAIL: TestSecondBuriedFailure (2.00s)' >>"$first"
printf '    --- FAIL: TestNestedBuriedFailure (0.10s)\n' >"$second"
printf '1\n' >"$first.failed"
printf '2\n' >"$second.failed"

summary="$(gc_test_failure_report "$scratch")"
for expected in TestFirstBuriedFailure TestSecondBuriedFailure TestNestedBuriedFailure; do
  if [[ "$summary" != *"$expected"* ]]; then
    echo "missing $expected from complete failure summary: $summary" >&2
    exit 1
  fi
done
[[ "$summary" == *"[first] exit 1"* && "$summary" == *"[second] exit 2"* ]] || {
  echo "summary omitted a failed log or exit code: $summary" >&2
  exit 1
}
[[ "$(wc -l < "$first")" -ge 300 ]] || exit 1
echo "failure-summary tests passed"
