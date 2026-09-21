package scripts_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Contract for gcy-98p: `make test` must shard examples/gastown instead of
// running it inside the unsharded ./... sweep. The 250-test binary outgrew
// the 15m per-binary timeout on a loaded host (915s wall, no individual
// failure), failing the full-suite gate for branches that touch no gastown
// files. The suite is serial (no t.Parallel) and subprocess-heavy, so its
// wall time scales with box contention rather than core count; splitting it
// into per-shard binaries keeps every binary comfortably under the timeout.
//
// The recipe shape mirrors the cmd/gc sharding (gcy-sdm): one sweep over
// every package except the sharded one, then a sequential
// test-go-test-shard loop.
//
// Helper names are gastown-prefixed to coexist with the cmd/gc sharding
// contract (scripts/make_test_cmdgc_shard_test.go) in this same package.
func TestMakeTestShardsGastownExample(t *testing.T) {
	recipe := gastownMakeTestDryRun(t)

	if !strings.Contains(recipe, "test-go-test-shard ./examples/gastown") {
		t.Fatalf("make test runs examples/gastown unsharded; it must shard it via test-go-test-shard:\n%s", recipe)
	}
	total := gastownShardTotal(t, recipe)
	if total <= 1 {
		t.Fatalf("make test shards examples/gastown %d ways; more than one shard is what keeps each binary under the timeout", total)
	}
}

// TestMakeTestSweepExcludesGastownExample pins the other half of the sharding:
// the unsharded sweep must not list the examples/gastown package, or the
// suite pays for it twice and the long pole is back in the single-binary
// timeout.
func TestMakeTestSweepExcludesGastownExample(t *testing.T) {
	sweep := gastownSweepLine(t)
	const gastownPackage = "github.com/gastownhall/gascity/examples/gastown"
	for _, field := range strings.Fields(sweep) {
		if field == gastownPackage {
			t.Fatalf("make test sweep still lists %s; examples/gastown must run sharded only:\n%s", gastownPackage, sweep)
		}
	}
}

// TestMakeTestGastownShardsRunFastLoop keeps the shards on the same fast unit
// loop the sweep runs: GC_FAST_UNIT=1 gates the slow process-backed scenarios
// and GO_TEST_COUNT=1 disables result caching so the gate reports a real run
// instead of stalling on cache-input hashing.
func TestMakeTestGastownShardsRunFastLoop(t *testing.T) {
	window := gastownShardWindow(t, gastownMakeTestDryRun(t))
	for _, want := range []string{"GC_FAST_UNIT=1", "GO_TEST_COUNT=1"} {
		if !strings.Contains(window, want) {
			t.Fatalf("make test examples/gastown shards must carry %s:\n%s", want, window)
		}
	}
}

// TestMakeTestSharesOneTimeoutBudgetAcrossSweepAndGastownShards applies the
// ga-9au lesson to `make test`: the sweep's -timeout and the shards'
// GO_TEST_TIMEOUT are one budget written twice, so a retune that moves only
// one of them fails here instead of drifting silently.
func TestMakeTestSharesOneTimeoutBudgetAcrossSweepAndGastownShards(t *testing.T) {
	recipe := gastownMakeTestDryRun(t)
	sweep := goTestFlagValue(t, gastownSweepLineFrom(t, recipe), "timeout")
	match := regexp.MustCompile(`GO_TEST_TIMEOUT=([^\s\\]+)`).FindStringSubmatch(gastownShardWindow(t, recipe))
	if match == nil {
		t.Fatalf("make test examples/gastown shards pass no GO_TEST_TIMEOUT:\n%s", recipe)
	}
	if sweep == "" {
		t.Fatalf("make test sweep passes no -timeout:\n%s", recipe)
	}
	if sweep != match[1] {
		t.Fatalf("make test sweep -timeout = %q but examples/gastown shards use GO_TEST_TIMEOUT=%q; the target must share one budget",
			sweep, match[1])
	}
}

// gastownMakeTestDryRun returns the `make -n test` recipe with ambient
// shard/timeout overrides scrubbed, so the assertions read the target's own
// shape rather than the caller's environment.
func gastownMakeTestDryRun(t *testing.T) string {
	t.Helper()
	cmd := makeCommand("-n", "test")
	cmd.Dir = repoRoot(t)
	env := os.Environ()
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, "GASTOWN_EXAMPLE_TOTAL=") ||
			strings.HasPrefix(entry, "GO_TEST_TIMEOUT=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	cmd.Env = filtered
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n test failed: %v\n%s", err, out)
	}
	return string(out)
}

// gastownSweepLine returns the dry-run recipe line that runs the unsharded
// package sweep through go-test-observable.
func gastownSweepLine(t *testing.T) string {
	t.Helper()
	return gastownSweepLineFrom(t, gastownMakeTestDryRun(t))
}

func gastownSweepLineFrom(t *testing.T, recipe string) string {
	t.Helper()
	for _, line := range strings.Split(recipe, "\n") {
		if strings.Contains(line, "go-test-observable test --") {
			return line
		}
	}
	t.Fatalf("make test no longer sweeps packages through go-test-observable:\n%s", recipe)
	return ""
}

// gastownShardWindow returns the dry-run recipe text of the examples/gastown
// shard loop, from its `for s in` to the shard invocation, so shard-env
// assertions cannot accidentally match the sweep line's identical assignments
// — or a second shard loop's, once cmd/gc sharding (gcy-sdm) lands alongside.
func gastownShardWindow(t *testing.T, recipe string) string {
	t.Helper()
	const invocation = "test-go-test-shard ./examples/gastown"
	end := strings.Index(recipe, invocation)
	if end < 0 {
		t.Fatalf("make test has no examples/gastown shard loop:\n%s", recipe)
	}
	start := strings.LastIndex(recipe[:end], "for s in")
	if start < 0 {
		t.Fatalf("make test examples/gastown shard invocation is not inside a shard loop:\n%s", recipe)
	}
	return recipe[start : end+len(invocation)]
}

// gastownShardTotal parses the shard loop bound (`seq 1 N`) from the
// examples/gastown shard window.
func gastownShardTotal(t *testing.T, recipe string) int {
	t.Helper()
	match := regexp.MustCompile(`seq 1 (\d+)`).FindStringSubmatch(gastownShardWindow(t, recipe))
	if match == nil {
		t.Fatalf("make test examples/gastown shard loop has no `seq 1 N` bound:\n%s", recipe)
	}
	total, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("shard bound %q is not a number: %v", match[1], err)
	}
	return total
}
