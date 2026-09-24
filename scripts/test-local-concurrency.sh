#!/usr/bin/env bash
#
# test-local-concurrency.sh — unit tests for load-aware outer job counting
# (scripts/test-local-job-count) and GOFLAGS=-p= inner-test-binary
# parallelism (scripts/lib/inner-parallelism.sh).
#
# Part A exercises scripts/test-local-job-count as a real subprocess, since
# its behavior spans multiple detection functions and env-var seams already
# tested that way. Part B sources scripts/lib/inner-parallelism.sh directly
# and calls gc_inner_parallelism in-process.
#
# Coverage: outer-job load subtraction (zero/mid/saturating load), the
# min_auto_jobs=2 floor, a small machine skipping load adjustment
# entirely, fractional-load truncation (not rounding), a malformed
# GC_TEST_LOCAL_LOADAVG failing by name, a live-host regression guard that
# the default path actually reads /proc/loadavg (skipped when strace is
# unavailable), inner-parallelism arithmetic (clean division, the real
# ga-04m84s repro numbers, job-count-exceeds-outer-jobs, the trivial 1x1
# case, the GC_TEST_INNER_P override, a malformed GC_TEST_INNER_P failing by
# name), and executable test-local-parallel wiring.

set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
JOB_COUNT="$TEST_DIR/test-local-job-count"
LOCAL_PARALLEL="$TEST_DIR/test-local-parallel"
INNER_LIB="$TEST_DIR/lib/inner-parallelism.sh"

pass=0; fail=0
record_pass() { echo "  ok   $1"; pass=$((pass + 1)); }
record_fail() { echo "  FAIL $1 — $2"; fail=$((fail + 1)); }

assert_eq() {
    local name="$1" got="$2" want="$3"
    if [[ "$got" == "$want" ]]; then record_pass "$name"
    else record_fail "$name" "got '$got', want '$want'"; fi
}
assert_true()  { if "${@:2}"; then record_pass "$1"; else record_fail "$1" "expected true"; fi; }
assert_contains() {
    local name="$1" haystack="$2" needle="$3"
    if [[ "$haystack" == *"$needle"* ]]; then record_pass "$name"
    else record_fail "$name" "missing '$needle' in: $haystack"; fi
}

# A huge, non-binding memory pin so every Part A case below exercises
# load-awareness alone — never accidentally gated by the real host's live
# /proc/meminfo or cgroup budget.
HUGE_MEM_KIB=$((64 * 1024 * 1024))
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT
printf '%s\n' '#!/bin/sh' 'if [ "$1" = "-n" ] && [ "$2" = "vm.loadavg" ]; then' \
    '  printf "{ 5.75 0.00 0.00 }\n"' 'fi' >"$fixture_dir/sysctl"
chmod +x "$fixture_dir/sysctl"

# ============================================================
# Part A — scripts/test-local-job-count (real subprocess, pinned cpus/memory)
# ============================================================

GOT="$(GC_TEST_LOCAL_CPUS=16 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" GC_TEST_LOCAL_LOADAVG=0 "$JOB_COUNT")"
assert_eq "loadavg.zero_load_unchanged" "$GOT" "16"

GOT="$(GC_TEST_LOCAL_CPUS=16 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" GC_TEST_LOCAL_LOADAVG=10 "$JOB_COUNT")"
assert_eq "loadavg.subtracts_from_cpus" "$GOT" "6"

GOT="$(GC_TEST_LOCAL_CPUS=16 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" GC_TEST_LOCAL_LOADAVG=28 "$JOB_COUNT")"
assert_eq "loadavg.floors_at_min_auto_jobs" "$GOT" "2"

GOT="$(GC_TEST_LOCAL_CPUS=4 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" GC_TEST_LOCAL_LOADAVG=28 "$JOB_COUNT")"
assert_eq "loadavg.small_machine_skips_load_adjustment" "$GOT" "4"

GOT="$(GC_TEST_LOCAL_CPUS=16 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" GC_TEST_LOCAL_LOADAVG=3.9 "$JOB_COUNT")"
assert_eq "loadavg.truncates_fractional_load" "$GOT" "13"

GOT="$(PATH="$fixture_dir:$PATH" GC_TEST_LOCAL_CPUS=12 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" \
    GC_TEST_LOCAL_LOADAVG_FILE="$fixture_dir/missing" "$JOB_COUNT")"
assert_eq "loadavg.macos_sysctl_fallback" "$GOT" "7"

MALFORMED_OUT="$(GC_TEST_LOCAL_CPUS=16 GC_TEST_LOCAL_MEMORY_KIB="$HUGE_MEM_KIB" GC_TEST_LOCAL_LOADAVG=abc "$JOB_COUNT" 2>&1)"
MALFORMED_RC=$?
assert_true "loadavg.malformed_nonzero_exit" test "$MALFORMED_RC" -ne 0
assert_contains "loadavg.malformed_names_var" "$MALFORMED_OUT" "GC_TEST_LOCAL_LOADAVG"

# Regression guard: the default (no-override) path must actually read
# /proc/loadavg, mirroring how detect_memory_kib is already proven to read
# /proc/meminfo. Skipped gracefully where strace is unavailable (containers
# without CAP_SYS_PTRACE, macOS) rather than failing the whole suite on an
# environment gap unrelated to the feature itself. The cpu seam is pinned
# above the small-machine threshold because test-local-job-count skips
# load-awareness entirely at cpus <= min_auto_jobs*2 — an unpinned probe
# inherits the real host's core count and so false-fails on a small host.
# GC_TEST_LOCAL_LOADAVG stays unset, which is what gives the guard its
# teeth: it still proves the default path reads /proc/loadavg.
if command -v strace >/dev/null 2>&1; then
    # Captured into a variable rather than piped live into grep: a piped
    # `grep -q` closes its end of the pipe as soon as it finds a match, and
    # under pipefail that early close can race strace's own exit — SIGPIPEing
    # strace mid-write turns into a spurious pipeline failure even though the
    # match was genuinely found. Capturing first removes the race entirely.
    STRACE_OUT="$(GC_TEST_LOCAL_CPUS=16 strace -f -e trace=%file -- "$JOB_COUNT" 2>&1 >/dev/null || true)"
    if [[ "$STRACE_OUT" == *"/proc/loadavg"* ]]; then
        record_pass "loadavg.default_path_opens_proc_loadavg"
    else
        record_fail "loadavg.default_path_opens_proc_loadavg" "/proc/loadavg not opened by the default (no-override) path"
    fi
else
    echo "  skip loadavg.default_path_opens_proc_loadavg — strace not installed"
fi

# ============================================================
# Part B — scripts/lib/inner-parallelism.sh (sourced in-process)
# ============================================================

if [[ -r "$INNER_LIB" ]]; then
    # shellcheck source=lib/inner-parallelism.sh disable=SC1091
    . "$INNER_LIB"
fi
assert_true "inner_p.lib_file_exists" test -r "$INNER_LIB"

GOT="$(gc_inner_parallelism 16 4 2>/dev/null)"
assert_eq "inner_p.clean_division" "$GOT" "4"

GOT="$(gc_inner_parallelism 16 9 2>/dev/null)"
assert_eq "inner_p.matches_ga_04m84s_repro_numbers" "$GOT" "1"

GOT="$(gc_inner_parallelism 4 9 2>/dev/null)"
assert_eq "inner_p.job_count_exceeds_outer_jobs" "$GOT" "1"

GOT="$(gc_inner_parallelism 1 1 2>/dev/null)"
assert_eq "inner_p.trivial_single_job" "$GOT" "1"

GOT="$(GC_TEST_INNER_P=7 gc_inner_parallelism 16 9 2>/dev/null)"
assert_eq "inner_p.explicit_override_wins" "$GOT" "7"

MALFORMED_INNER_OUT="$(GC_TEST_INNER_P=abc gc_inner_parallelism 16 9 2>&1)"
MALFORMED_INNER_RC=$?
assert_true "inner_p.malformed_override_nonzero_exit" test "$MALFORMED_INNER_RC" -ne 0
assert_contains "inner_p.malformed_override_names_var" "$MALFORMED_INNER_OUT" "GC_TEST_INNER_P"

GOT="$(gc_shared_auto_jobs 8 2 2>/dev/null)"
assert_eq "shared_budget.reserves_both_slots_before_second_starts" "$GOT" "4"
GOT="$(gc_shared_auto_jobs 3 2 2>/dev/null)"
assert_eq "shared_budget.rounds_down" "$GOT" "1"
GOT="$(gc_shared_auto_jobs 1 2 2>/dev/null)"
assert_eq "shared_budget.keeps_one_job" "$GOT" "1"

mkdir -p "$fixture_dir/bin"
cat > "$fixture_dir/bin/xargs" <<'EOF'
#!/bin/sh
cat > "$GC_LOCAL_JOBSPECS"
while [ "$#" -gt 0 ] && [ "$1" != bash ]; do shift; done
[ "$#" -gt 0 ] || exit 1
exec "$@" "probe::$GC_LOCAL_PROBE"
EOF
chmod +x "$fixture_dir/bin/xargs"
cat > "$fixture_dir/probe" <<EOF
#!/bin/sh
printf 'GOFLAGS=%s\nGOMAXPROCS=%s\nOBSERVABLE_TEST_LOG=%s\nOBSERVABLE_FAILURE_LINES=%s\nGC_CITY=%s\nGC_HOME=%s\nGC_SESSION_ID=%s\n' \
    "\$GOFLAGS" "\$GOMAXPROCS" "\${OBSERVABLE_TEST_LOG-<unset>}" \
    "\${OBSERVABLE_FAILURE_LINES-<unset>}" "\${GC_CITY-<unset>}" \
    "\${GC_HOME-<unset>}" "\${GC_SESSION_ID-<unset>}" > "$fixture_dir/probe.out"
EOF
chmod +x "$fixture_dir/probe"

for mode in fast full; do
    jobspecs="$fixture_dir/$mode.jobspecs"
    runner_output="$fixture_dir/$mode.out"
    PATH="$fixture_dir/bin:$PATH" GC_PUSH_GATE_NO_CAP=1 LOCAL_TEST_JOBS=2 GC_TEST_INNER_P=7 \
        OBSERVABLE_TEST_LOG=fixture-log OBSERVABLE_FAILURE_LINES=17 \
        GC_CITY=must-not-leak GC_HOME=must-not-leak GC_SESSION_ID=must-not-leak \
        GO_TEST_TIMEOUT=999h GC_LOCAL_JOBSPECS="$jobspecs" GC_LOCAL_PROBE="$fixture_dir/probe" \
        "$LOCAL_PARALLEL" "$mode" > "$runner_output" 2>&1
    runner_rc=$?
    assert_eq "wiring.$mode.runner_exit" "$runner_rc" "0"
    if [[ "$runner_rc" -ne 0 ]]; then
        continue
    fi
    tr '\000' '\n' < "$jobspecs" > "$fixture_dir/$mode.jobs"
    assert_true "wiring.$mode.selftest_job" grep -q '^local-concurrency-selftest::' "$fixture_dir/$mode.jobs"
    assert_true "wiring.$mode.goflags" grep -q 'GOFLAGS=.*-p=7' "$fixture_dir/probe.out"
    assert_true "wiring.$mode.gomaxprocs" grep -q '^GOMAXPROCS=7$' "$fixture_dir/probe.out"
    assert_true "wiring.$mode.observable_log" grep -q '^OBSERVABLE_TEST_LOG=fixture-log$' "$fixture_dir/probe.out"
    assert_true "wiring.$mode.observable_lines" grep -q '^OBSERVABLE_FAILURE_LINES=17$' "$fixture_dir/probe.out"
    for key in GC_CITY GC_HOME GC_SESSION_ID; do
        assert_true "wiring.$mode.$key.sanitized" grep -q "^$key=<unset>$" "$fixture_dir/probe.out"
    done
    assert_true "wiring.$mode.output" grep -q 'inner_p=7' "$runner_output"
done


echo
echo "local-concurrency tests: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
