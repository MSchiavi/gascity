package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/usage"
)

// ProfileMuseTmuxCLI is the test profile for muse-provider sessions. The muse
// provider is user-configured (city.toml [providers.muse]), not a builtin, so
// no canonical builtin profile exists; the literal names the family the same
// way canonical "family/tmux-cli" profiles do.
const ProfileMuseTmuxCLI Profile = "muse/tmux-cli"

// museWorkerModelCompleted builds a runtime.session run/model_completed map
// mirroring the real muse session.jsonl shape.
func museWorkerModelCompleted(seq int, recordID, runRecordID string, recordedAtMicros int64, model string, input, output, cached, cacheWrite, cacheRead, reasoning int) map[string]any {
	return map[string]any{
		"schema_version": 1, "id": recordID,
		"stream":   map[string]any{"kind": "session", "id": "muse-test-session"},
		"sequence": seq, "recorded_at": recordedAtMicros,
		"record_type": "event", "durability": "durable", "causation_id": nil,
		"payload_type": "runtime.session", "payload_schema_version": 1,
		"payload": map[string]any{
			"kind": "run", "run_id": "956851f8-a3fd-420d-96d7-4fec84578466",
			"event": map[string]any{
				"kind": "model_completed",
				"usage": map[string]any{
					"input_tokens": input, "output_tokens": output,
					"cached_tokens": cached, "cache_write_tokens": cacheWrite,
					"cache_read_tokens": cacheRead, "reasoning_tokens": reasoning,
				},
				"duration_ms": 2858, "finish_reason": "tool_calls", "model": model,
			},
			"source_run_record_id": runRecordID, "source_run_record_sequence": seq,
		},
	}
}

// writeMuseWorkerSession creates searchBase/YYYY/MM/DD/<sessionID>/session.jsonl
// (day from mtime in local time) with a workspace-bearing head line plus the
// given event maps, and sets the file mtime to mtime so window discovery can
// find it. It returns the session.jsonl path.
func writeMuseWorkerSession(t *testing.T, searchBase string, mtime time.Time, sessionID, workDir string, events []map[string]any) string {
	t.Helper()
	local := mtime.In(time.Local)
	dir := filepath.Join(searchBase, local.Format("2006"), local.Format("01"), local.Format("02"), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	path := filepath.Join(dir, "session.jsonl")
	head := []map[string]any{{
		"record_type": "event", "payload_type": "runtime.session.metadata",
		"payload": map[string]any{"workspace_root": workDir},
	}}
	writeWorkerTestJSONL(t, path, append(head, events...))
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes(%q): %v", mtime, err)
	}
	return path
}

func TestInvocationUsageFamilyMuse(t *testing.T) {
	if got := invocationUsageFamily("muse"); got != "muse" {
		t.Fatalf("invocationUsageFamily(muse) = %q, want muse", got)
	}
	family, supported := InvocationUsageFamily("muse")
	if family != "muse" || !supported {
		t.Fatalf("InvocationUsageFamily(muse) = (%q, %t), want (muse, true)", family, supported)
	}
	if _, ok := invocationUsageSpecs["muse"]; !ok {
		t.Fatal("muse spec missing from invocationUsageSpecs; the resolved family reaches no extractor")
	}
}

func TestMessageRecordsMuseInvocationTokens(t *testing.T) {
	reader := setupInvocationMetricsReader(t)
	handle, store, searchBase, workDir := newFamilyTelemetryHandle(t, ProfileMuseTmuxCLI, "muse", "muse", nil)

	now := time.Now().Truncate(time.Second)
	writeMuseWorkerSession(t, searchBase, now, "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir, []map[string]any{
		museWorkerModelCompleted(22, "rec-1", "run-rec-1", now.Add(-time.Minute).UnixMicro(), "muse-spark-1.3-contributor", 1000, 100, 200, 0, 200, 7),
		museWorkerModelCompleted(23, "rec-2", "run-rec-2", now.UnixMicro(), "muse-spark-1.3-contributor", 2000, 50, 1900, 0, 1900, 3),
	})
	if err := store.SetMetadata(handle.sessionID, "last_woke_at", now.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("SetMetadata(last_woke_at): %v", err)
	}

	if _, err := handle.Message(context.Background(), MessageRequest{Text: "hello"}); err != nil {
		t.Fatalf("Message: %v", err)
	}

	// No persisted cursor: only the newest invocation is recorded.
	out := collectInvocationMetrics(t, reader)
	wantTokens := map[string]int64{
		"gc.agent.tokens.input":      2000 - 1900,
		"gc.agent.tokens.output":     50,
		"gc.agent.tokens.cache_read": 1900,
	}
	for name, want := range wantTokens {
		got, attrSets := invocationInt64Total(out, name)
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
		if len(attrSets) != 1 {
			t.Errorf("%s: %d datapoint attribute sets, want 1", name, len(attrSets))
			continue
		}
		attrs := attrSets[0]
		if got := attrs["model"]; got != "muse-spark-1.3-contributor" {
			t.Errorf("%s: model = %q, want muse-spark-1.3-contributor", name, got)
		}
		if got := attrs["provider"]; got != "muse" {
			t.Errorf("%s: provider = %q, want muse", name, got)
		}
	}
	// muse-spark-1.3-contributor ships in the default pricing registry, so
	// unlike codex the cost estimate must flow: (100*0.10 + 50*0.20 +
	// 1900*0.002)/1e6.
	gotCost, dps := invocationCostTotal(out)
	if dps != 1 {
		t.Fatalf("gc.agent.invocation.cost_usd: %d datapoints, want 1 (muse is priced)", dps)
	}
	wantCost := (100*0.10 + 50*0.20 + 1900*0.002) / 1_000_000
	if diff := gotCost - wantCost; diff < -1e-12 || diff > 1e-12 {
		t.Errorf("gc.agent.invocation.cost_usd = %v, want %v", gotCost, wantCost)
	}
}

func TestFactorySweepSessionModelUsageKeylessMuseDiscoversByWorkdir(t *testing.T) {
	museRoot := t.TempDir()
	workDir := t.TempDir()
	otherDir := t.TempDir()
	sinkPath := filepath.Join(t.TempDir(), "usage.jsonl")

	store := beads.NewMemStore()
	sp := runtime.NewFake()
	factory, err := NewFactory(FactoryConfig{
		Store:       store,
		Provider:    sp,
		SearchPaths: []string{museRoot},
		UsageSink:   usage.NewLocalSink(sinkPath),
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	start := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	slept := start.Add(90 * time.Second)
	// This session's transcript (workspace == workDir): distinctive output=50.
	writeMuseWorkerSession(t, museRoot, slept.Add(-time.Second), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir, []map[string]any{
		museWorkerModelCompleted(23, "rec-1", "run-rec-1", slept.Add(-time.Second).UnixMicro(), "muse-spark-1.3", 1000, 50, 200, 0, 200, 0),
	})
	// A DIFFERENT session's transcript in the same window but a different
	// workspace. The workspace filter must reject it (its output=999 would
	// betray a wrong pick).
	writeMuseWorkerSession(t, museRoot, slept.Add(-time.Second), "01a0bde4-25aa-7f50-8a21-1c2bd19389c8", otherDir, []map[string]any{
		museWorkerModelCompleted(23, "rec-9", "run-rec-9", slept.Add(-time.Second).UnixMicro(), "muse-spark-1.3", 9999, 999, 0, 0, 0, 0),
	})

	meta := map[string]string{
		"provider":         "muse",
		"work_dir":         workDir,
		"awake_started_at": start.Format(time.RFC3339),
		"slept_at":         slept.Format(time.RFC3339),
		"session_name":     "muse-wisp-1",
		"molecule_id":      "run-Z",
		// NB: NO session_key — the whole point of the keyless fallback.
	}
	now := slept.Add(time.Minute)
	emitted, settled, err := factory.SweepSessionModelUsage(context.Background(), "gcg-muse-wisp-1", meta, now)
	if err != nil {
		t.Fatalf("SweepSessionModelUsage: %v", err)
	}
	if !settled {
		t.Fatal("a keyless muse sweep that discovered its transcript by workdir must settle")
	}
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (the one in-window transcript whose workspace matches work_dir)", emitted)
	}

	facts, warnings, err := usage.ReadFacts(sinkPath)
	if err != nil {
		t.Fatalf("ReadFacts: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(facts) != 1 {
		t.Fatalf("want 1 model fact, got %d: %+v", len(facts), facts)
	}
	f := facts[0]
	if f.Kind != usage.KindModel {
		t.Fatalf("kind = %q, want model", f.Kind)
	}
	if f.OutputTokens != 50 {
		t.Fatalf("OutputTokens = %d, want 50 (proves the workspace==work_dir transcript was chosen, not the other)", f.OutputTokens)
	}
	if f.Provider != "muse" {
		t.Fatalf("Provider = %q, want muse", f.Provider)
	}
	if f.Model != "muse-spark-1.3" {
		t.Fatalf("Model = %q, want muse-spark-1.3", f.Model)
	}
	if f.Unpriced {
		t.Fatal("fact must be priced (muse-spark-1.3 ships in the default registry)")
	}
	if f.RunID != "run-Z" || f.StepID != "" {
		t.Fatalf("RunID/StepID = %q/%q, want run-Z/\"\" (run-level attribution)", f.RunID, f.StepID)
	}
}

func TestFactorySweepSessionModelUsageKeylessMuseAmbiguousSettles(t *testing.T) {
	museRoot := t.TempDir()
	workDir := t.TempDir()
	sinkPath := filepath.Join(t.TempDir(), "usage.jsonl")

	store := beads.NewMemStore()
	sp := runtime.NewFake()
	factory, err := NewFactory(FactoryConfig{
		Store:       store,
		Provider:    sp,
		SearchPaths: []string{museRoot},
		UsageSink:   usage.NewLocalSink(sinkPath),
	})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	start := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	slept := start.Add(90 * time.Second)
	writeMuseWorkerSession(t, museRoot, slept.Add(-time.Second), "01a0bde4-25aa-7f50-8a21-1c2bd19389c7", workDir, []map[string]any{
		museWorkerModelCompleted(23, "rec-1", "run-rec-1", slept.Add(-time.Second).UnixMicro(), "muse-spark-1.3", 1000, 50, 0, 0, 0, 0),
	})
	writeMuseWorkerSession(t, museRoot, slept.Add(-time.Second), "01a0bde4-25aa-7f50-8a21-1c2bd19389c8", workDir, []map[string]any{
		museWorkerModelCompleted(23, "rec-2", "run-rec-2", slept.Add(-time.Second).UnixMicro(), "muse-spark-1.3", 2000, 60, 0, 0, 0, 0),
	})

	meta := map[string]string{
		"provider":         "muse",
		"work_dir":         workDir,
		"awake_started_at": start.Format(time.RFC3339),
		"slept_at":         slept.Format(time.RFC3339),
		"session_name":     "muse-wisp-1",
	}
	now := slept.Add(time.Minute)
	emitted, settled, err := factory.SweepSessionModelUsage(context.Background(), "gcg-muse-wisp-2", meta, now)
	if err != nil {
		t.Fatalf("SweepSessionModelUsage: %v", err)
	}
	if emitted != 0 {
		t.Fatalf("emitted = %d, want 0 (ambiguous workdir must record nothing)", emitted)
	}
	if !settled {
		t.Fatal("ambiguous keyless muse miss must settle (retrying cannot disambiguate)")
	}
}
