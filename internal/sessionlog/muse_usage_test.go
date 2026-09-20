package sessionlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeMuseUsageLines writes raw JSONL lines to path, creating parents.
func writeMuseUsageLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// museModelCompletedLine builds a runtime.session run/model_completed line
// mirroring the real muse session.jsonl shape.
func museModelCompletedLine(seq int, recordID, runRecordID string, recordedAt int64, model string, input, output, cached, cacheWrite, cacheRead, reasoning int) string {
	return fmt.Sprintf(`{"schema_version":1,"id":%q,"stream":{"kind":"session","id":"01a0bde4-25aa-7f50-8a21-1c2bd19389c7"},"sequence":%d,"recorded_at":%d,"record_type":"event","durability":"durable","causation_id":null,"payload_type":"runtime.session","payload_schema_version":1,"payload":{"kind":"run","run_id":"956851f8-a3fd-420d-96d7-4fec84578466","event":{"kind":"model_completed","usage":{"input_tokens":%d,"output_tokens":%d,"cached_tokens":%d,"cache_write_tokens":%d,"cache_read_tokens":%d,"reasoning_tokens":%d},"duration_ms":2858,"finish_reason":"tool_calls","model":%q},"source_run_record_id":%q,"source_run_record_sequence":%d}}`,
		recordID, seq, recordedAt, input, output, cached, cacheWrite, cacheRead, reasoning, model, runRecordID, seq)
}

// museGoalAttributionLine builds a runtime.session run/goal_usage_attribution
// line, which must NOT be extracted (it is goal accounting, not per-invocation
// model usage).
func museGoalAttributionLine(seq int) string {
	return fmt.Sprintf(`{"schema_version":1,"id":"goal-%d","stream":{"kind":"session","id":"01a0bde4-25aa-7f50-8a21-1c2bd19389c7"},"sequence":%d,"recorded_at":1789892240839929,"record_type":"event","durability":"durable","causation_id":null,"payload_type":"runtime.session","payload_schema_version":1,"payload":{"kind":"run","run_id":"956851f8-a3fd-420d-96d7-4fec84578466","event":{"kind":"goal_usage_attribution","record":{"schema_version":1,"usage_id":"usage-%d","usage_family":"provider","quantity":{"unit":"tokens","reported":true,"input_tokens":100,"output_tokens":10,"cached_tokens":50,"reasoning_tokens":1,"main_llm_steps":1}}},"source_run_record_id":"goal-record-%d","source_run_record_sequence":%d}}`,
		seq, seq, seq, seq, seq)
}

func TestExtractMuseTailUsageParsesModelCompleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeMuseUsageLines(t, path, []string{
		museModelCompletedLine(22, "rec-1", "run-rec-1", 1789892240839929, "muse-spark-1.3-contributor", 42653, 111, 17009, 0, 17009, 12),
		museGoalAttributionLine(23),
		museModelCompletedLine(24, "rec-2", "run-rec-2", 1789892300000000, "muse-spark-1.3-contributor", 44049, 117, 43889, 0, 43889, 5),
	})
	usages, err := ExtractMuseTailUsage(path)
	if err != nil {
		t.Fatalf("ExtractMuseTailUsage: %v", err)
	}
	if len(usages) != 2 {
		t.Fatalf("got %d usages, want 2 (goal_usage_attribution must be skipped)", len(usages))
	}
	first := usages[0]
	if first.Model != "muse-spark-1.3-contributor" {
		t.Errorf("Model = %q, want muse-spark-1.3-contributor", first.Model)
	}
	// Muse input_tokens includes cached tokens (OpenAI-style gauge), so the
	// non-cached prompt count is input minus cached.
	if first.InputTokens != 42653-17009 {
		t.Errorf("InputTokens = %d, want %d", first.InputTokens, 42653-17009)
	}
	if first.OutputTokens != 111 {
		t.Errorf("OutputTokens = %d, want 111", first.OutputTokens)
	}
	if first.CacheReadTokens != 17009 {
		t.Errorf("CacheReadTokens = %d, want 17009", first.CacheReadTokens)
	}
	if first.CacheCreationTokens != 0 {
		t.Errorf("CacheCreationTokens = %d, want 0", first.CacheCreationTokens)
	}
	if first.ReasoningTokens != 12 {
		t.Errorf("ReasoningTokens = %d, want 12", first.ReasoningTokens)
	}
	if first.MessageID != "run-rec-1" {
		t.Errorf("MessageID = %q, want run-rec-1", first.MessageID)
	}
	if first.EntryUUID != "run-rec-1" {
		t.Errorf("EntryUUID = %q, want run-rec-1", first.EntryUUID)
	}
	wantTS := time.Unix(1789892240, 839929000).UTC()
	if !first.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v", first.Timestamp, wantTS)
	}
}

func TestExtractMuseTailUsageSkipsZeroUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeMuseUsageLines(t, path, []string{
		museModelCompletedLine(22, "rec-1", "run-rec-1", 1789892240839929, "muse-spark-1.3-contributor", 0, 0, 0, 0, 0, 0),
		museModelCompletedLine(23, "rec-2", "run-rec-2", 1789892300000000, "muse-spark-1.3-contributor", 100, 10, 0, 0, 0, 0),
	})
	usages, err := ExtractMuseTailUsage(path)
	if err != nil {
		t.Fatalf("ExtractMuseTailUsage: %v", err)
	}
	if len(usages) != 1 || usages[0].MessageID != "run-rec-2" {
		t.Fatalf("got %+v, want only the non-zero entry", usages)
	}
}

func TestExtractMuseTailUsageToleratesMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeMuseUsageLines(t, path, []string{
		`{"schema_version":1,"id":"broken`,
		`{"record_type":"status","payload_type":"runtime.session.route_facts"}`,
		museModelCompletedLine(24, "rec-2", "run-rec-2", 1789892300000000, "muse-spark-1.3", 100, 10, 0, 0, 0, 0),
	})
	usages, err := ExtractMuseTailUsage(path)
	if err != nil {
		t.Fatalf("ExtractMuseTailUsage: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("got %d usages, want 1", len(usages))
	}
}

func TestExtractMuseTailUsageFallsBackToRecordID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	line := museModelCompletedLine(24, "rec-9", "", 1789892300000000, "muse-spark-1.3", 100, 10, 0, 0, 0, 0)
	writeMuseUsageLines(t, path, []string{line})
	usages, err := ExtractMuseTailUsage(path)
	if err != nil {
		t.Fatalf("ExtractMuseTailUsage: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("got %d usages, want 1", len(usages))
	}
	if usages[0].MessageID != "rec-9" {
		t.Errorf("MessageID = %q, want rec-9 fallback to top-level id", usages[0].MessageID)
	}
}

func TestExtractMuseTailUsageClampsCachedAboveInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeMuseUsageLines(t, path, []string{
		museModelCompletedLine(24, "rec-2", "run-rec-2", 1789892300000000, "muse-spark-1.3", 100, 10, 250, 0, 250, 0),
	})
	usages, err := ExtractMuseTailUsage(path)
	if err != nil {
		t.Fatalf("ExtractMuseTailUsage: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("got %d usages, want 1", len(usages))
	}
	if usages[0].InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (clamped, never negative)", usages[0].InputTokens)
	}
	if usages[0].CacheReadTokens != 250 {
		t.Errorf("CacheReadTokens = %d, want 250", usages[0].CacheReadTokens)
	}
}

func TestExtractMuseTailUsageFromSearchPathsRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "session.jsonl")
	writeMuseUsageLines(t, path, []string{
		museModelCompletedLine(24, "rec-2", "run-rec-2", 1789892300000000, "muse-spark-1.3", 100, 10, 0, 0, 0, 0),
	})
	if _, err := ExtractMuseTailUsageFromSearchPaths([]string{root}, path); err == nil {
		t.Fatal("expected containment error for path outside search roots")
	}
}
