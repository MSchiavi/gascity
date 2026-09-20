package workrecord

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// Bare metadata keys of the polecat/refinery branch-handoff convention: the
// work bead's branch pointer, the refinery's merge record, and the
// pull-request handoff pointer. These are pack-minted vocabulary, not
// engine-minted gc.* keys, so they live with this consumer rather than in
// beadmeta (whose scope is the gc. namespace); this gate is their first Go
// reader. The close discipline below gives them teeth: a bead that records
// branch work must close with merge proof.
const (
	branchMetadataKey         = "branch"
	mergedSHAMetadataKey      = "merged_sha"
	mergedTargetMetadataKey   = "merged_target"
	pullRequestURLMetadataKey = "pr_url"
)

// branchCarrying reports whether the close carries branch work: either the
// stored or the prospective bead records a branch pointer. Reading both keeps
// an atomic close from escaping the discipline by unsetting branch on its way
// out — the stored pointer is what would be lost.
func branchCarrying(stored, prospective beads.Bead) bool {
	return strings.TrimSpace(stored.Metadata[branchMetadataKey]) != "" ||
		strings.TrimSpace(prospective.Metadata[branchMetadataKey]) != ""
}

// ValidateBranchClose checks a branch-carrying bead against the close
// discipline (gcy-49m) and returns a human-readable message for each violation
// (empty slice ⇒ the close satisfies the discipline, or the bead carries no
// branch work). Unlike ValidateOnClose, these violations ALWAYS block: the
// population is exactly the beads whose work lives on a branch, and closing
// one without proof is how work is silently lost (a closed bead with its
// branch still awaiting merge, or with the branch gone entirely).
//
// A close satisfies the discipline with one of three proofs, read off the
// prospective bead (the record the close is about to write):
//
//  1. Direct merge: merged_sha reachable on merged_target (verified through
//     commitReachable, injected so the rule is unit-testable without a repo).
//  2. Pull-request handoff: pr_url recorded with the branch pointer preserved.
//     The mr-strategy refinery closes at PR creation, before anything lands;
//     the open PR is the durable pointer and the branch must survive for it.
//     The URL itself is a forensic pointer, not a verified artifact.
//  3. Explicit non-shipped outcome (no-op|blocked|abandoned) with the branch
//     pointer preserved. The typed outcome is the machine-checkable form of
//     the explicit reason; judging free-text close reasons would put judgment
//     in Go.
//
// Proof overrides labels: a verified merge record satisfies the close whatever
// the outcome stamp says, so the refinery's merge flow (which stamps no
// outcome) is never blocked. Conversely, outcome=shipped alone never
// satisfies: a commit on the feature branch is implementation-complete, not
// merged. A half-stamped merge record (sha without target or vice versa)
// fails closed.
//
// The caller is responsible for scoping (Gated on the stored row); this
// function deliberately does not re-check it, so a close cannot escape by
// stamping gc.kind or retyping on its way out.
func ValidateBranchClose(stored, prospective beads.Bead, commitReachable func(commit, branch string) bool) []string {
	if !branchCarrying(stored, prospective) {
		return nil
	}
	branch := strings.TrimSpace(prospective.Metadata[branchMetadataKey])
	display := branch
	if display == "" {
		display = strings.TrimSpace(stored.Metadata[branchMetadataKey])
	}
	sha := strings.TrimSpace(prospective.Metadata[mergedSHAMetadataKey])
	target := strings.TrimSpace(prospective.Metadata[mergedTargetMetadataKey])
	prURL := strings.TrimSpace(prospective.Metadata[pullRequestURLMetadataKey])

	if sha != "" && target != "" {
		if commitReachable(sha, target) {
			return nil
		}
		return []string{fmt.Sprintf("%s %s is not reachable on %s %s (branch %q is not merged yet)", mergedSHAMetadataKey, sha, mergedTargetMetadataKey, target, display)}
	}
	// The pull-request record excuses missing merge evidence, not false
	// evidence: a presented sha/target pair above is always verified, but the
	// mr handoff legitimately stamps merged_target with no merged_sha, so the
	// half-stamp checks below only fire when no pr_url is recorded.
	if prURL != "" {
		if branch == "" {
			return []string{fmt.Sprintf("%s is recorded but branch %q was removed by this close; the pull-request handoff must preserve the branch pointer", pullRequestURLMetadataKey, display)}
		}
		return nil
	}
	if sha != "" {
		return []string{fmt.Sprintf("branch %q records %s %s without %s (want both: the merge commit and the target it landed on)", display, mergedSHAMetadataKey, sha, mergedTargetMetadataKey)}
	}
	if target != "" {
		return []string{fmt.Sprintf("branch %q records %s %q without %s (want both: the merge commit and the target it landed on)", display, mergedTargetMetadataKey, target, mergedSHAMetadataKey)}
	}
	outcome := strings.TrimSpace(prospective.Metadata[beadmeta.WorkOutcomeMetadataKey])
	switch {
	case outcome == "":
		return []string{fmt.Sprintf("branch %q closes with no merge proof (want %s reachable on %s, or %s for a pull-request handoff) and no explicit %s (want one of no-op|blocked|abandoned)", display, mergedSHAMetadataKey, mergedTargetMetadataKey, pullRequestURLMetadataKey, beadmeta.WorkOutcomeMetadataKey)}
	case !ValidOutcome(outcome):
		return []string{fmt.Sprintf("invalid %s=%q on branch %q (want one of no-op|blocked|abandoned, or merge proof via %s/%s)", beadmeta.WorkOutcomeMetadataKey, outcome, display, mergedSHAMetadataKey, pullRequestURLMetadataKey)}
	case outcome == beadmeta.WorkOutcomeShipped:
		return []string{fmt.Sprintf("%s=shipped on branch %q requires merge proof (want %s reachable on %s, or %s for a pull-request handoff); a commit on the branch alone is implementation-complete, not merged", beadmeta.WorkOutcomeMetadataKey, display, mergedSHAMetadataKey, mergedTargetMetadataKey, pullRequestURLMetadataKey)}
	case branch == "":
		return []string{fmt.Sprintf("branch %q was removed by this close; %s=%s must preserve the branch pointer", display, beadmeta.WorkOutcomeMetadataKey, outcome)}
	default:
		return nil
	}
}
