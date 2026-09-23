package main

import (
	"context"
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/coordclass"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TestBeadReconcileTick_OrphanReleaseCallSite_RetainsLiveReclaimsBareTemplateResidue
// is the production-call-site control for the rebased orphan-release stack
// (precondition 3.0): it drives the REAL tick — beadReconcileTick — and asserts
// both legs hold AT THE CALL SITE, so a lost or mis-merged hunk at
// city_runtime.go's release call turns this red rather than leaving a green
// suite over regressed production code.
//
// Two work beads, one per leg:
//
//   - W1 (sessionStore cure, upstream 512b79c67 / ga-g3pf0): assigned to a
//     session whose bead lives ONLY in the relocated sessions-class store.
//     Its liveness is invisible to the work store; only the call site passing
//     the typed session store keeps it from being released every tick.
//     LEG D reachable-red: repoint the call site's sessionStore argument at
//     the work store and W1 is falsely released — this test MUST fail.
//
//   - W2 (gcy-bzq reclaim leg): assigned to the bare template identity of a
//     multi-slot pool, with a live slot session running. No seat of an
//     expanded-identity pool holds the bare template as a claim identity, so
//     the assignment is unservable residue: the wake arm cannot serve it
//     (no template-key reachability) and the release arm must reclaim it back
//     to the pool queue (assignee cleared, route kept). This leg formerly
//     pinned wake-protection retention (gc-ft31x Leg E); that expectation was
//     the spawn/drain wedge — retaining unservable residue while pool demand
//     spawns a seat per tick no hook can ever claim. The protectedWakeWork
//     mechanics themselves remain pinned by
//     pool_session_name_protected_wake_test.go.
//
// Coverage denominator, stated per the receipt rule: this control covers the
// beadReconcileTick call site (city_runtime.go). The one-shot path's separate
// call site in cmd_start.go is NOT covered by this test.
func TestBeadReconcileTick_OrphanReleaseCallSite_RetainsLiveReclaimsBareTemplateResidue(t *testing.T) {
	workStore := beads.NewMemStore()
	sessionStore := beads.NewMemStore() // relocated [beads.classes.sessions] binding

	inProgress := "in_progress"

	// W1: assignee live ONLY in the relocated sessions store.
	w1, err := workStore.Create(beads.Bead{
		Title:    "w1 routed work, assignee live in relocated sessions store",
		Assignee: "worker-w1-live",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"},
	})
	if err != nil {
		t.Fatalf("create w1: %v", err)
	}
	if err := workStore.Update(w1.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("w1 in_progress: %v", err)
	}
	if _, err := sessionStore.Create(beads.Bead{
		Title:  "live session for w1, resident in the sessions class only",
		Type:   sessionBeadType,
		Status: "open",
		Labels: []string{sessionBeadLabel, "agent:worker"},
		Metadata: map[string]string{
			"session_name": "worker-w1-live",
			"template":     "worker",
			"agent_name":   "worker",
			"state":        "active",
		},
	}); err != nil {
		t.Fatalf("create w1 session bead: %v", err)
	}

	// W2: bare-template assignee on a multi-slot pool, with a live slot session
	// whose own identities match neither assignee. No seat of this pool holds
	// the bare template, so the assignment is unservable residue the tick must
	// reclaim — not wake-protect (gcy-bzq).
	w2, err := workStore.Create(beads.Bead{
		Title:    "w2 bare-template residue on a multi-slot pool",
		Assignee: "worker",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"},
	})
	if err != nil {
		t.Fatalf("create w2: %v", err)
	}
	if err := workStore.Update(w2.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("w2 in_progress: %v", err)
	}

	// Snapshot: one open worker session whose own identities match neither
	// assignee — pre-gcy-bzq it made W2 wake-reachable through the template
	// key; post-fix the template key holds only for singleton pools, so the
	// slot session neither wake-protects nor backs this residue.
	snapshotSession := beads.Bead{
		ID:     "sc-snap-worker",
		Title:  "open worker session in the pre-tick snapshot",
		Type:   sessionBeadType,
		Status: "open",
		Labels: []string{sessionBeadLabel, "agent:worker"},
		Metadata: map[string]string{
			"session_name": "worker-1",
			"template":     "worker",
			"agent_name":   "worker",
			"state":        "active",
		},
	}

	cfg := &config.City{Agents: []config.Agent{{
		Name:              "worker",
		MinActiveSessions: intPtr(0),
		MaxActiveSessions: intPtr(2),
	}}}

	cr := &CityRuntime{
		cityPath:            t.TempDir(),
		cityName:            "callsite-control-city",
		cfg:                 cfg,
		sp:                  runtime.NewFake(),
		standaloneCityStore: workStore,
		storageRoutes: &storageRoutes{stores: map[coordclass.Class]beads.Store{
			coordclass.ClassSessions: sessionStore,
		}},
		sessionDrains: newDrainTracker(),
		rec:           events.Discard,
		stdout:        io.Discard,
		stderr:        io.Discard,
	}

	result := DesiredStateResult{
		State:                 map[string]TemplateParams{},
		AssignedWorkBeads:     []beads.Bead{callsiteControlGet(t, workStore, w1.ID), callsiteControlGet(t, workStore, w2.ID)},
		AssignedWorkStores:    []beads.Store{workStore, workStore},
		AssignedWorkStoreRefs: []string{"", ""},
	}

	cr.beadReconcileTick(context.Background(), result, newSessionBeadSnapshot([]beads.Bead{snapshotSession}), nil, false)

	gotW1 := callsiteControlGet(t, workStore, w1.ID)
	if gotW1.Assignee != "worker-w1-live" || gotW1.Status != inProgress {
		t.Errorf("%s was released at the production call site: assignee=%q status=%q, want assignee=%q status=%q — lost cure: %s",
			w1.ID, gotW1.Assignee, gotW1.Status, "worker-w1-live", inProgress,
			"sessionStore (liveness read must hit the relocated sessions class, not the work store)")
	}
	gotW2 := callsiteControlGet(t, workStore, w2.ID)
	if gotW2.Assignee != "" || gotW2.Status != "open" {
		t.Errorf("%s was NOT reclaimed at the production call site: assignee=%q status=%q, want assignee=%q status=%q — the bare-template residue on an expanded pool must be released back to the pool queue (gcy-bzq)",
			w2.ID, gotW2.Assignee, gotW2.Status, "", "open")
	}
	if gotW2.Metadata[beadmeta.RoutedToMetadataKey] != "worker" {
		t.Errorf("%s lost its route on reclaim: gc.routed_to=%q, want %q", w2.ID, gotW2.Metadata[beadmeta.RoutedToMetadataKey], "worker")
	}
}

func callsiteControlGet(t *testing.T, store beads.Store, id string) beads.Bead {
	t.Helper()
	b, err := store.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return b
}
