//go:build !windows

package packman

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/processgroup/processgrouptest"
)

// TestDefaultRunNetworkGitKillsDescendants pins that the deadline reaches git's
// children, not just git.
//
// The first version of this bound relied on cmd.WaitDelay, whose contract is to
// close the parent's ends of the I/O pipes and kill the command's own process.
// It does not signal descendants. So the call returned on time while
// git-remote-http and index-pack stayed alive — still writing into the cache
// directory whose write lock this call had just released. The next process to
// take that lock can RemoveAll a tree a live orphan is repopulating, which is
// the same corruption the lock exists to prevent, arriving by a different door.
//
// Returning on time is therefore not the property under test. The assertion is
// that the writing stopped, measured directly: the shim's child appends to a
// heartbeat file for as long as it lives, so a file that stops growing is the
// descendant's death and a file that keeps growing is the leak. That is also
// why this asserts on bytes rather than on the pid — a killed orphan is a
// zombie until init reaps it, so pid liveness is ambiguous for exactly as long
// as it takes to be misleading.
func TestDefaultRunNetworkGitKillsDescendants(t *testing.T) {
	var wedged wedgedRemote

	// The deadline here is scheduling headroom for the shim, not the bound
	// under test: a full second for /bin/sh to exec and append one byte.
	// TestDefaultRunNetworkGitIsBounded pins that the bound tracks a tight
	// 300ms deadline; this test pins that the kill reaches descendants, a
	// property independent of the deadline's magnitude. A tight deadline
	// here only narrows the window the shim has to demonstrably start
	// before the kill lands (gcy-8xl).
	restore := networkGitTimeout
	networkGitTimeout = time.Second
	t.Cleanup(func() { networkGitTimeout = restore })
	restoreWait := networkGitWaitDelay
	networkGitWaitDelay = time.Second
	t.Cleanup(func() { networkGitWaitDelay = restoreWait })

	// The deadline races the shim's first write: on a loaded host the
	// group kill can land before /bin/sh is scheduled to append its first
	// heartbeat byte, and then there is no file for WaitForFileSize to find
	// (gcy-8xl). That is a lost scheduling race, not a leaked descendant, so
	// an attempt whose shim never demonstrably started is retried with a fresh
	// shim directory rather than failed — a previous attempt's already-signaled
	// group cannot append to the file under assertion.
	//
	// The retry does not weaken the test. The stability assertion below runs
	// exactly once, on an attempt whose heartbeat landed before the kill, so a
	// pass still means "heartbeats flowed, then stopped". A genuinely broken
	// group kill fails every attempt the same way: the final attempt's file
	// keeps growing past the stability deadline. And requiring the timeout
	// sentinel on every attempt is strictly stronger than before: previously a
	// run where the bound never fired but a heartbeat existed (shim dying on
	// its own) would pass vacuously.
	const maxAttempts = 3
	for attempt := 1; ; attempt++ {
		wedged = wedgedGit(t)
		_, err := defaultRunNetworkGit("", wedged.URL, "", "clone", "--quiet", wedged.URL, t.TempDir()+"/dest")
		if err == nil {
			t.Fatal("cloning a wedged remote succeeded, want a timeout error")
		}
		if !errors.Is(err, errNetworkGitTimeout) {
			t.Fatalf("cloning a wedged remote failed without the deadline firing: %v", err)
		}
		if info, statErr := os.Stat(wedged.HeartbeatPath); statErr == nil && info.Size() > 0 {
			break
		}
		t.Logf("attempt %d: no heartbeat yet, retrying", attempt)
		if attempt >= maxAttempts {
			t.Fatalf("shim wrote no heartbeat in %d attempts (%v); the host cannot start /bin/sh within the %s test deadline", maxAttempts, err, networkGitTimeout)
		}
	}

	size := processgrouptest.WaitForFileSize(t, wedged.HeartbeatPath)
	// The window has to be a comfortable multiple of the shim's 50ms write
	// cadence, because the failure mode of getting it wrong is silent: a live
	// orphan that happens to be descheduled for one window reads as a dead one
	// and the test goes green having stopped guarding. 300ms is 6x, and is what
	// the other users of this helper pair with the same cadence.
	processgrouptest.AssertFileSizeStable(t, wedged.HeartbeatPath, size, 300*time.Millisecond)
}
