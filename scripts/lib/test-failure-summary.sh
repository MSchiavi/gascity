#!/usr/bin/env bash

# Print every named Go test failure, even when it appears after the runner's
# first 240 displayed lines. NULs from captured subprocess output are treated
# as line breaks; the original log remains untouched for diagnosis.
gc_test_failure_summary() {
  local label="$1" log="$2" matches
  matches="$(LC_ALL=C tr '\000' '\n' <"$log" |
    grep -aE '^[[:space:]]*--- FAIL:|^FAIL([[:space:]]|$)|^panic:|^fatal error:' || true)"
  if [[ -n "$matches" ]]; then
    printf '%s\n' "$matches" | awk -v label="$label" '{ print "[" label "] " $0 }'
  else
    printf '[%s] no named Go test failure found; last 30 log lines:\n' "$label"
    tail -n 30 "$log"
  fi
}

# The end-of-run artifact survives terminal/tool output truncation. A failed
# job writes <label>.log.failed; its full log stays beside that marker.
gc_test_failure_report() {
  local log_dir="$1" marker log label status found=0
  for marker in "$log_dir"/*.log.failed; do
    [[ -f "$marker" ]] || continue
    found=1
    log="${marker%.failed}"
    label="${log##*/}"
    label="${label%.log}"
    read -r status <"$marker" || status="unknown"
    printf '[%s] exit %s; full log: %s\n' "$label" "$status" "$log"
    gc_test_failure_summary "$label" "$log"
  done
  if [[ "$found" -eq 0 ]]; then
    echo "fan-out failed without a completed job failure marker; inspect the job logs"
  fi
}
