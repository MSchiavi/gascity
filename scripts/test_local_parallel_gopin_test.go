package scripts_test

import (
	"strings"
	"testing"
)

// TestLocalParallelPinsGoBindirAcrossLoginShellHop guards gcy-a8m: every
// fan-out job runs under `bash -lc`, and on macOS the login shell rebuilds
// PATH via path_helper from /etc/paths and /etc/paths.d — which puts a fossil
// go (/etc/paths.d/go -> /usr/local/go/bin, go1.17) ahead of the real
// toolchain no matter what PATH the sanitized `env -i` block carries. A PATH
// prefix applied before the hop does not survive the rebuild, so the runner
// must resolve the go bindir once from its own known-good environment and
// re-prepend it *inside* the login shell, after profiles have run.
func TestLocalParallelPinsGoBindirAcrossLoginShellHop(t *testing.T) {
	script := localParallelScript(t)

	// The bindir is resolved from the outer environment, before any
	// login-shell hop can reorder PATH underneath the lookup.
	if !strings.Contains(script, "command -v go") {
		t.Fatalf("scripts/test-local-parallel no longer resolves the go binary from the outer environment:\n%s", script)
	}
	if !strings.Contains(script, "TEST_LOCAL_GO_BINDIR") {
		t.Fatalf("scripts/test-local-parallel no longer exports the pinned go bindir to its workers:\n%s", script)
	}

	// The pin is applied inside the login shell: the $PATH reference must
	// reach the login shell escaped (literal `\$PATH` in the script source)
	// so it expands to the login shell's own post-profile PATH — nvm and
	// friends keep working — with only the pinned go bindir moved ahead.
	// An unescaped $PATH would bake the worker's PATH into the command and
	// discard everything the login profiles provide.
	const want = `export PATH=\"${TEST_LOCAL_GO_BINDIR}:\$PATH\"`
	if !strings.Contains(script, want) {
		t.Fatalf("scripts/test-local-parallel no longer re-prepends the pinned go bindir inside the login shell (want %q):\n%s", want, script)
	}
	if strings.Contains(script, `bash -lc "$command"`) {
		t.Fatalf("scripts/test-local-parallel runs jobspecs under a bare `bash -lc \"$command\"`, so shards resolve go from login PATH order again:\n%s", script)
	}
}
