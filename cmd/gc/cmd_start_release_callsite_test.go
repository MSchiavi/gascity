package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/coordclass"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TestStartStandalone_OrphanReleaseCallSite_RetainsLiveReclaimsBareTemplateResidue
// is the production-call-site control for the ONE-SHOT release call in
// doStartStandalone (cmd_start.go) — the second of the two sites, and per the
// acceptance review the more important one: it is the path that runs when the
// controller STARTS (a bounce IS a controller start), and it has no follow-up
// patrol tick to repair a wrong release.
//
// Same two-leg discrimination as the daemon-site control
// (city_runtime_release_callsite_test.go):
//
//   - W1 (sessionStore cure): assigned to a running rig-scoped session whose
//     bead lives ONLY in the relocated sessions-class binding. LEG D
//     reachable-red: repoint the call site's sessionStore argument at the
//     work store and W1 is falsely released.
//   - W2 (gcy-bzq reclaim leg): bare-template assignee on a multi-slot pool,
//     with a live slot session running. No seat of an expanded-identity pool
//     holds the bare template, so the wake arm cannot serve it and the
//     one-shot release must reclaim it (assignee cleared, route kept). This
//     leg formerly pinned wake-protection retention (gc-ft31x Leg E); that
//     expectation was the spawn/drain wedge.
func TestStartStandalone_OrphanReleaseCallSite_RetainsLiveReclaimsBareTemplateResidue(t *testing.T) {
	cityPath := t.TempDir()
	clearInheritedBeadsEnv(t)
	t.Chdir(t.TempDir())

	for _, rig := range []string{"rigA", "rigB"} {
		if err := os.MkdirAll(filepath.Join(cityPath, rig), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rig, err)
		}
	}
	cityTOML := `[workspace]
name = "oneshot-callsite-city"
provider = "shell"

[providers.shell]
command = "echo"

[beads]
provider = "file"

[storage.classes]
work = "work"
graph = "infra"
sessions = "infra"
messaging = "infra"
orders = "infra"
nudges = "infra"

[storage.bindings.infra]
provider = "sqlite-beads"
path = ".gc/session-class-store"

[[agent]]
name = "worker"
scope = "city"
max_active_sessions = 2

[[agent]]
name = "rigworker"
dir = "rigA"
max_active_sessions = 2

[[rigs]]
name = "rigA"
path = "rigA"
prefix = "ra"

[[rigs]]
name = "rigB"
path = "rigB"
prefix = "rb"
`
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityTOML), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	if err := ensureScopedFileStoreLayout(cityPath); err != nil {
		t.Fatalf("ensureScopedFileStoreLayout: %v", err)
	}
	for _, root := range []string{cityPath, filepath.Join(cityPath, "rigA"), filepath.Join(cityPath, "rigB")} {
		if err := ensurePersistedScopeLocalFileStore(root); err != nil {
			t.Fatalf("ensurePersistedScopeLocalFileStore(%q): %v", root, err)
		}
	}

	rigBStore, err := openScopeLocalFileStore(filepath.Join(cityPath, "rigB"))
	if err != nil {
		t.Fatalf("open rigB store: %v", err)
	}
	inProgress := "in_progress"
	seedWorkIn := func(store beads.Store, title, assignee, routedTo string) beads.Bead {
		t.Helper()
		wb, err := store.Create(beads.Bead{
			Title:    title,
			Assignee: assignee,
			Metadata: map[string]string{beadmeta.RoutedToMetadataKey: routedTo},
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		if err := store.Update(wb.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
			t.Fatalf("%s in_progress: %v", title, err)
		}
		return wb
	}
	// W1 lives in rigB's store while its assignee is rigA's session: the
	// cross-rig claim keeps snapshot-ownership and wake-reachability ref
	// mismatched, so retention falls to the sessionStore liveness probe —
	// the argument Leg D mutates.
	w1 := seedWorkIn(rigBStore, "w1: assignee live only in the relocated sessions binding", "rigworker-w1-live", "rigA/rigworker")
	w2 := seedWorkIn(rigBStore, "w2: bare-template residue on a multi-slot pool", "worker", "worker")
	// Keeps the worker-1 session bead owning open work so no sweep closes it
	// before the release arm needs it for W2's wake reachability.
	seedWorkIn(rigBStore, "w3: anchors the worker-1 session through sweeps", "worker-1", "worker")
	// Canary: a genuinely orphaned bead. If the release arm runs at all, this
	// one MUST be released; it staying assigned means the fixture never
	// reached the release gates and every retention above is vacuous.
	w0 := seedWorkIn(rigBStore, "w0 canary: dead assignee, releasable", "ghost-nobody", "worker")

	// Sessions-class beads live ONLY in the relocated binding.
	sessStore, relocated := cliStorageRoutes(cityPath).storeFor(coordclass.ClassSessions)
	if !relocated || sessStore == nil {
		t.Fatalf("sessions class did not relocate to the sqlite binding; storeFor(sessions) relocated=%v", relocated)
	}
	seedSession := func(sessionName, template, agentLabel string) {
		t.Helper()
		if _, err := sessStore.Create(beads.Bead{
			Title:  "session " + sessionName,
			Type:   sessionBeadType,
			Status: "open",
			Labels: []string{sessionBeadLabel, "agent:" + agentLabel},
			Metadata: map[string]string{
				"session_name": sessionName,
				"template":     template,
				"agent_name":   agentLabel,
				"state":        "active",
			},
		}); err != nil {
			t.Fatalf("create session bead %s: %v", sessionName, err)
		}
	}
	seedSession("rigworker-w1-live", "rigA/rigworker", "rigworker")
	seedSession("worker-1", "worker", "worker")

	// The provider reports both sessions RUNNING so reconciliation does not
	// kill their beads before the release arm reads them.
	fake := runtime.NewFake()
	for _, name := range []string{"rigworker-w1-live", "worker-1"} {
		if err := fake.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatalf("fake start %s: %v", name, err)
		}
	}
	oldBuild := buildSessionProviderByName
	t.Cleanup(func() { buildSessionProviderByName = oldBuild })
	buildSessionProviderByName = func(_ *config.City, _ string, _ config.SessionConfig, _, _ string) (runtime.Provider, error) {
		return fake, nil
	}

	var stdout, stderr bytes.Buffer
	if code := doStartStandalone([]string{cityPath}, false, &stdout, &stderr); code != 0 {
		t.Fatalf("doStartStandalone exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Reachability canary: the release arm must have actually run and released w0.
	if got, err := rigBStore.Get(w0.ID); err != nil {
		t.Fatalf("get canary: %v", err)
	} else if got.Assignee == "ghost-nobody" {
		t.Fatalf("canary w0 still assigned — the release arm never gated this fixture; retention assertions are vacuous\nstderr:\n%s", stderr.String())
	}

	// Bead IDs collide across independent stores (both minted "gc-1"), so each
	// row carries its own store — the same store-scoped-key lesson
	// readyAssignedFlagsForBeads documents.
	gotW1, err := rigBStore.Get(w1.ID)
	if err != nil {
		t.Fatalf("get %s: %v", w1.ID, err)
	}
	if gotW1.Assignee != "rigworker-w1-live" || gotW1.Status != inProgress {
		t.Errorf("%s was released at the one-shot call site: assignee=%q status=%q, want assignee=%q status=%q — lost cure: %s\nstderr:\n%s",
			w1.ID, gotW1.Assignee, gotW1.Status, "rigworker-w1-live", inProgress,
			"sessionStore (one-shot liveness read must hit the relocated sessions class, not the work store)", stderr.String())
	}
	gotW2, err := rigBStore.Get(w2.ID)
	if err != nil {
		t.Fatalf("get %s: %v", w2.ID, err)
	}
	if gotW2.Assignee != "" || gotW2.Status != "open" {
		t.Errorf("%s was NOT reclaimed at the one-shot call site: assignee=%q status=%q, want assignee=%q status=%q — the bare-template residue on an expanded pool must be released back to the pool queue (gcy-bzq)\nstderr:\n%s",
			w2.ID, gotW2.Assignee, gotW2.Status, "", "open", stderr.String())
	}
	if gotW2.Metadata[beadmeta.RoutedToMetadataKey] != "worker" {
		t.Errorf("%s lost its route on reclaim: gc.routed_to=%q, want %q", w2.ID, gotW2.Metadata[beadmeta.RoutedToMetadataKey], "worker")
	}
}
