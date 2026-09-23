package sessionlog

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"time"
)

// museRunUsage mirrors the token-usage object the muse CLI embeds in
// runtime.session run/model_completed events. input_tokens includes cached
// tokens (OpenAI-style gauge: the running session total grows while the
// cached subset dominates), so InputTokens is derived by subtraction, exactly
// like the codex extractor's last-input-minus-cached mapping.
type museRunUsage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
}

// museRunEvent is the subset of a runtime.session run event needed for usage
// extraction. Only model_completed carries per-invocation usage;
// goal_usage_attribution is goal accounting over the same turns and must not
// be extracted (it would double-count every invocation).
type museRunEvent struct {
	Kind         string       `json:"kind"`
	Usage        museRunUsage `json:"usage"`
	Model        string       `json:"model"`
	FinishReason string       `json:"finish_reason"`
	DurationMs   int64        `json:"duration_ms"`
}

// museSessionRecord is the subset of a muse session.jsonl top-level record
// needed for usage extraction.
type museSessionRecord struct {
	ID          string `json:"id"`
	RecordedAt  int64  `json:"recorded_at"`
	RecordType  string `json:"record_type"`
	PayloadType string `json:"payload_type"`
	Payload     struct {
		Kind              string       `json:"kind"`
		Event             museRunEvent `json:"event"`
		SourceRunRecordID string       `json:"source_run_record_id"`
	} `json:"payload"`
}

// ExtractMuseTailUsage reads the tail of a muse session transcript
// (session.jsonl) and returns one usage-bearing TailUsage per completed model
// invocation, in file order. Mapping:
//
//   - InputTokens = usage.input_tokens - cached (clamped at zero)
//   - CacheReadTokens = usage.cache_read_tokens, falling back to
//     usage.cached_tokens (both are written; goal records carry only the
//     latter and are skipped anyway)
//   - OutputTokens = usage.output_tokens
//   - ReasoningTokens = usage.reasoning_tokens
//   - CacheCreationTokens = usage.cache_write_tokens
//   - Model = event.model (e.g. "muse-spark-1.3-contributor")
//
// MessageID and EntryUUID are the run record id (source_run_record_id,
// falling back to the top-level record id): one model_completed event is one
// invocation, so no multi-block collapse is needed and the identity is stable
// across tail re-reads for the telemetry cursor. Timestamp is recorded_at,
// which the CLI writes in microseconds since epoch. Entries with all-zero
// usage and malformed lines are tolerated silently (mirroring
// ExtractTailUsage). The scan window is the last tailChunkSize bytes, so
// usage that scrolled past the window is not returned.
func ExtractMuseTailUsage(path string) ([]TailUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	data, _, err := readTail(f)
	if err != nil {
		return nil, err
	}
	lines, err := splitLines(data)
	if err != nil {
		return nil, err
	}
	return parseMuseTailUsage(lines), nil
}

// ExtractMuseTailUsageSince grows a bounded transcript window until it finds
// the cursor, reaches the start of the file, or reaches the 16 MiB cap.
// Callers still filter entries at or before cursorID and deduplicate facts.
func ExtractMuseTailUsageSince(path, cursorID string) ([]TailUsage, error) {
	return extractMuseTailUsageSince(path, cursorID, maxUsageScanBytes)
}

func extractMuseTailUsageSince(path, cursorID string, maxScanBytes int64) ([]TailUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	for window := min(int64(tailChunkSize), maxScanBytes); ; window = min(window*2, maxScanBytes) {
		data, _, truncated, err := readTailWindowAt(f, size, window)
		if err != nil {
			return nil, err
		}
		lines, err := splitLines(data)
		if err != nil {
			return nil, err
		}
		usages := parseMuseTailUsage(lines)
		if !truncated || (cursorID != "" && containsCursor(usages, cursorID) && !museCursorMayBeReplay(usages, cursorID)) {
			return usages, nil
		}
		if window == maxScanBytes {
			log.Printf("sessionlog: muse usage scan cap reached path=%q window_bytes=%d cursor=%q; older invocations are not returned", path, window, cursorID)
			return usages, nil
		}
	}
}

func museCursorMayBeReplay(usages []TailUsage, cursorID string) bool {
	for i, usage := range usages {
		if usage.MessageID != cursorID && usage.EntryUUID != cursorID {
			continue
		}
		for _, earlier := range usages[:i] {
			if earlier.Timestamp.After(usage.Timestamp) {
				return true
			}
		}
		return false
	}
	return false
}

// ExtractMuseTailUsageFromSearchPaths reads muse tail usage only after
// verifying path resolves under one of the merged muse session roots (the
// defaults plus searchPaths).
func ExtractMuseTailUsageFromSearchPaths(searchPaths []string, path string) ([]TailUsage, error) {
	safePath, err := validateSearchPathFile(mergeMuseSearchPaths(searchPaths), path)
	if err != nil {
		return nil, err
	}
	return ExtractMuseTailUsage(safePath)
}

// ExtractMuseTailUsageSinceFromSearchPaths validates the transcript against
// Muse's merged session roots before scanning from the usage cursor.
func ExtractMuseTailUsageSinceFromSearchPaths(searchPaths []string, path, cursorID string) ([]TailUsage, error) {
	safePath, err := validateSearchPathFile(mergeMuseSearchPaths(searchPaths), path)
	if err != nil {
		return nil, err
	}
	return ExtractMuseTailUsageSince(safePath, cursorID)
}

func parseMuseTailUsage(lines [][]byte) []TailUsage {
	var out []TailUsage
	firstIndex := make(map[string]int)
	for _, line := range lines {
		var rec museSessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.PayloadType != "runtime.session" || rec.Payload.Kind != "run" {
			continue
		}
		ev := rec.Payload.Event
		if ev.Kind != "model_completed" {
			continue
		}
		u := ev.Usage
		if u.InputTokens <= 0 && u.OutputTokens <= 0 && u.CachedTokens <= 0 &&
			u.CacheReadTokens <= 0 && u.CacheWriteTokens <= 0 {
			continue
		}
		cached := u.CacheReadTokens
		if cached <= 0 {
			cached = u.CachedTokens
		}
		input := u.InputTokens - cached
		if input < 0 {
			input = 0
		}
		identity := rec.Payload.SourceRunRecordID
		if identity == "" {
			identity = rec.ID
		}
		if identity == "" {
			continue
		}
		usage := TailUsage{
			EntryUUID:           identity,
			MessageID:           identity,
			Model:               ev.Model,
			InputTokens:         input,
			OutputTokens:        u.OutputTokens,
			ReasoningTokens:     u.ReasoningTokens,
			CacheReadTokens:     cached,
			CacheCreationTokens: u.CacheWriteTokens,
			Timestamp:           museRecordTime(rec.RecordedAt),
		}
		if i, ok := firstIndex[identity]; ok {
			// A replay may carry corrected usage, but moving a prior cursor
			// past newer invocations would make those invocations disappear.
			out[i] = usage
			continue
		}
		firstIndex[identity] = len(out)
		out = append(out, usage)
	}
	return out
}

// museRecordTime converts a muse recorded_at timestamp to time.Time. The CLI
// writes microseconds since epoch; the magnitude branches keep the conversion
// correct if the unit ever changes, instead of silently producing dates in
// 1970 or year 50000.
func museRecordTime(recordedAt int64) time.Time {
	if recordedAt <= 0 {
		return time.Time{}
	}
	switch {
	case recordedAt > 1e17:
		return time.Unix(0, recordedAt).UTC()
	case recordedAt > 1e14:
		return time.Unix(recordedAt/1e6, (recordedAt%1e6)*1e3).UTC()
	case recordedAt > 1e11:
		return time.Unix(recordedAt/1e3, (recordedAt%1e3)*1e6).UTC()
	default:
		return time.Unix(recordedAt, 0).UTC()
	}
}
