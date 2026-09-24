package sessionlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/pathutil"
)

// This file holds transcript discovery for the muse provider family (the
// Meta `muse` CLI). The CLI stores one directory per session under
// ~/.local/share/muse/sessions/YYYY/MM/DD/<session-uuid>/ holding
// session.jsonl, an append-only JSONL event log. The directory name IS the
// session id; per-invocation token usage lives in runtime.session
// run/model_completed events (see muse_usage.go).
//
// Attribution is by the workspace_root the session log carries in its head
// records, confirmed with a bounded head read — never by newest-mtime alone.
// gc never passes its session key to the muse CLI (the provider config has no
// session_id_flag), so the common case is the keyless window lookup, mirroring
// the keyless-codex fallback: strictly bounded day directories, an mtime
// filter, and an ambiguity refusal rather than a newest-wins guess.

// museSessionFileName is the transcript filename inside a muse session dir.
const museSessionFileName = "session.jsonl"

// museWorkspaceProbeBytes bounds the head read that confirms a candidate
// session's workspace. workspace_root is written by the session-open records
// at the head of the log.
const museWorkspaceProbeBytes = 64 * 1024

// DefaultMuseSearchPaths returns the default search paths for muse session
// files (~/.local/share/muse/sessions).
func DefaultMuseSearchPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".local", "share", "muse", "sessions")}
}

func mergeMuseSearchPaths(extraPaths []string) []string {
	return mergePaths(DefaultMuseSearchPaths(), extraPaths)
}

// FindMuseSessionFileNear resolves a muse transcript with a strictly bounded
// lookup: it opens only the local-date day directories (YYYY/MM/DD)
// intersecting [anchor-1m, anchor+window] under each merged muse root,
// keeps session dirs whose session.jsonl mtime falls in that range and whose
// head-carried workspace_root matches workDir, and returns the path only when
// exactly one physically distinct session matches. Multiple matches are
// refused as ambiguous (mirroring Manager.TranscriptPath's same-workdir
// guard); zero matches, an empty workDir, a zero anchor, or a non-positive
// window return "". Unlike a full-tree walk it never scales with total muse
// history, so it is safe to call inside a prompt operation.
//
// mtime (not the directory name) is the time signal because muse session
// dirs are UUIDs carrying no timestamp. Both ends share the local clock —
// file mtimes and the anchor are written by the same machine — so no
// timezone tolerance is needed.
func FindMuseSessionFileNear(searchPaths []string, workDir string, anchor time.Time, window time.Duration) string {
	path, _ := FindMuseSessionFileNearScan(searchPaths, workDir, anchor, window)
	return path
}

// FindMuseSessionFileNearScan is FindMuseSessionFileNear with a clean-scan
// signal, mirroring FindCodexSessionFileNearScan: scanClean is false when ANY
// os.ReadDir or workspace-probe open during the scan failed with a
// non-ENOENT IO fault, so a caller that must decide whether its result is
// definitive can tell a genuine zero/ambiguous match (scanClean true —
// retrying cannot change it) from a transient scan fault (scanClean false).
// An ambiguity refusal returns scanClean true regardless of unrelated IO
// noise; bad inputs return ("", true).
func FindMuseSessionFileNearScan(searchPaths []string, workDir string, anchor time.Time, window time.Duration) (string, bool) {
	if strings.TrimSpace(workDir) == "" || anchor.IsZero() || window <= 0 {
		return "", true
	}
	return FindMuseSessionFileInRangeScan(searchPaths, workDir, anchor.Add(-time.Minute), anchor.Add(window))
}

// FindMuseSessionFileInRange resolves a muse transcript whose session.jsonl
// mtime falls in [start, end] and whose workspace matches workDir, refusing
// ambiguity. It is the range core behind FindMuseSessionFileNearScan; the
// sweep uses it directly with the awake interval so a long-awake session's
// trailing writes (mtime near the interval end) still match, where an
// anchor-at-start fixed window would age out.
func FindMuseSessionFileInRange(searchPaths []string, workDir string, start, end time.Time) string {
	path, _ := FindMuseSessionFileInRangeScan(searchPaths, workDir, start, end)
	return path
}

// FindMuseSessionFileInRangeScan is FindMuseSessionFileInRange with a
// clean-scan signal. A zero start or an end before start returns ("", true).
func FindMuseSessionFileInRangeScan(searchPaths []string, workDir string, start, end time.Time) (string, bool) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" || start.IsZero() || end.Before(start) {
		return "", true
	}
	var matches []string
	seen := make(map[string]bool)
	dirty := false
	for _, root := range mergeMuseSearchPaths(searchPaths) {
		collectMuseSessionsInRange(root, workDir, start, end, seen, &matches, &dirty)
		if len(matches) > 1 {
			return "", true // ambiguous: a definitive refusal, independent of scan noise
		}
	}
	if len(matches) == 1 {
		return matches[0], !dirty
	}
	return "", !dirty
}

// collectMuseSessionsInRange appends in-range workspace-matching muse
// transcripts under one root to matches, deduplicated by physical identity
// (seen is shared across roots by the caller).
func collectMuseSessionsInRange(root, workDir string, start, end time.Time, seen map[string]bool, matches *[]string, dirty *bool) {
	firstDay := startOfLocalDay(start.In(time.Local)).AddDate(0, 0, -1)
	lastDay := startOfLocalDay(end.In(time.Local)).AddDate(0, 0, 1)
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		dayDir := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
		if scanMuseSessionDay(dayDir, workDir, start, end, seen, matches, dirty) {
			return // ambiguity reached: further scanning cannot change the refusal
		}
	}
}

// scanMuseSessionDay stats each session.jsonl under dayDir, keeps the ones
// whose mtime falls in [start, end] with a matching workspace, and reports
// whether ambiguity was reached. A non-ENOENT readdir fault marks the scan
// dirty; ENOENT (a day with no sessions) is free.
func scanMuseSessionDay(dayDir, workDir string, start, end time.Time, seen map[string]bool, matches *[]string, dirty *bool) bool {
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		if !os.IsNotExist(err) {
			*dirty = true
		}
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dayDir, e.Name(), museSessionFileName)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		mt := info.ModTime()
		if mt.Before(start) || mt.After(end) {
			continue
		}
		match, clean := museSessionWorkspaceMatchesScan(path, workDir)
		if !clean {
			*dirty = true
			continue
		}
		if !match {
			continue
		}
		key := path
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			key = resolved
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		*matches = append(*matches, path)
		if len(*matches) > 1 {
			return true
		}
	}
	return false
}

// museSessionWorkspaceMatchesScan reports whether the head of the muse
// session log at path carries workspace_root == workDir, with a clean-scan
// signal: clean is false only when opening the file failed with a non-ENOENT
// IO fault. A missing/unreadable-as-JSON head, a head with no workspace_root,
// or a file that vanished between readdir and open are all clean non-matches.
func museSessionWorkspaceMatchesScan(path, workDir string) (match bool, clean bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, os.IsNotExist(err)
	}
	defer f.Close() //nolint:errcheck // read-only
	r := bufio.NewReader(f)
	budget := museWorkspaceProbeBytes
	for budget > 0 {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if ws := museWorkspaceRootInLine(line); ws != "" && pathutil.SamePath(ws, workDir) {
				return true, true
			}
			budget -= len(line)
		}
		if err != nil {
			break
		}
	}
	return false, true
}

// museWorkspaceRootInLine extracts the workspace_root string from one
// session.jsonl line at any nesting depth, or "" when absent. The probe only
// needs the value, so a minimal recursive walk beats full decoding.
func museWorkspaceRootInLine(line []byte) string {
	var v any
	if err := json.Unmarshal(line, &v); err != nil {
		return ""
	}
	return findWorkspaceRoot(v)
}

func findWorkspaceRoot(v any) string {
	switch t := v.(type) {
	case map[string]any:
		if ws, ok := t["workspace_root"]; ok {
			if s, ok := ws.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		for _, child := range t {
			if s := findWorkspaceRoot(child); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range t {
			if s := findWorkspaceRoot(child); s != "" {
				return s
			}
		}
	}
	return ""
}

// FindMuseSessionFileByID resolves a muse transcript by provider session ID
// over the day directories intersecting [notBefore, notAfter] (each padded by
// a day for local-time skew). The session dir name IS the id, so the lookup
// is exact; the candidate's workspace is still confirmed so a foreign session
// reusing the uuid namespace can never attribute. Zero notBefore/notAfter are
// treated as unbounded on that end; a reversed range returns "".
func FindMuseSessionFileByID(searchPaths []string, workDir, sessionID string, notBefore, notAfter time.Time) string {
	workDir = strings.TrimSpace(workDir)
	sessionID = strings.TrimSpace(sessionID)
	if workDir == "" || sessionID == "" || strings.Contains(sessionID, "..") || strings.ContainsAny(sessionID, `/\`) {
		return ""
	}
	if !notBefore.IsZero() && !notAfter.IsZero() && notAfter.Before(notBefore) {
		return ""
	}
	// The range walk enumerates calendar days, so it needs BOTH bounds —
	// with either missing the walk falls back to the unbounded newest-first
	// scan rather than iterating from the zero time.
	bounded := !notBefore.IsZero() && !notAfter.IsZero()
	firstDay, lastDay := museDayRange(notBefore, notAfter)
	for _, root := range mergeMuseSearchPaths(searchPaths) {
		var path string
		if bounded {
			path = findMuseSessionByIDInRange(root, workDir, sessionID, firstDay, lastDay)
		} else {
			path = findMuseSessionByIDUnbounded(root, workDir, sessionID)
		}
		if path != "" {
			return path
		}
	}
	return ""
}

// museDayRange returns the padded inclusive day range covering
// [notBefore, notAfter]. Callers only invoke it with both bounds set (see
// FindMuseSessionFileByID); the padding absorbs local-time skew at the
// edges.
func museDayRange(notBefore, notAfter time.Time) (firstDay, lastDay time.Time) {
	firstDay = startOfLocalDay(notBefore.In(time.Local)).AddDate(0, 0, -1)
	return firstDay, startOfLocalDay(notAfter.In(time.Local)).AddDate(0, 0, 1)
}

func findMuseSessionByIDInRange(root, workDir, sessionID string, firstDay, lastDay time.Time) string {
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		path := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"), sessionID, museSessionFileName)
		if match, _ := museSessionWorkspaceMatchesScan(path, workDir); match {
			return path
		}
	}
	return ""
}

// findMuseSessionByIDUnbounded walks the whole date tree newest-first for the
// session dir. Used only when no window bounds the lookup.
func findMuseSessionByIDUnbounded(root, workDir, sessionID string) string {
	yearDirs := splitMuseSessionRoots(root)
	sort.Sort(sort.Reverse(sort.StringSlice(yearDirs)))
	for _, year := range yearDirs {
		yearDir := filepath.Join(root, year)
		for _, month := range listDirsReverse(yearDir) {
			monthDir := filepath.Join(yearDir, month)
			for _, day := range listDirsReverse(monthDir) {
				path := filepath.Join(monthDir, day, sessionID, museSessionFileName)
				if match, _ := museSessionWorkspaceMatchesScan(path, workDir); match {
					return path
				}
			}
		}
	}
	return ""
}

// splitMuseSessionRoots returns directory names under a muse sessions root.
func splitMuseSessionRoots(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	yearDirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		yearDirs = append(yearDirs, e.Name())
	}
	return yearDirs
}

// FindMuseSessionFileByIDNoWindow resolves a muse transcript by provider
// session ID without a creation/wake window, walking the date tree
// newest-first and confirming the workspace. A session id containing path
// separators or ".." is rejected.
func FindMuseSessionFileByIDNoWindow(searchPaths []string, workDir, sessionID string) string {
	workDir = strings.TrimSpace(workDir)
	sessionID = strings.TrimSpace(sessionID)
	if workDir == "" || sessionID == "" || strings.Contains(sessionID, "..") || strings.ContainsAny(sessionID, `/\`) {
		return ""
	}
	for _, root := range mergeMuseSearchPaths(searchPaths) {
		if path := findMuseSessionByIDUnbounded(root, workDir, sessionID); path != "" {
			return path
		}
	}
	return ""
}
