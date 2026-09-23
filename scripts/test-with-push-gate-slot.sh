#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
wrapper="$script_dir/with-push-gate-slot"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
city="$scratch/city"
mkdir -p "$city"
: >"$city/city.toml"
slot="$city/.gc/gate-slots/slot-0.lock"

# A direct command must hold the slot while it runs and release it afterward.
GC_CITY_PATH="$city" GC_PUSH_GATE_SLOT_HELD= PUSH_GATE_MAX_CONCURRENT=1 \
  "$wrapper" selftest -- bash -c 'printf "%s\n" "$$" >"$1"; sleep 30' _ "$scratch/child.pid" &
wrapper_pid=$!
for (( i = 0; i < 50; i++ )); do
  [[ -s "$scratch/child.pid" ]] && break
  sleep 0.1
done
if [[ ! -s "$scratch/child.pid" ]]; then
  kill -TERM "$wrapper_pid" 2>/dev/null || true
  wait "$wrapper_pid" 2>/dev/null || true
  echo "wrapper did not start its child" >&2
  exit 1
fi
if flock -n "$slot" -c true; then
  echo "wrapper did not hold its slot" >&2
  kill -TERM "$wrapper_pid" 2>/dev/null || true
  wait "$wrapper_pid" 2>/dev/null || true
  exit 1
fi

set +e
GC_CITY_PATH="$city" GC_PUSH_GATE_SLOT_HELD= PUSH_GATE_MAX_CONCURRENT=1 \
  PUSH_GATE_MAX_WAIT_SECONDS=0 PUSH_GATE_POLL_SECONDS=1 \
  "$wrapper" contended -- true >"$scratch/contended.log" 2>&1
contended_status=$?
set -e
if [[ "$contended_status" -ne 75 ]]; then
  echo "contended wrapper returned $contended_status, want 75" >&2
  kill -TERM "$wrapper_pid" 2>/dev/null || true
  wait "$wrapper_pid" 2>/dev/null || true
  exit 1
fi

read -r child_pid <"$scratch/child.pid"
kill -TERM "$wrapper_pid"
wait "$wrapper_pid" 2>/dev/null || true
for (( i = 0; i < 50; i++ )); do
  if ! kill -0 "$child_pid" 2>/dev/null; then break; fi
  sleep 0.1
done
if kill -0 "$child_pid" 2>/dev/null; then
  echo "wrapper left its child alive after SIGTERM" >&2
  exit 1
fi
if ! flock -n "$slot" -c true; then
  echo "wrapper kept its slot after SIGTERM" >&2
  exit 1
fi

# A nested recipe must not consume a second slot or wait on itself.
GC_CITY_PATH="$city" GC_PUSH_GATE_SLOT_HELD=1 PUSH_GATE_MAX_CONCURRENT=1 \
  "$wrapper" nested -- bash -c 'test "$GC_PUSH_GATE_SLOT_HELD" = 1'

# The direct Make recipes use env -i, whose empty GOMAXPROCS argument was
# expanded before entering the wrapper. It must receive the shared budget.
GOMAXPROCS= GC_CITY_PATH="$city" GC_PUSH_GATE_SLOT_HELD= PUSH_GATE_MAX_CONCURRENT=2 \
  GC_TEST_LOCAL_CPUS=4 GC_TEST_LOCAL_MEMORY_KIB=67108864 GC_TEST_LOCAL_LOADAVG=0 \
  "$wrapper" bounded -- env -i GOMAXPROCS= bash -c 'test "$GOMAXPROCS" = 2'
GOMAXPROCS=3 GC_CITY_PATH="$city" GC_PUSH_GATE_SLOT_HELD= PUSH_GATE_MAX_CONCURRENT=2 \
  "$wrapper" explicit -- env -i GOMAXPROCS=3 bash -c 'test "$GOMAXPROCS" = 3'
echo "with-push-gate-slot tests passed"
