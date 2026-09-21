package sling

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TestMergeStrategyForBead pins the sling-time merge strategy resolution:
// an explicit strategy (--merge / API merge) always wins; otherwise the
// bead's rig default_merge_strategy applies. Unknown prefixes, HQ beads,
// and rigs without a default resolve to "" — no metadata write — and the
// refinery treats unset as direct (gcy-br7).
func TestMergeStrategyForBead(t *testing.T) {
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test"},
		Rigs: []config.Rig{
			{Name: "wow_logs", Path: "/wl", Prefix: "wl", DefaultMergeStrategy: "mr"},
			{Name: "gascity", Path: "/gcy", Prefix: "gcy"},
		},
	}
	testCases := []struct {
		name     string
		cfg      *config.City
		beadID   string
		explicit string
		want     string
	}{
		{name: "explicit wins over rig default", cfg: cfg, beadID: "wl-1", explicit: "direct", want: "direct"},
		{name: "rig default applies when explicit empty", cfg: cfg, beadID: "wl-1", explicit: "", want: "mr"},
		{name: "rig without default resolves empty", cfg: cfg, beadID: "gcy-9", explicit: "", want: ""},
		{name: "unknown prefix resolves empty", cfg: cfg, beadID: "zz-3", explicit: "", want: ""},
		{name: "empty bead resolves empty", cfg: cfg, beadID: "", explicit: "", want: ""},
		{name: "nil cfg keeps explicit", cfg: nil, beadID: "wl-1", explicit: "mr", want: "mr"},
		{name: "nil cfg without explicit resolves empty", cfg: nil, beadID: "wl-1", explicit: "", want: ""},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MergeStrategyForBead(tc.cfg, tc.beadID, tc.explicit); got != tc.want {
				t.Errorf("MergeStrategyForBead(%q, %q) = %q, want %q", tc.beadID, tc.explicit, got, tc.want)
			}
		})
	}
}

// TestRouteBeadAppliesRigDefaultMergeStrategy is the gcy-br7 end-to-end
// proof: a bead slung without --merge on a PR-required rig (wow_logs main
// rejects direct push) lands with merge_strategy=mr instead of reaching
// the refinery as direct.
func TestRouteBeadAppliesRigDefaultMergeStrategy(t *testing.T) {
	deps := testDeps(&config.City{
		Workspace: config.Workspace{Name: "test"},
		Rigs:      []config.Rig{{Name: "wow_logs", Path: "/wl", Prefix: "wl", DefaultMergeStrategy: "mr"}},
	}, runtime.NewFake(), newFakeRunner().run)
	deps.Store = seededStore("wl-1")
	s, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}

	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	if _, err := s.RouteBead(context.Background(), "wl-1", a, RouteOpts{}); err != nil {
		t.Fatalf("RouteBead: %v", err)
	}

	b, err := deps.Store.Get("wl-1")
	if err != nil {
		t.Fatalf("Store.Get(wl-1): %v", err)
	}
	if got := b.Metadata[beadmeta.MergeStrategyMetadataKey]; got != "mr" {
		t.Errorf("merge_strategy = %q, want %q (rig default)", got, "mr")
	}
}

// TestRouteBeadExplicitMergeOverridesRigDefault pins that --merge still
// wins over the rig default: operators can force a direct landing on an
// mr-default rig without touching config.
func TestRouteBeadExplicitMergeOverridesRigDefault(t *testing.T) {
	deps := testDeps(&config.City{
		Workspace: config.Workspace{Name: "test"},
		Rigs:      []config.Rig{{Name: "wow_logs", Path: "/wl", Prefix: "wl", DefaultMergeStrategy: "mr"}},
	}, runtime.NewFake(), newFakeRunner().run)
	deps.Store = seededStore("wl-1")
	s, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}

	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	if _, err := s.RouteBead(context.Background(), "wl-1", a, RouteOpts{Merge: "direct"}); err != nil {
		t.Fatalf("RouteBead: %v", err)
	}

	b, err := deps.Store.Get("wl-1")
	if err != nil {
		t.Fatalf("Store.Get(wl-1): %v", err)
	}
	if got := b.Metadata[beadmeta.MergeStrategyMetadataKey]; got != "direct" {
		t.Errorf("merge_strategy = %q, want %q (explicit wins)", got, "direct")
	}
}

// TestRouteBeadWithoutMergeDefaultWritesNoMetadata pins the legacy shape:
// rigs without default_merge_strategy leave merge_strategy unset, which
// the refinery reads as direct.
func TestRouteBeadWithoutMergeDefaultWritesNoMetadata(t *testing.T) {
	deps := testDeps(&config.City{
		Workspace: config.Workspace{Name: "test"},
		Rigs:      []config.Rig{{Name: "gascity", Path: "/gcy", Prefix: "gcy"}},
	}, runtime.NewFake(), newFakeRunner().run)
	deps.Store = seededStore("gcy-9")
	s, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}

	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	if _, err := s.RouteBead(context.Background(), "gcy-9", a, RouteOpts{}); err != nil {
		t.Fatalf("RouteBead: %v", err)
	}

	b, err := deps.Store.Get("gcy-9")
	if err != nil {
		t.Fatalf("Store.Get(gcy-9): %v", err)
	}
	if got, ok := b.Metadata[beadmeta.MergeStrategyMetadataKey]; ok {
		t.Errorf("merge_strategy = %q, want unset (no rig default)", got)
	}
}
