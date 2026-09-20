package sessionlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jsonQuote JSON-quotes s for embedding in a fixture line.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// writeMuseSessionAt creates root/YYYY/MM/DD/<sessionID>/session.jsonl (day
// from ts in local time) whose head carries workspace_root=workDir, and sets
// the file mtime to ts. It returns the session.jsonl path.
func writeMuseSessionAt(t *testing.T, root string, ts time.Time, sessionID, workDir string) string {
	t.Helper()
	dayDir := filepath.Join(root, ts.Format("2006"), ts.Format("01"), ts.Format("02"), sessionID)
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dayDir, "session.jsonl")
	head := `{"record_type":"event","payload_type":"runtime.session.metadata","payload":{"workspace_root":` + jsonQuote(workDir) + "}}\n"
	body := museModelCompletedLine(23, "rec-1", "run-rec-1", ts.UnixMicro(), "muse-spark-1.3", 100, 10, 0, 0, 0, 0) + "\n"
	if err := os.WriteFile(path, []byte(head+body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return path
}

func TestFindMuseSessionFileNearFindsInWindowMatch(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	anchor := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	window := 10 * time.Minute
	want := writeMuseSessionAt(t, root, anchor.Add(time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir)
	if got := FindMuseSessionFileNear([]string{root}, workDir, anchor, window); got != want {
		t.Fatalf("FindMuseSessionFileNear = %q, want %q", got, want)
	}
}

func TestFindMuseSessionFileNearRefusesOutOfWindow(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	anchor := time.Now().Add(-time.Hour).Truncate(time.Second)
	window := 10 * time.Minute
	writeMuseSessionAt(t, root, anchor.Add(-30*time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir)
	writeMuseSessionAt(t, root, anchor.Add(window+time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c8", workDir)
	if got := FindMuseSessionFileNear([]string{root}, workDir, anchor, window); got != "" {
		t.Fatalf("FindMuseSessionFileNear = %q, want empty (both outside window)", got)
	}
}

func TestFindMuseSessionFileNearRefusesWorkspaceMismatch(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	anchor := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	window := 10 * time.Minute
	writeMuseSessionAt(t, root, anchor.Add(time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", filepath.Join(root, "other"))
	if got := FindMuseSessionFileNear([]string{root}, workDir, anchor, window); got != "" {
		t.Fatalf("FindMuseSessionFileNear = %q, want empty (workspace mismatch)", got)
	}
}

func TestFindMuseSessionFileNearRefusesAmbiguity(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	anchor := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	window := 10 * time.Minute
	writeMuseSessionAt(t, root, anchor.Add(time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir)
	writeMuseSessionAt(t, root, anchor.Add(2*time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c8", workDir)
	if got := FindMuseSessionFileNear([]string{root}, workDir, anchor, window); got != "" {
		t.Fatalf("FindMuseSessionFileNear = %q, want empty (ambiguous)", got)
	}
}

func TestFindMuseSessionFileNearBadInputsAreCleanNoOps(t *testing.T) {
	root := t.TempDir()
	anchor := time.Now().Add(-time.Minute)
	window := 10 * time.Minute
	if path, clean := FindMuseSessionFileNearScan([]string{root}, "", anchor, window); path != "" || !clean {
		t.Fatalf("empty workDir: got (%q, %t), want (\"\", true)", path, clean)
	}
	if path, clean := FindMuseSessionFileNearScan([]string{root}, "/work", time.Time{}, window); path != "" || !clean {
		t.Fatalf("zero anchor: got (%q, %t), want (\"\", true)", path, clean)
	}
	if path, clean := FindMuseSessionFileNearScan([]string{root}, "/work", anchor, 0); path != "" || !clean {
		t.Fatalf("zero window: got (%q, %t), want (\"\", true)", path, clean)
	}
}

func TestFindMuseSessionFileNearScanDirtyDayDir(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	anchor := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	window := 10 * time.Minute
	writeMuseSessionAt(t, root, anchor.Add(time.Minute), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir)
	dayDir := filepath.Join(root, anchor.Format("2006"), anchor.Format("01"), anchor.Format("02"))
	if err := os.Chmod(dayDir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dayDir, 0o755) })
	if _, clean := FindMuseSessionFileNearScan([]string{root}, workDir, anchor, window); clean {
		t.Fatal("unreadable day dir must report scanClean=false")
	}
}

func TestFindMuseSessionFileByID(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	now := time.Now().Truncate(time.Second)
	want := writeMuseSessionAt(t, root, now.Add(-time.Hour), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir)
	got := FindMuseSessionFileByID([]string{root}, workDir, "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", now.Add(-2*time.Hour), now)
	if got != want {
		t.Fatalf("FindMuseSessionFileByID = %q, want %q", got, want)
	}
	// The window bounds the day-directory scan (day granularity, like the
	// codex ByID lookup), so a session on an out-of-range day is a miss.
	writeMuseSessionAt(t, root, now.Add(-72*time.Hour), "01a0bde4-25aa-7f50-8a21-1c2bd19389c9", workDir)
	if got := FindMuseSessionFileByID([]string{root}, workDir, "01a0bde4-25aa-7f50-8a21-1c2bd19389c9", now.Add(-2*time.Hour), now); got != "" {
		t.Fatalf("FindMuseSessionFileByID outside window = %q, want empty", got)
	}
	if got := FindMuseSessionFileByID([]string{root}, workDir, "../escape", now.Add(-2*time.Hour), now); got != "" {
		t.Fatalf("FindMuseSessionFileByID path traversal = %q, want empty", got)
	}
	// A missing bound must fall back to the unbounded walk, never enumerate
	// days from the zero time (which would hang the caller).
	if got := FindMuseSessionFileByID([]string{root}, workDir, "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", time.Time{}, now); got != want {
		t.Fatalf("FindMuseSessionFileByID unbounded start = %q, want %q", got, want)
	}
}

func TestFindMuseSessionFileByIDNoWindow(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	want := writeMuseSessionAt(t, root, time.Now().Add(-48*time.Hour), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir)
	if got := FindMuseSessionFileByIDNoWindow([]string{root}, workDir, "01a0bde4-25aa-7f50-8a21-1c2bd19389c7"); got != want {
		t.Fatalf("FindMuseSessionFileByIDNoWindow = %q, want %q", got, want)
	}
	if got := FindMuseSessionFileByIDNoWindow([]string{root}, filepath.Join(root, "other"), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7"); got != "" {
		t.Fatalf("FindMuseSessionFileByIDNoWindow workspace mismatch = %q, want empty", got)
	}
}

func TestProviderFamilyMuse(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{provider: "muse", want: "muse"},
		{provider: "Muse", want: "muse"},
		{provider: "muse/tmux-cli", want: "muse"},
		{provider: "wrapped/muse", want: "muse"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			if got := ProviderFamily(tt.provider); got != tt.want {
				t.Fatalf("ProviderFamily(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestDefaultMuseSearchPaths(t *testing.T) {
	roots := DefaultMuseSearchPaths()
	if len(roots) == 0 {
		t.Fatal("DefaultMuseSearchPaths is empty")
	}
	last := roots[0]
	if !strings.HasSuffix(filepath.Clean(last), filepath.Join(".local", "share", "muse", "sessions")) {
		t.Fatalf("DefaultMuseSearchPaths[0] = %q, want ~/.local/share/muse/sessions", last)
	}
}
