package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/nudgepoller"
	"github.com/gastownhall/gascity/internal/pidutil"
)

// Stop sidecars before storage: their native-store reconnect can otherwise
// restart managed Dolt after an intentional shutdown. Agents must already be
// stopped by the caller so they cannot launch replacement sidecars.
var stopCityNudgePollers = stopCityNudgePollersByPID

func stopCityNudgePollersByPID(cityPath string) error {
	dir := citylayout.RuntimePath(cityPath, "nudges", "pollers")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	match := nudgepoller.CityCmdlineMatcher(cityPath)
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			continue
		}
		var startTime string
		// Recheck argv on every probe, including immediately before escalation.
		// Cmdline uses native macOS argv access, not a flattened ps substring.
		matches := func() (bool, error) {
			if !pidutil.Alive(pid) {
				return false, nil
			}
			argv, err := pidutil.Cmdline(pid)
			if err != nil {
				if !pidutil.Alive(pid) {
					return false, nil
				}
				return false, fmt.Errorf("read poller pid %d identity: %w", pid, err)
			}
			if !match(argv) {
				return false, nil
			}
			currentStart, err := pidutil.StartTime(pid)
			if err != nil {
				if !pidutil.Alive(pid) {
					return false, nil
				}
				return false, fmt.Errorf("read poller pid %d start time: %w", pid, err)
			}
			if startTime == "" {
				startTime = currentStart
			}
			return startTime == currentStart, nil
		}
		if err := stopNudgePoller(pid, matches, func(sig syscall.Signal) error { return syscall.Kill(pid, sig) }); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func stopNudgePoller(pid int, matches func() (bool, error), signal func(syscall.Signal) error) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		live, err := matches()
		if err != nil || !live {
			return err
		}
		if err := signal(sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop nudge poller %d: %w", pid, err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			live, err := matches()
			if err != nil || !live {
				return err
			}
			if !time.Now().Before(deadline) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	return fmt.Errorf("nudge poller %d survived shutdown; leaving storage running", pid)
}
