package workrecord

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// TestValidateBranchClose locks the branch-handoff close discipline (gcy-49m):
// a gated bead that records branch work must close with merge proof — the
// merge commit reachable on its target, a pull-request handoff pointer, or an
// explicit non-shipped outcome with the branch pointer preserved. Absence of
// proof is judged without consulting the reachability oracle (wantCalls == 0);
// only a presented merged_sha spends a reachability probe.
func TestValidateBranchClose(t *testing.T) {
	tests := []struct {
		name        string
		stored      map[string]string
		prospective map[string]string
		reachable   bool // oracle answer when consulted
		wantViol    string
		wantCalls   int
	}{
		{
			name:        "bead with no branch anywhere is exempt",
			stored:      map[string]string{},
			prospective: map[string]string{},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "whitespace-only branch is absent",
			stored:      map[string]string{branchMetadataKey: "  "},
			prospective: map[string]string{branchMetadataKey: "  "},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "branch with no proof and no outcome is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt", "target": "main"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", "target": "main"},
			wantViol:    "no merge proof",
			wantCalls:   0,
		},
		{
			name:        "merged sha reachable on target passes",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", mergedSHAMetadataKey: "abc123", mergedTargetMetadataKey: "main"},
			reachable:   true,
			wantViol:    "",
			wantCalls:   1,
		},
		{
			name:        "merged sha unreachable on target is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", mergedSHAMetadataKey: "abc123", mergedTargetMetadataKey: "main"},
			reachable:   false,
			wantViol:    "not reachable",
			wantCalls:   1,
		},
		{
			name:        "sha without target is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", mergedSHAMetadataKey: "abc123"},
			wantViol:    "without merged_target",
			wantCalls:   0,
		},
		{
			name:        "target without sha is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", mergedTargetMetadataKey: "main"},
			wantViol:    "without merged_sha",
			wantCalls:   0,
		},
		{
			name:        "pull-request handoff with preserved branch passes",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", pullRequestURLMetadataKey: "https://github.com/o/r/pull/1", mergedTargetMetadataKey: "main"},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "false merge evidence is refused even with pr_url",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", pullRequestURLMetadataKey: "https://github.com/o/r/pull/1", mergedSHAMetadataKey: "abc123", mergedTargetMetadataKey: "main"},
			reachable:   false,
			wantViol:    "not reachable",
			wantCalls:   1,
		},
		{
			name:        "pull-request handoff that drops the branch is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{pullRequestURLMetadataKey: "https://github.com/o/r/pull/1"},
			wantViol:    "must preserve the branch pointer",
			wantCalls:   0,
		},
		{
			name:        "abandoned with preserved branch passes",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeAbandoned},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "abandoned that drops the branch is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeAbandoned},
			wantViol:    "must preserve the branch pointer",
			wantCalls:   0,
		},
		{
			name:        "no-op with preserved branch passes",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeNoOp},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "blocked with preserved branch passes",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeBlocked},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "shipped without merge proof is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeShipped},
			wantViol:    "requires merge proof",
			wantCalls:   0,
		},
		{
			name:        "shipped with merge proof passes",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeShipped, mergedSHAMetadataKey: "abc123", mergedTargetMetadataKey: "main"},
			reachable:   true,
			wantViol:    "",
			wantCalls:   1,
		},
		{
			name:        "invalid outcome without proof is refused",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: "done"},
			wantViol:    "invalid gc.work_outcome",
			wantCalls:   0,
		},
		{
			name:        "merge proof overrides a mistyped outcome label",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: "done", mergedSHAMetadataKey: "abc123", mergedTargetMetadataKey: "main"},
			reachable:   true,
			wantViol:    "",
			wantCalls:   1,
		},
		{
			name:        "prospective-only branch with abandoned passes",
			stored:      map[string]string{},
			prospective: map[string]string{branchMetadataKey: "polecat/gcy-drt", beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeAbandoned},
			wantViol:    "",
			wantCalls:   0,
		},
		{
			name:        "merged close may drop the stale branch pointer",
			stored:      map[string]string{branchMetadataKey: "polecat/gcy-drt"},
			prospective: map[string]string{mergedSHAMetadataKey: "abc123", mergedTargetMetadataKey: "main"},
			reachable:   true,
			wantViol:    "",
			wantCalls:   1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			oracle := func(_, _ string) bool {
				calls++
				return tc.reachable
			}
			stored := beads.Bead{ID: "gcy-drt", Type: "task", Metadata: tc.stored}
			prospective := beads.Bead{ID: "gcy-drt", Type: "task", Metadata: tc.prospective}
			got := ValidateBranchClose(stored, prospective, oracle)
			if calls != tc.wantCalls {
				t.Fatalf("reachability oracle called %d times, want %d", calls, tc.wantCalls)
			}
			if tc.wantViol == "" {
				if len(got) != 0 {
					t.Fatalf("expected no violations, got %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("expected a violation containing %q, got none", tc.wantViol)
			}
			joined := strings.Join(got, " | ")
			if !strings.Contains(joined, tc.wantViol) {
				t.Fatalf("violation %q does not contain %q", joined, tc.wantViol)
			}
		})
	}
}
