package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
)

// seedRoutedWorkAssignedTo creates an in_progress work bead routed to
// template with the given assignee, mirroring the shape
// releaseOrphanedPoolAssignments expects (gc.routed_to metadata, reloaded
// after the status transition so it reads back realistically).
func seedRoutedWorkAssignedTo(t *testing.T, store beads.Store, title, template, assignee string) beads.Bead {
	t.Helper()

	work, err := store.Create(beads.Bead{
		Title:    title,
		Type:     "task",
		Status:   "open",
		Assignee: assignee,
		Metadata: map[string]string{"gc.routed_to": template},
	})
	if err != nil {
		t.Fatalf("create work bead: %v", err)
	}
	if err := store.Update(work.ID, beads.UpdateOpts{Status: stringPtr("in_progress")}); err != nil {
		t.Fatalf("mark work in_progress: %v", err)
	}
	work, err = store.Get(work.ID)
	if err != nil {
		t.Fatalf("reload work bead: %v", err)
	}
	return work
}

// TestReleaseOrphanedPoolAssignments_TemplateAssigneeSkippedWhenSessionLive
// covers the bug on ga-r22k2y: some routing paths write the bare template
// name itself into Assignee rather than a concrete session identity (never a
// real session), so the reconciler must recognize a live ephemeral session
// for that template as backing the claim instead of treating the assignee as
// a dead named session and reopening live routed work out from under it.
//
// The pool here is a canonical singleton (max_active_sessions=1, no namepool):
// the only shape whose seat carries the bare template as its own claim
// identity (GC_ALIAS), and therefore the only shape where a live seat can
// actually back a bare-template claim. Expanded-identity pools (multi-slot,
// namepool, unbounded) claim under instance identities, so the same
// bare-template assignee there is unservable residue the sweep must reclaim
// (gcy-bzq) — see TemplateAssigneeReleasedWhenPoolUsesExpandedIdentities.
func TestReleaseOrphanedPoolAssignments_TemplateAssigneeSkippedWhenSessionLive(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "routed to template name", "worker", "worker")

	openSessions := []session.Info{
		{ID: "sess-live", Template: "worker", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "1"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testSingletonPoolReleaseConfig(),
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	if len(released) != 0 {
		t.Fatalf("released %v, want none — a live ephemeral session for template %q backs this bead's "+
			"bare-template assignee, so it must stay claimed, not be reopened for the pool to reclaim",
			released, "worker")
	}
}

// testSingletonPoolReleaseConfig is testPoolReleaseConfig narrowed to a
// canonical-singleton pool (max_active_sessions=1, no namepool): the only
// pool shape whose seat holds the bare template as its claim identity.
func testSingletonPoolReleaseConfig() *config.City {
	return &config.City{Agents: []config.Agent{{Name: "worker", MinActiveSessions: intPtr(0), MaxActiveSessions: intPtr(1)}}}
}

// TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenPoolUsesExpandedIdentities
// is the gcy-bzq regression: a bare-template assignee on an expanded-identity
// pool (multi-slot here; namepool and unbounded behave the same) is residue no
// live seat backs — every seat claims under its own instance identity, never
// the bare template — so a live same-template seat must NOT shield it from
// reclamation. Retaining it wedges the bead: pool demand keeps spawning seats
// for it (wake-known-identity) while no seat's hook can serve it (claim
// requires an own-identity assignee or unassigned), a spawn/drain loop that
// burns a session per cycle until the residue is cleared.
//
// The release must preserve gc.routed_to: clearing the assignee returns the
// bead to the pool queue it was routed to, which is what lets a later seat
// claim it through the normal unassigned routed tier.
func TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenPoolUsesExpandedIdentities(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "bare template assignee on a multi-slot pool", "worker", "worker")

	openSessions := []session.Info{
		{ID: "sess-live", Template: "worker", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "1"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testPoolReleaseConfig(),
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released %v, want exactly [%s] — no seat of a multi-slot pool holds the bare "+
			"template identity, so a live slot session must not shield a bare-template assignee",
			released, work.ID)
	}
	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("re-read work: %v", err)
	}
	if got.Status != "open" || got.Assignee != "" {
		t.Fatalf("released work not reopened: status=%q assignee=%q", got.Status, got.Assignee)
	}
	if got.Metadata["gc.routed_to"] != "worker" {
		t.Fatalf("released work lost its route: gc.routed_to=%q, want %q", got.Metadata["gc.routed_to"], "worker")
	}
}

// TestReleaseOrphanedPoolAssignments_OpenBareTemplateAssigneeReleasedOnNamepool
// is the exact gcy-bzq incident shape: an OPEN bead (never claimed) assigned
// to the bare template of a namepool, routed to that same pool, with live
// namepool seats running. The seats serve their own namepool identities only,
// so the bare assignee is unclaimable residue: it must be released back to
// the pool queue (assignee cleared, route kept), not retained while seats live.
func TestReleaseOrphanedPoolAssignments_OpenBareTemplateAssigneeReleasedOnNamepool(t *testing.T) {
	store := beads.NewMemStore()
	work, err := store.Create(beads.Bead{
		Title:    "open bare-template residue on a namepool",
		Type:     "task",
		Status:   "open",
		Assignee: "rig/polecat",
		Metadata: map[string]string{"gc.routed_to": "rig/polecat"},
	})
	if err != nil {
		t.Fatalf("create work bead: %v", err)
	}

	cfg := &config.City{
		Rigs: []config.Rig{{Name: "rig", Path: "/nonexistent/rig"}},
		Agents: []config.Agent{{
			Name:              "polecat",
			Dir:               "rig",
			MinActiveSessions: intPtr(0),
			MaxActiveSessions: intPtr(5),
			NamepoolNames:     []string{"furiosa", "slit"},
		}},
	}
	openSessions := []session.Info{
		{ID: "sess-furiosa", Template: "rig/polecat", Alias: "rig/furiosa", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "1"},
		{ID: "sess-slit", Template: "rig/polecat", Alias: "rig/slit", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "2"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		cfg,
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released %v, want exactly [%s] — live namepool seats hold namepool identities, "+
			"never the bare template, so they must not shield its residue", released, work.ID)
	}
	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("re-read work: %v", err)
	}
	if got.Status != "open" || got.Assignee != "" {
		t.Fatalf("released work not reopened: status=%q assignee=%q", got.Status, got.Assignee)
	}
	if got.Metadata["gc.routed_to"] != "rig/polecat" {
		t.Fatalf("released work lost its route: gc.routed_to=%q, want %q", got.Metadata["gc.routed_to"], "rig/polecat")
	}
}

// TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenNoLiveSession
// is the counterpart: the same bare-template-assignee shape, but with no live
// session anywhere for the template. Nothing is serving this route, so the
// pool must still be able to reclaim it — the assignee-is-template shape must
// not become a permanent shield.
func TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenNoLiveSession(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "routed to template name, no session", "worker", "worker")

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testPoolReleaseConfig(),
		"",
		nil,
		[]beads.Bead{work},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released %v, want exactly [%s] — no live session serves template %q, so the bare-template "+
			"assignee must not permanently shield the bead from reclamation", released, work.ID, "worker")
	}
}

// TestReleaseOrphanedPoolAssignments_DeadNamedAssigneeReleasedDespiteLiveTemplateSession
// pins the regression a naive fix gets wrong: a genuinely dead NAMED-session
// assignee that happens to share a template with an unrelated LIVE sibling
// session must still be released. Scoping liveness to "some live session
// exists for this template" instead of "the assignee itself IS this
// template" would let any live sibling shield an unrelated dead assignee's
// claim forever — this was the round-1 defect on ga-r22k2y.
func TestReleaseOrphanedPoolAssignments_DeadNamedAssigneeReleasedDespiteLiveTemplateSession(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "dead named assignee, live sibling session", "worker", "sess-dead-999")

	openSessions := []session.Info{
		// Live ephemeral pool session for the SAME template, under a different
		// identity than the bead's assignee — must not shield "sess-dead-999".
		{ID: "sess-live", Template: "worker", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "1"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testPoolReleaseConfig(),
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released %v, want exactly [%s] — assignee %q is a distinct dead session, not the template "+
			"name itself; a live sibling session for template %q must not shield its dead claim from reclamation",
			released, work.ID, "sess-dead-999", "worker")
	}
}

// TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenOnlyNamedSessionLive
// pins the other half of the gate's name: only an EPHEMERAL pool session backs
// a bare-template claim. A configured named session shares the template but
// serves its own identity, and being long-lived it would shield the bead from
// reclamation indefinitely — exactly the permanent-shield failure mode
// TemplateAssigneeReleasedWhenNoLiveSession guards against from the other
// direction.
func TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenOnlyNamedSessionLive(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "bare template assignee, named session live", "worker", "worker")

	openSessions := []session.Info{
		{ID: "seth", Template: "worker", Closed: false, SessionOrigin: "named", ConfiguredNamedSession: true, ConfiguredNamedIdentity: "seth"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testPoolReleaseConfig(),
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released %v, want exactly [%s] — a configured named session does not serve a bare-template "+
			"pool claim and must not shield it", released, work.ID)
	}
}

// testRigScopedPoolReleaseConfig is testPoolReleaseConfig with the worker agent
// scoped to a single rig, so assignedWorkStoreRefForAgent resolves to "repo"
// instead of the empty city ref. The agent stays rig-scoped (no Scope="city"),
// so it is not cross-store eligible and the store-ref equality branch of
// liveEphemeralSessionForTemplate is the one that decides. A rig-scoped agent is
// addressed by its qualified template name ("repo/worker"), which is what
// findAgentByTemplate resolves and therefore what the routed work must name.
// The pool is a canonical singleton (max_active_sessions=1): the only shape
// where a live seat holds the bare template, so the only shape where the
// store-scope retain arm below is correct (gcy-bzq).
func testRigScopedPoolReleaseConfig() *config.City {
	return &config.City{
		Rigs: []config.Rig{{Name: "repo", Path: "/nonexistent/repo"}},
		Agents: []config.Agent{{
			Name:              "worker",
			Dir:               "repo",
			MinActiveSessions: intPtr(0),
			MaxActiveSessions: intPtr(1),
		}},
	}
}

// TestReleaseOrphanedPoolAssignments_TemplateAssigneeSkippedWhenSessionLiveInSameStoreScope
// exercises the store-ref-aware branch of the liveness gate that the
// nil-storeRefs cases above cannot reach: with index-aligned store refs the
// gate must compare the serving agent's store scope against the work bead's,
// and a rig-scoped agent whose rig IS the work's rig still backs the claim.
func TestReleaseOrphanedPoolAssignments_TemplateAssigneeSkippedWhenSessionLiveInSameStoreScope(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "bare template assignee, live session in the work's rig", "repo/worker", "repo/worker")

	openSessions := []session.Info{
		{ID: "sess-live", Template: "repo/worker", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "1"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testRigScopedPoolReleaseConfig(),
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		[]string{"repo"}, // store-ref aware: the work lives in the agent's own rig
		nil,
		nil,
		nil,
	)

	if len(released) != 0 {
		t.Fatalf("released %v, want none — the live ephemeral session's agent serves store ref %q, the same "+
			"scope the work bead is in, so the bare-template claim stays backed", released, "repo")
	}
}

// TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenLiveSessionServesAnotherStore
// is the negative store-ref case: the live ephemeral session's agent cannot
// reach the work bead's store, so it cannot be the thing serving this claim and
// must not shield it. The agent is rig-scoped (not cross-store eligible), so
// the store-ref equality branch decides and its refs differ.
func TestReleaseOrphanedPoolAssignments_TemplateAssigneeReleasedWhenLiveSessionServesAnotherStore(t *testing.T) {
	store := beads.NewMemStore()
	work := seedRoutedWorkAssignedTo(t, store, "bare template assignee, live session in another rig", "repo/worker", "repo/worker")

	openSessions := []session.Info{
		{ID: "sess-live", Template: "repo/worker", Closed: false, SessionOrigin: "ephemeral", PoolManaged: true, PoolSlot: "1"},
	}

	released := releaseOrphanedPoolAssignments(
		store,
		beads.SessionStore{Store: store},
		testRigScopedPoolReleaseConfig(),
		"",
		openSessions,
		[]beads.Bead{work},
		nil,
		[]string{"other-repo"}, // store-ref aware: the work lives outside the agent's rig
		nil,
		nil,
		nil,
	)

	if len(released) != 1 || released[0].ID != work.ID {
		t.Fatalf("released %v, want exactly [%s] — the live session's agent serves rig %q and cannot reach the "+
			"work bead's store %q, so it must not shield the bare-template claim", released, work.ID, "repo", "other-repo")
	}
}
